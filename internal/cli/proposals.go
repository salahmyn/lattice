package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/salahmyn/lattice/pkg/lattice/blocks"
	"github.com/salahmyn/lattice/pkg/lattice/store"
)

// proposalError carries the error code of a bad initiative or evidence record.
type proposalError struct {
	code string
	err  error
}

func (e *proposalError) Error() string { return e.err.Error() }

func proposalCode(err error) string {
	var pe *proposalError
	if errors.As(err, &pe) {
		return pe.code
	}
	return "STORE_READ"
}

// loadProposals reads the initiative records and their evidence records.
func loadProposals(ctx context.Context, st store.Store) ([]blocks.Proposal, error) {
	inits, err := st.List(ctx, "initiative")
	if err != nil {
		return nil, err
	}
	evs, err := st.List(ctx, "evidence")
	if err != nil {
		return nil, err
	}
	byID := map[string]*blocks.Evidence{}
	for _, r := range evs {
		ev, err := blocks.ParseEvidence(r.Body)
		if err != nil {
			return nil, &proposalError{"EVIDENCE_INVALID", fmt.Errorf("evidence %s: %w", r.ID, err)}
		}
		if ev.ID != r.ID {
			return nil, &proposalError{"EVIDENCE_INVALID", fmt.Errorf("evidence %s: its id is %q", r.ID, ev.ID)}
		}
		byID[r.ID] = &ev
	}
	var ps []blocks.Proposal
	for _, r := range inits {
		in, err := blocks.ParseInitiative(r.Body, "initiative "+r.ID)
		if err != nil {
			return nil, &proposalError{"INITIATIVE_INVALID", err}
		}
		ps = append(ps, blocks.Proposal{Initiative: in, Evidence: byID[in.ID]})
		delete(byID, in.ID)
	}
	for _, r := range evs { // evidence without an initiative: Build warns
		if ev := byID[r.ID]; ev != nil {
			ps = append(ps, blocks.Proposal{Evidence: ev})
		}
	}
	return ps, nil
}

// checkRecord validates the record kinds refresh reads, so a bad write cannot break every later refresh.
func checkRecord(kind string, body []byte, label string) (string, error) {
	switch kind {
	case "initiative":
		if _, err := blocks.ParseInitiative(body, label); err != nil {
			return "INITIATIVE_INVALID", err
		}
	case "evidence":
		if _, err := blocks.ParseEvidence(body); err != nil {
			return "EVIDENCE_INVALID", fmt.Errorf("%s: %w", label, err)
		}
	}
	return "", nil
}
