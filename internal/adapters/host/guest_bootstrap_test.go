//go:build !windows

package host

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuestBootstrapScriptPublishesVerifiedOwnedBinary(t *testing.T) {
	home := t.TempDir()
	archive, digest := guestBootstrapFixture(t)
	fakeBin := guestBootstrapFakeBin(t, archive)

	result, err := runGuestBootstrapScript(home, fakeBin, digest)
	if err != nil {
		t.Fatalf("guest bootstrap failed: %v\n%s", err, result)
	}
	binary, err := os.ReadFile(filepath.Join(home, ".local", "bin", "mds"))
	if err != nil {
		t.Fatalf("read published binary: %v", err)
	}
	if string(binary) != "#!/bin/sh\necho mds\n" {
		t.Fatalf("published binary = %q", binary)
	}
	marker, err := os.ReadFile(
		filepath.Join(home, ".local", "share", "mds", "bootstrap-owner-v1"),
	)
	if err != nil {
		t.Fatalf("read ownership marker: %v", err)
	}
	if !strings.Contains(string(marker), "schema=mds.guest-bootstrap/v1") ||
		!strings.Contains(string(marker), "archive_sha256="+digest) ||
		!strings.Contains(string(marker), "binary_sha256=") {
		t.Fatalf("ownership marker = %q", marker)
	}
}

func TestGuestBootstrapScriptResumesCrashBetweenBinaryAndOwnerMarker(t *testing.T) {
	home := t.TempDir()
	archive, digest := guestBootstrapFixture(t)
	fakeBin := guestBootstrapFakeBin(t, archive)
	t.Setenv("MDS_TEST_CRASH_AFTER_BINARY", "1")

	result, err := runGuestBootstrapScript(home, fakeBin, digest)
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 75 {
		t.Fatalf("crash simulation error = %v output=%s, want exit 75", err, result)
	}
	if _, statErr := os.Stat(filepath.Join(
		home, ".local", "share", "mds", "bootstrap-transaction-v1",
	)); statErr != nil {
		t.Fatalf("transaction marker after simulated crash: %v", statErr)
	}

	t.Setenv("MDS_TEST_CRASH_AFTER_BINARY", "")
	result, err = runGuestBootstrapScript(home, fakeBin, digest)
	if err != nil {
		t.Fatalf("guest bootstrap recovery failed: %v\n%s", err, result)
	}
	if _, statErr := os.Stat(filepath.Join(
		home, ".local", "share", "mds", "bootstrap-transaction-v1",
	)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("transaction marker remains after recovery: %v", statErr)
	}
}

func TestGuestBootstrapScriptRejectsMismatchedOwnerMarker(t *testing.T) {
	home := t.TempDir()
	destination := filepath.Join(home, ".local", "bin", "mds")
	stateDirectory := filepath.Join(home, ".local", "share", "mds")
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatalf("MkdirAll(binary): %v", err)
	}
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatalf("MkdirAll(state): %v", err)
	}
	if err := os.WriteFile(destination, []byte("foreign"), 0o700); err != nil {
		t.Fatalf("WriteFile(binary): %v", err)
	}
	marker := strings.Join([]string{
		"schema=mds.guest-bootstrap/v1",
		"archive_sha256=" + strings.Repeat("a", 64),
		"binary_sha256=" + strings.Repeat("b", 64),
		"",
	}, "\n")
	if err := os.WriteFile(
		filepath.Join(stateDirectory, "bootstrap-owner-v1"),
		[]byte(marker),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(marker): %v", err)
	}
	archive, digest := guestBootstrapFixture(t)
	result, err := runGuestBootstrapScript(
		home,
		guestBootstrapFakeBin(t, archive),
		digest,
	)
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 73 {
		t.Fatalf("guest bootstrap error = %v output=%s, want exit 73", err, result)
	}
	content, readErr := os.ReadFile(destination)
	if readErr != nil || string(content) != "foreign" {
		t.Fatalf("foreign binary changed: content=%q error=%v", content, readErr)
	}
}

func TestGuestBootstrapScriptPreservesUnownedExistingBinary(t *testing.T) {
	home := t.TempDir()
	destination := filepath.Join(home, ".local", "bin", "mds")
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(destination, []byte("user-owned"), 0o700); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	archive, digest := guestBootstrapFixture(t)
	result, err := runGuestBootstrapScript(
		home,
		guestBootstrapFakeBin(t, archive),
		digest,
	)
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 73 {
		t.Fatalf("guest bootstrap error = %v output=%s, want exit 73", err, result)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile(existing): %v", err)
	}
	if string(content) != "user-owned" {
		t.Fatalf("existing binary changed to %q", content)
	}
}

func TestGuestBootstrapScriptRejectsChecksumBeforePublication(t *testing.T) {
	home := t.TempDir()
	archive, _ := guestBootstrapFixture(t)
	result, err := runGuestBootstrapScript(
		home,
		guestBootstrapFakeBin(t, archive),
		strings.Repeat("0", 64),
	)
	if err == nil {
		t.Fatalf("guest bootstrap succeeded with bad checksum: %s", result)
	}
	if _, statErr := os.Stat(
		filepath.Join(home, ".local", "bin", "mds"),
	); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("binary exists after checksum failure: %v", statErr)
	}
}

func TestGuestBootstrapScriptRejectsCredentialedRedirectBeforePublication(t *testing.T) {
	home := t.TempDir()
	archive, digest := guestBootstrapFixture(t)
	fakeBin := guestBootstrapFakeBin(t, archive)
	t.Setenv(
		"MDS_TEST_EFFECTIVE_URL",
		"https://user:password@example.invalid/mds.tar.gz",
	)

	result, err := runGuestBootstrapScript(home, fakeBin, digest)
	if err == nil {
		t.Fatalf("guest bootstrap accepted credentialed redirect: %s", result)
	}
	if _, statErr := os.Stat(
		filepath.Join(home, ".local", "bin", "mds"),
	); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("binary exists after redirect rejection: %v", statErr)
	}
}

func runGuestBootstrapScript(home, path, digest string) (string, error) {
	command := exec.Command(
		"/bin/sh",
		"-eu",
		"-s",
		"--",
		"https://example.invalid/mds.tar.gz",
		digest,
	)
	command.Stdin = bytes.NewReader(guestBootstrapScript)
	command.Env = []string{
		"HOME=" + home,
		"MDS_TEST_ARCHIVE=" + os.Getenv("MDS_TEST_ARCHIVE"),
		"MDS_TEST_EFFECTIVE_URL=" + os.Getenv("MDS_TEST_EFFECTIVE_URL"),
		"MDS_TEST_CRASH_AFTER_BINARY=" + os.Getenv("MDS_TEST_CRASH_AFTER_BINARY"),
		"PATH=" + path + ":/usr/bin:/bin:/opt/homebrew/bin",
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

func guestBootstrapFixture(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mds.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	content := []byte("#!/bin/sh\necho mds\n")
	if err := archive.WriteHeader(&tar.Header{
		Name: "mds",
		Mode: 0o700,
		Size: int64(len(content)),
	}); err != nil {
		t.Fatalf("WriteHeader(): %v", err)
	}
	if _, err := archive.Write(content); err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(fixture): %v", err)
	}
	sum := sha256.Sum256(encoded)
	return path, hex.EncodeToString(sum[:])
}

func guestBootstrapFakeBin(t *testing.T, archive string) string {
	t.Helper()
	directory := t.TempDir()
	curl := `#!/bin/sh
set -eu
destination=
location=
max_redirs=
max_filesize=
https_only=
https_redirects_only=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output)
      shift
      destination=$1
      ;;
    --location)
      location=1
      ;;
    --max-redirs)
      shift
      max_redirs=$1
      ;;
    --max-filesize)
      shift
      max_filesize=$1
      ;;
    --proto)
      shift
      test "$1" = "=https"
      https_only=1
      ;;
    --proto-redir)
      shift
      test "$1" = "=https"
      https_redirects_only=1
      ;;
  esac
  shift
done
test "$location" = 1
test "$max_redirs" = 3
test "$max_filesize" = 536870912
test "$https_only" = 1
test "$https_redirects_only" = 1
/bin/cp "$MDS_TEST_ARCHIVE" "$destination"
printf '%s' "${MDS_TEST_EFFECTIVE_URL:-https://release-assets.githubusercontent.com/mds.tar.gz?signature=test}"
`
	checksum := `#!/bin/sh
set -eu
if [ "$#" -eq 1 ]; then
  if [ -x /usr/bin/sha256sum ]; then
    /usr/bin/sha256sum "$1"
  else
    /usr/bin/shasum -a 256 "$1"
  fi
  exit 0
fi
read expected path
if [ -x /usr/bin/sha256sum ]; then
  printf '%s  %s\n' "$expected" "$path" | /usr/bin/sha256sum -c -
else
  actual=$(/usr/bin/shasum -a 256 "$path")
  actual=${actual%% *}
  test "$actual" = "$expected"
fi
`
	move := `#!/bin/sh
set -eu
last=
for argument in "$@"; do
  last=$argument
done
/bin/mv "$@"
case "$last" in
  */.local/bin/mds)
    if [ "${MDS_TEST_CRASH_AFTER_BINARY:-}" = 1 ]; then
      exit 75
    fi
    ;;
esac
`
	sync := `#!/bin/sh
set -eu
test "$1" = "-f"
test -e "$2"
`
	for name, content := range map[string]string{
		"curl":      curl,
		"mv":        move,
		"sha256sum": checksum,
		"sync":      sync,
	} {
		if err := os.WriteFile(
			filepath.Join(directory, name),
			[]byte(content),
			0o700,
		); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	t.Setenv("MDS_TEST_ARCHIVE", archive)
	return directory
}
