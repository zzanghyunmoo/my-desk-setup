package packages

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func TestRuntimeTreeLauncherTracksTheCurrentExactGeneration(t *testing.T) {
	manager, component, lock := runtimeTreeFixture(t, false)
	if err := manager.Apply(context.Background(), component, lock); err != nil {
		t.Fatalf("Apply(runtime tree): %v", err)
	}
	adapter := Adapter{Home: manager.Home}
	specs, err := adapter.runtimeTreeLauncherSpecs(planning.Action{
		Verification: [][]string{{"tool", "--version"}},
	}, component, lock, manager, "")
	if err != nil || len(specs) != 1 {
		t.Fatalf("runtimeTreeLauncherSpecs() = %+v, %v", specs, err)
	}
	if err := publishLaunchers(specs); err != nil {
		t.Fatalf("publishLaunchers(): %v", err)
	}
	if observation := observeLaunchers(specs); observation.State != adapters.StateReady {
		t.Fatalf("observeLaunchers() = %+v, want ready", observation)
	}
	output, err := exec.Command(specs[0].path).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "exact" {
		t.Fatalf("stable runtime launcher output = %q, %v", output, err)
	}
}

func TestHTMLLanguageServerLauncherUsesManagedBun(t *testing.T) {
	home := t.TempDir()
	content := bunAwareLauncherContent(
		home,
		"vscode-html-language-server",
		"4.10.0",
		filepath.Join(home, ".local", "share", "bun", "bin", "vscode-html-language-server"),
	)
	if !strings.Contains(content, filepath.Join(home, ".local", "bin", "bun")) ||
		!strings.Contains(content, "vscode-html-language-server") ||
		!strings.Contains(content, "4.10.0") {
		t.Fatalf("HTML launcher does not bind the managed Bun runtime: %q", content)
	}
}

func TestHTMLLanguageServerMigratesOnlyTheExactLegacyRuntimeLauncher(t *testing.T) {
	home := t.TempDir()
	const identity = "cdd36db14f9e3e891aac10835756f66dbf232d3f9f454314b680e088aed42c54-" +
		"d6e2d090d09c4b91daa74e9e7462a3d3f244efb96aa5111004cfffa49d6dc9ef"
	legacy := runtimeTreeDynamicLauncher(
		filepath.Join(home, ".local", "share", "mds", "runtime-trees", "vscode-html-language-server", identity),
		"package/bin/vscode-html-language-server",
	)
	if !legacyHTMLRuntimeLauncher(legacy, home) {
		t.Fatal("exact legacy HTML runtime launcher was not recognized")
	}
	if legacyHTMLRuntimeLauncher(legacy+"# user\n", home) {
		t.Fatal("modified launcher was treated as managed migration input")
	}
}

func TestRuntimeTreeLauncherMigratesOnlyExactLegacyNeovimWrapper(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".local", "bin", "nvim")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := "#!/bin/sh\nexec \"" + filepath.Join(home, ".local", "share", "mds", "neovim", "0.11.5", "bin", "nvim") + "\" \"$@\"\n"
	if err := os.WriteFile(path, []byte(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	spec := launcherSpec{
		path: path, content: "#!/bin/sh\n# Managed by my-desk-setup.\nexit 0\n",
		legacyOwned: func(content string) bool { return legacyNeovimLauncher(content, home) },
	}
	if observation := observeLaunchers([]launcherSpec{spec}); observation.State != adapters.StateAbsent {
		t.Fatalf("legacy observation = %+v, want absent for migration", observation)
	}
	if err := publishLaunchers([]launcherSpec{spec}); err != nil {
		t.Fatalf("publishLaunchers(legacy): %v", err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != spec.content {
		t.Fatalf("migrated launcher = %q, %v", content, err)
	}

	userContent := "#!/bin/sh\necho user-owned\n"
	if err := os.WriteFile(path, []byte(userContent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := publishLaunchers([]launcherSpec{spec}); err == nil {
		t.Fatal("publishLaunchers() overwrote a user-owned launcher")
	}
	if content, _ := os.ReadFile(path); string(content) != userContent {
		t.Fatalf("user-owned launcher changed: %q", content)
	}
}
