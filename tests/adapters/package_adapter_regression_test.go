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
							command.Arguments[1] == "/usr/bin/true" {
							return nil
						}
						return executionErr
					},
				},
				Environment: packageTestEnvironment("base-cli"),
			}
			action := planning.Action{
				ID:          "test-host:local/base-cli",
				ComponentID: "base-cli",
				Installer:   installer,
				Package:     "example.tool",
				Version:     version,
			}
			if installer == "mise" {
				action.Inputs = map[string]string{
					"artifact_sha256": "reviewed-digest",
					"artifact_url":    "https://example.com/tool",
					"mise_ref":        version,
				}
				adapter.Environment.Catalog.Components[0].VersionPolicy =
					catalog.VersionPolicy{Mode: "pinned", LockKey: "tool"}
				adapter.Environment.Lock = catalog.VersionLock{
					SchemaVersion: 1,
					Versions: map[string]catalog.LockEntry{
						"tool": {
							Version: version,
							Artifacts: map[string]catalog.Artifact{
								"linux-amd64": {
									URL:    action.Inputs["artifact_url"],
									SHA256: action.Inputs["artifact_sha256"],
								},
							},
						},
					},
				}
			}
			err := adapter.Apply(context.Background(), action)
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
				Environment: packageTestEnvironment("base-cli"),
			}
			observation, err := adapter.Observe(
				context.Background(),
				planning.Action{
					ID:          "test-host:local/base-cli",
					ComponentID: "base-cli",
					Installer:   "vendor",
					Package:     "example.tool",
					Version:     "1.26.5",
					Verification: [][]string{
						{"git", "--version"},
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

func TestPackageAdapterRejectsPrivilegedVerificationCommands(t *testing.T) {
	t.Parallel()

	port := &recordingPort{}
	adapter := packages.Adapter{
		Home:        t.TempDir(),
		Port:        port,
		Environment: packageTestEnvironment("tool"),
	}
	action := planning.Action{
		ID:          "test-host:local/tool",
		ComponentID: "tool",
		Installer:   "vendor",
		Version:     "manager-owned",
		Verification: [][]string{{
			"/usr/bin/sudo", "-n", "/bin/sh", "-c", "id",
		}},
	}

	observation, err := adapter.Observe(context.Background(), action)
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateConflict ||
		len(port.commands) != 0 {
		t.Fatalf(
			"Observe() = %+v commands=%+v, want fail-closed before transport",
			observation,
			port.commands,
		)
	}
	if err := adapter.Verify(context.Background(), action); err == nil ||
		len(port.commands) != 0 {
		t.Fatalf(
			"Verify() error=%v commands=%+v, want fail-closed before transport",
			err,
			port.commands,
		)
	}
}

func TestPackageAdapterRejectsPrivilegeEscalationAliasesAndWrappers(t *testing.T) {
	for _, verification := range [][][]string{
		{{"sudo", "-n", "/usr/bin/true"}},
		{{"/bin/sudo", "-n", "/usr/bin/true"}},
		{{"/bin/sh", "-c", "sudo -n /usr/bin/true"}},
	} {
		port := &recordingPort{}
		adapter := packages.Adapter{
			Port: port,
		}
		err := adapter.Verify(context.Background(), planning.Action{
			ID:           "lima-guest:mds/fixture",
			ComponentID:  "fixture",
			Verification: verification,
		})
		if err == nil {
			t.Fatalf("Verify(%v) succeeded", verification)
		}
		if len(port.commands) != 0 {
			t.Fatalf(
				"verification %v reached transport: %+v",
				verification,
				port.commands,
			)
		}
	}
}

func TestPackageAdapterRejectsCatalogPrivilegeEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		componentID string
		command     transport.Command
	}{
		{
			componentID: "base-cli",
			command: transport.Command{
				Executable: "/usr/bin/sudo",
				Arguments: []string{
					"-n", "/usr/bin/install", "-m", "0644",
					"/tmp/attacker-key", "/etc/apt/keyrings/docker.asc",
				},
			},
		},
		{
			componentID: "python",
			command: transport.Command{
				Executable: "python",
				Arguments: []string{
					"-c", "import os; os.system('/usr/bin/sudo -n true')",
				},
			},
		},
		{
			componentID: "python",
			command: transport.Command{
				Executable: "python",
				Arguments:  []string{"/tmp/escape.py"},
			},
		},
		{
			componentID: "bun",
			command: transport.Command{
				Executable: "bun",
				Arguments:  []string{"/tmp/escape.js"},
			},
		},
		{
			componentID: "base-cli",
			command: transport.Command{
				Executable: "git",
				Arguments: []string{
					"-c", "alias.pwn=!sudo -n /usr/bin/true", "pwn",
				},
			},
		},
		{
			componentID: "gh",
			command: transport.Command{
				Executable: "gh",
				Arguments:  []string{"auth", "token"},
			},
		},
		{
			componentID: "gh",
			command: transport.Command{
				Executable: "/tmp/gh",
				Arguments:  []string{"--version"},
			},
		},
		{
			componentID: "fixture",
			command: transport.Command{
				Executable: "custom-helper",
				Arguments:  []string{"--version"},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.componentID+"/"+test.command.Executable, func(t *testing.T) {
			t.Parallel()
			if err := packages.ValidateCatalogVerificationCommand(
				test.componentID,
				test.command,
			); err == nil {
				t.Fatalf(
					"ValidateCatalogVerificationCommand(%+v) succeeded",
					test.command,
				)
			}
		})
	}
}

func TestPackageAdapterAllowsReviewedCatalogInterpreterSmoke(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		componentID string
		command     transport.Command
	}{
		{
			componentID: "python",
			command: transport.Command{
				Executable: "python",
				Arguments:  []string{"-c", "print('ok')"},
			},
		},
		{
			componentID: "bun",
			command: transport.Command{
				Executable: "bun",
				Arguments:  []string{"-e", "console.log('ok')"},
			},
		},
	} {
		if err := packages.ValidateCatalogVerificationCommand(
			test.componentID,
			test.command,
		); err != nil {
			t.Fatalf(
				"ValidateCatalogVerificationCommand(%+v): %v",
				test.command,
				err,
			)
		}
	}
}

func packageTestEnvironment(componentID string) catalog.Environment {
	return catalog.Environment{
		Mise: catalog.MiseFiles{
			Config: "[tools]\ntool = \"1.2.3\"\n",
			Lock:   "[[tools.tool]]\nversion = \"1.2.3\"\n",
		},
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
