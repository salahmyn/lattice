package entrypoints

// @module-feature entrypoints

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Reacher answers what a handler reaches through the call graph. *reach.Graph
// is one.
type Reacher interface {
	// Reached returns the FQNs of the nodes reachable from handler through
	// resolved edges, handler included.
	Reached(handler string) map[string]bool
}

// RuntimeInput is what proposals are made from.
type RuntimeInput struct {
	// Roots are the runtime roots (reach.Graph.RuntimeRoots).
	Roots []reach.RuntimeRoot
	// Known are the entry points already declared, detected or persisted.
	Known []schema.EntryPoint
	// Reach walks the call graph from a handler.
	Reach Reacher
	// Symbols and Features say which feature each symbol belongs to.
	Symbols  []schema.GraphSymbol
	Features []schema.Manifest
	// Rejected reports an entry-point id a person rejected; the root is not
	// proposed again.
	Rejected func(id string) bool
}

// ProposeFromRuntime turns every runtime root that no known entry point
// covers into a proposed entry point (status proposal, never production),
// with the flow its handler reaches through the call graph. A root that
// cannot be proposed (see reach.RuntimeRootFindings) yields nothing here.
// @enforces entrypoints:INV-5
func ProposeFromRuntime(in RuntimeInput) []schema.EntryPoint {
	var out []schema.EntryPoint
	used := map[string]bool{}
	for _, rt := range in.Roots {
		if rt.Blocked != "" || rt.FQN == "" || rt.CoveredBy(in.Known) {
			continue
		}
		ep := proposalOf(rt)
		if used[ep.ID] { // two roots with one name: the line tells them apart
			ep.ID += "_" + strconv.Itoa(rt.Line)
		}
		if in.Rejected != nil && in.Rejected(ep.ID) {
			continue
		}
		used[ep.ID] = true
		out = append(out, ep)
	}
	return TraceFromReach(out, in.Reach, in.Symbols, in.Features)
}

// proposalOf builds the entry point a runtime root stands for.
func proposalOf(rt reach.RuntimeRoot) schema.EntryPoint {
	ep := schema.EntryPoint{
		Version: 1, Status: schema.StatusProposal, Kind: rt.Kind,
		Handler: schema.Handler{Symbol: rt.FQN, File: rt.File, Line: rt.Line},
	}
	switch rt.Source {
	case reach.RootServe:
		ep.DetectedBy = "runtime_root:serve"
		ep.ID = "ep.http." + idPath(strings.TrimSuffix(rt.FQN, ".<serve>")) + ".serve"
	case reach.RootCron:
		ep.DetectedBy = "runtime_root:cron"
		ep.Trigger = schema.Trigger{Schedule: rt.Schedule, Job: rt.Job}
		ep.ID = "ep.cron." + idPath(strings.TrimPrefix(rt.FQN, "sql.cron."))
	default:
		ep.DetectedBy = "surface:" + rt.Kind
		ep.Trigger = schema.Trigger{Method: strings.ToUpper(rt.Method), Path: rt.Path}
		ep.ID = "ep." + strings.TrimSuffix(rt.Kind, "_route") + "." + idPath(rt.Method+" "+rt.Path)
	}
	return ep
}

// idPath makes a dotted entry-point id part from a name or an FQN: each dot
// separated part lower-cased, anything but letters and digits an underscore.
func idPath(s string) string {
	parts := strings.Split(s, ".")
	out := parts[:0]
	for _, p := range parts {
		var b strings.Builder
		under := true // drops leading underscores and collapses runs
		for _, r := range strings.ToLower(p) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
				under = false
			} else if !under {
				b.WriteByte('_')
				under = true
			}
		}
		if seg := strings.TrimSuffix(b.String(), "_"); seg != "" {
			out = append(out, seg)
		}
	}
	return strings.Join(out, ".")
}

// TraceFromReach sets each entry point's flow to the features whose symbols
// its handler reaches through the call graph, one step per feature, sorted
// by feature id. Each step is basis "reach": it exists only because the
// handler reaches a symbol of that feature, and its via_symbols are those
// symbols. Test symbols and symbols with no feature give no step.
// @enforces entrypoints:INV-6
func TraceFromReach(eps []schema.EntryPoint, r Reacher, symbols []schema.GraphSymbol, features []schema.Manifest) []schema.EntryPoint {
	if len(eps) == 0 || r == nil {
		return eps
	}
	featureOf := map[string]string{}
	for _, s := range symbols {
		if s.Feature != "" && !s.IsTest {
			featureOf[s.FQN] = s.Feature
		}
	}
	caps := map[string][]schema.Capability{}
	for _, f := range features {
		caps[f.ID] = f.Capabilities
	}
	out := make([]schema.EntryPoint, len(eps))
	copy(out, eps)
	for i := range out {
		byFeature := map[string][]string{}
		for fqn := range r.Reached(out[i].Handler.Symbol) {
			if f := featureOf[fqn]; f != "" {
				byFeature[f] = append(byFeature[f], fqn)
			}
		}
		out[i].Flow = nil
		ids := make([]string, 0, len(byFeature))
		for f := range byFeature {
			ids = append(ids, f)
		}
		sort.Strings(ids)
		for _, f := range ids {
			via := byFeature[f]
			sort.Strings(via)
			if len(via) > 3 {
				via = via[:3]
			}
			out[i].Flow = append(out[i].Flow, schema.FlowStep{
				Feature: f, Capability: bestCapability(via, caps[f]), ViaSymbols: via,
				Basis: schema.FlowBasisReach,
			})
		}
	}
	return out
}

// IsRejected reports that a person rejected the entry point with this id:
// its manifest sits in the .rejected/ archive.
func IsRejected(entryPointsDir, id string) bool {
	rel := filepath.FromSlash(strings.ReplaceAll(id, ".", "/")) + ".yaml"
	_, err := os.Stat(filepath.Join(entryPointsDir, ".rejected", rel))
	return err == nil
}

// PersistProposals writes each proposal that has no manifest yet to
// entryPointsDir, so `lattice ep accept` and `reject` can act on it. The
// flow is left out: it is derived from the call graph and recomputed on
// every extract (see Merge). A manifest that exists is never touched, so a
// reviewer's edits and status survive. Returns the ids it wrote.
func PersistProposals(entryPointsDir string, proposals, persisted []schema.EntryPoint) ([]string, error) {
	have := map[string]bool{}
	for _, ep := range persisted {
		have[ep.ID] = true
	}
	var wrote []string
	for _, ep := range proposals {
		if have[ep.ID] || ep.Status != schema.StatusProposal || ep.DetectedBy == "" {
			continue
		}
		ep.Flow, ep.SideEffects = nil, nil
		if _, err := SaveEntryPoint(entryPointsDir, ep); err != nil {
			return wrote, err
		}
		wrote = append(wrote, ep.ID)
	}
	return wrote, nil
}
