package check

// @module-feature check

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/results"
)

// Gather reads the provenance stamps of the evidence files under latticeDir
// and decides which of them the check may count. Evidence stamped with a
// commit that is not head (or with no commit) is set aside in every mode,
// local runs included: it says nothing about this commit, so it reads unknown
// (EVIDENCE_STALE), never a pass and never a fall. Under ci, a file must also
// come from a CI run; anything else is set aside (EVIDENCE_NOT_CI). Without
// ci, a local run at head counts (a local run is advice) and Tied only
// reports the stamp.
//
// The stamp is a commit, not a tree: evidence ingested at head while the
// working tree has uncommitted changes still counts, since the stamp cannot
// tell whether the run saw those edits.
// @enforces check:INV-4
// @enforces check:INV-8
func Gather(latticeDir, head string, ci bool) Evidence {
	return gather(latticeDir, head, ci, true)
}

// GatherSnapshot reads the evidence for the audit snapshot of head, the
// base side of a later check. It keeps the snapshot's own rule: only ci sets
// evidence aside. The stale rule belongs to the head side of a check; it does
// not rewrite what a snapshot records.
func GatherSnapshot(latticeDir, head string, ci bool) Evidence {
	return gather(latticeDir, head, ci, false)
}

func gather(latticeDir, head string, ci, stale bool) Evidence {
	var ev Evidence
	j := func(name string, present bool, commit, ciRun string) EvidenceItem {
		return judge(name, present, commit, ciRun, head, ci, stale)
	}

	rs := results.Load(latticeDir)
	ev.Results = j("results", len(rs.Results) > 0, rs.Commit, rs.CIRun)

	if cs, ok := coverage.Load(latticeDir); ok {
		ev.Coverage = j("coverage", true, cs.Commit, cs.CIRun)
	} else {
		ev.Coverage = EvidenceItem{Counted: true}
	}

	var bf struct {
		Commit string `json:"commit"`
		CIRun  string `json:"ci_run"`
	}
	if data, err := os.ReadFile(filepath.Join(latticeDir, ".cache", "results", "breaks.json")); err == nil && json.Unmarshal(data, &bf) == nil {
		ev.Breaks = j("breaks", true, bf.Commit, bf.CIRun)
	} else {
		ev.Breaks = EvidenceItem{Counted: true}
	}

	if ev.Results.Counted && ev.Results.Present {
		cases, _, fail, _ := rs.Tally()
		ev.Suite = SuiteResult{Present: true, Cases: cases, Failed: fail, Red: rs.FailedCases()}
	}
	return ev
}

// staleWords are the subject of the stale text and how to refresh the file.
var staleWords = map[string][2]string{
	"results":  {"results are", "re-run the tests and ingest (lattice results ingest)"},
	"coverage": {"coverage is", "re-run the tests with coverage and ingest it"},
	"breaks":   {"break results are", "re-run lattice break"},
}

// judge reads one file's stamp. Under ci, a file not tied to head and a CI
// run reads EVIDENCE_NOT_CI, as before. With stale set, a file whose commit
// is not head reads EVIDENCE_STALE in a local run too.
func judge(name string, present bool, commit, ciRun, head string, ci, stale bool) EvidenceItem {
	it := EvidenceItem{Present: present, Commit: commit, CIRun: ciRun, Counted: true}
	if !present {
		return it
	}
	local := ciRun == "" || ciRun == results.LocalRun
	it.Tied = commit != "" && commit == head && !local
	switch {
	case ci && !it.Tied:
		it.Counted = false
		it.Code = CodeEvidenceNotCI
		switch {
		case local:
			it.Text = "evidence was produced outside CI (ci_run " + orNone(ciRun) + "): it reads unknown"
		default:
			it.Text = fmt.Sprintf("evidence is for commit %s, not head %s: it reads unknown", short(commit), short(head))
		}
	case stale && (commit == "" || commit != head):
		it.Counted = false
		it.Code = CodeEvidenceStale
		w := staleWords[name]
		if commit == "" {
			it.Text = fmt.Sprintf("%s stamped with no commit, not HEAD %s: %s", w[0], short(head), w[1])
		} else {
			it.Text = fmt.Sprintf("%s for %s, not HEAD %s: %s", w[0], short(commit), short(head), w[1])
		}
	}
	return it
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	if sha == "" {
		return "(none)"
	}
	return sha
}

// Apply removes from src the evidence the check set aside, so audit.Compute
// reads it as absent (unknown), never as a pass.
func (e Evidence) Apply(src audit.Sources) audit.Sources {
	if !e.Results.Counted {
		src.Results = results.Set{Results: map[string]results.Outcome{}}
		src.Stamps.Results = audit.ResultsInput{}
	}
	if !e.Coverage.Counted {
		src.Coverage = nil
		src.Stamps.Coverage = audit.CoverageInput{}
	}
	if !e.Breaks.Counted {
		src.Breaks = nil
		src.Stamps.Breaks = audit.BreaksInput{}
	}
	return src
}

// counted says which of the three files the check counts.
func (e Evidence) counted() [3]bool {
	return [3]bool{e.Results.Counted, e.Coverage.Counted, e.Breaks.Counted}
}

// --- base audit snapshots ---

// snapshotDir is where audit snapshots live, relative to lattice/. The files
// are derived and never committed.
const snapshotDir = ".cache/audit"

// SnapshotPath is the snapshot file of one commit.
func SnapshotPath(latticeDir, sha string) string {
	return filepath.Join(latticeDir, filepath.FromSlash(snapshotDir), sha+".json")
}

// SaveSnapshot writes the audit report of a commit as
// lattice/.cache/audit/<sha>.json. It skips a report with no commit or of a
// dirty tree: that report is not the commit's.
func SaveSnapshot(latticeDir string, rep audit.Report) (string, error) {
	if rep.Commit == "" || rep.Dirty {
		return "", nil
	}
	p := SnapshotPath(latticeDir, rep.Commit)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", err
	}
	return p, os.WriteFile(p, data, 0o644)
}

// LoadSnapshot reads the audit snapshot of sha. It returns nil and the
// reason when there is none or it is not that commit's.
func LoadSnapshot(latticeDir, sha string) (*audit.Report, string) {
	data, err := os.ReadFile(SnapshotPath(latticeDir, sha))
	if err != nil {
		return nil, "no base audit: " + snapshotDir + "/" + short(sha) + ".json is missing"
	}
	var rep audit.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, "no base audit: the snapshot does not parse (" + err.Error() + ")"
	}
	if rep.Schema != audit.Schema {
		return nil, "no base audit: the snapshot is " + rep.Schema + ", not " + audit.Schema
	}
	if rep.Commit != sha {
		return nil, "no base audit: the snapshot is for " + short(rep.Commit) + ", not " + short(sha)
	}
	return &rep, ""
}

// --- waivers ---

// waiversFile is the human-written exceptions file, relative to lattice/.
const waiversFile = "waivers.yaml"

// LoadWaivers reads lattice/waivers.yaml: a list, or a map with a
// `waivers:` list. Only subject, kind, code and reason are read here; the
// check shows waivers and never counts them as proof.
func LoadWaivers(latticeDir string) []Waiver {
	data, err := os.ReadFile(filepath.Join(latticeDir, waiversFile))
	if err != nil {
		return nil
	}
	type entry struct {
		Subject string `yaml:"subject"`
		Kind    string `yaml:"kind"`
		Code    string `yaml:"code"`
		Reason  string `yaml:"reason"`
	}
	var list []entry
	if err := yaml.Unmarshal(data, &list); err != nil {
		var wrapped struct {
			Waivers []entry `yaml:"waivers"`
		}
		if yaml.Unmarshal(data, &wrapped) != nil {
			return nil
		}
		list = wrapped.Waivers
	}
	var out []Waiver
	for _, e := range list {
		if strings.TrimSpace(e.Subject) == "" {
			continue
		}
		out = append(out, Waiver{Subject: strings.TrimSpace(e.Subject), Kind: e.Kind, Code: e.Code, Reason: e.Reason})
	}
	return out
}
