//go:build windows

package update

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

func TestWriteLockPublishesWithoutDirectorySyncFailureOnWindows(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := filepath.Join(root, "locks")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create lock directory: %v", err)
	}
	path := filepath.Join(directory, "versions.lock.yaml")
	if err := os.WriteFile(path, []byte("schema_version: 1\nversions: {}\n"), 0o600); err != nil {
		t.Fatalf("write original lock: %v", err)
	}
	lock := catalog.VersionLock{
		SchemaVersion: 1,
		Versions: map[string]catalog.LockEntry{
			"tool": {
				Version:    "2.0.0",
				Source:     "test",
				Provenance: "https://example.com/tool/2.0.0",
			},
		},
	}

	if err := writeLock(root, lock); err != nil {
		t.Fatalf("writeLock(): %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read published lock: %v", err)
	}
	var got catalog.VersionLock
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatalf("decode published lock: %v", err)
	}
	entry, ok := got.Versions["tool"]
	if !ok || entry.Version != "2.0.0" {
		t.Fatalf("published lock = %+v, want tool 2.0.0", got)
	}
}
