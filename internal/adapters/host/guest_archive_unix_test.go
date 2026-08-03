//go:build !windows

package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGuestBootstrapArchiveRejectsSymlinkWithoutPathLeak(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "private-release-target.tar.gz")
	linkPath := filepath.Join(root, "private-release-link.tar.gz")
	content := []byte("reviewed archive")
	if err := os.WriteFile(targetPath, content, 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("Symlink(): %v", err)
	}

	_, err := loadGuestBootstrapArchive(linkPath, archiveDigest(content))
	if err == nil {
		t.Fatal("loadGuestBootstrapArchive() accepted symlink")
	}
	if strings.Contains(err.Error(), root) ||
		strings.Contains(err.Error(), filepath.Base(linkPath)) {
		t.Fatalf("error leaks local path: %q", err)
	}
}
