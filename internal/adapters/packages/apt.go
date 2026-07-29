package packages

import (
	"fmt"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func APTInstall(action planning.Action) ([]transport.Command, error) {
	packages := strings.Fields(action.Package)
	if len(packages) == 0 {
		return nil, fmt.Errorf("APT package is required for %s", action.ID)
	}
	arguments := []string{
		"-n",
		"env",
		"DEBIAN_FRONTEND=noninteractive",
		"APT_LISTCHANGES_FRONTEND=none",
		"apt-get",
		"install",
		"-y",
		"--no-install-recommends",
	}
	arguments = append(arguments, packages...)
	return []transport.Command{
		{Executable: "sudo", Arguments: []string{"-n", "true"}},
		{
			Executable: "sudo",
			Arguments:  arguments,
		},
	}, nil
}
