package packages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
)

const (
	runtimeViewOwnerSchema = "mds.runtime-view-owner/v1"
	runtimeViewOwnerFile   = ".mds-runtime-view-owner.json"
)

type runtimeViewOwner struct {
	SchemaVersion  string `json:"schema_version"`
	ComponentID    string `json:"component_id"`
	Version        string `json:"version"`
	ArchiveSHA256  string `json:"archive_sha256"`
	ManifestSHA256 string `json:"manifest_sha256"`
	LauncherSHA256 string `json:"launcher_sha256"`
}

func requiresRuntimeView(component catalog.Component) bool {
	return component.ID == "neovim"
}

func (manager RuntimeTreeManager) ObserveRuntimeView(
	component catalog.Component,
	lock catalog.LockEntry,
) (adapters.Observation, string, error) {
	if !requiresRuntimeView(component) {
		return adapters.Observation{State: adapters.StateReady, InstalledVersion: lock.Version}, "", nil
	}
	artifactValue, identity, err := manager.runtimeTreeArtifact(lock)
	if err != nil {
		return adapters.Observation{}, "", err
	}
	destination, err := manager.runtimeViewDestination(component, lock)
	if err != nil {
		return adapters.Observation{}, "", err
	}
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
		return adapters.Observation{}, "", fmt.Errorf("inspect runtime view: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return runtimeTreeConflict("runtime view destination is not a regular directory")
	}
	expectedOwner, err := runtimeViewOwnerBytes(component, lock, artifactValue)
	if err != nil {
		return adapters.Observation{}, "", err
	}
	owner, err := readRuntimeTreeRegular(filepath.Join(destination, runtimeViewOwnerFile))
	if err != nil || !bytes.Equal(owner, expectedOwner) {
		return runtimeTreeConflict("existing runtime view is user-owned or unmarked")
	}
	payload := filepath.Join(destination, "payload")
	manifest, _, manifestSHA, err := artifact.BuildTreeManifest(payload)
	if err != nil || manifestSHA != identity.ManifestSHA256 {
		return runtimeTreeConflict("runtime view content differs")
	}
	for _, entry := range manifest.Entries {
		path := filepath.Join(payload, filepath.FromSlash(entry.Path))
		entryInfo, statErr := os.Lstat(path)
		if statErr != nil || entryInfo.Mode()&os.ModeSymlink != 0 || entryInfo.Mode().Perm()&0o222 != 0 {
			return runtimeTreeConflict("runtime view is writable or missing")
		}
	}
	launcher := filepath.Join(payload, filepath.FromSlash(artifactValue.Executable))
	launcherInfo, err := os.Lstat(launcher)
	if err != nil || !launcherInfo.Mode().IsRegular() ||
		(runtime.GOOS != "windows" && launcherInfo.Mode().Perm()&0o111 == 0) {
		return runtimeTreeConflict("runtime view launcher differs")
	}
	return adapters.Observation{State: adapters.StateReady, InstalledVersion: lock.Version}, launcher, nil
}

func (manager RuntimeTreeManager) ApplyRuntimeView(
	_ context.Context,
	component catalog.Component,
	lock catalog.LockEntry,
) (returnErr error) {
	if !requiresRuntimeView(component) {
		return nil
	}
	observation, _, err := manager.ObserveRuntimeView(component, lock)
	if err != nil {
		return err
	}
	if observation.State == adapters.StateReady {
		return nil
	}
	if observation.State == adapters.StateConflict {
		return errors.New(observation.Detail)
	}
	treeObservation, launcher, err := manager.Observe(component, lock)
	if err != nil {
		return err
	}
	if treeObservation.State != adapters.StateReady {
		return errors.New("runtime tree is not ready for activation view")
	}
	artifactValue, identity, err := manager.runtimeTreeArtifact(lock)
	if err != nil {
		return err
	}
	source, err := runtimeTreePayloadRoot(launcher, artifactValue.Executable)
	if err != nil {
		return err
	}
	manifest, _, manifestSHA, err := artifact.BuildTreeManifest(source)
	if err != nil {
		return err
	}
	if manifestSHA != identity.ManifestSHA256 {
		return errors.New("runtime tree changed before activation view publication")
	}
	destination, err := manager.runtimeViewDestination(component, lock)
	if err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	if err := ensureRuntimeTreeDirectoryBelow(manager.Home, parent); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".runtime-view-staging-*")
	if err != nil {
		return fmt.Errorf("create runtime view staging: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, cleanupRuntimeViewStaging(staging))
	}()
	payload := filepath.Join(staging, "payload")
	if err := hardlinkRuntimeView(source, payload, manifest); err != nil {
		return err
	}
	owner, err := runtimeViewOwnerBytes(component, lock, artifactValue)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, runtimeViewOwnerFile), owner, 0o444); err != nil {
		return fmt.Errorf("write runtime view owner: %w", err)
	}
	if err := protectRuntimeViewDirectories(payload); err != nil {
		return err
	}
	if err := durable.PublishDirectoryNoReplace(staging, destination); err != nil {
		ready, _, observeErr := manager.ObserveRuntimeView(component, lock)
		if observeErr == nil && ready.State == adapters.StateReady {
			return nil
		}
		return err
	}
	staging = ""
	ready, _, err := manager.ObserveRuntimeView(component, lock)
	if err != nil {
		return err
	}
	if ready.State != adapters.StateReady {
		return errors.New("runtime view is not ready after publication")
	}
	return nil
}

func (manager RuntimeTreeManager) runtimeViewDestination(
	component catalog.Component,
	lock catalog.LockEntry,
) (string, error) {
	if lock.Version == "" || !adapters.ValidExecutableName(lock.Version) {
		return "", errors.New("runtime view version is not a safe path segment")
	}
	return filepath.Join(manager.Home, ".local", "share", "mds", component.ID, lock.Version), nil
}

func runtimeViewOwnerBytes(
	component catalog.Component,
	lock catalog.LockEntry,
	artifactValue catalog.Artifact,
) ([]byte, error) {
	owner := runtimeViewOwner{
		SchemaVersion: runtimeViewOwnerSchema, ComponentID: component.ID,
		Version: lock.Version, ArchiveSHA256: artifactValue.SHA256,
		ManifestSHA256: artifactValue.Tree.ManifestSHA256,
		LauncherSHA256: artifactValue.Tree.LauncherSHA256,
	}
	content, err := json.Marshal(owner)
	if err != nil {
		return nil, fmt.Errorf("encode runtime view owner: %w", err)
	}
	return append(content, '\n'), nil
}

func runtimeTreePayloadRoot(launcher, executable string) (string, error) {
	relative := filepath.FromSlash(executable)
	suffix := string(os.PathSeparator) + relative
	if !strings.HasSuffix(launcher, suffix) {
		return "", errors.New("runtime tree launcher is outside its payload")
	}
	return strings.TrimSuffix(launcher, suffix), nil
}

func hardlinkRuntimeView(source, destination string, manifest artifact.TreeManifest) error {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return fmt.Errorf("create runtime view payload: %w", err)
	}
	for _, entry := range manifest.Entries {
		target := filepath.Join(destination, filepath.FromSlash(entry.Path))
		switch entry.Type {
		case "directory":
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("create runtime view directory: %w", err)
			}
		case "file":
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return fmt.Errorf("create runtime view parent: %w", err)
			}
			if err := os.Link(filepath.Join(source, filepath.FromSlash(entry.Path)), target); err != nil {
				return fmt.Errorf("link runtime view file: %w", err)
			}
		default:
			return fmt.Errorf("unsupported runtime view entry type %q", entry.Type)
		}
	}
	return nil
}

func protectRuntimeViewDirectories(root string) error {
	var directories []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			directories = append(directories, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("inspect runtime view directories: %w", err)
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Chmod(directories[index], 0o555); err != nil {
			return fmt.Errorf("protect runtime view directory: %w", err)
		}
	}
	return nil
}

func cleanupRuntimeViewStaging(root string) error {
	if root == "" {
		return nil
	}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove runtime view staging: %w", err)
	}
	return nil
}
