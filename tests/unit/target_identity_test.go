package unit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

func TestTargetFingerprintExcludesVolatileReadinessFacts(t *testing.T) {
	id, _ := target.NewID(target.KindLimaGuest, "dev")
	before := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		ImageRevision: "sha256:image", ImageProvenance: "https://example.invalid/image",
		Reachable: true, SystemdSupported: true, SystemdActive: true,
	}
	after := before
	after.Reachable = false
	after.SystemdSupported = false
	after.SystemdActive = false

	beforeFingerprint, err := before.Fingerprint()
	if err != nil {
		t.Fatalf("before fingerprint: %v", err)
	}
	afterFingerprint, err := after.Fingerprint()
	if err != nil {
		t.Fatalf("after fingerprint: %v", err)
	}
	if beforeFingerprint != afterFingerprint {
		t.Fatalf("volatile readiness changed stable fingerprint: %s != %s", beforeFingerprint, afterFingerprint)
	}
	if err := after.ApplyPreflight(); err == nil ||
		!strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("ApplyPreflight() error = %v, want reachability failure", err)
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
		switch key {
		case "WSL_DISTRO_NAME":
			return "Ubuntu-26.04"
		case "MDS_IMAGE_REVISION":
			return "sha256:reviewed"
		case "MDS_IMAGE_PROVENANCE":
			return "https://example.invalid/ubuntu.wsl"
		case "MDS_IMAGE_CREATION_NONCE_COMMITMENT":
			commitment, _ := target.GuestCreationNonceCommitment(
				strings.Repeat("b", 64),
			)
			return commitment
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
	if facts.ImageRevision != "sha256:reviewed" ||
		facts.ImageProvenance != "https://example.invalid/ubuntu.wsl" ||
		facts.ImageCreationNonceCommitment == "" {
		t.Fatalf("image identity = %+v, want host-verified provenance and nonce", facts)
	}
}

func TestGuestCreationNonceCommitmentUsesDomainSeparatedDecodedBytes(t *testing.T) {
	const nonce = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitment, err := target.GuestCreationNonceCommitment(nonce)
	if err != nil {
		t.Fatalf("GuestCreationNonceCommitment(): %v", err)
	}
	const expected = "sha256:af9f154751dbcb69c5da74f3286c025e7d52cd256540b93bdb10116d33da5157"
	if commitment != expected {
		t.Fatalf("commitment = %q, want %q", commitment, expected)
	}
	if err := target.ValidateGuestCreationNonceCommitment(commitment); err != nil {
		t.Fatalf("ValidateGuestCreationNonceCommitment(): %v", err)
	}

	for _, invalid := range []string{
		"",
		"short",
		strings.ToUpper(nonce),
		strings.Repeat("g", 64),
	} {
		if _, err := target.GuestCreationNonceCommitment(invalid); err == nil {
			t.Fatalf("GuestCreationNonceCommitment(%q) succeeded", invalid)
		}
	}
	for _, invalid := range []string{
		strings.TrimPrefix(commitment, "sha256:"),
		"sha256:" + strings.Repeat("A", 64),
		"sha256:" + strings.Repeat("g", 64),
	} {
		if err := target.ValidateGuestCreationNonceCommitment(invalid); err == nil {
			t.Fatalf("ValidateGuestCreationNonceCommitment(%q) succeeded", invalid)
		}
	}
}

func TestParseGuestImageIdentityRequiresExactPinnedIdentity(t *testing.T) {
	digest := strings.Repeat("a", 64)
	nonce := strings.Repeat("b", 64)
	identity, err := target.ParseImageIdentity([]byte(
		"schema=mds.guest-image/v2\n" +
			"image_revision=sha256:" + digest + "\n" +
			"image_provenance=https://cloud-images.example/ubuntu.img\n" +
			"creation_nonce=" + nonce + "\n",
	))
	if err != nil {
		t.Fatalf("ParseImageIdentity(): %v", err)
	}
	if identity.Revision != "sha256:"+digest ||
		identity.Provenance != "https://cloud-images.example/ubuntu.img" ||
		identity.CreationNonce != nonce {
		t.Fatalf("image identity = %+v", identity)
	}
	for _, content := range []string{
		"schema=mds.guest-image/v2\nimage_revision=sha256:" + digest +
			"\nimage_provenance=https://user:secret@example.invalid/image\n" +
			"creation_nonce=" + nonce + "\n",
		"schema=mds.guest-image/v2\nimage_revision=sha256:short\n" +
			"image_provenance=https://example.invalid/image\n" +
			"creation_nonce=" + nonce + "\n",
		"schema=mds.guest-image/v2\nimage_revision=sha256:" + digest +
			"\nimage_provenance=https://example.invalid/image?moving=1\n" +
			"creation_nonce=" + nonce + "\n",
		"schema=mds.guest-image/v2\nimage_revision=sha256:" + digest +
			"\nimage_provenance=https://example.invalid/image\n" +
			"creation_nonce=short\n",
	} {
		if _, err := target.ParseImageIdentity([]byte(content)); err == nil {
			t.Fatalf("ParseImageIdentity(%q) succeeded", content)
		}
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

func TestGuestTransportCarriesEnvironmentAndWorkingDirectoryAsArgv(t *testing.T) {
	command := transport.Command{
		Executable:       "tool",
		Arguments:        []string{"--version"},
		Environment:      map[string]string{"Z_KEY": "last", "A_KEY": "first"},
		WorkingDirectory: "/workspace/project",
	}
	_, wsl := transport.WSLArgv("Ubuntu-26.04", command)
	wantWSL := []string{
		"--distribution", "Ubuntu-26.04",
		"--cd", "/workspace/project",
		"--exec", "env",
		"A_KEY=first", "Z_KEY=last",
		"tool", "--version",
	}
	if !slices.Equal(wsl, wantWSL) {
		t.Fatalf("WSL argv = %v, want %v", wsl, wantWSL)
	}
	_, lima := transport.LimaArgv("mds", command)
	wantLima := []string{
		"shell", "--tty=false",
		"--workdir", "/workspace/project",
		"mds", "--", "env",
		"A_KEY=first", "Z_KEY=last",
		"tool", "--version",
	}
	if !slices.Equal(lima, wantLima) {
		t.Fatalf("Lima argv = %v, want %v", lima, wantLima)
	}
}

func TestObserveLocalRequiresUbuntu2604AndDetectsSystemd(t *testing.T) {
	release := filepath.Join(t.TempDir(), "os-release")
	releaseContent := []byte("ID=ubuntu\nVERSION_ID=\"26.04\"\n")
	if err := os.WriteFile(release, releaseContent, 0o600); err != nil {
		t.Fatalf("write os-release: %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	facts, err := target.ObserveLocal(
		context.Background(),
		target.Facts{
			ID: id, OS: "linux", Architecture: "arm64",
			ImageRevision:   "sha256:reviewed-image",
			ImageProvenance: "https://example.invalid/ubuntu-26.04.img",
		},
		targetObservationPort{},
		release,
	)
	if err != nil {
		t.Fatalf("ObserveLocal(): %v", err)
	}
	if facts.OSVersion != "26.04" ||
		facts.RuntimeVersion != "6.17.0-mds" ||
		facts.ImageRevision != "sha256:reviewed-image" ||
		facts.ImageProvenance != "https://example.invalid/ubuntu-26.04.img" ||
		!facts.SystemdSupported ||
		!facts.SystemdActive {
		t.Fatalf(
			"facts = %+v, want observed Ubuntu image/runtime with active systemd",
			facts,
		)
	}

	if err := os.WriteFile(release, []byte(
		"ID=ubuntu\nVERSION_ID=\"24.04\"\n",
	), 0o600); err != nil {
		t.Fatalf("rewrite os-release: %v", err)
	}
	if _, err := target.ObserveLocal(
		context.Background(),
		target.Facts{ID: id, OS: "linux", Architecture: "arm64"},
		targetObservationPort{},
		release,
	); err == nil || !strings.Contains(err.Error(), "requires Ubuntu 26.04") {
		t.Fatalf("ObserveLocal(noncanonical) error = %v", err)
	}
}

type targetObservationPort struct{}

func (targetObservationPort) Run(
	_ context.Context,
	command transport.Command,
) (transport.Result, error) {
	switch {
	case command.Executable == "uname" &&
		slices.Equal(command.Arguments, []string{"-r"}):
		return transport.Result{Stdout: "6.17.0-mds\n"}, nil
	case command.Executable == "systemctl" &&
		slices.Equal(command.Arguments, []string{"--version"}):
		return transport.Result{Stdout: "systemd 259\n"}, nil
	case command.Executable == "systemctl" &&
		slices.Equal(command.Arguments, []string{"is-system-running"}):
		return transport.Result{Stdout: "running\n"}, nil
	default:
		return transport.Result{}, errors.New("unexpected command")
	}
}

func TestUbuntuGuestSpecIsPinned(t *testing.T) {
	spec, err := guest.LoadSpec(
		filepath.Join(repositoryRoot(t), "catalog", "targets", "ubuntu-26.04.yaml"),
	)
	if err != nil {
		t.Fatalf("LoadSpec(): %v", err)
	}
	if spec.Release != "26.04" || !spec.SystemdRequired {
		t.Fatalf("spec = %+v, want Ubuntu 26.04 with systemd", spec)
	}
	for architecture, image := range spec.Images {
		if image.URL == "" || len(image.SHA256) != 64 {
			t.Fatalf("image %s = %+v, want pinned URL and SHA-256", architecture, image)
		}
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
