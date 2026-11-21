package cli

// @module-feature cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// writersContract names the shape of `lattice writers --json`.
const writersContract = "lattice.writers/1"

// writersOutput is `lattice writers --json`.
type writersOutput struct {
	Contract string `json:"contract"`
	graph.WriterReport
	// Complete is true when no statement the adapter could not read may write
	// the table (or column): the writers listed are all there are, as far as
	// the migrations say.
	Complete bool `json:"complete"`
}

// newWritersCommand is `lattice writers <table>[.<column>]`: every writer of
// a table or column the migrations show, and what guards each for the
// promises whose columns it writes.
func newWritersCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "writers <table>[.<column>]",
		Short: "Show who writes a table or column, and which promises each writer is guarded or unguarded for",
		Long: `Lists each writer of a table, or of one column of it, that the migrations
show: a SQL function (by INSERT, UPDATE, DELETE or MERGE in its body, or by
assigning NEW.<column> in a trigger function), a migration that runs DML itself,
and a role that holds INSERT, UPDATE or DELETE on the table through a GRANT.

For each writer it prints the operation, the columns, the file and line, and,
for every promise (<feature>:INV-n) whose columns it writes, whether it is
guarded for it (it enforces it, or a trigger or policy that enforces it applies
to its table and command) or unguarded for it, and whether that promise's columns were declared (columns: on
the invariant in its feature YAML) or derived from the columns its enforcers
write. 'lattice validate' reports an
unguarded writer as UNGUARDED_WRITER (info).

It ends with the reasons the table's writer set is incomplete (an EXECUTE of a
string, a write through a view, a foreign-key action, a statement it could not
read), or says the writer set is complete.

The name is [schema.]table or [schema.]table.column; the schema defaults to
public. --json prints lattice.writers/1. Read-only.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			var mods []ir.Module
			kg, _, err := buildGraphReach(cmd.Context(), ws, buildOptions{readOnly: true, onModules: func(m []ir.Module) { mods = m }})
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			ix := graph.BuildWriterIndex(kg, mods, ws.SQL.APIRoles, ws.SQL.DefaultWrite)
			table, column := ix.ResolveTable(args[0])
			rep := ix.Report(table, column)
			if io.JSON {
				return io.printJSON(writersOutput{Contract: writersContract, WriterReport: rep, Complete: len(rep.Incomplete) == 0})
			}
			writeWriters(io, rep)
			return nil
		},
	}
}

func writeWriters(io *IO, rep graph.WriterReport) {
	target := rep.Table
	if rep.Column != "" {
		target += "." + rep.Column
	}
	if len(rep.Writers) == 0 {
		io.printf("%s: no writer found\n", target)
	} else {
		io.printf("%s: %d writer(s)\n", target, len(rep.Writers))
	}
	for _, w := range rep.Writers {
		cols := strings.Join(w.Columns, ", ")
		switch {
		case w.Op == ir.WriteDelete:
			cols = "the row"
		case cols == ir.WriteAll:
			cols = "every column"
		}
		name := w.Name
		if w.Kind == graph.WriterRole {
			name = "role " + name
		}
		io.printf("\n  %s  %s  %s  %s:%d\n", name, w.Op, cols, w.File, w.Line)
		if w.Via != "" {
			io.printf("    %s\n", w.Via)
		}
		for _, g := range w.Guarded {
			io.printf("    guarded for %s by %s (columns %s)\n", g.Invariant, g.By, w.ColumnsFrom[g.Invariant])
		}
		for _, ref := range w.Unguarded {
			io.printf("    unguarded for %s (columns %s)\n", ref, w.ColumnsFrom[ref])
		}
	}
	io.printf("\n")
	if len(rep.Incomplete) == 0 {
		if rep.RolesChecked {
			io.printf("writer set complete (from the migrations and sql.default_write)\n")
		} else {
			io.printf("writer set not known to be complete: functions and migrations were read, but sql.api_roles is not declared, so roles that no statement names were not checked\n")
		}
		return
	}
	io.printf("writer set incomplete:\n")
	for _, g := range rep.Incomplete {
		io.printf("  - %s\n", g.Reason)
	}
}
