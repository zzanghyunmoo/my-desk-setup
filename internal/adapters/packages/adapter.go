package packages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

type Adapter struct {
	Environment  catalog.Environment
	Port         transport.Port
	Vendor       Vendor
	Home         string
	AllowReplace bool
}

func (adapter Adapter) Observe(
	ctx context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	if adapter.Port == nil {
		return adapters.Observation{}, errors.New("package transport port is required")
	}
	component, _, err := adapter.componentAndLock(action.ComponentID)
	if err != nil {
		return adapters.Observation{}, err
	}
	if len(action.Verification) == 0 || len(action.Verification[0]) == 0 {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "verification command is missing",
		}, nil
	}
	command := adapter.verificationCommand(action.Verification[0])
	result, err := adapter.Port.Run(ctx, command)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return adapters.Observation{State: adapters.StateAbsent}, nil
		}
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: err.Error(),
		}, nil
	}
	output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	if action.Version != "manager-owned" && action.Version != "manual" &&
		!strings.Contains(output, action.Version) {
		if adapter.AllowReplace {
			return adapters.Observation{
				State: adapters.StateAbsent,
				Detail: fmt.Sprintf(
					"installed version differs from explicit update target %s",
					action.Version,
				),
			}, nil
		}
		return adapters.Observation{
			State: adapters.StateConflict,
			Detail: fmt.Sprintf(
				"installed version output %q does not contain requested %s",
				output,
				action.Version,
			),
		}, nil
	}
	specs, err := adapter.launcherSpecs(action, component)
	if err != nil {
		return adapters.Observation{}, err
	}
	if launcher := observeLaunchers(specs); launcher.State != adapters.StateReady {
		return launcher, nil
	}
	return adapters.Observation{
		State: adapters.StateReady, InstalledVersion: firstLine(output),
	}, nil
}

func (adapter Adapter) Apply(
	ctx context.Context,
	action planning.Action,
) error {
	if adapter.Port == nil {
		return errors.New("package transport port is required")
	}
	component, lock, err := adapter.componentAndLock(action.ComponentID)
	if err != nil {
		return err
	}
	specs, err := adapter.launcherSpecs(action, component)
	if err != nil {
		return err
	}
	if launcher := observeLaunchers(specs); launcher.State == adapters.StateConflict {
		return errors.New(launcher.Detail)
	}
	switch action.Installer {
	case "brew":
		command, err := HomebrewInstall(action)
		if err != nil {
			return err
		}
		err = adapter.run(ctx, command)
	case "winget":
		command, err := WinGetInstall(action)
		if err != nil {
			return err
		}
		err = adapter.run(ctx, command)
	case "apt":
		commands, err := APTInstall(action)
		if err != nil {
			return err
		}
		err = adapter.runAll(ctx, commands)
	case "mise":
		commands, err := MiseInstall(action, adapter.environment())
		if err != nil {
			return err
		}
		err = adapter.runAll(ctx, commands)
	case "bun":
		command, err := BunInstall(action, adapter.environment())
		if err != nil {
			return err
		}
		err = adapter.run(ctx, command)
	case "vendor":
		adapter.Vendor.Home = adapter.Home
		err = adapter.Vendor.Install(ctx, component, lock)
	default:
		return fmt.Errorf("installer %q is not implemented for %s", action.Installer, action.ID)
	}
	if err != nil {
		return err
	}
	return publishLaunchers(specs)
}

func (adapter Adapter) Verify(
	ctx context.Context,
	action planning.Action,
) error {
	if adapter.Port == nil {
		return errors.New("package transport port is required")
	}
	for _, argv := range action.Verification {
		if len(argv) == 0 {
			return fmt.Errorf("empty verification command for %s", action.ID)
		}
		if _, err := adapter.Port.Run(ctx, adapter.verificationCommand(argv)); err != nil {
			return fmt.Errorf("verify %s with %s: %w", action.ID, argv[0], err)
		}
	}
	return nil
}

func (adapter Adapter) componentAndLock(
	componentID string,
) (catalog.Component, catalog.LockEntry, error) {
	for _, component := range adapter.Environment.Catalog.Components {
		if component.ID != componentID {
			continue
		}
		if component.VersionPolicy.Mode != "pinned" {
			return component, catalog.LockEntry{}, nil
		}
		lock := adapter.Environment.Lock.Versions[component.VersionPolicy.LockKey]
		return component, lock, nil
	}
	return catalog.Component{}, catalog.LockEntry{}, fmt.Errorf("unknown component %q", componentID)
}

func (adapter Adapter) run(ctx context.Context, command transport.Command) error {
	_, err := adapter.Port.Run(ctx, command)
	return err
}

func (adapter Adapter) runAll(ctx context.Context, commands []transport.Command) error {
	for _, command := range commands {
		if err := adapter.run(ctx, command); err != nil {
			return err
		}
	}
	return nil
}

func (adapter Adapter) verificationCommand(argv []string) transport.Command {
	return transport.Command{
		Executable:  argv[0],
		Arguments:   append([]string(nil), argv[1:]...),
		Environment: adapter.environment(),
	}
}

func (adapter Adapter) environment() map[string]string {
	localBin := filepath.Join(adapter.Home, ".local", "bin")
	bunHome := filepath.Join(adapter.Home, ".local", "share", "bun")
	miseHome := filepath.Join(adapter.Home, ".local", "share", "mise")
	path := strings.Join([]string{
		localBin,
		filepath.Join(bunHome, "bin"),
		filepath.Join(miseHome, "shims"),
		os.Getenv("PATH"),
	}, string(os.PathListSeparator))
	return map[string]string{
		"PATH":                        path,
		"BUN_INSTALL":                 bunHome,
		"MISE_DATA_DIR":               miseHome,
		"MISE_CONFIG_DIR":             filepath.Join(adapter.Home, ".config", "mise"),
		"MISE_STATE_DIR":              filepath.Join(adapter.Home, ".local", "state", "mise"),
		"MISE_CACHE_DIR":              filepath.Join(adapter.Home, ".cache", "mise"),
		"DISABLE_AUTOUPDATER":         "1",
		"OPENCODE_DISABLE_AUTOUPDATE": "1",
	}
}

func firstLine(value string) string {
	if newline := strings.IndexByte(value, '\n'); newline >= 0 {
		return strings.TrimSpace(value[:newline])
	}
	return strings.TrimSpace(value)
}
