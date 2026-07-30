package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceFileDurablyReplacesExistingFile(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	source := filepath.Join(directory, "next.lock")
	destination := filepath.Join(directory, "versions.lock.yaml")
	if err := os.WriteFile(source, []byte("new lock\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(destination, []byte("old lock\n"), 0o600); err != nil {
		t.Fatalf("write destination: %v", err)
	}

	if err := replaceFileDurably(source, destination); err != nil {
		t.Fatalf("replaceFileDurably(): %v", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(content) != "new lock\n" {
		t.Fatalf("destination = %q, want new lock", content)
	}
	if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists after replace: %v", err)
	}
}
