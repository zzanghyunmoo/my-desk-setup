//go:build !windows && !darwin && !linux

package durable

import (
	"errors"
	"os"
	"path/filepath"
)

func publishDirectoryNoReplaceDurably(source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	return SyncDirectory(filepath.Dir(destination))
}
