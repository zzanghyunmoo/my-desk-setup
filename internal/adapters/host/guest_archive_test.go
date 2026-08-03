package host

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func TestLoadGuestBootstrapArchiveReturnsVerifiedPrivateSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mds-linux.tar.gz")
	original := []byte("reviewed release archive")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	snapshot, err := loadGuestBootstrapArchive(path, archiveDigest(original))
	if err != nil {
		t.Fatalf("loadGuestBootstrapArchive(): %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("replace path: %v", err)
	}
	if string(snapshot) != string(original) {
		t.Fatalf("snapshot = %q, want original bytes", snapshot)
	}
}

func TestLoadGuestBootstrapArchiveRejectsUnsafeInputsWithoutPathLeak(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "private-release-name.tar.gz")
	if err := os.WriteFile(regular, []byte("wrong bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	directory := filepath.Join(root, "private-release-directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("Mkdir(): %v", err)
	}
	oversize := filepath.Join(root, "private-release-oversize.tar.gz")
	file, err := os.Create(oversize)
	if err != nil {
		t.Fatalf("Create(oversize): %v", err)
	}
	if err := file.Truncate(maxGuestBootstrapArchiveBytes + 1); err != nil {
		t.Fatalf("Truncate(oversize): %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(oversize): %v", err)
	}

	for _, test := range []struct {
		name string
		path string
	}{
		{name: "checksum mismatch", path: regular},
		{name: "non regular", path: directory},
		{name: "oversize", path: oversize},
		{name: "missing", path: filepath.Join(root, "private-release-missing.tar.gz")},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadGuestBootstrapArchive(
				test.path,
				strings.Repeat("a", 64),
			)
			if err == nil {
				t.Fatal("loadGuestBootstrapArchive() succeeded")
			}
			if strings.Contains(err.Error(), root) ||
				strings.Contains(err.Error(), filepath.Base(test.path)) {
				t.Fatalf("error leaks local path: %q", err)
			}
		})
	}
}

func TestGuestBootstrapCommandSeparatesFixedScriptFromLocalArchive(t *testing.T) {
	archive := []byte("exact private archive snapshot")
	artifact := GuestBootstrapArtifact{
		URL:     "https://example.invalid/mds-linux.tar.gz",
		SHA256:  archiveDigest(archive),
		Archive: archive,
	}
	command, err := (GuestRuntime{}).guestBootstrapCommand(
		planning.Action{ComponentID: "lima"},
		artifact,
	)
	if err != nil {
		t.Fatalf("guestBootstrapCommand(): %v", err)
	}
	if !bytes.Equal(command.Stdin, archive) {
		t.Fatalf("stdin = %q, want exact archive snapshot", command.Stdin)
	}
	joined := strings.Join(command.Arguments, " ")
	for _, expected := range []string{
		"mds.guest-bootstrap/v1",
		"mds-bootstrap stdin",
		artifact.URL,
		artifact.SHA256,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("bootstrap argv missing %q: %q", expected, joined)
		}
	}
	if bytes.Contains([]byte(joined), archive) {
		t.Fatal("bootstrap argv contains archive bytes")
	}
}

func TestGuestBootstrapCommandPreservesCanonicalURLMode(t *testing.T) {
	artifact := GuestBootstrapArtifact{
		URL:    "https://example.invalid/mds-linux.tar.gz",
		SHA256: strings.Repeat("a", 64),
	}
	command, err := (GuestRuntime{}).guestBootstrapCommand(
		planning.Action{ComponentID: "lima"},
		artifact,
	)
	if err != nil {
		t.Fatalf("guestBootstrapCommand(): %v", err)
	}
	if len(command.Stdin) != 0 {
		t.Fatalf("URL bootstrap stdin = %q, want empty", command.Stdin)
	}
	joined := strings.Join(command.Arguments, " ")
	if !strings.Contains(joined, "mds-bootstrap url") ||
		!strings.Contains(joined, artifact.URL) ||
		!strings.Contains(joined, artifact.SHA256) {
		t.Fatalf("URL bootstrap argv = %q", joined)
	}
}

func archiveDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
