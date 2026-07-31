//go:build windows

package host

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func openGuestBootstrapArchive(path string) (*os.File, int64, error) {
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
		return nil, 0, errors.New("create guest bootstrap archive handle")
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = file.Close()
		return nil, 0, errors.New("guest bootstrap archive is a reparse point")
	}
	status, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	if !status.Mode().IsRegular() {
		_ = file.Close()
		return nil, 0, errors.New("guest bootstrap archive is not regular")
	}
	return file, status.Size(), nil
}
