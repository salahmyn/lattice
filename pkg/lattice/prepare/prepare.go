// Package prepare turns a human's intent to decide into a decision branch.
//
// It validates the action against the kernel, writes one decision file (and,
// for a waiver, the protected waivers file) onto a new branch `decision/...`
// built from HEAD with git plumbing, and prints the `gh pr create` command to
// run. It never switches the checkout, never touches the working tree or the
// index, never pushes and never opens a pull request. Nothing it writes is a
// confirmation: CONFIRMED comes only from `lattice confirm`, which reads the
// platform's approval on the merged pull request.
package prepare

// @module-feature confirm

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Schema is the contract of prepare's JSON.
const Schema = "lattice.prepare/1"

// Request is one action to prepare.
type Request struct {
	Action  string // a confirm.Action* value
	ID      string // BRD id (brd.*, promise.confirm)
	Version int    // BRD version; required for brd.approve
	Reason  string
	// Refs are the promises of a promise.confirm batch, or the rows a
	// return refers to.
	Refs   []string
	Waiver *confirm.WaiverEntry
}

// Env is what prepare reads. It never builds a graph itself.
type Env struct {
	Git        confirm.Git
	LatticeDir string // absolute path of lattice/
	KG         schema.KnowledgeGraph
	Report     audit.Report
	Config     config.Config
	// Actor is the --actor / LATTICE_ACTOR identity running the command.
	Actor string
	Now   func() time.Time
	// Cache is the last confirm.json, for the structural diff base.
	Cache *confirm.File
}

// Result is lattice.prepare/1.
type Result struct {
	Contract   string   `json:"contract"`
	Commit     string   `json:"commit"`
	Branch     string   `json:"branch"`
	Base       string   `json:"base"`
	Files      []string `json:"files"`
	PacketHash string   `json:"packet_hash"`
	Command    string   `json:"command"`
	Note       string   `json:"note"`
}

// Refusal is the kernel saying no: {refused, params, text, next}.
type Refusal struct {
	Contract string                 `json:"contract"`
	Commit   string                 `json:"commit,omitempty"`
	Refused  string                 `json:"refused"`
	Params   map[string]interface{} `json:"params,omitempty"`
	Text     string                 `json:"text"`
	Next     string                 `json:"next"`
}

func (r *Refusal) Error() string { return r.Refused + ": " + r.Text }

func refuse(code, text, next string, params map[string]interface{}) *Refusal {
	return &Refusal{Contract: Schema, Refused: code, Params: params, Text: text, Next: next}
}

// Run validates one request against the kernel and, when it holds, writes the
// decision branch. It returns exactly one of a result or a refusal.
// @enforces confirm:INV-5
func Run(env Env, req Request) (*Result, *Refusal) {
	res, ref := run(env, req)
	if ref != nil && ref.Commit == "" {
		ref.Commit, _ = env.Git.Head()
	}
	return res, ref
}

func run(env Env, req Request) (*Result, *Refusal) {
	g := env.Git
	head, err := g.Head()
	if err != nil {
		return nil, refuse("NOT_A_REPO", "this is not a git repository with a commit: "+err.Error(), "run prepare inside a git repository", nil)
	}
	top, err := g.Top()
	if err != nil {
		return nil, refuse("NOT_A_REPO", err.Error(), "run prepare inside a git repository", nil)
	}
	rel, err := relTo(top, env.LatticeDir)
	if err != nil {
		return nil, refuse("NOT_A_REPO", "lattice/ is outside the repository: "+err.Error(), "", nil)
	}
	if files := dirtyLattice(g, rel); len(files) > 0 {
		return nil, refuse("DIRTY_LATTICE", "lattice/ has uncommitted changes, and a decision is prepared from HEAD: "+strings.Join(files, ", "),
			"commit or discard them, then prepare again", map[string]interface{}{"files": files})
	}
	name, email := identity(g)
	now := time.Now
	if env.Now != nil {
		now = env.Now
	}

	// Agents never decide. Returning a BRD with changes is advice and stays open.
	if req.Action != confirm.ActionReturn && schema.IsAgentActor(env.Actor) {
		return nil, refuse("AGENT_PREPARER", fmt.Sprintf("%s is an agent; only a human prepares a decision", env.Actor),
			"ask a human to run this command", map[string]interface{}{"actor": env.Actor})
	}

	d := confirm.Decision{Action: req.Action, PreparedBy: name + " <" + email + ">", PreparedAt: now().UTC().Format(time.RFC3339)}
	var extra map[string][]byte

	switch req.Action {
	case confirm.ActionApprove, confirm.ActionReturn, confirm.ActionAck, confirm.ActionOverturn:
		if ref := env.brdDecision(&d, req, rel, name, email); ref != nil {
			return nil, ref
		}
	case confirm.ActionPromiseConfirm:
		if ref := env.promiseDecision(&d, req, rel); ref != nil {
			return nil, ref
		}
	case confirm.ActionWaiverAdd:
		var ref *Refusal
		if extra, ref = env.waiverDecision(&d, req, rel, head); ref != nil {
			return nil, ref
		}
	default:
		return nil, refuse("UNKNOWN_ACTION", fmt.Sprintf("%q is not an action", req.Action),
			"use one of "+strings.Join(confirm.Actions, ", "), nil)
	}

	branch := d.Branch()
	if !validBranch(g, branch) {
		return nil, refuse("BAD_ID", fmt.Sprintf("%q cannot be a branch name", branch), "use an id of letters, digits, dots and dashes", map[string]interface{}{"branch": branch})
	}
	if branchExists(g, branch) {
		return nil, refuse("BRANCH_EXISTS", "decision branch "+branch+" already exists", "open its pull request, or delete the branch to prepare again", map[string]interface{}{"branch": branch})
	}

	body, err := d.Marshal()
	if err != nil {
		return nil, refuse("WRITE_FAILED", err.Error(), "", nil)
	}
	files := map[string][]byte{path.Join(rel, d.RelPath()): body}
	for p, b := range extra {
		files[p] = b
	}
	msg := fmt.Sprintf("decision: %s %s v%d\n\nPrepared by lattice prepare. Packet %s.\nNot a confirmation: it counts when a human other than the author approves the head and merges.",
		d.Action, d.ID, d.Version, d.PacketHash)
	if _, err := commitBranch(g, branch, msg, name, email, files); err != nil {
		return nil, refuse("WRITE_FAILED", err.Error(), "", nil)
	}

	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	base := confirm.BaseName(confirm.DefaultBranch(g))
	title := fmt.Sprintf("decision: %s %s v%d", d.Action, d.ID, d.Version)
	prBody := fmt.Sprintf("Prepared by `lattice prepare`. Action: %s. ID: %s. Version: %d. Packet: %s. This counts only when a human other than the author approves the current head and merges.",
		d.Action, d.ID, d.Version, d.PacketHash)
	return &Result{
		Contract: Schema, Commit: head, Branch: branch, Base: base, Files: out, PacketHash: d.PacketHash,
		Command: fmt.Sprintf("gh pr create --head %s --base %s --title %s --body %s", branch, base, shellQuote(title), shellQuote(prBody)),
		Note:    "Push the branch first (git push origin " + branch + "). Recorded only when a human other than the author approves the merged head and CI reads the approver.",
	}, nil
}

// brdDecision validates brd.approve, brd.return, brd.ack and brd.overturn.
func (env Env) brdDecision(d *confirm.Decision, req Request, rel, name, email string) *Refusal {
	b := brd.Find(env.KG.BRDs, req.ID)
	if b == nil {
		return refuse("BRD_NOT_FOUND", "no BRD "+req.ID, "list BRDs with `lattice brd list`", map[string]interface{}{"id": req.ID})
	}
	switch {
	case req.Action == confirm.ActionApprove && req.Version < 1:
		return refuse("VERSION_REQUIRED", "name the version you reviewed: --version "+fmt.Sprint(b.Version), "re-run with --version", map[string]interface{}{"current": b.Version})
	case req.Version > 0 && req.Version != b.Version:
		return refuse("VERSION_MISMATCH", fmt.Sprintf("%s is at v%d, not v%d", b.ID, b.Version, req.Version),
			"review the current version, then prepare it", map[string]interface{}{"current": b.Version, "requested": req.Version})
	case b.Status == schema.BRDSuperseded:
		return refuse("BRD_SUPERSEDED", b.ID+" is superseded", "decide its replacement instead", map[string]interface{}{"id": b.ID})
	}
	d.ID, d.Version, d.Reason = b.ID, b.Version, strings.TrimSpace(req.Reason)

	if (req.Action == confirm.ActionReturn || req.Action == confirm.ActionOverturn) && d.Reason == "" {
		return refuse("REASON_REQUIRED", "a reason is required", "re-run with --reason", nil)
	}
	if req.Action == confirm.ActionApprove || req.Action == confirm.ActionAck {
		if ref := regulatedMachineWritten(*b); ref != nil {
			return ref
		}
	}

	extra := ""
	switch req.Action {
	case confirm.ActionAck, confirm.ActionOverturn:
		votes, ref := env.checkCouncil(*b, req.Action == confirm.ActionAck, name, email)
		if ref != nil {
			return ref
		}
		d.VotesHash = confirm.VotesHash(votes)
		extra = d.VotesHash
	case confirm.ActionApprove:
		// A human may approve a council version too, but not one the preparer
		// took part in.
		if rec, err := brd.LoadRecord(env.LatticeDir, b.ID, b.Version); err == nil && len(rec.Votes) > 0 {
			if ref := env.touched(*b, rec.Votes, name, email); ref != nil {
				return ref
			}
		}
	}

	promises := confirm.PromisesOf(*b)
	known := map[string]audit.Row{}
	for _, r := range env.Report.Rows {
		known[r.Promise] = r
	}
	refs := promises
	if req.Action == confirm.ActionReturn {
		refs = nil
		for _, r := range req.Refs {
			if !contains(promises, r) {
				return refuse("PROMISE_NOT_FOUND", fmt.Sprintf("%s is not a promise of %s", r, b.ID), "name promises the BRD's criteria map to", map[string]interface{}{"promise": r})
			}
			refs = append(refs, r)
		}
	}
	for _, r := range refs {
		row := known[r]
		d.Rows = append(d.Rows, confirm.Row{Promise: r, Statement: row.Statement, Tier: row.Tier, Class: row.Class})
	}
	d.PacketHash = env.packet(d, *b, rel, extra)
	return nil
}

// packet hashes a BRD version's review packet and records the diff base.
func (env Env) packet(d *confirm.Decision, b schema.BRD, rel, extra string) string {
	prev, since := env.lastConfirmed(b.ID, b.Version, rel)
	d.Since = since
	return confirm.PacketHash(confirm.PacketInput{
		BRD: b, Promises: confirm.FactsFor(env.Report.Rows, confirm.PromisesOf(b)),
		Diff: confirm.StructuralDiff(prev, b), Extra: extra,
	})
}

// lastConfirmed finds the last version of a BRD a human confirmed before
// `before`, from the confirm cache, and reads it from the commit it merged in.
func (env Env) lastConfirmed(id string, before int, rel string) (*schema.BRD, *confirm.Since) {
	if env.Cache == nil {
		return nil, nil
	}
	var best *confirm.Record
	for i, r := range env.Cache.Records {
		if r.ID != id || r.Version >= before || r.State != confirm.StateConfirmed || r.MergedSHA == "" {
			continue
		}
		if r.Action != confirm.ActionApprove && r.Action != confirm.ActionAck {
			continue
		}
		if best == nil || r.Version > best.Version {
			best = &env.Cache.Records[i]
		}
	}
	if best == nil {
		return nil, nil
	}
	prev, err := confirm.BRDAt(env.Git, rel, best.MergedSHA, id)
	if err != nil {
		return nil, nil
	}
	return prev, &confirm.Since{Version: best.Version, SHA: best.MergedSHA}
}

// regulatedMachineWritten refuses a version whose regulatory, legal or
// financial constraint was written by a machine: those come from a human.
// @enforces confirm:INV-5
func regulatedMachineWritten(b schema.BRD) *Refusal {
	if !b.MachineWritten() {
		return nil
	}
	author := b.Provenance.AuthoredBy
	if author == "" {
		author = string(b.Provenance.Source)
	}
	for _, c := range b.Constraints {
		switch c.Kind {
		case schema.ConstraintRegulatory, schema.ConstraintLegal, schema.ConstraintFinancial:
			ref := c.Ref
			if ref == "" {
				ref = string(c.Kind)
			}
			return refuse("REGULATED_MACHINE_WRITTEN",
				fmt.Sprintf("constraint %s (%s) was written by %s", ref, c.Kind, author),
				"rewrite it yourself, then re-open this review",
				map[string]interface{}{"constraint": ref, "kind": string(c.Kind), "author": author})
		}
	}
	return nil
}

// touched refuses when the preparer took part in the council's version: a
// seat's actor, the version's author, or the preparer's git identity.
func (env Env) touched(b schema.BRD, votes []schema.BRDCouncilVote, name, email string) *Refusal {
	me := []string{env.Actor, email}
	check := func(who, role string) *Refusal {
		for _, m := range me {
			if strings.TrimSpace(m) != "" && strings.EqualFold(strings.TrimSpace(who), strings.TrimSpace(m)) {
				return refuse("COUNCIL_TOUCHED",
					fmt.Sprintf("%s is %s on %s v%d", m, role, b.ID, b.Version),
					"someone who did not write or vote on this version prepares it",
					map[string]interface{}{"who": m, "role": role, "version": b.Version})
			}
		}
		return nil
	}
	if a := b.Provenance.AuthoredBy; a != "" {
		if r := check(a, "the author"); r != nil {
			return r
		}
	}
	for _, v := range votes {
		if r := check(v.Actor, "seat "+v.Seat); r != nil {
			return r
		}
	}
	return nil
}

// checkCouncil validates an ack or an overturn of a council approval and
// returns the votes it is pinned to.
func (env Env) checkCouncil(b schema.BRD, ack bool, name, email string) ([]schema.BRDCouncilVote, *Refusal) {
	a := b.Approval
	if b.Status != schema.BRDApproved || a == nil || a.Mandate == "" {
		return nil, refuse("NO_COUNCIL_APPROVAL", b.ID+" has no council approval to review", "a council decides with `lattice brd vote` and `lattice brd decide` first", nil)
	}
	if a.ApprovedVersion != b.Version {
		return nil, refuse("VERSION_MOVED", fmt.Sprintf("the council approved v%d; the BRD is now v%d", a.ApprovedVersion, b.Version),
			"have the council vote on the current version", map[string]interface{}{"approved": a.ApprovedVersion, "current": b.Version})
	}
	rec, err := brd.LoadRecord(env.LatticeDir, b.ID, b.Version)
	if err != nil {
		return nil, refuse("COUNCIL_VOTES", err.Error(), "", nil)
	}
	if o := rec.Overturned; o != nil {
		return nil, refuse("COUNCIL_OVERTURNED", fmt.Sprintf("v%d was already overturned by %s", b.Version, o.By), "revise the BRD for a new version", nil)
	}
	if r := env.touched(b, rec.Votes, name, email); r != nil {
		return nil, r
	}
	if !ack {
		return rec.Votes, nil
	}
	if why := brd.HumanOnly(b); len(why) > 0 {
		return nil, refuse("COUNCIL_HUMAN_ONLY", "a council cannot approve this BRD: "+strings.Join(why, "; "),
			"approve it yourself with `lattice prepare brd approve`", map[string]interface{}{"reasons": why})
	}
	m, ok := env.Config.Autonomy.FindMandate(a.Mandate)
	if !ok {
		return nil, refuse("MANDATE_NOT_PINNED", fmt.Sprintf("mandate %q is not pinned in lattice/config.yaml", a.Mandate), "a human pins mandates", nil)
	}
	voted := map[string]bool{}
	for _, v := range rec.Votes {
		if v.Verdict == schema.VerdictApprove {
			voted[v.Seat] = true
		}
	}
	var missing []string
	if m.Council != nil {
		for _, s := range m.Council.Seats {
			if !voted[s] {
				missing = append(missing, s)
			}
		}
	}
	if len(missing) > 0 {
		return nil, refuse("VOTES_INCOMPLETE", fmt.Sprintf("votes are incomplete for v%d: %s", b.Version, strings.Join(missing, ", ")),
			"each seat votes approve on this version", map[string]interface{}{"missing": missing, "version": b.Version})
	}
	if bad := brd.RecheckApproval(b, env.Config.Autonomy); len(bad) > 0 {
		return nil, refuse("COUNCIL_INVALID", "the council approval does not hold: "+strings.Join(bad, "; "), "have the council decide again", map[string]interface{}{"reasons": bad})
	}
	return rec.Votes, nil
}

// promiseDecision validates promise.confirm: one BRD per batch.
func (env Env) promiseDecision(d *confirm.Decision, req Request, rel string) *Refusal {
	if len(req.Refs) == 0 {
		return refuse("REFS_REQUIRED", "name the promises to confirm", "lattice prepare promise confirm <feature:INV-n>...", nil)
	}
	known := map[string]audit.Row{}
	for _, r := range env.Report.Rows {
		known[r.Promise] = r
	}
	var candidates []string
	for i, r := range req.Refs {
		if _, ok := known[r]; !ok {
			return refuse("PROMISE_NOT_FOUND", fmt.Sprintf("no promise %s", r), "promises are <feature>:INV-n", map[string]interface{}{"promise": r})
		}
		var owners []string
		for _, b := range env.KG.BRDs {
			if contains(confirm.PromisesOf(b), r) {
				owners = append(owners, b.ID)
			}
		}
		if len(owners) == 0 {
			return refuse("PROMISE_NO_BRD", fmt.Sprintf("no BRD criterion maps to %s", r), "map it from a BRD criterion first", map[string]interface{}{"promise": r})
		}
		if i == 0 {
			candidates = owners
			continue
		}
		var keep []string
		for _, c := range candidates {
			if contains(owners, c) {
				keep = append(keep, c)
			}
		}
		candidates = keep
	}
	if req.ID != "" {
		if !contains(candidates, req.ID) {
			return refuse("BATCH_MULTI_BRD", fmt.Sprintf("these promises are not all in %s", req.ID), "one BRD per batch", map[string]interface{}{"brd": req.ID})
		}
		candidates = []string{req.ID}
	}
	switch {
	case len(candidates) == 0:
		return refuse("BATCH_MULTI_BRD", "these promises belong to different BRDs", "one BRD per batch: split them", nil)
	case len(candidates) > 1:
		return refuse("BATCH_MULTI_BRD", "these promises belong to several BRDs: "+strings.Join(candidates, ", "), "name one with --brd", map[string]interface{}{"candidates": candidates})
	}
	b := brd.Find(env.KG.BRDs, candidates[0])
	if b.Status == schema.BRDSuperseded {
		return refuse("BRD_SUPERSEDED", b.ID+" is superseded", "", nil)
	}
	if ref := regulatedMachineWritten(*b); ref != nil {
		return ref
	}
	var bad []string
	for _, r := range req.Refs {
		if known[r].Signals.Enforced.Value != "yes" {
			bad = append(bad, r)
		}
	}
	if len(bad) > 0 {
		return refuse("NO_ENFORCER", "cannot confirm: "+strings.Join(bad, ", ")+" cite no enforcer",
			"anchor an enforcer, or leave the promise unconfirmed", map[string]interface{}{"promises": bad})
	}
	d.ID, d.Version, d.Reason = b.ID, b.Version, strings.TrimSpace(req.Reason)
	for _, r := range uniq(req.Refs) {
		row := known[r]
		d.Rows = append(d.Rows, confirm.Row{Promise: r, Statement: row.Statement, Tier: row.Tier, Class: row.Class})
	}
	d.PacketHash = env.packet(d, *b, rel, "")
	return nil
}

// waiverDecision validates waiver.add and returns the waivers file to write.
func (env Env) waiverDecision(d *confirm.Decision, req Request, rel, head string) (map[string][]byte, *Refusal) {
	if req.Waiver == nil {
		return nil, refuse("WAIVER_REQUIRED", "a waiver needs --code, --subject, --kind and --reason", "", nil)
	}
	w := *req.Waiver
	w.Code, w.Subject, w.Kind = strings.TrimSpace(w.Code), strings.TrimSpace(w.Subject), strings.TrimSpace(w.Kind)
	w.Reason, w.ADR = strings.TrimSpace(w.Reason), strings.TrimSpace(w.ADR)
	if w.Code == "" || w.Subject == "" {
		return nil, refuse("WAIVER_REQUIRED", "a waiver names a finding --code and a --subject", "", nil)
	}
	needsADR, ok := confirm.WaiverKinds[w.Kind]
	if !ok {
		kinds := make([]string, 0, len(confirm.WaiverKinds))
		for k := range confirm.WaiverKinds {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		return nil, refuse("UNKNOWN_WAIVER_KIND", fmt.Sprintf("%q is not a waiver kind", w.Kind), "use one of "+strings.Join(kinds, ", "), map[string]interface{}{"kinds": kinds})
	}
	if w.Reason == "" {
		return nil, refuse("REASON_REQUIRED", "a waiver needs a reason", "re-run with --reason", nil)
	}
	if needsADR && w.ADR == "" {
		return nil, refuse("ADR_REQUIRED", fmt.Sprintf("a %s waiver needs an ADR", w.Kind), "record the decision, then pass --adr", map[string]interface{}{"kind": w.Kind})
	}
	if w.ADR != "" && !env.adrExists(w.ADR) {
		return nil, refuse("ADR_NOT_FOUND", fmt.Sprintf("no ADR %q in lattice/decisions, docs/adr or docs", w.ADR), "commit the ADR first", map[string]interface{}{"adr": w.ADR})
	}
	var row *audit.Row
	for i := range env.Report.Rows {
		if env.Report.Rows[i].Promise == w.Subject {
			row = &env.Report.Rows[i]
		}
	}
	switch {
	case row != nil && row.Tier >= 3:
		return nil, refuse("WAIVER_TIER3", fmt.Sprintf("%s is tier 3 and cannot be waived to ASSURED", w.Subject),
			"a tier-3 promise reaches ASSURED only by evidence", map[string]interface{}{"subject": w.Subject})
	case row == nil && !env.fileExists(head, w.Subject):
		return nil, refuse("SUBJECT_NOT_FOUND", fmt.Sprintf("%q is neither a promise nor a tracked file", w.Subject), "name a promise (<feature>:INV-n) or a repository path", map[string]interface{}{"subject": w.Subject})
	}

	// The list only shrinks except through a reviewed addition: read it from HEAD.
	var cur confirm.Waivers
	wpath := path.Join(rel, confirm.WaiversFile)
	if data, err := env.Git.Show("HEAD", wpath); err == nil {
		if err := yaml.Unmarshal(data, &cur); err != nil {
			return nil, refuse("WAIVERS_UNREADABLE", wpath+": "+err.Error(), "fix the file first", nil)
		}
	}
	for _, e := range cur.Waivers {
		if e.Code == w.Code && e.Subject == w.Subject {
			return nil, refuse("WAIVER_DUPLICATE", fmt.Sprintf("%s on %s is already waived", w.Code, w.Subject), "", nil)
		}
	}
	w.AddedBy, w.AddedAt = d.PreparedBy, d.PreparedAt
	cur.Waivers = append(cur.Waivers, w)
	data, err := schema.MarshalCanonical(cur)
	if err != nil {
		return nil, refuse("WRITE_FAILED", err.Error(), "", nil)
	}
	d.ID, d.Version, d.Reason, d.Waiver = "waiver."+confirm.Slug(w.Code)+"."+confirm.Slug(w.Subject), 1, w.Reason, &w
	d.PacketHash = confirm.WaiverHash(w)
	return map[string][]byte{wpath: data}, nil
}

func (env Env) adrExists(adr string) bool {
	out, err := env.Git.Run("ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return false
	}
	adr = strings.ToLower(adr)
	for _, p := range strings.Split(string(out), "\n") {
		lp := strings.ToLower(p)
		if !strings.Contains(lp, "decisions/") && !strings.Contains(lp, "adr/") && !strings.HasPrefix(lp, "docs/") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(path.Base(p)), adr) {
			return true
		}
	}
	return false
}

func (env Env) fileExists(rev, p string) bool {
	_, err := env.Git.Run("cat-file", "-e", rev+":"+p)
	return err == nil
}

// dirtyLattice lists tracked lattice/ files that differ from HEAD. Derived
// caches are not source.
func dirtyLattice(g confirm.Git, rel string) []string {
	out, err := g.Run("status", "--porcelain", "--untracked-files=no", "--", rel)
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		if strings.Contains(p, "/.cache/") || strings.Contains(p, "/.ledger/") {
			continue
		}
		files = append(files, p)
	}
	return files
}

func relTo(top, dir string) (string, error) {
	t, err := filepath.EvalSymlinks(top)
	if err != nil {
		t = top
	}
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		if _, serr := os.Stat(dir); serr != nil {
			return "", serr
		}
		d = dir
	}
	r, err := filepath.Rel(t, d)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("%s is not under %s", dir, top)
	}
	return filepath.ToSlash(r), nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
