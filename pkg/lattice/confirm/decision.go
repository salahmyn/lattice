// Package confirm computes CONFIRMED: which decisions a human made, as the
// platform vouches for them.
//
// `lattice prepare` writes a decision branch holding one decision file. The
// file is a request, not a record: nothing is confirmed until a pull request
// that adds it is approved by a user other than its author, on the head that
// was merged, and merged. This package finds each decision file on the default
// branch, finds the commit that added it and the pull request that merged it,
// asks the platform for that request's approving reviews, and writes the
// verdicts to lattice/.cache/confirm.json (lattice.confirm/1, derived, never
// committed). A BRD's approved_by, and anything written by `lattice brd
// approve --by`, are display only and never read here.
package confirm

// @module-feature confirm

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Decision actions.
const (
	ActionApprove        = "brd.approve"
	ActionReturn         = "brd.return"
	ActionAck            = "brd.ack"
	ActionOverturn       = "brd.overturn"
	ActionPromiseConfirm = "promise.confirm"
	ActionWaiverAdd      = "waiver.add"
)

// Actions lists every decision action.
var Actions = []string{ActionApprove, ActionReturn, ActionAck, ActionOverturn, ActionPromiseConfirm, ActionWaiverAdd}

// ApprovalsDir is where decision files live, relative to lattice/.
const ApprovalsDir = "decisions/approvals"

// WaiversFile is the protected waiver list, relative to lattice/.
const WaiversFile = "waivers.yaml"

// Row is one promise as the human saw it when the decision was prepared.
type Row struct {
	Promise   string `yaml:"promise" json:"promise"`
	Statement string `yaml:"statement,omitempty" json:"statement,omitempty"`
	Tier      int    `yaml:"tier,omitempty" json:"tier,omitempty"`
	Class     string `yaml:"class,omitempty" json:"class,omitempty"`
}

// Since names the last confirmed version a packet's structural diff is
// against, and the commit to read that version from.
type Since struct {
	Version int    `yaml:"version"`
	SHA     string `yaml:"sha"`
}

// WaiverEntry is one waiver. A waiver is shown and never counted as proof.
type WaiverEntry struct {
	Code    string `yaml:"code" json:"code"`
	Subject string `yaml:"subject" json:"subject"`
	Kind    string `yaml:"kind" json:"kind"`
	Reason  string `yaml:"reason" json:"reason"`
	ADR     string `yaml:"adr,omitempty" json:"adr,omitempty"`
	// AddedBy is the git identity of the preparer: display only.
	AddedBy string `yaml:"added_by,omitempty" json:"added_by,omitempty"`
	AddedAt string `yaml:"added_at,omitempty" json:"added_at,omitempty"`
}

// Waivers is lattice/waivers.yaml.
type Waivers struct {
	Waivers []WaiverEntry `yaml:"waivers"`
}

// WaiverKinds is the closed list of waiver kinds. The value says whether the
// kind needs an ADR.
var WaiverKinds = map[string]bool{
	"no_tool":        false, // no mutation or coverage tool exists for the language
	"generated_code": false, // the lines are generated, not authored
	"out_of_reach":   true,  // CI cannot reach the promise (hardware, third party)
	"accepted_risk":  true,  // the owner accepts the gap, with a decision on record
}

// Decision is one decision file: lattice/decisions/approvals/<stem>.yaml.
// Field order is the file's order.
type Decision struct {
	Action     string `yaml:"action"`
	ID         string `yaml:"id"`
	Version    int    `yaml:"version"`
	PacketHash string `yaml:"packet_hash"`
	Rows       []Row  `yaml:"rows,omitempty"`
	Reason     string `yaml:"reason,omitempty"`
	// VotesHash pins the council votes behind an ack or overturn.
	VotesHash string       `yaml:"votes_hash,omitempty"`
	Since     *Since       `yaml:"since,omitempty"`
	Waiver    *WaiverEntry `yaml:"waiver,omitempty"`
	// PreparedBy is the preparer's git identity: display only, never an approver.
	PreparedBy string `yaml:"prepared_by"`
	PreparedAt string `yaml:"prepared_at"`
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Slug makes a string safe for a file name and a branch name.
func Slug(s string) string {
	return strings.Trim(unsafe.ReplaceAllString(s, "-"), "-.")
}

// Stem is the file and branch stem of a decision. An approval is
// <id>-v<N>; every other action adds a suffix so several decisions on one
// version never share a file.
func (d Decision) Stem() string {
	base := fmt.Sprintf("%s-v%d", d.ID, d.Version)
	switch d.Action {
	case ActionApprove, ActionWaiverAdd:
		return base
	case ActionPromiseConfirm:
		refs := make([]string, 0, len(d.Rows))
		for _, r := range d.Rows {
			refs = append(refs, r.Promise)
		}
		return base + ".confirm-" + shortHash(strings.Join(refs, ","))
	default:
		return base + "." + strings.TrimPrefix(d.Action, "brd.")
	}
}

// Branch is the decision branch name.
func (d Decision) Branch() string { return "decision/" + d.Stem() }

// RelPath is the decision file's path relative to lattice/.
func (d Decision) RelPath() string { return path.Join(ApprovalsDir, d.Stem()+".yaml") }

// Marshal renders the decision file.
func (d Decision) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ParseDecision reads a decision file.
func ParseDecision(data []byte) (Decision, error) {
	var d Decision
	if err := yaml.Unmarshal(data, &d); err != nil {
		return d, err
	}
	switch {
	case d.ID == "" || d.Version < 1:
		return d, fmt.Errorf("decision has no id or version")
	case !knownAction(d.Action):
		return d, fmt.Errorf("decision action %q is not one of %s", d.Action, strings.Join(Actions, ", "))
	}
	return d, nil
}

func knownAction(a string) bool {
	for _, k := range Actions {
		if k == a {
			return true
		}
	}
	return false
}
