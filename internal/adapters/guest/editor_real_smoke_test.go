package guest

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestManagedNeovimRealPluginGraph(t *testing.T) {
	if os.Getenv("MDS_REAL_NVIM_SMOKE") != "1" {
		t.Skip("set MDS_REAL_NVIM_SMOKE=1 to run the networked Neovim plugin smoke test")
	}
	for _, executable := range []string{"git", "nvim"} {
		if _, err := exec.LookPath(executable); err != nil {
			t.Skipf("%s is unavailable: %v", executable, err)
		}
	}
	home, err := os.MkdirTemp("/tmp", "mds-nvim-smoke-")
	if err != nil {
		t.Fatalf("create short smoke-test home: %v", err)
	}
	defer os.RemoveAll(home)
	port := transport.NewLocal()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	editorAction := planning.Action{
		ID:          "lima-guest:mds/nvchad",
		ComponentID: "nvchad",
		Version:     "e3572e1f5e1c297212c3deeb17b7863139ce663e",
	}
	editor := Editor{
		Home: home, Port: port, Now: time.Now,
	}
	if err := editor.Apply(ctx, editorAction); err != nil {
		t.Fatalf("Editor Apply(): %v", err)
	}
	if err := editor.Verify(ctx, editorAction); err != nil {
		t.Fatalf("Editor Verify(): %v", err)
	}
	ideAction := planning.Action{
		ID:          "lima-guest:mds/nvim-ide-tools",
		ComponentID: "nvim-ide-tools",
		Version:     "manager-owned",
	}
	ide := IDE{Home: home, Port: port, Delegate: realSmokeReadyComponent{}}
	if err := ide.Apply(ctx, ideAction); err != nil {
		t.Fatalf("IDE Apply(): %v", err)
	}
	if err := ide.Verify(ctx, ideAction); err != nil {
		t.Fatalf("IDE Verify(): %v", err)
	}
}

type realSmokeReadyComponent struct{}

func (realSmokeReadyComponent) Observe(
	context.Context,
	planning.Action,
) (adapters.Observation, error) {
	return adapters.Observation{State: adapters.StateReady}, nil
}

func (realSmokeReadyComponent) Apply(context.Context, planning.Action) error { return nil }

func (realSmokeReadyComponent) Verify(context.Context, planning.Action) error { return nil }
