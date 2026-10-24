package cli

// @module-feature cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// fmtFile is one workspace file and what `lattice fmt` did or would do.
type fmtFile struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	Skipped string `json:"skipped,omitempty"`
}

// newFmtCommand rewrites feature manifests, BRDs and entry points in the
// canonical form Lattice's own writers use, so that normalising a
// hand-written file is its own change and not noise in the next
// `brd approve` or `brd link` diff.
func newFmtCommand(io *IO) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "fmt [path...]",
		Short: "Rewrite features, BRDs and entry points in canonical field order",
		Long: `Rewrites feature manifests, BRDs and entry points in canonical form: the
field order of their schema and 2-space indent, as Lattice's own writers
produce. With no paths it formats every such file in the workspace.

A file that cannot be rewritten without losing something (a YAML comment,
an anchor or alias, or a key its schema does not know) is skipped and
reported; it is never rewritten.

--check writes nothing and exits non-zero when a file would change, for CI.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			paths, err := fmtTargets(ws, args)
			if err != nil {
				return io.fail("FMT_FAILED", err.Error(), nil)
			}
			var files []fmtFile
			changed := 0
			for _, p := range paths {
				f, err := fmtOne(ws, p, check)
				if err != nil {
					return io.fail("FMT_FAILED", err.Error(), nil)
				}
				if f.Changed {
					changed++
				}
				if f.Changed || f.Skipped != "" {
					files = append(files, f)
				}
			}
			if io.JSON {
				if err := io.printJSON(map[string]interface{}{"checked": len(paths), "changed": changed, "files": files}); err != nil {
					return err
				}
			} else {
				verb := "formatted"
				if check {
					verb = "would format"
				}
				for _, f := range files {
					if f.Skipped != "" {
						io.printf("skipped %s: %s\n", f.Path, f.Skipped)
					} else {
						io.printf("%s %s\n", verb, f.Path)
					}
				}
				io.printf("%d of %d file(s) %s\n", changed, len(paths), verb)
			}
			if check && changed > 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "write nothing; exit non-zero when a file would change")
	return cmd
}

// fmtTargets lists the files to format: the given paths, or every
// feature, BRD and entry point in the workspace.
func fmtTargets(ws *workspace.Workspace, args []string) ([]string, error) {
	if len(args) > 0 {
		var out []string
		for _, a := range args {
			p, err := filepath.Abs(a)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
		return out, nil
	}
	var out []string
	for _, dir := range []string{ws.FeaturesDir(), ws.BRDsDir(), ws.EntryPointsDir()} {
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return filepath.SkipDir
				}
				return err
			}
			if d.IsDir() && d.Name() == ".rejected" {
				return filepath.SkipDir // archived entry points are not part of the graph
			}
			if !d.IsDir() && strings.HasSuffix(p, ".yaml") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// fmtOne formats one file, picking its schema by the directory it is in.
func fmtOne(ws *workspace.Workspace, path string, check bool) (fmtFile, error) {
	rel := path
	if r, err := filepath.Rel(filepath.Dir(ws.LatticeDir), path); err == nil {
		rel = r
	}
	var v interface{}
	switch {
	case within(path, ws.FeaturesDir()):
		v = &schema.Manifest{}
	case within(path, ws.BRDsDir()):
		v = &schema.BRD{}
	case within(path, ws.EntryPointsDir()):
		v = &schema.EntryPoint{}
	default:
		return fmtFile{Path: rel, Skipped: "not a feature, BRD or entry point file"}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmtFile{}, err
	}
	res, err := schema.Canonicalize(data, v)
	if err != nil {
		return fmtFile{}, err
	}
	f := fmtFile{Path: rel, Changed: res.Changed, Skipped: res.Skipped}
	if res.Changed && !check {
		if err := os.WriteFile(path, res.Canonical, 0o644); err != nil {
			return fmtFile{}, err
		}
	}
	return f, nil
}

// within reports whether path is inside dir.
func within(path, dir string) bool {
	r, err := filepath.Rel(dir, path)
	return err == nil && r != "." && !strings.HasPrefix(r, "..")
}
