package guest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

// IDE owns only the generated language-tool configuration. The NvChad
// starter remains owned by Editor so selecting nvchad alone does not publish
// references to tools that were not selected.
type IDE struct {
	Home     string
	Port     transport.Port
	Delegate adapters.Component
}

func (ide IDE) Observe(
	ctx context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	if ide.Delegate == nil {
		return adapters.Observation{}, errors.New("IDE package delegate is required")
	}
	observation, err := ide.Delegate.Observe(ctx, action)
	if err != nil || observation.State != adapters.StateReady {
		return observation, err
	}
	ready, detail, err := inspectIDEConfiguration(ide.Home)
	if err != nil {
		return adapters.Observation{}, err
	}
	if !ready {
		return adapters.Observation{
			State: adapters.StateAbsent, Detail: detail,
			InstalledVersion: observation.InstalledVersion,
		}, nil
	}
	ready, detail, err = inspectPluginRuntime(ctx, ide.Home, ide.Port, idePluginSet)
	if err != nil {
		return adapters.Observation{}, err
	}
	if !ready {
		return adapters.Observation{
			State: adapters.StateAbsent, Detail: detail,
			InstalledVersion: observation.InstalledVersion,
		}, nil
	}
	return observation, nil
}

func (ide IDE) Apply(ctx context.Context, action planning.Action) error {
	if ide.Delegate == nil {
		return errors.New("IDE package delegate is required")
	}
	observation, err := ide.Delegate.Observe(ctx, action)
	if err != nil {
		return err
	}
	switch observation.State {
	case adapters.StateReady:
		if err := ide.Delegate.Verify(ctx, action); err != nil {
			if err := ide.Delegate.Apply(ctx, action); err != nil {
				return fmt.Errorf("repair incomplete IDE package dependencies: %w", err)
			}
		}
	case adapters.StateAbsent:
		if err := ide.Delegate.Apply(ctx, action); err != nil {
			return err
		}
	default:
		return fmt.Errorf("IDE package dependencies are not repairable: %s", observation.Detail)
	}
	root, err := managedEditorRoot(ide.Home)
	if err != nil {
		return err
	}
	if err := writeIDEConfiguration(root); err != nil {
		return err
	}
	return preparePluginRuntime(ctx, ide.Home, ide.Port, idePluginSet)
}

func (ide IDE) Verify(ctx context.Context, action planning.Action) error {
	if ide.Delegate == nil {
		return errors.New("IDE package delegate is required")
	}
	if err := ide.Delegate.Verify(ctx, action); err != nil {
		return err
	}
	ready, detail, err := inspectIDEConfiguration(ide.Home)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("managed IDE configuration is not ready: %s", detail)
	}
	return verifyManagedNeovim(ctx, ide.Home, ide.Port, idePluginSet)
}

func inspectIDEConfiguration(home string) (bool, string, error) {
	root, err := managedEditorRoot(home)
	if errors.Is(err, os.ErrNotExist) {
		return false, "managed NvChad starter is missing", nil
	}
	if err != nil {
		return false, "", err
	}
	includeIDE, ready, detail, err := inspectEditorConfiguration(root)
	if err != nil || !ready {
		return false, detail, err
	}
	if !includeIDE {
		return false, "managed IDE plugin specification is missing", nil
	}
	for relativePath, expected := range ideConfiguration {
		if relativePath == "lua/plugins/init.lua" {
			continue
		}
		ready, detail, err := inspectConfigurationFile(root, relativePath, expected)
		if err != nil || !ready {
			return false, detail, err
		}
	}
	return true, "", nil
}

func managedEditorRoot(home string) (string, error) {
	root := filepath.Join(home, ".config", "nvim")
	parentExists, err := inspectDirectoryBelow(home, filepath.Dir(root))
	if err != nil {
		return "", err
	}
	if !parentExists {
		return "", os.ErrNotExist
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("inspect managed Neovim config: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("managed Neovim config is not a regular directory")
	}
	markerPath := filepath.Join(root, ".mds-managed.json")
	markerInfo, err := os.Lstat(markerPath)
	if err != nil {
		return "", fmt.Errorf("inspect Neovim ownership marker: %w", err)
	}
	if markerInfo.Mode()&os.ModeSymlink != 0 || !markerInfo.Mode().IsRegular() {
		return "", errors.New("Neovim ownership marker is not a regular file")
	}
	content, err := os.ReadFile(markerPath)
	if err != nil {
		return "", fmt.Errorf("read Neovim ownership marker: %w", err)
	}
	var marker editorMarker
	if json.Unmarshal(content, &marker) != nil ||
		marker.SchemaVersion != editorOwnershipSchema ||
		marker.ComponentID != "nvchad" {
		return "", errors.New("Neovim configuration is not owned by mds NvChad")
	}
	return root, nil
}
