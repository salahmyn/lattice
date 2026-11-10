package ui

// @module-feature api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/check"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/contract"
	"github.com/salahmyn/lattice/pkg/lattice/prepare"
)

// --- GET /api/v2/check ---

// v2Check is `lattice check --json`: check.Run, the function the CLI calls,
// and its report as it is. ?base= names the ref to compare with; ?ci=1 counts
// only evidence stamped with the head commit and a CI run. The report is not
// reshaped and gets no new arithmetic; it is the only payload that may read
// ASSURED or BROKEN, because it is computed against a base.
func (s *Server) v2Check(wr http.ResponseWriter, r *http.Request) {
	base := strings.TrimSpace(r.URL.Query().Get("base"))
	if base == "" {
		writeV2Error(wr, http.StatusBadRequest, "BASE_REQUIRED", "?base=<rev> is required", nil)
		return
	}
	kg, err := s.readGraph(r.Context())
	if err != nil {
		writeV2Error(wr, http.StatusInternalServerError, "GRAPH_FAILED", err.Error(), nil)
		return
	}
	ci := r.URL.Query().Get("ci") == "1" || r.URL.Query().Get("ci") == "true"
	rep, err := check.Run(r.Context(), s.ws, kg, check.RunOptions{Base: base, CI: ci})
	if err != nil {
		var re *check.RunError
		code, status := "CHECK_FAILED", http.StatusInternalServerError
		if errors.As(err, &re) {
			code = re.Code
			if re.Code == check.RunBadBase || re.Code == check.RunNotARepo || re.Code == check.RunGitFailed {
				status = http.StatusBadRequest
			}
		}
		writeV2Error(wr, status, code, err.Error(), nil)
		return
	}
	writeV2(wr, http.StatusOK, rep, contract.LintOptions{AllowAssured: true})
}

// --- POST /api/v2/prepare ---

// prepareRequest is the body of POST /api/v2/prepare. The action is a
// confirm.Action* value: brd.approve, brd.return, brd.ack, brd.overturn,
// promise.confirm or waiver.add.
type prepareRequest struct {
	Action     string               `json:"action"`
	ID         string               `json:"id"`
	Version    int                  `json:"version"`
	Reason     string               `json:"reason"`
	Refs       []string             `json:"refs"`
	Waiver     *confirm.WaiverEntry `json:"waiver"`
	PacketHash string               `json:"packet_hash"`
}

func isLoopbackAddr(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func refusal(code, text, next string) *prepare.Refusal {
	return &prepare.Refusal{Contract: prepare.Schema, Refused: code, Text: text, Next: next}
}

func (s *Server) refuse(wr http.ResponseWriter, status int, ref *prepare.Refusal) {
	ref.Commit, _ = s.gitState()
	writeV2(wr, status, ref, contract.LintOptions{})
}

// sameOrigin refuses a request a page on another origin sent.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	return true
}

// prepareEnv is what `lattice prepare` reads, from the world of this request.
func (s *Server) prepareEnv(w *v2world) prepare.Env {
	cfg, _ := config.Load(s.ws.LatticeDir)
	return prepare.Env{
		Git: confirm.Git{Dir: s.repoDir()}, LatticeDir: s.ws.LatticeDir, KG: w.kg, Report: w.report,
		Config: cfg, Actor: s.opts.Actor, Cache: w.conf,
	}
}

// v2Prepare validates a decision against the kernel and writes a decision
// branch: prepare.Run, as the CLI calls it. It is the only write the API
// makes, and it writes one local branch. It never pushes, never opens a pull
// request and never returns anything that confirms: CONFIRMED comes only from
// `lattice confirm`, reading the platform's approval of the merged PR.
//
// It is open only to a client on this machine that holds the CSRF token
// /api/v2/meta issued to it, and from this origin.
// @enforces api:INV-3
func (s *Server) v2Prepare(wr http.ResponseWriter, r *http.Request) {
	if !loopbackRequest(r) {
		s.refuse(wr, http.StatusForbidden, refusal("NOT_LOOPBACK", "prepare is open only to a client on this machine",
			"run `lattice prepare` where the repository is, or open the UI on localhost"))
		return
	}
	got := r.Header.Get(csrfHeader)
	if s.csrf == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.csrf)) != 1 {
		s.refuse(wr, http.StatusForbidden, refusal("CSRF_REQUIRED", "this request carries no valid "+csrfHeader+" token",
			"read the token from GET /api/v2/meta and send it in the "+csrfHeader+" header"))
		return
	}
	if !sameOrigin(r) {
		s.refuse(wr, http.StatusForbidden, refusal("CSRF_ORIGIN", "this request came from another origin", "send it from the UI this server serves"))
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		s.refuse(wr, http.StatusUnsupportedMediaType, refusal("BAD_REQUEST", "the body must be application/json", "send Content-Type: application/json"))
		return
	}
	var req prepareRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.refuse(wr, http.StatusBadRequest, refusal("BAD_REQUEST", "the body is not a prepare request: "+err.Error(),
			`send {"action": "brd.approve", "id": ..., "version": ..., "packet_hash": ...}`))
		return
	}

	w := s.loadOr500(wr, r)
	if w == nil {
		return
	}
	env := s.prepareEnv(w)
	res, ref := prepare.Run(env, prepare.Request{
		Action: req.Action, ID: req.ID, Version: req.Version, Reason: req.Reason, Refs: req.Refs,
		Waiver: req.Waiver, PacketHash: req.PacketHash,
	})
	if ref != nil {
		writeV2(wr, http.StatusUnprocessableEntity, ref, contract.LintOptions{})
		return
	}
	writeV2(wr, http.StatusOK, res, contract.LintOptions{})
}
