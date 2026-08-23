package packages

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/managedfile"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

type launcherSpec struct {
	path        string
	content     string
	legacyOwned func(string) bool
}

func (adapter Adapter) runtimeTreeLauncherSpecs(
	action planning.Action,
	component catalog.Component,
	lock catalog.LockEntry,
	manager RuntimeTreeManager,
	runtimeViewLauncher string,
) ([]launcherSpec, error) {
	artifactValue, identity, err := manager.runtimeTreeArtifact(lock)
	if err != nil {
		return nil, err
	}
	if runtimeTreeUsage(identity) != "direct" || manager.Platform == "windows" {
		return nil, nil
	}
	name := filepath.Base(filepath.FromSlash(artifactValue.Executable))
	if !adapters.ValidExecutableName(name) {
		return nil, fmt.Errorf("invalid runtime tree executable %q", name)
	}
	publish := false
	for _, argv := range action.Verification {
		if len(argv) > 0 && argv[0] == name {
			publish = true
			break
		}
	}
	if !publish {
		return nil, nil
	}
	root := manager.destinationFor(component.ID, artifactValue)
	dynamicContent := runtimeTreeDynamicLauncher(root, artifactValue.Executable)
	var content string
	if requiresRuntimeView(component) {
		if runtimeViewLauncher == "" || !filepath.IsAbs(runtimeViewLauncher) {
			return nil, errors.New("runtime view launcher is required")
		}
		content = strings.Join([]string{
			"#!/bin/sh",
			"# Managed by my-desk-setup.",
			"exec " + adapters.ShellSingleQuote(runtimeViewLauncher) + ` "$@"`,
			"",
		}, "\n")
	} else {
		content = dynamicContent
	}
	spec := launcherSpec{
		path:    filepath.Join(adapter.Home, ".local", "bin", name),
		content: content,
	}
	if component.ID == "neovim" && name == "nvim" {
		spec.legacyOwned = func(existing string) bool {
			return legacyNeovimLauncher(existing, adapter.Home) || existing == dynamicContent
		}
	}
	return []launcherSpec{spec}, nil
}

func runtimeTreeDynamicLauncher(root, executable string) string {
	return strings.Join([]string{
		"#!/bin/sh",
		"# Managed by my-desk-setup.",
		"root=" + adapters.ShellSingleQuote(root),
		`IFS= read -r generation < "$root/.mds-runtime-tree-current" || exit 126`,
		`case "$generation" in g-[A-Za-z0-9]*) ;; *) exit 126 ;; esac`,
		`case "$generation" in *[!A-Za-z0-9-]*) exit 126 ;; esac`,
		`exec "$root/generations/$generation/payload/` + executable + `" "$@"`,
		"",
	}, "\n")
}

func legacyNeovimLauncher(content, home string) bool {
	const prefix = "#!/bin/sh\nexec \""
	const suffix = "\" \"$@\"\n"
	if !strings.HasPrefix(content, prefix) || !strings.HasSuffix(content, suffix) {
		return false
	}
	executable := strings.TrimSuffix(strings.TrimPrefix(content, prefix), suffix)
	root := filepath.Join(home, ".local", "share", "mds", "neovim")
	separator := string(os.PathSeparator)
	pathPrefix := root + separator
	pathSuffix := separator + "bin" + separator + "nvim"
	if !strings.HasPrefix(executable, pathPrefix) ||
		!strings.HasSuffix(executable, pathSuffix) {
		return false
	}
	version := strings.TrimSuffix(strings.TrimPrefix(executable, pathPrefix), pathSuffix)
	if version == "" || strings.ContainsAny(version, "/\\\x00\n\r\"'") {
		return false
	}
	hasDigit := false
	for _, value := range version {
		if value >= '0' && value <= '9' {
			hasDigit = true
			continue
		}
		if value != '.' && value != '-' && value != '+' {
			return false
		}
	}
	return hasDigit && executable == filepath.Join(root, version, "bin", "nvim")
}

func (adapter Adapter) launcherSpecs(
	action planning.Action,
	component catalog.Component,
) ([]launcherSpec, error) {
	if component.Kind == "agent" ||
		(action.Installer != "bun" && action.Installer != "mise") {
		return nil, nil
	}
	if adapter.Home == "" {
		return nil, errors.New("home directory is required for managed launchers")
	}
	seen := make(map[string]bool)
	var specs []launcherSpec
	for _, argv := range action.Verification {
		if len(argv) == 0 {
			continue
		}
		executable := argv[0]
		if executable == "bun" || executable == "mise" || seen[executable] {
			continue
		}
		if !adapters.ValidExecutableName(executable) {
			return nil, fmt.Errorf("invalid managed executable %q", executable)
		}
		seen[executable] = true
		var source string
		switch action.Installer {
		case "bun":
			source = filepath.Join(
				adapter.Home,
				".local", "share", "bun", "bin", executable,
			)
		case "mise":
			source = filepath.Join(
				adapter.Home,
				".local", "share", "mise", "shims", executable,
			)
		}
		spec := launcherSpec{
			path:    filepath.Join(adapter.Home, ".local", "bin", executable),
			content: bunAwareLauncherContent(adapter.Home, component.ID, action.Version, source),
		}
		if component.ID == "vscode-html-language-server" {
			spec.legacyOwned = func(existing string) bool {
				return legacyHTMLRuntimeLauncher(existing, adapter.Home)
			}
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func legacyHTMLRuntimeLauncher(content, home string) bool {
	const identity = "cdd36db14f9e3e891aac10835756f66dbf232d3f9f454314b680e088aed42c54-" +
		"d6e2d090d09c4b91daa74e9e7462a3d3f244efb96aa5111004cfffa49d6dc9ef"
	root := filepath.Join(
		home, ".local", "share", "mds", "runtime-trees",
		"vscode-html-language-server", identity,
	)
	return content == runtimeTreeDynamicLauncher(
		root,
		"package/bin/vscode-html-language-server",
	)
}

func bunAwareLauncherContent(home, componentID, version, source string) string {
	arguments := adapters.ShellSingleQuote(source) + ` "$@"`
	prefix := []string{"#!/bin/sh", "# Managed by my-desk-setup."}
	if componentID == "vscode-html-language-server" {
		arguments = adapters.ShellSingleQuote(
			filepath.Join(home, ".local", "bin", "bun"),
		) + " " + arguments
		prefix = append(prefix,
			`if [ "$#" -eq 1 ] && [ "$1" = "--version" ]; then`,
			"  printf '%s\\n' "+adapters.ShellSingleQuote(version),
			"  exit 0",
			"fi",
		)
	}
	return strings.Join(append(prefix, "exec "+arguments, ""), "\n")
}

func observeLaunchers(specs []launcherSpec) adapters.Observation {
	for _, spec := range specs {
		inspection := managedfile.Inspect(spec.path, spec.content)
		switch inspection.State {
		case managedfile.StateMissing:
			return adapters.Observation{State: adapters.StateAbsent}
		case managedfile.StateConflict:
			if spec.legacyOwned != nil {
				content, err := os.ReadFile(spec.path)
				if err == nil && spec.legacyOwned(string(content)) {
					return adapters.Observation{State: adapters.StateAbsent}
				}
			}
			return adapters.Observation{
				State: adapters.StateConflict,
				Detail: managedLauncherConflictDetail(
					inspection.Conflict,
					inspection.Err,
				),
			}
		}
	}
	return adapters.Observation{State: adapters.StateReady}
}

func publishLaunchers(specs []launcherSpec) error {
	for _, spec := range specs {
		inspection := managedfile.Inspect(spec.path, spec.content)
		if inspection.State == managedfile.StateConflict && spec.legacyOwned != nil {
			content, err := os.ReadFile(spec.path)
			if err == nil && spec.legacyOwned(string(content)) {
				if err := durable.WriteFile(spec.path, []byte(spec.content), 0o700); err != nil {
					return fmt.Errorf("replace legacy managed launcher: %w", err)
				}
				continue
			}
		}
		if err := managedfile.Publish(spec.path, spec.content); err != nil {
			var conflict *managedfile.ConflictError
			if errors.As(err, &conflict) {
				return errors.New(
					managedLauncherConflictDetail(conflict.Kind, conflict.Err),
				)
			}
			return fmt.Errorf("publish launcher without overwrite: %w", err)
		}
	}
	return nil
}

func managedLauncherConflictDetail(
	conflict managedfile.ConflictKind,
	err error,
) string {
	switch conflict {
	case managedfile.ConflictInspect:
		return "inspect managed launcher: " + err.Error()
	case managedfile.ConflictNonRegular:
		return "managed launcher destination is not a regular file"
	default:
		return "existing launcher is user-owned; it will not be overwritten"
	}
}
