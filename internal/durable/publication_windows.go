//go:build windows

package durable

import "golang.org/x/sys/windows"

func renameDurably(source, destination string) error {
	return moveFileDurably(source, destination, windows.MOVEFILE_WRITE_THROUGH)
}

func replaceFileDurably(source, destination string) error {
	return moveFileDurably(
		source,
		destination,
		windows.MOVEFILE_WRITE_THROUGH|windows.MOVEFILE_REPLACE_EXISTING,
	)
}

func publishFileNoReplaceDurably(source, destination string) error {
	return moveFileDurably(
		source,
		destination,
		windows.MOVEFILE_WRITE_THROUGH,
	)
}

func moveFileDurably(source, destination string, flags uint32) error {
	sourceUTF16, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationUTF16, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		sourceUTF16,
		destinationUTF16,
		flags,
	)
}

func SyncDirectory(string) error {
	// MoveFileEx(MOVEFILE_WRITE_THROUGH) flushes directory publication.
	return nil
}
