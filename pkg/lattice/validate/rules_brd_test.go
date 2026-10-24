package validate

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// brdManifest is a minimal-but-valid Manifest stub for BRD tests so we
// don't constantly tickle the unrelated feature-rule failures (no
// invariants → no UNENFORCED/UNVERIFIED noise).
func brdManifest(id string) schema.Manifest {
	return schema.Manifest{
		ID: id, Version: 1, Status: schema.StatusProduction,
		Purpose: "p", Owners: schema.Owners{Business: "b", Engineering: "e"},
	}
}

// @verifies brds:INV-1
func TestBRDPhantomFeature(t *testing.T) {
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.checkout", Version: 1, Status: schema.BRDApproved,
			Title: "Checkout", BusinessProblem: "p",
			ImplementsVia: []string{"missing.feature"},
			Approval:      &schema.BRDApproval{ApprovedBy: "x", ApprovedVersion: 1},
		}},
	}
	got := codes(Validate(kg, config.Default(), Options{}))
	if !got[schema.CodeBRDPhantomFeature] {
		t.Error("expected BRD_PHANTOM_FEATURE for missing feature")
	}
}

// @verifies brds:INV-1
func TestBRDPhantomFeatureClean(t *testing.T) {
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.checkout", Version: 1, Status: schema.BRDApproved,
			Title: "Checkout", BusinessProblem: "p",
			ImplementsVia: []string{"checkout"},
			Approval:      &schema.BRDApproval{ApprovedBy: "x", ApprovedVersion: 1},
		}},
		Features: []schema.Manifest{brdManifest("checkout")},
	}
	got := codes(Validate(kg, config.Default(), Options{}))
	if got[schema.CodeBRDPhantomFeature] {
		t.Error("BRD_PHANTOM_FEATURE fired on a clean reference")
	}
}

// @verifies brds:INV-4
func TestBRDIDFormat(t *testing.T) {
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID:      "checkout", // missing brd. prefix
			Version: 1, Status: schema.BRDDraft,
			Title: "x", BusinessProblem: "p",
		}},
	}
	if !codes(Validate(kg, config.Default(), Options{}))[schema.CodeBRDIDFormat] {
		t.Error("expected BRD_ID_FORMAT for an id without the brd. prefix")
	}
}

func TestBRDDuplicate(t *testing.T) {
	b := schema.BRD{
		ID: "brd.checkout", Version: 1, Status: schema.BRDDraft,
		Title: "x", BusinessProblem: "p",
	}
	kg := schema.KnowledgeGraph{BRDs: []schema.BRD{b, b}}
	if !codes(Validate(kg, config.Default(), Options{}))[schema.CodeBRDIDDuplicate] {
		t.Error("expected BRD_ID_DUPLICATE")
	}
}

// @verifies brds:INV-3
func TestBRDDriftAfterEdit(t *testing.T) {
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.checkout", Version: 3, Status: schema.BRDApproved,
			Title: "x", BusinessProblem: "p",
			ImplementsVia: []string{"checkout"},
			Approval:      &schema.BRDApproval{ApprovedBy: "x", ApprovedVersion: 2},
		}},
		Features: []schema.Manifest{brdManifest("checkout")},
	}
	if !codes(Validate(kg, config.Default(), Options{}))[schema.CodeBRDDrift] {
		t.Error("expected BRD_DRIFT when version advances past approval")
	}
}

// @verifies brds:INV-2
func TestBRDUnapprovedLLM(t *testing.T) {
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.checkout", Version: 1, Status: schema.BRDDraft,
			Title: "x", BusinessProblem: "p",
			Provenance: schema.BRDProvenance{Source: schema.BRDSourceLLMFromCode},
		}},
	}
	if !codes(Validate(kg, config.Default(), Options{}))[schema.CodeBRDUnapprovedLLM] {
		t.Error("expected BRD_UNAPPROVED_LLM for an LLM-drafted BRD")
	}
}

// A draft an agent wrote needs review whatever its source says: `brd new`
// run by an agent records source: human. A human's draft and an approved
// agent draft raise nothing.
func TestBRDUnapprovedAgentAuthored(t *testing.T) {
	for name, tc := range map[string]struct {
		status schema.BRDStatus
		author string
		want   bool
	}{
		"agent draft":    {schema.BRDDraft, "agent:drafter", true},
		"Agent prefix":   {"", "Agent-7", true},
		"human draft":    {schema.BRDDraft, "pm@example.com", false},
		"no author":      {schema.BRDDraft, "", false},
		"agent approved": {schema.BRDApproved, "agent:drafter", false},
	} {
		kg := schema.KnowledgeGraph{BRDs: []schema.BRD{{
			ID: "brd.checkout", Version: 1, Status: tc.status, Title: "x", BusinessProblem: "p",
			Provenance: schema.BRDProvenance{Source: schema.BRDSourceHuman, AuthoredBy: tc.author},
		}}}
		var got *schema.Violation
		for _, x := range Validate(kg, config.Default(), Options{}) {
			if x.Code == schema.CodeBRDUnapprovedLLM {
				got = &x
			}
		}
		if (got != nil) != tc.want {
			t.Errorf("%s: BRD_UNAPPROVED_LLM = %v, want %v", name, got != nil, tc.want)
		}
		if got != nil && !strings.Contains(got.Message, "written by "+tc.author) {
			t.Errorf("%s: message %q does not name the author", name, got.Message)
		}
	}
}

func TestFeatureNoBRDWarning(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{brdManifest("checkout")},
	}
	if !codes(Validate(kg, config.Default(), Options{}))[schema.CodeFeatureNoBRD] {
		t.Error("expected FEATURE_NO_BRD for a feature without an upstream BRD")
	}
}

func TestFeatureNoBRDSuppressedByReverseLink(t *testing.T) {
	// Feature has no implements_brd, but BRD lists it in implements_via.
	// That implicit link should suppress FEATURE_NO_BRD.
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.checkout", Version: 1, Status: schema.BRDApproved,
			Title: "x", BusinessProblem: "p",
			ImplementsVia: []string{"checkout"},
			Approval:      &schema.BRDApproval{ApprovedBy: "x", ApprovedVersion: 1},
		}},
		Features: []schema.Manifest{brdManifest("checkout")},
	}
	if codes(Validate(kg, config.Default(), Options{}))[schema.CodeFeatureNoBRD] {
		t.Error("FEATURE_NO_BRD should be suppressed when a BRD lists the feature in implements_via")
	}
}

func TestFeatureBRDMissing(t *testing.T) {
	f := brdManifest("checkout")
	f.ImplementsBRD = "brd.nonexistent"
	kg := schema.KnowledgeGraph{Features: []schema.Manifest{f}}
	if !codes(Validate(kg, config.Default(), Options{}))[schema.CodeFeatureBRDMissing] {
		t.Error("expected FEATURE_BRD_MISSING when implements_brd names a missing BRD")
	}
}

func TestBRDCriterionUnmappedInfo(t *testing.T) {
	// A criterion with no maps_to_invariant — info-level only.
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.x", Version: 1, Status: schema.BRDApproved,
			Title: "x", BusinessProblem: "p",
			ImplementsVia: []string{"checkout"},
			SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", Statement: "x"}, // no maps_to_invariant
			},
			Approval: &schema.BRDApproval{ApprovedBy: "x", ApprovedVersion: 1},
		}},
		Features: []schema.Manifest{brdManifest("checkout")},
	}
	got := Validate(kg, config.Default(), Options{})
	if !codes(got)[schema.CodeBRDCriterionUnmapped] {
		t.Error("expected BRD_CRITERION_UNMAPPED")
	}
	for _, v := range got {
		if v.Code == schema.CodeBRDCriterionUnmapped && v.Severity != schema.SeverityInfo {
			t.Errorf("BRD_CRITERION_UNMAPPED should be info, got %q", v.Severity)
		}
	}
}

// @verifies brds:INV-5
func TestBRDCriterionPhantomInvariant(t *testing.T) {
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.x", Version: 1, Status: schema.BRDApproved,
			Title: "x", BusinessProblem: "p",
			ImplementsVia: []string{"checkout"},
			SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", Statement: "x", MapsToInvariant: schema.InvariantRefs{"checkout:INV-NOPE"}},
			},
			Approval: &schema.BRDApproval{ApprovedBy: "x", ApprovedVersion: 1},
		}},
		Features: []schema.Manifest{brdManifest("checkout")},
	}
	if !codes(Validate(kg, config.Default(), Options{}))[schema.CodeBRDCriterionPhantomInvariant] {
		t.Error("expected BRD_CRITERION_PHANTOM_INVARIANT for bad maps_to_invariant")
	}
}

func TestBRDCriterionUnverifiedWarning(t *testing.T) {
	// Invariant exists, but no enforcer + no verifier.
	feat := brdManifest("checkout")
	feat.Invariants = []schema.Invariant{{ID: "INV-1", Statement: "x"}}
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.x", Version: 1, Status: schema.BRDApproved,
			Title: "x", BusinessProblem: "p",
			ImplementsVia: []string{"checkout"},
			SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", Statement: "x", MapsToInvariant: schema.InvariantRefs{"checkout:INV-1"}},
			},
			Approval: &schema.BRDApproval{ApprovedBy: "x", ApprovedVersion: 1},
		}},
		Features: []schema.Manifest{feat},
	}
	got := Validate(kg, config.Default(), Options{})
	if !codes(got)[schema.CodeBRDCriterionUnverified] {
		t.Error("expected BRD_CRITERION_UNVERIFIED")
	}
}

func TestBRDCriterionVerifiedNoViolation(t *testing.T) {
	// Full happy path: enforcer + verifier present, criterion verified.
	feat := brdManifest("checkout")
	feat.Invariants = []schema.Invariant{{ID: "INV-1", Statement: "x"}}
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.x", Version: 1, Status: schema.BRDApproved,
			Title: "x", BusinessProblem: "p",
			ImplementsVia: []string{"checkout"},
			SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", Statement: "x", MapsToInvariant: schema.InvariantRefs{"checkout:INV-1"}},
			},
			Approval: &schema.BRDApproval{ApprovedBy: "x", ApprovedVersion: 1},
		}},
		Features: []schema.Manifest{feat},
		Symbols:  []schema.GraphSymbol{{FQN: "S", Feature: "checkout", EnforcesInvariants: []string{"INV-1"}}},
		Tests:    []schema.GraphSymbol{{FQN: "T", IsTest: true, Verifies: []string{"checkout:INV-1"}}},
	}
	got := codes(Validate(kg, config.Default(), Options{}))
	for _, c := range []string{
		schema.CodeBRDCriterionPhantomInvariant,
		schema.CodeBRDCriterionUnverified,
		schema.CodeBRDCriterionUnmapped,
	} {
		if got[c] {
			t.Errorf("unexpected %s on a verified criterion", c)
		}
	}
}

func TestBRDUnreferencedInfo(t *testing.T) {
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.draft", Version: 1, Status: schema.BRDDraft,
			Title: "x", BusinessProblem: "p",
		}},
	}
	got := Validate(kg, config.Default(), Options{})
	found := false
	for _, v := range got {
		if v.Code == schema.CodeBRDUnreferenced {
			if v.Severity != schema.SeverityInfo {
				t.Errorf("BRD_UNREFERENCED should be info-severity, got %q", v.Severity)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected BRD_UNREFERENCED info violation")
	}
}
