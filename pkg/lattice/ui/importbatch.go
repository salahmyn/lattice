package ui

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/importer"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// applyImportBatch is the UI-side mirror of the v0.2.1 CLI bulk-review
// driver. It writes accepted draft manifests into features/, records
// every decision in the session, and runs PromoteParents once at the
// end. The same byte-for-byte output as `lattice import review
// --from-file decisions.yaml`.
type importBatchResult struct {
	Candidate string `json:"candidate"`
	Decision  string `json:"decision"`
	Manifest  string `json:"manifest,omitempty"`
	Skipped   string `json:"skipped,omitempty"`
}

func (s *Server) applyImportBatch(r *http.Request, decisions map[string]string) ([]importBatchResult, []string, error) {
	if len(decisions) == 0 {
		return nil, nil, nil
	}
	cf, err := importer.LoadCandidates(filepath.Join(s.ws.ImportDir(), importer.CandidatesFileName))
	if err != nil {
		return nil, nil, err
	}
	sessPath := filepath.Join(s.ws.ImportDir(), importer.SessionFileName)
	sess, _ := importer.LoadSession(sessPath)
	if sess.Decisions == nil {
		sess.Decisions = map[string]string{}
	}
	candByID := map[string]importer.Candidate{}
	for _, c := range cf.Candidates {
		candByID[c.ID] = c
	}
	results := make([]importBatchResult, 0, len(decisions))
	wroteAccept := false
	for candID, decision := range decisions {
		cand, ok := candByID[candID]
		if !ok {
			results = append(results, importBatchResult{Candidate: candID, Decision: decision,
				Skipped: "unknown candidate (not in current scan)"})
			continue
		}
		res := importBatchResult{Candidate: candID, Decision: decision}
		if decision == importer.DecisionAccepted {
			draftPath := filepath.Join(s.ws.ImportDir(), importer.DraftsDirName, cand.ID+".yaml")
			m, derr := schema.LoadManifest(draftPath)
			if derr != nil {
				res.Skipped = "no draft (run `lattice import draft` first)"
				results = append(results, res)
				continue
			}
			mp := filepath.Join(s.ws.FeaturesDir(),
				filepath.FromSlash(strings.ReplaceAll(m.ID, ".", "/"))+".yaml")
			if _, err := os.Stat(mp); err == nil {
				res.Skipped = "manifest already exists: " + mp
				results = append(results, res)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(mp), 0o755); err != nil {
				return results, nil, err
			}
			if err := schema.SaveCanonical(mp, m); err != nil {
				return results, nil, err
			}
			res.Manifest = mp
			wroteAccept = true
		}
		sess.Decisions[candID] = decision
		results = append(results, res)
	}
	sess.Status = importer.StatusReviewing
	if err := importer.SaveSession(sessPath, sess); err != nil {
		return results, nil, err
	}
	var promoted []string
	if wroteAccept {
		promoted, err = importer.PromoteParents(s.ws.FeaturesDir())
		if err != nil {
			return results, nil, err
		}
	}
	return results, promoted, nil
}
