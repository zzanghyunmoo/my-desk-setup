package packages

import (
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func BunInstall(action planning.Action, environment map[string]string) (transport.Command, error) {
	if action.Package == "" || action.Version == "" ||
		action.Version == "manager-owned" || action.Version == "manual" {
		return transport.Command{}, fmt.Errorf("Bun requires an exact package and version for %s", action.ID)
	}
	return transport.Command{
		Executable:  "bun",
		Arguments:   []string{"add", "--global", action.Package + "@" + action.Version},
		Environment: environment,
	}, nil
}
