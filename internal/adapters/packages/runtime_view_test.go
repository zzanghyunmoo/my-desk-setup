package packages

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
)

func TestRuntimeViewPublishesShortImmutableNeovimTree(t *testing.T) {
	manager, component, lock := runtimeTreeFixture(t, false)
	component.ID = "neovim"
	if err := manager.Apply(context.Background(), component, lock); err != nil {
		t.Fatalf("Apply(runtime tree): %v", err)
	}
	if err := manager.ApplyRuntimeView(context.Background(), component, lock); err != nil {
		t.Fatalf("ApplyRuntimeView(): %v", err)
	}
	observation, launcher, err := manager.ObserveRuntimeView(component, lock)
	if err != nil || observation.State != adapters.StateReady {
		t.Fatalf("ObserveRuntimeView() = %+v, %q, %v", observation, launcher, err)
	}
	if strings.Contains(launcher, "runtime-trees") {
		t.Fatalf("runtime view launcher retained the long identity path: %s", launcher)
	}
	if runtime.GOOS != "windows" {
		output, err := exec.Command(launcher).CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) != "exact" {
			t.Fatalf("runtime view launcher output = %q, %v", output, err)
		}
	}
	if info, err := os.Stat(launcher); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("runtime view launcher mode = %v, %v; want read-only", info, err)
	}
}

func TestRuntimeViewRejectsUserOwnedDestination(t *testing.T) {
	manager, component, lock := runtimeTreeFixture(t, false)
	component.ID = "neovim"
	if err := manager.Apply(context.Background(), component, lock); err != nil {
		t.Fatalf("Apply(runtime tree): %v", err)
	}
	destination, err := manager.runtimeViewDestination(component, lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(destination, "user-owned")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyRuntimeView(context.Background(), component, lock); err == nil {
		t.Fatal("ApplyRuntimeView() adopted an unmarked destination")
	}
	if content, _ := os.ReadFile(sentinel); string(content) != "keep" {
		t.Fatalf("user-owned content changed: %q", content)
	}
}
