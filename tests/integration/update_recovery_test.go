package integration_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	updateflow "github.com/zzanghyunmoo/my-desk-setup/internal/update"
)

func TestExactUpdateMovesLockAndTargetTogether(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	environment, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		SystemdSupported: true, SystemdActive: true, Reachable: true,
		CLIRevision: "dev",
	}
	plan, _, err := updateflow.Build(
		environment,
		facts,
		updateflow.Candidate{
			ComponentID: "typescript", Version: "6.0.3",
			Source:     "npm registry",
			Provenance: "https://www.npmjs.com/package/typescript/v/6.0.3",
		},
	)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	stateRoot := filepath.Join(t.TempDir(), "state")
	result, err := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		stateRoot,
		testRunner(newFakeAdapter()),
	)
	if err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if !result.Receipt.Complete ||
		result.CatalogRevision != plan.AfterCatalogRevision {
		t.Fatalf("result = %+v", result)
	}
	updated, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(updated): %v", err)
	}
	if got := updated.Lock.Versions["typescript"].Version; got != "6.0.3" {
		t.Fatalf("TypeScript lock = %s, want 6.0.3", got)
	}
}

func TestStaleUpdateDigestMutatesNeitherLockNorState(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	environment, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		SystemdSupported: true, SystemdActive: true, Reachable: true,
		CLIRevision: "dev",
	}
	plan, _, err := updateflow.Build(
		environment,
		facts,
		updateflow.Candidate{
			ComponentID: "typescript", Version: "6.0.3",
			Source:     "npm registry",
			Provenance: "https://www.npmjs.com/package/typescript/v/6.0.3",
		},
	)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	stateRoot := filepath.Join(t.TempDir(), "state")
	if _, err := updateflow.Apply(
		context.Background(),
		plan,
		"sha256:not-reviewed",
		catalogRoot,
		stateRoot,
		testRunner(newFakeAdapter()),
	); err == nil {
		t.Fatal("Apply() accepted stale update digest")
	}
	unchanged, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(unchanged): %v", err)
	}
	if got := unchanged.Lock.Versions["typescript"].Version; got !=
		environment.Lock.Versions["typescript"].Version {
		t.Fatalf("TypeScript lock changed to %s after stale digest", got)
	}
	if _, err := os.Stat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("state root exists after stale update: %v", err)
	}
}

func TestUpdateRejectsSymlinkedLockDirectory(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	environment, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		SystemdSupported: true, SystemdActive: true, Reachable: true,
		CLIRevision: "dev",
	}
	plan, _, err := updateflow.Build(
		environment,
		facts,
		updateflow.Candidate{
			ComponentID: "typescript", Version: "6.0.3",
			Source:     "npm registry",
			Provenance: "https://www.npmjs.com/package/typescript/v/6.0.3",
		},
	)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	outside := t.TempDir()
	lockContent, err := os.ReadFile(filepath.Join(catalogRoot, "locks", "versions.lock.yaml"))
	if err != nil {
		t.Fatalf("read original lock: %v", err)
	}
	outsideLock := filepath.Join(outside, "versions.lock.yaml")
	if err := os.WriteFile(outsideLock, lockContent, 0o600); err != nil {
		t.Fatalf("write outside lock: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(catalogRoot, "locks")); err != nil {
		t.Fatalf("remove copied locks directory: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(catalogRoot, "locks")); err != nil {
		t.Fatalf("symlink locks directory: %v", err)
	}
	_, err = updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		filepath.Join(t.TempDir(), "state"),
		testRunner(newFakeAdapter()),
	)
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("Apply() error = %v, want symlinked directory rejection", err)
	}
	after, err := os.ReadFile(outsideLock)
	if err != nil {
		t.Fatalf("read outside lock after rejection: %v", err)
	}
	if string(after) != string(lockContent) {
		t.Fatal("outside lock changed through symlinked catalog directory")
	}
}

func copyEmbeddedCatalog(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	err := fs.WalkDir(catalogdata.FS, ".", func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		destination := filepath.Join(root, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		content, err := fs.ReadFile(catalogdata.FS, path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o600)
	})
	if err != nil {
		t.Fatalf("copy embedded catalog: %v", err)
	}
	return root
}
