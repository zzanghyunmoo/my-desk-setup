//go:build darwin

package durable

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

func publishDirectoryNoReplaceDurably(source, destination string) error {
	if err := unix.RenamexNp(source, destination, unix.RENAME_EXCL); err != nil {
		return err
	}
	return SyncDirectory(filepath.Dir(destination))
}
