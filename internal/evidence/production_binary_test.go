package evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProductionBinarySnapshotCopiesBytesOnEveryPlatform(t *testing.T) {
	binaryPath := filepath.Join(t.TempDir(), "mds")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}
	content := []byte("reviewed production binary fixture")
	if err := os.WriteFile(binaryPath, content, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotProductionBinary(binaryPath)
	if err != nil {
		t.Fatalf("snapshotProductionBinary(): %v", err)
	}
	snapshotPath := snapshot.Path
	defer snapshot.Remove()
	copied, err := os.ReadFile(snapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copied, content) {
		t.Fatal("binary snapshot bytes differ from the reviewed source")
	}
	sum := sha256.Sum256(content)
	if snapshot.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("snapshot SHA-256 = %q", snapshot.SHA256)
	}
	snapshot.Remove()
	if _, err := os.Stat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("binary snapshot remains after cleanup: %v", err)
	}
}

func TestProductionBinarySnapshotRejectsNonExecutablePOSIXInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows executable identity is not represented by POSIX mode bits")
	}
	binaryPath := filepath.Join(t.TempDir(), "mds")
	if err := os.WriteFile(binaryPath, []byte("not executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotProductionBinary(binaryPath); err == nil ||
		!strings.Contains(err.Error(), "not executable") {
		t.Fatalf("snapshotProductionBinary(non-executable) error = %v", err)
	}
}

func TestRunMDSPreservesActionRequiredSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the focused fake binary fixture uses a POSIX executable")
	}
	binaryPath := filepath.Join(t.TempDir(), "fake-mds")
	if err := os.WriteFile(
		binaryPath,
		[]byte("#!/bin/sh\nprintf '{}\\n'\nexit 4\n"),
		0o700,
	); err != nil {
		t.Fatalf("write fake mds: %v", err)
	}

	output, actionRequired, err := runMDS(
		context.Background(),
		binaryPath,
		[]string{"doctor"},
		nil,
		true,
		certificationReadTimeout,
	)
	if err != nil {
		t.Fatalf("runMDS(): %v", err)
	}
	if strings.TrimSpace(string(output)) != "{}" || !actionRequired {
		t.Fatalf(
			"runMDS() output=%q actionRequired=%t, want JSON and true",
			output,
			actionRequired,
		)
	}
}

func TestProductionBinarySnapshotSurvivesSourceRewriteAndSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the focused fake binary fixture uses a POSIX executable")
	}
	root := t.TempDir()
	binaryPath := filepath.Join(root, "mds")
	trusted := []byte("#!/bin/sh\nprintf 'trusted\\n'\n")
	malicious := []byte("#!/bin/sh\nprintf 'untrusted\\n'\n")
	if err := os.WriteFile(binaryPath, trusted, 0o700); err != nil {
		t.Fatalf("write trusted mds: %v", err)
	}

	snapshot, err := snapshotProductionBinary(binaryPath)
	if err != nil {
		t.Fatalf("snapshotProductionBinary(): %v", err)
	}
	snapshotPath := snapshot.Path
	defer snapshot.Remove()
	sum := sha256.Sum256(trusted)
	if snapshot.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("snapshot SHA-256 = %q", snapshot.SHA256)
	}
	info, err := os.Stat(snapshot.Path)
	if err != nil {
		t.Fatalf("stat binary snapshot: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("binary snapshot mode = %o, want 700", info.Mode().Perm())
	}

	if err := os.WriteFile(binaryPath, malicious, 0o700); err != nil {
		t.Fatalf("rewrite source mds: %v", err)
	}
	output, _, err := runMDS(
		context.Background(),
		snapshot.Path,
		nil,
		nil,
		false,
		certificationReadTimeout,
	)
	if err != nil {
		t.Fatalf("run rewritten-source snapshot: %v", err)
	}
	if strings.TrimSpace(string(output)) != "trusted" {
		t.Fatalf("rewritten-source snapshot output = %q", output)
	}

	if err := os.Remove(binaryPath); err != nil {
		t.Fatalf("remove rewritten source mds: %v", err)
	}
	if err := os.WriteFile(binaryPath, malicious, 0o700); err != nil {
		t.Fatalf("swap source mds: %v", err)
	}
	output, _, err = runMDS(
		context.Background(),
		snapshot.Path,
		nil,
		nil,
		false,
		certificationReadTimeout,
	)
	if err != nil {
		t.Fatalf("run swapped-source snapshot: %v", err)
	}
	if strings.TrimSpace(string(output)) != "trusted" {
		t.Fatalf("swapped-source snapshot output = %q", output)
	}

	snapshot.Remove()
	if _, err := os.Stat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("binary snapshot remains after cleanup: %v", err)
	}
}

func TestRunMDSRejectsNonContractPartialResults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the focused fake binary fixture uses a POSIX executable")
	}
	tests := []struct {
		name         string
		script       string
		allowUnready bool
	}{
		{
			name:         "legacy generic failure",
			script:       "#!/bin/sh\nprintf '{}\\n'\nexit 1\n",
			allowUnready: true,
		},
		{
			name:         "action required without report",
			script:       "#!/bin/sh\nexit 4\n",
			allowUnready: true,
		},
		{
			name:         "action required not allowed",
			script:       "#!/bin/sh\nprintf '{}\\n'\nexit 4\n",
			allowUnready: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binaryPath := filepath.Join(t.TempDir(), "fake-mds")
			if err := os.WriteFile(
				binaryPath,
				[]byte(test.script),
				0o700,
			); err != nil {
				t.Fatalf("write fake mds: %v", err)
			}
			if _, _, err := runMDS(
				context.Background(),
				binaryPath,
				[]string{"doctor"},
				nil,
				test.allowUnready,
				certificationReadTimeout,
			); err == nil {
				t.Fatal("runMDS() error = nil, want contract rejection")
			}
		})
	}
}
