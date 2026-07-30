//go:build windows

package evidence

import (
	"errors"
	"io/fs"
)

func runtimeMarkerOwnedByRoot(fs.FileInfo) bool {
	return false
}

func readRootOwnedRuntimeMarker(string) ([]byte, error) {
	return nil, errors.New("root-owned runtime markers are unavailable on Windows")
}
