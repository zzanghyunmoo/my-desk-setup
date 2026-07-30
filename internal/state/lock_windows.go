//go:build windows

package state

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func acquireAdvisoryLock(file *os.File) error {
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&windows.Overlapped{},
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errAdvisoryLockContended
	}
	return err
}

func releaseAdvisoryLock(file *os.File) error {
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		1,
		0,
		&windows.Overlapped{},
	)
}
