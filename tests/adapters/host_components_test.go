package adapters_test

import (
	"context"
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
			return transport.Result{}
		},
	}
	runtime := hostadapter.GuestRuntime{
		HostOS: "darwin", Architecture: "arm64", Port: port,
		Delegate: readyComponent{
			observation: adapters.Observation{State: adapters.StateReady},
		},
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
			return transport.Result{}
		},
	}
	runtime := hostadapter.GuestRuntime{
		HostOS: "windows", Architecture: "amd64", Port: port,
		Delegate: readyComponent{
			observation: adapters.Observation{State: adapters.StateAbsent},
		},
		Spec: guest.Spec{WSLDistribution: "Ubuntu-26.04"},
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
