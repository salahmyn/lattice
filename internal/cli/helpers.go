package cli

import (
	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// openWorkspace resolves the Lattice workspace from the --repo path.
func openWorkspace(io *IO) (*workspace.Workspace, error) {
	return workspace.Open(io.Repo)
}

// graphFor resolves the workspace and builds the knowledge graph in one step.
// It is the common entry point for read-only commands.
func graphFor(io *IO, cmd *cobra.Command, withCodeGraph bool) (schema.KnowledgeGraph, *workspace.Workspace, error) {
	ws, err := openWorkspace(io)
	if err != nil {
		return schema.KnowledgeGraph{}, nil, err
	}
	kg, err := buildGraph(cmd.Context(), ws, withCodeGraph)
	if err != nil {
		return schema.KnowledgeGraph{}, ws, err
	}
	return kg, ws, nil
}
