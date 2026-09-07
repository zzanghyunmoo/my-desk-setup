package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

// VSCodeExtension manages one exact, reviewed host extension without signing in.
type VSCodeExtension struct {
	Platform     string
	Port         transport.Port
	Executable   string
	LocalAppData string
	ProgramFiles string
}

func (extension VSCodeExtension) Observe(ctx context.Context, action planning.Action) (adapters.Observation, error) {
	if extension.Port == nil {
		return adapters.Observation{}, errors.New("VS Code extension adapter requires a port")
	}
	result, err := extension.Port.Run(ctx, transport.Command{
		Executable: extension.executable(),
		Arguments:  []string{"--list-extensions", "--show-versions"},
	})
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return adapters.Observation{State: adapters.StateAbsent}, nil
		}
		return adapters.Observation{State: adapters.StateConflict, Detail: err.Error()}, nil
	}
	wantedID := strings.ToLower(action.Inputs["install_ref"])
	for _, line := range strings.Fields(result.Stdout) {
		id, version, ok := strings.Cut(strings.TrimSpace(line), "@")
		if !ok || strings.ToLower(id) != wantedID {
			continue
		}
		if version == action.Version {
			return adapters.Observation{State: adapters.StateReady, InstalledVersion: version}, nil
		}
		return adapters.Observation{
			State: adapters.StateConflict, InstalledVersion: version,
			Detail: fmt.Sprintf(
				"installed extension version %s differs from reviewed version %s",
				version,
				action.Version,
			),
		}, nil
	}
	return adapters.Observation{State: adapters.StateAbsent}, nil
}

func (extension VSCodeExtension) Apply(ctx context.Context, action planning.Action) error {
	observation, err := extension.Observe(ctx, action)
	if err != nil {
		return err
	}
	if observation.State == adapters.StateReady {
		return nil
	}
	if observation.State == adapters.StateConflict {
		return errors.New(observation.Detail)
	}
	ref := action.Inputs["install_ref"]
	if ref == "" || action.Version == "" {
		return errors.New("VS Code extension action requires exact install_ref and version")
	}
	_, err = extension.Port.Run(ctx, transport.Command{
		Executable: extension.executable(),
		Arguments:  []string{"--install-extension", ref + "@" + action.Version, "--force"},
	})
	return err
}

func (extension VSCodeExtension) Verify(ctx context.Context, action planning.Action) error {
	observation, err := extension.Observe(ctx, action)
	if err != nil {
		return err
	}
	if observation.State != adapters.StateReady {
		return fmt.Errorf("VS Code extension %s is not at reviewed version %s: %s", action.Inputs["install_ref"], action.Version, observation.State)
	}
	return nil
}

func (extension VSCodeExtension) executable() string {
	if extension.Executable != "" {
		return extension.Executable
	}
	var candidates []string
	switch extension.Platform {
	case "darwin":
		candidates = []string{"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"}
	case "windows":
		if extension.LocalAppData != "" {
			candidates = append(candidates, filepath.Join(extension.LocalAppData, "Programs", "Microsoft VS Code", "Code.exe"))
		}
		if extension.ProgramFiles != "" {
			candidates = append(candidates, filepath.Join(extension.ProgramFiles, "Microsoft VS Code", "Code.exe"))
		}
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "code"
}
