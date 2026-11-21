package validate

// @module-feature validation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// maxNamedFailures is how many failing tests a finding names before it
// says "and N more".
const maxNamedFailures = 3

// resultsCurrent reports whether the ingested results belong to the graph's
// commit. It is the audit's matches_head rule: the stamps agree, or one of
// them is unknown (no git, or a set ingested without a commit).
func (c *corpus) resultsCurrent() bool {
	stamp, head := c.opts.ResultsCommit, c.kg.GeneratedFromCommit
	return stamp == "" || head == "" || stamp == head
}

// failingVerifiers maps each declared invariant ref ("<feature>:INV-N") to
// the sorted FQNs of its verifier tests whose ingested result is red. A test
// tagged for one of the invariant's cases (`@verifies f:INV-N#c`) counts: a
// failing case fails its invariant. A parameterized test reads as the lookup
// aggregates its expansions (the worst one wins). The map is empty when no
// results were ingested or they are for another commit.
func (c *corpus) failingVerifiers() map[string][]string {
	if c.failingDone {
		return c.failing
	}
	c.failingDone = true
	c.failing = map[string][]string{}
	if c.opts.ResultOf == nil || !c.resultsCurrent() {
		return c.failing
	}
	red := map[string]bool{} // test FQN -> its result is red, looked up once
	seen := map[string]bool{}
	for _, t := range c.kg.Tests {
		for _, ref := range t.Verifies {
			base, _, _ := schema.SplitCaseRef(ref)
			feature, inv := resolveRef(base, t.Feature)
			if !c.invs[feature][inv] {
				continue // a scenario ref or an orphan: not an invariant
			}
			if !seen[t.FQN] {
				seen[t.FQN] = true
				passed, known := c.opts.ResultOf(t.FQN)
				red[t.FQN] = known && !passed
			}
			if !red[t.FQN] {
				continue
			}
			key := feature + ":" + inv
			if !containsString(c.failing[key], t.FQN) {
				c.failing[key] = append(c.failing[key], t.FQN)
			}
		}
	}
	for k := range c.failing {
		sort.Strings(c.failing[k])
	}
	return c.failing
}

// checkFailingVerifiers raises VERIFIER_FAILING for every invariant with a
// red verifier in the current results, whether or not a BRD criterion maps
// it. An invariant a criterion already reports as failing is left to that
// finding, which names the same tests, so a red test is reported once.
// @enforces validation:INV-16
func (c *corpus) checkFailingVerifiers() []schema.Violation {
	failing := c.failingVerifiers()
	if len(failing) == 0 {
		return nil
	}
	reported := map[string]bool{}
	for _, row := range rtm.Build(c.kg, c.rtmOptions()).Rows {
		if row.Status == rtm.StatusFailing {
			reported[row.Ref()] = true
		}
	}
	refs := make([]string, 0, len(failing))
	for ref := range failing {
		if !reported[ref] {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	var v []schema.Violation
	for _, ref := range refs {
		feature := ref[:strings.LastIndex(ref, ":")]
		var loc *schema.Location
		if m := c.features[feature]; m != nil && m.SourcePath != "" {
			loc = &schema.Location{File: m.SourcePath}
		}
		v = append(v, schema.Violation{
			Code: schema.CodeVerifierFailing, Severity: schema.SeverityWarning,
			FeatureID: feature, Subject: ref,
			Message: fmt.Sprintf("invariant %s has a failing verifier: %s (evidence: ingested results)",
				ref, nameFailures(failing[ref])),
			Location: loc,
			NextAction: &schema.NextAction{
				Kind:   "fix_test",
				Ref:    ref,
				Detail: "fix the code or the test so " + ref + " holds, then re-ingest results",
			},
		})
	}
	return v
}

// nameFailures lists up to maxNamedFailures tests, then "and N more".
func nameFailures(tests []string) string {
	if len(tests) <= maxNamedFailures {
		return strings.Join(tests, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(tests[:maxNamedFailures], ", "), len(tests)-maxNamedFailures)
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
