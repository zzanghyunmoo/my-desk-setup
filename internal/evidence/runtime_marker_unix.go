//go:build !windows

package evidence

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func runtimeMarkerOwnedByRoot(info fs.FileInfo) bool {
	status, ok := info.Sys().(*syscall.Stat_t)
	return ok && status.Uid == 0
}

func readRootOwnedRuntimeMarker(path string) ([]byte, error) {
	descriptor, err := unix.Open(
		path,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open root-owned runtime marker: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, errors.New("open root-owned runtime marker: invalid descriptor")
	}
	defer func() {
		_ = file.Close()
	}()
	var status unix.Stat_t
	if err := unix.Fstat(descriptor, &status); err != nil {
		return nil, fmt.Errorf("inspect root-owned runtime marker: %w", err)
	}
	if status.Mode&unix.S_IFMT != unix.S_IFREG ||
		status.Uid != 0 ||
		status.Mode&0o022 != 0 {
		return nil, errors.New(
			"runtime marker must be a root-owned regular file that is not group/world writable",
		)
	}
	content, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return nil, fmt.Errorf("read root-owned runtime marker: %w", err)
	}
	if len(content) > 4096 {
		return nil, errors.New("root-owned runtime marker exceeds 4096 bytes")
	}
	return content, nil
}
