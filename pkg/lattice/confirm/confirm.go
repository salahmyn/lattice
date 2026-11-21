package confirm

// @module-feature confirm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/stamp"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Schema is the contract of lattice/.cache/confirm.json.
const Schema = "lattice.confirm/1"

// CacheFile is the derived result, relative to lattice/.
const CacheFile = ".cache/confirm.json"

// Assurance is the shape every assurance value takes in the lattice
// contracts: {state, honesty, code, params, text}.
type Assurance struct {
	State   string                 `json:"state"`
	Honesty string                 `json:"honesty"`
	Code    string                 `json:"code,omitempty"`
	Params  map[string]interface{} `json:"params,omitempty"`
	Text    string                 `json:"text,omitempty"`
}

// Record is the verdict on one decision.
type Record struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Action  string `json:"action"`
	State   string `json:"state"`
	// Via is the route the verdict came by: "review" (a platform approval
	// on the merged pull request) or "signature" (a hardware-key signature
	// over the decision). It is empty for a decision not on the default
	// branch.
	Via           string `json:"via,omitempty"`
	ApproverLogin string `json:"approver_login,omitempty"`
	// Fingerprint is the SHA256 fingerprint of the key behind a signature.
	Fingerprint string `json:"fingerprint,omitempty"`
	PR          int    `json:"pr,omitempty"`
	MergedSHA   string `json:"merged_sha,omitempty"`
	VerifiedAt  string `json:"verified_at,omitempty"`
	// Rows are the promises a promise.confirm decision covers.
	Rows []string `json:"rows,omitempty"`
	// Cases are the acceptance cases the decision recorded for its promises,
	// copied from the decision file. A reader compares them with the cases
	// now to give each case its own confirmation state.
	Cases []schema.PromiseCases `json:"cases,omitempty"`
	// CasesCheck says whether Cases is bound to what was confirmed:
	// "verified" when the decision's packet hash matched the workspace when
	// confirm ran, so the cases now are the ones the packet hashed, and Cases
	// equals them (or an intact cache verified the same list before);
	// "mismatch" when the packet matched and Cases does not; empty when it
	// could not be checked. Readers confirm a case only on "verified".
	CasesCheck string `json:"cases_check,omitempty"`
	// DecisionSHA is the commit that added the decision file on the default branch.
	DecisionSHA string `json:"decision_sha,omitempty"`
	// DecisionPath is the decision file a signature verdict is about, from
	// the repository root. Readers re-verify the signature there.
	DecisionPath string `json:"decision_path,omitempty"`
	// SignatureBase is the commit allowed_signers was read at: the first
	// parent of the default-branch commit that introduced the signature.
	SignatureBase string `json:"signature_base,omitempty"`
	// PRAuthor is who opened the pull request that merged the decision, and
	// PRAuthorBot is true when that is a bot or agent account. Display only:
	// the signer or approver is ApproverLogin, a human login.
	PRAuthor    string    `json:"pr_author,omitempty"`
	PRAuthorBot bool      `json:"pr_author_bot,omitempty"`
	Assurance   Assurance `json:"assurance"`
	// Notes are informational facts about the verdict, such as
	// SOLE_HUMAN_APPROVER. A note never changes the verdict's state.
	Notes []Note `json:"notes,omitempty"`
}

// Note is one informational fact about a verdict: {code, params, text}.
type Note struct {
	Code   string                 `json:"code"`
	Params map[string]interface{} `json:"params,omitempty"`
	Text   string                 `json:"text"`
}

// Codes of the confirm.routes setting and of the notes on a verdict.
const (
	// CodeRouteNotAccepted is a decision confirmed by a route the
	// workspace's confirm.routes leaves out: it reads pending.
	CodeRouteNotAccepted = "ROUTE_NOT_ACCEPTED"
	// CodeRoutesUnreadable is a confirmed verdict read while config.yaml
	// could not be read: which routes count is unknown, so it reads unknown.
	CodeRoutesUnreadable = "CONFIRM_ROUTES_UNREADABLE"
	// CodeSoleHumanApprover notes a review by the repository's only human
	// with write access on a pull request a bot or agent account opened.
	CodeSoleHumanApprover = "SOLE_HUMAN_APPROVER"
)

// Routes a verdict comes by.
const (
	ViaReview    = "review"
	ViaSignature = "signature"
)

// Method says how a human verdict was reached, as every display of it must:
// "human (signed by <login>, <fingerprint>)" or "human (approved by <login>
// on PR #<n>)". It is "" for a record that names no one.
// @enforces confirm:INV-15
func (r Record) Method() string {
	switch {
	case r.ApproverLogin == "":
		return ""
	case r.Via == ViaSignature:
		return "signed by " + r.ApproverLogin + ", " + signature.Short(r.Fingerprint)
	case r.PR != 0:
		return "approved by " + r.ApproverLogin + " on PR #" + itoa(r.PR)
	}
	return "approved by " + r.ApproverLogin
}

// Stamp is the provenance of a confirm.json: the commit and CI run that
// produced it, the platform, and a digest of its verdicts.
type Stamp = stamp.Stamp

// File is lattice/.cache/confirm.json: derived, cached by commit, never committed.
type File struct {
	Contract    string    `json:"contract"`
	Commit      string    `json:"commit"`
	Branch      string    `json:"branch,omitempty"`
	GeneratedAt string    `json:"generated_at"`
	Offline     bool      `json:"offline,omitempty"`
	Assurance   Assurance `json:"assurance"`
	Records     []Record  `json:"records"`
	Warnings    []string  `json:"warnings,omitempty"`
	// Signers is how allowed_signers on the branch came to trust whom: the
	// root of trust and every change since. It is a report, for a human to
	// read; readers re-verify signatures from git, not from it.
	Signers *Signers `json:"signers,omitempty"`
	// Stamp says what produced the file. A file without one, or whose digest
	// no longer matches its records, is read as unknown, never confirmed.
	Stamp *Stamp `json:"stamp,omitempty"`
}

// Signers is the trust report of allowed_signers: ROOT_SIGNERS names the
// commit trust started at.
type Signers struct {
	Root      string             `json:"root,omitempty"`
	Changes   []signature.Change `json:"changes"`
	Assurance Assurance          `json:"assurance"`
}

// signersReport follows allowed_signers on the signature route's branch. It
// is nil when the branch never had the file.
func signersReport(r *signature.Repo) *Signers {
	if r == nil {
		return nil
	}
	t := r.Trust(r.Ref)
	if t.Failure == nil && len(t.Changes) == 0 {
		return nil
	}
	out := &Signers{Root: t.Root, Changes: t.Changes}
	if out.Changes == nil {
		out.Changes = []signature.Change{}
	}
	switch {
	case t.Failure != nil:
		out.Assurance = Assurance{State: StateUnknown, Honesty: "fact", Code: t.Failure.Code, Text: t.Failure.Text}
	case t.Root == "":
		out.Assurance = Assurance{State: StateUnknown, Honesty: "fact", Code: signature.CodeEdited, Text: "allowed_signers grants no signer yet"}
	default:
		root := t.Changes[0]
		for _, c := range t.Changes {
			if c.Code == signature.CodeRootSigners {
				root = c
			}
		}
		out.Assurance = Assurance{State: "pass", Honesty: "fact", Code: signature.CodeRootSigners,
			Params: map[string]interface{}{"root": t.Root},
			Text:   "trust starts at " + short(t.Root) + ": its allowed_signers is the root, accepted as is (" + strings.Join(root.Added, ", ") + ")"}
	}
	return out
}

// SetAside reports the code a reader gave this file when it refused its
// verdicts (CONFIRM_UNSTAMPED, CONFIRM_TAMPERED, CONFIRM_TRACKED), or "".
func (f File) SetAside() string {
	switch f.Assurance.Code {
	case stamp.CodeUnstamped, stamp.CodeTampered, stamp.CodeTracked, stamp.CodeNotCI:
		return f.Assurance.Code
	}
	return ""
}

// PacketFunc recomputes a decision's packet hash as of now. current is false
// when the BRD is no longer at the decision's version (or is gone), so the
// hash cannot be compared.
type PacketFunc func(d Decision) (hash string, current bool)

// Input is what Compute reads.
type Input struct {
	Commit string
	Branch string
	Found  []Found
	// Platform is nil when no token is configured: platform data is unknown.
	Platform Platform
	Packet   PacketFunc
	// Cache is the previous confirm.json. A decision whose platform verdict
	// is in it, at the same added commit, is not asked about again.
	Cache *File
	// Refresh ignores Cache.
	Refresh bool
	Now     func() time.Time
	// CIRun is the CI run this process is part of (results.CIRunID); empty
	// reads "local". PlatformName and Repository name the code host. All
	// three go into the file's stamp.
	CIRun        string
	PlatformName string
	Repository   string
	// Signed reads the signatures beside decision files on the default
	// branch. Nil leaves the signature route out.
	Signed *signature.Repo
	// CurrentCases stamps the cases of the given promises as they are now
	// (CasesFor over the audit rows the Packet func hashes). With it, the
	// case list of a decision whose packet still matches is checked against
	// them. Nil leaves every case list unchecked.
	CurrentCases func(promises []string) []schema.PromiseCases
	// Routes is the workspace's confirm: block. A decision confirmed only by
	// a route it leaves out reads pending (ROUTE_NOT_ACCEPTED). The zero
	// value accepts both routes.
	Routes config.Confirm
}

// Case-list checks of a record.
const (
	CasesVerified = "verified"
	CasesMismatch = "mismatch"
)

func (r Record) key() string {
	return r.ID + "|" + itoa(r.Version) + "|" + r.Action + "|" + r.DecisionSHA
}

func itoa(n int) string { return strconv.Itoa(n) }

// Compute turns the decision files seen in the repository into verdicts. It
// is the only place a decision becomes confirmed.
// @enforces confirm:INV-3, confirm:INV-4
func Compute(ctx context.Context, in Input) File {
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	f := File{Contract: Schema, Commit: in.Commit, Branch: in.Branch, GeneratedAt: now().UTC().Format(time.RFC3339), Records: []Record{}}
	ciRun := strings.TrimSpace(in.CIRun)
	if ciRun == "" {
		ciRun = stamp.LocalRun
	}
	cached := map[string]Record{}
	if reusable(in.Cache, ciRun) && !in.Refresh {
		for _, r := range in.Cache.Records {
			if r.ApproverLogin != "" && r.PR != 0 && (r.State == StateConfirmed || r.State == StateStale) {
				cached[r.key()] = r
			}
		}
	}
	if in.Platform == nil {
		f.Assurance = Assurance{State: StateUnknown, Honesty: "fact", Code: "NO_PLATFORM",
			Text: "no platform token: decisions on the default branch are unknown until `lattice confirm` runs with GITHUB_TOKEN or GH_TOKEN"}
	} else {
		f.Assurance = Assurance{State: "pass", Honesty: "fact", Code: "PLATFORM_READ", Text: "approvals read from the platform"}
	}

	keys := signingKeys{platform: in.Platform, lists: map[string]keyList{}}
	writers := writeAccess{platform: in.Platform}
	for _, fd := range in.Found {
		d := fd.Decision
		rec := Record{ID: d.ID, Version: d.Version, Action: d.Action, DecisionSHA: fd.AddedSHA, Cases: d.Cases}
		for _, r := range d.Rows {
			if d.Action == ActionPromiseConfirm {
				rec.Rows = append(rec.Rows, r.Promise)
			}
		}
		var o Outcome
		switch {
		case !fd.OnDefault:
			o = Outcome{State: StatePending, Code: "NOT_MERGED", Text: "prepared on " + fd.Ref + ", not merged to the default branch"}
		case fd.AddedSHA == "":
			o = Outcome{State: StateUnknown, Code: "NO_COMMIT", Text: "the commit that added the decision file was not found"}
		default:
			// Either accepted route suffices. A signature is checked first:
			// it needs one platform read per signer, the review route several.
			var signed *Outcome
			if in.Signed != nil {
				if ev, sf, ok := in.Signed.Evaluate(fd.Path, signatureDecision(d)); ok {
					so := keys.outcome(ctx, ev, sf)
					signed = &so
				}
			}
			verifiedAt := ""
			review := func() Outcome {
				var r Outcome
				if c, ok := cached[rec.key()]; ok {
					r = Outcome{State: StateConfirmed, Code: "APPROVED", Approver: c.ApproverLogin, PR: c.PR, Merged: c.MergedSHA,
						Author: c.PRAuthor, AuthorBot: c.PRAuthorBot, Text: "approved by " + c.ApproverLogin + " (cached)"}
					verifiedAt = c.VerifiedAt
				} else if in.Platform == nil {
					r = Outcome{State: StateUnknown, Code: "NO_PLATFORM", Text: "no platform data for this decision"}
				} else {
					r = Verify(ctx, in.Platform, fd.AddedSHA)
				}
				r.Via = ViaReview
				return r
			}
			o = pickRoute(signed, review, in.Routes)
			if o.Via == ViaReview {
				rec.VerifiedAt = verifiedAt
				rec.PRAuthor, rec.PRAuthorBot = o.Author, o.AuthorBot
			}
		}
		if o.Via == ViaSignature {
			rec.PRAuthor, rec.PRAuthorBot = pullAuthor(ctx, in.Platform, fd.AddedSHA)
		}
		packetMatched := false
		if o.State == StateConfirmed && in.Packet != nil {
			hash, current := in.Packet(d)
			packetMatched = current && hash == d.PacketHash
			if current && hash != d.PacketHash {
				verb := "approved by "
				if o.Via == ViaSignature {
					verb = "signed by "
				}
				o.State, o.Code = StateStale, "PACKET_MISMATCH"
				o.Text = verb + o.Approver + ", but the BRD version, its promises or the diff changed since the packet was prepared"
				o.Params = map[string]interface{}{"prepared": d.PacketHash, "now": hash}
			}
		}
		rec.CasesCheck = casesCheck(d, packetMatched, in.CurrentCases, cached[rec.key()])
		rec.State, rec.ApproverLogin, rec.PR, rec.MergedSHA = o.State, o.Approver, o.PR, o.Merged
		rec.Via, rec.Fingerprint, rec.SignatureBase = o.Via, o.Fingerprint, o.Base
		if o.Via == ViaSignature {
			rec.DecisionPath = fd.Path
		}
		if (o.State == StateConfirmed || o.State == StateStale) && rec.VerifiedAt == "" {
			rec.VerifiedAt = now().UTC().Format(time.RFC3339)
		}
		rec.Assurance = Assurance{State: o.State, Honesty: "fact", Code: o.Code, Params: o.Params, Text: o.Text}
		if n, ok := writers.soleApprover(ctx, rec); ok {
			rec.Notes = append(rec.Notes, n)
		}
		f.Records = append(f.Records, rec)
	}
	f.Signers = signersReport(in.Signed)
	sort.SliceStable(f.Records, func(i, j int) bool {
		a, b := f.Records[i], f.Records[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.Action < b.Action
	})
	return Seal(f, Stamp{Commit: in.Commit, CIRun: ciRun, Platform: in.PlatformName, Repository: in.Repository})
}

// casesCheck binds a decision's case list to what was confirmed. While the
// decision's packet hash still matches the workspace, the cases now are the
// ones the packet hashed, so the list must equal them, promise by promise,
// for rows of the decision only: verified, else mismatch. A list that cannot
// be checked now (the packet changed, or the BRD moved on) stays verified
// only when an intact cache verified this very list for this decision.
// Anything else is unchecked, and no reader confirms a case on it.
// @enforces confirm:INV-20
func casesCheck(d Decision, packetMatched bool, current func([]string) []schema.PromiseCases, cached Record) string {
	if len(d.Cases) == 0 {
		return ""
	}
	if packetMatched && current != nil {
		rows := map[string]bool{}
		for _, r := range d.Rows {
			rows[r.Promise] = true
		}
		refs := make([]string, 0, len(d.Cases))
		for _, pc := range d.Cases {
			if !rows[pc.Promise] {
				return CasesMismatch
			}
			refs = append(refs, pc.Promise)
		}
		if !sameCases(d.Cases, current(refs)) {
			return CasesMismatch
		}
		return CasesVerified
	}
	if cached.CasesCheck == CasesVerified && sameCases(cached.Cases, d.Cases) {
		return CasesVerified
	}
	return ""
}

func sameCases(a, b []schema.PromiseCases) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Promise != b[i].Promise || len(a[i].Cases) != len(b[i].Cases) {
			return false
		}
		for j := range a[i].Cases {
			if a[i].Cases[j] != b[i].Cases[j] {
				return false
			}
		}
	}
	return true
}

// pullAuthor names who opened the pull request that merged a signed
// decision, and whether that is a bot or agent account. The signature's
// verdict does not depend on it: it is shown so a reviewer sees that a
// machine prepared what a human signed.
// @enforces confirm:INV-18
func pullAuthor(ctx context.Context, p Platform, sha string) (string, bool) {
	pa, ok := p.(PullAuthors)
	if p == nil || !ok || sha == "" {
		return "", false
	}
	login, kind, err := pa.PullAuthor(ctx, sha)
	if err != nil || login == "" {
		return "", false
	}
	return login, IsBot(login, kind) || schema.IsAgentActor(login)
}

// reusable reports whether a previous confirm.json's verdicts may stand in
// for asking the platform again. Only an intact stamp counts, and a CI run
// never takes a verdict from a local file: otherwise a hand-written cache
// would come back out of CI with a CI stamp.
// @enforces confirm:INV-6
func reusable(cache *File, ciRun string) bool {
	if cache == nil || cache.Stamp == nil || cache.SetAside() != "" {
		return false
	}
	if cache.Stamp.Local() && ciRun != stamp.LocalRun {
		return false
	}
	return true
}

// Seal stamps f with what produced it and a digest of its records. A reader
// accepts the verdicts only while the digest matches.
func Seal(f File, st Stamp) File {
	if f.Records == nil {
		f.Records = []Record{}
	}
	if st.CIRun == "" {
		st.CIRun = stamp.LocalRun
	}
	st.Commit = f.Commit
	sealed, err := stamp.Seal(st, f.Records)
	if err != nil {
		f.Stamp = nil
		return f
	}
	f.Stamp = &sealed
	return f
}

// Offline reads the cache only. A missing cache reads unknown, never confirmed.
// @enforces confirm:INV-4
func Offline(cache *File, commit string) File {
	if cache == nil {
		return File{Contract: Schema, Commit: commit, Offline: true, Records: []Record{},
			Assurance: Assurance{State: StateUnknown, Honesty: "fact", Code: "NO_CACHE",
				Text: "no confirm data: run `lattice confirm` online in CI first"}}
	}
	out := *cache
	out.Offline = true
	if cache.SetAside() == "" {
		out.Assurance = Assurance{State: "pass", Honesty: "fact", Code: "FROM_CACHE", Text: "read from the cache written at " + short(cache.Commit)}
	}
	return out
}

// CachePath is where confirm.json lives.
func CachePath(latticeDir string) string {
	return filepath.Join(latticeDir, filepath.FromSlash(CacheFile))
}

// LoadCache reads confirm.json; nil when absent, unreadable or another schema.
// A file whose stamp is missing or broken, or that git tracks, is returned
// set aside: its assurance carries the code (CONFIRM_UNSTAMPED,
// CONFIRM_TAMPERED, CONFIRM_TRACKED) and every record reads unknown, so no
// reader takes a confirmed verdict from it.
// @enforces confirm:INV-6
func LoadCache(latticeDir string) *File {
	p := CachePath(latticeDir)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var f File
	if json.Unmarshal(data, &f) != nil || f.Contract != Schema {
		return nil
	}
	if res := stamp.Check(p, data); !res.OK() {
		f = SetAside(f, res.Code, res.Text)
		return &f
	}
	f = Recheck(latticeDir, f)
	routes, err := config.LoadConfirm(latticeDir)
	f = Narrow(f, routes, err)
	return &f
}

// Recheck re-verifies, from the repository, every signature verdict f
// claims, as every reader of confirm.json does. A claim that no longer holds
// (the signature is gone or does not verify, a key was removed from
// allowed_signers, or the record was written by hand) reads unknown. Only
// the platform's word that the key is the signer's is taken from the file.
// @enforces confirm:INV-13
func Recheck(latticeDir string, f File) File {
	var idx []int
	var claims []signature.Claim
	for i, r := range f.Records {
		if r.Via == ViaSignature && r.State == StateConfirmed {
			idx = append(idx, i)
			claims = append(claims, signature.Claim{ID: r.ID, Version: r.Version, Action: r.Action, Rows: r.Rows,
				DecisionPath: r.DecisionPath, Login: r.ApproverLogin, Fingerprint: r.Fingerprint})
		}
	}
	if len(claims) == 0 {
		return f
	}
	recs := append([]Record(nil), f.Records...)
	for j, fail := range signature.Recheck(latticeDir, f.Branch, claims) {
		if fail == nil {
			continue
		}
		r := &recs[idx[j]]
		r.State = StateUnknown
		r.Assurance = Assurance{State: StateUnknown, Honesty: "fact", Code: fail.Code, Text: "the signature verdict does not re-verify: " + fail.Text}
	}
	f.Records = recs
	return f
}

// SetAside returns f with every verdict read as unknown and the file's
// assurance carrying code and text. The records stay, for display.
func SetAside(f File, code, text string) File {
	recs := make([]Record, len(f.Records))
	for i, r := range f.Records {
		r.State = StateUnknown
		r.Assurance = Assurance{State: StateUnknown, Honesty: "fact", Code: code, Text: text}
		recs[i] = r
	}
	f.Records = recs
	f.Assurance = Assurance{State: StateUnknown, Honesty: "fact", Code: code, Text: text}
	return f
}

// SaveCache writes confirm.json, sealed: a file without a stamp is stamped as
// a local run at its commit.
func SaveCache(latticeDir string, f File) error {
	st := Stamp{Commit: f.Commit, CIRun: stamp.LocalRun}
	if f.Stamp != nil {
		st = *f.Stamp
	}
	f = Seal(f, st)
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	p := CachePath(latticeDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// Text is a one-line-per-decision rendering.
func (f File) Text() string {
	var b strings.Builder
	for _, r := range f.Records {
		who := ""
		if m := r.Method(); m != "" {
			who = " (" + m + ")"
			if r.State == StateConfirmed {
				who = " human" + who
			}
		}
		b.WriteString(r.State + "  " + r.Action + " " + r.ID + " v" + itoa(r.Version) + who + "  [" + r.Assurance.Code + "]\n")
		if r.PRAuthor != "" && r.Via == ViaSignature {
			note := "    note: the pull request was opened by " + r.PRAuthor
			if r.PRAuthorBot {
				note += ", a bot or agent account: it prepared the decision"
				if r.ApproverLogin != "" {
					note += ", and " + r.ApproverLogin + " signed it"
				}
			}
			b.WriteString(note + "\n")
		}
		for _, n := range r.Notes {
			b.WriteString("    note: " + n.Text + "  [" + n.Code + "]\n")
		}
	}
	if len(f.Records) == 0 {
		b.WriteString("no decisions (" + f.Assurance.Code + ")\n")
	}
	if s := f.Signers; s != nil {
		b.WriteString("signers  " + s.Assurance.Text + "  [" + s.Assurance.Code + "]\n")
		for _, c := range s.Changes {
			if c.Code != signature.CodeRootSigners {
				b.WriteString("signers  " + short(c.Commit) + " " + c.Text + "  [" + c.Code + "]\n")
			}
		}
	}
	return b.String()
}
