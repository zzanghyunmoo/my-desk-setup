package adapters_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestAPTUsesNoninteractiveSudoArgv(t *testing.T) {
	commands, err := packages.APTInstall(planning.Action{
		ID:      "wsl-guest:Ubuntu-26.04/base-cli",
		Package: "git curl",
	})
	if err != nil {
		t.Fatalf("APTInstall(): %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %d, want preflight and install", len(commands))
	}
	if got, want := commands[0].Executable, "/usr/bin/sudo"; got != want {
		t.Fatalf("sudo preflight executable = %q, want %q", got, want)
	}
	if got, want := commands[0].Arguments, []string{"-n", "/usr/bin/true"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sudo preflight = %v, want %v", got, want)
	}
	joined := strings.Join(commands[1].Arguments, " ")
	for _, expected := range []string{
		"-n /usr/bin/env",
		"DEBIAN_FRONTEND=noninteractive",
		"/usr/bin/apt-get install -y --no-install-recommends git curl",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("APT arguments %q do not contain %q", joined, expected)
		}
	}
}

func TestPrivilegedCommandAllowlistRejectsShellAndUnreviewedService(t *testing.T) {
	for _, command := range []transport.Command{
		{
			Executable: "/usr/bin/sudo",
			Arguments:  []string{"-n", "sh", "-c", "apt-get install curl"},
		},
		{
			Executable: "/usr/bin/sudo",
			Arguments:  []string{"-n", "/usr/bin/systemctl", "enable", "--now", "ssh"},
		},
		{
			Executable: "/usr/bin/sudo",
			Arguments:  []string{"apt-get", "update"},
		},
	} {
		if err := packages.ValidatePrivilegedCommand(command); err == nil {
			t.Fatalf("ValidatePrivilegedCommand(%+v) succeeded", command)
		}
	}
}

func TestAPTRequiresUserManagedSudoCredentialRefresh(t *testing.T) {
	port := &recordingPort{
		err: func(command transport.Command) error {
			if command.Executable == "/usr/bin/sudo" &&
				reflect.DeepEqual(command.Arguments, []string{"-n", "/usr/bin/true"}) {
				return errors.New("sudo: a password is required")
			}
			return nil
		},
	}
	err := packages.RequireSudo(context.Background(), port)
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) {
		t.Fatalf("RequireSudo() error = %v, want action-required", err)
	}
	for _, expected := range []string{"sudo -v", "does not collect sudo credentials"} {
		if !strings.Contains(actionRequired.Reason, expected) {
			t.Fatalf("action-required reason = %q, want %q", actionRequired.Reason, expected)
		}
	}
}

func TestBunUsesVerifiedLocalArtifact(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "reviewed-codex.tgz")
	command, err := packages.BunInstall(planning.Action{
		ID: "lima-guest:mds/codex", Package: "@openai/codex", Version: "0.144.6",
	}, artifact, map[string]string{"BUN_INSTALL": filepath.Join(t.TempDir(), "bun")})
	if err != nil {
		t.Fatalf("BunInstall(): %v", err)
	}
	if got, want := command.Arguments, []string{
		"add", "--global", artifact,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %v, want %v", got, want)
	}
}

func TestPackageAdapterPublishesStableGuestLauncher(t *testing.T) {
	home := t.TempDir()
	tarball := []byte("reviewed notion CLI tarball")
	tarballSum := sha256.Sum256(tarball)
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = writer.Write(tarball)
	}))
	defer server.Close()
	adapter := packages.Adapter{
		Home: home,
		Port: &recordingPort{},
		Vendor: packages.Vendor{
			Client: server.Client(),
		},
		Environment: catalog.Environment{
			Catalog: catalog.Catalog{Components: []catalog.Component{
				{
					ID: "notion-cli", Kind: "cli",
					VersionPolicy: catalog.VersionPolicy{
						Mode: "pinned", LockKey: "notion-cli",
					},
				},
			}},
			Lock: catalog.VersionLock{Versions: map[string]catalog.LockEntry{
				"notion-cli": {
					Version: "0.21.5",
					NPM: &catalog.NPMArtifact{
						Tarball:   server.URL + "/ntn-0.21.5.tgz",
						Integrity: bunFixtureSRI(tarball),
						SHA256:    hex.EncodeToString(tarballSum[:]),
					},
				},
			}},
		},
	}
	action := planning.Action{
		ID: "lima-guest:mds/notion-cli", ComponentID: "notion-cli",
		Installer: "bun", Package: "ntn", Version: "0.21.5",
		Verification: [][]string{
			{"ntn", "--version"},
		},
	}
	if err := adapter.Apply(context.Background(), action); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	path := filepath.Join(home, ".local", "bin", "ntn")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read launcher: %v", err)
	}
	if !strings.Contains(
		string(content),
		filepath.Join(home, ".local", "share", "bun", "bin", "ntn"),
	) {
		t.Fatalf("launcher = %s", content)
	}
	if err := os.WriteFile(path, []byte("user-owned\n"), 0o700); err != nil {
		t.Fatalf("replace launcher fixture: %v", err)
	}
	if err := adapter.Apply(context.Background(), action); err == nil ||
		!strings.Contains(err.Error(), "user-owned") {
		t.Fatalf("Apply(user-owned) error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove user-owned launcher fixture: %v", err)
	}
	target := filepath.Join(home, "user-owned-target")
	if err := os.WriteFile(target, content, 0o700); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("create launcher symlink: %v", err)
	}
	if err := adapter.Apply(context.Background(), action); err == nil ||
		!strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Apply(symlink) error = %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat launcher symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("launcher mode = %v, want preserved symlink", info.Mode())
	}
}

func TestPinnedMisePackageUsesMissingManagedLauncherAsInstallBoundary(t *testing.T) {
	port := &recordingPort{
		result: func(transport.Command) transport.Result {
			return transport.Result{Stdout: "tool 1.0.0\n"}
		},
	}
	environment := catalog.Environment{
		Catalog: catalog.Catalog{Components: []catalog.Component{
			{
				ID: "base-cli", Kind: "build",
				VersionPolicy: catalog.VersionPolicy{
					Mode: "pinned", LockKey: "tool",
				},
			},
		}},
		Lock: catalog.VersionLock{Versions: map[string]catalog.LockEntry{
			"tool": {Version: "2.0.0"},
		}},
	}
	action := planning.Action{
		ID: "lima-guest:mds/base-cli", ComponentID: "base-cli",
		Installer: "mise", Package: "tool", Version: "2.0.0",
		Verification: [][]string{
			{"git", "--version"},
		},
	}
	normal := packages.Adapter{
		Home: t.TempDir(), Port: port, Environment: environment,
	}
	observation, err := normal.Observe(context.Background(), action)
	if err != nil {
		t.Fatalf("normal Observe(): %v", err)
	}
	if observation.State != adapters.StateAbsent {
		t.Fatalf("normal observation = %+v, want absent managed launcher", observation)
	}
	explicit := normal
	explicit.AllowReplace = true
	observation, err = explicit.Observe(context.Background(), action)
	if err != nil {
		t.Fatalf("explicit Observe(): %v", err)
	}
	if observation.State != adapters.StateAbsent {
		t.Fatalf("explicit observation = %+v, want replaceable absent", observation)
	}
}

func TestVendorInstallVerifiesChecksumAndPublishesAtomically(t *testing.T) {
	archive := zipArtifact(t, "fixture/bin/tool", []byte("#!/bin/sh\necho ok\n"))
	sum := sha256.Sum256(archive)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(archive)
	}))
	defer server.Close()

	home := t.TempDir()
	vendor := packages.Vendor{
		Home: home, Platform: "linux", Arch: "amd64", Client: server.Client(),
	}
	err := vendor.Install(
		context.Background(),
		catalog.Component{ID: "fixture"},
		catalog.LockEntry{
			Artifacts: map[string]catalog.Artifact{
				"linux-amd64": {
					URL: server.URL, SHA256: hex.EncodeToString(sum[:]),
					Format: "zip", Executable: "fixture/bin/tool",
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("Install(): %v", err)
	}
	path := filepath.Join(home, ".local", "bin", "tool")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed tool: %v", err)
	}
	if string(content) != "#!/bin/sh\necho ok\n" {
		t.Fatalf("installed content = %q", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat installed tool: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("installed mode = %o, want 700", info.Mode().Perm())
	}
}

func TestVendorInstallAcceptsChecksumPinnedHTTPSReleaseRedirect(t *testing.T) {
	archive := zipArtifact(t, "tool", []byte("#!/bin/sh\necho ok\n"))
	sum := sha256.Sum256(archive)
	assetServer := httptest.NewTLSServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write(archive)
		},
	))
	defer assetServer.Close()
	redirectServer := httptest.NewTLSServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(
				writer,
				request,
				assetServer.URL+"/asset?signature=temporary",
				http.StatusFound,
			)
		},
	))
	defer redirectServer.Close()

	home := t.TempDir()
	vendor := packages.Vendor{
		Home: home, Platform: "linux", Arch: "amd64",
		Client: redirectServer.Client(),
	}
	err := vendor.Install(
		context.Background(),
		catalog.Component{ID: "fixture"},
		catalog.LockEntry{
			Artifacts: map[string]catalog.Artifact{
				"linux-amd64": {
					URL:    redirectServer.URL + "/start",
					SHA256: hex.EncodeToString(sum[:]),
					Format: "zip", Executable: "tool",
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("Install(): %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "tool")); err != nil {
		t.Fatalf("installed tool: %v", err)
	}
}

func TestVendorRejectsChecksumMismatch(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("not the reviewed artifact"))
	}))
	defer server.Close()
	home := t.TempDir()
	vendor := packages.Vendor{
		Home: home, Platform: "linux", Arch: "amd64", Client: server.Client(),
	}
	err := vendor.Install(
		context.Background(),
		catalog.Component{ID: "fixture"},
		catalog.LockEntry{
			Artifacts: map[string]catalog.Artifact{
				"linux-amd64": {
					URL: server.URL, SHA256: strings.Repeat("0", 64),
					Format: "binary", Executable: "tool",
				},
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Install() error = %v, want checksum mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".local", "bin", "tool")); !os.IsNotExist(statErr) {
		t.Fatalf("tool exists after checksum failure: %v", statErr)
	}
}

func TestVendorExtractsExactTarGzExecutable(t *testing.T) {
	archive := tarGzArtifact(
		t,
		"acli_1.3.22-stable_linux_arm64/acli",
		[]byte("#!/bin/sh\necho acli\n"),
	)
	sum := sha256.Sum256(archive)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(archive)
	}))
	defer server.Close()

	home := t.TempDir()
	vendor := packages.Vendor{
		Home: home, Platform: "linux", Arch: "arm64", Client: server.Client(),
	}
	err := vendor.Install(
		context.Background(),
		catalog.Component{ID: "atlassian-cli"},
		catalog.LockEntry{
			Artifacts: map[string]catalog.Artifact{
				"linux-arm64": {
					URL: server.URL, SHA256: hex.EncodeToString(sum[:]),
					Format: "tar.gz", Executable: "acli_1.3.22-stable_linux_arm64/acli",
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("Install(): %v", err)
	}
	content, err := os.ReadFile(filepath.Join(home, ".local", "bin", "acli"))
	if err != nil {
		t.Fatalf("read installed ACLI: %v", err)
	}
	if string(content) != "#!/bin/sh\necho acli\n" {
		t.Fatalf("installed ACLI content = %q", content)
	}
}

func zipArtifact(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create(name)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buffer.Bytes()
}

func tarGzArtifact(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	if err := writer.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}
