package adapters_test

import (
	"reflect"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
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
