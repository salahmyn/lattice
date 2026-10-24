package impact

// @module-feature impact

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

type codeInput struct {
	g            repoGit
	base         string
	pm           pathMap
	ws           *workspace.Workspace
	reg          *adapters.Registry
	head         schema.KnowledgeGraph
	baseFeatures []schema.Manifest
	changes      []FileChange
}

type codeResult struct {
	symbols []ChangedSymbol
	removed []RemovedEnforcement
	// added is enforcement that appeared on a symbol whose own lines did
	// not change (a tag added above it).
	added []EnforcerChange
}

// filePair is one changed code file, parsed on each side it exists on.
type filePair struct {
	c                *FileChange
	oldGP, newGP     string // graph paths
	oldLines         []string
	newLines         []string
	oldOK, newOK     bool // parsed (and, for the new side, in the head graph)
	oldCode, newCode bool // an adapter handles the path
}

// codeChanges finds the changed symbols and the enforcement that changed in
// every changed code file. Both sides are resolved the same way — the
// changed files' modules run through the graph builder with that side's
// manifests — so a difference between them is a difference in those files,
// not in how much of the repository was read.
func codeChanges(ctx context.Context, in codeInput) codeResult {
	headModules := map[string]bool{}
	for _, m := range in.head.Modules {
		headModules[m.File] = true
	}
	full := map[string]schema.GraphSymbol{} // file\x00fqn -> head-graph symbol
	enforcedByName := map[string]bool{}     // name\x00ref anywhere in the head graph
	for _, s := range allSymbols(in.head) {
		full[s.File+"\x00"+s.FQN] = s
		for _, ref := range normRefs(s.EnforcesInvariants, s.Feature) {
			enforcedByName[s.Name+"\x00"+ref] = true
		}
	}

	var pairs []filePair
	var baseMods, headMods []ir.Module
	for i := range in.changes {
		c := &in.changes[i]
		p := filePair{c: c}
		if c.OldPath != "" {
			p.oldCode = in.reg.For(c.OldPath) != nil
			if gp, rp, ok := in.pm.toGraph(c.OldPath); ok && p.oldCode &&
				extract.Scans(in.ws, in.reg, rp.root, strings.TrimPrefix(gp, rp.prefix)) {
				if src, ok := showBlob(in.g, in.base, c.OldPath); ok {
					if mod, ok := parseModule(ctx, in.reg, gp, src); ok {
						p.oldGP, p.oldLines, p.oldOK = gp, splitLines(src), true
						baseMods = append(baseMods, mod)
					}
				}
			}
		}
		if c.NewPath != "" {
			p.newCode = in.reg.For(c.NewPath) != nil
			if gp, _, ok := in.pm.toGraph(c.NewPath); ok && p.newCode && headModules[gp] {
				if src, err := os.ReadFile(filepath.Join(in.g.dir, filepath.FromSlash(c.NewPath))); err == nil {
					if mod, ok := parseModule(ctx, in.reg, gp, src); ok {
						p.newGP, p.newLines, p.newOK = gp, splitLines(src), true
						headMods = append(headMods, mod)
					}
				}
			}
		}
		if p.oldCode || p.newCode {
			pairs = append(pairs, p)
		}
	}

	stamp := graph.Options{GeneratedAt: time.Unix(0, 0)}
	baseBy := symbolsByFile(graph.Build(graph.Input{Manifests: in.baseFeatures, Modules: baseMods}, stamp))
	headBy := symbolsByFile(graph.Build(graph.Input{Manifests: in.head.Features, Modules: headMods}, stamp))

	var res codeResult
	for _, p := range pairs {
		var oldSyms, newSyms []schema.GraphSymbol
		if p.oldOK {
			oldSyms = baseBy[p.oldGP]
		}
		if p.newOK {
			newSyms = headBy[p.newGP]
		}
		oldBy, oldKeys := byKey(oldSyms)
		newBy, newKeys := byKey(newSyms)

		// 1. Symbols a hunk touches, matched across the sides by key.
		hit := map[string]bool{}
		var order []string
		mark := func(k string) {
			if !hit[k] {
				hit[k] = true
				order = append(order, k)
			}
		}
		for i, s := range oldSyms {
			if overlaps(s, p.c.oldRanges) {
				mark(oldKeys[i])
			}
		}
		for i, s := range newSyms {
			if overlaps(s, p.c.newRanges) {
				mark(newKeys[i])
			}
		}
		changedKeys := map[string]bool{}
		for _, k := range order {
			o, oOK := oldBy[k]
			n, nOK := newBy[k]
			if oOK && nOK && extent(p.oldLines, o) == extent(p.newLines, n) {
				continue // moved, text unchanged
			}
			changedKeys[k] = true
			res.symbols = append(res.symbols, changedSymbol(p, o, oOK, n, nOK, full))
		}

		// 2. Enforcement pairs per file: base side minus working-tree side.
		// Tags sit above a symbol, outside its lines, so a dropped tag
		// shows here even when step 1 saw no change.
		basePairs := enforcementPairs(oldSyms, oldKeys)
		headPairs := enforcementPairs(newSyms, newKeys)
		for _, k := range sortedKeys(basePairs) {
			if _, ok := headPairs[k]; ok {
				continue
			}
			s := basePairs[k]
			ref := k[strings.LastIndex(k, "\x00")+1:]
			symKey := k[:strings.LastIndex(k, "\x00")]
			r := RemovedEnforcement{Symbol: s.FQN, Invariant: ref, File: p.c.OldPath, Line: s.Line}
			r.Reason = removalReason(p, newBy, symKey)
			if r.Reason == ReasonMovedOutOfScan {
				r.NewFile = p.c.NewPath
			}
			if r.Reason != ReasonTagDropped && enforcedByName[s.Name+"\x00"+ref] {
				continue // the symbol moved to another scanned file and still enforces it
			}
			res.removed = append(res.removed, r)
		}
		for _, k := range sortedKeys(headPairs) {
			symKey := k[:strings.LastIndex(k, "\x00")]
			if _, ok := basePairs[k]; ok || changedKeys[symKey] {
				continue
			}
			s := headPairs[k]
			res.added = append(res.added, EnforcerChange{
				Symbol: s.FQN, File: p.c.NewPath, Invariant: k[strings.LastIndex(k, "\x00")+1:],
				Change: ChangeEnforcementAdded,
			})
		}
	}
	sort.SliceStable(res.symbols, func(i, j int) bool {
		if res.symbols[i].File != res.symbols[j].File {
			return res.symbols[i].File < res.symbols[j].File
		}
		return firstLine(res.symbols[i]) < firstLine(res.symbols[j])
	})
	return res
}

// removalReason says why a base-side enforcement pair has no working-tree
// counterpart.
func removalReason(p filePair, newBy map[string]schema.GraphSymbol, symKey string) string {
	switch {
	case p.c.NewPath == "":
		return ReasonFileDeleted
	case !p.newOK && p.c.OldPath != p.c.NewPath:
		return ReasonMovedOutOfScan
	case !p.newOK:
		return ReasonNoLongerScanned
	}
	if _, ok := newBy[symKey]; ok {
		return ReasonTagDropped
	}
	return ReasonSymbolDeleted
}

func changedSymbol(p filePair, o schema.GraphSymbol, oOK bool, n schema.GraphSymbol, nOK bool, full map[string]schema.GraphSymbol) ChangedSymbol {
	var cs ChangedSymbol
	var enf []string
	if nOK {
		if f, ok := full[n.File+"\x00"+n.FQN]; ok {
			n = f // the full graph knows inherited and cross-file enforcement
		}
		cs = ChangedSymbol{Symbol: n.FQN, Kind: n.Kind, File: p.c.NewPath, Lines: span(n),
			Feature: n.Feature, Test: n.IsTest}
		enf = append(enf, normRefs(n.EnforcesInvariants, n.Feature)...)
	}
	if oOK {
		if !nOK {
			cs = ChangedSymbol{Symbol: o.FQN, Kind: o.Kind, File: p.c.OldPath, Feature: o.Feature, Test: o.IsTest}
		} else {
			if o.FQN != n.FQN {
				cs.OldSymbol = o.FQN
			}
			if p.c.OldPath != p.c.NewPath {
				cs.OldFile = p.c.OldPath
			}
		}
		cs.OldLines = span(o)
		enf = append(enf, normRefs(o.EnforcesInvariants, o.Feature)...)
	}
	switch {
	case oOK && nOK:
		cs.Change = ChangeModified
	case nOK:
		cs.Change = ChangeAdded
	case p.c.NewPath != "" && !p.newOK:
		cs.Change = ChangeUnscanned
	default:
		cs.Change = ChangeRemoved
	}
	cs.Enforces = normRefs(enf, "")
	return cs
}

// overlaps reports whether a changed range touches the symbol's own lines,
// from its declaration line to its last line. Comments above the symbol
// (where its tags sit) are outside, so a tag edit alone does not change it.
// @enforces impact:INV-2
func overlaps(s schema.GraphSymbol, ranges []lineRange) bool {
	from, to := s.Line, s.EndLine
	if to < from {
		to = from
	}
	for _, r := range ranges {
		if r.from <= to && r.to >= from {
			return true
		}
	}
	return false
}

// extent returns the symbol's own text, for telling a moved symbol from a
// changed one.
func extent(lines []string, s schema.GraphSymbol) string {
	from, to := s.Line, s.EndLine
	if to < from {
		to = from
	}
	if from < 1 || from > len(lines) {
		return ""
	}
	if to > len(lines) {
		to = len(lines)
	}
	return strings.Join(lines[from-1:to], "\n")
}

// enforcementPairs maps "<symbol key>\x00<invariant ref>" to the symbol, for
// every invariant a symbol of the file enforces after propagation.
// @enforces impact:INV-3
func enforcementPairs(syms []schema.GraphSymbol, keys []string) map[string]schema.GraphSymbol {
	out := map[string]schema.GraphSymbol{}
	for i, s := range syms {
		for _, ref := range normRefs(s.EnforcesInvariants, s.Feature) {
			out[keys[i]+"\x00"+ref] = s
		}
	}
	return out
}

// byKey indexes a file's symbols by a path-independent key (enclosing name
// and own name), so a file that moved directories, which changes the FQN
// some adapters derive from the path, still pairs its symbols. Repeated
// keys get an ordinal.
func byKey(syms []schema.GraphSymbol) (map[string]schema.GraphSymbol, []string) {
	out := map[string]schema.GraphSymbol{}
	keys := make([]string, len(syms))
	seen := map[string]int{}
	for i, s := range syms {
		k := localName(s.EnclosingFQN) + "." + s.Name + "/" + s.Kind
		if n := seen[k]; n > 0 {
			seen[k] = n + 1
			k = fmt.Sprintf("%s#%d", k, n)
		} else {
			seen[k] = 1
		}
		keys[i] = k
		out[k] = s
	}
	return out, keys
}

func localName(fqn string) string {
	for _, sep := range []string{"::", ".", "\\", "/", "#"} {
		if i := strings.LastIndex(fqn, sep); i >= 0 {
			fqn = fqn[i+len(sep):]
		}
	}
	return fqn
}

func parseModule(ctx context.Context, reg *adapters.Registry, graphPath string, src []byte) (ir.Module, bool) {
	ad := reg.For(graphPath)
	if ad == nil {
		return ir.Module{}, false
	}
	mod, err := ad.Parse(ctx, graphPath, src)
	if err != nil {
		return ir.Module{}, false
	}
	return mod, true
}

func allSymbols(kg schema.KnowledgeGraph) []schema.GraphSymbol {
	out := make([]schema.GraphSymbol, 0, len(kg.Symbols)+len(kg.Tests))
	out = append(out, kg.Symbols...)
	return append(out, kg.Tests...)
}

// symbolsByFile groups a graph's symbols and tests by file, in line order.
func symbolsByFile(kg schema.KnowledgeGraph) map[string][]schema.GraphSymbol {
	out := map[string][]schema.GraphSymbol{}
	for _, s := range allSymbols(kg) {
		out[s.File] = append(out[s.File], s)
	}
	for f := range out {
		syms := out[f]
		sort.SliceStable(syms, func(i, j int) bool {
			if syms[i].Line != syms[j].Line {
				return syms[i].Line < syms[j].Line
			}
			return syms[i].FQN < syms[j].FQN
		})
	}
	return out
}

func splitLines(src []byte) []string {
	return strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
}

func span(s schema.GraphSymbol) string {
	if s.EndLine > s.Line {
		return fmt.Sprintf("%d-%d", s.Line, s.EndLine)
	}
	return fmt.Sprint(s.Line)
}

func firstLine(cs ChangedSymbol) int {
	l := cs.Lines
	if l == "" {
		l = cs.OldLines
	}
	n := 0
	for _, c := range l {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- section 2: affected criteria ---

// affectedCriteria lists every criterion whose invariant has a changed
// enforcer, split into approved BRDs and draft/proposed ones. A direct-wired
// criterion is its own invariant: its enforced_by symbols and any symbol
// tagged `@enforces <brd>:<criterion>` count.
func affectedCriteria(brds []schema.BRD, cc codeResult) (approved, drafts []AffectedCriterion) {
	byRef := map[string][]EnforcerChange{}
	byFQN := map[string][]EnforcerChange{}
	idx := map[string]int{} // ref\x00symbol -> position in byRef[ref]
	add := func(ec EnforcerChange) {
		k := ec.Invariant + "\x00" + ec.Symbol
		if i, ok := idx[k]; ok {
			if ec.Change == ChangeEnforcementRemoved {
				byRef[ec.Invariant][i].Change = ec.Change
			}
			return
		}
		idx[k] = len(byRef[ec.Invariant])
		byRef[ec.Invariant] = append(byRef[ec.Invariant], ec)
	}
	for _, cs := range cc.symbols {
		file := cs.File
		for _, ref := range cs.Enforces {
			add(EnforcerChange{Symbol: cs.Symbol, File: file, Invariant: ref, Change: cs.Change})
		}
		ec := EnforcerChange{Symbol: cs.Symbol, File: file, Change: cs.Change}
		byFQN[cs.Symbol] = append(byFQN[cs.Symbol], ec)
		if cs.OldSymbol != "" {
			byFQN[cs.OldSymbol] = append(byFQN[cs.OldSymbol], ec)
		}
	}
	for _, r := range cc.removed {
		add(EnforcerChange{Symbol: r.Symbol, File: r.File, Invariant: r.Invariant, Change: ChangeEnforcementRemoved})
		byFQN[r.Symbol] = append(byFQN[r.Symbol], EnforcerChange{Symbol: r.Symbol, File: r.File, Change: ChangeEnforcementRemoved})
	}
	for _, a := range cc.added {
		add(a)
		byFQN[a.Symbol] = append(byFQN[a.Symbol], EnforcerChange{Symbol: a.Symbol, File: a.File, Change: a.Change})
	}

	sorted := append([]schema.BRD(nil), brds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, b := range sorted {
		if b.Status == schema.BRDSuperseded {
			continue
		}
		for _, sc := range b.SuccessCriteria {
			refs := normRefs(sc.MapsToInvariant, "")
			if sc.DirectWire {
				refs = append(refs, b.ID+":"+sc.ID)
			}
			var enf []EnforcerChange
			seen := map[string]bool{}
			for _, ref := range refs {
				for _, ec := range byRef[ref] {
					if k := ref + "\x00" + ec.Symbol; !seen[k] {
						seen[k] = true
						enf = append(enf, ec)
					}
				}
			}
			if sc.DirectWire {
				self := b.ID + ":" + sc.ID
				for _, fqn := range sc.EnforcedBy {
					for _, ec := range byFQN[strings.TrimSpace(fqn)] {
						if k := self + "\x00" + ec.Symbol; !seen[k] {
							seen[k] = true
							ec.Invariant = self
							enf = append(enf, ec)
						}
					}
				}
			}
			if len(enf) == 0 {
				continue
			}
			ac := AffectedCriterion{
				BRD: b.ID, BRDTitle: b.Title, BRDStatus: string(b.Status),
				Criterion: sc.ID, Statement: schema.InlineText(sc.Statement), Tier: sc.EffectiveTier(),
				DirectWire: sc.DirectWire, Invariants: refs, Enforcers: enf,
			}
			if ac.Invariants == nil {
				ac.Invariants = []string{}
			}
			if b.Status == schema.BRDApproved {
				approved = append(approved, ac)
			} else {
				drafts = append(drafts, ac)
			}
		}
	}
	less := func(list []AffectedCriterion) func(i, j int) bool {
		return func(i, j int) bool {
			if list[i].BRD != list[j].BRD {
				return list[i].BRD < list[j].BRD
			}
			return lessID(list[i].Criterion, list[j].Criterion)
		}
	}
	sort.SliceStable(approved, less(approved))
	sort.SliceStable(drafts, less(drafts))
	return approved, drafts
}
