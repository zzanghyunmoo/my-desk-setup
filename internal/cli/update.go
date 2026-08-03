package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/execution"
	"github.com/zzanghyunmoo/my-desk-setup/internal/output"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	updateflow "github.com/zzanghyunmoo/my-desk-setup/internal/update"
	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
)

const maxCandidateSize = 1 << 20

func newUpdateCommand(streams Streams, system Runtime) *cobra.Command {
	var targetID string
	var format string
	var catalogPath string
	var candidatePath string
	var componentID string
	var expectedDigest string
	var stateRoot string

	command := &cobra.Command{
		Use:   "update",
		Short: "Preview or apply an exact reviewed version-lock update",
		Args:  noPositionalArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validateOutputFormat(format); err != nil {
				return err
			}
			if catalogPath == "" {
				return invalidInput(errors.New(
					"--catalog is required because embedded catalog data is read-only",
				))
			}
			if (candidatePath == "") == (componentID == "") {
				return invalidInput(errors.New(
					"choose exactly one of --candidate or --component",
				))
			}
			if err := updateflow.ValidateCatalogRoot(catalogPath); err != nil {
				return updateError(err)
			}
			environment, err := loadEnvironment(catalogPath)
			if err != nil {
				return invalidInput(err)
			}
			facts, err := resolveTarget(command.Context(), targetID, system, true)
			if err != nil {
				return updateError(err)
			}
			var candidate updateflow.Candidate
			candidateLoaded := false
			if candidatePath != "" {
				candidate, err = readCandidate(candidatePath)
				if err != nil {
					return invalidInput(err)
				}
				candidateLoaded = true
			}

			var plan updateflow.Plan
			var updated catalog.Environment
			var home string
			resumed := false
			if expectedDigest != "" {
				home, err = runtimeHome(system)
				if err != nil {
					return updateError(err)
				}
				if stateRoot == "" {
					stateRoot = defaultStateRoot(home, system)
				}
				plan, updated, resumed, err = updateflow.ResumePlan(
					catalogPath,
					stateRoot,
					expectedDigest,
				)
				if err != nil {
					return updateError(err)
				}
				if resumed {
					switch {
					case candidateLoaded:
						if err := updateflow.ValidateCandidateForPlan(
							plan,
							candidate,
						); err != nil {
							return updateError(err)
						}
					case componentID != plan.ComponentID:
						return invalidInput(fmt.Errorf(
							"component %q does not match resumable update component %q",
							componentID,
							plan.ComponentID,
						))
					}
				}
			}
			if !resumed {
				if !candidateLoaded {
					candidate, err = updateflow.Discover(
						command.Context(),
						environment,
						catalog.TargetKind(facts.ID.Kind),
						componentID,
						&http.Client{Timeout: 30 * time.Second},
						"",
					)
					if err != nil {
						return updateError(err)
					}
				}
				beforeRevision, err := catalog.Revision(environment)
				if err != nil {
					return fmt.Errorf("catalog revision: %w", err)
				}
				facts.CLIRevision = version.String()
				facts.CatalogRevision = beforeRevision
				plan, updated, err = updateflow.Build(
					environment,
					facts,
					candidate,
				)
				if err != nil {
					return updateError(err)
				}
			}
			if expectedDigest == "" {
				switch format {
				case "human":
					return output.UpdateHuman(streams.Output, plan)
				case "json":
					return output.JSON(streams.Output, plan)
				default:
					return fmt.Errorf("unsupported format %q; use human or json", format)
				}
			}
			if err := updateflow.Verify(plan, expectedDigest); err != nil {
				return updateError(err)
			}
			if home == "" {
				home, err = runtimeHome(system)
				if err != nil {
					return updateError(err)
				}
			}
			componentAdapter, err := currentAdapter(
				updated,
				plan.TargetPlan.Target,
				home,
				system,
				adapterOptions{AllowReplace: true},
			)
			if err != nil {
				return updateError(err)
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
					observed.CatalogRevision = plan.AfterCatalogRevision
					return observed, nil
				},
				Now: now,
			}
			result, err := updateflow.Apply(
				command.Context(),
				plan,
				expectedDigest,
				catalogPath,
				stateRoot,
				runner,
			)
			if err != nil {
				return updateError(err)
			}
			switch format {
			case "human":
				if err := output.UpdateResultHuman(streams.Output, result); err != nil {
					return err
				}
			case "json":
				if err := output.JSON(streams.Output, result); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported format %q; use human or json", format)
			}
			if !result.Receipt.Complete {
				return actionRequired(
					errors.New(
						"update is incomplete; resolve the reported component outcome and rerun the same mds update with the same --plan-digest and writable --catalog",
					),
				)
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.StringVar(&targetID, "target", "", "current target ID")
	flags.StringVar(&catalogPath, "catalog", "", "writable catalog directory")
	flags.StringVar(&candidatePath, "candidate", "", "reviewed exact candidate JSON")
	flags.StringVar(&componentID, "component", "", "discover an exact supported candidate")
	flags.StringVar(&expectedDigest, "plan-digest", "", "apply this exact update digest")
	flags.StringVar(&stateRoot, "state-root", "", "override target-local state root")
	flags.StringVar(&format, "format", "human", "output format: human or json")
	return command
}

func readCandidate(
	path string,
) (candidate updateflow.Candidate, resultErr error) {
	info, err := os.Lstat(path)
	if err != nil {
		return updateflow.Candidate{}, fmt.Errorf("inspect update candidate: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return updateflow.Candidate{}, errors.New(
			"update candidate must be regular and not a symlink",
		)
	}
	file, err := os.Open(path)
	if err != nil {
		return updateflow.Candidate{}, fmt.Errorf("open update candidate: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			candidate = updateflow.Candidate{}
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close update candidate: %w", err),
			)
		}
	}()
	content, err := io.ReadAll(io.LimitReader(file, maxCandidateSize+1))
	if err != nil {
		return updateflow.Candidate{}, fmt.Errorf("read update candidate: %w", err)
	}
	if len(content) > maxCandidateSize {
		return updateflow.Candidate{}, errors.New("update candidate exceeds 1 MiB")
	}
	return updateflow.DecodeCandidate(content)
}
