package importer

import (
	"os"

	"gopkg.in/yaml.v3"
)

// SessionFileName is the persisted import session under the import dir.
const SessionFileName = "session.yaml"

const sessionVersion = 1

// Import session statuses, in pipeline order.
const (
	// StatusScanned: Stage 1 has run; candidates exist, none drafted.
	StatusScanned = "scanned"
	// StatusDrafted: Stage 2 has run; every candidate has a draft manifest.
	StatusDrafted = "drafted"
	// StatusReviewing: Stage 3 is under way; at least one candidate decided.
	StatusReviewing = "reviewing"
	// StatusInscribed: Stage 5 has run; accepted features are attached to code.
	StatusInscribed = "inscribed"
)

// Per-candidate review decisions.
const (
	DecisionAccepted = "accepted"
	DecisionRejected = "rejected"
)

// Session is the persisted, re-runnable import session (import/session.yaml).
// It records the scan scope and every per-candidate decision so a re-scan
// reconciles rather than discards human work.
type Session struct {
	Version    int      `yaml:"version"`
	Scopes     []string `yaml:"scopes,omitempty"`
	Status     string   `yaml:"status"`
	Candidates int      `yaml:"candidates"`
	// Decisions maps a candidate ID to a reviewer decision. Empty after a
	// scan; populated by the Stage-3 review loop.
	Decisions map[string]string `yaml:"decisions,omitempty"`
}

// LoadSession reads a session file. A missing file is reported via the
// returned error (os.IsNotExist).
func LoadSession(path string) (Session, error) {
	var s Session
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	err = yaml.Unmarshal(data, &s)
	return s, err
}

// SaveSession writes the session file to path.
func SaveSession(path string, s Session) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// NewScannedSession builds the session for a completed scan, carrying forward
// any decisions from a prior session so re-scans do not lose review work.
func NewScannedSession(prior Session, cf CandidatesFile) Session {
	s := Session{
		Version:    sessionVersion,
		Scopes:     append([]string(nil), cf.Scopes...),
		Status:     StatusScanned,
		Candidates: len(cf.Candidates),
		Decisions:  map[string]string{},
	}
	// Keep decisions whose candidate still exists after the re-scan.
	live := make(map[string]bool, len(cf.Candidates))
	for _, c := range cf.Candidates {
		live[c.ID] = true
	}
	for id, decision := range prior.Decisions {
		if live[id] {
			s.Decisions[id] = decision
		}
	}
	if len(s.Decisions) == 0 {
		s.Decisions = nil
	}
	return s
}
