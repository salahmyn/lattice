package brd

// @module-feature brds.council

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Council approval (v0.12): in a mode without a brd_approval human gate, a
// council of agent seats may approve a BRD under a human-pinned brd_approve
// mandate. Each seat reviews one BRD version and votes; the decision is
// unanimous; the human it acts for gets the summary and the reasons, and
// may overturn it. Money and regulation stay human-only.

// CouncilDir is where votes and decision summaries live:
// lattice/decisions/brd-council/.
func CouncilDir(latticeDir string) string {
	return filepath.Join(latticeDir, "decisions", "brd-council")
}

// VotesPath is the vote file for one BRD version.
func VotesPath(latticeDir, id string, version int) string {
	return filepath.Join(CouncilDir(latticeDir), fmt.Sprintf("%s-v%d.votes.yaml", id, version))
}

// SummaryPath is the human-facing decision summary for one BRD version.
func SummaryPath(latticeDir, id string, version int) string {
	return filepath.Join(CouncilDir(latticeDir), fmt.Sprintf("%s-v%d.md", id, version))
}

// CouncilRecord is what is kept for one BRD version: the seats' votes and,
// once the delegating human overturned the council, who did so and why.
type CouncilRecord struct {
	BRD        string                  `yaml:"brd"`
	Version    int                     `yaml:"version"`
	Votes      []schema.BRDCouncilVote `yaml:"votes"`
	Overturned *Overturn               `yaml:"overturned,omitempty"`
}

// Overturn is a human's reversal of a council decision on one version.
type Overturn struct {
	By     string `yaml:"by"`
	At     string `yaml:"at"`
	Reason string `yaml:"reason"`
}

// LoadRecord returns the council record of one BRD version. A missing file
// is an empty record.
func LoadRecord(latticeDir, id string, version int) (CouncilRecord, error) {
	rec := CouncilRecord{BRD: id, Version: version}
	data, err := os.ReadFile(VotesPath(latticeDir, id, version))
	if errors.Is(err, os.ErrNotExist) {
		return rec, nil
	}
	if err != nil {
		return rec, err
	}
	err = yaml.Unmarshal(data, &rec)
	return rec, err
}

// LoadVotes returns the votes recorded for one BRD version.
func LoadVotes(latticeDir, id string, version int) ([]schema.BRDCouncilVote, error) {
	rec, err := LoadRecord(latticeDir, id, version)
	return rec.Votes, err
}

func saveRecord(latticeDir string, rec CouncilRecord) error {
	data, err := yaml.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(CouncilDir(latticeDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(VotesPath(latticeDir, rec.BRD, rec.Version), data, 0o644)
}

// MarkOverturned records that the human overturned the council on this
// version. The council may not decide that version again: the BRD has to
// be revised (a new version) first.
func MarkOverturned(latticeDir, id string, version int, o Overturn) error {
	rec, err := LoadRecord(latticeDir, id, version)
	if err != nil {
		return err
	}
	rec.Overturned = &o
	return saveRecord(latticeDir, rec)
}

// RecordVote stores a seat's vote on the BRD's current version, replacing
// an earlier vote by the same seat. The seat must belong to the council,
// the verdict must be approve or changes, and the reason is required: a
// vote without a reason gives the human nothing to read.
// @enforces brds.council:INV-5
func RecordVote(latticeDir string, b schema.BRD, council config.Council, v schema.BRDCouncilVote) error {
	if !contains(council.Seats, v.Seat) {
		return fmt.Errorf("seat %q is not on the council (%s)", v.Seat, strings.Join(council.Seats, ", "))
	}
	if v.Verdict != schema.VerdictApprove && v.Verdict != schema.VerdictChanges {
		return fmt.Errorf("verdict %q: want approve or changes", v.Verdict)
	}
	if strings.TrimSpace(v.Actor) == "" {
		return fmt.Errorf("a vote names its actor: pass --actor (or set LATTICE_ACTOR)")
	}
	if strings.TrimSpace(v.Reason) == "" {
		return fmt.Errorf("a vote needs a reason")
	}
	rec, err := LoadRecord(latticeDir, b.ID, b.Version)
	if err != nil {
		return err
	}
	if rec.Overturned != nil {
		return fmt.Errorf("%s v%d was overturned by %s; revise the BRD before the council reviews it again", b.ID, b.Version, rec.Overturned.By)
	}
	kept := rec.Votes[:0]
	for _, old := range rec.Votes {
		if old.Seat != v.Seat {
			kept = append(kept, old)
		}
	}
	kept = append(kept, v)
	sort.SliceStable(kept, func(i, j int) bool {
		return seatIndex(council.Seats, kept[i].Seat) < seatIndex(council.Seats, kept[j].Seat)
	})
	rec.Votes = kept
	return saveRecord(latticeDir, rec)
}

// MaxTier is the highest criterion tier in the BRD (1 when it has none).
func MaxTier(b schema.BRD) int {
	max := 1
	for _, c := range b.SuccessCriteria {
		if t := c.EffectiveTier(); t > max {
			max = t
		}
	}
	return max
}

// regulatedKinds are constraint kinds only a human may sign off.
var regulatedKinds = map[schema.BRDConstraintKind]bool{
	schema.ConstraintRegulatory: true, schema.ConstraintLegal: true, schema.ConstraintFinancial: true,
}

// HumanOnly reports why a BRD is outside any council's reach: a tier-3
// (money or regulatory) criterion, or a regulatory, legal or financial
// constraint. Empty means a council may decide it.
// @enforces brds.council:INV-2
func HumanOnly(b schema.BRD) []string {
	var why []string
	for _, c := range b.SuccessCriteria {
		if c.EffectiveTier() >= 3 {
			why = append(why, fmt.Sprintf("criterion %s is tier 3 (money or regulatory)", c.ID))
		}
	}
	for _, c := range b.Constraints {
		if regulatedKinds[c.Kind] {
			why = append(why, fmt.Sprintf("it has a %s constraint", c.Kind))
		}
	}
	return why
}

// CouncilVerdict is what the recorded votes add up to.
type CouncilVerdict struct {
	// Blockers are reasons the council cannot decide at all: the mode,
	// the mandate, the human-only floor, a missing or conflicted seat.
	Blockers []string
	// Approved is true when nothing blocks and every seat approved.
	Approved bool
	// Changes lists the "changes" votes when the council sends it back.
	Changes []schema.BRDCouncilVote
}

// Assess checks whether a council may approve the BRD's current version
// under the named mandate on the given date (YYYY-MM-DD), and what its
// recorded votes decide. It is the one rule behind `lattice brd decide`
// and the BRD_COUNCIL_INVALID validation.
func Assess(b schema.BRD, auto config.Autonomy, mandateID string, votes []schema.BRDCouncilVote, today string) CouncilVerdict {
	return assess(b, auto, mandateID, votes, today, true)
}

// RecheckApproval re-checks a council approval recorded on a BRD, as of the
// day it was given: the mandate was pinned and covered it, the BRD is not
// human-only, and every seat approved. The current mode is not re-checked:
// turning council mode off later does not void approvals it already gave.
// Empty means the approval holds.
func RecheckApproval(b schema.BRD, auto config.Autonomy) []string {
	a := b.Approval
	if a == nil || a.Mandate == "" {
		return nil
	}
	if a.Decision == nil {
		return []string{"the approval records no council decision"}
	}
	day := a.ApprovedAt
	if len(day) >= 10 {
		day = day[:10]
	}
	v := assess(b, auto, a.Mandate, a.Decision.Votes, day, false)
	out := v.Blockers
	if len(out) == 0 && !v.Approved {
		out = append(out, "not every seat approved")
	}
	if m, ok := auto.FindMandate(a.Mandate); ok && !strings.EqualFold(a.DelegatedBy, m.DelegatedBy) {
		out = append(out, fmt.Sprintf("the approval names %s, but mandate %s acts for %s", a.DelegatedBy, m.ID, m.DelegatedBy))
	}
	return out
}

// @enforces brds.council:INV-1, brds.council:INV-3, brds.council:INV-4
func assess(b schema.BRD, auto config.Autonomy, mandateID string, votes []schema.BRDCouncilVote, today string, checkMode bool) CouncilVerdict {
	var out CouncilVerdict
	block := func(format string, args ...any) { out.Blockers = append(out.Blockers, fmt.Sprintf(format, args...)) }

	if checkMode && !auto.CouncilMayApprove() {
		block("the autonomy mode keeps BRD approval with a human (set autonomy.default_mode: council)")
	}
	m, ok := auto.FindMandate(mandateID)
	switch {
	case !ok:
		block("mandate %q is not pinned in lattice/config.yaml; agents never create their own mandates", mandateID)
	case m.Grants != config.GrantBRDApprove:
		block("mandate %s grants %q, not %s", m.ID, m.Grants, config.GrantBRDApprove)
	case strings.TrimSpace(m.DelegatedBy) == "":
		block("mandate %s names no delegated_by human", m.ID)
	case m.Council == nil || len(m.Council.Seats) == 0:
		block("mandate %s declares no council seats", m.ID)
	case m.Council.Quorum != "" && m.Council.Quorum != "unanimous":
		block("mandate %s quorum %q: only unanimous is supported", m.ID, m.Council.Quorum)
	case !m.CoversBRD(MaxTier(b), today):
		block("mandate %s does not cover this BRD (tier %d, tier_max %d, expires %q)", m.ID, MaxTier(b), max(m.TierMax, 1), m.Expires)
	}
	for _, why := range HumanOnly(b) {
		block("human-only: %s", why)
	}
	if len(out.Blockers) > 0 {
		return out
	}

	bySeat := map[string]schema.BRDCouncilVote{}
	for _, v := range votes {
		bySeat[v.Seat] = v
	}
	actors := map[string]string{}
	for _, seat := range m.Council.Seats {
		v, ok := bySeat[seat]
		if !ok {
			block("seat %s has not voted on v%d", seat, b.Version)
			continue
		}
		if author := b.Provenance.AuthoredBy; author != "" && strings.EqualFold(v.Actor, author) {
			block("seat %s is held by %s, who wrote this version", seat, v.Actor)
		}
		if other, dup := actors[strings.ToLower(v.Actor)]; dup {
			block("%s holds both seat %s and seat %s; each seat needs its own agent", v.Actor, other, seat)
		}
		actors[strings.ToLower(v.Actor)] = seat
		if v.Verdict == schema.VerdictChanges {
			out.Changes = append(out.Changes, v)
		}
	}
	out.Approved = len(out.Blockers) == 0 && len(out.Changes) == 0
	return out
}

// RenderSummary is the decision as the human reads it: what was decided,
// why, the evidence the engine computed, and each seat's vote.
func RenderSummary(b schema.BRD, d schema.BRDCouncilDecision, mandate config.Mandate, at string) string {
	var s strings.Builder
	fmt.Fprintf(&s, "# Council decision: %s v%d\n\n", b.ID, b.Version)
	fmt.Fprintf(&s, "- **Outcome:** %s\n", d.Outcome)
	fmt.Fprintf(&s, "- **BRD:** %s\n", b.Title)
	fmt.Fprintf(&s, "- **Decided:** %s, under mandate `%s` for %s\n", at, mandate.ID, mandate.DelegatedBy)
	if d.Evidence != "" {
		fmt.Fprintf(&s, "- **Evidence:** %s\n", d.Evidence)
	}
	fmt.Fprintf(&s, "\n## Summary\n\n%s\n", d.Summary)
	if d.Why != "" {
		fmt.Fprintf(&s, "\n## Why\n\n%s\n", d.Why)
	}
	s.WriteString("\n## Votes\n\n| Seat | Agent | Verdict | Reason |\n|---|---|---|---|\n")
	for _, v := range d.Votes {
		fmt.Fprintf(&s, "| %s | %s | %s | %s |\n", v.Seat, v.Actor, v.Verdict, strings.ReplaceAll(v.Reason, "|", "\\|"))
	}
	if d.Outcome == "approved" {
		fmt.Fprintf(&s, "\n## Your review\n\nThe BRD is approved now. Acknowledge with `lattice brd ack %s --by <you>`, "+
			"or send it back with `lattice brd overturn %s --by <you> --reason \"...\"`.\n", b.ID, b.ID)
	}
	return s.String()
}

// WriteSummary writes the decision summary for the BRD's current version.
// An earlier round on the same version (a "changes" decision, then a
// re-vote) is kept below the new one, so the human sees the whole history.
func WriteSummary(latticeDir string, b schema.BRD, body string) (string, error) {
	if err := os.MkdirAll(CouncilDir(latticeDir), 0o755); err != nil {
		return "", err
	}
	path := SummaryPath(latticeDir, b.ID, b.Version)
	if old, err := os.ReadFile(path); err == nil && len(old) > 0 {
		body = strings.TrimRight(body, "\n") + earlierMarker + string(old)
	}
	return path, os.WriteFile(path, []byte(body), 0o644)
}

// AppendSummary adds a section (an acknowledgement, an overturn) to the
// decision summary of one BRD version.
func AppendSummary(latticeDir, id string, version int, section string) error {
	path := SummaryPath(latticeDir, id, version)
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// The section belongs to the current decision, above any earlier round.
	text, earlier := string(old), ""
	if i := strings.Index(text, earlierMarker); i >= 0 {
		text, earlier = text[:i], text[i:]
	}
	return os.WriteFile(path, []byte(strings.TrimRight(text, "\n")+"\n\n"+strings.TrimRight(section, "\n")+"\n"+earlier), 0o644)
}

const earlierMarker = "\n\n---\n\n## Earlier round\n\n"

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func seatIndex(seats []string, seat string) int {
	for i, s := range seats {
		if s == seat {
			return i
		}
	}
	return len(seats)
}
