package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/output"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
	"github.com/zzanghyunmoo/my-desk-setup/internal/ui"
	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
)

type Streams struct {
	Input  io.Reader
	Output io.Writer
	Error  io.Writer
}

type Runtime struct {
	GOOS          string
	GOARCH        string
	Getenv        func(string) string
	HomeDir       func() (string, error)
	Now           func() time.Time
	ObserveTarget func(context.Context, target.Facts) (target.Facts, error)
}

func DefaultStreams() Streams {
	return Streams{Input: os.Stdin, Output: os.Stdout, Error: os.Stderr}
}

func DefaultRuntime() Runtime {
	local := transport.NewLocal()
	return Runtime{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Getenv: os.Getenv,
		HomeDir: os.UserHomeDir,
		Now:     time.Now,
		ObserveTarget: func(ctx context.Context, facts target.Facts) (target.Facts, error) {
			return target.ObserveLocal(ctx, facts, local, "")
		},
	}
}

func NewRoot(streams Streams, system Runtime) *cobra.Command {
	root := &cobra.Command{
		Use:           "mds",
		Short:         "Reproducible host and Linux guest development setup",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	root.SetIn(streams.Input)
	root.SetOut(streams.Output)
	root.SetErr(streams.Error)
	root.AddCommand(newPlanCommand(streams, system))
	root.AddCommand(newApplyCommand(streams, system))
	for _, name := range []string{"doctor", "update"} {
		commandName := name
		root.AddCommand(&cobra.Command{
			Use:   commandName,
			Short: commandName + " is introduced by the next implementation unit",
			RunE: func(_ *cobra.Command, _ []string) error {
				return fmt.Errorf("%s is not available in this build", commandName)
			},
		})
	}
	return root
}

func Run(args []string, streams Streams, system Runtime) int {
	root := NewRoot(streams, system)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(streams.Error, err)
		return 1
	}
	return 0
}

func newPlanCommand(streams Streams, system Runtime) *cobra.Command {
	var selection selectionArguments
	var targetID string
	var format string
	var catalogPath string

	command := &cobra.Command{
		Use:   "plan",
		Short: "Create a read-only deterministic installation plan",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			environment, err := loadEnvironment(catalogPath)
			if err != nil {
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
				return err
			}
			facts, err := resolveTarget(command.Context(), targetID, system, false)
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
				return err
			}
			switch format {
			case "human":
				return output.Human(streams.Output, plan)
			case "json":
				return output.JSON(streams.Output, plan)
			default:
				return fmt.Errorf("unsupported format %q; use human or json", format)
			}
		},
	}
	flags := command.Flags()
	flags.BoolVar(&selection.all, "all", false, "select every target-eligible component")
	flags.StringVar(&selection.profile, "profile", "", "select a named profile")
	flags.StringSliceVar(&selection.components, "component", nil, "select a component or capability")
	flags.BoolVar(&selection.interactive, "interactive", false, "choose components interactively")
	flags.StringVar(&targetID, "target", "", "explicit target ID")
	flags.StringVar(&format, "format", "human", "output format: human or json")
	flags.StringVar(&catalogPath, "catalog", "", "override the embedded catalog directory")
	return command
}

func loadEnvironment(path string) (catalog.Environment, error) {
	if path == "" {
		return catalog.LoadFS(catalogdata.FS)
	}
	return catalog.Load(path)
}

func resolveTarget(
	ctx context.Context,
	value string,
	system Runtime,
	requireCurrent bool,
) (target.Facts, error) {
	var facts target.Facts
	var err error
	if value == "" {
		facts, err = target.DiscoverLocal(
			system.GOOS,
			system.GOARCH,
			target.GetenvFunc(system.Getenv),
		)
	} else {
		var id target.ID
		id, err = target.ParseID(value)
		if err == nil {
			osName := "linux"
			if id.Kind == target.KindMacOSHost {
				osName = "darwin"
			}
			if id.Kind == target.KindWindowsHost {
				osName = "windows"
			}
			facts = target.Facts{
				ID: id, OS: osName, Architecture: system.GOARCH, Reachable: true,
			}
		}
	}
	if err != nil {
		return target.Facts{}, err
	}
	local, localErr := target.DiscoverLocal(
		system.GOOS,
		system.GOARCH,
		target.GetenvFunc(system.Getenv),
	)
	isCurrent := localErr == nil && local.ID == facts.ID
	if requireCurrent && !isCurrent {
		return target.Facts{}, fmt.Errorf(
			"apply target %s is not the current local target; run the same mds revision inside that guest",
			facts.ID.String(),
		)
	}
	if isCurrent && system.ObserveTarget != nil {
		return system.ObserveTarget(ctx, facts)
	}
	return facts, nil
}
