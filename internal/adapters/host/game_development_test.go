package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

type gamePort struct {
	commands []transport.Command
	run      func(transport.Command) (transport.Result, error)
}

func (port *gamePort) Run(_ context.Context, command transport.Command) (transport.Result, error) {
	port.commands = append(port.commands, command)
	if port.run != nil {
		return port.run(command)
	}
	return transport.Result{}, nil
}

func extensionAction() planning.Action {
	return planning.Action{ComponentID: "vscode-csharp", Version: "2.151.28", Inputs: map[string]string{"install_ref": "ms-dotnettools.csharp"}}
}

func editorAction() planning.Action {
	return planning.Action{ComponentID: "unity-editor", Version: "6000.3.23f1", Inputs: map[string]string{"install_ref": "09d2ecc7fb28"}}
}

func TestVSCodeExtensionExactReadyIsNoOp(t *testing.T) {
	port := &gamePort{run: func(command transport.Command) (transport.Result, error) {
		return transport.Result{Stdout: "MS-DOTNETTOOLS.CSHARP@2.151.28\r\n"}, nil
	}}
	extension := VSCodeExtension{Port: port, Executable: "/path with spaces/code"}
	if err := extension.Apply(context.Background(), extensionAction()); err != nil {
		t.Fatal(err)
	}
	if len(port.commands) != 1 || port.commands[0].Executable != "/path with spaces/code" {
		t.Fatalf("commands = %#v", port.commands)
	}
}

func TestVSCodeExtensionAbsentInstallsExactVersion(t *testing.T) {
	port := &gamePort{run: func(command transport.Command) (transport.Result, error) { return transport.Result{}, nil }}
	extension := VSCodeExtension{Port: port}
	if err := extension.Apply(context.Background(), extensionAction()); err != nil {
		t.Fatal(err)
	}
	if len(port.commands) != 2 {
		t.Fatalf("commands = %#v", port.commands)
	}
	want := []string{"--install-extension", "ms-dotnettools.csharp@2.151.28", "--force"}
	if !reflect.DeepEqual(port.commands[1].Arguments, want) {
		t.Fatalf("install args = %v, want %v", port.commands[1].Arguments, want)
	}
}

func TestVSCodeExtensionMismatchFailsClosed(t *testing.T) {
	port := &gamePort{run: func(command transport.Command) (transport.Result, error) {
		return transport.Result{Stdout: "ms-dotnettools.csharp@2.0.0"}, nil
	}}
	err := (VSCodeExtension{Port: port}).Apply(context.Background(), extensionAction())
	if err == nil || len(port.commands) != 1 {
		t.Fatalf("err=%v commands=%#v", err, port.commands)
	}
}

func TestUnityEditorApplyUsesExactMacIdentity(t *testing.T) {
	port := &gamePort{run: func(command transport.Command) (transport.Result, error) {
		return transport.Result{Stdout: `{"success":true,"data":[]}`}, nil
	}}
	editor := UnityEditor{Platform: "darwin", Architecture: "arm64", Port: port}
	if err := editor.Apply(context.Background(), editorAction()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--no-banner", "--no-pager", "--non-interactive",
		"install", "6000.3.23f1", "--changeset", "09d2ecc7fb28",
		"--yes", "--accept-eula", "--architecture", "arm64",
	}
	if len(port.commands) != 2 || !reflect.DeepEqual(port.commands[1].Arguments, want) {
		t.Fatalf("commands = %#v", port.commands)
	}
	for _, key := range []string{
		"UNITY_NO_CONSENT_PROMPT", "UNITY_NO_CRASH_REPORT", "UNITY_NO_UPDATE_CHECK",
	} {
		if port.commands[1].Environment[key] != "1" {
			t.Fatalf("install environment %s = %q", key, port.commands[1].Environment[key])
		}
	}
}

func TestUnityEditorApplyUsesExactWindowsIdentity(t *testing.T) {
	port := &gamePort{run: func(command transport.Command) (transport.Result, error) {
		return transport.Result{Stdout: `{"success":true,"data":[]}`}, nil
	}}
	if err := (UnityEditor{Platform: "windows", Architecture: "amd64", Port: port}).Apply(context.Background(), editorAction()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--no-banner", "--no-pager", "--non-interactive",
		"install", "6000.3.23f1", "--changeset", "09d2ecc7fb28",
		"--yes", "--accept-eula", "--architecture", "x86_64",
	}
	if len(port.commands) != 2 || !reflect.DeepEqual(port.commands[1].Arguments, want) {
		t.Fatalf("commands = %#v", port.commands)
	}
}

func TestUnityEditorReadyAndVerifyUseReadOnlyCLI(t *testing.T) {
	calls := 0
	port := &gamePort{run: func(command transport.Command) (transport.Result, error) {
		calls++
		if calls == 1 {
			return transport.Result{Stdout: `{"success":true,"command":"editors","data":[{"version":"6000.3.23f1","alias":"6.3.23f1","architecture":"arm64","location":"/Applications/Unity/Hub/Editor/6000.3.23f1/Unity.app","modules":"","default":false}],"errors":[],"warnings":[]}`}, nil
		}
		return transport.Result{Stdout: `{"success":true,"data":[]}`}, nil
	}}
	editor := UnityEditor{Platform: "darwin", Architecture: "arm64", Port: port}
	if err := editor.Verify(context.Background(), editorAction()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--no-banner", "--no-pager", "--non-interactive",
		"editors", "verify", "6000.3.23f1", "--architecture", "arm64",
		"--format", "json",
	}
	if !reflect.DeepEqual(port.commands[1].Arguments, want) {
		t.Fatalf("verify args = %v", port.commands[1].Arguments)
	}
}

func TestMacDesktopFallsBackToInstalledBundleBeforeLaunchServicesRefresh(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Unity Hub.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	port := &gamePort{run: func(transport.Command) (transport.Result, error) {
		return transport.Result{}, errors.New("LaunchServices has not registered the app yet")
	}}
	desktop := Desktop{
		Platform: "darwin", Port: port,
		Delegate: VSCodeExtension{Port: port}, ApplicationsRoot: root,
	}
	observation, err := desktop.Observe(context.Background(), planning.Action{
		ComponentID: "unity-hub", Package: "unity-hub",
	})
	if err != nil || observation.State != adapters.StateReady {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
}

func TestVSCodeExtensionResolvesInstalledWindowsCLI(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "Programs", "Microsoft VS Code", "Code.exe")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	extension := VSCodeExtension{Platform: "windows", LocalAppData: root}
	if got := extension.executable(); got != executable {
		t.Fatalf("executable = %q, want %q", got, executable)
	}
}

func TestHostRouterUsesGameDevelopmentAdapters(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatal(err)
	}
	port := &gamePort{run: func(command transport.Command) (transport.Result, error) {
		switch command.Executable {
		case "custom-code":
			return transport.Result{Stdout: "ms-dotnettools.csharp@2.151.28"}, nil
		case "custom-unity":
			return transport.Result{Stdout: `{"success":true,"data":[]}`}, nil
		default:
			return transport.Result{}, nil
		}
	}}
	router, err := NewWithOptions(environment, port, "/tmp/home", "darwin", "arm64", Options{CodeExecutable: "custom-code", UnityExecutable: "custom-unity"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Observe(context.Background(), extensionAction()); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Observe(context.Background(), editorAction()); err != nil {
		t.Fatal(err)
	}
	if len(port.commands) != 2 || port.commands[0].Executable != "custom-code" || port.commands[1].Executable != "custom-unity" {
		t.Fatalf("routed commands = %#v", port.commands)
	}
}

func TestUnityEditorMismatchAndCommandFailureAreNotReady(t *testing.T) {
	for name, runner := range map[string]func(transport.Command) (transport.Result, error){
		"architecture-mismatch": func(transport.Command) (transport.Result, error) {
			return transport.Result{Stdout: `{"success":true,"data":[{"version":"6000.3.23f1","architecture":"x86_64"}]}`}, nil
		},
		"missing-architecture": func(transport.Command) (transport.Result, error) {
			return transport.Result{Stdout: `{"success":true,"data":[{"version":"6000.3.23f1","changeset":"09d2ecc7fb28"}]}`}, nil
		},
		"failure": func(transport.Command) (transport.Result, error) { return transport.Result{}, errors.New("failed") },
	} {
		t.Run(name, func(t *testing.T) {
			observation, err := (UnityEditor{
				Platform: "darwin", Architecture: "arm64", Port: &gamePort{run: runner},
			}).Observe(context.Background(), editorAction())
			if err != nil || observation.State != adapters.StateConflict {
				t.Fatalf("observation=%+v err=%v", observation, err)
			}
		})
	}
}

func TestUnityEditorRejectsUnreviewedTargetBeforeCommand(t *testing.T) {
	port := &gamePort{}
	observation, err := (UnityEditor{
		Platform: "darwin", Architecture: "amd64", Port: port,
	}).Observe(context.Background(), editorAction())
	if err != nil || observation.State != adapters.StateConflict {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	if len(port.commands) != 0 {
		t.Fatalf("unreviewed target executed commands: %#v", port.commands)
	}
}
