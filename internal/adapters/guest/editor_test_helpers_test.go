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
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCINeovimPinMatchesCatalog(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "catalog", "locks", "versions.lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{ciNeovimURL, "sha256: " + ciNeovimSHA} {
		if !strings.Contains(string(content), expected) {
			t.Fatalf("CI Neovim pin is not present in the reviewed catalog lock: %s", expected)
		}
	}
}

const (
	ciNeovimURL = "https://github.com/neovim/neovim/releases/download/v0.12.4/nvim-linux-x86_64.tar.gz"
	ciNeovimSHA = "012bf3fcac5ade43914df3f174668bf64d05e049a4f032a388c027b1ebd78628"
)

var (
	ciNeovimOnce sync.Once
	ciNeovimPath string
	ciNeovimErr  error
)

func requireHeadlessNeovim(t *testing.T) string {
	t.Helper()
	nvim, err := exec.LookPath("nvim")
	if err == nil {
		return nvim
	}
	if os.Getenv("CI") != "" && runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		ciNeovimOnce.Do(func() { ciNeovimPath, ciNeovimErr = installCINeovim() })
		if ciNeovimErr != nil {
			t.Fatalf("prepare locked headless Neovim in CI: %v", ciNeovimErr)
		}
		return ciNeovimPath
	}
	t.Skipf("headless Neovim is unavailable: %v", err)
	return ""
}

func installCINeovim() (string, error) {
	directory, err := os.MkdirTemp("", "mds-ci-neovim-")
	if err != nil {
		return "", fmt.Errorf("create Neovim test directory: %w", err)
	}
	archive := filepath.Join(directory, "nvim-linux-x86_64.tar.gz")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "curl", "--fail", "--location", "--retry", "3",
		"--output", archive, ciNeovimURL)
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		return "", fmt.Errorf("download locked Neovim: %w: %s", commandErr, output)
	}
	content, err := os.ReadFile(archive)
	if err != nil {
		return "", fmt.Errorf("read locked Neovim archive: %w", err)
	}
	digest := sha256.Sum256(content)
	if actual := hex.EncodeToString(digest[:]); actual != ciNeovimSHA {
		return "", fmt.Errorf("locked Neovim checksum = %s", actual)
	}
	command = exec.CommandContext(ctx, "tar", "--extract", "--gzip", "--file", archive,
		"--directory", directory)
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		return "", fmt.Errorf("extract locked Neovim: %w: %s", commandErr, output)
	}
	nvim := filepath.Join(directory, "nvim-linux-x86_64", "bin", "nvim")
	stat, err := os.Stat(nvim)
	if err != nil {
		return "", fmt.Errorf("stat locked Neovim executable: %w", err)
	}
	if stat.Mode()&0o111 == 0 {
		return "", fmt.Errorf("locked Neovim executable is not executable")
	}
	return nvim, nil
}
