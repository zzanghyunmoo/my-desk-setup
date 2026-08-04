package durable

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileAtomicallyReplacesContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "report.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile(old): %v", err)
	}

	if err := WriteFile(path, []byte("new"), 0o640); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	if string(content) != "new" {
		t.Fatalf("content = %q, want new", content)
	}
}

func TestPublishDirectoryMovesSyncedTree(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	destination := filepath.Join(root, "published")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatalf("Mkdir(): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(staging, "manifest.json"),
		[]byte("{}\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	if err := PublishDirectory(staging, destination); err != nil {
		t.Fatalf("PublishDirectory(): %v", err)
	}
	if _, err := os.Lstat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "manifest.json")); err != nil {
		t.Fatalf("published manifest: %v", err)
	}
}

func TestPublishDirectoryNoReplacePreservesExistingDirectory(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	destination := filepath.Join(root, "published")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "new"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "owned"), []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := PublishDirectoryNoReplace(staging, destination); err == nil {
		t.Fatal("PublishDirectoryNoReplace() replaced an existing directory")
	}
	if content, err := os.ReadFile(filepath.Join(destination, "owned")); err != nil ||
		string(content) != "owned" {
		t.Fatalf("existing destination changed: content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(staging, "new")); err != nil ||
		string(content) != "new" {
		t.Fatalf("failed staging was not retained: content=%q err=%v", content, err)
	}
}

func TestWriteFileNoReplaceNeverExposesOrOverwritesPartialContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ownership.json")
	if err := WriteFileNoReplace(path, []byte("complete"), 0o600); err != nil {
		t.Fatalf("WriteFileNoReplace(): %v", err)
	}
	if err := WriteFileNoReplace(path, []byte("replacement"), 0o600); err == nil {
		t.Fatal("WriteFileNoReplace() replaced an existing file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	if string(content) != "complete" {
		t.Fatalf("content = %q, want original complete content", content)
	}
}

func TestRemoveFileMakesAuthoritativePathAbsent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "partial.json")
	if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	if err := RemoveFile(path); err != nil {
		t.Fatalf("RemoveFile(): %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("authoritative path remains: %v", err)
	}
	if err := RemoveFile(path); err != nil {
		t.Fatalf("RemoveFile(missing): %v", err)
	}
}

func TestSyncTreeRejectsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink requires additional Windows privileges")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("content"), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Fatalf("Symlink(): %v", err)
	}

	if err := SyncTree(root); err == nil {
		t.Fatal("SyncTree() accepted a symlink")
	}
}
