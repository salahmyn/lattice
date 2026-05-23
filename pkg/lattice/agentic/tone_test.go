package agentic

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

func TestToneContractEmptyByDefault(t *testing.T) {
	if got := ToneContract(config.Tone{}); got != "" {
		t.Errorf("zero-value tone should render empty, got %q", got)
	}
	if got := ToneContract(config.Tone{Audience: "mixed"}); got != "" {
		t.Errorf("explicit mixed audience should render empty, got %q", got)
	}
}

func TestToneContractBusinessAudience(t *testing.T) {
	c := ToneContract(config.Tone{
		Audience:     "business",
		ReadingLevel: "simple",
		AvoidJargon:  true,
		ExtraInstructions: "Always refer to the product as 'Lattice', never 'the tool'.",
	})
	for _, want := range []string{
		"Audience: business",
		"plain English",
		"9th-grade",
		"Avoid software jargon",
		"Lattice",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("expected %q in tone contract:\n%s", want, c)
		}
	}
}

func TestToneContractEngineeringAudience(t *testing.T) {
	c := ToneContract(config.Tone{Audience: "engineer", ReadingLevel: "expert"})
	if !strings.Contains(c, "senior engineers") {
		t.Errorf("engineer alias should normalise to senior-engineers prompt:\n%s", c)
	}
	if !strings.Contains(c, "domain expert") {
		t.Errorf("expert reading level missing:\n%s", c)
	}
}

func TestToneContractCustomAudience(t *testing.T) {
	c := ToneContract(config.Tone{Audience: "compliance officer"})
	if !strings.Contains(c, "compliance officer") {
		t.Errorf("custom audience should be echoed back:\n%s", c)
	}
}
