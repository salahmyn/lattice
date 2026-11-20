package validate

// @module-feature validation

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// caseIDPattern is the legal case id: lowercase letters (a … z, aa, ab, …).
var caseIDPattern = regexp.MustCompile(`^[a-z]+$`)

// checkCaseSchema runs the case-shape rules over every invariant's cases:
// CASE_ID_FORMAT, CASE_ID_DUPLICATE and CASE_SCHEMA (unknown kind, empty
// statement). They need no source, so they run in review mode too.
// @enforces validation:INV-14
func (c *corpus) checkCaseSchema() []schema.Violation {
	var v []schema.Violation
	for i := range c.kg.Features {
		m := &c.kg.Features[i]
		loc := &schema.Location{File: m.SourcePath}
		for _, inv := range m.Invariants {
			seen := map[string]bool{}
			for _, cs := range inv.Cases {
				ref := m.ID + ":" + inv.ID + "#" + cs.ID
				add := func(code, msg, detail string) {
					v = append(v, schema.Violation{
						Code: code, Severity: schema.SeverityError,
						FeatureID: m.ID, InvariantID: inv.ID, Subject: ref, Location: loc, Message: msg,
						NextAction: &schema.NextAction{Kind: "edit_manifest", Field: "invariants.cases", Ref: ref, Detail: detail},
					})
				}
				switch {
				case !caseIDPattern.MatchString(cs.ID):
					add(schema.CodeCaseIDFormat, fmt.Sprintf("invariant %s has a case with id %q; a case id is lowercase letters (a, b, …, z, aa, …)", inv.ID, cs.ID),
						"give the case the next unused letter; ids are never reused")
				case seen[cs.ID]:
					add(schema.CodeCaseIDDuplicate, fmt.Sprintf("invariant %s declares case %q twice", inv.ID, cs.ID),
						"give the second case the next unused letter; ids are never reused")
				}
				seen[cs.ID] = true
				if !validCaseKind(cs.Kind) {
					add(schema.CodeCaseSchema, fmt.Sprintf("case %s of %s has kind %q (want happy|edge|error|security)", cs.ID, inv.ID, cs.Kind),
						"set kind to happy, edge, error or security")
				}
				if strings.TrimSpace(cs.Statement) == "" {
					add(schema.CodeCaseSchema, fmt.Sprintf("case %s of %s has no statement", cs.ID, inv.ID),
						"state the one claim the case makes")
				}
			}
		}
	}
	return v
}

func validCaseKind(k schema.CaseKind) bool {
	for _, x := range schema.ValidCaseKinds {
		if k == x {
			return true
		}
	}
	return false
}

// findCase returns the named case of a feature's invariant.
func (c *corpus) findCase(feature, inv, id string) (schema.InvariantCase, bool) {
	m, ok := c.features[feature]
	if !ok {
		return schema.InvariantCase{}, false
	}
	for _, in := range m.Invariants {
		if in.ID != inv {
			continue
		}
		for _, cs := range in.Cases {
			if cs.ID == id {
				return cs, true
			}
		}
	}
	return schema.InvariantCase{}, false
}

// checkCaseRefs runs the rules that join cases to code: a verifier ref to a
// case the invariant does not declare (ORPHAN_ANNOTATION_CASE), to a retired
// case (CASE_RETIRED_VERIFIED, warning), and a case enforcer that is not a
// symbol enforcing its invariant (CASE_ENFORCER_UNKNOWN). A ref to an
// invariant that does not exist is ORPHAN_ANNOTATION_INVARIANT already and
// is not reported twice.
// @enforces validation:INV-14
func (c *corpus) checkCaseRefs() []schema.Violation {
	var v []schema.Violation
	for _, t := range c.kg.Tests {
		loc := &schema.Location{File: t.File, Line: t.Line}
		for _, ref := range t.VerifiesCases {
			base, id, _ := schema.SplitCaseRef(ref)
			f, inv := resolveRef(base, t.Feature)
			if !c.hasInvariant(f, inv) {
				continue
			}
			full := f + ":" + inv + "#" + id
			cs, ok := c.findCase(f, inv, id)
			switch {
			case !ok:
				v = append(v, schema.Violation{
					Code: schema.CodeOrphanAnnotationCase, Severity: schema.SeverityError,
					FeatureID: f, InvariantID: inv, Subject: full, Location: loc,
					Message:    fmt.Sprintf("%s verifies case %q, which invariant %s of %q does not declare", t.FQN, id, inv, f),
					NextAction: &schema.NextAction{Kind: "edit_manifest", Field: "invariants.cases", Ref: full, Detail: "declare the case, or correct the tag"},
				})
			case cs.Retired:
				v = append(v, schema.Violation{
					Code: schema.CodeCaseRetiredVerified, Severity: schema.SeverityWarning,
					FeatureID: f, InvariantID: inv, Subject: full, Location: loc,
					Message:    fmt.Sprintf("%s verifies case %s of %s:%s, which is retired", t.FQN, id, f, inv),
					NextAction: &schema.NextAction{Kind: "add_annotation", Annotation: "verifies", Ref: f + ":" + inv, Detail: "tag the case the test checks now, or drop the case suffix"},
				})
			}
		}
	}

	enforcers := map[string][]string{} // feature\x00invariant -> enforcer FQNs
	enforces := map[string]bool{}      // fqn\x00feature\x00invariant
	for _, s := range c.kg.Symbols {
		for _, ref := range s.EnforcesInvariants {
			f, inv := resolveRef(ref, s.Feature)
			k := key(f, inv)
			if !enforces[s.FQN+"\x00"+k] {
				enforces[s.FQN+"\x00"+k] = true
				enforcers[k] = append(enforcers[k], s.FQN)
			}
		}
	}
	for i := range c.kg.Features {
		m := &c.kg.Features[i]
		for _, inv := range m.Invariants {
			for _, cs := range inv.Cases {
				if cs.Enforcer == "" || enforces[cs.Enforcer+"\x00"+key(m.ID, inv.ID)] {
					continue
				}
				ref := m.ID + ":" + inv.ID + "#" + cs.ID
				known := "it has no enforcer yet"
				if e := enforcers[key(m.ID, inv.ID)]; len(e) > 0 {
					known = "its enforcers are " + strings.Join(e, ", ")
				}
				v = append(v, schema.Violation{
					Code: schema.CodeCaseEnforcerUnknown, Severity: schema.SeverityError,
					FeatureID: m.ID, InvariantID: inv.ID, Subject: ref, Location: &schema.Location{File: m.SourcePath},
					Message: fmt.Sprintf("case %s of %s names enforcer %q, which is not a symbol enforcing %s:%s (%s)",
						cs.ID, inv.ID, cs.Enforcer, m.ID, inv.ID, known),
					NextAction: &schema.NextAction{Kind: "edit_manifest", Field: "invariants.cases.enforcer", Ref: ref,
						Detail: "name the FQN of a symbol tagged @enforces " + m.ID + ":" + inv.ID + ", or drop the enforcer"},
				})
			}
		}
	}
	return v
}

// Broad-invariant heuristic (INVARIANT_BROAD). It is deliberately simple
// and deterministic: a statement reads as several claims when it makes them
// in separate sentences or semicolon clauses, each with a verb, and is long
// enough for that to matter. Length alone never fires: one claim with a long
// list, or with conditions joined by "and", is one claim, and so is a rule
// with its exception ("X; Y"). The thresholds were tuned on Lattice's own
// features and on a real project's: they name about one invariant in six
// there, and one in fourteen here, where statements are shorter.
const (
	// broadSentences sentences that each have a verb make a statement broad.
	broadSentences = 2
	// broadClaims sentences or semicolon clauses that each have a verb make
	// a statement broad.
	broadClaims = 3
	// broadMinWords is the length floor: a shorter statement is never broad,
	// however it is punctuated.
	broadMinWords = 30
)

// sentenceSplit cuts a statement into sentences; claimSplit cuts a sentence
// into semicolon clauses.
var (
	sentenceSplit = regexp.MustCompile(`[.!?]\s+`)
	claimSplit    = regexp.MustCompile(`;\s+`)
)

// claimVerbs mark a clause as a claim of its own: auxiliaries, modals and
// the predicates invariant statements use most.
var claimVerbs = map[string]bool{
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
	"must": true, "may": true, "can": true, "cannot": true, "never": true,
	"reads": true, "keeps": true, "has": true, "have": true, "does": true, "do": true,
	"counts": true, "stays": true, "gets": true, "returns": true, "fails": true, "passes": true,
	"carries": true, "records": true, "lists": true, "names": true, "makes": true, "changes": true,
	"runs": true, "writes": true, "refuses": true, "shows": true, "goes": true, "becomes": true,
	"matches": true, "takes": true, "wins": true, "holds": true, "applies": true, "fires": true,
}

func hasClaimVerb(s string) bool {
	for _, w := range strings.Fields(strings.ToLower(s)) {
		if claimVerbs[strings.Trim(w, ",:()'\"")] {
			return true
		}
	}
	return false
}

// claims counts what a statement says: its sentences that have a verb, and
// its claims, each sentence or semicolon clause that has a verb. A list or
// a conjunction inside a clause does not add a claim. Common abbreviations
// (e.g., i.e.) do not end a sentence.
func claims(statement string) (sentences, claims int) {
	s := schema.InlineText(statement)
	for _, abbr := range []string{"e.g. ", "i.e. ", "etc. ", "vs. "} {
		s = strings.ReplaceAll(s, abbr, strings.TrimSuffix(abbr, ". ")+" ")
	}
	for _, sentence := range sentenceSplit.Split(s, -1) {
		if hasClaimVerb(sentence) {
			sentences++
		}
		for _, clause := range claimSplit.Split(sentence, -1) {
			if hasClaimVerb(clause) {
				claims++
			}
		}
	}
	return sentences, claims
}

// broad reports whether an invariant statement reads as several claims, and
// why: at least broadMinWords words, and broadSentences sentences or
// broadClaims claims (see claims).
func broad(statement string) (bool, string) {
	words := len(strings.Fields(statement))
	if words < broadMinWords {
		return false, ""
	}
	sentences, n := claims(statement)
	switch {
	case sentences >= broadSentences:
		return true, fmt.Sprintf("%d sentences in %d words", sentences, words)
	case n >= broadClaims:
		return true, fmt.Sprintf("%d claims in %d words", n, words)
	}
	return false, ""
}

// checkBroadInvariants raises INVARIANT_BROAD (info) on an invariant with no
// cases whose statement reads as several claims. When one of its tests
// fails, the report names a paragraph, not a claim.
// @enforces validation:INV-15
func (c *corpus) checkBroadInvariants() []schema.Violation {
	var v []schema.Violation
	for i := range c.kg.Features {
		m := &c.kg.Features[i]
		for _, inv := range m.Invariants {
			if len(inv.Cases) > 0 {
				continue
			}
			ok, why := broad(inv.Statement)
			if !ok {
				continue
			}
			ref := m.ID + ":" + inv.ID
			v = append(v, schema.Violation{
				Code: schema.CodeInvariantBroad, Severity: schema.SeverityInfo,
				FeatureID: m.ID, InvariantID: inv.ID, Subject: ref, Location: &schema.Location{File: m.SourcePath},
				Message: fmt.Sprintf("invariant %s reads as several claims (%s) and has no cases, so a failing test names the paragraph, not the claim", inv.ID, why),
				NextAction: &schema.NextAction{Kind: "edit_manifest", Field: "invariants.cases", Ref: ref,
					Detail: "add cases (one claim each, tagged @verifies " + ref + "#a); split the invariant only if its claims are unrelated"},
			})
		}
	}
	return v
}
