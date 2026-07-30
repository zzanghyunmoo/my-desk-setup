package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	ErrLockContended         = errors.New("writer lock is already held")
	errAdvisoryLockContended = errors.New("advisory lock is contended")
)

type Lock struct {
	path string
	file *os.File
}

func Acquire(path string) (*Lock, error) {
	if err := ensureRegularOrMissing(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("acquire writer lock %s: %w", path, err)
	}
	if err := acquireAdvisoryLock(file); err != nil {
		closeErr := file.Close()
		if errors.Is(err, errAdvisoryLockContended) {
			return nil, errors.Join(
				fmt.Errorf("%w: %s", ErrLockContended, path),
				wrapCloseError(path, closeErr),
			)
		}
		return nil, errors.Join(
			fmt.Errorf("acquire OS writer lock %s: %w", path, err),
			wrapCloseError(path, closeErr),
		)
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, releaseAfterAcquireFailure(
			path,
			file,
			fmt.Errorf("restrict writer lock %s: %w", path, err),
		)
	}
	if err := file.Truncate(0); err != nil {
		return nil, releaseAfterAcquireFailure(
			path,
			file,
			fmt.Errorf("clear legacy writer lock owner %s: %w", path, err),
		)
	}
	return &Lock{path: path, file: file}, nil
}

func (lock *Lock) Holds(path string) bool {
	return lock != nil &&
		lock.file != nil &&
		filepath.Clean(lock.path) == filepath.Clean(path)
}

func (lock *Lock) Release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	unlockErr := releaseAdvisoryLock(file)
	closeErr := file.Close()
	return errors.Join(
		wrapUnlockError(lock.path, unlockErr),
		wrapCloseError(lock.path, closeErr),
	)
}

func releaseAfterAcquireFailure(
	path string,
	file *os.File,
	operationErr error,
) error {
	return errors.Join(
		operationErr,
		wrapUnlockError(path, releaseAdvisoryLock(file)),
		wrapCloseError(path, file.Close()),
	)
}

func wrapUnlockError(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("release OS writer lock %s: %w", path, err)
}

func wrapCloseError(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close writer lock %s: %w", path, err)
}
