package state

import (
	"errors"
	"fmt"
	"os"
)

func ValidateOwnedRegularFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect owned path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("owned path %s must be a regular file and not a symlink", path)
	}
	return nil
}
