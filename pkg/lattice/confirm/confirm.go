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

	"github.com/salahmyn/lattice/pkg/lattice/confirm/stamp"
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
	ID            string `json:"id"`
	Version       int    `json:"version"`
	Action        string `json:"action"`
	State         string `json:"state"`
	ApproverLogin string `json:"approver_login,omitempty"`
	PR            int    `json:"pr,omitempty"`
	MergedSHA     string `json:"merged_sha,omitempty"`
	VerifiedAt    string `json:"verified_at,omitempty"`
	// Rows are the promises a promise.confirm decision covers.
	Rows []string `json:"rows,omitempty"`
	// DecisionSHA is the commit that added the decision file on the default branch.
	DecisionSHA string    `json:"decision_sha,omitempty"`
	Assurance   Assurance `json:"assurance"`
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
	// Stamp says what produced the file. A file without one, or whose digest
	// no longer matches its records, is read as unknown, never confirmed.
	Stamp *Stamp `json:"stamp,omitempty"`
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
}

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

	for _, fd := range in.Found {
		d := fd.Decision
		rec := Record{ID: d.ID, Version: d.Version, Action: d.Action, DecisionSHA: fd.AddedSHA}
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
			if c, ok := cached[rec.key()]; ok {
				o = Outcome{State: StateConfirmed, Code: "APPROVED", Approver: c.ApproverLogin, PR: c.PR, Merged: c.MergedSHA,
					Text: "approved by " + c.ApproverLogin + " (cached)"}
				rec.VerifiedAt = c.VerifiedAt
			} else if in.Platform == nil {
				o = Outcome{State: StateUnknown, Code: "NO_PLATFORM", Text: "no platform data for this decision"}
			} else {
				o = Verify(ctx, in.Platform, fd.AddedSHA)
			}
		}
		if o.State == StateConfirmed && in.Packet != nil {
			if hash, current := in.Packet(d); current && hash != d.PacketHash {
				o.State, o.Code = StateStale, "PACKET_MISMATCH"
				o.Text = "approved by " + o.Approver + ", but the BRD version, its promises or the diff changed since the packet was prepared"
				o.Params = map[string]interface{}{"prepared": d.PacketHash, "now": hash}
			}
		}
		rec.State, rec.ApproverLogin, rec.PR, rec.MergedSHA = o.State, o.Approver, o.PR, o.Merged
		if (o.State == StateConfirmed || o.State == StateStale) && rec.VerifiedAt == "" {
			rec.VerifiedAt = now().UTC().Format(time.RFC3339)
		}
		rec.Assurance = Assurance{State: o.State, Honesty: "fact", Code: o.Code, Params: o.Params, Text: o.Text}
		f.Records = append(f.Records, rec)
	}
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
	}
	return &f
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
		if r.ApproverLogin != "" {
			who = " by " + r.ApproverLogin + " (PR #" + itoa(r.PR) + ")"
		}
		b.WriteString(r.State + "  " + r.Action + " " + r.ID + " v" + itoa(r.Version) + who + "  [" + r.Assurance.Code + "]\n")
	}
	if len(f.Records) == 0 {
		b.WriteString("no decisions (" + f.Assurance.Code + ")\n")
	}
	return b.String()
}
