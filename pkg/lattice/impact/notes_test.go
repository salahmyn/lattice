package impact

import (
	"bytes"
	"strings"
	"testing"
)

const feesFeature = `id: fees
version: 1
status: production
purpose: Charges a fee on each payment.
owners:
  business: payments
  engineering: payments-eng
invariants:
  - id: INV-1
    statement: A fee never exceeds the cap.
  - id: INV-2
    statement: A fee is never negative.
`

const feesBRD = `id: brd.fees
version: 1
status: approved
title: Fair fees
business_problem: Customers pay a fair fee.
success_criteria:
  - id: SC-1
    statement: The fee is capped.
    maps_to_invariant: fees:INV-1
    tier: 2
implements_via:
  - fees
`

const feesCode = `package fees

// @module-feature fees

// Cap caps the fee.
// @enforces fees:INV-1
func Cap(fee int) int {
	if fee > 100 {
		return 100
	}
	return fee
}
`

const feesTest = `package fees

import "testing"

// @verifies fees:INV-1
func TestCap(t *testing.T) {
	if Cap(500) != 100 {
		t.Fatal("cap")
	}
}
`

// feesRepo commits the fees fixture and branches off it.
func feesRepo(t *testing.T) (*repo, string) {
	r := newRepo(t)
	r.write("lattice/features/fees.yaml", feesFeature)
	r.write("lattice/brds/brd.fees.yaml", feesBRD)
	r.write("fees/fees.go", feesCode)
	r.write("fees/fees_test.go", feesTest)
	base := r.commit("base")
	r.git("checkout", "-q", "-b", "change")
	return r, base
}

func editedCap() string {
	return strings.Replace(feesCode, "fee > 100", "fee >= 100", 1)
}

func notesOf(rep Report, promise string) []Note {
	var out []Note
	for _, n := range rep.Notes {
		if n.Promise == promise {
			out = append(out, n)
		}
	}
	return out
}

// An enforcer body edited under an unchanged statement and tier gets one
// STATEMENT_UNCHANGED note, shown in every form.
// @verifies impact:INV-5
func TestStatementUnchangedNote(t *testing.T) {
	r, base := feesRepo(t)
	r.write("fees/fees.go", editedCap())
	rep := r.report(r.input(base))
	notes := notesOf(rep, "fees:INV-1")
	if len(notes) != 1 || notes[0].Code != NoteStatementUnchanged {
		t.Fatalf("notes = %+v", rep.Notes)
	}
	n := notes[0]
	if !strings.Contains(n.Text, "the enforcer of fees:INV-1 changed and its statement did not") ||
		n.Next == "" || n.Statement != "A fee never exceeds the cap." || len(n.Enforcers) != 1 || n.Enforcers[0] != "fees.Cap" {
		t.Fatalf("note = %+v", n)
	}
	var text, md bytes.Buffer
	Text(&text, rep)
	Markdown(&md, rep)
	for name, out := range map[string]string{"text": text.String(), "markdown": md.String()} {
		if !strings.Contains(out, NoteStatementUnchanged) || !strings.Contains(out, "re-read the statement") {
			t.Errorf("%s form lacks the note:\n%s", name, out)
		}
	}
	js, err := rep.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `"notes":[{"code":"STATEMENT_UNCHANGED","promise":"fees:INV-1"`) {
		t.Errorf("json lacks the note: %s", js)
	}
}

// When the statement changes with its enforcer, or the tier does, there is
// no note: other rules read the edited promise.
// @verifies impact:INV-5
func TestNoNoteWhenPromiseEdited(t *testing.T) {
	t.Run("statement", func(t *testing.T) {
		r, base := feesRepo(t)
		r.write("fees/fees.go", editedCap())
		r.write("lattice/features/fees.yaml", strings.Replace(feesFeature, "never exceeds the cap", "stays at or below the cap", 1))
		if rep := r.report(r.input(base)); len(rep.Notes) != 0 {
			t.Fatalf("notes = %+v", rep.Notes)
		}
	})
	t.Run("tier", func(t *testing.T) {
		r, base := feesRepo(t)
		r.write("fees/fees.go", editedCap())
		r.write("lattice/brds/brd.fees.yaml", strings.Replace(feesBRD, "tier: 2", "tier: 3", 1))
		if rep := r.report(r.input(base)); len(rep.Notes) != 0 {
			t.Fatalf("notes = %+v", rep.Notes)
		}
	})
}

// Only a verifier changed, or the promise is new: no note.
// @verifies impact:INV-5
func TestNoNoteForVerifierOrNewPromise(t *testing.T) {
	t.Run("verifier", func(t *testing.T) {
		r, base := feesRepo(t)
		r.write("fees/fees_test.go", strings.Replace(feesTest, "Cap(500)", "Cap(900)", 1))
		rep := r.report(r.input(base))
		if len(rep.ChangedSymbols) == 0 {
			t.Fatal("the verifier edit is not a changed symbol")
		}
		if len(rep.Notes) != 0 {
			t.Fatalf("notes = %+v", rep.Notes)
		}
	})
	t.Run("new promise", func(t *testing.T) {
		r, base := feesRepo(t)
		r.write("lattice/features/fees.yaml", feesFeature+"  - id: INV-3\n    statement: A fee is rounded to cents.\n")
		r.write("fees/round.go", "package fees\n\n// @module-feature fees\n\n// Round rounds a fee.\n// @enforces fees:INV-3\nfunc Round(fee int) int {\n\treturn fee\n}\n")
		rep := r.report(r.input(base))
		if _, ok := findSymbol(rep, "fees.Round"); !ok {
			t.Fatalf("the new enforcer is not a changed symbol: %+v", rep.ChangedSymbols)
		}
		if len(rep.Notes) != 0 {
			t.Fatalf("notes = %+v", rep.Notes)
		}
	})
}
