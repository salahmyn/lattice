package agentic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// scripted is a provider that records the prompt and returns one reply.
type scripted struct {
	reply  string
	prompt string
}

func (s *scripted) Name() string { return "scripted" }
func (s *scripted) Complete(_ context.Context, req CompletionRequest) (CompletionResponse, error) {
	s.prompt = req.UserMessage
	return CompletionResponse{Text: s.reply}, nil
}

// A model may draft cases under an invariant, and the prompt says how. What
// comes back is the manifest in canonical form: the cases survive, and a
// confirmation or coverage the model typed onto a case is dropped, so
// nothing machine-written claims anything is confirmed.
// @verifies agentic:INV-2
func TestDraftedCasesCarryNoConfirmation(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lattice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lattice", "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &scripted{reply: "```yaml\n" + `id: wallet.fees
version: 1
status: proposal
purpose: Charges withdrawal fees.
owners: {business: payments, engineering: wallet}
invariants:
  - id: INV-1
    statement: A fee never exceeds five percent of the amount.
    confirmed: true
    cases:
      - {id: a, kind: happy, statement: A 100.00 withdrawal pays at most 5.00., confirmed: true, coverage: covered}
      - {id: b, kind: edge, statement: A 0.01 withdrawal pays no fee.}
` + "```\nOPEN QUESTIONS:\n- Is the cap per withdrawal or per day?\n"}
	res, err := New(ws, config.Default()).WithProvider(p).DraftProposal(context.Background(), "cap the withdrawal fee", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeLLM || !res.SchemaValid {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(p.prompt, "cases") || !strings.Contains(p.prompt, "Never mark a case or\n    an invariant confirmed") {
		t.Errorf("the prompt should say how to draft cases and forbid confirming them:\n%s", p.prompt)
	}
	for _, typed := range []string{"confirmed", "coverage"} {
		if strings.Contains(res.ManifestYAML, typed) {
			t.Errorf("a typed %s survived the draft:\n%s", typed, res.ManifestYAML)
		}
	}
	var m schema.Manifest
	if err := yaml.Unmarshal([]byte(res.ManifestYAML), &m); err != nil {
		t.Fatal(err)
	}
	if cs := m.Invariants[0].Cases; len(cs) != 2 || cs[0].Statement != "A 100.00 withdrawal pays at most 5.00." || cs[1].Kind != schema.CaseEdge {
		t.Errorf("drafted cases = %+v", cs)
	}
	if again, err := schema.Canonicalize([]byte(res.ManifestYAML), &schema.Manifest{}); err != nil || again.Changed || again.Skipped != "" {
		t.Errorf("the draft is not canonical: %+v, %v", again, err)
	}
	if len(res.OpenQuestions) != 1 {
		t.Errorf("open questions = %v", res.OpenQuestions)
	}
}
