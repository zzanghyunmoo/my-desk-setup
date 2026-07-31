//go:build windows

package safefile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// OpenRegularNoFollow opens one regular file without traversing a reparse
// point and denies concurrent writes, renames, and deletes while it is open.
func OpenRegularNoFollow(path string) (*os.File, int64, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, 0, err
	}
	handle, err := windows.CreateFile(
		pathPointer,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN,
		0,
	)
	if err != nil {
		return nil, 0, err
	}
	file := os.NewFile(uintptr(handle), "")
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, 0, errors.New("create regular file handle")
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = file.Close()
		return nil, 0, errors.New("path is a reparse point")
	}
	status, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	if !status.Mode().IsRegular() {
		_ = file.Close()
		return nil, 0, errors.New("path is not a regular file")
	}
	return file, status.Size(), nil
}
