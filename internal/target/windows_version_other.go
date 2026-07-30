//go:build !windows

package target

import "errors"

func observeWindowsVersion() (string, error) {
	return "", errors.New("native Windows version observation is unavailable")
}
