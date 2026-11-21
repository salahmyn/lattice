package confirm

// @module-feature confirm

import (
	"context"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// pickRoute chooses the verdict of one decision from its routes. A confirmed
// signature on an accepted route wins without asking for reviews; then a
// confirmed review on an accepted route. Otherwise a signature's own failure
// is shown when that route is accepted, and a confirmation by a route
// routes leaves out reads pending, ROUTE_NOT_ACCEPTED. With both routes
// accepted it is the either-route rule: the signature, else the review, else
// the signature's failure.
// @enforces confirm:INV-22
func pickRoute(signed *Outcome, review func() Outcome, routes config.Confirm) Outcome {
	sig, rev := routes.Accepts(ViaSignature), routes.Accepts(ViaReview)
	if signed != nil && signed.State == StateConfirmed && sig {
		return *signed
	}
	r := review()
	switch {
	case r.State == StateConfirmed && rev:
		return r
	case signed != nil && sig:
		return *signed
	case r.State == StateConfirmed:
		return notAccepted(r, routes)
	case signed != nil && signed.State == StateConfirmed:
		return notAccepted(*signed, routes)
	case signed != nil:
		return *signed
	}
	return r
}

// notAccepted turns a confirmation by a route the workspace does not accept
// into pending, keeping who approved or signed it for display.
func notAccepted(o Outcome, routes config.Confirm) Outcome {
	method := Record{Via: o.Via, ApproverLogin: o.Approver, PR: o.PR, Fingerprint: o.Fingerprint}.Method()
	o.State, o.Code = StatePending, CodeRouteNotAccepted
	o.Params = map[string]interface{}{"via": o.Via, "routes": acceptedRoutes(routes)}
	o.Text = routes.NotAccepted(method, o.Via)
	return o
}

func acceptedRoutes(routes config.Confirm) []string {
	out := []string{}
	for _, k := range config.KnownRoutes {
		if routes.Accepts(k) {
			out = append(out, k)
		}
	}
	return out
}

// route is a record's route: its via, or review for a record written
// before records carried one.
func (r Record) route() string {
	if r.Via == "" && r.PR != 0 {
		return ViaReview
	}
	return r.Via
}

// Narrow applies the workspace's confirm.routes to a confirm.json as every
// reader does, so a file written before the setting changed narrows too: a
// confirmed or stale verdict by a route routes leaves out reads pending
// (ROUTE_NOT_ACCEPTED). When config.yaml could not be read (loadErr), which
// routes count is unknown, and every confirmed or stale verdict reads
// unknown (CONFIRM_ROUTES_UNREADABLE). It never makes a verdict confirmed.
// @enforces confirm:INV-22
func Narrow(f File, routes config.Confirm, loadErr error) File {
	if loadErr == nil && !routes.Narrowed() {
		return f
	}
	recs := append([]Record(nil), f.Records...)
	for i := range recs {
		r := &recs[i]
		if r.State != StateConfirmed && r.State != StateStale {
			continue
		}
		switch {
		case loadErr != nil:
			r.State = StateUnknown
			r.Assurance = Assurance{State: StateUnknown, Honesty: "fact", Code: CodeRoutesUnreadable,
				Text: config.Unreadable(loadErr)}
		case !routes.Accepts(r.route()):
			r.State = StatePending
			r.Assurance = Assurance{State: StatePending, Honesty: "fact", Code: CodeRouteNotAccepted,
				Params: map[string]interface{}{"via": r.route(), "routes": acceptedRoutes(routes)},
				Text:   routes.NotAccepted(r.Method(), r.route())}
		}
	}
	f.Records = recs
	return f
}

// writeAccess asks the platform once per run who can push to the
// repository. ok is false when the platform cannot list collaborators or
// the list cannot be read: then nothing is said.
type writeAccess struct {
	platform Platform
	asked    bool
	humans   []string
	ok       bool
}

func (w *writeAccess) humanWriters(ctx context.Context) ([]string, bool) {
	if w.asked {
		return w.humans, w.ok
	}
	w.asked = true
	l, ok := w.platform.(CollaboratorLister)
	if w.platform == nil || !ok {
		return nil, false
	}
	list, err := l.Collaborators(ctx)
	if err != nil {
		return nil, false
	}
	for _, c := range list {
		if c.Write && !IsBot(c.Login, c.Type) && !schema.IsAgentActor(c.Login) {
			w.humans = append(w.humans, c.Login)
		}
	}
	w.ok = true
	return w.humans, w.ok
}

// soleApprover notes a review verdict whose pull request a bot or agent
// account opened and whose approver is the repository's only human with
// write access: there, the review route proves only that an account other
// than the author approved. It is information: it reads the record and
// never changes it. With no readable collaborator list it says nothing.
// @enforces confirm:INV-23
func (w *writeAccess) soleApprover(ctx context.Context, rec Record) (Note, bool) {
	if rec.Via != ViaReview || (rec.State != StateConfirmed && rec.State != StateStale) ||
		!rec.PRAuthorBot || rec.PRAuthor == "" || rec.ApproverLogin == "" {
		return Note{}, false
	}
	humans, ok := w.humanWriters(ctx)
	if !ok || len(humans) != 1 || !strings.EqualFold(humans[0], rec.ApproverLogin) {
		return Note{}, false
	}
	return Note{Code: CodeSoleHumanApprover,
		Params: map[string]interface{}{"author": rec.PRAuthor, "approver": rec.ApproverLogin, "pr": rec.PR},
		Text: "PR #" + itoa(rec.PR) + " was opened by " + rec.PRAuthor + ", a bot or agent account, and " + rec.ApproverLogin +
			", who approved it, is the only human with write access: the review route here proves only that an account other than the author approved; " +
			"to require a hardware-key signature, set confirm.routes: [signature] in lattice/config.yaml"}, true
}
