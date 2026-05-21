package cli

import (
	"runtime"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/buildinfo"
)

type versionInfo struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Date          string `json:"date"`
	SchemaVersion string `json:"schema_version"`
	Go            string `json:"go"`
	Platform      string `json:"platform"`
}

func newVersionCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(_ *cobra.Command, _ []string) error {
			info := versionInfo{
				Version:       buildinfo.Version,
				Commit:        buildinfo.Commit,
				Date:          buildinfo.Date,
				SchemaVersion: buildinfo.SchemaVersion,
				Go:            runtime.Version(),
				Platform:      runtime.GOOS + "/" + runtime.GOARCH,
			}
			if io.JSON {
				return io.printJSON(info)
			}
			io.printf("lattice %s\n", info.Version)
			io.printf("  commit:         %s\n", info.Commit)
			io.printf("  built:          %s\n", info.Date)
			io.printf("  schema version: %s\n", info.SchemaVersion)
			io.printf("  go:             %s\n", info.Go)
			io.printf("  platform:       %s\n", info.Platform)
			return nil
		},
	}
}
