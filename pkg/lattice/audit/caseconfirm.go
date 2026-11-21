package audit

// @module-feature audit

import (
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// caseBasis is the decision a promise's cases are compared with under one
// BRD: the current version's confirmed decision, else its stale one (a
// human confirmed it, and the packet changed since), else the confirmed
// decision of the latest earlier version. why says why there is none. A
// return or overturn of the current version leaves none.
func caseBasis(b schema.BRD, promise string, cd *ConfirmData) (rec *ConfirmRecord, why string) {
	switch {
	case cd == nil:
		return nil, "no confirm data (run `lattice confirm` in CI)"
	case cd.SetAside != "":
		return nil, "confirm.json set aside (" + cd.SetAside + ")"
	}
	var current, stale, older *ConfirmRecord
	for i := range cd.Records {
		r := &cd.Records[i]
		if r.ID != b.ID {
			continue
		}
		if r.Version == b.Version && r.State == "confirmed" && (r.Action == "brd.return" || r.Action == "brd.overturn") {
			return nil, "v" + strconv.Itoa(b.Version) + " was " + strings.TrimPrefix(r.Action, "brd.") + "ed"
		}
		covers := r.Action == "brd.approve" || r.Action == "brd.ack" || (r.Action == "promise.confirm" && hasString(r.Rows, promise))
		switch {
		case !covers:
		case r.Version == b.Version && r.State == "confirmed":
			current = r
		case r.Version == b.Version && r.State == "stale" && stale == nil:
			stale = r
		case r.Version < b.Version && r.State == "confirmed" && (older == nil || r.Version > older.Version):
			older = r
		}
	}
	switch {
	case current != nil:
		return current, ""
	case stale != nil:
		return stale, ""
	case older != nil:
		return older, ""
	}
	return nil, "no human confirmation of " + b.ID + " covers this promise"
}

// caseConfirmations gives each case of a promise its own confirmation state
// (rnd/0010 §B5). Under every BRD behind the promise it compares the case
// with the one the confirming decision recorded: the same id and hash read
// confirmed; a case the decision did not hold reads unconfirmed, added after
// confirmation; a changed kind, statement or enforcer reads edited after
// confirmation; a newly retired case reads retired after confirmation; a
// decision prepared before decisions recorded their cases leaves every case
// unconfirmed with that reason; and no confirmation at all leaves them
// unconfirmed. The least confirmed BRD decides. The promise's own confirmed
// signal is untouched: it still reads the packet as a whole.
// @enforces confirm:INV-20
func caseConfirmations(rows []CaseRow, crit []criterionRef, promise string, cd *ConfirmData, promiseSignal Signal) {
	for i := range rows {
		rows[i].Confirmation = CaseConfirmation{}
	}
	if len(crit) == 0 {
		for i := range rows {
			rows[i].Confirmation = CaseConfirmation{State: CaseUnconfirmed, Code: CaseCodeNeverConfirmed,
				Reason: "no confirmation: no BRD criterion maps to this promise"}
		}
		return
	}
	local := ""
	if cd.Local() {
		local = " (local, not CI)"
	}
	for _, c := range crit {
		rec, why := caseBasis(c.brd, promise, cd)
		var stamps []schema.CaseStamp
		recorded := false
		if rec != nil {
			stamps, recorded = schema.CasesOf(rec.Cases, promise)
		}
		for i := range rows {
			if rows[i].Confirmation.State == CaseUnconfirmed {
				continue // an earlier BRD already leaves it unconfirmed
			}
			var cc CaseConfirmation
			switch {
			case rec == nil:
				cc = CaseConfirmation{State: CaseUnconfirmed, Code: CaseCodeNeverConfirmed, Reason: "no confirmation: " + why}
			case !recorded:
				cc = CaseConfirmation{State: CaseUnconfirmed, Code: CaseCodeNotRecorded,
					Reason: c.brd.ID + " v" + strconv.Itoa(rec.Version) + " was confirmed (" + rec.Method() + ") before decisions recorded their cases: prepare and confirm it again to record them"}
			default:
				cc = caseAgainst(rows[i], stamps, c.brd.ID, rec, local, promiseSignal)
			}
			if rows[i].Confirmation.State == "" || cc.State == CaseUnconfirmed {
				rows[i].Confirmation = cc
			}
		}
	}
}

// caseAgainst compares one case with the stamps a decision recorded.
func caseAgainst(row CaseRow, stamps []schema.CaseStamp, brdID string, rec *ConfirmRecord, local string, promiseSignal Signal) CaseConfirmation {
	at := brdID + " v" + strconv.Itoa(rec.Version) + ", " + rec.Method() + local
	cur := schema.NewCaseStamp(row.ID, schema.CaseKind(row.Kind), row.Statement, row.Enforcer, row.Retired)
	for _, s := range stamps {
		if s.ID != row.ID {
			continue
		}
		delta := s.Delta(cur)
		switch {
		case len(delta) == 0:
			reason := "as confirmed at " + at
			if promiseSignal.Value != ConfirmedHuman {
				reason += "; the promise reads " + promiseSignal.Value + ", so it needs confirming again as a whole"
			}
			return CaseConfirmation{State: CaseConfirmed, Code: CaseCodeConfirmed, Reason: reason}
		case len(delta) == 1 && delta[0] == "retired":
			return CaseConfirmation{State: CaseUnconfirmed, Code: CaseCodeRetired, Reason: "retired after confirmation (" + at + ")"}
		}
		return CaseConfirmation{State: CaseUnconfirmed, Code: CaseCodeEdited,
			Reason: "edited after confirmation: " + strings.Join(delta, ", ") + " (" + at + ")"}
	}
	return CaseConfirmation{State: CaseUnconfirmed, Code: CaseCodeAdded, Reason: "added after confirmation (" + at + " did not hold it)"}
}
