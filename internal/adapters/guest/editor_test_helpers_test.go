package guest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	mdsartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	mdscatalog "github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

var (
	ciNeovimOnce sync.Once
	ciNeovimPath string
	ciNeovimErr  error
	ciNeovim     *mdsartifact.Snapshot
)

func TestMain(m *testing.M) {
	code := m.Run()
	if ciNeovim != nil {
		if err := ciNeovim.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "clean up locked CI Neovim: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

func requireHeadlessNeovim(t *testing.T) string {
	t.Helper()
	targetCI := os.Getenv("CI") != "" && runtime.GOOS == "linux" && runtime.GOARCH == "amd64"
	nvim, err := resolveHeadlessNeovim(targetCI, exec.LookPath, func() (string, error) {
		ciNeovimOnce.Do(func() { ciNeovimPath, ciNeovimErr = installCINeovim() })
		return ciNeovimPath, ciNeovimErr
	})
	if err == nil {
		return nvim
	}
	if targetCI {
		t.Fatalf("prepare locked headless Neovim in CI: %v", err)
	}
	t.Skipf("headless Neovim is unavailable: %v", err)
	return ""
}

func resolveHeadlessNeovim(
	targetCI bool,
	lookPath func(string) (string, error),
	installLocked func() (string, error),
) (string, error) {
	if targetCI {
		return installLocked()
	}
	return lookPath("nvim")
}

func TestResolveHeadlessNeovimUsesLockedArtifactInTargetCI(t *testing.T) {
	lookedUp := false
	path, err := resolveHeadlessNeovim(true, func(string) (string, error) {
		lookedUp = true
		return "/unreviewed/nvim", nil
	}, func() (string, error) {
		return "/reviewed/nvim", nil
	})
	if err != nil || path != "/reviewed/nvim" || lookedUp {
		t.Fatalf("resolveHeadlessNeovim() = %q, %v, PATH lookup=%t", path, err, lookedUp)
	}
}

func installCINeovim() (string, error) {
	environment, err := mdscatalog.Load(filepath.Join("..", "..", "..", "catalog"))
	if err != nil {
		return "", fmt.Errorf("load reviewed catalog for Neovim tests: %w", err)
	}
	entry, exists := environment.Lock.Versions["neovim"]
	if !exists {
		return "", fmt.Errorf("reviewed catalog omits Neovim")
	}
	catalogArtifact, exists := entry.Artifacts["linux-amd64"]
	if !exists || catalogArtifact.URL == "" || catalogArtifact.SHA256 == "" ||
		catalogArtifact.Executable == "" || catalogArtifact.Format != "tar.gz" {
		return "", fmt.Errorf("reviewed catalog has no usable linux-amd64 Neovim artifact")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	snapshot, err := (mdsartifact.Snapshotter{}).Acquire(ctx, mdsartifact.SnapshotRequest{
		URL:        catalogArtifact.URL,
		SHA256:     catalogArtifact.SHA256,
		Format:     catalogArtifact.Format,
		Executable: catalogArtifact.Executable,
		ExtractAll: true,
	})
	if err != nil {
		return "", fmt.Errorf("acquire locked Neovim artifact: %w", err)
	}
	ciNeovim = snapshot
	return snapshot.Executable(), nil
}
