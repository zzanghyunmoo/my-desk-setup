package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestVSCodeExtensionUsesWindowsCLIRatherThanGUI(t *testing.T) {
	for _, folder := range []string{"", "645f29cc31"} {
		t.Run("version-folder="+folder, func(t *testing.T) {
			// Given an installed Windows launcher, including the versioned layout.
			root := t.TempDir()
			install := filepath.Join(root, "Programs", "Microsoft VS Code")
			cli := filepath.Join(install, folder, "resources", "app", "out", "cli.js")
			for _, directory := range []string{filepath.Join(install, "bin"), filepath.Dir(cli)} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{filepath.Join(install, "Code.exe"), cli} {
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			version := ""
			if folder != "" {
				version = folder + `\`
			}
			launcher := fmt.Sprintf("@echo off\r\nset ELECTRON_RUN_AS_NODE=1\r\n\"%%~dp0..\\Code.exe\" \"%%~dp0..\\%sresources\\app\\out\\cli.js\" %%*\r\n", version)
			if err := os.WriteFile(filepath.Join(install, "bin", "code.cmd"), []byte(launcher), 0o644); err != nil {
				t.Fatal(err)
			}
			port := &gamePort{run: func(command transport.Command) (transport.Result, error) {
				devMode, hasDevMode := command.Environment["VSCODE_DEV"]
				if command.Environment["ELECTRON_RUN_AS_NODE"] != "1" ||
					!hasDevMode || devMode != "" ||
					len(command.Arguments) == 0 || command.Arguments[0] != cli {
					return transport.Result{}, fmt.Errorf("GUI entry point used instead of the installed CLI: %+v", command)
				}
				return transport.Result{}, nil
			}}
			extension := VSCodeExtension{Platform: "windows", LocalAppData: root, Port: port}

			// When both the inventory and exact extension install execute.
			err := extension.Apply(context.Background(), extensionAction())
			// Then both use Node CLI mode and preserve the reviewed install arguments.
			if err != nil {
				t.Fatal(err)
			}
			wantList := []string{cli, "--list-extensions", "--show-versions"}
			wantInstall := []string{cli, "--install-extension", "ms-dotnettools.csharp@2.151.28", "--force"}
			if len(port.commands) != 2 || !reflect.DeepEqual(port.commands[0].Arguments, wantList) ||
				!reflect.DeepEqual(port.commands[1].Arguments, wantInstall) {
				t.Fatalf("commands = %+v, want list %v and install %v", port.commands, wantList, wantInstall)
			}
		})
	}
}

func TestVSCodeExtensionMissingWindowsLauncherNeverStartsGUI(t *testing.T) {
	// Given an incomplete installation with no command-line launcher.
	root := t.TempDir()
	executable := filepath.Join(root, "Programs", "Microsoft VS Code", "Code.exe")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	port := &gamePort{}
	extension := VSCodeExtension{Platform: "windows", LocalAppData: root, Port: port}

	// When inventory is requested.
	observation, err := extension.Observe(context.Background(), extensionAction())

	// Then a broken CLI is a conflict, not an absent extension or a GUI launch.
	if err != nil || observation.State != adapters.StateConflict || len(port.commands) != 0 {
		t.Fatalf("observation=%+v err=%v commands=%+v", observation, err, port.commands)
	}
}

func TestVSCodeExtensionInvalidWindowsLauncherNeverRunsCommands(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		launcher string
		detail   string
	}{
		{"unsupported syntax", "@echo off\r\n", "no supported CLI entry point"},
		{"path escape", `"%~dp0..\..\outside\resources\app\out\cli.js"`, "escapes its installation"},
		{"missing script", `"%~dp0..\resources\app\out\cli.js"`, "locate installed VS Code CLI entry point"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given a readable launcher and an existing script outside the installation.
			root := t.TempDir()
			install := filepath.Join(root, "Programs", "Microsoft VS Code")
			for _, path := range []string{
				filepath.Join(install, "Code.exe"),
				filepath.Join(root, "Programs", "outside", "resources", "app", "out", "cli.js"),
			} {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(install, "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(install, "bin", "code.cmd"), []byte(scenario.launcher), 0o644); err != nil {
				t.Fatal(err)
			}
			port := &gamePort{}
			extension := VSCodeExtension{Platform: "windows", LocalAppData: root, Port: port}

			// When observation and apply encounter the unusable CLI entry point.
			observation, err := extension.Observe(context.Background(), extensionAction())
			if err != nil || observation.State != adapters.StateConflict ||
				!strings.Contains(observation.Detail, scenario.detail) {
				t.Fatalf("observation=%+v err=%v", observation, err)
			}
			err = extension.Apply(context.Background(), extensionAction())

			// Then both fail closed without running the GUI or installing an extension.
			if err == nil || !strings.Contains(err.Error(), scenario.detail) || len(port.commands) != 0 {
				t.Fatalf("err=%v commands=%+v", err, port.commands)
			}
		})
	}
}
