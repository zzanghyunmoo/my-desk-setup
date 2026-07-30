package guest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

type Editor struct {
	Home         string
	Port         transport.Port
	Delegate     adapters.Component
	Now          func() time.Time
	AllowReplace bool
}

type editorMarker struct {
	SchemaVersion string    `json:"schema_version"`
	ComponentID   string    `json:"component_id"`
	Revision      string    `json:"revision"`
	InstalledAt   time.Time `json:"installed_at"`
}

func (editor Editor) Observe(
	_ context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	root := filepath.Join(editor.Home, ".config", "nvim")
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return adapters.Observation{State: adapters.StateAbsent}, nil
	}
	if err != nil {
		return adapters.Observation{}, fmt.Errorf("inspect Neovim config: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "existing ~/.config/nvim is not a regular directory",
		}, nil
	}
	markerPath := filepath.Join(root, ".mds-managed.json")
	content, err := os.ReadFile(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "existing ~/.config/nvim is user-owned; it will not be overwritten",
		}, nil
	}
	if err != nil {
		return adapters.Observation{}, fmt.Errorf("read Neovim ownership marker: %w", err)
	}
	var marker editorMarker
	if err := json.Unmarshal(content, &marker); err != nil {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "existing Neovim ownership marker is invalid",
		}, nil
	}
	if marker.ComponentID != action.ComponentID || marker.Revision != action.Version {
		if editor.AllowReplace && marker.ComponentID == action.ComponentID {
			return adapters.Observation{
				State:            adapters.StateAbsent,
				InstalledVersion: marker.Revision,
				Detail:           "managed Neovim config will move through explicit update",
			}, nil
		}
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "managed Neovim config revision differs; use explicit update",
		}, nil
	}
	return adapters.Observation{
		State: adapters.StateReady, InstalledVersion: marker.Revision,
	}, nil
}

func (editor Editor) Apply(
	ctx context.Context,
	action planning.Action,
) (returnErr error) {
	if editor.Home == "" || editor.Port == nil || editor.Now == nil {
		return errors.New("editor adapter requires home, port, and clock")
	}
	configDirectory := filepath.Join(editor.Home, ".config")
	if err := ensureDirectory(configDirectory); err != nil {
		return err
	}
	target := filepath.Join(configDirectory, "nvim")
	replaceExisting := false
	if info, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("inspect existing Neovim config: %w", err)
		}
		if !editor.AllowReplace {
			return errors.New("existing ~/.config/nvim will not be overwritten")
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("existing ~/.config/nvim is not a managed directory")
		}
		markerContent, err := os.ReadFile(filepath.Join(target, ".mds-managed.json"))
		if err != nil {
			return errors.New("existing ~/.config/nvim is not mds-managed")
		}
		var marker editorMarker
		if json.Unmarshal(markerContent, &marker) != nil ||
			marker.ComponentID != action.ComponentID {
			return errors.New("existing ~/.config/nvim is not mds-managed")
		}
		replaceExisting = true
	}
	temporary, err := os.MkdirTemp(configDirectory, ".nvim-mds-*")
	if err != nil {
		return fmt.Errorf("create Neovim config temporary directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(temporary); err != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("remove Neovim config temporary directory: %w", err),
			)
		}
	}()

	commands := []transport.Command{
		{Executable: "git", Arguments: []string{"init", temporary}},
		{
			Executable: "git",
			Arguments: []string{
				"-C", temporary, "remote", "add", "origin",
				"https://github.com/NvChad/starter.git",
			},
		},
		{
			Executable: "git",
			Arguments: []string{
				"-C", temporary, "fetch", "--depth", "1", "origin", action.Version,
			},
		},
		{
			Executable: "git",
			Arguments:  []string{"-C", temporary, "checkout", "--detach", "FETCH_HEAD"},
		},
	}
	for _, command := range commands {
		if _, err := editor.Port.Run(ctx, command); err != nil {
			return fmt.Errorf("prepare NvChad starter: %w", err)
		}
	}
	result, err := editor.Port.Run(ctx, transport.Command{
		Executable: "git",
		Arguments:  []string{"-C", temporary, "rev-parse", "HEAD"},
	})
	if err != nil {
		return fmt.Errorf("verify NvChad revision: %w", err)
	}
	if strings.TrimSpace(result.Stdout) != action.Version {
		return fmt.Errorf(
			"NvChad revision mismatch: expected %s got %s",
			action.Version,
			strings.TrimSpace(result.Stdout),
		)
	}
	if err := os.RemoveAll(filepath.Join(temporary, ".git")); err != nil {
		return fmt.Errorf("remove NvChad transport metadata: %w", err)
	}
	marker := editorMarker{
		SchemaVersion: "mds.ownership/v1",
		ComponentID:   action.ComponentID,
		Revision:      action.Version,
		InstalledAt:   editor.Now().UTC(),
	}
	encoded, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Neovim ownership marker: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(temporary, ".mds-managed.json"),
		append(encoded, '\n'),
		0o600,
	); err != nil {
		return fmt.Errorf("write Neovim ownership marker: %w", err)
	}
	if !replaceExisting {
		if err := os.Rename(temporary, target); err != nil {
			return fmt.Errorf("publish Neovim config: %w", err)
		}
		return nil
	}
	backupFile, err := os.CreateTemp(configDirectory, ".nvim-mds-backup-*")
	if err != nil {
		return fmt.Errorf("reserve Neovim backup path: %w", err)
	}
	backup := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		_ = os.Remove(backup)
		return fmt.Errorf("close Neovim backup reservation: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("release Neovim backup reservation: %w", err)
	}
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("stage managed Neovim config replacement: %w", err)
	}
	if err := os.Rename(temporary, target); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return fmt.Errorf(
				"publish Neovim config: %v; restore previous config: %w",
				err,
				restoreErr,
			)
		}
		return fmt.Errorf("publish Neovim config: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove replaced managed Neovim config: %w", err)
	}
	return nil
}

func (editor Editor) Verify(
	ctx context.Context,
	action planning.Action,
) error {
	observation, err := editor.Observe(ctx, action)
	if err != nil {
		return err
	}
	if observation.State != adapters.StateReady {
		return fmt.Errorf("neovim config is not ready: %s", observation.Detail)
	}
	if editor.Delegate == nil {
		return errors.New("editor verification delegate is required")
	}
	return editor.Delegate.Verify(ctx, action)
}

func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create directory %s: %w", path, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("directory %s must not be a symlink", path)
	}
	return nil
}
