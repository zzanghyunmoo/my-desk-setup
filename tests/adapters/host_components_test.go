package adapters_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	hostadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/host"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
)

const (
	hostRuntimeCLIRevision     = "1.2.3 (commit=reviewed, date=2026-07-30T00:00:00Z)"
	hostRuntimeCatalogRevision = "sha256:reviewed-catalog"
)

func TestHostAllContainsNoGuestToolchainOrAuthCommand(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	id, _ := target.NewID(target.KindMacOSHost, "local")
	plan, err := planning.Build(
		environment,
		target.Facts{ID: id, OS: "darwin", Architecture: "arm64", Reachable: true},
		planning.All(),
	)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	for _, action := range plan.Actions {
		switch action.ComponentID {
		case "java", "kotlin", "go", "python", "flutter", "neovim", "docker-engine":
			t.Fatalf("host all contains guest-owned component %q", action.ComponentID)
		}
		for _, command := range action.Verification {
			for _, argument := range command {
				if argument == "login" || argument == "auth" {
					t.Fatalf("verification contains authentication command: %v", command)
				}
			}
		}
	}
}

func TestLimaRuntimeCreatesPinnedUbuntuGuest(t *testing.T) {
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			if command.Executable == "limactl" &&
				len(command.Arguments) > 0 &&
				command.Arguments[0] == "list" {
				return transport.Result{Stdout: ""}
			}
			if isHostRuntimeMDSCommand(command) {
				return transport.Result{Stdout: hostRuntimePlanIdentity(
					"lima-guest:mds",
				)}
			}
			return transport.Result{}
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture: "arm64", Port: port,
		Delegate: readyComponent{
			observation: adapters.Observation{State: adapters.StateReady},
		},
		CLIRevision:     hostRuntimeCLIRevision,
		CatalogRevision: hostRuntimeCatalogRevision,
		Spec: guest.Spec{
			WSLDistribution: "Ubuntu-26.04",
			Images: map[string]guest.ImageSpec{
				"arm64": {
					URL:    "https://cloud-images.example/ubuntu-26.04-arm64.img",
					SHA256: strings.Repeat("a", 64),
				},
			},
		},
	}
	if err := runtime.Apply(context.Background(), planning.Action{
		ID: "macos-host:local/lima", ComponentID: "lima",
	}); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	joined := recordedArgv(port.commands)
	for _, expected := range []string{
		"limactl create --name mds",
		".images[0].location=https://cloud-images.example/ubuntu-26.04-arm64.img",
		".images[0].digest=sha256:" + strings.Repeat("a", 64),
		"limactl start mds",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("commands do not contain %q:\n%s", expected, joined)
		}
	}
}

func TestWSLRuntimeInstallsCanonicalGuestWithoutAuth(t *testing.T) {
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			if command.Executable == "wsl.exe" &&
				len(command.Arguments) >= 2 &&
				command.Arguments[0] == "--list" {
				return transport.Result{Stdout: ""}
			}
			if isHostRuntimeMDSCommand(command) {
				return transport.Result{Stdout: hostRuntimePlanIdentity(
					"wsl-guest:Ubuntu-26.04",
				)}
			}
			return transport.Result{}
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture: "amd64", Port: port,
		Delegate: readyComponent{
			observation: adapters.Observation{State: adapters.StateAbsent},
		},
		Spec:            guest.Spec{WSLDistribution: "Ubuntu-26.04"},
		CLIRevision:     hostRuntimeCLIRevision,
		CatalogRevision: hostRuntimeCatalogRevision,
	}
	if err := runtime.Apply(context.Background(), planning.Action{
		ID: "windows-host:local/wsl", ComponentID: "wsl",
	}); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	joined := recordedArgv(port.commands)
	for _, expected := range []string{
		"wsl.exe --install --no-distribution",
		"wsl.exe --install --distribution Ubuntu-26.04 --no-launch",
		"wsl.exe --distribution Ubuntu-26.04 --exec true",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("commands do not contain %q:\n%s", expected, joined)
		}
	}
	for _, forbidden := range []string{" auth ", " login ", "token"} {
		if strings.Contains(strings.ToLower(joined), forbidden) {
			t.Fatalf("WSL lifecycle contains forbidden auth surface %q:\n%s", forbidden, joined)
		}
	}
}

func TestProductionHostAdapterRequiresMatchingGuestRuntimeRevision(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	catalogRevision, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(): %v", err)
	}
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			switch {
			case command.Executable == "limactl" &&
				len(command.Arguments) > 0 &&
				command.Arguments[0] == "list":
				return transport.Result{
					Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
				}
			case isHostRuntimeMDSCommand(command):
				return transport.Result{Stdout: hostRuntimePlanIdentityWithRevisions(
					"lima-guest:mds",
					version.String(),
					catalogRevision,
				)}
			default:
				return transport.Result{Stdout: "limactl version 2.1.1\n"}
			}
		},
	}
	component, err := hostadapter.New(
		environment,
		port,
		t.TempDir(),
		"darwin",
		"arm64",
		false,
	)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	observation, err := component.Observe(context.Background(), planning.Action{
		ID:          "macos-host:local/lima",
		ComponentID: "lima",
		Installer:   "brew",
		Version:     "manager-owned",
		Verification: [][]string{
			{"limactl", "--version"},
		},
	})
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateReady {
		t.Fatalf("observation = %+v, want matching production handoff ready", observation)
	}
	if !isHostRuntimeMDSCommand(port.commands[len(port.commands)-1]) {
		t.Fatalf("production adapter did not execute guest-local mds: %+v", port.commands)
	}
}

func isHostRuntimeMDSCommand(command transport.Command) bool {
	for index := 0; index+1 < len(command.Arguments); index++ {
		if command.Arguments[index] == "mds" &&
			command.Arguments[index+1] == "plan" {
			return true
		}
	}
	return false
}

func hostRuntimePlanIdentity(targetID string) string {
	return hostRuntimePlanIdentityWithRevisions(
		targetID,
		hostRuntimeCLIRevision,
		hostRuntimeCatalogRevision,
	)
}

func hostRuntimePlanIdentityWithRevisions(
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

func TestWindowsDesktopUsesWinGetInventoryWithoutLaunchingApp(t *testing.T) {
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			return transport.Result{
				Stdout: "Name  Id  Version\nNotion  Notion.Notion  4.0.0\n",
			}
		},
	}
	desktop := hostadapter.Desktop{
		Platform: "windows", Port: port, Delegate: readyComponent{},
	}
	observation, err := desktop.Observe(context.Background(), planning.Action{
		ComponentID: "notion-desktop",
		Package:     "Notion.Notion",
		Version:     "manager-owned",
	})
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateReady {
		t.Fatalf("observation = %+v, want ready", observation)
	}
	joined := recordedArgv(port.commands)
	if !strings.Contains(joined, "winget list --id Notion.Notion --exact") {
		t.Fatalf("desktop probe = %s", joined)
	}
	for _, forbidden := range []string{" open ", " start ", " login ", " auth "} {
		if strings.Contains(" "+strings.ToLower(joined)+" ", forbidden) {
			t.Fatalf("desktop probe contains forbidden operation %q: %s", forbidden, joined)
		}
	}
}
