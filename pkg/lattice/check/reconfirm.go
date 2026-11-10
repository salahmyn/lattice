package check

// @module-feature check

import (
	"path"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Reconfirmed lists the promises a change re-confirms itself: a decision file
// it adds or edits under decisions/approvals approves or acks a BRD at its
// head version, or confirms the promises by name. Such a change is confirmed
// by the human approval of its own pull request when it merges, so an edit to
// a confirmed promise that carries its decision is not a weakening; without
// the decision file the edit voids the confirmation. paths are the changed
// files relative to the code root, latticeRel is the lattice/ directory
// relative to it, and read returns a file's content at head.
// @enforces check:INV-6
func Reconfirmed(paths []string, latticeRel string, read func(string) ([]byte, error), brds []schema.BRD) map[string]bool {
	dir := path.Join(latticeRel, confirm.ApprovalsDir) + "/"
	byID := map[string]schema.BRD{}
	for _, b := range brds {
		byID[b.ID] = b
	}
	out := map[string]bool{}
	for _, p := range paths {
		if !strings.HasPrefix(p, dir) || !strings.HasSuffix(p, ".yaml") {
			continue
		}
		data, err := read(p)
		if err != nil {
			continue
		}
		d, err := confirm.ParseDecision(data)
		if err != nil {
			continue
		}
		switch d.Action {
		case confirm.ActionApprove, confirm.ActionAck:
			b, ok := byID[d.ID]
			if !ok || b.Version != d.Version {
				continue // a decision for another version re-confirms nothing here
			}
			for _, ref := range confirm.PromisesOf(b) {
				out[ref] = true
			}
		case confirm.ActionPromiseConfirm:
			for _, r := range d.Rows {
				out[r.Promise] = true
			}
		}
	}
	return out
}
