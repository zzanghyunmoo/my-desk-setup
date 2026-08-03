//go:build windows

package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRegularNoFollowDeniesConcurrentWindowsMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mds.exe")
	if err := os.WriteFile(path, []byte("reviewed binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, _, err := OpenRegularNoFollow(path)
	if err != nil {
		t.Fatalf("OpenRegularNoFollow(): %v", err)
	}
	defer file.Close()
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err == nil {
		t.Fatal("open Windows handle allowed concurrent source rewrite")
	}
	if err := os.Remove(path); err == nil {
		t.Fatal("open Windows handle allowed concurrent source deletion")
	}
}
