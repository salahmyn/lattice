// Package validate runs every Lattice validation rule (design section 20)
// over a knowledge graph and returns structured violations.
package validate

import (
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/lease"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// idPattern is the legal manifest-id format (design section 7).
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// maxSubfeatureDepth is the dot-nesting depth past which a warning fires.
const maxSubfeatureDepth = 4

// corpus is the indexed knowledge graph the rules query.
type corpus struct {
	kg       schema.KnowledgeGraph
	cfg      config.Config
	opts     Options
	features map[string]*schema.Manifest
	caps     map[string]map[string]bool // feature -> capability ids
	invs     map[string]map[string]bool // feature -> invariant ids
	roles    map[string]bool            // role ids declared anywhere
	initOK   map[string]bool            // initiative ids
	streams  map[string]map[string]bool // initiative -> stream ids
}

func newCorpus(kg schema.KnowledgeGraph, cfg config.Config, opts Options) *corpus {
	c := &corpus{
		kg: kg, cfg: cfg, opts: opts,
		features: map[string]*schema.Manifest{},
		caps:     map[string]map[string]bool{},
		invs:     map[string]map[string]bool{},
		roles:    map[string]bool{},
		initOK:   map[string]bool{},
		streams:  map[string]map[string]bool{},
	}
	for i := range kg.Features {
		m := &kg.Features[i]
		c.features[m.ID] = m
		c.caps[m.ID] = map[string]bool{}
		c.invs[m.ID] = map[string]bool{}
		for _, cap := range m.Capabilities {
			c.caps[m.ID][cap.ID] = true
		}
		for _, inv := range m.Invariants {
			c.invs[m.ID][inv.ID] = true
		}
		for _, role := range m.Roles {
			c.roles[role.ID] = true
		}
	}
	for i := range kg.Initiatives {
		in := &kg.Initiatives[i]
		c.initOK[in.ID] = true
		c.streams[in.ID] = map[string]bool{}
		for _, s := range in.Streams {
			c.streams[in.ID][s.ID] = true
		}
	}
	return c
}

// Options controls a validation run.
type Options struct {
	// ReviewMode runs only the checks that need no source code: manifest,
	// dependency, and initiative/task integrity. The code-coupled checks
	// (annotation and verification integrity) are skipped — used when an
	// operator has access to the lattice/ directory but not the code.
	ReviewMode bool

	// ResultOf reports the ingested pass/fail of a verifier test (v0.8 γ),
	// wired by the CLI to results.Set.Lookup so the same demonstration
	// evidence drives validation, RTM display, and coverage. nil means no
	// results ingested — validation behaves as v0.7.
	ResultOf func(testFQN string) (passed bool, known bool)

	// Leases are the active work-claim leases (v0.8 §5). When two leases
	// held by different actors claim overlapping scopes, LEASE_SCOPE_OVERLAP
	// fires. Empty in a single-agent run.
	Leases []lease.Lease
}

// Validate runs all rules and returns the complete, sorted violation set,
// including any extraction violations already present on the graph.
func Validate(kg schema.KnowledgeGraph, cfg config.Config, opts Options) []schema.Violation {
	c := newCorpus(kg, cfg, opts)
	var v []schema.Violation

	v = append(v, kg.Violations...) // parse errors carried from extraction
	v = append(v, c.checkManifests()...)
	v = append(v, c.checkBRDs()...)
	v = append(v, c.checkScenarios()...)  // v0.8 α/β — scenario + reach
	v = append(v, c.checkDependencies()...)
	v = append(v, c.checkInitiativesAndTasks()...)
	v = append(v, c.checkLeases()...) // v0.8 §5 — fleet coordination

	if !opts.ReviewMode {
		v = append(v, c.checkAnnotations()...)
		v = append(v, c.checkVerification()...)
		v = append(v, c.checkSurfaces()...)
		v = append(v, c.checkErrors()...)
		v = append(v, checkEntryPoints(kg)...)
		v = append(v, c.checkAMA()...)     // v0.7 — AMA structural checks
		v = append(v, c.checkMeaning()...) // v0.8 δ — meaning fidelity
	}

	sortViolations(v)
	return dedupe(v)
}

// CodeCoupledChecks names the validation categories that review mode defers.
var CodeCoupledChecks = []string{
	"annotation integrity", "verification integrity",
	"surface integrity", "error-contract integrity",
}

// HasErrors reports whether any violation is error-severity.
func HasErrors(v []schema.Violation) bool {
	for _, x := range v {
		if x.IsError() {
			return true
		}
	}
	return false
}

// resolveRef interprets an annotation reference. A qualified "feature:item"
// ref returns its parts; a bare ref is scoped to fallbackFeature.
func resolveRef(ref, fallbackFeature string) (feature, item string) {
	if i := strings.LastIndex(ref, ":"); i > 0 {
		return ref[:i], ref[i+1:]
	}
	return fallbackFeature, ref
}

func sortViolations(v []schema.Violation) {
	sort.SliceStable(v, func(i, j int) bool {
		fi, fj := "", ""
		if v[i].Location != nil {
			fi = v[i].Location.File
		}
		if v[j].Location != nil {
			fj = v[j].Location.File
		}
		if fi != fj {
			return fi < fj
		}
		if v[i].Code != v[j].Code {
			return v[i].Code < v[j].Code
		}
		return v[i].Message < v[j].Message
	})
}

// dedupe removes exact-duplicate violations.
func dedupe(v []schema.Violation) []schema.Violation {
	seen := map[string]bool{}
	out := v[:0]
	for _, x := range v {
		key := x.Code + "|" + x.Message
		if x.Location != nil {
			key += "|" + x.Location.File
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, x)
	}
	return out
}
