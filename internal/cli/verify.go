package cli

// @module-feature cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/blocks"
	"github.com/salahmyn/lattice/pkg/lattice/entrypoints/laravel"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// readLaravelRoutes scans a Laravel source's route files, from its clone at
// the ref or from its directory, and returns the routes with the source's
// stamp. The string is the error code.
func readLaravelRoutes(ws *workspace.Workspace, s workspace.Source) (blocks.Verification, string, error) {
	v := blocks.Verification{Source: s.ID, Target: s.Verifies,
		Stamp: schema.SourceStamp{ID: s.ID, Repo: s.Repo, Ref: s.Ref, System: s.System}}
	if s.Catalogue != "" {
		return v, "SOURCE_READ", fmt.Errorf("source %s: use either catalogue or laravel_routes, not both", s.ID)
	}
	if s.Verifies == "" {
		return v, "SOURCE_READ", fmt.Errorf("source %s: laravel_routes needs verifies: <catalogue source>", s.ID)
	}
	n := 0
	for _, rf := range s.LaravelRoutes {
		var files []blocks.File
		var err error
		switch {
		case s.Clone != "":
			clone := ws.ResolvePath(s.Clone)
			if _, err := os.Stat(clone); err != nil {
				return v, "SOURCE_MISSING", fmt.Errorf("source %s: clone %s not found; nothing written", s.ID, clone)
			}
			files, v.Stamp.Commit, v.Stamp.CommitDate, err = blocks.ReadGit(clone, s.Ref, rf.Files)
		case s.Path != "":
			files, err = blocks.ReadDir(ws.ResolvePath(s.Path), rf.Files)
		default:
			err = fmt.Errorf("needs clone or path")
		}
		if err != nil {
			return v, "SOURCE_READ", fmt.Errorf("source %s: %v", s.ID, err)
		}
		n += len(files)
		for _, f := range files {
			for _, r := range laravel.ScanRoutes(string(f.Data), rf.Prefix) {
				v.Routes = append(v.Routes, blocks.CodeRoute{Method: r.Method, Path: r.Path, File: f.Path, Line: r.Line})
			}
		}
	}
	if n == 0 {
		return v, "SOURCE_EMPTY", fmt.Errorf("source %s: no route files match laravel_routes; nothing written", s.ID)
	}
	if s.Path != "" && s.Stamp != "" {
		data, err := os.ReadFile(ws.ResolvePath(s.Stamp))
		if err != nil {
			return v, "SOURCE_READ", fmt.Errorf("source %s: %v", s.ID, err)
		}
		snap, err := blocks.ParseSnapshot(data)
		if err != nil {
			return v, "STAMP_INVALID", fmt.Errorf("source %s: %v", s.ID, err)
		}
		if snap.Repo != "" {
			v.Stamp.Repo = snap.Repo
		}
		v.Stamp.Branch, v.Stamp.Commit, v.Stamp.CommitDate, v.Stamp.State = snap.Branch, snap.Commit, snap.CommitDate, snap.State
	}
	return v, "", nil
}

// wiredCount says how many of the verified source's route blocks are wired.
func wiredCount(ws *workspace.Workspace, v blocks.Verification, bs []schema.Block) string {
	prefix := ""
	for _, s := range ws.Sources {
		if s.ID == v.Target {
			prefix = s.Prefix + ":"
		}
	}
	n, wired := 0, 0
	for _, b := range bs {
		if b.Kind == "route" && b.Attrs["http"] != "" && prefix != ":" && strings.HasPrefix(b.ID, prefix) {
			n++
			if strings.HasPrefix(b.Attrs["wired_by"], v.Source+":") {
				wired++
			}
		}
	}
	return fmt.Sprintf("%s: %d of %d %s routes wired (%d code routes)", v.Source, wired, n, v.Target, len(v.Routes))
}
