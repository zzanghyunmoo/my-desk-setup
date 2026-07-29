package packages

import (
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func MiseInstall(action planning.Action, environment map[string]string) ([]transport.Command, error) {
	if action.Package == "" || action.Version == "" ||
		action.Version == "manager-owned" || action.Version == "manual" {
		return nil, fmt.Errorf("mise requires an exact package and version for %s", action.ID)
	}
	tool := action.Package + "@" + action.Version
	return []transport.Command{
		{Executable: "mise", Arguments: []string{"install", tool}, Environment: environment},
		{
			Executable:  "mise",
			Arguments:   []string{"use", "--global", "--yes", tool},
			Environment: environment,
		},
	}, nil
}
