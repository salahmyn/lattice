package cli

import (
	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
)

type adapterInfo struct {
	Name       string   `json:"name"`
	Extensions []string `json:"extensions"`
}

func newAdaptersCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "adapters",
		Short: "Inspect language adapters",
	}
	cmd.AddCommand(newAdaptersListCommand(io), newAdaptersCheckCommand(io))
	return cmd
}

func newAdaptersListCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered language adapters",
		RunE: func(_ *cobra.Command, _ []string) error {
			adCfg, _ := config.LoadAdapters(io.Repo)
			reg := all.Registry(adCfg)
			var infos []adapterInfo
			for _, a := range reg.All() {
				infos = append(infos, adapterInfo{Name: a.Name(), Extensions: a.FileExtensions()})
			}
			if io.JSON {
				return io.printJSON(infos)
			}
			for _, in := range infos {
				io.printf("%-12s %v\n", in.Name, in.Extensions)
			}
			return nil
		},
	}
}

func newAdaptersCheckCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "check <file>",
		Short: "Report which adapter handles a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			reg := all.All()
			a := reg.For(args[0])
			result := map[string]interface{}{"file": args[0], "handled": a != nil}
			if a != nil {
				result["adapter"] = a.Name()
			}
			if io.JSON {
				return io.printJSON(result)
			}
			if a == nil {
				io.printf("%s: no adapter\n", args[0])
				return nil
			}
			io.printf("%s: %s\n", args[0], a.Name())
			return nil
		},
	}
}
