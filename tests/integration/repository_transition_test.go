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
	if normalizeRepositoryURL(actualRemote) !=
		normalizeRepositoryURL(expectedRemote) {
		t.Fatalf("origin URL = %q, want %q", actualRemote, expectedRemote)
	}
}

func TestNormalizeRepositoryURL(t *testing.T) {
	t.Parallel()

	const canonical = "https://github.com/zzanghyunmoo/my-desk-setup"
	for _, candidate := range []string{
		canonical,
		canonical + "/",
		canonical + "///",
		canonical + ".git",
		canonical + ".git/",
	} {
		candidate := candidate
		t.Run(candidate, func(t *testing.T) {
			t.Parallel()
			if got := normalizeRepositoryURL(candidate); got != canonical {
				t.Fatalf("normalizeRepositoryURL(%q) = %q, want %q", candidate, got, canonical)
			}
		})
	}

	for _, different := range []string{
		"https://github.com/another-owner/my-desk-setup",
		"https://github.com/zzanghyunmoo/my-desk-setup-fork",
		"https://github.com/zzanghyunmoo/My-Desk-Setup",
	} {
		if normalizeRepositoryURL(different) == canonical {
			t.Fatalf("normalizeRepositoryURL(%q) lost exact owner/repo identity", different)
		}
	}
}

func normalizeRepositoryURL(value string) string {
	normalized := strings.TrimRight(strings.TrimSpace(value), "/")
	return strings.TrimSuffix(normalized, ".git")
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
