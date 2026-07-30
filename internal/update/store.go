package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
)

const lockRelativePath = "locks/versions.lock.yaml"

func ValidateCatalogRoot(root string) error {
	if err := validateWritableCatalogRoot(root); err != nil {
		return invalid(err)
	}
	return nil
}

func validateWritableCatalogRoot(root string) error {
	if root == "" {
		return errors.New(
			"writable checkout catalog root is required; embedded release catalog data is read-only",
		)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect writable catalog root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return errors.New(
			"writable catalog root must be a checkout directory and not a symlink",
		)
	}
	path := filepath.Join(root, filepath.FromSlash(lockRelativePath))
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect writable version lock directory: %w", err)
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return errors.New(
			"writable version lock directory must be a directory and not a symlink",
		)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect writable version lock: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New(
			"writable version lock must be regular and not a symlink",
		)
	}
	for label, mode := range map[string]os.FileMode{
		"catalog root":   rootInfo.Mode(),
		"lock directory": directoryInfo.Mode(),
		"version lock":   info.Mode(),
	} {
		if mode.Perm()&0o222 == 0 {
			return fmt.Errorf(
				"%s is read-only; update requires a writable checkout passed with --catalog",
				label,
			)
		}
	}
	return nil
}

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
	if err := durable.WriteFile(path, encoded, info.Mode().Perm()); err != nil {
		return fmt.Errorf("publish version lock durably: %w", err)
	}
	return nil
}
