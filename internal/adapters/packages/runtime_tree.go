package packages

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
)

const (
	runtimeTreeOwnerSchema  = "mds.runtime-tree-owner/v1"
	runtimeTreeOwnerFile    = ".mds-runtime-tree-owner.json"
	runtimeTreeCurrentFile  = ".mds-runtime-tree-current"
	runtimeTreeManifestFile = ".mds-runtime-tree-manifest.json"
)

type runtimeTreeOwner struct {
	SchemaVersion  string `json:"schema_version"`
	ComponentID    string `json:"component_id"`
	ArchiveSHA256  string `json:"archive_sha256"`
	ManifestSHA256 string `json:"manifest_sha256"`
	LauncherSHA256 string `json:"launcher_sha256"`
}

// RuntimeTreeManager publishes extracted multi-file artifacts as immutable,
// content-addressed generations. The current descriptor is replaced only
// after the new generation is completely durable.
type RuntimeTreeManager struct {
	Home        string
	Platform    string
	Arch        string
	Snapshotter artifact.Snapshotter

	// BeforeActivate is a deterministic crash seam for publication tests.
	BeforeActivate func() error
}

func (manager RuntimeTreeManager) Observe(
	component catalog.Component,
	lock catalog.LockEntry,
) (adapters.Observation, string, error) {
	artifactValue, identity, err := manager.runtimeTreeArtifact(lock)
	if err != nil {
		return adapters.Observation{}, "", err
	}
	destination := manager.destinationFor(component.ID, artifactValue)
	parentsReady, err := inspectRuntimeTreePathBelow(manager.Home, filepath.Dir(destination))
	if err != nil {
		return runtimeTreeConflict(err.Error())
	}
	if !parentsReady {
		return adapters.Observation{State: adapters.StateAbsent}, "", nil
	}
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return adapters.Observation{State: adapters.StateAbsent}, "", nil
	}
	if err != nil {
		return adapters.Observation{}, "", fmt.Errorf("inspect runtime tree: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return runtimeTreeConflict("runtime tree destination is not a regular directory")
	}
	expectedOwner, err := runtimeTreeOwnerBytes(component.ID, artifactValue)
	if err != nil {
		return adapters.Observation{}, "", err
	}
	owner, err := readRuntimeTreeRegular(filepath.Join(destination, runtimeTreeOwnerFile))
	if err != nil || !bytes.Equal(owner, expectedOwner) {
		return runtimeTreeConflict("existing runtime tree destination is user-owned or unmarked")
	}
	current, err := readRuntimeTreeRegular(filepath.Join(destination, runtimeTreeCurrentFile))
	if err != nil {
		return adapters.Observation{State: adapters.StateAbsent, Detail: "runtime tree current generation is missing"}, "", nil
	}
	generation := strings.TrimSpace(string(current))
	if !validRuntimeTreeGeneration(generation) {
		return adapters.Observation{State: adapters.StateAbsent, Detail: "runtime tree current generation is invalid"}, "", nil
	}
	payload := filepath.Join(destination, "generations", generation, "payload")
	storedManifest, err := readRuntimeTreeRegular(filepath.Join(destination, "generations", generation, runtimeTreeManifestFile))
	if err != nil || digestBytes(storedManifest) != identity.ManifestSHA256 {
		return adapters.Observation{State: adapters.StateAbsent, Detail: "runtime tree manifest differs"}, "", nil
	}
	manifest, actualManifest, manifestSHA, err := artifact.BuildTreeManifest(payload)
	if err != nil || manifestSHA != identity.ManifestSHA256 || !bytes.Equal(actualManifest, storedManifest) {
		return adapters.Observation{State: adapters.StateAbsent, Detail: "runtime tree content differs"}, "", nil
	}
	if lock.FixtureCache != nil && digestBytes(storedManifest) != lock.FixtureCache.ReadOnlyManifestSHA256 {
		return adapters.Observation{State: adapters.StateAbsent, Detail: "fixture cache read-only manifest differs"}, "", nil
	}
	executablePaths := runtimeTreeExecutablePaths(identity, artifactValue.Executable)
	executableSet := make(map[string]bool, len(executablePaths))
	for _, executable := range executablePaths {
		executableSet[executable] = true
	}
	entryByPath := make(map[string]artifact.TreeManifestEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		entryByPath[entry.Path] = entry
		path := filepath.Join(payload, filepath.FromSlash(entry.Path))
		entryInfo, statErr := os.Lstat(path)
		if statErr != nil || entryInfo.Mode().Perm()&0o222 != 0 {
			return adapters.Observation{State: adapters.StateAbsent, Detail: "runtime tree is writable or missing"}, "", nil
		}
		if entry.Type == "file" && runtime.GOOS != "windows" &&
			(entryInfo.Mode().Perm()&0o111 != 0) != executableSet[entry.Path] {
			return adapters.Observation{State: adapters.StateAbsent, Detail: "runtime tree executable mode differs: " + entry.Path}, "", nil
		}
	}
	for _, required := range identity.RequiredPaths {
		if _, exists := entryByPath[required]; !exists {
			return adapters.Observation{State: adapters.StateAbsent, Detail: "runtime tree required path is missing: " + required}, "", nil
		}
	}
	for _, executable := range executablePaths {
		entry, exists := entryByPath[executable]
		path := filepath.Join(payload, filepath.FromSlash(executable))
		info, statErr := os.Lstat(path)
		if !exists || entry.Type != "file" || statErr != nil ||
			!info.Mode().IsRegular() ||
			(runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
			return adapters.Observation{State: adapters.StateAbsent, Detail: "runtime tree executable differs: " + executable}, "", nil
		}
	}
	launcherEntry, exists := entryByPath[artifactValue.Executable]
	launcher := filepath.Join(payload, filepath.FromSlash(artifactValue.Executable))
	launcherInfo, statErr := os.Lstat(launcher)
	if !exists || launcherEntry.Type != "file" || statErr != nil ||
		!launcherInfo.Mode().IsRegular() ||
		(runtimeTreeUsage(identity) == "direct" && runtime.GOOS != "windows" && launcherInfo.Mode().Perm()&0o111 == 0) ||
		launcherEntry.SHA256 != identity.LauncherSHA256 {
		return adapters.Observation{State: adapters.StateAbsent, Detail: "runtime tree launcher differs"}, "", nil
	}
	absolute, err := filepath.Abs(launcher)
	if err != nil {
		return adapters.Observation{}, "", err
	}
	return adapters.Observation{State: adapters.StateReady, InstalledVersion: lock.Version}, absolute, nil
}

func (manager RuntimeTreeManager) Apply(
	ctx context.Context,
	component catalog.Component,
	lock catalog.LockEntry,
) (returnErr error) {
	observation, _, err := manager.Observe(component, lock)
	if err != nil {
		return err
	}
	if observation.State == adapters.StateReady {
		return nil
	}
	if observation.State == adapters.StateConflict {
		return errors.New(observation.Detail)
	}
	artifactValue, identity, err := manager.runtimeTreeArtifact(lock)
	if err != nil {
		return err
	}
	snapshotter := manager.Snapshotter
	if identity.MaxTotalBytes > 0 {
		snapshotter.Limits.TotalBytes = identity.MaxTotalBytes
		snapshotter.Limits.Entries = identity.MaxEntries
	}
	snapshot, err := snapshotter.Acquire(ctx, artifact.SnapshotRequest{
		URL: artifactValue.URL, SHA256: artifactValue.SHA256,
		Format: artifactValue.Format, Executable: artifactValue.Executable,
		ExecutableSHA256: identity.LauncherSHA256, ExtractAll: true,
	})
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, snapshot.Close()) }()
	manifest, manifestBytes, manifestSHA, err := artifact.BuildTreeManifest(snapshot.Root())
	if err != nil {
		return err
	}
	if manifestSHA != identity.ManifestSHA256 {
		return fmt.Errorf("runtime tree manifest digest mismatch: expected %s got %s", identity.ManifestSHA256, manifestSHA)
	}
	if lock.FixtureCache != nil && lock.FixtureCache.ReadOnlyManifestSHA256 != manifestSHA {
		return errors.New("fixture cache read-only manifest does not match extracted runtime tree")
	}
	entries := make(map[string]artifact.TreeManifestEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		entries[entry.Path] = entry
	}
	for _, required := range identity.RequiredPaths {
		if _, exists := entries[required]; !exists {
			return fmt.Errorf("runtime tree required path is missing: %s", required)
		}
	}
	destination := manager.destinationFor(component.ID, artifactValue)
	if err := manager.ensureDestination(component.ID, artifactValue); err != nil {
		return err
	}
	generations := filepath.Join(destination, "generations")
	staging, err := os.MkdirTemp(generations, ".runtime-tree-staging-*")
	if err != nil {
		return fmt.Errorf("create runtime tree staging generation: %w", err)
	}
	defer os.RemoveAll(staging)
	payload := filepath.Join(staging, "payload")
	if err := copyRuntimeTree(snapshot.Root(), payload, manifest); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, runtimeTreeManifestFile), manifestBytes, 0o444); err != nil {
		return fmt.Errorf("write runtime tree manifest: %w", err)
	}
	if err := protectRuntimeTree(
		payload,
		runtimeTreeExecutablePaths(identity, artifactValue.Executable),
		runtimeTreeUsage(identity),
	); err != nil {
		return err
	}
	generation := "g-" + strings.TrimPrefix(filepath.Base(staging), ".runtime-tree-staging-")
	published := filepath.Join(generations, generation)
	if err := durable.PublishDirectoryNoReplace(staging, published); err != nil {
		return fmt.Errorf("publish runtime tree generation: %w", err)
	}
	if manager.BeforeActivate != nil {
		if err := manager.BeforeActivate(); err != nil {
			return errors.Join(
				fmt.Errorf("activate runtime tree generation: %w", err),
				removeRuntimeTreeGeneration(destination, generation),
			)
		}
	}
	if err := durable.WriteFile(filepath.Join(destination, runtimeTreeCurrentFile), []byte(generation+"\n"), 0o600); err != nil {
		return errors.Join(
			fmt.Errorf("activate runtime tree generation: %w", err),
			removeRuntimeTreeGeneration(destination, generation),
		)
	}
	ready, _, err := manager.Observe(component, lock)
	if err != nil {
		return err
	}
	if ready.State != adapters.StateReady {
		return errors.New("runtime tree is not ready after publication: " + ready.Detail)
	}
	return pruneRuntimeTreeGenerations(destination, generation)
}

func (manager RuntimeTreeManager) runtimeTreeArtifact(lock catalog.LockEntry) (catalog.Artifact, *catalog.RuntimeTreeIdentity, error) {
	key := manager.Platform + "-" + manager.Arch
	value, exists := lock.Artifacts[key]
	if !exists || value.Tree == nil {
		return catalog.Artifact{}, nil, fmt.Errorf("runtime tree artifact is missing for %s", key)
	}
	return value, value.Tree, nil
}

func (manager RuntimeTreeManager) destination(component catalog.Component, lock catalog.LockEntry) string {
	value, _, err := manager.runtimeTreeArtifact(lock)
	if err != nil {
		return ""
	}
	return manager.destinationFor(component.ID, value)
}

func (manager RuntimeTreeManager) destinationFor(componentID string, value catalog.Artifact) string {
	return filepath.Join(manager.Home, ".local", "share", "mds", "runtime-trees", componentID, value.Tree.ManifestSHA256+"-"+value.SHA256)
}

func (manager RuntimeTreeManager) ensureDestination(componentID string, value catalog.Artifact) error {
	if componentID == "" || filepath.Base(componentID) != componentID || strings.ContainsAny(componentID, "/\\\x00") {
		return errors.New("runtime tree component ID is not a safe path segment")
	}
	destination := manager.destinationFor(componentID, value)
	if err := ensureRuntimeTreeDirectoryBelow(manager.Home, filepath.Dir(destination)); err != nil {
		return err
	}
	expected, err := runtimeTreeOwnerBytes(componentID, value)
	if err != nil {
		return err
	}
	info, err := os.Lstat(destination)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("runtime tree destination is not a regular directory")
		}
		owner, readErr := readRuntimeTreeRegular(filepath.Join(destination, runtimeTreeOwnerFile))
		if readErr != nil || !bytes.Equal(owner, expected) {
			return errors.New("existing runtime tree destination is user-owned or unmarked")
		}
		return ensureRuntimeTreeDirectoryBelow(manager.Home, filepath.Join(destination, "generations"))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".runtime-tree-root-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := os.WriteFile(filepath.Join(staging, runtimeTreeOwnerFile), expected, 0o600); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(staging, "generations"), 0o700); err != nil {
		return err
	}
	if err := durable.PublishDirectoryNoReplace(staging, destination); err != nil {
		return fmt.Errorf("publish runtime tree root: %w", err)
	}
	return nil
}

func runtimeTreeOwnerBytes(componentID string, value catalog.Artifact) ([]byte, error) {
	encoded, err := json.Marshal(runtimeTreeOwner{
		SchemaVersion: runtimeTreeOwnerSchema, ComponentID: componentID,
		ArchiveSHA256: value.SHA256, ManifestSHA256: value.Tree.ManifestSHA256,
		LauncherSHA256: value.Tree.LauncherSHA256,
	})
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func copyRuntimeTree(source, destination string, manifest artifact.TreeManifest) error {
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	for _, entry := range manifest.Entries {
		target := filepath.Join(destination, filepath.FromSlash(entry.Path))
		if entry.Type == "directory" {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := copyRuntimeTreeFile(
			filepath.Join(source, filepath.FromSlash(entry.Path)),
			target,
			entry.Path,
			entry.SHA256,
		); err != nil {
			return err
		}
	}
	return nil
}

func copyRuntimeTreeFile(source, destination, displayPath, expectedSHA256 string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.CopyBuffer(io.MultiWriter(output, hash), input, make([]byte, 64<<10))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return fmt.Errorf("runtime tree source changed while publishing %s", displayPath)
	}
	return nil
}

func protectRuntimeTree(root string, executables []string, usage string) error {
	executableSet := make(map[string]bool, len(executables))
	for _, executable := range executables {
		executableSet[executable] = true
	}
	var directories []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			directories = append(directories, path)
			return nil
		}
		mode := os.FileMode(0o444)
		relative, _ := filepath.Rel(root, path)
		if executableSet[filepath.ToSlash(relative)] && usage == "direct" {
			mode = 0o555
		}
		return os.Chmod(path, mode)
	})
	if err != nil {
		return err
	}
	sort.Slice(directories, func(left, right int) bool { return len(directories[left]) > len(directories[right]) })
	for _, directory := range directories {
		if err := os.Chmod(directory, 0o555); err != nil {
			return err
		}
	}
	return nil
}

func runtimeTreeExecutablePaths(identity *catalog.RuntimeTreeIdentity, launcher string) []string {
	if identity == nil || runtimeTreeUsage(identity) == "resource" {
		return nil
	}
	if len(identity.ExecutablePaths) == 0 {
		return []string{launcher}
	}
	return identity.ExecutablePaths
}

func runtimeTreeUsage(identity *catalog.RuntimeTreeIdentity) string {
	if identity != nil && identity.Usage == "resource" {
		return "resource"
	}
	return "direct"
}

func ensureRuntimeTreeDirectoryBelow(root, destination string) error {
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("runtime tree parent escapes home")
	}
	current := root
	for _, part := range strings.Split(relative, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("runtime tree parent is not a regular directory")
		}
	}
	return nil
}

func inspectRuntimeTreePathBelow(root, destination string) (bool, error) {
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return false, errors.New("runtime tree parent escapes home")
	}
	current := root
	for _, part := range strings.Split(relative, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false, errors.New("runtime tree parent is not a regular directory")
		}
	}
	return true, nil
}

func readRuntimeTreeRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("runtime tree metadata is not a regular file")
	}
	return os.ReadFile(path)
}

func validRuntimeTreeGeneration(value string) bool {
	if !strings.HasPrefix(value, "g-") || len(value) < 4 || filepath.Base(value) != value {
		return false
	}
	return !strings.ContainsAny(value, "/\\\x00")
}

func pruneRuntimeTreeGenerations(destination, current string) error {
	if !validRuntimeTreeGeneration(current) {
		return errors.New("runtime tree current generation is invalid")
	}
	entries, err := os.ReadDir(filepath.Join(destination, "generations"))
	if err != nil {
		return fmt.Errorf("list runtime tree generations: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == current || !validRuntimeTreeGeneration(entry.Name()) {
			continue
		}
		if err := removeRuntimeTreeGeneration(destination, entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

func removeRuntimeTreeGeneration(destination, generation string) error {
	if !validRuntimeTreeGeneration(generation) {
		return errors.New("runtime tree generation is invalid")
	}
	root := filepath.Join(destination, "generations", generation)
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect runtime tree generation for cleanup: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("runtime tree generation cleanup target is not a regular directory")
	}
	if err := filepath.Walk(root, func(path string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("runtime tree generation contains a symbolic link")
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("prepare runtime tree generation cleanup: %w", err)
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove runtime tree generation: %w", err)
	}
	return nil
}

func digestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func runtimeTreeConflict(detail string) (adapters.Observation, string, error) {
	return adapters.Observation{State: adapters.StateConflict, Detail: detail}, "", nil
}
