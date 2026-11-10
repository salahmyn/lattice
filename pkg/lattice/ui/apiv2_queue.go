package ui

// @module-feature api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/contract"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// queueEntry is an item and the sort keys behind its rank.
type queueEntry struct {
	item  contract.QueueItem
	since time.Time // zero when the item has no age source
}

// v2Queue lists what needs a human, ranked: by tier (highest first), then by
// blast radius (entry points, then promises), then by age (oldest first). The
// rank is the order of this list; a reason says why each item is on it.
func (s *Server) v2Queue(wr http.ResponseWriter, r *http.Request) {
	w := s.loadOr500(wr, r)
	if w == nil {
		return
	}
	var entries []queueEntry

	// BRD versions awaiting a human: draft, proposed (a council-reviewed one
	// that is not acked among them), and drifted.
	for _, b := range w.kg.BRDs {
		state, chips, _ := w.brdStanding(b)
		var kind string
		var reason contract.Reason
		switch state {
		case contract.BRDDrifted:
			kind = contract.QueueBRDDrifted
			_, detail, _ := audit.BRDStanding(b, w.cd)
			reason = contract.Reason{Code: "BRD_DRIFTED", Text: detail}
		case contract.BRDProposed, contract.BRDDraft:
			kind = contract.QueueBRDAwaiting
			reason = w.awaitingReason(b, state, chips)
		default:
			continue
		}
		refs := confirm.PromisesOf(b)
		it := contract.QueueItem{
			ID: kind + ":" + b.ID + "@" + strconv.Itoa(b.Version), Kind: kind, Subject: b.ID, Version: b.Version,
			Tier: tierMaxOf(b), State: state, Chips: chips, Reason: reason, Link: w.brdLink(b),
			BlastRadius: contract.BlastRadius{EntryPoints: len(w.entryPointsOf(w.featuresOf(b))), Promises: len(refs)},
		}
		age, since := w.ageOf(b)
		it.Age = age
		entries = append(entries, queueEntry{item: it, since: since})
	}

	// Promises the audit reads as fakes a second reader confirmed, and tier-3
	// promises whose evidence is unknown (a partial run is a kind of unknown).
	for _, row := range w.report.Rows {
		var kind string
		var reason contract.Reason
		switch {
		case row.Class == audit.ClassFake && confirmedFake(row):
			kind = contract.QueueConfirmedFake
			n := fakeNote(row)
			reason = contract.Reason{Code: "FAKE_CONFIRMED", Params: map[string]interface{}{"reader": n.Reader, "kind": row.Kind},
				Text: "a second reader (" + n.Reader + ") confirmed it is a fake: " + row.Reason}
		case row.Class == audit.ClassUnknown && row.Tier >= 3:
			kind = contract.QueuePromiseUnknown
			reason = contract.Reason{Code: "PROMISE_UNKNOWN_T3", Params: map[string]interface{}{"kind": row.Kind},
				Text: "a tier-3 promise whose evidence is unknown: " + row.Reason}
		default:
			continue
		}
		entries = append(entries, queueEntry{item: contract.QueueItem{
			ID: kind + ":" + row.Promise, Kind: kind, Subject: row.Promise, Tier: row.Tier, Reason: reason,
			Link:        "/features?f=" + row.Feature,
			BlastRadius: contract.BlastRadius{EntryPoints: len(w.entryPointsOf([]string{row.Feature})), Promises: 1},
			Age:         contract.NotComputed("the age of a finding"),
		}})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		switch {
		case a.item.Tier != b.item.Tier:
			return a.item.Tier > b.item.Tier
		case a.item.BlastRadius.EntryPoints != b.item.BlastRadius.EntryPoints:
			return a.item.BlastRadius.EntryPoints > b.item.BlastRadius.EntryPoints
		case a.item.BlastRadius.Promises != b.item.BlastRadius.Promises:
			return a.item.BlastRadius.Promises > b.item.BlastRadius.Promises
		case a.since.IsZero() != b.since.IsZero():
			return !a.since.IsZero() // an item with an age outranks one with none
		case !a.since.Equal(b.since):
			return a.since.Before(b.since) // waiting longest first
		}
		return a.item.ID < b.item.ID
	})
	out := contract.Queue{Header: w.header(contract.QueueV1), Items: []contract.QueueItem{},
		Proposals: contract.NotComputed("open proposals (none are stored)")}
	for i, e := range entries {
		e.item.Rank = i + 1
		out.Items = append(out.Items, e.item)
	}
	writeV2(wr, http.StatusOK, out, contract.LintOptions{})
}

func (w *v2world) awaitingReason(b schema.BRD, state string, chips []contract.Chip) contract.Reason {
	for _, c := range chips {
		if c.Code == "REVIEWED_BY_COUNCIL" {
			return contract.Reason{Code: "COUNCIL_AWAITING_ACK", Params: c.Params, Text: "reviewed by the council; a human has not acked it"}
		}
	}
	if state == contract.BRDDraft {
		return contract.Reason{Code: "BRD_DRAFT", Text: "a draft: no human has confirmed any version"}
	}
	return contract.Reason{Code: "BRD_PROPOSED", Text: "proposed: awaiting a human's confirmation"}
}

func confirmedFake(row audit.Row) bool {
	return fakeNote(row).Verdict == audit.VerdictConfirmedFake
}

func fakeNote(row audit.Row) audit.Note {
	for _, n := range row.Notes {
		if n.Verdict == audit.VerdictConfirmedFake {
			return n
		}
	}
	return audit.Note{}
}

// ageOf says since when a BRD version has waited, from the earliest stamp its
// own record carries: when it was generated, when a council voted. With none, the file's modification time, a weaker proxy. With no
// file either, there is no age.
func (w *v2world) ageOf(b schema.BRD) (contract.Assurance, time.Time) {
	var best time.Time
	var source string
	consider := func(ts, src string) {
		if ts == "" {
			return
		}
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return
		}
		if best.IsZero() || t.Before(best) {
			best, source = t, src
		}
	}
	consider(b.Provenance.GeneratedAt, "generated_at")
	if rec, ok := w.councilRecord(b); ok {
		for _, v := range rec.Votes {
			consider(v.At, "council_vote")
		}
	}
	if best.IsZero() && b.SourcePath != "" {
		for _, base := range []string{filepath.Dir(w.s.ws.LatticeDir), w.s.ws.LatticeDir} {
			if fi, err := os.Stat(filepath.Join(base, filepath.FromSlash(b.SourcePath))); err == nil {
				best, source = fi.ModTime().UTC(), "file_mtime"
				break
			}
		}
	}
	if best.IsZero() {
		return contract.NotComputed("the age of a BRD version"), time.Time{}
	}
	return contract.Assurance{State: contract.StateInfo, Honesty: contract.Proxy, Code: "WAITING_SINCE",
		Params: map[string]interface{}{"since": best.UTC().Format(time.RFC3339), "source": source},
		Text:   "waiting since " + best.UTC().Format(time.RFC3339) + " (" + source + ")"}, best
}
