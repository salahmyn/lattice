package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/blocks"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// quietStore opens the workspace store without reporting, for commands that
// can do without it.
func quietStore(io *IO) (store.Store, *workspace.Workspace, error) {
	ws, err := workspace.Open(io.Repo)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(context.Background(), ws)
	return st, ws, err
}

// initiativeRecords returns the initiatives kept as store records.
func initiativeRecords(io *IO) ([]schema.Initiative, error) {
	st, _, err := quietStore(io)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	recs, err := st.List(context.Background(), "initiative")
	if err != nil {
		return nil, err
	}
	var out []schema.Initiative
	for _, r := range recs {
		in, err := blocks.ParseInitiative(r.Body, "initiative "+r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
}

// putRecord writes a record, reporting a stale version the CLI way.
func putRecord(io *IO, st store.Store, r store.Record) (store.Record, error) {
	out, err := st.Put(context.Background(), r)
	var stale *store.StaleError
	if errors.As(err, &stale) {
		return out, io.fail("STALE_VERSION", err.Error(), map[string]string{"current_version": stale.Current})
	}
	if err != nil {
		return out, io.fail("STORE_WRITE", err.Error(), nil)
	}
	return out, nil
}

func newInitiativeAddCommand(io *IO) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "add <id> --file <initiative.yaml>",
		Short: "Create an initiative record (with its pins and map) in the workspace store",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := os.ReadFile(file)
			if err != nil {
				return io.fail("READ", err.Error(), nil)
			}
			in, err := blocks.ParseInitiative(body, file)
			if err != nil {
				return io.fail("INITIATIVE_INVALID", err.Error(), nil)
			}
			if in.ID != args[0] {
				return io.fail("INITIATIVE_INVALID", fmt.Sprintf("%s has id %q, not %q", file, in.ID, args[0]), nil)
			}
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			r, err := putRecord(io, st, store.Record{Kind: "initiative", ID: in.ID, Body: body})
			if err != nil {
				return err
			}
			if io.JSON {
				return io.printJSON(r)
			}
			io.printf("added initiative %s [%s] version %s\n", r.ID, in.Status, r.Version)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "initiative YAML")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newInitiativeEvidenceCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "evidence <id> <evidence.json>",
		Short: "Attach evidence/v1 to an initiative, replacing what it had",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := os.ReadFile(args[1])
			if err != nil {
				return io.fail("READ", err.Error(), nil)
			}
			ev, err := blocks.ParseEvidence(body)
			if err != nil {
				return io.fail("EVIDENCE_INVALID", fmt.Sprintf("%s: %v", args[1], err), nil)
			}
			if ev.ID != args[0] {
				return io.fail("EVIDENCE_INVALID", fmt.Sprintf("%s is evidence for %q, not %q", args[1], ev.ID, args[0]), nil)
			}
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			if _, err := st.Get(ctx, "initiative", ev.ID); errors.Is(err, store.ErrNotFound) {
				return io.fail("INITIATIVE_NOT_FOUND", "initiative not found: "+ev.ID, "add it first: lattice initiative add")
			} else if err != nil {
				return io.fail("STORE_READ", err.Error(), nil)
			}
			cur, err := st.Get(ctx, "evidence", ev.ID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return io.fail("STORE_READ", err.Error(), nil)
			}
			r, err := putRecord(io, st, store.Record{Kind: "evidence", ID: ev.ID, Version: cur.Version, Body: body})
			if err != nil {
				return err
			}
			if io.JSON {
				return io.printJSON(r)
			}
			io.printf("evidence for %s: %d verdicts, %d gaps, %d payload rows; version %s\n",
				ev.ID, len(ev.Verdicts), len(ev.Gaps), len(ev.Payload), r.Version)
			return nil
		},
	}
}

func newInitiativeStatusCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "status <id> <status>",
		Short: "Move an initiative record to another lifecycle status",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			status := schema.InitiativeStatus(args[1])
			if !blocks.ValidStatus(status) {
				return io.fail("BAD_STATUS", fmt.Sprintf("unknown status %q", args[1]),
					"use proposed, accepted, in_progress, paused, complete or cancelled")
			}
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			cur, err := st.Get(context.Background(), "initiative", args[0])
			if errors.Is(err, store.ErrNotFound) {
				return io.fail("INITIATIVE_NOT_FOUND", "initiative record not found: "+args[0], nil)
			}
			if err != nil {
				return io.fail("STORE_READ", err.Error(), nil)
			}
			body, err := setTopLevel(cur.Body, "status", string(status))
			if err != nil {
				return io.fail("INITIATIVE_INVALID", err.Error(), nil)
			}
			r, err := putRecord(io, st, store.Record{Kind: "initiative", ID: cur.ID, Version: cur.Version, Body: body})
			if err != nil {
				return err
			}
			if io.JSON {
				return io.printJSON(r)
			}
			io.printf("initiative %s is %s (version %s)\n", r.ID, status, r.Version)
			return nil
		},
	}
}

// setTopLevel sets one top-level scalar of a YAML mapping, keeping the rest of
// the document (comments included) as written.
func setTopLevel(body []byte, key, value string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("the record is not a YAML mapping")
	}
	m := doc.Content[0]
	found := false
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].Kind, m.Content[i+1].Tag, m.Content[i+1].Value = yaml.ScalarNode, "!!str", value
			m.Content[i+1].Style = 0
			found = true
		}
	}
	if !found {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Value: value})
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// showInitiativeRecord prints an initiative kept as a store record: its pins
// checked against their clones, and the roll-ups refresh drew on its system
// block. It reports false when there is no such record.
func showInitiativeRecord(io *IO, id string) (bool, error) {
	st, ws, err := quietStore(io)
	if err != nil {
		return false, nil // no store: show reports the file initiatives' outcome
	}
	defer st.Close()
	ctx := context.Background()
	r, err := st.Get(ctx, "initiative", id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, io.fail("STORE_READ", err.Error(), nil)
	}
	in, err := blocks.ParseInitiative(r.Body, "initiative "+id)
	if err != nil {
		return true, io.fail("INITIATIVE_INVALID", err.Error(), nil)
	}
	g, err := st.Graph(ctx)
	if err != nil {
		return true, io.fail("GRAPH_READ", err.Error(), nil)
	}
	var drawn map[string]string
	for _, b := range g.Blocks {
		if b.ID == "init:"+id {
			drawn = b.Attrs
		}
	}
	type pinView struct {
		schema.Pin
		Check schema.PinCheck `json:"check"`
	}
	var pins []pinView
	for _, p := range in.Pins {
		clone := ""
		if p.Clone != "" {
			clone = ws.ResolvePath(p.Clone)
		}
		pins = append(pins, pinView{p, blocks.CheckPin(p, clone)})
	}
	if io.JSON {
		return true, io.printJSON(map[string]any{"initiative": in, "version": r.Version, "pins": pins, "drawn": drawn})
	}
	title := in.Title
	if title == "" {
		title = id
	}
	io.printf("%s  [%s]  %s\n", id, in.Status, title)
	if in.Map != nil && in.Map.Decision != "" {
		io.printf("decision: %s\n", in.Map.Decision)
	}
	io.printf("\nPins:\n")
	for _, p := range pins {
		at := p.Commit
		if len(at) > 7 {
			at = at[:7]
		}
		io.printf("  %-12s %s %s@%s: %s\n", p.Role, p.Repo, p.Ref, at, p.Check.State)
	}
	if drawn == nil {
		io.printf("\nnot drawn yet: run lattice refresh\n")
		return true, nil
	}
	io.printf("\nVerdicts: %s\n", orNone(drawn["verdicts"]))
	io.printf("Gaps: %s, blocking: %s\n", orNone(drawn["gaps"]), orNone(drawn["blocking"]))
	if drawn["unmapped"] != "" {
		io.printf("Unmapped rows: %s\n", strings.ReplaceAll(drawn["unmapped"], " ", ", "))
	}
	return true, nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
