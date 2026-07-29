package adapters_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	guestadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/guest"
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
			if command.Executable == "git" &&
				len(command.Arguments) >= 3 &&
				command.Arguments[2] == "rev-parse" {
				return transport.Result{Stdout: nvchadAction().Version + "\n"}
			}
			return transport.Result{}
		},
	}
	editor := guestadapter.Editor{
		Home: home,
		Port: port,
		Delegate: readyComponent{
			observation: adapters.Observation{
				State: adapters.StateReady, InstalledVersion: nvchadAction().Version,
			},
		},
		Now: func() time.Time {
			return time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
		},
	}
	if err := editor.Apply(context.Background(), nvchadAction()); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	observation, err := editor.Observe(context.Background(), nvchadAction())
	if err != nil {
		t.Fatalf("Observe(after apply): %v", err)
	}
	if observation.State != adapters.StateReady ||
		observation.InstalledVersion != nvchadAction().Version {
		t.Fatalf("observation = %+v, want exact managed revision", observation)
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
	if info.Mode().Perm() != 0o700 {
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
}

func TestDockerInstallsGuestEngineAndRequestsShellRestart(t *testing.T) {
	key := []byte("reviewed Docker key fixture")
	sum := sha256.Sum256(key)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
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
		!strings.Contains(actionRequired.Reason, "restart the guest shell") {
		t.Fatalf("Apply() error = %v, want shell restart action", err)
	}
	joined := recordedArgv(port.commands)
	for _, expected := range []string{
		"apt-get update",
		"apt-get install -y --no-install-recommends docker-ce docker-ce-cli containerd.io docker-compose-plugin",
		"systemctl enable --now docker",
		"usermod -aG docker gurumee",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("commands do not contain %q:\n%s", expected, joined)
		}
	}
	if strings.Contains(joined, "docker login") {
		t.Fatalf("Docker installation attempted authentication:\n%s", joined)
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

func dockerAction() planning.Action {
	return planning.Action{
		ID:          "lima-guest:mds/docker-engine",
		ComponentID: "docker-engine",
		Installer:   "docker-apt",
		Package:     "docker-ce docker-ce-cli containerd.io docker-compose-plugin",
		Version:     "manager-owned",
		Verification: [][]string{
			{"docker", "version"},
			{"docker", "run", "--rm", "hello-world"},
		},
	}
}

type recordingPort struct {
	commands []transport.Command
	result   func(transport.Command) transport.Result
	err      func(transport.Command) error
}

func (port *recordingPort) Run(
	_ context.Context,
	command transport.Command,
) (transport.Result, error) {
	port.commands = append(port.commands, command)
	if port.err != nil {
		if err := port.err(command); err != nil {
			return transport.Result{}, err
		}
	}
	if port.result != nil {
		return port.result(command), nil
	}
	return transport.Result{}, nil
}

type readyComponent struct {
	observation adapters.Observation
}

func (component readyComponent) Observe(
	context.Context,
	planning.Action,
) (adapters.Observation, error) {
	if component.observation.State == "" {
		return adapters.Observation{State: adapters.StateReady}, nil
	}
	return component.observation, nil
}

func (readyComponent) Apply(context.Context, planning.Action) error { return nil }
func (readyComponent) Verify(context.Context, planning.Action) error {
	return nil
}

func recordedArgv(commands []transport.Command) string {
	var lines []string
	for _, command := range commands {
		lines = append(lines, strings.Join(append(
			[]string{command.Executable},
			command.Arguments...,
		), " "))
	}
	return strings.Join(lines, "\n")
}
