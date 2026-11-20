package confirm

// @module-feature confirm

import (
	"bytes"
	"context"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
)

// SigningKeyLister is a Platform that also lists a user's SSH signing keys,
// in authorized_keys form. It anchors the signature route: a key counts only
// when the code host lists it among the signer's signing keys. A Platform
// that does not implement it leaves every signature unknown.
type SigningKeyLister interface {
	SigningKeys(ctx context.Context, login string) ([]string, error)
}

type keyList struct {
	keys []ssh.PublicKey
	err  error
}

// signingKeys asks the platform once per login per run. Nothing is cached
// across runs: a key removed from the platform un-confirms on the next run.
type signingKeys struct {
	platform Platform
	lists    map[string]keyList
}

func (s *signingKeys) list(ctx context.Context, l SigningKeyLister, login string) keyList {
	k := strings.ToLower(login)
	if kl, ok := s.lists[k]; ok {
		return kl
	}
	var kl keyList
	lines, err := l.SigningKeys(ctx, login)
	if err != nil {
		kl.err = err
	}
	for _, line := range lines {
		if pk, _, _, _, perr := ssh.ParseAuthorizedKey([]byte(line)); perr == nil {
			kl.keys = append(kl.keys, pk)
		}
	}
	s.lists[k] = kl
	return kl
}

// outcome turns what the repository says about a signature into a verdict,
// adding the platform anchor. A definite rejection reads pending; a check
// that cannot be made reads unknown, never confirmed.
// @enforces confirm:INV-12
func (s *signingKeys) outcome(ctx context.Context, ev signature.Evidence, f *signature.Failure) Outcome {
	o := Outcome{Via: ViaSignature, Fingerprint: ev.Fingerprint, Base: ev.Base}
	if f != nil {
		o.State, o.Code, o.Text = StatePending, f.Code, f.Text
		if f.Unknown {
			o.State = StateUnknown
		}
		return o
	}
	who := strings.Join(ev.Logins, ", ")
	lister, ok := s.platform.(SigningKeyLister)
	if s.platform == nil || !ok {
		o.State, o.Code = StateUnknown, signature.CodeAnchorUnknown
		o.Text = "no platform token: whether key " + signature.Short(ev.Fingerprint) + " is among the GitHub signing keys of " + who + " is unknown"
		return o
	}
	var errs []string
	for _, login := range ev.Logins {
		kl := s.list(ctx, lister, login)
		if kl.err != nil {
			errs = append(errs, login+": "+kl.err.Error())
			continue
		}
		for _, k := range kl.keys {
			if bytes.Equal(k.Marshal(), ev.Key.Marshal()) {
				o.State, o.Code, o.Approver = StateConfirmed, signature.CodeSigned, login
				o.Text = "signed by " + login + " with hardware key " + signature.Short(ev.Fingerprint) +
					", listed in allowed_signers at " + short(ev.Base) + " and among " + login + "'s GitHub signing keys"
				return o
			}
		}
	}
	if len(errs) > 0 {
		o.State, o.Code = StateUnknown, signature.CodeAnchorUnknown
		o.Text = "the GitHub signing keys could not be read (" + strings.Join(errs, "; ") + "): the signature is unknown"
		return o
	}
	o.State, o.Code = StatePending, signature.CodeNotOnHost
	o.Text = "key " + signature.Short(ev.Fingerprint) + " is in allowed_signers for " + who + " but is not among their GitHub signing keys"
	return o
}

// signatureDecision is the part of a decision a signature covers.
func signatureDecision(d Decision) signature.Decision {
	sd := signature.Decision{Action: d.Action, ID: d.ID, Version: d.Version, PacketHash: d.PacketHash}
	for _, r := range d.Rows {
		sd.Rows = append(sd.Rows, struct {
			Promise string `yaml:"promise"`
		}{r.Promise})
	}
	return sd
}

// SignatureTuple is the tuple a signature over d covers in a repository:
// what `lattice sign` signs and `lattice confirm` verifies.
func SignatureTuple(d Decision, repository string) signature.Tuple {
	return signatureDecision(d).Tuple(repository)
}
