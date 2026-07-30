//go:build !windows

package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func replaceFileDurably(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("open replacement directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	var durabilityErrors []error
	if syncErr != nil {
		durabilityErrors = append(
			durabilityErrors,
			fmt.Errorf("sync replacement directory: %w", syncErr),
		)
	}
	if closeErr != nil {
		durabilityErrors = append(
			durabilityErrors,
			fmt.Errorf("close replacement directory: %w", closeErr),
		)
	}
	return errors.Join(durabilityErrors...)
}
