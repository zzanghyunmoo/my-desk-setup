package packages

import (
	"context"
	"encoding/json"
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
	RuntimeTrees RuntimeTreeManager
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
	component, lock, err := adapter.componentAndLock(action.ComponentID)
	if err != nil {
		return adapters.Observation{}, err
	}
	runtimeTree, hasRuntimeTree := adapter.runtimeTree(lock)
	if hasRuntimeTree {
		observation, _, err := runtimeTree.Observe(component, lock)
		if err != nil || observation.State != adapters.StateReady {
			return observation, err
		}
		viewLauncher := ""
		if requiresRuntimeView(component) {
			viewObservation, launcher, err := runtimeTree.ObserveRuntimeView(component, lock)
			if err != nil || viewObservation.State != adapters.StateReady {
				return viewObservation, err
			}
			viewLauncher = launcher
		}
		specs, err := adapter.runtimeTreeLauncherSpecs(action, component, lock, runtimeTree, viewLauncher)
		if err != nil {
			return adapters.Observation{}, err
		}
		if launcher := observeLaunchers(specs); launcher.State != adapters.StateReady {
			return launcher, nil
		}
		// The immutable tree identity is the authoritative installed version.
		// Executing a launcher belongs to Verify: protocol servers may not expose
		// a version flag and Observe must remain a side-effect-free state probe.
		return observation, nil
	} else {
		specs, err := adapter.launcherSpecs(action, component)
		if err != nil {
			return adapters.Observation{}, err
		}
		if launcher := observeLaunchers(specs); launcher.State != adapters.StateReady {
			return launcher, nil
		}
	}
	if len(action.Verification) == 0 || len(action.Verification[0]) == 0 {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "verification command is missing",
		}, nil
	}
	if !hasRuntimeTree && action.Installer == "vendor" && component.VersionPolicy.Mode == "pinned" {
		path := filepath.Join(adapter.Home, ".local", "bin", action.Verification[0][0])
		if adapter.Vendor.Platform == "windows" {
			path += ".exe"
		}
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
	runtimeTree, hasRuntimeTree := adapter.runtimeTree(lock)
	if hasRuntimeTree {
		if err := runtimeTree.Apply(ctx, component, lock); err != nil {
			return err
		}
		if err := runtimeTree.ApplyRuntimeView(ctx, component, lock); err != nil {
			return err
		}
		viewLauncher := ""
		if requiresRuntimeView(component) {
			viewObservation, launcher, err := runtimeTree.ObserveRuntimeView(component, lock)
			if err != nil {
				return err
			}
			if viewObservation.State != adapters.StateReady {
				return errors.New("runtime view is not ready: " + viewObservation.Detail)
			}
			viewLauncher = launcher
		}
		specs, err := adapter.runtimeTreeLauncherSpecs(action, component, lock, runtimeTree, viewLauncher)
		if err != nil {
			return err
		}
		return publishLaunchers(specs)
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
		commands := make([]transport.Command, 0, 2)
		if adapter.bunGlobalDependencyExists(action.Package) {
			commands = append(commands, BunRemove(action.Package, adapter.environment()))
		}
		commands = append(commands, command)
		err = errors.Join(adapter.runAll(ctx, commands), cleanup())
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

func (adapter Adapter) bunGlobalDependencyExists(packageName string) bool {
	manifestPath := filepath.Join(
		adapter.Home,
		".local", "share", "bun", "install", "global", "package.json",
	)
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return false
	}
	var manifest struct {
		Dependencies map[string]json.RawMessage `json:"dependencies"`
	}
	if json.Unmarshal(content, &manifest) != nil {
		return false
	}
	_, exists := manifest.Dependencies[packageName]
	return exists
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
	runtimeLauncher := ""
	runtimeTree, component, lock, hasRuntimeTree, err := adapter.runtimeTreeForVerification(
		action.ComponentID,
	)
	if err != nil {
		return err
	}
	if hasRuntimeTree {
		observation, launcher, err := runtimeTree.Observe(component, lock)
		if err != nil {
			return err
		}
		if observation.State != adapters.StateReady {
			return errors.New("runtime tree is not ready: " + observation.Detail)
		}
		viewLauncher := ""
		if requiresRuntimeView(component) {
			viewObservation, candidate, err := runtimeTree.ObserveRuntimeView(component, lock)
			if err != nil {
				return err
			}
			if viewObservation.State != adapters.StateReady {
				return errors.New("runtime view is not ready: " + viewObservation.Detail)
			}
			viewLauncher = candidate
			launcher = candidate
		}
		specs, err := adapter.runtimeTreeLauncherSpecs(action, component, lock, runtimeTree, viewLauncher)
		if err != nil {
			return err
		}
		if launcherObservation := observeLaunchers(specs); launcherObservation.State != adapters.StateReady {
			return errors.New("runtime tree launcher is not ready: " + launcherObservation.Detail)
		}
		runtimeLauncher = launcher
		if runtimeTreeIsResource(lock, runtimeTree.Platform, runtimeTree.Arch) {
			return nil
		}
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
		if runtimeLauncher != "" && runtimeTreeLauncherMatchesCommand(
			lock, runtimeTree.Platform, runtimeTree.Arch, argv[0],
		) {
			command.Executable = runtimeLauncher
		}
		if _, err := adapter.execute(ctx, command); err != nil {
			return fmt.Errorf("verify %s with %s: %w", action.ID, argv[0], err)
		}
	}
	return adapter.verifyFunctionalToolchain(ctx, action)
}

// runtimeTreeForVerification preserves Verify's existing ability to execute
// standalone bounded actions that are not backed by a catalog component. A
// catalog-owned component still fails closed if its pinned lock is invalid.
func (adapter Adapter) runtimeTreeForVerification(
	componentID string,
) (RuntimeTreeManager, catalog.Component, catalog.LockEntry, bool, error) {
	for _, candidate := range adapter.Environment.Catalog.Components {
		if candidate.ID != componentID {
			continue
		}
		component, lock, err := adapter.componentAndLock(componentID)
		if err != nil {
			return RuntimeTreeManager{}, catalog.Component{}, catalog.LockEntry{}, false, err
		}
		manager, ok := adapter.runtimeTree(lock)
		return manager, component, lock, ok, nil
	}
	return RuntimeTreeManager{}, catalog.Component{}, catalog.LockEntry{}, false, nil
}

func (adapter Adapter) runtimeTree(lock catalog.LockEntry) (RuntimeTreeManager, bool) {
	key := adapter.Vendor.Platform + "-" + adapter.Vendor.Arch
	artifactValue, exists := lock.Artifacts[key]
	if !exists || artifactValue.Tree == nil {
		return RuntimeTreeManager{}, false
	}
	manager := adapter.RuntimeTrees
	manager.Home = adapter.Home
	manager.Platform = adapter.Vendor.Platform
	manager.Arch = adapter.Vendor.Arch
	if manager.Snapshotter.Client == nil {
		manager.Snapshotter.Client = adapter.Vendor.Client
	}
	return manager, true
}

func runtimeTreeIsResource(lock catalog.LockEntry, platform, arch string) bool {
	value, exists := lock.Artifacts[platform+"-"+arch]
	return exists && value.Tree != nil && value.Tree.Usage == "resource"
}

func runtimeTreeLauncherMatchesCommand(
	lock catalog.LockEntry,
	platform,
	arch,
	executable string,
) bool {
	value, exists := lock.Artifacts[platform+"-"+arch]
	return exists && value.Tree != nil &&
		filepath.Base(filepath.FromSlash(value.Executable)) == executable
}

func (adapter Adapter) commandWithManagedLauncher(
	action planning.Action,
	command transport.Command,
) transport.Command {
	if action.Version == "manager-owned" || action.Version == "manual" || adapter.Home == "" {
		return command
	}
	path := filepath.Join(adapter.Home, ".local", "bin", command.Executable)
	if adapter.Vendor.Platform == "windows" {
		path += ".exe"
	}
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
