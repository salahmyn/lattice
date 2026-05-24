package brd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// ErrNoLLM is returned when no usable LLM provider is supplied. Callers
// surface this as a friendlier "no LLM configured — set lattice/config.yaml
// agentic.llm" message; the brd package doesn't depend on agentic to
// avoid an import cycle (agentic → extract → brd).
var ErrNoLLM = errors.New("brd from-code requires an LLM provider")

// LLMRequest is one prompt to a provider. Mirrors agentic.CompletionRequest
// — same fields, same semantics — but kept here so the brd package can be
// referenced from extract without pulling agentic in.
type LLMRequest struct {
	SystemPrompt string
	UserMessage  string
	MaxTokens    int
	Temperature  float64
}

// LLMResponse is a provider's reply.
type LLMResponse struct {
	Text       string
	TokensUsed int
}

// LLMProvider is the minimal LLM surface FromCode needs. The CLI passes
// an agentic.Provider — the agentic Provider interface satisfies this
// one structurally (Go's duck-typed interfaces let us cross the package
// boundary without depending on agentic).
type LLMProvider interface {
	Complete(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

// FromCodeOptions configures a single BRD regeneration. The provider
// produces the prose fields; the deterministic shape (provenance,
// status, implements_via, human_review_required) is set by this
// package — never by the model — so constraints and approvals can't
// be silently fabricated.
type FromCodeOptions struct {
	Provider     LLMProvider
	SystemPrompt string // typically ToneContract(cfg) + the role contract below
	MaxTokens    int
	Model        string // recorded in provenance.model for audit
}

// FromCodePrompt is the role contract appended to the operator's tone
// contract. It is intentionally restrictive about what the model may
// invent — `constraints` (regulatory / legal) are the highest-risk
// field to fabricate, and the validator's BRD_UNAPPROVED_LLM rule
// keeps drafts honest until a human signs off.
const FromCodePrompt = `You are reverse-engineering a Business Requirements Document from
the technical evidence of a software feature. The user will hand you:
- the feature manifest (purpose, capabilities, invariants, owners),
- the entry points that reach this feature (HTTP routes, CLI commands,
  cron, queue),
- the rules and invariants the feature declares.

You MUST produce JSON of the shape:
{
  "title":            "<short, business-readable title>",
  "business_problem": "<2-4 sentence prose: what user/business problem
                       this feature exists to solve>",
  "business_goals":   ["<outcome 1>", "<outcome 2>", ...],
  "stakeholders": {
    "business_owner":    "<infer from owners.business if available>",
    "product_owner":     "",
    "engineering_owner": "<infer from owners.engineering>",
    "legal":             "",
    "compliance":        ""
  },
  "user_scenarios": [
    {"id":"US-1", "actor":"<role>", "narrative":"<one paragraph>"},
    ...
  ],
  "success_criteria": [
    {"id":"SC-1", "statement":"<measurable>", "maps_to_invariant":"<feature_id:INV-N or empty>"}
  ]
}

You MUST NOT invent constraints (regulatory, legal, financial). Leave
that array empty — a human will fill it during review.

You MUST NOT invent stakeholders that aren't in the manifest. Leave a
slot empty rather than guess a name or team.

You MUST treat existing invariants as canonical truth: every success_criterion
that has a "maps_to_invariant" must name an invariant that exists in the
manifest. Format: "<feature_id>:<invariant_id>".

Output JSON only. No prose around it. No markdown fences.`

// FromCode regenerates a BRD draft from a feature manifest and the
// entry points that reach it. Returns the BRD draft and the LLM
// response text (for debugging); the caller is responsible for
// persisting via SaveForce.
//
// The returned BRD always has:
//   - id        = "brd." + featureID
//   - status    = draft
//   - provenance.source = llm_from_code
//   - human_review_required = true
//   - implements_via = [featureID]
//
// These fields are NOT taken from the model — they are set here so a
// rogue model can't promote itself to "approved" or attach to a
// different feature.
func FromCode(ctx context.Context, feature schema.Manifest, reachedBy []schema.EntryPoint, opts FromCodeOptions) (schema.BRD, string, error) {
	if opts.Provider == nil {
		return schema.BRD{}, "", ErrNoLLM
	}

	sysPrompt := opts.SystemPrompt
	if !strings.Contains(sysPrompt, "JSON") {
		// The caller passed only a tone contract; append the role contract
		// so the model knows what shape we want.
		sysPrompt = strings.TrimRight(sysPrompt, "\n ") + "\n\n" + FromCodePrompt
	}

	prompt := buildFromCodePrompt(feature, reachedBy)
	resp, err := opts.Provider.Complete(ctx, LLMRequest{
		SystemPrompt: sysPrompt,
		UserMessage:  prompt,
		MaxTokens:    opts.MaxTokens,
	})
	if err != nil {
		return schema.BRD{}, "", err
	}

	parsed, err := parseFromCodeResponse(resp.Text)
	if err != nil {
		return schema.BRD{}, resp.Text, fmt.Errorf("LLM produced unparseable JSON: %w", err)
	}

	// Build the BRD. Deterministic fields first; LLM-supplied prose
	// fields last. The model's `stakeholders` is merged into a
	// pre-seeded struct that uses Owners as fallback, so an LLM that
	// returns "" for an owner doesn't blow away the real owner name.
	id := "brd." + feature.ID
	stakeholders := schema.BRDStakeholders{
		BusinessOwner:    coalesce(parsed.Stakeholders.BusinessOwner, feature.Owners.Business),
		ProductOwner:     parsed.Stakeholders.ProductOwner,
		EngineeringOwner: coalesce(parsed.Stakeholders.EngineeringOwner, feature.Owners.Engineering),
		Legal:            parsed.Stakeholders.Legal,
		Compliance:       parsed.Stakeholders.Compliance,
	}

	// Filter success criteria: the model is told to use existing
	// invariants. Drop any maps_to_invariant that points at an
	// unknown invariant of this feature — silently rather than
	// erroring, since the rest of the criterion is still useful.
	knownInvariants := map[string]bool{}
	for _, inv := range feature.Invariants {
		knownInvariants[feature.ID+":"+inv.ID] = true
	}
	criteria := make([]schema.BRDCriterion, 0, len(parsed.SuccessCriteria))
	for _, c := range parsed.SuccessCriteria {
		sc := c.toSchema()
		if sc.MapsToInvariant != "" && !knownInvariants[sc.MapsToInvariant] {
			sc.MapsToInvariant = "" // drop the bogus reference, keep the statement
		}
		criteria = append(criteria, sc)
	}

	// User scenarios: straight pass-through (no fields we need to
	// validate against the manifest yet).
	scenarios := make([]schema.UserScenario, 0, len(parsed.UserScenarios))
	for _, s := range parsed.UserScenarios {
		scenarios = append(scenarios, s.toSchema())
	}

	b := schema.BRD{
		ID:              id,
		Version:         1,
		Status:          schema.BRDDraft,
		Title:           orDefault(parsed.Title, feature.Purpose),
		BusinessProblem: parsed.BusinessProblem,
		BusinessGoals:   parsed.BusinessGoals,
		Stakeholders:    stakeholders,
		UserScenarios:   scenarios,
		SuccessCriteria: criteria,
		// Constraints intentionally NOT taken from the model. A human
		// fills these in during review (the prompt forbids invention).
		Constraints:         nil,
		OutOfScope:          nil,
		ImplementsVia:       []string{feature.ID},
		HumanReviewRequired: true,
		Provenance: schema.BRDProvenance{
			Source:      schema.BRDSourceLLMFromCode,
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			Model:       opts.Model,
		},
	}
	return b, resp.Text, nil
}

// llmFromCodeResponse mirrors the JSON shape FromCodePrompt asks the
// model to produce. We define a JSON-tagged local mirror of the schema
// types (rather than reuse schema.BRDCriterion etc directly) because
// those schema types carry YAML tags only — they're canonical on
// disk, not over the wire. Mixing concerns would force JSON tags onto
// every schema struct.
type llmFromCodeResponse struct {
	Title           string             `json:"title"`
	BusinessProblem string             `json:"business_problem"`
	BusinessGoals   []string           `json:"business_goals"`
	Stakeholders    llmStakeholders    `json:"stakeholders"`
	UserScenarios   []llmUserScenario  `json:"user_scenarios"`
	SuccessCriteria []llmBRDCriterion  `json:"success_criteria"`
}

type llmStakeholders struct {
	BusinessOwner    string `json:"business_owner"`
	ProductOwner     string `json:"product_owner"`
	EngineeringOwner string `json:"engineering_owner"`
	Legal            string `json:"legal"`
	Compliance       string `json:"compliance"`
}

type llmUserScenario struct {
	ID        string `json:"id"`
	Actor     string `json:"actor"`
	Narrative string `json:"narrative"`
}

func (u llmUserScenario) toSchema() schema.UserScenario {
	return schema.UserScenario{ID: u.ID, Actor: u.Actor, Narrative: u.Narrative}
}

type llmBRDCriterion struct {
	ID              string `json:"id"`
	Statement       string `json:"statement"`
	MapsToInvariant string `json:"maps_to_invariant"`
}

func (c llmBRDCriterion) toSchema() schema.BRDCriterion {
	return schema.BRDCriterion{ID: c.ID, Statement: c.Statement, MapsToInvariant: c.MapsToInvariant}
}

func parseFromCodeResponse(text string) (llmFromCodeResponse, error) {
	var r llmFromCodeResponse
	body := extractJSON(text)
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return r, err
	}
	return r, nil
}

// buildFromCodePrompt assembles the evidence packet the model sees.
// We deliberately keep this short — adding more code context tends to
// push the model toward inventing capabilities the feature does not
// actually have.
func buildFromCodePrompt(f schema.Manifest, eps []schema.EntryPoint) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Feature id: %s\n", f.ID)
	fmt.Fprintf(&b, "Feature purpose (one-liner authored by an engineer): %s\n", f.Purpose)
	if f.Owners.Business != "" || f.Owners.Engineering != "" {
		fmt.Fprintf(&b, "Owners — business: %s, engineering: %s\n",
			coalesce(f.Owners.Business, "(unknown)"),
			coalesce(f.Owners.Engineering, "(unknown)"))
	}
	if len(f.Capabilities) > 0 {
		b.WriteString("\nCapabilities:\n")
		for _, c := range f.Capabilities {
			fmt.Fprintf(&b, "  - %s: %s\n", c.ID, c.Summary)
			for _, rule := range c.Rules {
				fmt.Fprintf(&b, "      rule: %s\n", rule)
			}
		}
	}
	if len(f.Invariants) > 0 {
		b.WriteString("\nInvariants (use these ids in success_criteria.maps_to_invariant):\n")
		for _, inv := range f.Invariants {
			fmt.Fprintf(&b, "  - %s:%s — %s\n", f.ID, inv.ID, inv.Statement)
		}
	}
	if len(eps) > 0 {
		b.WriteString("\nEntry points reaching this feature (use for user_scenarios):\n")
		for _, ep := range eps {
			fmt.Fprintf(&b, "  - %s %s\n", ep.Kind, triggerSummary(ep))
			if ep.Purpose != "" {
				fmt.Fprintf(&b, "      purpose: %s\n", ep.Purpose)
			}
		}
	}
	b.WriteString("\nProduce the BRD JSON. Only fields described in the system prompt. JSON only.")
	return b.String()
}

func triggerSummary(ep schema.EntryPoint) string {
	switch ep.Kind {
	case schema.EntryPointKindHTTP:
		return ep.Trigger.Method + " " + ep.Trigger.Path
	case schema.EntryPointKindCLI:
		return "command " + ep.Trigger.Command
	case schema.EntryPointKindCron:
		return "cron " + ep.Trigger.Schedule
	case schema.EntryPointKindQueue:
		return "queue " + ep.Trigger.Queue
	case schema.EntryPointKindEventConsumer:
		return "event " + ep.Trigger.Event
	}
	return ep.ID
}

// extractJSON strips fences and prose around the JSON body. Same
// contract as agentic.extractJSON and entrypoints.extractJSON; lives
// here so the brd package stays self-contained.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return s
}

func coalesce(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func orDefault(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
