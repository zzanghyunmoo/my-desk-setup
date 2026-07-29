package integration_test

import (
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestGuestHandoffUsesExactRevisionAndArgv(t *testing.T) {
	if err := target.CheckRevision(
		"v0.1.0",
		"sha256:catalog",
		"v0.1.0",
		"sha256:catalog",
	); err != nil {
		t.Fatalf("matching revision: %v", err)
	}

	command := transport.Command{
		Executable: "mds",
		Arguments: []string{
			"plan",
			"--target",
			"wsl-guest:Ubuntu-26.04",
			"--catalog-revision",
			"sha256:catalog",
		},
	}
	executable, arguments := transport.WSLArgv("Ubuntu-26.04", command)
	if executable != "wsl.exe" || arguments[3] != "mds" {
		t.Fatalf("WSL handoff = %q %v", executable, arguments)
	}
	if arguments[len(arguments)-1] != "sha256:catalog" {
		t.Fatalf("catalog revision missing from handoff: %v", arguments)
	}
}

func TestDockerPreflightStopsWithoutActiveSystemd(t *testing.T) {
	err := guest.DockerPreflight(target.Facts{
		SystemdSupported: true,
		SystemdActive:    false,
	})
	if err == nil {
		t.Fatal("DockerPreflight() succeeded without active systemd")
	}
	if !strings.Contains(err.Error(), "action-required") {
		t.Fatalf("DockerPreflight() error = %v, want action-required", err)
	}
}
