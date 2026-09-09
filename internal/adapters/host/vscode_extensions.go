package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	command, err := extension.command("--list-extensions", "--show-versions")
	if err != nil {
		return adapters.Observation{State: adapters.StateConflict, Detail: err.Error()}, nil
	}
	result, err := extension.Port.Run(ctx, command)
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
	command, err := extension.command("--install-extension", ref+"@"+action.Version, "--force")
	if err != nil {
		return err
	}
	_, err = extension.Port.Run(ctx, command)
	return err
}

var windowsCodeCLI = regexp.MustCompile(`"%~dp0([^"\r\n]*\\resources\\app\\out\\cli\.js)"`)

func (extension VSCodeExtension) command(arguments ...string) (transport.Command, error) {
	command := transport.Command{Executable: extension.executable(), Arguments: arguments}
	if extension.Platform != "windows" || !strings.EqualFold(filepath.Base(command.Executable), "Code.exe") {
		return command, nil
	}
	installation := filepath.Dir(command.Executable)
	launcher, err := os.ReadFile(filepath.Join(installation, "bin", "code.cmd"))
	if err != nil {
		return transport.Command{}, fmt.Errorf("read installed VS Code CLI launcher: %w", err)
	}
	// Follow the vendor launcher: current Windows installs version the CLI folder.
	// Invoke Node directly so neither GUI startup nor cmd.exe interpolation occurs.
	match := windowsCodeCLI.FindStringSubmatch(string(launcher))
	if len(match) != 2 {
		return transport.Command{}, errors.New("installed VS Code launcher has no supported CLI entry point")
	}
	cli := filepath.Join(installation, "bin", strings.ReplaceAll(match[1], `\`, string(filepath.Separator)))
	relative, err := filepath.Rel(installation, cli)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return transport.Command{}, errors.New("VS Code CLI entry point escapes its installation")
	}
	if _, err := os.Stat(cli); err != nil {
		return transport.Command{}, fmt.Errorf("locate installed VS Code CLI entry point: %w", err)
	}
	command.Arguments = append([]string{cli}, arguments...)
	command.Environment = map[string]string{"ELECTRON_RUN_AS_NODE": "1", "VSCODE_DEV": ""}
	return command, nil
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
