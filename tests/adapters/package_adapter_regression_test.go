package adapters_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestPackageAdapterPropagatesInstallerExecutionFailure(t *testing.T) {
	t.Parallel()

	for _, installer := range []string{"brew", "winget", "apt", "mise"} {
		t.Run(installer, func(t *testing.T) {
			t.Parallel()

			executionErr := errors.New("installer execution failed")
			version := "manager-owned"
			if installer == "mise" {
				version = "1.2.3"
			}
			adapter := packages.Adapter{
				Home: t.TempDir(),
				Port: &recordingPort{
					err: func(command transport.Command) error {
						if installer == "apt" &&
							command.Executable == "/usr/bin/sudo" &&
							len(command.Arguments) == 2 &&
							command.Arguments[0] == "-n" &&
							command.Arguments[1] == "true" {
							return nil
						}
						return executionErr
					},
				},
				Environment: packageTestEnvironment("tool"),
			}
			err := adapter.Apply(context.Background(), planning.Action{
				ID:          "test-host:local/tool",
				ComponentID: "tool",
				Installer:   installer,
				Package:     "example.tool",
				Version:     version,
			})
			if !errors.Is(err, executionErr) {
				t.Fatalf("Apply() error = %v, want %v", err, executionErr)
			}
		})
	}
}

func TestPackageAdapterRequiresExactVersionToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		state  adapters.ObservedState
	}{
		{name: "longer patch", output: "tool 1.26.50\n", state: adapters.StateConflict},
		{name: "longer major", output: "tool 11.26.5\n", state: adapters.StateConflict},
		{name: "Go prefix", output: "go1.26.5 linux/arm64\n", state: adapters.StateReady},
		{name: "v prefix", output: "tool v1.26.5\n", state: adapters.StateReady},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			adapter := packages.Adapter{
				Home: t.TempDir(),
				Port: &recordingPort{
					result: func(transport.Command) transport.Result {
						return transport.Result{Stdout: test.output}
					},
				},
				Environment: packageTestEnvironment("tool"),
			}
			observation, err := adapter.Observe(
				context.Background(),
				planning.Action{
					ID:          "test-host:local/tool",
					ComponentID: "tool",
					Installer:   "vendor",
					Package:     "example.tool",
					Version:     "1.26.5",
					Verification: [][]string{
						{"tool", "--version"},
					},
				},
			)
			if err != nil {
				t.Fatalf("Observe(): %v", err)
			}
			if observation.State != test.state {
				t.Fatalf(
					"observation = %+v, want state %s",
					observation,
					test.state,
				)
			}
		})
	}
}

func packageTestEnvironment(componentID string) catalog.Environment {
	return catalog.Environment{
		Catalog: catalog.Catalog{Components: []catalog.Component{
			{
				ID:   componentID,
				Kind: "build",
				VersionPolicy: catalog.VersionPolicy{
					Mode: "manager-owned",
				},
			},
		}},
	}
}
