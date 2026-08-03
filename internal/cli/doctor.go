package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
	"github.com/zzanghyunmoo/my-desk-setup/internal/output"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/ui"
	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
)

func newDoctorCommand(streams Streams, system Runtime) *cobra.Command {
	var selection selectionArguments
	var targetID string
	var format string
	var catalogPath string

	command := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose current-target readiness without authentication",
		Args:  noPositionalArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validateOutputFormat(format); err != nil {
				return err
			}
			if selection.interactive && format == "json" {
				return invalidInput(fmt.Errorf(
					"--interactive cannot share stdout with --format json",
				))
			}
			environment, err := loadEnvironment(catalogPath)
			if err != nil {
				if catalogPath != "" {
					return invalidInput(err)
				}
				return err
			}
			var interactive []string
			if selection.interactive {
				labels := make(map[string]string, len(environment.Catalog.Components))
				for _, component := range environment.Catalog.Components {
					labels[component.ID] = component.Name
				}
				interactive, err = ui.Choose(
					ui.Choices(labels),
					streams.Input,
					streams.Output,
				)
				if err != nil {
					return err
				}
			}
			request, err := selection.selection(interactive)
			if err != nil {
				return invalidInput(err)
			}
			facts, err := resolveTarget(command.Context(), targetID, system, true)
			if err != nil {
				return err
			}
			revision, err := catalog.Revision(environment)
			if err != nil {
				return fmt.Errorf("catalog revision: %w", err)
			}
			facts.CLIRevision = version.String()
			facts.CatalogRevision = revision
			plan, err := planning.Build(environment, facts, request)
			if err != nil {
				return invalidInput(err)
			}
			home, err := runtimeHome(system)
			if err != nil {
				return err
			}
			componentAdapter, err := currentAdapter(
				environment, facts, home, system, adapterOptions{},
			)
			if err != nil {
				return err
			}
			report, err := doctor.Run(command.Context(), plan, componentAdapter)
			if err != nil {
				return err
			}
			switch format {
			case "human":
				if err := output.DoctorHuman(streams.Output, report); err != nil {
					return err
				}
			case "json":
				if err := output.JSON(streams.Output, report); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported format %q; use human or json", format)
			}
			if !report.Ready {
				return actionRequired(
					errors.New("doctor found unresolved component readiness"),
				)
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.BoolVar(&selection.all, "all", false, "check every target-eligible component")
	flags.StringVar(&selection.profile, "profile", "", "check a named profile")
	flags.StringSliceVar(&selection.components, "component", nil, "check a component or capability")
	flags.BoolVar(&selection.interactive, "interactive", false, "choose components interactively")
	flags.StringVar(&targetID, "target", "", "current target ID")
	flags.StringVar(&format, "format", "human", "output format: human or json")
	flags.StringVar(&catalogPath, "catalog", "", "override the embedded catalog directory")
	return command
}
