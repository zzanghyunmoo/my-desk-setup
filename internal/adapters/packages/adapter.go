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
	specs, err := adapter.launcherSpecs(action, component)
	if err != nil {
		return adapters.Observation{}, err
	}
	if launcher := observeLaunchers(specs); launcher.State != adapters.StateReady {
		return launcher, nil
	}
	if len(action.Verification) == 0 || len(action.Verification[0]) == 0 {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "verification command is missing",
		}, nil
	}
	if action.Installer == "vendor" && component.VersionPolicy.Mode == "pinned" {
		path := filepath.Join(adapter.Home, ".local", "bin", action.Verification[0][0])
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return adapters.Observation{State: adapters.StateAbsent}, nil
		}
	}
	command := adapter.verificationCommand(action.Verification[0])
	if err := ValidateCatalogVerificationCommand(action.ComponentID, command); err != nil {
		return adapters.Observation{
			State: adapters.StateConflict, Detail: err.Error(),
		}, nil
	}
	command = adapter.commandWithManagedLauncher(action, command)
	result, err := adapter.execute(ctx, command)
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
		!containsExactVersion(output, action.Version) {
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
				"installed version output %q does not contain requested exact version %s",
				output,
				action.Version,
			),
		}, nil
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
		command, buildErr := HomebrewInstall(action)
		if buildErr != nil {
			return buildErr
		}
		err = adapter.run(ctx, command)
	case "winget":
		command, buildErr := WinGetInstall(action)
		if buildErr != nil {
			return buildErr
		}
		err = adapter.run(ctx, command)
	case "apt":
		commands, buildErr := APTInstall(action)
		if buildErr != nil {
			return buildErr
		}
		err = adapter.runAll(ctx, commands)
	case "mise":
		if err := validateMiseAction(action, lock); err != nil {
			return err
		}
		if err := PublishMiseConfig(adapter.Home, adapter.Environment.Mise); err != nil {
			return err
		}
		commands, buildErr := MiseInstall(action, adapter.environment())
		if buildErr != nil {
			return buildErr
		}
		err = adapter.runAll(ctx, commands)
	case "bun":
		if lock.NPM == nil {
			return fmt.Errorf(
				"component %s has no reviewed npm tarball",
				action.ComponentID,
			)
		}
		localArtifact, cleanup, downloadErr := adapter.Vendor.downloadNPMTarball(
			ctx,
			*lock.NPM,
		)
		if downloadErr != nil {
			return downloadErr
		}
		command, buildErr := BunInstall(
			action,
			localArtifact,
			adapter.environment(),
		)
		if buildErr != nil {
			return errors.Join(buildErr, cleanup())
		}
		err = errors.Join(adapter.run(ctx, command), cleanup())
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

func validateMiseAction(action planning.Action, lock catalog.LockEntry) error {
	expectedRef := lock.InstallRef
	if expectedRef == "" {
		expectedRef = lock.Version
	}
	if action.Inputs["mise_ref"] != expectedRef {
		return fmt.Errorf(
			"mise action %s ref does not match the reviewed catalog",
			action.ID,
		)
	}
	for _, artifact := range lock.Artifacts {
		if artifact.URL == action.Inputs["artifact_url"] &&
			artifact.SHA256 == action.Inputs["artifact_sha256"] {
			return nil
		}
	}
	return fmt.Errorf(
		"mise action %s artifact does not match the reviewed catalog",
		action.ID,
	)
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
		command := adapter.verificationCommand(argv)
		if err := ValidateCatalogVerificationCommand(
			action.ComponentID,
			command,
		); err != nil {
			return err
		}
		command = adapter.commandWithManagedLauncher(action, command)
		if _, err := adapter.execute(ctx, command); err != nil {
			return fmt.Errorf("verify %s with %s: %w", action.ID, argv[0], err)
		}
	}
	return adapter.verifyFunctionalToolchain(ctx, action)
}

func (adapter Adapter) commandWithManagedLauncher(
	action planning.Action,
	command transport.Command,
) transport.Command {
	if action.Version == "manager-owned" || action.Version == "manual" || adapter.Home == "" {
		return command
	}
	path := filepath.Join(adapter.Home, ".local", "bin", command.Executable)
	info, err := os.Lstat(path)
	if err == nil && info.Mode().IsRegular() {
		command.Executable = path
	}
	return command
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
	_, err := adapter.execute(ctx, command)
	return err
}

func (adapter Adapter) execute(
	ctx context.Context,
	command transport.Command,
) (transport.Result, error) {
	if err := ValidatePrivilegedCommand(command); err != nil {
		return transport.Result{}, err
	}
	return adapter.Port.Run(ctx, command)
}

func (adapter Adapter) runAll(ctx context.Context, commands []transport.Command) error {
	for _, command := range commands {
		if err := adapter.run(ctx, command); err != nil {
			if isSudoPreflight(command) {
				return &adapters.ActionRequiredError{
					Reason: "run `sudo -v` inside the guest, then rerun the same mds apply; mds does not collect sudo credentials",
				}
			}
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
		"HOME":                        adapter.Home,
		"PATH":                        path,
		"BUN_INSTALL":                 bunHome,
		"MISE_DATA_DIR":               miseHome,
		"MISE_CONFIG_DIR":             filepath.Join(adapter.Home, ".config", "mise"),
		"MISE_STATE_DIR":              filepath.Join(adapter.Home, ".local", "state", "mise"),
		"MISE_CACHE_DIR":              filepath.Join(adapter.Home, ".cache", "mise"),
		"GOTOOLCHAIN":                 "local",
		"DISABLE_AUTOUPDATER":         "1",
		"OPENCODE_DISABLE_AUTOUPDATE": "1",
	}
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	return strings.TrimSpace(line)
}

func containsExactVersion(output, version string) bool {
	if version == "" {
		return false
	}
	for searchFrom := 0; searchFrom <= len(output)-len(version); {
		relativeStart := strings.Index(output[searchFrom:], version)
		if relativeStart < 0 {
			return false
		}
		start := searchFrom + relativeStart
		end := start + len(version)
		if versionTokenStart(output, start) &&
			(end == len(output) || !isVersionTokenByte(output[end])) {
			return true
		}
		searchFrom = start + 1
	}
	return false
}

func versionTokenStart(output string, start int) bool {
	if start == 0 || !isVersionTokenByte(output[start-1]) {
		return true
	}
	for _, prefix := range []string{"v", "go"} {
		prefixStart := start - len(prefix)
		if prefixStart >= 0 &&
			output[prefixStart:start] == prefix &&
			(prefixStart == 0 || !isVersionTokenByte(output[prefixStart-1])) {
			return true
		}
	}
	return false
}

func isVersionTokenByte(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		strings.ContainsRune(".+-_", rune(value))
}
