//go:build !windows

package host

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openGuestBootstrapArchive(path string) (*os.File, int64, error) {
	descriptor, err := unix.Open(
		path,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, 0, err
	}
	file := os.NewFile(uintptr(descriptor), "")
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, 0, errors.New("create guest bootstrap archive handle")
	}
	var status unix.Stat_t
	if err := unix.Fstat(descriptor, &status); err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	if status.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = file.Close()
		return nil, 0, errors.New("guest bootstrap archive is not regular")
	}
	return file, status.Size, nil
}
