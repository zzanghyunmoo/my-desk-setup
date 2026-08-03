package safefile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRegularNoFollowReadsStableRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	content := []byte("reviewed artifact")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	file, size, err := OpenRegularNoFollow(path)
	if err != nil {
		t.Fatalf("OpenRegularNoFollow(): %v", err)
	}
	defer file.Close()
	if size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", size, len(content))
	}
}

func TestOpenRegularNoFollowRejectsFinalSymlinkOrReparsePoint(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if file, _, err := OpenRegularNoFollow(link); err == nil {
		_ = file.Close()
		t.Fatal("OpenRegularNoFollow() accepted a symlink or reparse point")
	}
}

func TestReadRegularNoFollowReturnsBoundedStableBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	content := []byte("reviewed artifact bytes")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := ReadRegularNoFollow(path, int64(len(content)))
	if err != nil {
		t.Fatalf("ReadRegularNoFollow(): %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("ReadRegularNoFollow() = %q", data)
	}
	if _, err := ReadRegularNoFollow(path, int64(len(content)-1)); err == nil {
		t.Fatal("ReadRegularNoFollow() accepted an oversized file")
	}
}
