package durable

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// PublishDirectory makes every staged regular file durable before atomically
// publishing the directory and flushing the destination parent.
func PublishDirectory(staging, destination string) error {
	if err := SyncTree(staging); err != nil {
		return err
	}
	if err := renameDurably(staging, destination); err != nil {
		return fmt.Errorf("rename durable directory: %w", err)
	}
	return nil
}

// PublishDirectoryNoReplace makes a staged regular tree durable and publishes
// it atomically only when the destination does not exist. It is intended for
// immutable, content-addressed generations that must never replace user data.
func PublishDirectoryNoReplace(staging, destination string) error {
	if err := SyncTree(staging); err != nil {
		return err
	}
	if err := publishDirectoryNoReplaceDurably(staging, destination); err != nil {
		return fmt.Errorf("publish durable directory without overwrite: %w", err)
	}
	return nil
}

func WriteFile(path string, content []byte, permission os.FileMode) error {
	return writeFile(path, content, permission, false)
}

// WriteFileNoReplace atomically publishes a new file and fails if the
// destination already exists.
func WriteFileNoReplace(
	path string,
	content []byte,
	permission os.FileMode,
) error {
	return writeFile(path, content, permission, true)
}

// RemoveFile durably removes the authoritative path by first moving it to a
// private tombstone with write-through semantics. A crash may leave only the
// tombstone, never resurrect the authoritative path.
func RemoveFile(path string) error {
	directory := filepath.Dir(path)
	tombstone, err := os.CreateTemp(directory, ".durable-remove-*")
	if err != nil {
		return err
	}
	tombstonePath := tombstone.Name()
	if err := tombstone.Close(); err != nil {
		_ = os.Remove(tombstonePath)
		return err
	}
	if err := os.Remove(tombstonePath); err != nil {
		return err
	}
	if err := renameDurably(path, tombstonePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.Remove(tombstonePath); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return SyncDirectory(directory)
}

func writeFile(
	path string,
	content []byte,
	permission os.FileMode,
	noReplace bool,
) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".durable-file-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	open := true
	cleanup := func() {
		if open {
			open = false
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}
	defer cleanup()
	if err := temporary.Chmod(permission); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	open = false
	if err := temporary.Close(); err != nil {
		return err
	}
	publish := replaceFileDurably
	if noReplace {
		publish = publishFileNoReplaceDurably
	}
	if err := publish(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

// SyncTree flushes regular files first and directories from leaves to root.
func SyncTree(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			return fmt.Errorf("durable publication refuses symlink %s", path)
		case entry.IsDir():
			directories = append(directories, path)
		case entry.Type().IsRegular():
			if err := syncRegularFile(path); err != nil {
				return fmt.Errorf("sync durable file %s: %w", path, err)
			}
		default:
			return fmt.Errorf("durable publication refuses non-regular path %s", path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(directories, func(left, right int) bool {
		return len(directories[left]) > len(directories[right])
	})
	for _, directory := range directories {
		if err := SyncDirectory(directory); err != nil {
			return fmt.Errorf("sync durable directory %s: %w", directory, err)
		}
	}
	return nil
}
