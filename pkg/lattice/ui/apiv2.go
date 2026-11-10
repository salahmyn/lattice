package ui

// @module-feature api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/check"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/contract"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// The /api/v2 payloads are the contracts the React UI and agents bind to
// (docs/rnd UI plan, section 7). They compute nothing that counts: every
// value is read from audit, check, confirm, prepare and the graph. A field
// the kernel has no source for is contract.NotComputed. /api/v1 is unchanged.

// csrfHeader carries the token /api/v2/meta issues, on POST /api/v2/prepare.
const csrfHeader = "X-Lattice-CSRF"

func newCSRFToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		// no entropy: a token nobody can guess is better than none, and an
		// empty one is refused outright by the prepare handler
		return ""
	}
	return hex.EncodeToString(b)
}

// routesV2 wires the /api/v2 tree.
func (s *Server) routesV2(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v2/meta", s.v2Meta)
	mux.HandleFunc("GET /api/v2/queue", s.v2Queue)
	mux.HandleFunc("GET /api/v2/brds", s.v2BRDs)
	mux.HandleFunc("GET /api/v2/brds/{id}", s.v2BRD)
	mux.HandleFunc("GET /api/v2/features/{id}", s.v2Feature)
	mux.HandleFunc("GET /api/v2/check", s.v2Check)
	mux.HandleFunc("GET /api/v2/health", s.v2Health)
	mux.HandleFunc("POST /api/v2/prepare", s.v2Prepare)
}

// v2world is what one request reads: the graph, the audit over it, and the
// evidence stamps. It is built once per request and never written to.
type v2world struct {
	s       *Server
	kg      schema.KnowledgeGraph
	head    string
	dirty   *bool
	report  audit.Report
	cd      *audit.ConfirmData
	conf    *confirm.File
	ev      check.Evidence
	waivers []check.Waiver
	rows    map[string]audit.Row
}

// readGraph builds the graph without writing anything: the read-only builder
// when one is set, else the server's own.
// @enforces api:INV-4
func (s *Server) readGraph(ctx context.Context) (schema.KnowledgeGraph, error) {
	if s.opts.ReadOnlyGraphBuilder != nil {
		return s.opts.ReadOnlyGraphBuilder(ctx)
	}
	return s.graph(ctx)
}

// repoDir is the directory git commands run in: the parent of lattice/.
func (s *Server) repoDir() string { return filepath.Dir(s.ws.LatticeDir) }

func (s *Server) gitState() (head string, dirty *bool) {
	g := confirm.Git{Dir: s.repoDir()}
	h, err := g.Head()
	if err != nil {
		return "", nil
	}
	d, _ := check.GitDirty(s.repoDir())
	return h, &d
}

func (s *Server) loadWorld(ctx context.Context) (*v2world, error) {
	kg, err := s.readGraph(ctx)
	if err != nil {
		return nil, err
	}
	w := &v2world{s: s, kg: kg}
	gitHead, dirty := s.gitState()
	w.dirty = dirty
	w.head = kg.GeneratedFromCommit
	if w.head == "" {
		w.head = gitHead
	}
	isDirty := dirty != nil && *dirty
	w.report = audit.Compute(audit.Collect(s.ws.LatticeDir, kg, isDirty))
	w.cd = audit.LoadConfirm(s.ws.LatticeDir)
	w.conf = confirm.LoadCache(s.ws.LatticeDir)
	w.ev = check.Gather(s.ws.LatticeDir, w.head, false)
	w.waivers = check.LoadWaivers(s.ws.LatticeDir)
	w.rows = map[string]audit.Row{}
	for _, r := range w.report.Rows {
		w.rows[r.Promise] = r
	}
	return w, nil
}

// header is the envelope of a payload.
func (w *v2world) header(tag string) contract.Header {
	h := contract.Header{Contract: tag, Commit: w.head, EvidenceTied: w.ev.Results.Tied}
	if w.ev.Results.Present && w.ev.Results.CIRun != results.LocalRun {
		h.CIRun = w.ev.Results.CIRun
	}
	return h
}

// rowsOf lists the audit rows of the given promises, in order. A promise the
// audit does not know is left out.
func (w *v2world) rowsOf(refs []string) []audit.Row {
	var out []audit.Row
	for _, r := range refs {
		if row, ok := w.rows[r]; ok {
			out = append(out, row)
		}
	}
	return out
}

func (w *v2world) featureRows(feature string) []audit.Row {
	var out []audit.Row
	for _, r := range w.report.Rows {
		if r.Feature == feature {
			out = append(out, r)
		}
	}
	return out
}

// waiversFor lists the waivers that name a promise (or one of its signals).
func (w *v2world) waiversFor(promise string) []contract.Waiver {
	var out []contract.Waiver
	for _, x := range w.waivers {
		if x.Subject == promise || strings.HasPrefix(x.Subject, promise+"/") {
			out = append(out, contract.Waiver{Subject: x.Subject, Kind: x.Kind, Code: x.Code, Reason: x.Reason})
		}
	}
	return out
}

// entryPointsOf lists the entry points whose flow reaches any of the features.
func (w *v2world) entryPointsOf(features []string) []contract.EntryPoint {
	want := map[string]bool{}
	for _, f := range features {
		want[f] = true
	}
	var out []contract.EntryPoint
	for _, ep := range w.kg.EntryPoints {
		for i, st := range ep.Flow {
			if !want[st.Feature] {
				continue
			}
			label := ep.ID
			if ep.Trigger.Path != "" {
				label = strings.TrimSpace(ep.Trigger.Method + " " + ep.Trigger.Path)
			}
			out = append(out, contract.EntryPoint{
				ID: ep.ID, Kind: ep.Kind, Label: label,
				Live: contract.Assurance{State: contract.StatePass, Honesty: contract.UpperBound,
					Text: "reached from " + label + " (a path the code allows, not one that ran)"},
				Step: i + 1, Of: len(ep.Flow),
			})
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if out == nil {
		out = []contract.EntryPoint{}
	}
	return out
}

// writeV2 encodes a payload as the CLI does (indented, no HTML escaping),
// after Lint: a payload that shows missing evidence as a pass is a server bug
// and is never sent.
// @enforces api:INV-2
func writeV2(w http.ResponseWriter, status int, v interface{}, opt contract.LintOptions) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		writeV2Error(w, http.StatusInternalServerError, "ENCODE_FAILED", err.Error(), nil)
		return
	}
	if bad, err := contract.Lint(buf.Bytes(), opt); err != nil || len(bad) > 0 {
		msg := "payload breaks the contract floor"
		if err != nil {
			msg = err.Error()
		}
		writeV2Error(w, http.StatusInternalServerError, "CONTRACT_VIOLATION", msg, bad)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// v2Error is the body of a failed /api/v2 call.
type v2Error struct {
	Error      string   `json:"error"`
	Text       string   `json:"text"`
	Violations []string `json:"violations,omitempty"`
}

func writeV2Error(w http.ResponseWriter, status int, code, text string, violations []string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v2Error{Error: code, Text: text, Violations: violations})
}

// loadOr500 loads the world or writes the error.
func (s *Server) loadOr500(w http.ResponseWriter, r *http.Request) *v2world {
	world, err := s.loadWorld(r.Context())
	if err != nil {
		writeV2Error(w, http.StatusInternalServerError, "GRAPH_FAILED", err.Error(), nil)
		return nil
	}
	return world
}

// --- assurance conversion ---

// fromCheck converts the check package's assurance, as it is.
func fromCheck(a check.Assurance) contract.Assurance {
	return contract.Assurance{State: a.State, Honesty: a.Honesty, Code: a.Code, Params: a.Params, Text: a.Text}
}

func fromVerdict(v check.Verdict) contract.Verdict {
	return contract.Verdict{State: v.State, Code: v.Code, Params: v.Params, Text: v.Text}
}

// loopbackRequest reports whether the connection itself comes from this
// machine. The Host header is not enough, and a proxy in front hides the real
// client, so a forwarded request is never loopback.
func loopbackRequest(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-IP") != "" {
		return false
	}
	return isLoopbackAddr(r.RemoteAddr)
}
