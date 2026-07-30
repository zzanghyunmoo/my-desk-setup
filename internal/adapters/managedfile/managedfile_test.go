package managedfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/managedfile"
)

func TestInspectClassifiesManagedFileOwnership(t *testing.T) {
	t.Parallel()

	const expected = "#!/bin/sh\nexec managed\n"

	tests := []struct {
		name         string
		prepare      func(t *testing.T, path string)
		wantState    managedfile.State
		wantConflict managedfile.ConflictKind
	}{
		{
			name:      "missing",
			prepare:   func(*testing.T, string) {},
			wantState: managedfile.StateMissing,
		},
		{
			name: "regular expected",
			prepare: func(t *testing.T, path string) {
				writeFile(t, path, expected)
			},
			wantState: managedfile.StateReady,
		},
		{
			name: "user owned",
			prepare: func(t *testing.T, path string) {
				writeFile(t, path, "#!/bin/sh\nexec user\n")
			},
			wantState:    managedfile.StateConflict,
			wantConflict: managedfile.ConflictContent,
		},
		{
			name: "symlink",
			prepare: func(t *testing.T, path string) {
				target := filepath.Join(filepath.Dir(path), "target")
				writeFile(t, target, expected)
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("create symlink: %v", err)
				}
			},
			wantState:    managedfile.StateConflict,
			wantConflict: managedfile.ConflictNonRegular,
		},
		{
			name: "directory",
			prepare: func(t *testing.T, path string) {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatalf("create directory: %v", err)
				}
			},
			wantState:    managedfile.StateConflict,
			wantConflict: managedfile.ConflictNonRegular,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "bin", "tool")
			test.prepare(t, path)

			got := managedfile.Inspect(path, expected)
			if got.State != test.wantState || got.Conflict != test.wantConflict {
				t.Fatalf(
					"Inspect() = %+v, want state %v conflict %v",
					got,
					test.wantState,
					test.wantConflict,
				)
			}
		})
	}
}

func TestPublishCreatesExecutableAndPreservesExistingOwnership(t *testing.T) {
	t.Parallel()

	const expected = "#!/bin/sh\nexec managed\n"
	root := t.TempDir()
	path := filepath.Join(root, "nested", "bin", "tool")

	if err := managedfile.Publish(path, expected); err != nil {
		t.Fatalf("Publish(missing): %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read published file: %v", err)
	}
	if string(content) != expected {
		t.Fatalf("published content = %q, want %q", content, expected)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat published file: %v", err)
	}
	if !info.Mode().IsRegular() ||
		(runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
		t.Fatalf("published mode = %v, want regular 0700", info.Mode())
	}

	if err := managedfile.Publish(path, expected); err != nil {
		t.Fatalf("Publish(expected existing): %v", err)
	}
	if err := os.WriteFile(path, []byte("user-owned\n"), 0o700); err != nil {
		t.Fatalf("replace with user-owned content: %v", err)
	}
	if err := managedfile.Publish(path, expected); err == nil {
		t.Fatal("Publish(user-owned) error = nil, want conflict")
	}
	content, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved user-owned file: %v", err)
	}
	if string(content) != "user-owned\n" {
		t.Fatalf("user-owned content was overwritten: %q", content)
	}
}

func TestPublishRefusesNonRegularDestination(t *testing.T) {
	t.Parallel()

	const expected = "#!/bin/sh\nexec managed\n"

	tests := []struct {
		name    string
		prepare func(t *testing.T, path string)
	}{
		{
			name: "symlink",
			prepare: func(t *testing.T, path string) {
				target := filepath.Join(filepath.Dir(path), "target")
				writeFile(t, target, expected)
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("create symlink: %v", err)
				}
			},
		},
		{
			name: "directory",
			prepare: func(t *testing.T, path string) {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatalf("create directory: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "bin", "tool")
			test.prepare(t, path)

			err := managedfile.Publish(path, expected)
			var conflict *managedfile.ConflictError
			if !errors.As(err, &conflict) ||
				conflict.Kind != managedfile.ConflictNonRegular {
				t.Fatalf("Publish() error = %v, want non-regular conflict", err)
			}
			info, lstatErr := os.Lstat(path)
			if lstatErr != nil {
				t.Fatalf("lstat preserved path: %v", lstatErr)
			}
			if info.Mode().IsRegular() {
				t.Fatalf("destination mode = %v, want non-regular preserved", info.Mode())
			}
		})
	}
}

func TestPublishNeverOverwritesConcurrentWinner(t *testing.T) {
	t.Parallel()

	const publishers = 16
	path := filepath.Join(t.TempDir(), "bin", "tool")
	start := make(chan struct{})
	results := make(chan publishResult, publishers)
	var group sync.WaitGroup
	group.Add(publishers)

	for index := 0; index < publishers; index++ {
		content := "publisher-" + strconv.Itoa(index) + "\n"
		go func() {
			defer group.Done()
			<-start
			results <- publishResult{
				content: content,
				err:     managedfile.Publish(path, content),
			}
		}()
	}
	close(start)
	group.Wait()
	close(results)

	successes := 0
	var winningContent string
	for result := range results {
		if result.err == nil {
			successes++
			winningContent = result.content
			continue
		}
		var conflict *managedfile.ConflictError
		if !errors.As(result.err, &conflict) && !errors.Is(result.err, os.ErrExist) {
			t.Fatalf("Publish() error = %v, want ownership conflict or EEXIST", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful publishers = %d, want 1", successes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read winning content: %v", err)
	}
	if string(content) != winningContent {
		t.Fatalf("published content = %q, successful content = %q", content, winningContent)
	}
}

type publishResult struct {
	content string
	err     error
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
