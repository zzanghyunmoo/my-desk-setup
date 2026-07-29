package state

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

type Lock struct {
	path string
	file *os.File
}

func Acquire(path string) (*Lock, error) {
	if err := ensureRegularOrMissing(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("target already has an active writer lock: %s", path)
	}
	if err != nil {
		return nil, fmt.Errorf("acquire writer lock %s: %w", path, err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write writer lock %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("sync writer lock %s: %w", path, err)
	}
	return &Lock{path: path, file: file}, nil
}

func (lock *Lock) Release() error {
	if lock == nil {
		return nil
	}
	var closeError error
	if lock.file != nil {
		closeError = lock.file.Close()
		lock.file = nil
	}
	removeError := os.Remove(lock.path)
	if closeError != nil {
		return fmt.Errorf("close writer lock: %w", closeError)
	}
	if removeError != nil && !errors.Is(removeError, os.ErrNotExist) {
		return fmt.Errorf("remove writer lock: %w", removeError)
	}
	return nil
}
