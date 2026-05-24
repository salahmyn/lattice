package ui

import (
	"net/http"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/importer"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/validate"
)

// coveragePayload renders the v0.2.1 three-ratio adoption dashboard as
// the same shape the CLI's lattice coverage command emits.
type coveragePayload struct {
	Discovery     importer.DiscoveryCoverage     `json:"discovery"`
	Documentation importer.DocumentationCoverage `json:"documentation"`
	Verification  importer.VerificationCoverage  `json:"verification"`
	// BRD is the v0.5.0 4th ratio: features with an approved upstream BRD.
	// Always present in the payload (zeroes when no BRD axis is in use)
	// so the UI can render the card uniformly.
	BRD importer.BRDCoverage `json:"brd"`
}

func (s *Server) apiCoverage(w http.ResponseWriter, r *http.Request) {
	payload, err := s.computeCoverage(r)
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, payload)
}

func (s *Server) pageCoverage(w http.ResponseWriter, r *http.Request) {
	payload, err := s.computeCoverage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Sort per-package rows by ratio ascending so adoption gaps surface
	// at the top — UI surfaces ought to answer "where do I go next?"
	pkgs := append([]importer.PackageCoverage(nil), payload.Discovery.ByPackage...)
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Ratio != pkgs[j].Ratio {
			return pkgs[i].Ratio < pkgs[j].Ratio
		}
		return pkgs[i].Package < pkgs[j].Package
	})
	s.render(w, "coverage.html", pageData{
		Title:    "Coverage",
		Active:   "coverage",
		JSONHref: "/api/v1/coverage",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "Coverage"}},
		Body: map[string]interface{}{
			"Discovery":     payload.Discovery,
			"Documentation": payload.Documentation,
			"Verification":  payload.Verification,
			"BRD":           payload.BRD,
			"Packages":      pkgs,
		},
	})
}

// computeCoverage is the shared computation behind the API and the page.
// It tolerates a missing import session — discovery just comes back zero
// in that case, which the page surfaces as "run `lattice import scan`".
func (s *Server) computeCoverage(r *http.Request) (coveragePayload, error) {
	kg, err := s.graph(r.Context())
	if err != nil {
		return coveragePayload{}, err
	}
	out := coveragePayload{}
	cfPath := s.ws.ImportDir() + "/" + importer.CandidatesFileName
	if cf, err := importer.LoadCandidates(cfPath); err == nil {
		out.Discovery = cf.Coverage.Discovery
		sessPath := s.ws.ImportDir() + "/" + importer.SessionFileName
		if sess, err := importer.LoadSession(sessPath); err == nil {
			out.Documentation = importer.ComputeDocumentation(cf, sess.Decisions)
		}
	}
	cfg, _ := config.Load(s.ws.LatticeDir)
	violations := validate.Validate(kg, cfg, validate.Options{ReviewMode: s.ws.Review})
	out.Verification = importer.ComputeVerification(kg.Features, violations)
	out.BRD = importer.ComputeBRD(kg.Features, kg.BRDs)
	return out, nil
}

// validationPayload groups violations by code so the dashboard has a
// natural "issues to triage" structure — exactly the CLI's --json output
// re-shaped.
type validationPayload struct {
	OK         bool                            `json:"ok"`
	Errors     int                             `json:"errors"`
	Warnings   int                             `json:"warnings"`
	ByCode     map[string][]schema.Violation   `json:"by_code"`
	Violations []schema.Violation              `json:"violations"`
}

func (s *Server) apiValidation(w http.ResponseWriter, r *http.Request) {
	payload, err := s.computeValidation(r)
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, payload)
}

func (s *Server) pageValidation(w http.ResponseWriter, r *http.Request) {
	payload, err := s.computeValidation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Order codes by descending count so the noisiest category renders
	// first — that's where a triage session starts.
	codes := make([]string, 0, len(payload.ByCode))
	for c := range payload.ByCode {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool {
		ci, cj := payload.ByCode[codes[i]], payload.ByCode[codes[j]]
		if len(ci) != len(cj) {
			return len(ci) > len(cj)
		}
		return codes[i] < codes[j]
	})
	s.render(w, "validation.html", pageData{
		Title:    "Validation",
		Active:   "validation",
		JSONHref: "/api/v1/validation",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "Validation"}},
		Body: map[string]interface{}{
			"OK":       payload.OK,
			"Errors":   payload.Errors,
			"Warnings": payload.Warnings,
			"ByCode":   payload.ByCode,
			"Codes":    codes,
		},
	})
}

func (s *Server) computeValidation(r *http.Request) (validationPayload, error) {
	kg, err := s.graph(r.Context())
	if err != nil {
		return validationPayload{}, err
	}
	cfg, _ := config.Load(s.ws.LatticeDir)
	violations := validate.Validate(kg, cfg, validate.Options{ReviewMode: s.ws.Review})
	payload := validationPayload{
		Violations: violations,
		ByCode:     map[string][]schema.Violation{},
	}
	for _, v := range violations {
		payload.ByCode[v.Code] = append(payload.ByCode[v.Code], v)
		if v.IsError() {
			payload.Errors++
		} else {
			payload.Warnings++
		}
	}
	payload.OK = payload.Errors == 0
	return payload, nil
}
