package check

// @module-feature check

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CodePassesOnBase is an informational note: a new or changed verifier of
// the promise passes on the base commit's code too, so it does not tell the
// old behaviour from the new (lattice break --fails-on-base).
const CodePassesOnBase = "PASSES_ON_BASE"

// failsOnBaseSchema is the contract of lattice/.cache/results/fails-on-base.json,
// written by `lattice break --fails-on-base`. The check reads it by this
// contract and does not import the package that writes it.
const failsOnBaseSchema = "lattice.fails_on_base/1"

// BaseProof is what the check reads of fails-on-base.json.
type BaseProof struct {
	Schema    string          `json:"schema"`
	BaseRev   string          `json:"base_rev"`
	Base      string          `json:"base"`
	Head      string          `json:"head"`
	CIRun     string          `json:"ci_run"`
	Verifiers []BaseProofTest `json:"verifiers"`
}

// BaseProofTest is one verifier of BaseProof.
type BaseProofTest struct {
	Verifier string   `json:"verifier"`
	Promises []string `json:"promises"`
	Outcome  string   `json:"outcome"`
	Reason   string   `json:"reason"`
}

// FailsOnBaseEvidence says whether a fails-on-base result was read for this
// change. It is information only: it feeds notes, never a gap, the verdict
// or merge_allowed.
type FailsOnBaseEvidence struct {
	Present bool   `json:"present"`
	Base    string `json:"base,omitempty"`
	Commit  string `json:"commit,omitempty"`
	CIRun   string `json:"ci_run,omitempty"`
	// Counted is true when the file is for this change: its base is the
	// merge base and its head is HEAD (under --ci, from a CI run too).
	Counted bool   `json:"counted"`
	Text    string `json:"text,omitempty"`
}

// LoadBaseProof reads fails-on-base.json; nil when there is none or it does
// not hold the contract.
func LoadBaseProof(latticeDir string) *BaseProof {
	data, err := os.ReadFile(filepath.Join(latticeDir, ".cache", "results", "fails-on-base.json"))
	if err != nil {
		return nil
	}
	var p BaseProof
	if json.Unmarshal(data, &p) != nil || p.Schema != failsOnBaseSchema {
		return nil
	}
	return &p
}

// baseProof judges the fails-on-base file against this change and, when it
// counts, turns each passes_on_base verifier into a note on its promises.
// A file for another base or head says nothing about this change and is set
// aside. The notes ride on the promise rows only (check:INV-11).
// @enforces check:INV-11
func (c *computation) baseProof() (FailsOnBaseEvidence, map[string][]Note) {
	p := c.in.FailsOnBase
	if p == nil {
		return FailsOnBaseEvidence{}, nil
	}
	ev := FailsOnBaseEvidence{Present: true, Base: p.Base, Commit: p.Head, CIRun: p.CIRun}
	switch {
	case p.Base != c.in.Impact.Base || p.Head != c.in.Impact.Head:
		ev.Text = fmt.Sprintf("fails-on-base.json is for %s..%s, not this change: re-run lattice break --fails-on-base", short(p.Base), short(p.Head))
		return ev, nil
	case c.in.CI && (p.CIRun == "" || p.CIRun == "local"):
		ev.Text = "fails-on-base.json is from a local run; --ci counts only a CI run"
		return ev, nil
	}
	ev.Counted = true
	rev := p.BaseRev
	if rev == "" {
		rev = short(p.Base)
	}
	notes := map[string][]Note{}
	vs := append([]BaseProofTest(nil), p.Verifiers...)
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].Verifier < vs[j].Verifier })
	for _, v := range vs {
		if v.Outcome != "passes_on_base" {
			continue
		}
		for _, promise := range v.Promises {
			notes[promise] = append(notes[promise], Note{Code: CodePassesOnBase,
				Text: fmt.Sprintf("verifier %s of %s passes on the base code too (%s): it does not tell the old behaviour from the new",
					v.Verifier, promise, short(p.Base)),
				Next: fmt.Sprintf("make %s assert the behaviour this change fixes, so it fails on %s, then re-run lattice break --fails-on-base %s",
					v.Verifier, short(p.Base), shellWord(rev)),
			})
		}
	}
	return ev, notes
}

// shellWord quotes a rev for a command line only when it needs it.
func shellWord(s string) string {
	if strings.ContainsAny(s, " \t'\"$`\\") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}
