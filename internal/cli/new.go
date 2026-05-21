package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func newNewCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Scaffold a manifest, initiative, task, or ADR",
	}
	cmd.AddCommand(
		newNewFeatureCommand(io),
		newNewInitiativeCommand(io),
		newNewTaskCommand(io),
		newNewADRCommand(io),
	)
	return cmd
}

func newNewFeatureCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "feature <id>",
		Short: "Scaffold a feature manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			m := schema.Manifest{
				ID: id, Version: 1, Status: schema.StatusProposal,
				Purpose: "TODO: describe what this feature does.",
				Owners:  schema.Owners{Business: "TODO-team", Engineering: "TODO-team"},
				Capabilities: []schema.Capability{{
					ID: "TODO_capability", Summary: "TODO", Rules: []string{"TODO"},
				}},
			}
			path := filepath.Join(io.Repo, "features", filepath.FromSlash(strings.ReplaceAll(id, ".", "/"))+".yaml")
			return scaffoldFile(io, path, m)
		},
	}
}

func newNewInitiativeCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "initiative <id>",
		Short: "Scaffold an initiative",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			in := schema.Initiative{
				ID: args[0], Type: "initiative", Status: schema.InitiativeProposed,
				Created:    time.Now().UTC().Format(time.RFC3339),
				Motivation: "TODO: why this initiative exists.",
				Streams:    []schema.Stream{{ID: "backend", Owner: "TODO", Description: "TODO"}},
			}
			path := filepath.Join(io.Repo, "work", "initiatives", args[0], "initiative.yaml")
			return scaffoldFile(io, path, in)
		},
	}
}

func newNewTaskCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "task <initiative-id> <task-id>",
		Short: "Scaffold a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			t := schema.Task{
				ID: args[1], Title: "TODO: task title", Initiative: args[0],
				Stream: "backend", Status: schema.TaskNotStarted, StatusSource: schema.StatusDerived,
			}
			path := filepath.Join(io.Repo, "work", "initiatives", args[0], "tasks", args[1]+".yaml")
			return scaffoldFile(io, path, t)
		},
	}
}

func newNewADRCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "adr <slug>",
		Short: "Scaffold an architecture decision record",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			slug := args[0]
			content := fmt.Sprintf(`# ADR: %s

Status: proposed

## Context

TODO: the forces and constraints in play.

## Decision

TODO: what was chosen.

## Consequences

TODO: what becomes easier and what becomes harder.
`, slug)
			path := filepath.Join(io.Repo, "decisions", slug+".md")
			if exists(path) {
				return io.fail("ALREADY_EXISTS", "file already exists: "+path, nil)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return io.fail("NEW_FAILED", err.Error(), nil)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return io.fail("NEW_FAILED", err.Error(), nil)
			}
			io.printf("created %s\n", path)
			return nil
		},
	}
}

// scaffoldFile writes a YAML artifact, refusing to clobber.
func scaffoldFile(io *IO, path string, v interface{}) error {
	if exists(path) {
		return io.fail("ALREADY_EXISTS", "file already exists: "+path, nil)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return io.fail("NEW_FAILED", err.Error(), nil)
	}
	if err := schema.SaveCanonical(path, v); err != nil {
		return io.fail("NEW_FAILED", err.Error(), nil)
	}
	if io.JSON {
		return io.printJSON(map[string]string{"created": path})
	}
	io.printf("created %s\n", path)
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
