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
	"runtime"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	guestadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

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
