package ui

// @module-feature api

import (
	"net/http"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/buildinfo"
	"github.com/salahmyn/lattice/pkg/lattice/check"
	"github.com/salahmyn/lattice/pkg/lattice/contract"
	"github.com/salahmyn/lattice/pkg/lattice/results"
)

// stampOf reads one evidence file's provenance as the check does
// (check.Gather): the tie is a pass only when the file is stamped with the
// head commit and a CI run.
func (w *v2world) stampOf(it check.EvidenceItem) contract.Stamp {
	st := contract.Stamp{Present: it.Present, Commit: it.Commit}
	if it.CIRun != results.LocalRun {
		st.CIRun = it.CIRun
	}
	switch {
	case !it.Present:
		st.Tie = contract.Assurance{State: contract.StateUnknown, Honesty: contract.Fact, Code: "NO_FILE", Text: "no file: nothing to tie to the head commit"}
	case it.Tied:
		st.Tie = contract.Assurance{State: contract.StatePass, Honesty: contract.Fact, Text: "stamped with the head commit and a CI run"}
	case it.CIRun == "" || it.CIRun == results.LocalRun:
		st.Tie = contract.Assurance{State: contract.StateUnknown, Honesty: contract.Fact, Code: "NOT_CI_STAMPED", Text: "produced outside CI: local results are advice only"}
	default:
		st.Tie = contract.Assurance{State: contract.StateUnknown, Honesty: contract.Fact, Code: "COMMIT_DIFFERS",
			Text: "stamped with " + it.Commit + ", not the head " + w.head}
	}
	return st
}

// noCIField is the stamp of a file that has no ci_run field.
func noCIField(present bool, commit, file string) contract.Stamp {
	st := contract.Stamp{Present: present, Commit: commit}
	if !present {
		st.Tie = contract.Assurance{State: contract.StateUnknown, Honesty: contract.Fact, Code: "NO_FILE", Text: "no " + file}
		return st
	}
	st.Tie = contract.Assurance{State: contract.StateUnknown, Honesty: contract.Fact, Code: contract.CodeNotComputed,
		Text: file + " carries no ci_run, so the kernel cannot tie it to a CI run"}
	return st
}

func (w *v2world) provenance() contract.Provenance {
	p := contract.Provenance{
		Results: w.stampOf(w.ev.Results), Coverage: w.stampOf(w.ev.Coverage), Breaks: w.stampOf(w.ev.Breaks),
	}
	snap, _ := check.LoadSnapshot(w.s.ws.LatticeDir, w.head)
	if snap != nil {
		p.AuditSnapshot = noCIField(true, snap.Commit, "audit snapshot")
	} else {
		p.AuditSnapshot = noCIField(false, "", "audit snapshot for the head commit")
	}
	if w.cd != nil {
		p.Confirm = noCIField(true, w.cd.Commit, "confirm.json")
	} else {
		p.Confirm = noCIField(false, "", "confirm.json")
	}
	return p
}

// v2Meta says what built the payloads and what they stand on. It also issues
// the CSRF token prepare needs, to loopback clients only.
func (s *Server) v2Meta(wr http.ResponseWriter, r *http.Request) {
	w := s.loadOr500(wr, r)
	if w == nil {
		return
	}
	m := contract.Meta{
		Header:   w.header(contract.MetaV1),
		Kernel:   contract.Kernel{Version: buildinfo.Version, Commit: buildinfo.Commit, Date: buildinfo.Date},
		Dirty:    w.dirty,
		Evidence: w.provenance(),
		Prepare:  contract.PrepareInfo{Header: csrfHeader},
	}
	if loopbackRequest(r) && s.csrf != "" {
		m.Prepare.Enabled, m.Prepare.CSRFToken = true, s.csrf
	} else {
		m.Prepare.Header = ""
		m.Prepare.Reason = "prepare is open only to a client on this machine, with the token /api/v2/meta issues to it"
	}
	writeV2(wr, http.StatusOK, m, contract.LintOptions{})
}

// --- GET /api/v2/health ---

// cell shapes one evidence cell: how many of the feature's things have the
// evidence, from which commit, and whether CI stamped it. A cell is never a
// pass: a file being there says nothing about whether a promise is kept.
func (w *v2world) cell(have, of int, noneCode, noneText string, it check.EvidenceItem, hasCI bool) contract.Cell {
	c := contract.Cell{Honesty: contract.Fact, Commit: it.Commit}
	if hasCI {
		stamped := it.Present && it.CIRun != "" && it.CIRun != results.LocalRun
		c.CIStamped = &stamped
	}
	params := map[string]interface{}{"have": have, "of": of}
	switch {
	case of == 0:
		c.State, c.Code, c.Text = contract.StateAbsent, noneCode, noneText
	case have == 0:
		c.State, c.Code, c.Params, c.Text = contract.StateAbsent, "NO_EVIDENCE", params, "none of the evidence is there"
	case have < of:
		c.State, c.Code, c.Params, c.Text = contract.StatePartial, "PARTIAL", params, "some of the evidence is there"
	default:
		c.State, c.Params, c.Text = contract.StatePresent, params, "all of the evidence is there"
	}
	if have > 0 && it.Commit != "" && w.head != "" && it.Commit != w.head {
		c.State, c.Code, c.Text = contract.StateStale, "COMMIT_DIFFERS", "stamped with "+it.Commit+", not the head "+w.head
	}
	return c
}

func (s *Server) v2Health(wr http.ResponseWriter, r *http.Request) {
	w := s.loadOr500(wr, r)
	if w == nil {
		return
	}
	out := contract.Health{
		Header:        w.header(contract.HealthV1),
		Columns:       []string{"results", "coverage", "breaks", "confirm"},
		Rows:          []contract.HealthRow{},
		Waivers:       []contract.Waiver{},
		WaiverTrend:   contract.NotComputed("the waiver trend"),
		IgnoredInputs: contract.NotComputed("ignored inputs"),
	}
	for _, x := range w.waivers {
		out.Waivers = append(out.Waivers, contract.Waiver{Subject: x.Subject, Kind: x.Kind, Code: x.Code, Reason: x.Reason})
	}
	confirmItem := check.EvidenceItem{}
	if w.cd != nil {
		confirmItem = check.EvidenceItem{Present: true, Commit: w.cd.Commit}
	}
	ids := make([]string, 0, len(w.kg.Features))
	for _, f := range w.kg.Features {
		ids = append(ids, f.ID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rows := w.featureRows(id)
		verifiers := map[string]audit.VerifierEvidence{}
		var promises, withBreaks, withConfirm int
		for _, row := range rows {
			promises++
			for _, v := range row.Evidence.Verifiers {
				verifiers[v.FQN] = v
			}
			if len(row.Evidence.Breaks) > 0 {
				withBreaks++
			}
			if row.Signals.Confirmed.Value == audit.ConfirmedHuman {
				withConfirm++
			}
		}
		var withResult, withPerTest int
		for _, v := range verifiers {
			if v.Result != "none" && v.Result != "" {
				withResult++
			}
			if v.Coverage == "per_test" {
				withPerTest++
			}
		}
		hr := contract.HealthRow{Feature: id, Promises: promises}
		hr.Results = w.cell(withResult, len(verifiers), "NO_VERIFIERS", "no verifier to have a result", w.ev.Results, true)
		hr.Coverage = w.cell(withPerTest, len(verifiers), "NO_VERIFIERS", "no verifier to have per-test coverage", w.ev.Coverage, true)
		hr.Breaks = w.cell(withBreaks, promises, "NO_PROMISES", "no promise to break-test", w.ev.Breaks, true)
		hr.Confirm = w.cell(withConfirm, promises, "NO_PROMISES", "no promise to confirm", confirmItem, false)
		if w.cd == nil {
			hr.Confirm.State, hr.Confirm.Code, hr.Confirm.Text = contract.StateAbsent, "NO_CONFIRM_DATA", "no confirm.json: run `lattice confirm` in CI"
			hr.Confirm.Params = nil
		}
		out.Rows = append(out.Rows, hr)
	}
	writeV2(wr, http.StatusOK, out, contract.LintOptions{})
}
