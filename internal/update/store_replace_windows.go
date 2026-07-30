//go:build windows

package update

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func replaceFileDurably(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return fmt.Errorf("encode replacement source path: %w", err)
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return fmt.Errorf("encode replacement destination path: %w", err)
	}
	const flags = windows.MOVEFILE_REPLACE_EXISTING |
		windows.MOVEFILE_WRITE_THROUGH
	if err := windows.MoveFileEx(sourcePointer, destinationPointer, flags); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}
