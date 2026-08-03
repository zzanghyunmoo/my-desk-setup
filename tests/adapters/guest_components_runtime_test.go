package adapters_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	guestadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
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
		if strings.Contains(command.Executable, "sh") {
			t.Fatalf("editor used shell transport: %+v", command)
		}
	}
}

func TestEditorVerifyRestoresInitiallyMissingPluginCheckouts(t *testing.T) {
	home := t.TempDir()
	port := &recordingPort{}
	port.result = func(command transport.Command) transport.Result {
		if command.Executable == "nvim" && len(command.Arguments) >= 2 &&
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
	assertPinnedIDEPluginGraph(t, root)

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
		"nvim --headless +Lazy! restore +qa",
		"nvim --headless +checkhealth +qa",
	} {
		if !strings.Contains(commands, expected) {
			t.Fatalf("managed IDE verification does not contain %q:\n%s", expected, commands)
		}
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

func TestDockerRequiresActiveSystemdBeforeMutation(t *testing.T) {
	port := &recordingPort{}
	docker := guestadapter.Docker{
		Facts: target.Facts{SystemdSupported: true, SystemdActive: false},
		Port:  port,
	}
	err := docker.Apply(context.Background(), dockerAction())
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) {
		t.Fatalf("Apply() error = %v, want action-required", err)
	}
	if len(port.commands) != 0 {
		t.Fatalf("commands executed before systemd preflight: %+v", port.commands)
	}
}

func TestAgentPublishesNoAutoUpdateLauncherWithoutAuth(t *testing.T) {
	home := t.TempDir()
	action := planning.Action{
		ID:          "lima-guest:mds/claude-code",
		ComponentID: "claude-code",
		Version:     "2.1.212",
		Verification: [][]string{
			{"claude", "--version"},
		},
	}
	agent := guestadapter.Agent{
		Home: home,
		Delegate: readyComponent{
			observation: adapters.Observation{
				State: adapters.StateReady, InstalledVersion: action.Version,
			},
		},
	}
	if err := agent.Apply(context.Background(), action); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	path := filepath.Join(home, ".local", "bin", "claude")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read launcher: %v", err)
	}
	for _, expected := range []string{
		"DISABLE_AUTOUPDATER=1",
		"OPENCODE_DISABLE_AUTOUPDATE=1",
		filepath.Join(home, ".local", "share", "bun", "bin", "claude"),
	} {
		if !strings.Contains(string(content), expected) {
			t.Fatalf("launcher does not contain %q:\n%s", expected, content)
		}
	}
	for _, forbidden := range []string{" auth ", " login ", "TOKEN=", "KEY="} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("launcher contains forbidden authentication material %q", forbidden)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat launcher: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("launcher mode = %o, want 700", info.Mode().Perm())
	}
}

func TestAgentRefusesExistingLauncher(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".local", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create launcher directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho user-owned\n"), 0o700); err != nil {
		t.Fatalf("write user launcher: %v", err)
	}
	agent := guestadapter.Agent{Home: home, Delegate: readyComponent{}}
	observation, err := agent.Observe(context.Background(), planning.Action{
		ComponentID: "codex",
		Verification: [][]string{
			{"codex", "--version"},
		},
	})
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateConflict ||
		!strings.Contains(observation.Detail, "user-owned") {
		t.Fatalf("observation = %+v, want user-owned conflict", observation)
	}
}

func TestAgentRejectsSymlinkedManagedLauncher(t *testing.T) {
	home := t.TempDir()
	action := planning.Action{
		ComponentID: "codex",
		Verification: [][]string{
			{"codex", "--version"},
		},
	}
	agent := guestadapter.Agent{Home: home, Delegate: readyComponent{}}
	if err := agent.Apply(context.Background(), action); err != nil {
		t.Fatalf("Apply(create launcher): %v", err)
	}

	path := filepath.Join(home, ".local", "bin", "codex")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read launcher: %v", err)
	}
	target := filepath.Join(home, "user-owned-target")
	if err := os.WriteFile(target, content, 0o700); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove launcher: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("create launcher symlink: %v", err)
	}

	observation, err := agent.Observe(context.Background(), action)
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateConflict {
		t.Fatalf("observation = %+v, want symlink conflict", observation)
	}
	if err := agent.Apply(context.Background(), action); err == nil {
		t.Fatal("Apply(symlink) error = nil, want no-overwrite conflict")
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat launcher: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("launcher mode = %v, want preserved symlink", info.Mode())
	}
}

func TestDockerRejectsExternalHostSocket(t *testing.T) {
	docker := guestadapter.Docker{
		Delegate: readyComponent{},
		Getenv: func(key string) string {
			if key == "DOCKER_HOST" {
				return "tcp://host.docker.internal:2375"
			}
			return ""
		},
	}
	observation, err := docker.Observe(context.Background(), dockerAction())
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateConflict ||
		!strings.Contains(observation.Detail, "guest-local") {
		t.Fatalf("observation = %+v, want guest-local conflict", observation)
	}

	docker.Getenv = func(key string) string {
		if key == "DOCKER_HOST" {
			return "unix:///var/run/docker.sock"
		}
		return ""
	}
	observation, err = docker.Observe(context.Background(), dockerAction())
	if err != nil {
		t.Fatalf("Observe(guest-local): %v", err)
	}
	if observation.State != adapters.StateReady {
		t.Fatalf("guest-local observation = %+v, want ready", observation)
	}
}

func TestDockerPinsDaemonCommandsDespiteRemoteCurrentContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_HOST", "")
	configDirectory := filepath.Join(home, ".docker")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatalf("create Docker config directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(configDirectory, "config.json"),
		[]byte(`{"currentContext":"review-remote"}`+"\n"),
		0o600,
	); err != nil {
		t.Fatalf("write remote Docker context config: %v", err)
	}

	const localEndpoint = "unix:///var/run/docker.sock"
	port := &recordingPort{
		err: func(command transport.Command) error {
			if command.Executable != "docker" {
				return nil
			}
			if len(command.Arguments) < 3 ||
				command.Arguments[0] != "--host" ||
				command.Arguments[1] != localEndpoint {
				return errors.New("Docker command could reach configured remote context")
			}
			return nil
		},
		result: func(command transport.Command) transport.Result {
			if command.Executable == "docker" {
				return transport.Result{Stdout: "Docker version test\n"}
			}
			return transport.Result{}
		},
	}
	delegate := packages.Adapter{
		Home: home,
		Port: port,
		Environment: catalog.Environment{
			Catalog: catalog.Catalog{Components: []catalog.Component{
				{
					ID:   "docker-engine",
					Kind: "platform",
					VersionPolicy: catalog.VersionPolicy{
						Mode: "manager-owned",
					},
				},
			}},
		},
	}
	docker := guestadapter.Docker{Port: port, Delegate: delegate}

	observation, err := docker.Observe(context.Background(), dockerAction())
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateReady {
		t.Fatalf("observation = %+v, want ready", observation)
	}
	if err := docker.Verify(context.Background(), dockerAction()); err != nil {
		t.Fatalf("Verify(): %v", err)
	}

	var dockerCommands []transport.Command
	for _, command := range port.commands {
		if command.Executable == "docker" {
			dockerCommands = append(dockerCommands, command)
		}
	}
	if len(dockerCommands) != 4 {
		t.Fatalf("Docker commands = %d, want observe + three verify commands", len(dockerCommands))
	}
	for _, command := range dockerCommands {
		if len(command.Arguments) < 3 ||
			command.Arguments[0] != "--host" ||
			command.Arguments[1] != localEndpoint {
			t.Fatalf("Docker command is not pinned guest-local: %+v", command)
		}
	}
	joined := recordedArgv(dockerCommands)
	for _, expected := range []string{
		"docker --host " + localEndpoint + " version",
		"docker --host " + localEndpoint + " info --format {{.ServerVersion}}",
		"docker --host " + localEndpoint + " compose version",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("commands do not contain %q:\n%s", expected, joined)
		}
	}
	for _, forbidden := range []string{
		"review-remote",
		"docker login",
		"docker --host " + localEndpoint + " run ",
		" auth ",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("Docker commands contain forbidden %q:\n%s", forbidden, joined)
		}
	}
}

func TestDockerInstallsGuestEngineAndRequestsShellRestart(t *testing.T) {
	key := []byte("reviewed Docker key fixture")
	sum := sha256.Sum256(key)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(key)
	}))
	defer server.Close()

	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			if command.Executable == "id" {
				return transport.Result{Stdout: "gurumee sudo\n"}
			}
			return transport.Result{}
		},
	}
	docker := guestadapter.Docker{
		Facts: target.Facts{
			OS: "linux", Architecture: "arm64",
			SystemdSupported: true, SystemdActive: true,
		},
		Port:     port,
		Client:   server.Client(),
		KeyURL:   server.URL,
		KeySHA:   hex.EncodeToString(sum[:]),
		Username: func() (string, error) { return "gurumee", nil },
	}
	err := docker.Apply(context.Background(), dockerAction())
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) ||
		!strings.Contains(actionRequired.Reason, "root-equivalent daemon access") ||
		!strings.Contains(actionRequired.Reason, "sudo usermod -aG docker gurumee") {
		t.Fatalf("Apply() error = %v, want explicit privileged action", err)
	}
	joined := recordedArgv(port.commands)
	for _, expected := range []string{
		"apt-get update",
		"apt-get install -y --no-install-recommends docker-ce docker-ce-cli containerd.io docker-compose-plugin",
		"systemctl enable --now docker",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("commands do not contain %q:\n%s", expected, joined)
		}
	}
	for _, forbidden := range []string{"docker login", "usermod -aG docker"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("Docker installation attempted forbidden %q:\n%s", forbidden, joined)
		}
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

func assertPinnedIDEPluginGraph(t *testing.T, root string) {
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

type countingComponent struct {
	observation adapters.Observation
	applyCalls  int
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

func (*countingComponent) Verify(context.Context, planning.Action) error { return nil }

func dockerAction() planning.Action {
	return planning.Action{
		ID:          "lima-guest:mds/docker-engine",
		ComponentID: "docker-engine",
		Installer:   "docker-apt",
		Package:     "docker-ce docker-ce-cli containerd.io docker-compose-plugin",
		Version:     "manager-owned",
		Verification: [][]string{
			{"docker", "version"},
			{"docker", "info", "--format", "{{.ServerVersion}}"},
		},
	}
}
