package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

const lockRelativePath = "locks/versions.lock.yaml"

func writeLock(root string, lock catalog.VersionLock) error {
	if root == "" {
		return errors.New("catalog root is required")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect catalog root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return errors.New("catalog root must be a directory and not a symlink")
	}
	path := filepath.Join(root, filepath.FromSlash(lockRelativePath))
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect version lock directory: %w", err)
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return errors.New(
			"version lock directory must be a directory and not a symlink",
		)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect version lock: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("version lock must be regular and not a symlink")
	}
	encoded, err := yaml.Marshal(lock)
	if err != nil {
		return fmt.Errorf("encode version lock: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".versions-lock-*")
	if err != nil {
		return fmt.Errorf("create version lock temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryOpen := true
	cleanup := func() error {
		var cleanupErrors []error
		if temporaryOpen {
			temporaryOpen = false
			if err := temporary.Close(); err != nil {
				cleanupErrors = append(
					cleanupErrors,
					fmt.Errorf("close version lock temporary file during cleanup: %w", err),
				)
			}
		}
		if err := os.Remove(temporaryPath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(
				cleanupErrors,
				fmt.Errorf("remove version lock temporary file: %w", err),
			)
		}
		return errors.Join(cleanupErrors...)
	}
	failWithCleanup := func(operationErr error) error {
		return errors.Join(operationErr, cleanup())
	}
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return failWithCleanup(
			fmt.Errorf("chmod version lock temporary file: %w", err),
		)
	}
	if _, err := temporary.Write(encoded); err != nil {
		return failWithCleanup(
			fmt.Errorf("write version lock temporary file: %w", err),
		)
	}
	if err := temporary.Sync(); err != nil {
		return failWithCleanup(
			fmt.Errorf("sync version lock temporary file: %w", err),
		)
	}
	temporaryOpen = false
	if err := temporary.Close(); err != nil {
		return errors.Join(
			fmt.Errorf("close version lock temporary file: %w", err),
			cleanup(),
		)
	}
	if err := replaceFileDurably(temporaryPath, path); err != nil {
		return errors.Join(
			fmt.Errorf("publish version lock: %w", err),
			cleanup(),
		)
	}
	return nil
}
