package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/output"
)

const CatalogSchema = "mds.catalog/v1"

type catalogEnvelope struct {
	SchemaVersion   string              `json:"schema_version"`
	CatalogRevision string              `json:"catalog_revision"`
	Environment     catalog.Environment `json:"environment"`
}

func newCatalogCommand(streams Streams) *cobra.Command {
	var format string
	var catalogPath string
	command := &cobra.Command{
		Use:   "catalog",
		Short: "List components, capabilities, profiles, and target support",
		Args:  noPositionalArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := validateOutputFormat(format); err != nil {
				return err
			}
			environment, err := loadEnvironment(catalogPath)
			if err != nil {
				if catalogPath != "" {
					return invalidInput(err)
				}
				return err
			}
			revision, err := catalog.Revision(environment)
			if err != nil {
				return fmt.Errorf("catalog revision: %w", err)
			}
			envelope := catalogEnvelope{
				SchemaVersion:   CatalogSchema,
				CatalogRevision: revision,
				Environment:     environment,
			}
			if format == "json" {
				return output.JSON(streams.Output, envelope)
			}
			for _, component := range environment.Catalog.Components {
				if _, err := fmt.Fprintf(
					streams.Output,
					"%s\t%s\t%v\n",
					component.ID,
					component.Kind,
					component.Provides,
				); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "human", "output format: human or json")
	command.Flags().StringVar(&catalogPath, "catalog", "", "override the embedded catalog directory")
	return command
}
