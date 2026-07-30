package packages

import (
	"fmt"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func WinGetInstall(action planning.Action) (transport.Command, error) {
	if action.Package == "" {
		return transport.Command{}, fmt.Errorf("WinGet package ID is required for %s", action.ID)
	}
	arguments := []string{
		"install",
		"--id", action.Package,
		"--exact",
		"--disable-interactivity",
		"--accept-package-agreements",
		"--accept-source-agreements",
	}
	if action.Version != "" && action.Version != "manager-owned" {
		arguments = append(arguments, "--version", action.Version)
	}
	return transport.Command{
		Executable: "winget.exe",
		Arguments:  arguments,
		Timeout:    45 * time.Minute,
	}, nil
}
