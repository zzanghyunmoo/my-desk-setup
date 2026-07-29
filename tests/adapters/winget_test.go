package adapters_test

import (
	"slices"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func TestWinGetUsesExactIDAndVersionWithoutInteraction(t *testing.T) {
	command, err := packages.WinGetInstall(planning.Action{
		ID: "windows-host:local/example", Package: "Vendor.Example", Version: "1.2.3",
	})
	if err != nil {
		t.Fatalf("WinGetInstall(): %v", err)
	}
	for _, expected := range []string{
		"--id", "Vendor.Example", "--exact", "--version", "1.2.3",
		"--disable-interactivity", "--accept-package-agreements", "--accept-source-agreements",
	} {
		if !slices.Contains(command.Arguments, expected) {
			t.Fatalf("arguments %v do not contain %q", command.Arguments, expected)
		}
	}
}
