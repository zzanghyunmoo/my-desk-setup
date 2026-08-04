package cli

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/harness"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func buildPlan(
	ctx context.Context,
	environment catalog.Environment,
	facts target.Facts,
	selection planning.Selection,
	system Runtime,
) (planning.Plan, error) {
	base, err := planning.Build(environment, facts, selection)
	if err != nil {
		return planning.Plan{}, err
	}
	if !planSelectsComponent(base, "oh-my-harness") {
		return base, nil
	}
	home, err := runtimeHome(system)
	if err != nil {
		return planning.Plan{}, err
	}
	tempRoot := runtimeTemporaryRoot(system)
	acquirer := system.HarnessAcquirer
	if acquirer == nil {
		acquirer = planning.ArtifactSnapshotAcquirer{
			Snapshotter: artifact.Snapshotter{TempRoot: tempRoot},
		}
	}
	previewer := system.HarnessPreviewer
	if previewer == nil {
		previewer = harness.Runner{Port: transport.NewLocal()}
	}
	locale := runtimeEnvironment(system, "LC_ALL")
	if locale == "" {
		locale = runtimeEnvironment(system, "LANG")
	}
	return (planning.Composer{
		Acquirer: acquirer, Previewer: previewer,
		Home: home, ConfigRoot: runtimeConfigRoot(home, system),
		TempRoot:  tempRoot,
		StateRoot: filepath.Join(defaultStateRoot(home, system), "oh-my-harness"),
		Locale:    locale, SystemRoot: runtimeEnvironment(system, "SystemRoot"),
		ComSpec:      runtimeEnvironment(system, "ComSpec"),
		AppData:      runtimeEnvironment(system, "APPDATA"),
		LocalAppData: runtimeEnvironment(system, "LOCALAPPDATA"),
		PathExt:      runtimeEnvironment(system, "PATHEXT"),
		Timeout:      45 * time.Second,
	}).Compose(ctx, environment, base)
}

func runtimeEnvironment(system Runtime, key string) string {
	if system.Getenv == nil {
		return ""
	}
	return system.Getenv(key)
}

func runtimeConfigRoot(home string, system Runtime) string {
	if configured := runtimeEnvironment(system, "XDG_CONFIG_HOME"); filepath.IsAbs(configured) {
		return configured
	}
	if system.GOOS == "windows" {
		if configured := runtimeEnvironment(system, "APPDATA"); filepath.IsAbs(configured) {
			return configured
		}
	}
	return filepath.Join(home, ".config")
}

func runtimeTemporaryRoot(system Runtime) string {
	for _, key := range []string{"TMPDIR", "TEMP", "TMP"} {
		if value := runtimeEnvironment(system, key); filepath.IsAbs(value) {
			return value
		}
	}
	return os.TempDir()
}
