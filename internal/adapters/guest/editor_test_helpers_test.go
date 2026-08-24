package guest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	mdscatalog "github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

var (
	ciNeovimOnce sync.Once
	ciNeovimPath string
	ciNeovimErr  error
)

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
	artifact, exists := entry.Artifacts["linux-amd64"]
	if !exists || artifact.URL == "" || artifact.SHA256 == "" || artifact.Executable == "" ||
		artifact.Format != "tar.gz" {
		return "", fmt.Errorf("reviewed catalog has no usable linux-amd64 Neovim artifact")
	}
	directory, err := os.MkdirTemp("", "mds-ci-neovim-")
	if err != nil {
		return "", fmt.Errorf("create Neovim test directory: %w", err)
	}
	archive := filepath.Join(directory, "nvim-linux-x86_64.tar.gz")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "curl", "--fail", "--location", "--retry", "3",
		"--output", archive, artifact.URL)
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		return "", fmt.Errorf("download locked Neovim: %w: %s", commandErr, output)
	}
	content, err := os.ReadFile(archive)
	if err != nil {
		return "", fmt.Errorf("read locked Neovim archive: %w", err)
	}
	digest := sha256.Sum256(content)
	if actual := hex.EncodeToString(digest[:]); actual != artifact.SHA256 {
		return "", fmt.Errorf("locked Neovim checksum = %s", actual)
	}
	command = exec.CommandContext(ctx, "tar", "--extract", "--gzip", "--file", archive,
		"--directory", directory)
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		return "", fmt.Errorf("extract locked Neovim: %w: %s", commandErr, output)
	}
	nvim := filepath.Join(directory, filepath.FromSlash(artifact.Executable))
	stat, err := os.Stat(nvim)
	if err != nil {
		return "", fmt.Errorf("stat locked Neovim executable: %w", err)
	}
	if stat.Mode()&0o111 == 0 {
		return "", fmt.Errorf("locked Neovim executable is not executable")
	}
	return nvim, nil
}
