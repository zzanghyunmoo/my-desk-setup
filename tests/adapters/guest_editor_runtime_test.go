package adapters_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	guestadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestEditorRefusesUserOwnedConfiguration(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create user config: %v", err)
	}
	editor := guestadapter.Editor{Home: home}
	observation, err := editor.Observe(context.Background(), nvchadAction())
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateConflict ||
		!strings.Contains(observation.Detail, "user-owned") {
		t.Fatalf("observation = %+v, want user-owned conflict", observation)
	}
}

func TestEditorRefusesNonRegularConfigurationRootsWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string) string
	}{
		{
			name: "regular file",
			setup: func(t *testing.T, root string) string {
				t.Helper()
				if err := os.WriteFile(root, []byte("keep-file\n"), 0o600); err != nil {
					t.Fatalf("write non-directory config root: %v", err)
				}
				return root
			},
		},
		{
			name: "symlink",
			setup: func(t *testing.T, root string) string {
				t.Helper()
				target := filepath.Join(t.TempDir(), "user-nvim")
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatalf("create symlink target: %v", err)
				}
				if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("keep-link\n"), 0o600); err != nil {
					t.Fatalf("write symlink target: %v", err)
				}
				if err := os.Symlink(target, root); err != nil {
					t.Skipf("create config symlink: %v", err)
				}
				return filepath.Join(target, "sentinel")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			configDirectory := filepath.Join(home, ".config")
			if err := os.Mkdir(configDirectory, 0o700); err != nil {
				t.Fatalf("create config directory: %v", err)
			}
			root := filepath.Join(configDirectory, "nvim")
			preservedPath := test.setup(t, root)
			editor := guestadapter.Editor{
				Home: home, Port: &recordingPort{}, Now: time.Now,
			}
			observation, err := editor.Observe(context.Background(), nvchadAction())
			if err != nil || observation.State != adapters.StateConflict {
				t.Fatalf("Observe() = %+v, err=%v, want conflict", observation, err)
			}
			if err := editor.Apply(context.Background(), nvchadAction()); err == nil {
				t.Fatal("Apply() accepted non-regular config root")
			}
			content, err := os.ReadFile(preservedPath)
			if err != nil || !strings.HasPrefix(string(content), "keep-") {
				t.Fatalf("original config changed: content=%q err=%v", content, err)
			}
		})
	}
}

func TestEditorRefusesSymlinkedOwnershipMarkersWithoutFollowingThem(t *testing.T) {
	for _, markerRelativePath := range []string{
		filepath.Join(".config", "nvim", ".mds-managed.json"),
		filepath.Join(".local", "share", "mds", "nvim", ".mds-managed.json"),
	} {
		t.Run(markerRelativePath, func(t *testing.T) {
			home := t.TempDir()
			port := &recordingPort{result: func(command transport.Command) transport.Result {
				return managedEditorCommandResult(t, home, nvchadAction().Version, command)
			}}
			editor := guestadapter.Editor{Home: home, Port: port, Now: time.Now}
			if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
				t.Fatalf("Editor Apply(): %v", err)
			}
			marker := filepath.Join(home, markerRelativePath)
			content, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("read marker: %v", err)
			}
			external := filepath.Join(t.TempDir(), "external-marker")
			if err := os.WriteFile(external, content, 0o600); err != nil {
				t.Fatalf("write external marker: %v", err)
			}
			if err := os.Remove(marker); err != nil {
				t.Fatalf("remove managed marker: %v", err)
			}
			if err := os.Symlink(external, marker); err != nil {
				t.Skipf("create marker symlink: %v", err)
			}
			if _, err := editor.Observe(context.Background(), nvchadAction()); err == nil ||
				!strings.Contains(err.Error(), "regular file") {
				t.Fatalf("Observe(marker symlink) error=%v, want regular-file refusal", err)
			}
			if err := editor.Apply(context.Background(), nvchadAction()); err == nil ||
				!strings.Contains(err.Error(), "regular file") {
				t.Fatalf("Apply(marker symlink) error=%v, want regular-file refusal", err)
			}
			preserved, err := os.ReadFile(external)
			if err != nil || string(preserved) != string(content) {
				t.Fatalf("external marker changed: content=%q err=%v", preserved, err)
			}
		})
	}
}

func TestManagedEditorAndRuntimeRefuseIntermediateSymlinks(t *testing.T) {
	t.Run("IDE config parent", func(t *testing.T) {
		home := t.TempDir()
		port := &recordingPort{result: func(command transport.Command) transport.Result {
			return managedEditorCommandResult(t, home, nvchadAction().Version, command)
		}}
		editor := guestadapter.Editor{Home: home, Port: port, Now: time.Now}
		if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
			t.Fatalf("Editor Apply(): %v", err)
		}
		external := t.TempDir()
		sentinel := filepath.Join(external, "sentinel")
		if err := os.WriteFile(sentinel, []byte("keep\n"), 0o600); err != nil {
			t.Fatalf("write external sentinel: %v", err)
		}
		configs := filepath.Join(home, ".config", "nvim", "lua", "configs")
		if err := os.Symlink(external, configs); err != nil {
			t.Skipf("create configs symlink: %v", err)
		}
		ide := guestadapter.IDE{Home: home, Port: port, Delegate: readyComponent{
			observation: adapters.Observation{State: adapters.StateReady},
		}}
		if err := ide.Apply(context.Background(), ideAction()); err == nil ||
			!strings.Contains(err.Error(), "regular directory") {
			t.Fatalf("IDE Apply(config parent symlink) error=%v", err)
		}
		content, err := os.ReadFile(sentinel)
		if err != nil || string(content) != "keep\n" {
			t.Fatalf("external config tree changed: content=%q err=%v", content, err)
		}
	})

	t.Run("plugin runtime parent", func(t *testing.T) {
		home := t.TempDir()
		port := &recordingPort{result: func(command transport.Command) transport.Result {
			return managedEditorCommandResult(t, home, nvchadAction().Version, command)
		}}
		editor := guestadapter.Editor{Home: home, Port: port, Now: time.Now}
		if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
			t.Fatalf("Editor Apply(): %v", err)
		}
		pluginParent := filepath.Join(home, ".local", "share", "mds", "nvim", "p")
		if err := os.RemoveAll(pluginParent); err != nil {
			t.Fatalf("remove managed plugin parent fixture: %v", err)
		}
		external := t.TempDir()
		sentinel := filepath.Join(external, "sentinel")
		if err := os.WriteFile(sentinel, []byte("keep\n"), 0o600); err != nil {
			t.Fatalf("write external sentinel: %v", err)
		}
		if err := os.Symlink(external, pluginParent); err != nil {
			t.Skipf("create plugin parent symlink: %v", err)
		}
		if err := editor.Apply(context.Background(), nvchadAction()); err == nil ||
			!strings.Contains(err.Error(), "regular directory") {
			t.Fatalf("Editor Apply(plugin parent symlink) error=%v", err)
		}
		content, err := os.ReadFile(sentinel)
		if err != nil || string(content) != "keep\n" {
			t.Fatalf("external plugin tree changed: content=%q err=%v", content, err)
		}
	})
}

func TestEditorPublishesExactManagedRevision(t *testing.T) {
	home := t.TempDir()
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			return managedEditorCommandResult(t, home, nvchadAction().Version, command)
		},
	}
	editor := guestadapter.Editor{
		Home: home,
		Port: port,
		Now: func() time.Time {
			return time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
		},
	}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	materializeManagedPluginPaths(t, home, false)
	observation, err := editor.Observe(context.Background(), nvchadAction())
	if err != nil {
		t.Fatalf("Observe(after apply): %v", err)
	}
	if observation.State != adapters.StateReady ||
		observation.InstalledVersion != nvchadAction().Version {
		t.Fatalf("observation = %+v, want exact managed revision", observation)
	}
	if err := editor.Verify(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Verify(): %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(home, ".config", "nvim", ".mds-managed.json"))
	if err != nil {
		t.Fatalf("read ownership marker: %v", err)
	}
	if !strings.Contains(string(marker), `"schema_version": "mds.ownership/v1"`) {
		t.Fatalf("ownership marker = %s", marker)
	}
	for _, command := range port.commands {
		switch filepath.Base(command.Executable) {
		case "sh", "bash", "zsh":
			t.Fatalf("editor used shell transport: %+v", command)
		}
	}
}

func TestEditorVerifyRestoresInitiallyMissingPluginCheckouts(t *testing.T) {
	home := t.TempDir()
	port := &recordingPort{}
	port.result = func(command transport.Command) transport.Result {
		if filepath.Base(command.Executable) == "nvim" && len(command.Arguments) >= 2 &&
			command.Arguments[1] == "+Lazy! restore" {
			materializeManagedPluginPaths(t, home, false)
		}
		return managedEditorCommandResult(t, home, nvchadAction().Version, command)
	}
	editor := guestadapter.Editor{
		Home: home, Port: port,
		Now: func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) },
	}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	materializeManagedNeovimLauncher(t, home)
	observation, err := editor.Observe(context.Background(), nvchadAction())
	if err != nil || observation.State != adapters.StateAbsent {
		t.Fatalf("Observe(before restore) = %+v, err=%v, want absent", observation, err)
	}
	if err := editor.Verify(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Verify(restore missing plugins): %v", err)
	}
	observation, err = editor.Observe(context.Background(), nvchadAction())
	if err != nil || observation.State != adapters.StateReady {
		t.Fatalf("Observe(after restore) = %+v, err=%v, want ready", observation, err)
	}
}

func TestExplicitUpdateReplacesOnlyManagedEditorConfiguration(t *testing.T) {
	home := t.TempDir()
	revision := "1111111111111111111111111111111111111111"
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			return managedEditorCommandResult(t, home, revision, command)
		},
	}
	action := nvchadAction()
	action.Version = revision
	editor := guestadapter.Editor{
		Home: home, Port: port,
		Now: func() time.Time {
			return time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
		},
	}
	if err := editor.Apply(context.Background(), action); err != nil {
		t.Fatalf("Apply(initial): %v", err)
	}
	materializeManagedPluginPaths(t, home, false)
	revision = "2222222222222222222222222222222222222222"
	action.Version = revision
	editor.AllowReplace = true
	if err := editor.Apply(context.Background(), action); err != nil {
		t.Fatalf("Apply(update): %v", err)
	}
	materializeManagedPluginPaths(t, home, false)
	observation, err := editor.Observe(context.Background(), action)
	if err != nil {
		t.Fatalf("Observe(updated): %v", err)
	}
	if observation.State != adapters.StateReady ||
		observation.InstalledVersion != revision {
		t.Fatalf("observation = %+v, want updated managed revision", observation)
	}

	userHome := t.TempDir()
	userRoot := filepath.Join(userHome, ".config", "nvim")
	if err := os.MkdirAll(userRoot, 0o700); err != nil {
		t.Fatalf("create user config: %v", err)
	}
	userEditor := editor
	userEditor.Home = userHome
	observation, err = userEditor.Observe(context.Background(), action)
	if err != nil {
		t.Fatalf("Observe(user-owned): %v", err)
	}
	if observation.State != adapters.StateConflict {
		t.Fatalf("user-owned observation = %+v, want conflict", observation)
	}
}

func TestExplicitAdoptionBacksUpUserOwnedEditorConfiguration(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create user config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "init.lua"), []byte("-- user configuration\n"), 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}
	port := &recordingPort{result: func(command transport.Command) transport.Result {
		return managedEditorCommandResult(t, home, nvchadAction().Version, command)
	}}
	editor := guestadapter.Editor{
		Home: home, Port: port, AllowAdopt: true,
		Now: func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) },
	}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Apply(adopt): %v", err)
	}
	materializeManagedPluginPaths(t, home, false)
	backupMatches, err := filepath.Glob(filepath.Join(
		home,
		".config",
		".nvim-mds-backup-20260801T000000Z-*",
	))
	if err != nil || len(backupMatches) != 1 {
		t.Fatalf("backup paths = %v, err = %v", backupMatches, err)
	}
	backupContent, err := os.ReadFile(filepath.Join(backupMatches[0], "init.lua"))
	if err != nil || string(backupContent) != "-- user configuration\n" {
		t.Fatalf("backup content = %q, err = %v", backupContent, err)
	}
	if _, err := os.Stat(filepath.Join(root, "lua", "configs", "lspconfig.lua")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nvchad-only apply published IDE config: %v", err)
	}
	editorObservation, err := editor.Observe(context.Background(), nvchadAction())
	if err != nil || editorObservation.State != adapters.StateReady {
		t.Fatalf("Editor Observe(after adoption) = %+v, err = %v, want ready", editorObservation, err)
	}

	packageDelegate := &countingComponent{
		observation: adapters.Observation{State: adapters.StateReady},
	}
	ide := guestadapter.IDE{Home: home, Port: port, Delegate: packageDelegate}
	observation, err := ide.Observe(context.Background(), ideAction())
	if err != nil || observation.State != adapters.StateAbsent {
		t.Fatalf("IDE Observe(before apply) = %+v, err = %v, want absent", observation, err)
	}
	if err := ide.Apply(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Apply(): %v", err)
	}
	if packageDelegate.applyCalls != 0 {
		t.Fatalf("IDE config-only apply called ready package delegate %d times", packageDelegate.applyCalls)
	}
	materializeManagedPluginPaths(t, home, true)
	if err := ide.Verify(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Verify(): %v", err)
	}
	managed, err := os.ReadFile(filepath.Join(root, "lua", "configs", "lspconfig.lua"))
	if err != nil || !strings.Contains(string(managed), "pyright") {
		t.Fatalf("managed LSP config = %q, err = %v", managed, err)
	}
	assertPinnedIDEPluginGraph(t, home, root)

	lspPath := filepath.Join(root, "lua", "configs", "lspconfig.lua")
	if err := os.WriteFile(lspPath, []byte("-- drifted\n"), 0o600); err != nil {
		t.Fatalf("drift managed IDE config: %v", err)
	}
	observation, err = ide.Observe(context.Background(), ideAction())
	if err != nil || observation.State != adapters.StateAbsent ||
		!strings.Contains(observation.Detail, "differs") {
		t.Fatalf("IDE Observe(drifted) = %+v, err = %v, want absent", observation, err)
	}
	if err := ide.Apply(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Apply(repair): %v", err)
	}
	if packageDelegate.applyCalls != 0 {
		t.Fatalf("IDE drift repair called ready package delegate %d times", packageDelegate.applyCalls)
	}
	if err := ide.Verify(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Verify(repaired): %v", err)
	}
	commands := recordedArgv(port.commands)
	for _, expected := range []string{
		"git -C " + filepath.Join(home, ".local", "share", "mds", "nvim", "p"),
		filepath.Join(home, ".local", "bin", "nvim") + " --headless +Lazy! restore +qa",
		filepath.Join(home, ".local", "bin", "nvim") + " --headless +checkhealth +qa",
	} {
		if !strings.Contains(commands, expected) {
			t.Fatalf("managed IDE verification does not contain %q:\n%s", expected, commands)
		}
	}
}

func TestIDERepairsPartiallyReadyPackageDependencies(t *testing.T) {
	home := t.TempDir()
	port := &recordingPort{result: func(command transport.Command) transport.Result {
		return managedEditorCommandResult(t, home, nvchadAction().Version, command)
	}}
	editor := guestadapter.Editor{Home: home, Port: port, Now: time.Now}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Editor Apply(): %v", err)
	}
	delegate := &countingComponent{
		observation: adapters.Observation{State: adapters.StateReady},
		verifyErr:   errors.New("gopls is missing"),
	}
	ide := guestadapter.IDE{Home: home, Port: port, Delegate: delegate}
	if err := ide.Apply(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Apply(partial package drift): %v", err)
	}
	if delegate.verifyCalls != 1 || delegate.applyCalls != 1 {
		t.Fatalf("delegate verify/apply calls = %d/%d, want 1/1", delegate.verifyCalls, delegate.applyCalls)
	}
}

func TestEditorRepairsBasePluginDriftAfterIDEInstallation(t *testing.T) {
	home := t.TempDir()
	drifted := false
	port := &recordingPort{}
	port.result = func(command transport.Command) transport.Result {
		if drifted && command.Executable == "git" && len(command.Arguments) >= 3 &&
			command.Arguments[2] == "rev-parse" && filepath.Base(command.Arguments[1]) == "NvChad" {
			return transport.Result{Stdout: strings.Repeat("0", 40) + "\n"}
		}
		return managedEditorCommandResult(t, home, nvchadAction().Version, command)
	}
	editor := guestadapter.Editor{Home: home, Port: port, Now: time.Now}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Editor Apply(): %v", err)
	}
	materializeManagedPluginPaths(t, home, false)
	ide := guestadapter.IDE{Home: home, Port: port, Delegate: &countingComponent{
		observation: adapters.Observation{State: adapters.StateReady},
	}}
	if err := ide.Apply(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Apply(): %v", err)
	}
	materializeManagedPluginPaths(t, home, true)
	pluginSpecPath := filepath.Join(home, ".config", "nvim", "lua", "plugins", "init.lua")
	pluginSpecBefore, err := os.ReadFile(pluginSpecPath)
	if err != nil {
		t.Fatalf("read IDE plugin spec before base repair: %v", err)
	}
	observation, err := editor.Observe(context.Background(), nvchadAction())
	if err != nil || observation.State != adapters.StateReady {
		t.Fatalf("Editor Observe(ready IDE graph) = %+v, err=%v, want ready", observation, err)
	}
	drifted = true
	observation, err = editor.Observe(context.Background(), nvchadAction())
	if err != nil || observation.State != adapters.StateAbsent {
		t.Fatalf("Editor Observe(base drift) = %+v, err=%v, want absent", observation, err)
	}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Editor Apply(base drift): %v", err)
	}
	pluginSpecAfter, err := os.ReadFile(pluginSpecPath)
	if err != nil || string(pluginSpecAfter) != string(pluginSpecBefore) {
		t.Fatalf(
			"Editor base repair changed IDE-owned plugin spec: before=%q after=%q err=%v",
			pluginSpecBefore,
			pluginSpecAfter,
			err,
		)
	}
	drifted = false
	materializeManagedPluginPaths(t, home, false)
	if err := editor.Verify(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Editor Verify(repaired base graph): %v", err)
	}
}

func TestEditorLeavesIDEOnlyPluginDriftToIDEAction(t *testing.T) {
	home := t.TempDir()
	driftIDEPlugin := false
	port := &recordingPort{}
	port.result = func(command transport.Command) transport.Result {
		if driftIDEPlugin && command.Executable == "git" && len(command.Arguments) >= 3 &&
			command.Arguments[2] == "rev-parse" && filepath.Base(command.Arguments[1]) == "nvim-dap" {
			return transport.Result{Stdout: strings.Repeat("0", 40) + "\n"}
		}
		return managedEditorCommandResult(t, home, nvchadAction().Version, command)
	}
	editor := guestadapter.Editor{Home: home, Port: port, Now: time.Now}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Editor Apply(): %v", err)
	}
	materializeManagedPluginPaths(t, home, false)
	ide := guestadapter.IDE{Home: home, Port: port, Delegate: readyComponent{
		observation: adapters.Observation{State: adapters.StateReady},
	}}
	if err := ide.Apply(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Apply(): %v", err)
	}
	materializeManagedPluginPaths(t, home, true)
	pluginSpecPath := filepath.Join(home, ".config", "nvim", "lua", "plugins", "init.lua")
	before, err := os.ReadFile(pluginSpecPath)
	if err != nil {
		t.Fatalf("read IDE plugin spec: %v", err)
	}
	driftIDEPlugin = true
	observation, err := editor.Observe(context.Background(), nvchadAction())
	if err != nil || observation.State != adapters.StateReady {
		t.Fatalf("Editor Observe(IDE-only drift) = %+v, err=%v, want ready", observation, err)
	}
	if err := editor.Verify(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Editor Verify(IDE-only drift): %v", err)
	}
	after, err := os.ReadFile(pluginSpecPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("Editor changed IDE-owned plugin spec: before=%q after=%q err=%v", before, after, err)
	}
	observation, err = ide.Observe(context.Background(), ideAction())
	if err != nil || observation.State != adapters.StateAbsent {
		t.Fatalf("IDE Observe(IDE-only drift) = %+v, err=%v, want absent", observation, err)
	}
}

func TestIDERejectsAndRemovesUnexpectedPluginCheckout(t *testing.T) {
	home := t.TempDir()
	port := &recordingPort{result: func(command transport.Command) transport.Result {
		return managedEditorCommandResult(t, home, nvchadAction().Version, command)
	}}
	editor := guestadapter.Editor{Home: home, Port: port, Now: time.Now}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Editor Apply(): %v", err)
	}
	materializeManagedPluginPaths(t, home, false)
	ide := guestadapter.IDE{Home: home, Port: port, Delegate: readyComponent{
		observation: adapters.Observation{State: adapters.StateReady},
	}}
	if err := ide.Apply(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Apply(): %v", err)
	}
	materializeManagedPluginPaths(t, home, true)
	pluginRoots, err := filepath.Glob(filepath.Join(home, ".local", "share", "mds", "nvim", "p", "*"))
	if err != nil || len(pluginRoots) != 1 {
		t.Fatalf("plugin roots=%v err=%v", pluginRoots, err)
	}
	unexpected := filepath.Join(pluginRoots[0], "moving-head-plugin")
	if err := os.Mkdir(unexpected, 0o700); err != nil {
		t.Fatalf("create unexpected checkout: %v", err)
	}
	observation, err := ide.Observe(context.Background(), ideAction())
	if err != nil || observation.State != adapters.StateAbsent ||
		!strings.Contains(observation.Detail, "unexpected plugin checkout") {
		t.Fatalf("IDE Observe(unexpected)=%+v err=%v, want absent", observation, err)
	}
	if err := ide.Apply(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Apply(remove unexpected): %v", err)
	}
	if _, err := os.Lstat(unexpected); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected plugin checkout remains: %v", err)
	}
}

func TestNvChadAloneDoesNotPublishIDEConfiguration(t *testing.T) {
	home := t.TempDir()
	port := &recordingPort{result: func(command transport.Command) transport.Result {
		return managedEditorCommandResult(t, home, nvchadAction().Version, command)
	}}
	editor := guestadapter.Editor{
		Home: home, Port: port,
		Now: func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) },
	}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	materializeManagedPluginPaths(t, home, false)
	for relativePath := range map[string]bool{
		"lua/configs/lspconfig.lua": true,
	} {
		if _, err := os.Stat(filepath.Join(home, ".config", "nvim", filepath.FromSlash(relativePath))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("nvchad-only apply published %s: %v", relativePath, err)
		}
	}
	pluginSpec, err := os.ReadFile(filepath.Join(home, ".config", "nvim", "lua", "plugins", "init.lua"))
	if err != nil || string(pluginSpec) != "return {}\n" {
		t.Fatalf("nvchad-only plugin spec = %q, err = %v", pluginSpec, err)
	}
	for _, plugin := range []string{
		"conform.nvim", "nvim-dap", "nvim-dap-ui", "nvim-dap-virtual-text", "nvim-lint", "nvim-nio",
	} {
		paths, err := filepath.Glob(filepath.Join(
			home, ".local", "share", "mds", "nvim", "p", "*", plugin,
		))
		if err != nil || len(paths) != 0 {
			t.Fatalf("nvchad-only apply materialized IDE plugin %s at %v: %v", plugin, paths, err)
		}
	}
}

func TestIDERejectsAndRepairsDriftedManagedPluginCheckout(t *testing.T) {
	home := t.TempDir()
	drifted := false
	port := &recordingPort{}
	port.result = func(command transport.Command) transport.Result {
		if drifted && command.Executable == "git" && len(command.Arguments) >= 3 &&
			command.Arguments[2] == "rev-parse" && filepath.Base(command.Arguments[1]) == "NvChad" {
			return transport.Result{Stdout: strings.Repeat("0", 40) + "\n"}
		}
		return managedEditorCommandResult(t, home, nvchadAction().Version, command)
	}
	editor := guestadapter.Editor{
		Home: home, Port: port,
		Now: func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) },
	}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Editor Apply(): %v", err)
	}
	materializeManagedPluginPaths(t, home, false)
	ide := guestadapter.IDE{
		Home: home, Port: port,
		Delegate: readyComponent{observation: adapters.Observation{State: adapters.StateReady}},
	}
	if err := ide.Apply(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Apply(): %v", err)
	}
	materializeManagedPluginPaths(t, home, true)
	drifted = true

	observation, err := ide.Observe(context.Background(), ideAction())
	if err != nil {
		t.Fatalf("IDE Observe(drifted plugin): %v", err)
	}
	if observation.State != adapters.StateAbsent ||
		!strings.Contains(observation.Detail, "NvChad checkout revision differs") {
		t.Fatalf("observation=%+v, want exact plugin drift", observation)
	}
	if err := ide.Apply(context.Background(), ideAction()); err != nil {
		t.Fatalf("IDE Apply(repair drift): %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(
		home, ".local", "share", "mds", "nvim", "p", "*", "NvChad",
	))
	if err != nil || len(paths) != 0 {
		t.Fatalf("drifted NvChad checkout was not removed safely: paths=%v err=%v", paths, err)
	}
}

func TestIDEObservesAbsentWhenManagedNvChadIsNotInstalled(t *testing.T) {
	ide := guestadapter.IDE{Home: t.TempDir(), Delegate: readyComponent{}}
	observation, err := ide.Observe(context.Background(), ideAction())
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateAbsent ||
		!strings.Contains(observation.Detail, "starter is missing") {
		t.Fatalf("observation = %+v, want missing starter as absent", observation)
	}
}

func TestGuestAdapterOptionsWireExplicitNvChadAdoption(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create user config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "init.lua"), []byte("-- user\n"), 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}
	port := &recordingPort{result: func(command transport.Command) transport.Result {
		return managedEditorCommandResult(t, home, nvchadAction().Version, command)
	}}
	component := guestadapter.New(
		catalog.Environment{},
		target.Facts{},
		port,
		home,
		"linux",
		"amd64",
		http.DefaultClient,
		func() time.Time { return time.Date(2026, 8, 1, 1, 2, 3, 0, time.UTC) },
		guestadapter.Options{AllowAdopt: true},
	)
	if err := component.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(
		home,
		".config",
		".nvim-mds-backup-20260801T010203Z-*",
	))
	if err != nil || len(matches) != 1 {
		t.Fatalf("backup paths = %v, err = %v", matches, err)
	}
}

func nvchadAction() planning.Action {
	return planning.Action{
		ID:          "lima-guest:mds/nvchad",
		ComponentID: "nvchad",
		Version:     "e3572e1f5e1c297212c3deeb17b7863139ce663e",
		Verification: [][]string{
			{"nvim", "--headless", "+checkhealth", "+quit"},
		},
	}
}

func ideAction() planning.Action {
	return planning.Action{
		ID:          "lima-guest:mds/nvim-ide-tools",
		ComponentID: "nvim-ide-tools",
		Version:     "manager-owned",
		Verification: [][]string{
			{"clangd", "--version"},
			{"gopls", "version"},
		},
	}
}

func assertPinnedIDEPluginGraph(t *testing.T, home, root string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, "lazy-lock.json"))
	if err != nil {
		t.Fatalf("read lazy lock: %v", err)
	}
	var entries map[string]struct {
		Branch string `json:"branch"`
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal(content, &entries); err != nil {
		t.Fatalf("decode lazy lock: %v", err)
	}
	if len(entries) != 31 {
		t.Fatalf("lazy lock entries = %d, want 31", len(entries))
	}
	for name, entry := range entries {
		decoded, err := hex.DecodeString(entry.Commit)
		if err != nil || len(decoded) != 20 || entry.Branch == "" {
			t.Fatalf("plugin %s is not pinned exactly: %+v, err=%v", name, entry, err)
		}
	}
	if _, exists := entries["lazy.nvim"]; exists {
		t.Fatal("lazy.nvim bootstrap revision is duplicated in the plugin lock")
	}
	if got, want := entries["NvChad"].Commit, "add44b952d631981614bbb8cfc6f7002f296dfe6"; got != want {
		t.Fatalf("NvChad commit = %s, want %s", got, want)
	}
	initContent, err := os.ReadFile(filepath.Join(root, "init.lua"))
	if err != nil {
		t.Fatalf("read init.lua: %v", err)
	}
	if revisionPrefix := "306a05526ada"; !strings.Contains(string(initContent), revisionPrefix) {
		t.Fatalf("init.lua does not bind reviewed lazy.nvim path %s", revisionPrefix)
	}
	if strings.Contains(string(initContent), entries["NvChad"].Commit) {
		t.Fatal("init.lua duplicates the NvChad revision owned by lazy-lock.json")
	}
	pluginContent, err := os.ReadFile(filepath.Join(root, "lua", "plugins", "init.lua"))
	if err != nil {
		t.Fatalf("read plugin specification: %v", err)
	}
	if strings.Contains(string(pluginContent), "commit =") {
		t.Fatalf("plugin specification duplicates lazy-lock authority:\n%s", pluginContent)
	}
	assertManagedPluginDirectoryNames(t, home, entries)
}

func assertManagedPluginDirectoryNames(t *testing.T, home string, expected map[string]struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}) {
	t.Helper()
	roots, err := filepath.Glob(filepath.Join(home, ".local", "share", "mds", "nvim", "p", "*"))
	if err != nil || len(roots) != 1 {
		t.Fatalf("managed plugin roots=%v err=%v", roots, err)
	}
	entries, err := os.ReadDir(roots[0])
	if err != nil {
		t.Fatalf("read managed plugin directory: %v", err)
	}
	if len(entries) != len(expected) {
		t.Fatalf("managed plugin directory entries=%d, want %d", len(entries), len(expected))
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok {
			t.Fatalf("unexpected managed plugin directory %s", entry.Name())
		}
	}
}

func managedEditorCommandResult(
	t *testing.T,
	home,
	starterRevision string,
	command transport.Command,
) transport.Result {
	t.Helper()
	if command.Executable != "git" || len(command.Arguments) < 3 {
		return transport.Result{}
	}
	if command.Arguments[0] != "-C" {
		return transport.Result{}
	}
	path := command.Arguments[1]
	switch command.Arguments[2] {
	case "status":
		return transport.Result{}
	case "rev-parse":
		if strings.Contains(path, filepath.Join("mds", "nvim", "l")) {
			return transport.Result{Stdout: "306a05526ada86a7b30af95c5cc81ffba93fef97\n"}
		}
		if strings.Contains(path, filepath.Join("mds", "nvim", "p")) {
			entries := readPluginLock(t, home)
			entry, exists := entries[filepath.Base(path)]
			if !exists {
				t.Fatalf("plugin checkout %s has no lock entry", path)
			}
			return transport.Result{Stdout: entry.Commit + "\n"}
		}
		return transport.Result{Stdout: starterRevision + "\n"}
	default:
		return transport.Result{}
	}
}

type testPluginLockEntry struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

func readPluginLock(t *testing.T, home string) map[string]testPluginLockEntry {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(home, ".config", "nvim", "lazy-lock.json"))
	if err != nil {
		t.Fatalf("read managed plugin lock: %v", err)
	}
	var entries map[string]testPluginLockEntry
	if err := json.Unmarshal(content, &entries); err != nil {
		t.Fatalf("decode managed plugin lock: %v", err)
	}
	return entries
}

func materializeManagedPluginPaths(t *testing.T, home string, includeIDE bool) {
	t.Helper()
	materializeManagedNeovimLauncher(t, home)
	roots, err := filepath.Glob(filepath.Join(
		home,
		".local",
		"share",
		"mds",
		"nvim",
		"p",
		"*",
	))
	if err != nil || len(roots) != 1 {
		t.Fatalf("managed plugin roots = %v, err = %v", roots, err)
	}
	ideOnly := map[string]bool{
		"conform.nvim":          true,
		"nvim-dap":              true,
		"nvim-dap-ui":           true,
		"nvim-dap-virtual-text": true,
		"nvim-lint":             true,
		"nvim-nio":              true,
	}
	for name := range readPluginLock(t, home) {
		if ideOnly[name] && !includeIDE {
			continue
		}
		if err := os.MkdirAll(filepath.Join(roots[0], name), 0o700); err != nil {
			t.Fatalf("materialize plugin %s: %v", name, err)
		}
	}
}

func materializeManagedNeovimLauncher(t *testing.T, home string) {
	t.Helper()
	directory := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create managed launcher directory: %v", err)
	}
	path := filepath.Join(directory, "nvim")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write managed Neovim launcher: %v", err)
	}
}

type countingComponent struct {
	observation adapters.Observation
	applyCalls  int
	verifyCalls int
	verifyErr   error
}

func (component *countingComponent) Observe(
	context.Context,
	planning.Action,
) (adapters.Observation, error) {
	return component.observation, nil
}

func (component *countingComponent) Apply(context.Context, planning.Action) error {
	component.applyCalls++
	return nil
}

func (component *countingComponent) Verify(context.Context, planning.Action) error {
	component.verifyCalls++
	return component.verifyErr
}
