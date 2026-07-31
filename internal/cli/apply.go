package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	guestadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/guest"
	hostadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/host"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/execution"
	"github.com/zzanghyunmoo/my-desk-setup/internal/output"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
	"github.com/zzanghyunmoo/my-desk-setup/internal/ui"
	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
)

func newApplyCommand(streams Streams, system Runtime) *cobra.Command {
	var selection selectionArguments
	var targetID string
	var format string
	var catalogPath string
	var expectedDigest string
	var stateRoot string
	var guestBootstrapArchive string

	command := &cobra.Command{
		Use:   "apply",
		Short: "Apply one reviewed plan to the current target",
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
			if expectedDigest == "" {
				return invalidInput(errors.New("--plan-digest is required"))
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
			if err := execution.VerifyPlan(plan, expectedDigest); err != nil {
				return stalePlan(err)
			}
			if err := validateGuestBootstrapArchiveSelection(
				guestBootstrapArchive,
				facts,
				plan,
			); err != nil {
				return invalidInput(err)
			}
			home, err := runtimeHome(system)
			if err != nil {
				return err
			}
			componentAdapter, err := currentAdapterWithOptions(
				environment,
				facts,
				home,
				system,
				false,
				guestBootstrapArchive,
			)
			if err != nil {
				var archiveError *hostadapter.GuestBootstrapArchiveError
				if errors.As(err, &archiveError) {
					return invalidInput(err)
				}
				return err
			}
			if stateRoot == "" {
				stateRoot = defaultStateRoot(home, system)
			}
			now := system.Now
			if now == nil {
				now = time.Now
			}
			runner := execution.Runner{
				Adapter: componentAdapter,
				ObserveTarget: func(
					ctx context.Context,
					planned target.Facts,
				) (target.Facts, error) {
					if system.ObserveTarget == nil {
						return planned, nil
					}
					observed, err := system.ObserveTarget(ctx, planned)
					if err != nil {
						return target.Facts{}, unreachable(err)
					}
					observed.CLIRevision = version.String()
					observed.CatalogRevision = revision
					return observed, nil
				},
				Now: now,
			}
			receipt, err := runner.Apply(
				command.Context(),
				plan,
				expectedDigest,
				stateRoot,
			)
			if err != nil {
				return err
			}
			switch format {
			case "human":
				if err := output.ReceiptHuman(streams.Output, receipt); err != nil {
					return err
				}
			case "json":
				if err := output.JSON(streams.Output, receipt); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported format %q; use human or json", format)
			}
			if !receipt.Complete {
				return actionRequired(
					errors.New("apply completed with unresolved component outcomes"),
				)
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.BoolVar(&selection.all, "all", false, "select every target-eligible component")
	flags.StringVar(&selection.profile, "profile", "", "select a named profile")
	flags.StringSliceVar(&selection.components, "component", nil, "select a component or capability")
	flags.BoolVar(&selection.interactive, "interactive", false, "choose components interactively")
	flags.StringVar(&targetID, "target", "", "current target ID")
	flags.StringVar(&expectedDigest, "plan-digest", "", "exact digest printed by mds plan")
	flags.StringVar(&stateRoot, "state-root", "", "override target-local state root")
	flags.StringVar(
		&guestBootstrapArchive,
		"guest-bootstrap-archive",
		"",
		"use an exact local Linux release archive for host-to-guest bootstrap",
	)
	flags.StringVar(&format, "format", "human", "output format: human or json")
	flags.StringVar(&catalogPath, "catalog", "", "override the embedded catalog directory")
	return command
}

func currentAdapter(
	environment catalog.Environment,
	facts target.Facts,
	home string,
	system Runtime,
	allowReplace bool,
) (adapters.Component, error) {
	return currentAdapterWithOptions(
		environment,
		facts,
		home,
		system,
		allowReplace,
		"",
	)
}

func currentAdapterWithOptions(
	environment catalog.Environment,
	facts target.Facts,
	home string,
	system Runtime,
	allowReplace bool,
	guestBootstrapArchive string,
) (adapters.Component, error) {
	port := transport.NewLocal()
	switch facts.ID.Kind {
	case target.KindMacOSHost, target.KindWindowsHost:
		return hostadapter.NewWithOptions(
			environment,
			port,
			home,
			facts.OS,
			facts.Architecture,
			hostadapter.Options{
				AllowReplace:          allowReplace,
				GuestBootstrapArchive: guestBootstrapArchive,
			},
		)
	case target.KindWSLGuest, target.KindLimaGuest:
		now := system.Now
		if now == nil {
			now = time.Now
		}
		return guestadapter.New(
			environment,
			facts,
			port,
			home,
			"linux",
			facts.Architecture,
			&http.Client{Timeout: 5 * time.Minute},
			now,
			allowReplace,
		), nil
	default:
		return nil, fmt.Errorf("no adapter for target %s", facts.ID.String())
	}
}

func validateGuestBootstrapArchiveSelection(
	archivePath string,
	facts target.Facts,
	plan planning.Plan,
) error {
	if archivePath == "" {
		return nil
	}
	if !filepath.IsAbs(archivePath) {
		return errors.New(
			"--guest-bootstrap-archive requires an absolute path",
		)
	}
	if facts.ID.Kind != target.KindMacOSHost &&
		facts.ID.Kind != target.KindWindowsHost {
		return errors.New(
			"--guest-bootstrap-archive is available only on a host target",
		)
	}
	for _, action := range plan.Actions {
		if action.ComponentID == "lima" || action.ComponentID == "wsl" {
			return nil
		}
	}
	return errors.New(
		"--guest-bootstrap-archive requires a selected Lima or WSL guest lifecycle component",
	)
}

func runtimeHome(system Runtime) (string, error) {
	if system.HomeDir != nil {
		return system.HomeDir()
	}
	if system.Getenv != nil {
		if home := system.Getenv("HOME"); home != "" {
			return home, nil
		}
		if home := system.Getenv("USERPROFILE"); home != "" {
			return home, nil
		}
	}
	return "", errors.New("user home directory is unavailable")
}

func defaultStateRoot(home string, system Runtime) string {
	if system.Getenv != nil {
		if stateHome := system.Getenv("XDG_STATE_HOME"); stateHome != "" {
			return filepath.Join(stateHome, "my-desk-setup")
		}
		if system.GOOS == "windows" {
			if localAppData := system.Getenv("LOCALAPPDATA"); localAppData != "" {
				return filepath.Join(localAppData, "my-desk-setup", "state")
			}
		}
	}
	return filepath.Join(home, ".local", "state", "my-desk-setup")
}
