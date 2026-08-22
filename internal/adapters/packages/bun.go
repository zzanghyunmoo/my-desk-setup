package packages

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func BunInstall(
	action planning.Action,
	localArtifact string,
	environment map[string]string,
) (transport.Command, error) {
	if action.Package == "" || action.Version == "" ||
		action.Version == "manager-owned" || action.Version == "manual" {
		return transport.Command{}, fmt.Errorf("bun requires an exact package and version for %s", action.ID)
	}
	if !filepath.IsAbs(localArtifact) {
		return transport.Command{}, fmt.Errorf(
			"bun requires an absolute verified local artifact for %s",
			action.ID,
		)
	}
	return transport.Command{
		Executable:  "bun",
		Arguments:   []string{"add", "--global", localArtifact},
		Environment: environment,
		Timeout:     15 * time.Minute,
	}, nil
}

func BunRemove(
	packageName string,
	environment map[string]string,
) transport.Command {
	return transport.Command{
		Executable:  "bun",
		Arguments:   []string{"remove", "--global", packageName},
		Environment: environment,
		Timeout:     15 * time.Minute,
	}
}
