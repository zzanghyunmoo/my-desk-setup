package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRepositoryIdentity(t *testing.T) {
	root := repositoryRoot(t)

	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(
		string(module),
		"module github.com/zzanghyunmoo/my-desk-setup",
	) {
		t.Fatalf("go.mod does not declare the my-desk-setup module")
	}

	output := git(t, root, "rev-list", "--max-parents=0", "--all")
	roots := strings.Fields(output)
	if len(roots) != 1 {
		t.Fatalf("root commits = %d, want 1: %v", len(roots), roots)
	}

	expectedRemote := os.Getenv("MDS_EXPECT_REMOTE_URL")
	if expectedRemote == "" {
		return
	}
	actualRemote := strings.TrimSpace(git(t, root, "remote", "get-url", "origin"))
	if actualRemote != expectedRemote {
		t.Fatalf("origin URL = %q, want %q", actualRemote, expectedRemote)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()

	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
