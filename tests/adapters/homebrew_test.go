package adapters_test

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestHomebrewInstallDisablesImplicitUpdates(t *testing.T) {
	command, err := packages.HomebrewInstall(planning.Action{
		ID: "macos-host:local/wezterm", ComponentID: "wezterm", Package: "wezterm",
	})
	if err != nil {
		t.Fatalf("HomebrewInstall(): %v", err)
	}
	if got, want := command.Arguments, []string{"install", "--cask", "wezterm"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %v, want %v", got, want)
	}
	for _, key := range []string{
		"HOMEBREW_NO_AUTO_UPDATE",
		"HOMEBREW_NO_INSTALL_UPGRADE",
	} {
		if command.Environment[key] != "1" {
			t.Fatalf("%s = %q, want 1", key, command.Environment[key])
		}
	}
}

func TestMissingHomebrewIsAnExplicitPrerequisite(t *testing.T) {
	port := &recordingPort{
		err: func(command transport.Command) error {
			if command.Executable == "brew" {
				return exec.ErrNotFound
			}
			return nil
		},
	}
	component := packages.HomebrewPrerequisite{
		Port:     port,
		Delegate: readyComponent{},
	}
	action := planning.Action{
		ID:          "macos-host:local/wezterm",
		ComponentID: "wezterm",
		Installer:   "brew",
		Package:     "wezterm",
	}

	observation, err := component.Observe(context.Background(), action)
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateAbsent ||
		!strings.Contains(observation.Detail, "Homebrew prerequisite") {
		t.Fatalf("observation = %+v, want explicit Homebrew prerequisite", observation)
	}

	err = component.Apply(context.Background(), action)
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) ||
		!strings.Contains(actionRequired.Reason, "install Homebrew") {
		t.Fatalf("Apply() error = %v, want Homebrew action-required", err)
	}
	if len(port.commands) != 2 {
		t.Fatalf("commands = %+v, want only prerequisite probes", port.commands)
	}
	for _, command := range port.commands {
		if command.Executable != "brew" ||
			!reflect.DeepEqual(command.Arguments, []string{"--version"}) {
			t.Fatalf("unexpected prerequisite command: %+v", command)
		}
	}
}
