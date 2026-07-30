//go:build windows

package target

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func observeWindowsVersion() (string, error) {
	info := windows.RtlGetVersion()
	if info == nil {
		return "", errors.New("RtlGetVersion returned no version")
	}
	return fmt.Sprintf(
		"%d.%d.%d",
		info.MajorVersion,
		info.MinorVersion,
		info.BuildNumber,
	), nil
}
