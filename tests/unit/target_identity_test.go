package unit_test

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestTargetIdentityIsStableWhileFingerprintTracksFacts(t *testing.T) {
	id, err := target.NewID(target.KindLimaGuest, "dev")
	if err != nil {
		t.Fatalf("NewID(): %v", err)
	}
	before := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		ImageRevision: "sha256:one", Reachable: true,
	}
	after := before
	after.OSVersion = "26.04.1"
	after.ImageRevision = "sha256:two"

	beforeFingerprint, err := before.Fingerprint()
	if err != nil {
		t.Fatalf("before fingerprint: %v", err)
	}
	afterFingerprint, err := after.Fingerprint()
	if err != nil {
		t.Fatalf("after fingerprint: %v", err)
	}
	if before.ID != after.ID {
		t.Fatalf("stable ID changed: %v != %v", before.ID, after.ID)
	}
	if beforeFingerprint == afterFingerprint {
		t.Fatalf("mutable target facts did not change fingerprint")
	}
}

func TestSelectRequiresExplicitTargetWhenMultipleExist(t *testing.T) {
	firstID, _ := target.NewID(target.KindWSLGuest, "Ubuntu-26.04")
	secondID, _ := target.NewID(target.KindWSLGuest, "work")
	candidates := []target.Facts{{ID: firstID}, {ID: secondID}}

	_, err := target.Select(candidates, "")
	if err == nil || !strings.Contains(err.Error(), "choose one explicitly") {
		t.Fatalf("Select() error = %v, want ambiguity", err)
	}
	selected, err := target.Select(candidates, firstID.String())
	if err != nil {
		t.Fatalf("Select(explicit): %v", err)
	}
	if selected.ID != firstID {
		t.Fatalf("selected ID = %v, want %v", selected.ID, firstID)
	}
}

func TestDiscoverLocalGuestDoesNotConfuseHost(t *testing.T) {
	environment := target.GetenvFunc(func(key string) string {
		if key == "WSL_DISTRO_NAME" {
			return "Ubuntu-26.04"
		}
		return ""
	})
	facts, err := target.DiscoverLocal("linux", "amd64", environment)
	if err != nil {
		t.Fatalf("DiscoverLocal(): %v", err)
	}
	if facts.ID.Kind != target.KindWSLGuest || facts.ID.Name != "Ubuntu-26.04" {
		t.Fatalf("facts ID = %v, want WSL guest", facts.ID)
	}
}

func TestParseHostGuestInventories(t *testing.T) {
	wsl, err := target.ParseWSLDistributions(
		[]byte("U\x00b\x00u\x00n\x00t\x00u\x00-\x002\x006\x00.\x000\x004\x00\r\x00\n\x00"),
	)
	if err != nil {
		t.Fatalf("ParseWSLDistributions(): %v", err)
	}
	if len(wsl) != 1 || wsl[0].ID.String() != "wsl-guest:Ubuntu-26.04" {
		t.Fatalf("WSL facts = %+v", wsl)
	}

	lima, err := target.ParseLimaInstances([]byte(
		`{"name":"work","status":"Stopped","arch":"x86_64","limaVersion":"2.1.1"}
{"name":"dev","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
	))
	if err != nil {
		t.Fatalf("ParseLimaInstances(): %v", err)
	}
	if len(lima) != 2 || lima[0].ID.String() != "lima-guest:dev" || !lima[0].Reachable {
		t.Fatalf("Lima facts = %+v", lima)
	}
}

func TestTransportBuildsArgvWithoutShellJoining(t *testing.T) {
	command := transport.Command{
		Executable: "printf",
		Arguments:  []string{"%s", "hello; rm -rf /"},
	}
	executable, arguments := transport.WSLArgv("Ubuntu-26.04", command)
	if executable != "wsl.exe" {
		t.Fatalf("executable = %q, want wsl.exe", executable)
	}
	if got, want := arguments[len(arguments)-1], "hello; rm -rf /"; got != want {
		t.Fatalf("last argument = %q, want exact %q", got, want)
	}
	if strings.Contains(strings.Join(arguments[:len(arguments)-1], " "), "rm -rf") {
		t.Fatalf("argument content leaked into transport prefix: %v", arguments)
	}
}

func TestUbuntuGuestSpecAndProvisionPlan(t *testing.T) {
	spec, err := guest.LoadSpec(
		filepath.Join(repositoryRoot(t), "catalog", "targets", "ubuntu-26.04.yaml"),
	)
	if err != nil {
		t.Fatalf("LoadSpec(): %v", err)
	}
	if spec.Release != "26.04" || !spec.SystemdRequired {
		t.Fatalf("spec = %+v, want Ubuntu 26.04 with systemd", spec)
	}

	steps, err := guest.Plan(target.KindMacOSHost, "mds", "arm64", spec)
	if err != nil {
		t.Fatalf("Plan(macOS): %v", err)
	}
	if len(steps) != 3 || steps[1].Executable != "limactl" {
		t.Fatalf("macOS steps = %+v, want Lima create plan", steps)
	}
	if !containsArgumentSuffix(steps[1].Arguments, "sha256:"+spec.Images["arm64"].SHA256) {
		t.Fatalf("Lima plan does not pin image digest: %+v", steps[1])
	}

	windowsSteps, err := guest.Plan(target.KindWindowsHost, "mds", "amd64", spec)
	if err != nil {
		t.Fatalf("Plan(Windows): %v", err)
	}
	last := windowsSteps[len(windowsSteps)-1]
	if last.Status != guest.StepActionRequired {
		t.Fatalf("last Windows step = %+v, want action-required", last)
	}
}

func TestRevisionMismatchIsTyped(t *testing.T) {
	err := target.CheckRevision("cli-one", "catalog", "cli-two", "catalog")
	var mismatch *target.RevisionMismatchError
	if !errors.As(err, &mismatch) || mismatch.Field != "cli" {
		t.Fatalf("CheckRevision() error = %v, want typed CLI mismatch", err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func containsArgumentSuffix(arguments []string, wanted string) bool {
	for _, argument := range arguments {
		if strings.HasSuffix(argument, wanted) {
			return true
		}
	}
	return false
}
