package packages

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/managedfile"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func MiseInstall(action planning.Action, environment map[string]string) ([]transport.Command, error) {
	if action.Package == "" || action.Version == "" ||
		action.Version == "manager-owned" || action.Version == "manual" {
		return nil, fmt.Errorf("mise requires an exact package and version for %s", action.ID)
	}
	if action.Inputs["artifact_sha256"] == "" ||
		action.Inputs["artifact_url"] == "" ||
		action.Inputs["mise_ref"] == "" {
		return nil, fmt.Errorf(
			"mise requires reviewed artifact identity and install ref for %s",
			action.ID,
		)
	}
	return []transport.Command{
		{
			Executable: "mise",
			Arguments: []string{
				"install", "--locked", action.Package,
			},
			Environment: environment,
			Timeout:     45 * time.Minute,
		},
		{
			Executable:  "mise",
			Arguments:   []string{"reshim"},
			Environment: environment,
			Timeout:     5 * time.Minute,
		},
	}, nil
}

func PublishMiseConfig(home string, mise catalog.MiseFiles) error {
	if home == "" {
		return fmt.Errorf("home directory is required for mise configuration")
	}
	// Publish the lock first so an interruption cannot expose a new config
	// without its matching lock. A lock without the managed config is inert
	// and the exact-content inspection makes the next apply resumable.
	publications := []struct {
		source      string
		destination string
		content     string
	}{
		{
			source:      "mise.lock",
			destination: filepath.Join(home, ".config", "mise", "config.lock"),
			content:     mise.Lock,
		},
		{
			source:      "mise.toml",
			destination: filepath.Join(home, ".config", "mise", "config.toml"),
			content:     mise.Config,
		},
	}
	for _, publication := range publications {
		if publication.content == "" {
			return fmt.Errorf("reviewed %s content is required", publication.source)
		}
		inspection := managedfile.Inspect(
			publication.destination,
			publication.content,
		)
		if inspection.State == managedfile.StateConflict {
			return fmt.Errorf(
				"inspect managed mise %s: %w",
				publication.source,
				&managedfile.ConflictError{
					Kind: inspection.Conflict,
					Err:  inspection.Err,
				},
			)
		}
	}
	for _, publication := range publications {
		if err := managedfile.Publish(
			publication.destination,
			publication.content,
		); err != nil {
			return fmt.Errorf(
				"publish managed mise %s: %w",
				publication.source,
				err,
			)
		}
	}
	return nil
}
