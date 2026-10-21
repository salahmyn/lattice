package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/blocks"
	"github.com/salahmyn/lattice/pkg/lattice/store"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// openStore opens the workspace store, reporting failures the CLI way.
func openStore(io *IO) (store.Store, error) {
	ws, err := workspace.Open(io.Repo)
	if err != nil {
		return nil, io.fail("NO_WORKSPACE", err.Error(), "run `lattice init`")
	}
	st, err := store.Open(context.Background(), ws)
	if err != nil {
		return nil, io.fail("STORE_OPEN", err.Error(), nil)
	}
	return st, nil
}

func newRecordCommand(io *IO) *cobra.Command {
	var kind string
	cmd := &cobra.Command{Use: "record", Short: "List, read and write authored records (judgments) in the workspace store"}
	cmd.PersistentFlags().StringVar(&kind, "kind", "judgment", "record kind")

	list := &cobra.Command{Use: "list", Short: "List records of a kind", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			recs, err := st.List(context.Background(), kind)
			if err != nil {
				return io.fail("STORE_READ", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(recs)
			}
			for _, r := range recs {
				io.printf("%s\t%s\t%s\n", r.ID, r.Version, r.Author)
			}
			return nil
		}}

	get := &cobra.Command{Use: "get <id>", Short: "Print a record's body", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			r, err := st.Get(context.Background(), kind, args[0])
			if errors.Is(err, store.ErrNotFound) {
				return io.fail("NOT_FOUND", fmt.Sprintf("%s %s not found", kind, args[0]), nil)
			}
			if err != nil {
				return io.fail("STORE_READ", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(map[string]any{"record": r, "body": string(r.Body)})
			}
			io.printf("%s", r.Body)
			return nil
		}}

	var version string
	put := &cobra.Command{Use: "put <file>", Short: "Write a record from a YAML file; an update names the version it replaces",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := os.ReadFile(args[0])
			if err != nil {
				return io.fail("READ", err.Error(), nil)
			}
			id := store.IDOf(body)
			if kind == "judgment" { // a bad judgment in the store would break every later refresh
				j, err := blocks.ParseJudgment(body, args[0])
				if err != nil {
					return io.fail("JUDGMENT_INVALID", err.Error(), nil)
				}
				id = j.ID
			}
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			r, err := st.Put(context.Background(), store.Record{Kind: kind, ID: id, Version: version, Body: body})
			var stale *store.StaleError
			if errors.As(err, &stale) {
				return io.fail("STALE_VERSION", err.Error(), map[string]string{"current_version": stale.Current})
			}
			if err != nil {
				return io.fail("STORE_WRITE", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(r)
			}
			io.printf("put %s %s version %s\n", kind, r.ID, r.Version)
			return nil
		}}
	put.Flags().StringVar(&version, "version", "", "the version this write replaces (empty for a new record)")

	cmd.AddCommand(list, get, put)
	return cmd
}
