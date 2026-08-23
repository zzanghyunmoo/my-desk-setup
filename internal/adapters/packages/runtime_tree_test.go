package packages

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

func TestRuntimeTreePublishesObservesAndRepairsWholeTree(t *testing.T) {
	manager, component, lock := runtimeTreeFixture(t, false)
	ctx := context.Background()
	if err := manager.Apply(ctx, component, lock); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	observation, launcher, err := manager.Observe(component, lock)
	if err != nil || observation.State != adapters.StateReady || !filepath.IsAbs(launcher) {
		t.Fatalf("Observe() = %+v, %q, %v", observation, launcher, err)
	}
	currentBefore, err := os.ReadFile(filepath.Join(manager.destination(component, lock), runtimeTreeCurrentFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(ctx, component, lock); err != nil {
		t.Fatalf("Apply(repeat): %v", err)
	}
	currentAfter, _ := os.ReadFile(filepath.Join(manager.destination(component, lock), runtimeTreeCurrentFile))
	if !bytes.Equal(currentBefore, currentAfter) {
		t.Fatalf("repeat apply replaced ready generation: %q -> %q", currentBefore, currentAfter)
	}

	if err := os.Chmod(launcher, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("drift"), 0o700); err != nil {
		t.Fatal(err)
	}
	observation, _, err = manager.Observe(component, lock)
	if err != nil || observation.State != adapters.StateAbsent {
		t.Fatalf("Observe(drift) = %+v, %v", observation, err)
	}
	if err := manager.Apply(ctx, component, lock); err != nil {
		t.Fatalf("Apply(repair): %v", err)
	}
	observation, launcher, err = manager.Observe(component, lock)
	if err != nil || observation.State != adapters.StateReady {
		t.Fatalf("Observe(repaired) = %+v, %v", observation, err)
	}
	if content, err := os.ReadFile(launcher); err != nil || string(content) != "#!/bin/sh\necho exact\n" {
		t.Fatalf("repaired launcher = %q, %v", content, err)
	}
	if count := runtimeTreeGenerationCount(t, manager.destination(component, lock)); count != 1 {
		t.Fatalf("generation count after repair = %d, want 1", count)
	}
}

func TestRuntimeTreeRejectsIdentityMismatchAndUserOwnedDestination(t *testing.T) {
	manager, component, lock := runtimeTreeFixture(t, false)
	bad := lock
	entry := bad.Artifacts["linux-arm64"]
	tree := *entry.Tree
	tree.ManifestSHA256 = strings.Repeat("0", 64)
	entry.Tree = &tree
	bad.Artifacts = map[string]catalog.Artifact{"linux-arm64": entry}
	if err := manager.Apply(context.Background(), component, bad); err == nil ||
		!strings.Contains(err.Error(), "manifest digest mismatch") {
		t.Fatalf("Apply(manifest mismatch) = %v", err)
	}
	if _, err := os.Lstat(manager.destination(component, bad)); !os.IsNotExist(err) {
		t.Fatalf("identity mismatch published a destination: %v", err)
	}

	destination := manager.destination(component, lock)
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(destination, "user-owned")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	observation, _, err := manager.Observe(component, lock)
	if err != nil || observation.State != adapters.StateConflict {
		t.Fatalf("Observe(user-owned) = %+v, %v", observation, err)
	}
	if err := manager.Apply(context.Background(), component, lock); err == nil {
		t.Fatal("Apply() adopted an unmarked destination")
	}
	if content, _ := os.ReadFile(sentinel); string(content) != "keep" {
		t.Fatalf("user content changed: %q", content)
	}
}

func TestRuntimeTreeReusesSnapshotSafetyAndPublishesReadOnlyCache(t *testing.T) {
	manager, component, lock := runtimeTreeFixture(t, true)
	unsafe := zipTree(t, map[string][]byte{
		"package/bin/tool": []byte("#!/bin/sh\necho exact\n"),
		"../sentinel":      []byte("overwrite"),
	})
	manager.Snapshotter.Open = func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(unsafe)), nil
	}
	entry := lock.Artifacts["linux-arm64"]
	entry.SHA256 = digestBytes(unsafe)
	lock.Artifacts = map[string]catalog.Artifact{"linux-arm64": entry}
	sentinel := filepath.Join(manager.Home, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), component, lock); err == nil ||
		!strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("Apply(unsafe) = %v", err)
	}
	if content, _ := os.ReadFile(sentinel); string(content) != "keep" {
		t.Fatalf("external sentinel changed: %q", content)
	}

	manager, component, lock = runtimeTreeFixture(t, true)
	if err := manager.Apply(context.Background(), component, lock); err != nil {
		t.Fatalf("Apply(cache): %v", err)
	}
	observation, launcher, err := manager.Observe(component, lock)
	if err != nil || observation.State != adapters.StateReady {
		t.Fatalf("Observe(cache) = %+v, %v", observation, err)
	}
	if info, err := os.Stat(launcher); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("cache launcher mode = %v, %v; want read-only executable", info, err)
	}
}

func TestRuntimeTreeResourcePublishesNonExecutableAnchor(t *testing.T) {
	manager, component, lock := runtimeTreeFixture(t, false)
	entry := lock.Artifacts["linux-arm64"]
	tree := *entry.Tree
	tree.Usage = "resource"
	entry.Tree = &tree
	lock.Artifacts = map[string]catalog.Artifact{"linux-arm64": entry}

	if err := manager.Apply(context.Background(), component, lock); err != nil {
		t.Fatalf("Apply(resource): %v", err)
	}
	observation, anchor, err := manager.Observe(component, lock)
	if err != nil || observation.State != adapters.StateReady {
		t.Fatalf("Observe(resource) = %+v, %v", observation, err)
	}
	info, err := os.Stat(anchor)
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("resource anchor mode = %v, %v; want 0444", info, err)
	}
}

func TestRuntimeTreePreservesOnlyReviewedHelperExecutables(t *testing.T) {
	manager, component, lock := runtimeTreeFixture(t, false)
	entry := lock.Artifacts["linux-arm64"]
	tree := *entry.Tree
	tree.ExecutablePaths = []string{"package/bin/helper", "package/bin/tool"}
	tree.RequiredPaths = []string{"package/bin/helper", "package/bin/tool", "package/lib/data"}
	entry.Tree = &tree
	lock.Artifacts = map[string]catalog.Artifact{"linux-arm64": entry}

	if err := manager.Apply(context.Background(), component, lock); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	_, launcher, err := manager.Observe(component, lock)
	if err != nil {
		t.Fatal(err)
	}
	root, err := runtimeTreePayloadRoot(launcher, entry.Executable)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range tree.ExecutablePaths {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil || info.Mode().Perm()&0o222 != 0 ||
			(runtime.GOOS != "windows" && info.Mode().Perm() != 0o555) {
			t.Fatalf("executable %s mode = %v, %v; want read-only executable", relative, info, err)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Chmod(filepath.Join(root, "package/lib/data"), 0o555); err != nil {
		t.Fatal(err)
	}
	observation, _, err := manager.Observe(component, lock)
	if err != nil || observation.State != adapters.StateAbsent ||
		!strings.Contains(observation.Detail, "executable mode differs") {
		t.Fatalf("Observe(unreviewed executable) = %+v, %v", observation, err)
	}
	if err := os.Chmod(filepath.Join(root, "package/lib/data"), 0o444); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "package/lib/data"))
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("data mode = %v, %v; want 0444", info, err)
	}
}

func TestRuntimeTreeActivationFailureDoesNotActivatePartialTree(t *testing.T) {
	manager, component, lock := runtimeTreeFixture(t, false)
	if err := manager.Apply(context.Background(), component, lock); err != nil {
		t.Fatal(err)
	}
	manager.BeforeActivate = func() error { return errors.New("interrupted") }
	// Force a repair while the previous generation remains addressable.
	current := filepath.Join(manager.destination(component, lock), runtimeTreeCurrentFile)
	if err := os.Chmod(current, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("missing-generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), component, lock); err == nil ||
		!strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("Apply(interrupted) = %v", err)
	}
	// No partially staged generation became authoritative.
	content, err := os.ReadFile(current)
	if err != nil || string(content) != "missing-generation\n" {
		t.Fatalf("current pointer changed on interrupted activation: %q, %v", content, err)
	}
	if count := runtimeTreeGenerationCount(t, manager.destination(component, lock)); count != 1 {
		t.Fatalf("generation count after interrupted activation = %d, want 1", count)
	}
}

func runtimeTreeGenerationCount(t *testing.T, destination string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(destination, "generations"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && validRuntimeTreeGeneration(entry.Name()) {
			count++
		}
	}
	return count
}

func runtimeTreeFixture(t *testing.T, cache bool) (RuntimeTreeManager, catalog.Component, catalog.LockEntry) {
	t.Helper()
	launcher := []byte("#!/bin/sh\necho exact\n")
	files := map[string][]byte{
		"package/bin/tool":   launcher,
		"package/bin/helper": []byte("#!/bin/sh\necho helper\n"),
		"package/lib/data":   []byte("sibling\n"),
	}
	archive := zipTree(t, files)
	manifestRoot := t.TempDir()
	for name, content := range files {
		path := filepath.Join(manifestRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, manifestBytes, manifestSHA, err := artifact.BuildTreeManifest(manifestRoot)
	if err != nil {
		t.Fatal(err)
	}
	artifactValue := catalog.Artifact{
		URL: "fixture://tree.zip", SHA256: digestBytes(archive), Format: "zip",
		Executable: "package/bin/tool",
		Tree: &catalog.RuntimeTreeIdentity{
			ManifestSHA256: manifestSHA, LauncherSHA256: digestBytes(launcher),
			RequiredPaths: []string{"package/bin/tool", "package/lib/data"},
		},
	}
	lock := catalog.LockEntry{Version: "1.0.0", Artifacts: map[string]catalog.Artifact{
		"linux-arm64": artifactValue,
	}}
	if cache {
		lock.FixtureCache = &catalog.FixtureCacheIdentity{
			DependencyGraphSHA256:  strings.Repeat("a", 64),
			ReadOnlyManifestSHA256: digestBytes(manifestBytes),
			ProducerSHA256:         artifactValue.SHA256,
		}
	}
	manager := RuntimeTreeManager{
		Home: t.TempDir(), Platform: "linux", Arch: "arm64",
		Snapshotter: artifact.Snapshotter{Open: func(context.Context, string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(archive)), nil
		}},
	}
	t.Cleanup(func() {
		_ = filepath.Walk(manager.Home, func(path string, _ os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
	return manager, catalog.Component{ID: "fixture-runtime", VersionPolicy: catalog.VersionPolicy{Mode: "pinned"}}, lock
}

func zipTree(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
