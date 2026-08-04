//go:build linux

package durable

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

func publishDirectoryNoReplaceDurably(source, destination string) error {
	if err := unix.Renameat2(
		unix.AT_FDCWD,
		source,
		unix.AT_FDCWD,
		destination,
		unix.RENAME_NOREPLACE,
	); err != nil {
		return err
	}
	return SyncDirectory(filepath.Dir(destination))
}
