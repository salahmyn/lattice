// Package impact computes a read-only change-impact report: what a change
// since a base ref does to the code, to the grounded intent above it, and
// to the manifests that describe it. It is the engine behind
// `lattice impact`, meant to run on every pull request.
//
// The base side is never checked out. Changed files are read with
// `git cat-file` and parsed with the same language adapters extraction
// uses; the head side is the knowledge graph built from the working tree.
package impact

// @module-feature impact

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// DefaultMaxSymbols caps how many enforcer symbols the code roll-up expands.
const DefaultMaxSymbols = 50

// Input is everything one report needs. Graph is the head side: the graph
// built from the working tree, without writing anything.
type Input struct {
	Workspace *workspace.Workspace
	Registry  *adapters.Registry
	Graph     schema.KnowledgeGraph
	// Corpus is the SCIP code graph; nil or empty means none was indexed.
	Corpus *scip.Corpus
	// CorpusError explains why no corpus could be loaded, when one failed.
	CorpusError string

	Since         string
	MaxSymbols    int  // <= 0 takes DefaultMaxSymbols
	IncludeDrafts bool // list criteria of draft and proposed BRDs
}

// Report is the full impact of a change. Paths are repo-relative, as a pull
// request shows them.
type Report struct {
	Since string `json:"since"`
	// Base is merge-base(Since, HEAD): the commit the change is compared to.
	Base string `json:"base"`
	Head string `json:"head"`
	// Uncommitted is true when the working tree differs from HEAD; the
	// report then covers those changes too.
	Uncommitted bool `json:"uncommitted"`

	ChangedFiles       []FileChange         `json:"changed_files"`
	ChangedSymbols     []ChangedSymbol      `json:"changed_symbols"`
	AffectedCriteria   []AffectedCriterion  `json:"affected_criteria"`
	DraftsIncluded     bool                 `json:"drafts_included"`
	DraftCriteria      []AffectedCriterion  `json:"draft_criteria,omitempty"`
	RemovedEnforcement []RemovedEnforcement `json:"removed_enforcement"`
	ManifestDiff       ManifestDiff         `json:"manifest_diff"`
	CodeRollup         CodeRollup           `json:"code_rollup"`
}

// ChangedSymbol is a symbol whose own lines a hunk touches, on the base side,
// the working-tree side or both. A symbol that only shifted in its file, or
// moved with its text unchanged, is not listed.
type ChangedSymbol struct {
	Symbol    string   `json:"symbol"`
	OldSymbol string   `json:"old_symbol,omitempty"` // base FQN, when the move changed it
	Kind      string   `json:"kind"`
	File      string   `json:"file"`
	OldFile   string   `json:"old_file,omitempty"`
	Lines     string   `json:"lines,omitempty"`     // working-tree extent, "12-30"
	OldLines  string   `json:"old_lines,omitempty"` // base extent
	Change    string   `json:"change"`              // modified | added | removed | unscanned
	Feature   string   `json:"feature,omitempty"`
	Enforces  []string `json:"enforces,omitempty"`
	Test      bool     `json:"test,omitempty"`
}

// Change kinds of a ChangedSymbol and an EnforcerChange.
const (
	ChangeModified           = "modified"
	ChangeAdded              = "added"
	ChangeRemoved            = "removed"
	ChangeUnscanned          = "unscanned" // its file left the scanned tree
	ChangeEnforcementRemoved = "enforcement_removed"
	ChangeEnforcementAdded   = "enforcement_added"
)

// RemovedEnforcement is a base-side enforcer that no longer enforces its
// invariant on the working-tree side.
type RemovedEnforcement struct {
	Symbol    string `json:"symbol"`
	Invariant string `json:"invariant"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Reason    string `json:"reason"` // see the Reason* constants
	NewFile   string `json:"new_file,omitempty"`
}

// Why an enforcement pair disappeared.
const (
	ReasonSymbolDeleted   = "symbol_deleted"
	ReasonTagDropped      = "tag_dropped"
	ReasonFileDeleted     = "file_deleted"
	ReasonMovedOutOfScan  = "moved_out_of_scan"
	ReasonNoLongerScanned = "no_longer_scanned"
)

// ReasonText says a removal reason in words.
func ReasonText(r RemovedEnforcement) string {
	switch r.Reason {
	case ReasonSymbolDeleted:
		return "symbol deleted"
	case ReasonTagDropped:
		return "@enforces tag dropped"
	case ReasonFileDeleted:
		return "file deleted"
	case ReasonMovedOutOfScan:
		return "file moved out of the scanned tree (to " + r.NewFile + ")"
	case ReasonNoLongerScanned:
		return "file no longer scanned or no longer parses"
	}
	return r.Reason
}

// AffectedCriterion is a BRD success criterion whose invariant (or, when
// direct-wired, the criterion itself) has an enforcer this change touches.
type AffectedCriterion struct {
	BRD        string           `json:"brd"`
	BRDTitle   string           `json:"brd_title,omitempty"`
	BRDStatus  string           `json:"brd_status"`
	Criterion  string           `json:"criterion"`
	Statement  string           `json:"statement"`
	Tier       int              `json:"tier"`
	DirectWire bool             `json:"direct_wire,omitempty"`
	Invariants []string         `json:"invariants"`
	Enforcers  []EnforcerChange `json:"changed_enforcers"`
}

// EnforcerChange is one changed enforcer of an affected criterion.
type EnforcerChange struct {
	Symbol    string `json:"symbol"`
	File      string `json:"file"`
	Invariant string `json:"invariant"`
	Change    string `json:"change"`
}

// Compute builds the report. It only reads: git is asked for the merge
// base, the diff and base-side file contents, and nothing is written.
func Compute(ctx context.Context, in Input) (Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if in.MaxSymbols <= 0 {
		in.MaxSymbols = DefaultMaxSymbols
	}
	top, err := TopLevel(in.Workspace.LatticeDir)
	if err != nil {
		return Report{}, err
	}
	g, cleanup, err := openRepo(top)
	defer cleanup()
	if err != nil {
		return Report{}, err
	}
	base, head, err := resolveBase(g, in.Since)
	if err != nil {
		return Report{}, err
	}
	changes, err := changedFiles(g, base)
	if err != nil {
		return Report{}, err
	}

	pm := newPathMap(top, in.Workspace)
	rep := Report{
		Since: in.Since, Base: base, Head: head, Uncommitted: dirty(g),
		ChangedFiles: changes, DraftsIncluded: in.IncludeDrafts,
	}

	md, baseFeatures := diffManifests(g, base, pm, changes, in.Graph.Features)
	rep.ManifestDiff = md

	cc := codeChanges(ctx, codeInput{
		g: g, base: base, pm: pm, ws: in.Workspace, reg: in.Registry,
		head: in.Graph, baseFeatures: baseFeatures, changes: changes,
	})
	rep.ChangedSymbols = cc.symbols
	rep.RemovedEnforcement = cc.removed

	approved, drafts := affectedCriteria(in.Graph.BRDs, cc)
	rep.AffectedCriteria = approved
	if in.IncludeDrafts {
		rep.DraftCriteria = drafts
	}
	rep.CodeRollup = rollup(in, pm, cc, approved, drafts)

	normalize(&rep)
	return rep, nil
}

// MarshalJSON writes draft_criteria (as [] when empty) when drafts are
// included and leaves the key out when they are hidden.
func (r Report) MarshalJSON() ([]byte, error) {
	type plain Report
	var v interface{} = plain(r)
	if r.DraftsIncluded {
		list := r.DraftCriteria
		if list == nil {
			list = []AffectedCriterion{}
		}
		v = struct {
			plain
			DraftCriteria []AffectedCriterion `json:"draft_criteria"`
		}{plain(r), list}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// normalize turns nil lists into empty ones so the JSON says [] not null.
func normalize(r *Report) {
	if r.ChangedFiles == nil {
		r.ChangedFiles = []FileChange{}
	}
	if r.ChangedSymbols == nil {
		r.ChangedSymbols = []ChangedSymbol{}
	}
	if r.AffectedCriteria == nil {
		r.AffectedCriteria = []AffectedCriterion{}
	}
	if r.DraftsIncluded && r.DraftCriteria == nil {
		r.DraftCriteria = []AffectedCriterion{}
	}
	if r.RemovedEnforcement == nil {
		r.RemovedEnforcement = []RemovedEnforcement{}
	}
	for _, k := range []*KindDiff{&r.ManifestDiff.Features, &r.ManifestDiff.BRDs, &r.ManifestDiff.EntryPoints} {
		k.normalize()
	}
}

// --- paths ---

// pathMap translates between repo-relative git paths and the graph's paths
// (relative to a code root, prefixed with the root name when there are
// several) and knows where the manifest directories are.
type pathMap struct {
	roots      []rootPath
	featureDir string // repo-relative, with a trailing slash
	brdDir     string
	epDir      string
}

type rootPath struct {
	root   workspace.CodeRoot
	rel    string // repo-relative directory, "" for the repo root
	prefix string // graph path prefix, "" or "<name>/"
}

func newPathMap(top string, ws *workspace.Workspace) pathMap {
	var pm pathMap
	avail := 0
	for _, r := range ws.CodeRoots {
		if r.Available {
			avail++
		}
	}
	for _, r := range ws.CodeRoots {
		if !r.Available {
			continue
		}
		rel, err := filepath.Rel(top, r.Abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // a code root outside this repository is not in its diff
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		rp := rootPath{root: r, rel: rel}
		if avail > 1 {
			rp.prefix = r.Name + "/"
		}
		pm.roots = append(pm.roots, rp)
	}
	// Longest root first, so a nested root wins.
	sort.SliceStable(pm.roots, func(i, j int) bool { return len(pm.roots[i].rel) > len(pm.roots[j].rel) })
	if rel, err := filepath.Rel(top, ws.LatticeDir); err == nil && !strings.HasPrefix(rel, "..") {
		d := filepath.ToSlash(rel)
		if d == "." {
			d = ""
		} else {
			d += "/"
		}
		pm.featureDir, pm.brdDir, pm.epDir = d+"features/", d+"brds/", d+"entry-points/"
	}
	return pm
}

// toGraph maps a git path to its graph path and code root.
func (pm pathMap) toGraph(gitPath string) (string, rootPath, bool) {
	for _, r := range pm.roots {
		if r.rel == "" {
			return r.prefix + gitPath, r, true
		}
		if strings.HasPrefix(gitPath, r.rel+"/") {
			return r.prefix + strings.TrimPrefix(gitPath, r.rel+"/"), r, true
		}
	}
	return "", rootPath{}, false
}

// toGit maps a graph path (or a SCIP document path, which is relative to
// the code root) back to a git path.
func (pm pathMap) toGit(graphPath string) string {
	for _, r := range pm.roots {
		if r.prefix != "" && !strings.HasPrefix(graphPath, r.prefix) {
			continue
		}
		p := strings.TrimPrefix(graphPath, r.prefix)
		if r.rel == "" {
			return p
		}
		return r.rel + "/" + p
	}
	return graphPath
}

// --- refs ---

// normRef qualifies a bare invariant ref with the symbol's feature.
func normRef(ref, feature string) string {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") || feature == "" {
		return ref
	}
	return feature + ":" + ref
}

func normRefs(refs []string, feature string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range refs {
		n := normRef(r, feature)
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// lessID orders ids like SC-2 before SC-10.
func lessID(a, b string) bool {
	pa, na := splitNum(a)
	pb, nb := splitNum(b)
	if pa != pb {
		return pa < pb
	}
	if na != nb {
		return na < nb
	}
	return a < b
}

func splitNum(s string) (string, int) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	n := 0
	for _, c := range s[i:] {
		n = n*10 + int(c-'0')
		if n > 1<<30 {
			break
		}
	}
	return s[:i], n
}
