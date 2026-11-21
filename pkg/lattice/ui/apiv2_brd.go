package ui

// @module-feature api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/check"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/contract"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// --- BRD state ---

// brdStanding reads where a BRD version is in the UI plan's state machine,
// its sub-chips, and who confirmed which version. Everything comes from the
// BRD's own record and confirm.json (audit.BRDStanding); DRIFTED is computed,
// never stored. A typed approval is never CONFIRMED.
func (w *v2world) brdStanding(b schema.BRD) (state string, chips []contract.Chip, conf contract.Confirmation) {
	st, detail, approver := audit.BRDStanding(b, w.cd)
	_ = approver

	if b.Status == schema.BRDSuperseded {
		state = contract.BRDSuperseded
	}
	switch st {
	case audit.ConfirmedHuman:
		if state == "" {
			state = contract.BRDConfirmed
		}
		conf.State = contract.Assurance{State: contract.StatePass, Honesty: contract.Proxy, Text: detail}
	case audit.ConfirmedDrifted:
		if state == "" {
			state = contract.BRDDrifted
		}
		conf.State = contract.Assurance{State: contract.StateDrifted, Honesty: contract.Proxy, Code: "CONFIRMED_DRIFTED", Text: detail}
	case audit.ConfirmedAgent:
		if state == "" {
			state = contract.BRDProposed
		}
		n, of := w.councilTally(b)
		chips = append(chips, contract.Chip{Code: "REVIEWED_BY_COUNCIL", Params: map[string]interface{}{"approve": n, "seats": of},
			Text: "reviewed by council, awaiting you"})
		conf.State = contract.Assurance{State: contract.StateUnknown, Honesty: contract.Proxy, Code: "COUNCIL_AWAITING_HUMAN", Text: detail}
	case audit.ConfirmedPending:
		if state == "" {
			state = contract.BRDProposed
		}
		chips = append(chips, contract.Chip{Code: "PREPARED_AWAITING_MERGE", Text: "prepared, awaiting a human's merge"})
		conf.State = contract.Assurance{State: contract.StateUnknown, Honesty: contract.Proxy, Code: "PREPARED_NOT_CONFIRMED", Text: detail}
	case audit.ConfirmedUnknown:
		if state == "" {
			state = contract.BRDProposed
		}
		chips = append(chips, contract.Chip{Code: "APPROVAL_UNVERIFIED", Text: "an approval is recorded (display only); no platform record backs it"})
		conf.State = contract.Assurance{State: contract.StateUnknown, Honesty: contract.Proxy, Code: "CONFIRMED_UNKNOWN", Text: detail}
	default: // draft: not approved, returned or overturned
		if state == "" {
			state = contract.BRDDraft
			if b.Status == schema.BRDProposed {
				state = contract.BRDProposed
			}
		}
		conf.State = contract.Assurance{State: contract.StateFail, Honesty: contract.Proxy, Code: "CONFIRMED_DRAFT", Text: detail}
	}

	if rec := w.confirmedRecord(b.ID, b.Version); rec != nil {
		conf.ConfirmedVersion, conf.ConfirmedBy = rec.Version, rec.ApproverLogin
		conf.Via, conf.Method, conf.Fingerprint = via(*rec), rec.Route(), rec.Fingerprint
	}
	if o := w.overturn(b); o != "" {
		chips = append(chips, contract.Chip{Code: "COUNCIL_OVERTURNED", Params: map[string]interface{}{"by": o}, Text: "the council's approval of this version was overturned by " + o})
	}
	return state, chips, conf
}

// confirmedRecord is the latest platform-confirmed approval of a BRD at or
// before a version.
func (w *v2world) confirmedRecord(id string, upTo int) *audit.ConfirmRecord {
	if w.cd == nil {
		return nil
	}
	var best *audit.ConfirmRecord
	for i := range w.cd.Records {
		r := &w.cd.Records[i]
		if r.ID != id || r.State != "confirmed" || r.Version > upTo || (r.Action != "brd.approve" && r.Action != "brd.ack") {
			continue
		}
		if best == nil || r.Version > best.Version {
			best = r
		}
	}
	return best
}

func (w *v2world) councilRecord(b schema.BRD) (brd.CouncilRecord, bool) {
	rec, err := brd.LoadRecord(w.s.ws.LatticeDir, b.ID, b.Version)
	if err != nil {
		return rec, false
	}
	return rec, len(rec.Votes) > 0 || rec.Overturned != nil
}

func (w *v2world) overturn(b schema.BRD) string {
	if rec, ok := w.councilRecord(b); ok && rec.Overturned != nil {
		return rec.Overturned.By
	}
	return ""
}

// councilTally is how many seats approved, of how many voted.
func (w *v2world) councilTally(b schema.BRD) (approve, seats int) {
	rec, _ := w.councilRecord(b)
	for _, v := range rec.Votes {
		seats++
		if v.Verdict == schema.VerdictApprove {
			approve++
		}
	}
	return approve, seats
}

func (w *v2world) council(b schema.BRD) *contract.Council {
	rec, ok := w.councilRecord(b)
	a := b.Approval
	if !ok && (a == nil || a.Mandate == "") {
		return nil
	}
	c := &contract.Council{Votes: []contract.Vote{}}
	for _, v := range rec.Votes {
		c.Votes = append(c.Votes, contract.Vote{Seat: v.Seat, Actor: v.Actor, Verdict: v.Verdict, Reason: v.Reason})
	}
	if a != nil {
		c.Mandate, c.DelegatedBy, c.HumanReview = a.Mandate, a.DelegatedBy, string(a.HumanReview)
		if a.Decision != nil {
			c.Summary = a.Decision.Summary
		}
	}
	if rec.Overturned != nil {
		c.Overturned = rec.Overturned.By
	}
	return c
}

// --- promises ---

func tierMaxOf(b schema.BRD) int { return brd.MaxTier(b) }

// liveOf reads one enforcer's reach class the way the check reads a row's.
func liveOf(reach string) contract.Assurance {
	switch reach {
	case schema.ReachEntry:
		return contract.Assurance{State: contract.StatePass, Honesty: contract.UpperBound, Text: "an entry point reaches it"}
	case "", schema.ReachUnknown:
		return contract.Assurance{State: contract.StateUnknown, Honesty: contract.UpperBound, Code: "LIVE_UNKNOWN", Text: "reach is unknown"}
	}
	return contract.Assurance{State: contract.StateFail, Honesty: contract.UpperBound, Code: "LIVE_" + strings.ToUpper(reach),
		Text: "no entry point reaches it (" + reach + ")"}
}

// promiseOf shapes one audit row. Every signal is the check's: the contract
// adds no rule. Only nothing_fell is left out, because it needs a base.
func (w *v2world) promiseOf(row audit.Row, criteria []string) contract.Promise {
	sig := check.RowSignals(row)
	signals := contract.Signals{
		Traced: fromCheck(sig[check.SigTraced]), Confirmed: fromCheck(sig[check.SigConfirmed]), Live: fromCheck(sig[check.SigLive]),
		Exercised: fromCheck(sig[check.SigExercised]), BreakTested: fromCheck(sig[check.SigBreakTested]),
		NothingFell: contract.NotComputed("nothing_fell (it is measured against a base: lattice check)"),
	}
	if signals.Confirmed.Code == "CONFIRMED_DRIFTED" {
		signals.Confirmed.State = contract.StateDrifted
	}
	p := contract.Promise{
		ID: row.Promise, Feature: row.Feature, Statement: row.Statement, Tier: row.Tier,
		Class: row.Class, Kind: row.Kind, Reason: row.Reason,
		Verdict: fromVerdict(check.HeadVerdict(row)), Criteria: criteria, Signals: signals,
		Gaps: []contract.Gap{}, Enforcers: []contract.Enforcer{}, Verifiers: []contract.Verifier{},
		Cases: contract.NotComputed("cases (happy, edge, error)"),
	}
	if len(row.Cases) > 0 {
		p.Cases, p.CaseRows = casesOf(row.Cases)
	}
	gaps := check.HeadGaps(row)
	for _, g := range gaps {
		gap := contract.Gap{Signal: g.Signal, State: g.State, Honesty: g.Honesty, Code: g.Code, Text: g.Text, Next: g.NextAction}
		if gap.Code == "CONFIRMED_DRIFTED" {
			gap.State = contract.StateDrifted // the same word the signal uses
		}
		p.Gaps = append(p.Gaps, gap)
	}
	p.NextAction = "run lattice check --base <rev> to compare with a base"
	if len(gaps) > 0 {
		p.NextAction = gaps[0].NextAction
	}
	for _, e := range row.Evidence.Enforcers {
		p.Enforcers = append(p.Enforcers, contract.Enforcer{FQN: e.FQN, File: e.File, Line: e.Line, Live: liveOf(e.Reach)})
	}
	for _, v := range row.Evidence.Verifiers {
		p.Verifiers = append(p.Verifiers, contract.Verifier{FQN: v.FQN, File: v.File, Result: v.Result, Coverage: v.Coverage, RanEnforcer: v.RanEnforcer})
	}
	p.Waivers = w.waiversFor(row.Promise)
	for i := range p.Waivers {
		if p.Waivers[i].Kind == "out_of_reach" {
			x := p.Waivers[i]
			p.OutOfReach = &x
			break
		}
	}
	for _, n := range row.Notes {
		p.Notes = append(p.Notes, contract.Note{Reader: n.Reader, Verdict: n.Verdict, Note: n.Note})
	}
	return p
}

// casesOf shapes a promise's acceptance cases and sums them up: pass only
// when every case that is not retired is covered, fail when one is failing,
// unknown otherwise. The coverage of each is the audit's.
// @enforces api:INV-2
func casesOf(rows []audit.CaseRow) (contract.Assurance, []contract.CaseRow) {
	out := make([]contract.CaseRow, 0, len(rows))
	n := map[string]int{}
	live := 0
	for _, c := range rows {
		out = append(out, contract.CaseRow{ID: c.ID, Label: c.Label, Kind: c.Kind, Statement: c.Statement, Enforcer: c.Enforcer,
			Retired: c.Retired, Coverage: c.Coverage, Reason: c.Reason, Verifiers: nonNil(c.Verifiers),
			Confirmation: caseConfirmation(c.Confirmation)})
		if !c.Retired {
			live++
			n[c.Coverage]++
		}
	}
	params := map[string]interface{}{"cases": live, audit.CaseCovered: n[audit.CaseCovered], audit.CasePartial: n[audit.CasePartial],
		audit.CaseFailing: n[audit.CaseFailing], audit.CaseUncovered: n[audit.CaseUncovered]}
	a := contract.Assurance{Honesty: contract.Fact, Params: params}
	switch {
	case live == 0:
		a.State, a.Code, a.Text = contract.StateUnknown, "CASES_ALL_RETIRED", "every case is retired"
	case n[audit.CaseFailing] > 0:
		a.State, a.Code = contract.StateFail, "CASES_FAILING"
		a.Text = fmt.Sprintf("%d of %d cases failing", n[audit.CaseFailing], live)
	case n[audit.CaseCovered] == live:
		a.State, a.Code = contract.StatePass, "CASES_COVERED"
		a.Text = fmt.Sprintf("all %d cases covered", live)
	default:
		a.State, a.Code = contract.StateUnknown, "CASES_NOT_COVERED"
		a.Text = fmt.Sprintf("%d of %d cases covered (%d partial, %d uncovered)", n[audit.CaseCovered], live, n[audit.CasePartial], n[audit.CaseUncovered])
	}
	return a, out
}

// caseConfirmation carries the audit's confirmation state of one case into
// the contract: pass only for a confirmed case, drifted for one edited or
// retired after confirmation, unknown otherwise, always with the audit's
// code. A row the audit gave no state reads unknown.
func caseConfirmation(c audit.CaseConfirmation) contract.Assurance {
	a := contract.Assurance{Honesty: contract.Proxy, Code: c.Code, Text: c.Reason, Params: map[string]interface{}{"state": c.State}}
	switch {
	case c.State == audit.CaseConfirmed && c.Code == audit.CaseCodeConfirmed:
		a.State = contract.StatePass
	case c.Code == audit.CaseCodeStampMismatch:
		a.State = contract.StateFail
	case c.Code == audit.CaseCodeEdited || c.Code == audit.CaseCodeRetired || c.Code == audit.CaseCodeDecisionStale:
		a.State = contract.StateDrifted
	default:
		a.State = contract.StateUnknown
		if a.Code == "" {
			a.Code, a.Text = "CASE_CONFIRMATION_UNKNOWN", "the audit gave this case no confirmation state"
		}
	}
	return a
}

// countsOf counts the promises of a scope by what the check's rules say.
func countsOf(ps []contract.Promise) contract.Counts {
	c := contract.Counts{Promises: len(ps)}
	for _, p := range ps {
		switch p.Verdict.State {
		case check.Unproven:
			c.Unproven++
		case check.Unknown:
			c.Unknown++
		}
		if p.OutOfReach != nil {
			c.OutOfReach++
		}
	}
	return c
}

// --- GET /api/v2/brds ---

func (w *v2world) featuresOf(b schema.BRD) []string {
	f := brd.FeaturesByBRD(w.kg.BRDs, w.kg.Features)[b.ID]
	if f == nil {
		f = []string{}
	}
	return f
}

func (w *v2world) brdLink(b schema.BRD) string {
	return "/brd/" + b.ID + "/v/" + strconv.Itoa(b.Version)
}

func (s *Server) v2BRDs(wr http.ResponseWriter, r *http.Request) {
	w := s.loadOr500(wr, r)
	if w == nil {
		return
	}
	out := contract.BRDList{Header: w.header(contract.BRDV1), BRDs: []contract.BRDSummary{}}
	for _, b := range w.kg.BRDs {
		state, chips, _ := w.brdStanding(b)
		out.BRDs = append(out.BRDs, contract.BRDSummary{
			ID: b.ID, Version: b.Version, Title: b.Title, Status: string(b.Status), State: state, Chips: chips,
			TierMax: tierMaxOf(b), Features: w.featuresOf(b),
			Verdict: fromVerdict(check.RollUp(w.rowsOf(confirm.PromisesOf(b)))), Link: w.brdLink(b),
		})
	}
	sort.SliceStable(out.BRDs, func(i, j int) bool { return out.BRDs[i].ID < out.BRDs[j].ID })
	writeV2(wr, http.StatusOK, out, contract.LintOptions{})
}

// --- GET /api/v2/brds/{id}[?version=N] ---

func criteriaByPromise(b schema.BRD) map[string][]string {
	out := map[string][]string{}
	for _, sc := range b.SuccessCriteria {
		for _, ref := range sc.MapsToInvariant {
			out[ref] = append(out[ref], sc.ID)
		}
	}
	return out
}

func (s *Server) v2BRD(wr http.ResponseWriter, r *http.Request) {
	w := s.loadOr500(wr, r)
	if w == nil {
		return
	}
	b := brd.Find(w.kg.BRDs, r.PathValue("id"))
	if b == nil {
		writeV2Error(wr, http.StatusNotFound, "BRD_NOT_FOUND", "no BRD "+r.PathValue("id"), nil)
		return
	}
	wantVersion := 0
	if v := r.URL.Query().Get("version"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeV2Error(wr, http.StatusBadRequest, "BAD_VERSION", "version must be a positive integer", nil)
			return
		}
		if n != b.Version {
			writeV2Error(wr, http.StatusNotFound, "VERSION_NOT_CURRENT",
				b.ID+" is at v"+strconv.Itoa(b.Version)+"; only the current version can be opened as a review packet", nil)
			return
		}
		wantVersion = n
	}

	state, chips, conf := w.brdStanding(*b)
	refs := confirm.PromisesOf(*b)
	crit := criteriaByPromise(*b)
	page := contract.BRDPage{
		Header: w.header(contract.BRDV1), ID: b.ID, Version: b.Version, Status: string(b.Status),
		State: state, Chips: chips, Title: b.Title, Summary: b.BusinessProblem,
		Provenance: contract.BRDProvenance{Source: string(b.Provenance.Source), AuthoredBy: b.Provenance.AuthoredBy,
			Model: b.Provenance.Model, GeneratedAt: b.Provenance.GeneratedAt},
		Confirmation: conf, Features: w.featuresOf(*b), TierMax: tierMaxOf(*b),
		Owners: map[string]string{},
		Intent: contract.Intent{
			Story:           heldStory(),
			BusinessProblem: b.BusinessProblem, Goals: nonNil(b.BusinessGoals), Scenarios: []contract.Scenario{},
			OutOfScope: nonNil(b.OutOfScope), OpenQuestions: nonNil(b.OpenQuestions),
		},
		Criteria: []contract.Criterion{}, Promises: []contract.Promise{}, Constraints: []contract.Constraint{},
		Council: w.council(*b), Waivers: []contract.Waiver{},
		BlindSpot: contract.NotComputed("the blind spot (untraced touched lines are measured per change: lattice check)"),
		Sources:   sourcesOf(*b),
	}
	for role, v := range map[string]string{"business_owner": b.Stakeholders.BusinessOwner, "product_owner": b.Stakeholders.ProductOwner,
		"engineering_owner": b.Stakeholders.EngineeringOwner, "legal": b.Stakeholders.Legal, "compliance": b.Stakeholders.Compliance} {
		if v != "" {
			page.Owners[role] = v
		}
	}
	for _, sc := range b.UserScenarios {
		page.Intent.Scenarios = append(page.Intent.Scenarios, contract.Scenario{ID: sc.ID, Actor: sc.Actor, Narrative: sc.Narrative})
	}
	for _, sc := range b.SuccessCriteria {
		page.Criteria = append(page.Criteria, contract.Criterion{ID: sc.ID, Statement: sc.Statement, Tier: sc.EffectiveTier(), MapsTo: nonNil([]string(sc.MapsToInvariant))})
	}
	rows := w.rowsOf(refs)
	for _, row := range rows {
		page.Promises = append(page.Promises, w.promiseOf(row, crit[row.Promise]))
	}
	page.Verdict = fromVerdict(check.RollUp(rows))
	page.Counts = countsOf(page.Promises)
	seenW := map[string]bool{}
	for _, p := range page.Promises {
		for _, x := range p.Waivers {
			if k := x.Subject + "|" + x.Code; !seenW[k] {
				seenW[k] = true
				page.Waivers = append(page.Waivers, x)
			}
		}
	}
	for _, c := range b.Constraints {
		page.Constraints = append(page.Constraints, contract.Constraint{
			Kind: string(c.Kind), Ref: c.Ref, Note: c.Note, Locked: lockedKind(c.Kind), Author: authorOf(*b)})
	}
	page.EntryPoints = w.entryPointsOf(page.Features)
	page.History = w.history(b.ID)

	if wantVersion > 0 {
		page.Packet = w.packet(*b, page.Promises, page.Counts)
	}
	writeV2(wr, http.StatusOK, page, contract.LintOptions{})
}

// heldStory is the BRD's story: not computed, and held on purpose. The word
// is reserved for the planned story layer, whose name is not settled yet.
func heldStory() contract.Assurance {
	a := contract.NotComputed("the story (held until the story layer's name is settled)")
	a.Params = map[string]interface{}{"reason": contract.ReasonHeldByDecision}
	return a
}

// sourcesOf lists the BRD's sources as the BRD states them. A checked mark
// is reported as a human's claim (honesty claimed), never as verified: the
// kernel never fetches a source or checks it itself.
// @enforces api:INV-6
func sourcesOf(b schema.BRD) contract.Sources {
	out := contract.Sources{Entries: []contract.Source{}}
	checked := 0
	for _, s := range b.Sources {
		e := contract.Source{Kind: string(s.Kind), Ref: s.Ref, Title: s.Title, URL: s.URL}
		if s.CheckedBy != "" && s.CheckedAt != "" {
			checked++
			e.State, e.Honesty, e.Code = contract.StateChecked, contract.Claimed, "SOURCE_CHECK_CLAIMED"
			e.Params = map[string]interface{}{"by": s.CheckedBy, "at": s.CheckedAt}
			e.Text = s.CheckedBy + " says they checked the BRD against " + s.Ref + " on " + s.CheckedAt + " (not verified)"
			e.CheckedBy, e.CheckedAt = s.CheckedBy, s.CheckedAt
		} else {
			e.State, e.Honesty, e.Code = contract.StateUnchecked, contract.Fact, "SOURCE_UNCHECKED"
			e.Text = "no one has recorded checking the BRD against " + s.Ref
		}
		out.Entries = append(out.Entries, e)
	}
	out.Assurance = contract.Assurance{State: contract.StateInfo, Honesty: contract.Fact, Code: "SOURCES",
		Params: map[string]interface{}{"count": len(out.Entries), "checked": checked},
		Text:   strconv.Itoa(len(out.Entries)) + " source(s), " + strconv.Itoa(checked) + " marked checked by a human"}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func lockedKind(k schema.BRDConstraintKind) bool {
	switch k {
	case schema.ConstraintRegulatory, schema.ConstraintLegal, schema.ConstraintFinancial:
		return true
	}
	return false
}

func authorOf(b schema.BRD) string {
	if b.Provenance.AuthoredBy != "" {
		return b.Provenance.AuthoredBy
	}
	return string(b.Provenance.Source)
}

// history lists the decisions confirm.json holds for a BRD, as the platform
// recorded them.
func (w *v2world) history(id string) contract.History {
	h := contract.History{Entries: []contract.HistoryEntry{}}
	if w.cd == nil {
		h.State = contract.Assurance{State: contract.StateUnknown, Honesty: contract.Fact, Code: "NO_CONFIRM_DATA",
			Text: "no confirm.json: run `lattice confirm` in CI"}
		return h
	}
	h.State = contract.Assurance{State: contract.StateInfo, Honesty: contract.Fact, Code: "FROM_CONFIRM_JSON", Text: "decisions read from confirm.json"}
	for _, r := range w.cd.Records {
		if r.ID != id {
			continue
		}
		e := contract.HistoryEntry{Version: r.Version, Action: r.Action, State: r.State, By: r.ApproverLogin,
			Via: via(r), Method: r.Route(), Fingerprint: r.Fingerprint}
		h.Entries = append(h.Entries, e)
	}
	return h
}

// packet is the review packet of the BRD's current version: the structural
// diff since the last confirmed version and the hash a decision would pin,
// both from prepare.Env.Packet, which is how `lattice prepare` computes them.
func (w *v2world) packet(b schema.BRD, promises []contract.Promise, counts contract.Counts) *contract.Packet {
	env := w.s.prepareEnv(w)
	pk := env.Packet(b, "")
	p := &contract.Packet{
		Version: b.Version, Counts: counts,
		BlindSpot:  contract.NotComputed("the blind spot (untraced touched lines are measured per change: lattice check)"),
		OutOfReach: []contract.Waiver{},
		Diff: contract.Diff{NeverConfirmed: pk.Since == nil && pk.Err == nil, Structural: pk.Diff,
			TextOnly: contract.NotComputed("the text-only diff (the kernel diffs structure only)"), Friction: pk.Friction},
	}
	if pk.Since != nil {
		p.Diff.SinceVersion, p.Diff.SinceCommit = pk.Since.Version, pk.Since.SHA
	}
	if pk.Err != nil {
		p.HashState = contract.Assurance{State: contract.StateUnknown, Honesty: contract.Fact, Code: "DIFF_BASE_UNREADABLE",
			Text: "the last confirmed version could not be read from git (" + pk.Err.Error() + "): the diff and the hash are not those a decision would pin"}
		p.Diff.Structural = []string{}
		p.Diff.TextOnly = contract.NotComputed("the text-only diff")
		p.Diff.Friction = confirm.FrictionFull
	} else {
		p.PacketHash = pk.Hash
		p.HashState = contract.Assurance{State: contract.StateInfo, Honesty: contract.Fact, Code: "PACKET_HASH", Text: "computed as `lattice prepare` pins it"}
	}
	if rec, ok := w.councilRecord(b); ok && len(rec.Votes) > 0 {
		p.VotesHash = confirm.VotesHash(rec.Votes)
		if pk.Err == nil {
			p.AckPacketHash = env.Packet(b, p.VotesHash).Hash
		}
	}
	for _, pr := range promises {
		if pr.OutOfReach != nil {
			p.OutOfReach = append(p.OutOfReach, *pr.OutOfReach)
		}
	}
	return p
}

// --- GET /api/v2/features/{id} ---

func (s *Server) v2Feature(wr http.ResponseWriter, r *http.Request) {
	w := s.loadOr500(wr, r)
	if w == nil {
		return
	}
	id := r.PathValue("id")
	var m *schema.Manifest
	for i := range w.kg.Features {
		if w.kg.Features[i].ID == id {
			m = &w.kg.Features[i]
		}
	}
	if m == nil {
		writeV2Error(wr, http.StatusNotFound, "FEATURE_NOT_FOUND", "no feature "+id, nil)
		return
	}
	rows := w.featureRows(id)
	page := contract.FeaturePage{
		Header: w.header(contract.BRDV1), ID: m.ID, Status: string(m.Status), Purpose: strings.TrimSpace(m.Purpose),
		Owners: map[string]string{}, Origin: contract.NotComputed("origin"),
		Promises:  []contract.Promise{},
		BlindSpot: contract.NotComputed("the blind spot (untraced touched lines are measured per change: lattice check)"),
		RunsIn:    contract.NotComputed("where it runs (systems and apps)"),
		Sources:   contract.NotComputed("sources"), History: contract.NotComputed("history"),
		Related: contract.Related{DependsOn: nonNil(m.DependsOn), DependedBy: []string{},
			Effect: contract.NotComputed("the effect of a related feature breaking")},
	}
	for role, v := range map[string]string{"business": m.Owners.Business, "engineering": m.Owners.Engineering, "on_call": m.Owners.OnCall} {
		if v != "" {
			page.Owners[role] = v
		}
	}
	for _, f := range w.kg.Features {
		for _, d := range f.DependsOn {
			if d == id {
				page.Related.DependedBy = append(page.Related.DependedBy, f.ID)
			}
		}
	}
	sort.Strings(page.Related.DependedBy)

	if m.ImplementsBRD != "" {
		if b := brd.Find(w.kg.BRDs, m.ImplementsBRD); b != nil {
			state, _, conf := w.brdStanding(*b)
			page.BRD = &contract.BRDRef{ID: b.ID, Version: b.Version, State: state,
				ConfirmedVersion: conf.ConfirmedVersion, ConfirmedBy: conf.ConfirmedBy, Via: conf.Via,
				Method: conf.Method, Fingerprint: conf.Fingerprint}
		}
	}
	crit := map[string][]string{}
	for _, b := range w.kg.BRDs {
		for p, scs := range criteriaByPromise(b) {
			crit[p] = append(crit[p], scs...)
		}
	}
	for _, row := range rows {
		page.Promises = append(page.Promises, w.promiseOf(row, crit[row.Promise]))
	}
	page.Verdict = fromVerdict(check.RollUp(rows))
	page.Counts = countsOf(page.Promises)
	page.NoPromise = len(rows) == 0
	page.EntryPoints = w.entryPointsOf([]string{id})
	writeV2(wr, http.StatusOK, page, contract.LintOptions{})
}

// via is where a confirmation was made, for display: "PR #7" for a review,
// "signature SHA256:..." for a signature.
func via(r audit.ConfirmRecord) string {
	switch {
	case r.Route() == audit.ViaSignature:
		return "signature " + signature.Short(r.Fingerprint)
	case r.PR != 0:
		return "PR #" + strconv.Itoa(r.PR)
	}
	return ""
}
