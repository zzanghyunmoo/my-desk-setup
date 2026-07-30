package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/zzanghyunmoo/my-desk-setup/internal/output"
	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
)

const VersionSchema = "mds.version/v1"

type versionEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Date          string `json:"date"`
}

func newVersionCommand(streams Streams) *cobra.Command {
	var format string
	command := &cobra.Command{
		Use:   "version",
		Short: "Print the mds release identity",
		Args:  noPositionalArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := validateOutputFormat(format); err != nil {
				return err
			}
			if format == "json" {
				return output.JSON(streams.Output, versionEnvelope{
					SchemaVersion: VersionSchema,
					Version:       version.Version,
					Commit:        version.Commit,
					Date:          version.Date,
				})
			}
			_, err := fmt.Fprintln(streams.Output, version.String())
			return err
		},
	}
	command.Flags().StringVar(&format, "format", "human", "output format: human or json")
	return command
}
