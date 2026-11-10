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
// and decides which of them the check may count. Under ci, a file counts only
// when it is stamped with the head commit and a CI run; anything else is set
// aside and reads unknown (EVIDENCE_NOT_CI). Without ci, every file counts
// (a local run is advice) and Tied only reports the stamp.
// @enforces check:INV-4
func Gather(latticeDir, head string, ci bool) Evidence {
	var ev Evidence

	rs := results.Load(latticeDir)
	ev.Results = judge(len(rs.Results) > 0, rs.Commit, rs.CIRun, head, ci)

	if cs, ok := coverage.Load(latticeDir); ok {
		ev.Coverage = judge(true, cs.Commit, cs.CIRun, head, ci)
	} else {
		ev.Coverage = EvidenceItem{Counted: true}
	}

	var bf struct {
		Commit string `json:"commit"`
		CIRun  string `json:"ci_run"`
	}
	if data, err := os.ReadFile(filepath.Join(latticeDir, ".cache", "results", "breaks.json")); err == nil && json.Unmarshal(data, &bf) == nil {
		ev.Breaks = judge(true, bf.Commit, bf.CIRun, head, ci)
	} else {
		ev.Breaks = EvidenceItem{Counted: true}
	}

	if ev.Results.Counted && ev.Results.Present {
		cases, _, fail, _ := rs.Tally()
		ev.Suite = SuiteResult{Present: true, Cases: cases, Failed: fail, Red: rs.FailedCases()}
	}
	return ev
}

// judge reads one file's stamp.
func judge(present bool, commit, ciRun, head string, ci bool) EvidenceItem {
	it := EvidenceItem{Present: present, Commit: commit, CIRun: ciRun, Counted: true}
	if !present {
		return it
	}
	local := ciRun == "" || ciRun == results.LocalRun
	it.Tied = commit != "" && commit == head && !local
	if ci && !it.Tied {
		it.Counted = false
		it.Code = CodeEvidenceNotCI
		switch {
		case local:
			it.Text = "evidence was produced outside CI (ci_run " + orNone(ciRun) + "): it reads unknown"
		default:
			it.Text = fmt.Sprintf("evidence is for commit %s, not head %s: it reads unknown", short(commit), short(head))
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
