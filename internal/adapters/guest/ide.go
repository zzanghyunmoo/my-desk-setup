package guest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

// IDE owns only the generated language-tool configuration. The NvChad
// starter remains owned by Editor so selecting nvchad alone does not publish
// references to tools that were not selected.
type IDE struct {
	Home     string
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
	return observation, nil
}

func (ide IDE) Apply(ctx context.Context, action planning.Action) error {
	if ide.Delegate == nil {
		return errors.New("IDE package delegate is required")
	}
	if err := ide.Delegate.Apply(ctx, action); err != nil {
		return err
	}
	root, err := managedEditorRoot(ide.Home)
	if err != nil {
		return err
	}
	return writeIDEConfiguration(root)
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
	return nil
}

func inspectIDEConfiguration(home string) (bool, string, error) {
	root, err := managedEditorRoot(home)
	if errors.Is(err, os.ErrNotExist) {
		return false, "managed NvChad starter is missing", nil
	}
	if err != nil {
		return false, "", err
	}
	for relativePath, expected := range ideConfiguration {
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, "managed IDE configuration is missing " + relativePath, nil
		}
		if err != nil {
			return false, "", fmt.Errorf("inspect managed IDE configuration %s: %w", relativePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, "", fmt.Errorf("managed IDE configuration %s is not a regular file", relativePath)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return false, "", fmt.Errorf("read managed IDE configuration %s: %w", relativePath, err)
		}
		if !bytes.Equal(content, []byte(expected)) {
			return false, "managed IDE configuration differs at " + relativePath, nil
		}
	}
	return true, "", nil
}

func managedEditorRoot(home string) (string, error) {
	root := filepath.Join(home, ".config", "nvim")
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
		marker.SchemaVersion != "mds.ownership/v1" ||
		marker.ComponentID != "nvchad" {
		return "", errors.New("Neovim configuration is not owned by mds NvChad")
	}
	return root, nil
}
