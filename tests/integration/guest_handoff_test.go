package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	hostadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/host"
	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const (
	testGuestCLIRevision     = "1.2.3 (commit=reviewed, date=2026-07-30T00:00:00Z)"
	testGuestCatalogRevision = "sha256:reviewed-catalog"
)

func TestGuestRuntimeHandoffUsesExactRevisionAndBoundedArgv(t *testing.T) {
	tests := []struct {
		name       string
		action     planning.Action
		spec       guest.Spec
		inventory  transport.Result
		wantTarget string
		wantArgv   []string
	}{
		{
			name: "Lima",
			action: planning.Action{
				ID: "macos-host:local/lima", ComponentID: "lima",
			},
			spec: guest.Spec{WSLDistribution: "Ubuntu-26.04"},
			inventory: transport.Result{
				Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
			},
			wantTarget: "lima-guest:mds",
			wantArgv: []string{
				"shell", "--tty=false", "mds", "--",
				"/bin/sh", "-c", `exec "$HOME/.local/bin/mds" "$@"`,
				"mds", "plan", "--target", "lima-guest:mds",
				"--all", "--format", "json",
			},
		},
		{
			name: "WSL",
			action: planning.Action{
				ID: "windows-host:local/wsl", ComponentID: "wsl",
			},
			spec:       guest.Spec{WSLDistribution: "Ubuntu-26.04"},
			inventory:  transport.Result{Stdout: "Ubuntu-26.04\n"},
			wantTarget: "wsl-guest:Ubuntu-26.04",
			wantArgv: []string{
				"--distribution", "Ubuntu-26.04",
				"--exec", "/bin/sh", "-c",
				`exec "$HOME/.local/bin/mds" "$@"`, "mds", "plan",
				"--target", "wsl-guest:Ubuntu-26.04",
				"--all", "--format", "json",
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			port := &guestRuntimePort{}
			port.result = func(command transport.Command) (transport.Result, error) {
				switch {
				case command.Executable == "limactl" &&
					slices.Equal(command.Arguments, []string{"list", "--json"}):
					return test.inventory, nil
				case command.Executable == "wsl.exe" &&
					slices.Equal(command.Arguments, []string{"--list", "--quiet"}):
					return test.inventory, nil
				case isGuestMDSCommand(command):
					return transport.Result{Stdout: guestPlanIdentityJSON(
						test.wantTarget,
						testGuestCLIRevision,
						testGuestCatalogRevision,
					)}, nil
				default:
					return transport.Result{}, nil
				}
			}
			runtime := hostadapter.GuestRuntime{
				Architecture:    "arm64",
				Port:            port,
				Delegate:        guestRuntimeDelegate{},
				Spec:            test.spec,
				CLIRevision:     testGuestCLIRevision,
				CatalogRevision: testGuestCatalogRevision,
			}

			observation, err := runtime.Observe(context.Background(), test.action)
			if err != nil {
				t.Fatalf("Observe(): %v", err)
			}
			if observation.State != adapters.StateReady {
				t.Fatalf("observation = %+v, want ready", observation)
			}
			handoff := findGuestMDSCommand(t, port.commands)
			if !slices.Equal(handoff.Arguments, test.wantArgv) {
				t.Fatalf("handoff argv = %v, want %v", handoff.Arguments, test.wantArgv)
			}
			if handoff.Timeout <= 0 || handoff.OutputLimit <= 0 {
				t.Fatalf(
					"handoff bounds = timeout %s output %d, want explicit positive values",
					handoff.Timeout,
					handoff.OutputLimit,
				)
			}
			joined := handoff.Executable + " " + strings.Join(handoff.Arguments, " ")
			for _, forbidden := range []string{
				"curl ", "wget ", " cp ", " install ", " auth ", " login ",
			} {
				if strings.Contains(" "+joined+" ", forbidden) {
					t.Fatalf("handoff mutates guest or handles auth via %q: %s", forbidden, joined)
				}
			}
		})
	}
}

func TestGuestRuntimeMissingOrStaleMDSWithoutReleaseMetadataRequiresAction(t *testing.T) {
	tests := []struct {
		name      string
		handoff   func() (transport.Result, error)
		wantCause string
	}{
		{
			name: "missing",
			handoff: func() (transport.Result, error) {
				return transport.Result{}, errors.New("mds executable not found")
			},
			wantCause: "missing",
		},
		{
			name: "stale CLI",
			handoff: func() (transport.Result, error) {
				return transport.Result{Stdout: guestPlanIdentityJSON(
					"lima-guest:mds",
					"1.2.2 (commit=stale, date=2026-07-29T00:00:00Z)",
					testGuestCatalogRevision,
				)}, nil
			},
			wantCause: "stale guest cli revision",
		},
		{
			name: "stale catalog",
			handoff: func() (transport.Result, error) {
				return transport.Result{Stdout: guestPlanIdentityJSON(
					"lima-guest:mds",
					testGuestCLIRevision,
					"sha256:stale-catalog",
				)}, nil
			},
			wantCause: "stale guest catalog revision",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			port := &guestRuntimePort{
				result: func(command transport.Command) (transport.Result, error) {
					switch {
					case command.Executable == "limactl" &&
						slices.Equal(command.Arguments, []string{"list", "--json"}):
						return transport.Result{
							Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
						}, nil
					case isGuestMDSCommand(command):
						return test.handoff()
					default:
						return transport.Result{}, nil
					}
				},
			}
			runtime := hostadapter.GuestRuntime{
				Architecture:    "arm64",
				Port:            port,
				Delegate:        guestRuntimeDelegate{},
				Spec:            guest.Spec{WSLDistribution: "Ubuntu-26.04"},
				CLIRevision:     testGuestCLIRevision,
				CatalogRevision: testGuestCatalogRevision,
			}
			action := planning.Action{
				ID: "macos-host:local/lima", ComponentID: "lima",
			}

			observation, err := runtime.Observe(context.Background(), action)
			if err != nil {
				t.Fatalf("Observe(): %v", err)
			}
			if observation.State == adapters.StateReady {
				t.Fatalf("observation = %+v, stale/missing guest mds must not be ready", observation)
			}
			if !strings.Contains(strings.ToLower(observation.Detail), test.wantCause) {
				t.Fatalf("observation detail = %q, want %q", observation.Detail, test.wantCause)
			}

			err = runtime.Apply(context.Background(), action)
			var actionRequired *adapters.ActionRequiredError
			if !errors.As(err, &actionRequired) {
				t.Fatalf("Apply() error = %v, want action-required bootstrap handoff", err)
			}
			for _, expected := range []string{
				"no reviewed Linux/arm64 artifact URL and SHA-256",
				testGuestCLIRevision,
				testGuestCatalogRevision,
			} {
				if !strings.Contains(actionRequired.Reason, expected) {
					t.Fatalf("action-required reason = %q, want %q", actionRequired.Reason, expected)
				}
			}
		})
	}
}

func TestGuestRuntimeAutomaticallyBootstrapsReviewedLinuxArtifact(t *testing.T) {
	const (
		artifactURL = "https://github.com/zzanghyunmoo/my-desk-setup/releases/download/v1.2.3/mds_1.2.3_linux_arm64.tar.gz"
		artifactSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	handoffAttempts := 0
	port := &guestRuntimePort{
		result: func(command transport.Command) (transport.Result, error) {
			switch {
			case command.Executable == "limactl" &&
				slices.Equal(command.Arguments, []string{"list", "--json"}):
				return transport.Result{
					Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
				}, nil
			case isGuestBootstrapCommand(command):
				return transport.Result{}, nil
			case isGuestMDSCommand(command):
				handoffAttempts++
				if handoffAttempts < 2 {
					return transport.Result{}, errors.New("mds executable not found")
				}
				return transport.Result{Stdout: guestPlanIdentityJSON(
					"lima-guest:mds",
					testGuestCLIRevision,
					testGuestCatalogRevision,
				)}, nil
			default:
				return transport.Result{}, nil
			}
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture:    "arm64",
		Port:            port,
		Delegate:        guestRuntimeDelegate{},
		Spec:            guest.Spec{WSLDistribution: "Ubuntu-26.04"},
		CLIRevision:     testGuestCLIRevision,
		CatalogRevision: testGuestCatalogRevision,
		BootstrapArtifacts: map[string]hostadapter.GuestBootstrapArtifact{
			"arm64": {URL: artifactURL, SHA256: artifactSHA},
		},
	}
	if err := runtime.Apply(context.Background(), planning.Action{
		ID: "macos-host:local/lima", ComponentID: "lima",
	}); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	var bootstrap transport.Command
	for _, command := range port.commands {
		if isGuestBootstrapCommand(command) {
			bootstrap = command
			break
		}
	}
	if len(bootstrap.Stdin) == 0 ||
		!strings.Contains(string(bootstrap.Stdin), "sha256sum -c") {
		t.Fatalf("bootstrap stdin does not contain checksum-verifying installer")
	}
	joined := strings.Join(bootstrap.Arguments, " ")
	for _, expected := range []string{artifactURL, artifactSHA, "/bin/sh -eu -s --"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("bootstrap argv = %q, want %q", joined, expected)
		}
	}
	for _, forbidden := range []string{" auth ", " login ", "token"} {
		if strings.Contains(strings.ToLower(" "+joined+" "), forbidden) {
			t.Fatalf("bootstrap argv contains forbidden auth surface %q", forbidden)
		}
	}
}

func TestGuestRuntimeBootstrapPreservesUserOwnedMDS(t *testing.T) {
	const artifactSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	port := &guestRuntimePort{
		result: func(command transport.Command) (transport.Result, error) {
			switch {
			case command.Executable == "limactl" &&
				slices.Equal(command.Arguments, []string{"list", "--json"}):
				return transport.Result{
					Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
				}, nil
			case isGuestBootstrapCommand(command):
				return transport.Result{
					ExitCode: 73,
					Stderr:   "refusing to replace guest-local mds without the mds ownership marker",
				}, errors.New("bootstrap ownership conflict")
			case isGuestMDSCommand(command):
				return transport.Result{}, errors.New("stale guest-local mds")
			default:
				return transport.Result{}, nil
			}
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture:    "arm64",
		Port:            port,
		Delegate:        guestRuntimeDelegate{},
		Spec:            guest.Spec{WSLDistribution: "Ubuntu-26.04"},
		CLIRevision:     testGuestCLIRevision,
		CatalogRevision: testGuestCatalogRevision,
		BootstrapArtifacts: map[string]hostadapter.GuestBootstrapArtifact{
			"arm64": {
				URL: "https://example.invalid/mds.tar.gz", SHA256: artifactSHA,
			},
		},
	}
	err := runtime.Apply(context.Background(), planning.Action{
		ID: "macos-host:local/lima", ComponentID: "lima",
	})
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) ||
		!strings.Contains(actionRequired.Reason, "ownership marker") {
		t.Fatalf("Apply() error = %v, want ownership action-required", err)
	}
}

type guestRuntimePort struct {
	commands []transport.Command
	result   func(transport.Command) (transport.Result, error)
}

func (port *guestRuntimePort) Run(
	_ context.Context,
	command transport.Command,
) (transport.Result, error) {
	port.commands = append(port.commands, command)
	if port.result != nil {
		return port.result(command)
	}
	return transport.Result{}, nil
}

type guestRuntimeDelegate struct{}

func (guestRuntimeDelegate) Observe(
	context.Context,
	planning.Action,
) (adapters.Observation, error) {
	return adapters.Observation{State: adapters.StateReady}, nil
}

func (guestRuntimeDelegate) Apply(context.Context, planning.Action) error {
	return nil
}

func (guestRuntimeDelegate) Verify(context.Context, planning.Action) error {
	return nil
}

func isGuestMDSCommand(command transport.Command) bool {
	for index := 0; index+1 < len(command.Arguments); index++ {
		if command.Arguments[index] == "mds" &&
			command.Arguments[index+1] == "plan" {
			return true
		}
	}
	return false
}

func isGuestBootstrapCommand(command transport.Command) bool {
	return len(command.Stdin) > 0 &&
		strings.Contains(string(command.Stdin), "mds.guest-bootstrap/v1")
}

func findGuestMDSCommand(
	t *testing.T,
	commands []transport.Command,
) transport.Command {
	t.Helper()
	for _, command := range commands {
		if isGuestMDSCommand(command) {
			return command
		}
	}
	t.Fatal("guest-local mds handoff command was not executed")
	return transport.Command{}
}

func guestPlanIdentityJSON(
	targetID,
	cliRevision,
	catalogRevision string,
) string {
	id, _ := target.ParseID(targetID)
	encoded, _ := json.Marshal(struct {
		CatalogRevision string       `json:"catalog_revision"`
		Target          target.Facts `json:"target"`
	}{
		CatalogRevision: catalogRevision,
		Target: target.Facts{
			ID:              id,
			CLIRevision:     cliRevision,
			CatalogRevision: catalogRevision,
		},
	})
	return string(encoded)
}
