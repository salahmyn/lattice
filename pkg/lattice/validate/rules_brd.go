package validate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// brdIDPattern is the legal BRD-id format. We deliberately require the
// `brd.` prefix so a quick `grep '^id: brd\.'` finds every BRD on disk
// and prevents collision with the feature-id namespace.
var brdIDPattern = regexp.MustCompile(`^brd\.[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// checkBRDs runs every BRD integrity rule. All rules are conservative
// by design — the BRD axis is opt-in; the validator never errors on
// absence and only errors on outright structural problems (phantom
// references, schema breakage, duplicate ids).
func (c *corpus) checkBRDs() []schema.Violation {
	var v []schema.Violation

	seenID := map[string]string{} // id -> first source path

	// Pre-index feature ids and brd ids for fast lookup.
	featureIDs := map[string]bool{}
	for i := range c.kg.Features {
		featureIDs[c.kg.Features[i].ID] = true
	}
	brdIDs := map[string]bool{}
	for i := range c.kg.BRDs {
		brdIDs[c.kg.BRDs[i].ID] = true
	}

	for i := range c.kg.BRDs {
		b := &c.kg.BRDs[i]
		loc := &schema.Location{File: b.SourcePath}

		// BRD_SCHEMA: required fields + enum validity.
		for _, msg := range brdSchemaErrors(b) {
			v = append(v, schema.Violation{
				Code: schema.CodeBRDSchema, Severity: schema.SeverityError,
				Message:  msg,
				Location: loc,
				NextAction: &schema.NextAction{Kind: "edit_brd", Detail: msg},
			})
		}

		// BRD_ID_FORMAT.
		if b.ID != "" && !brdIDPattern.MatchString(b.ID) {
			v = append(v, schema.Violation{
				Code: schema.CodeBRDIDFormat, Severity: schema.SeverityError,
				Message:  fmt.Sprintf("BRD id %q must match brd.<segment>(.<segment>)*", b.ID),
				Location: loc,
				NextAction: &schema.NextAction{
					Kind:   "edit_brd",
					Field:  "id",
					Detail: "id must start with `brd.` and use lowercase dot-separated segments",
				},
			})
		}

		// BRD_ID_DUPLICATE.
		if b.ID != "" {
			if first, dup := seenID[b.ID]; dup {
				v = append(v, schema.Violation{
					Code: schema.CodeBRDIDDuplicate, Severity: schema.SeverityError,
					Message:  fmt.Sprintf("BRD id %q is already declared in %s", b.ID, first),
					Location: loc,
				})
			} else {
				seenID[b.ID] = b.SourcePath
			}
		}

		// BRD_PHANTOM_FEATURE — forward link points at a missing feature.
		for _, fid := range b.ImplementsVia {
			if !featureIDs[fid] {
				v = append(v, schema.Violation{
					Code: schema.CodeBRDPhantomFeature, Severity: schema.SeverityError,
					Message:  fmt.Sprintf("BRD %q implements_via names a missing feature: %s", b.ID, fid),
					Location: loc,
					NextAction: &schema.NextAction{
						Kind:   "edit_brd",
						Field:  "implements_via",
						Detail: "remove the reference, or create the feature first via `lattice new feature " + fid + "`",
					},
				})
			}
		}

		// BRD_UNREFERENCED — info only; no features point back yet. Most
		// commonly a fresh draft. Skip when the BRD has its own forward
		// links (a draft that already lists features is fine).
		hasForward := len(b.ImplementsVia) > 0
		hasReverse := false
		for _, f := range c.kg.Features {
			if f.ImplementsBRD == b.ID {
				hasReverse = true
				break
			}
		}
		if !hasForward && !hasReverse {
			v = append(v, schema.Violation{
				Code: schema.CodeBRDUnreferenced, Severity: schema.SeverityInfo,
				Message:  fmt.Sprintf("BRD %q has no implementing features yet", b.ID),
				Location: loc,
				NextAction: &schema.NextAction{
					Kind:   "edit_brd",
					Field:  "implements_via",
					Detail: "link a feature via `lattice brd link " + b.ID + " <feature-id>`",
				},
			})
		}

		// BRD_DRIFT — version moved past the last approval. Info only:
		// during a proposal cycle the version legitimately advances
		// before sign-off.
		if b.Approval != nil && b.Approval.ApprovedVersion > 0 && b.Version > b.Approval.ApprovedVersion {
			v = append(v, schema.Violation{
				Code: schema.CodeBRDDrift, Severity: schema.SeverityInfo,
				Message: fmt.Sprintf("BRD %q is at version %d but last approval was for version %d",
					b.ID, b.Version, b.Approval.ApprovedVersion),
				Location: loc,
				NextAction: &schema.NextAction{
					Kind:   "edit_brd",
					Detail: "re-approve with `lattice brd approve " + b.ID + " --by <email>`",
				},
			})
		}

		// BRD_UNAPPROVED_LLM — LLM-generated BRD still in draft.
		if b.Provenance.Source == schema.BRDSourceLLMFromCode &&
			(b.Status == schema.BRDDraft || b.Status == "") {
			v = append(v, schema.Violation{
				Code: schema.CodeBRDUnapprovedLLM, Severity: schema.SeverityWarning,
				Message:  fmt.Sprintf("BRD %q was LLM-regenerated and still draft — needs human review", b.ID),
				Location: loc,
				NextAction: &schema.NextAction{
					Kind:   "edit_brd",
					Field:  "status",
					Detail: "review the draft, edit as needed, then run `lattice brd approve " + b.ID + "`",
				},
			})
		}
	}

	// FEATURE_NO_BRD and FEATURE_BRD_MISSING.
	for i := range c.kg.Features {
		f := &c.kg.Features[i]
		fLoc := &schema.Location{File: f.SourcePath}

		if f.ImplementsBRD == "" {
			// Walk the reverse direction too: a feature whose id is in any
			// BRD's implements_via is implicitly linked, so no warning.
			implicit := false
			for _, b := range c.kg.BRDs {
				for _, fid := range b.ImplementsVia {
					if fid == f.ID {
						implicit = true
						break
					}
				}
				if implicit {
					break
				}
			}
			if !implicit {
				v = append(v, schema.Violation{
					Code: schema.CodeFeatureNoBRD, Severity: schema.SeverityWarning,
					FeatureID: f.ID, Message: fmt.Sprintf("feature %q has no upstream BRD", f.ID),
					Location: fLoc,
					NextAction: &schema.NextAction{
						Kind:   "edit_manifest",
						Field:  "implements_brd",
						Detail: "draft a BRD via `lattice brd new brd." + f.ID + "` then `lattice brd link <brd-id> " + f.ID + "`",
					},
				})
			}
			continue
		}

		// FEATURE_BRD_MISSING — points at a BRD that isn't on disk.
		if !brdIDs[f.ImplementsBRD] {
			v = append(v, schema.Violation{
				Code: schema.CodeFeatureBRDMissing, Severity: schema.SeverityError,
				FeatureID: f.ID,
				Message:   fmt.Sprintf("feature %q implements_brd names a missing BRD: %s", f.ID, f.ImplementsBRD),
				Location:  fLoc,
				NextAction: &schema.NextAction{
					Kind:   "edit_manifest",
					Field:  "implements_brd",
					Detail: "fix the reference, or create the BRD with `lattice brd new " + f.ImplementsBRD + "`",
				},
			})
		}
	}

	// RTM rules (v0.6): walk every BRD success_criterion via the
	// shared rtm package and surface the per-criterion status as a
	// validation finding. Reusing rtm.Build means CLI / UI / MCP /
	// validation all branch on the same status — a SC can never be
	// "verified" in one surface and "unverified" in another.
	matrix := rtm.Build(c.kg, rtm.Options{
		MutationThreshold: c.cfg.MutationTesting.Thresholds.Default,
		ResultOf:          c.opts.ResultOf,
	})
	for _, row := range matrix.Rows {
		// Pinpoint the BRD's own source file in the location.
		var rowLoc *schema.Location
		for i := range c.kg.BRDs {
			if c.kg.BRDs[i].ID == row.BRDID {
				rowLoc = &schema.Location{File: c.kg.BRDs[i].SourcePath}
				break
			}
		}
		switch row.Status {
		case rtm.StatusPhantom:
			v = append(v, schema.Violation{
				Code: schema.CodeBRDCriterionPhantomInvariant, Severity: schema.SeverityError,
				Message: fmt.Sprintf("BRD %q criterion %s: maps_to_invariant %q does not resolve (%s)",
					row.BRDID, row.CriterionID, row.MapsTo, row.StatusReason),
				Location: rowLoc,
				NextAction: &schema.NextAction{
					Kind:   "edit_brd",
					Field:  "success_criteria.maps_to_invariant",
					Detail: "either fix the ref to point at an existing invariant, or remove maps_to_invariant",
				},
			})
		case rtm.StatusFailing:
			// γ — an ingested test result for the criterion's verifier is
			// red. Distinct from "no test": the test we have is failing.
			v = append(v, schema.Violation{
				Code: schema.CodeVerifierFailing, Severity: schema.SeverityWarning,
				Message: fmt.Sprintf("BRD %q criterion %s: %s",
					row.BRDID, row.CriterionID, row.StatusReason),
				Location: rowLoc,
				NextAction: &schema.NextAction{
					Kind:   "fix_test",
					Ref:    row.MapsTo,
					Detail: "the verifier is failing on the generated commit — fix the code or the test, then re-ingest results",
				},
			})
		case rtm.StatusUnenforced, rtm.StatusUnverified, rtm.StatusPartial:
			// Single combined rule; severity warning. We don't repeat
			// the per-invariant UNENFORCED/UNVERIFIED here — those
			// still fire from rules_verification. The criterion-level
			// rule gives the operator the *business* consequence.
			v = append(v, schema.Violation{
				Code: schema.CodeBRDCriterionUnverified, Severity: schema.SeverityWarning,
				Message: fmt.Sprintf("BRD %q criterion %s is %s: %s",
					row.BRDID, row.CriterionID, row.Status, row.StatusReason),
				Location: rowLoc,
				NextAction: &schema.NextAction{
					Kind:   "add_verification",
					Ref:    row.MapsTo,
					Detail: "back the criterion with an enforcer + test verifying " + row.MapsTo,
				},
			})
		case rtm.StatusUnmapped:
			v = append(v, schema.Violation{
				Code: schema.CodeBRDCriterionUnmapped, Severity: schema.SeverityInfo,
				Message: fmt.Sprintf("BRD %q criterion %s has no maps_to_invariant — verification can't be traced",
					row.BRDID, row.CriterionID),
				Location: rowLoc,
				NextAction: &schema.NextAction{
					Kind:   "edit_brd",
					Field:  "success_criteria.maps_to_invariant",
					Detail: "add maps_to_invariant: <feature.id>:<INV-N> to thread this criterion to verification",
				},
			})
		}
	}

	return v
}

// brdSchemaErrors returns human-readable schema problems for a BRD.
func brdSchemaErrors(b *schema.BRD) []string {
	var errs []string
	if strings.TrimSpace(b.ID) == "" {
		errs = append(errs, "missing required field: id")
	}
	if b.Version < 1 {
		errs = append(errs, "field version must be >= 1")
	}
	if !validBRDStatus(b.Status) {
		errs = append(errs, fmt.Sprintf("invalid status %q (want draft|proposed|approved|superseded)", b.Status))
	}
	if strings.TrimSpace(b.Title) == "" {
		errs = append(errs, "missing required field: title")
	}
	if strings.TrimSpace(b.BusinessProblem) == "" {
		errs = append(errs, "missing required field: business_problem")
	}
	// Approval, when present, must name an approver and a version.
	if b.Approval != nil {
		if strings.TrimSpace(b.Approval.ApprovedBy) == "" {
			errs = append(errs, "approval.approved_by must be set when approval is present")
		}
		if b.Approval.ApprovedVersion < 1 {
			errs = append(errs, "approval.approved_version must be >= 1")
		}
	}
	// Provenance source, when set, must be a known value.
	if b.Provenance.Source != "" &&
		b.Provenance.Source != schema.BRDSourceHuman &&
		b.Provenance.Source != schema.BRDSourceLLMFromCode {
		errs = append(errs, fmt.Sprintf("invalid provenance.source %q (want human|llm_from_code)", b.Provenance.Source))
	}
	return errs
}

func validBRDStatus(s schema.BRDStatus) bool {
	for _, ok := range schema.ValidBRDStatuses {
		if s == ok {
			return true
		}
	}
	return false
}
