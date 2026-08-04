package artifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type archiveEntry struct {
	name     string
	body     []byte
	typeflag byte
	linkname string
}

func TestSnapshotterAcquiresExactTarGzAndOwnsCleanup(t *testing.T) {
	executable := []byte("#!/bin/sh\necho exact\n")
	archive := tarGzFixture(t, []archiveEntry{
		{name: "package", typeflag: tar.TypeDir},
		{name: "package/bin/tool", body: executable, typeflag: tar.TypeReg},
		{name: "package/data.json", body: []byte("{}\n"), typeflag: tar.TypeReg},
	})
	root := t.TempDir()
	snapshotter := Snapshotter{
		Open:     fixtureOpener(archive),
		TempRoot: root,
	}
	snapshot, err := snapshotter.Acquire(context.Background(), SnapshotRequest{
		URL:              "fixture://tool.tar.gz",
		SHA256:           digest(archive),
		Format:           "tar.gz",
		Executable:       "package/bin/tool",
		ExecutableSHA256: digest(executable),
		Alias:            "tool",
		ExtractAll:       true,
	})
	if err != nil {
		t.Fatalf("Acquire(): %v", err)
	}
	if snapshot.ArchiveSHA256 != digest(archive) ||
		snapshot.ExecutableSHA256 != digest(executable) {
		t.Fatalf("snapshot identity = %+v", snapshot)
	}
	if snapshot.Root() == "" || snapshot.Executable() == "" {
		t.Fatalf("snapshot paths are empty: %+v", snapshot)
	}
	if got, err := os.ReadFile(snapshot.Executable()); err != nil ||
		!bytes.Equal(got, executable) {
		t.Fatalf("ReadFile(executable) = %q, %v", got, err)
	}
	if got, err := os.ReadFile(snapshot.Path("package/data.json")); err != nil ||
		string(got) != "{}\n" {
		t.Fatalf("ReadFile(data) = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(snapshot.Executable())
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("executable mode = %v, %v; want 0700", info, err)
		}
	}
	ownedRoot := snapshot.Root()
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if _, err := os.Stat(ownedRoot); !os.IsNotExist(err) {
		t.Fatalf("owned root still exists: %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("second Close(): %v", err)
	}
}

func TestSnapshotterAcquiresExactZipExecutableOnly(t *testing.T) {
	executable := []byte("MZ exact fixture\n")
	archive := zipFixture(t, map[string][]byte{
		"nested/tool.exe": executable,
		"nested/ignored":  []byte("ignored"),
	})
	snapshot, err := (Snapshotter{Open: fixtureOpener(archive)}).Acquire(
		context.Background(),
		SnapshotRequest{
			URL:              "fixture://tool.zip",
			SHA256:           digest(archive),
			Format:           "zip",
			Executable:       "nested/tool.exe",
			ExecutableSHA256: digest(executable),
			Alias:            "tool.exe",
		},
	)
	if err != nil {
		t.Fatalf("Acquire(): %v", err)
	}
	defer snapshot.Close()
	if _, err := os.Stat(snapshot.Path("nested/ignored")); !os.IsNotExist(err) {
		t.Fatalf("non-executable extracted: %v", err)
	}
}

func TestSnapshotterRejectsUnsafeArchiveEntries(t *testing.T) {
	cases := []struct {
		name    string
		entries []archiveEntry
		want    string
	}{
		{name: "traversal", entries: []archiveEntry{{name: "../tool", body: []byte("x"), typeflag: tar.TypeReg}}, want: "unsafe archive path"},
		{name: "absolute", entries: []archiveEntry{{name: "/tool", body: []byte("x"), typeflag: tar.TypeReg}}, want: "unsafe archive path"},
		{name: "backslash", entries: []archiveEntry{{name: `package\\tool`, body: []byte("x"), typeflag: tar.TypeReg}}, want: "unsafe archive path"},
		{name: "symlink", entries: []archiveEntry{{name: "package/tool", typeflag: tar.TypeSymlink, linkname: "target"}}, want: "unsupported archive entry"},
		{name: "hardlink", entries: []archiveEntry{{name: "package/tool", typeflag: tar.TypeLink, linkname: "target"}}, want: "unsupported archive entry"},
		{name: "device", entries: []archiveEntry{{name: "package/tool", typeflag: tar.TypeChar}}, want: "unsupported archive entry"},
		{name: "duplicate", entries: []archiveEntry{{name: "package/tool", body: []byte("a"), typeflag: tar.TypeReg}, {name: "package/tool", body: []byte("b"), typeflag: tar.TypeReg}}, want: "duplicate archive path"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			archive := tarGzFixture(t, test.entries)
			root := t.TempDir()
			_, err := (Snapshotter{Open: fixtureOpener(archive), TempRoot: root}).Acquire(
				context.Background(),
				SnapshotRequest{
					URL:        "fixture://unsafe.tar.gz",
					SHA256:     digest(archive),
					Format:     "tar.gz",
					Executable: "package/tool",
					ExtractAll: true,
				},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Acquire() error = %v, want %q", err, test.want)
			}
			assertDirectoryEmpty(t, root)
		})
	}
}

func TestSnapshotterRejectsUnsafeZipEntries(t *testing.T) {
	cases := []struct {
		name    string
		entries []zipArchiveEntry
		want    string
	}{
		{name: "traversal", entries: []zipArchiveEntry{{name: "../tool", body: []byte("x")}}, want: "unsafe archive path"},
		{name: "absolute", entries: []zipArchiveEntry{{name: "/tool", body: []byte("x")}}, want: "unsafe archive path"},
		{name: "backslash", entries: []zipArchiveEntry{{name: `package\tool`, body: []byte("x")}}, want: "unsafe archive path"},
		{name: "symlink", entries: []zipArchiveEntry{{name: "tool", body: []byte("target"), mode: os.ModeSymlink | 0o777}}, want: "unsupported archive entry"},
		{name: "duplicate", entries: []zipArchiveEntry{{name: "tool", body: []byte("a")}, {name: "tool", body: []byte("b")}}, want: "duplicate archive path"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			archive := zipEntriesFixture(t, test.entries)
			root := t.TempDir()
			_, err := (Snapshotter{Open: fixtureOpener(archive), TempRoot: root}).Acquire(
				context.Background(),
				SnapshotRequest{
					URL:        "fixture://unsafe.zip",
					SHA256:     digest(archive),
					Format:     "zip",
					Executable: "tool",
					ExtractAll: true,
				},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Acquire() error = %v, want %q", err, test.want)
			}
			assertDirectoryEmpty(t, root)
		})
	}
}

func TestSnapshotterRejectsIdentityFormatAndBounds(t *testing.T) {
	executable := []byte("exact")
	archive := tarGzFixture(t, []archiveEntry{{
		name: "tool", body: executable, typeflag: tar.TypeReg,
	}})
	tests := []struct {
		name    string
		request SnapshotRequest
		limits  SnapshotLimits
		want    string
	}{
		{name: "archive digest", request: SnapshotRequest{URL: "fixture://tool", SHA256: strings.Repeat("0", 64), Format: "tar.gz", Executable: "tool"}, want: "archive digest mismatch"},
		{name: "executable digest", request: SnapshotRequest{URL: "fixture://tool", SHA256: digest(archive), Format: "tar.gz", Executable: "tool", ExecutableSHA256: strings.Repeat("0", 64)}, want: "executable digest mismatch"},
		{name: "format", request: SnapshotRequest{URL: "fixture://tool", SHA256: digest(archive), Format: "rar", Executable: "tool"}, want: "unsupported archive format"},
		{name: "compressed", request: SnapshotRequest{URL: "fixture://tool", SHA256: digest(archive), Format: "tar.gz", Executable: "tool"}, limits: SnapshotLimits{CompressedBytes: int64(len(archive) - 1)}, want: "compressed size limit"},
		{name: "file", request: SnapshotRequest{URL: "fixture://tool", SHA256: digest(archive), Format: "tar.gz", Executable: "tool"}, limits: SnapshotLimits{FileBytes: 2}, want: "file size limit"},
		{name: "total", request: SnapshotRequest{URL: "fixture://tool", SHA256: digest(archive), Format: "tar.gz", Executable: "tool"}, limits: SnapshotLimits{TotalBytes: 2}, want: "total extracted size limit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (Snapshotter{
				Open: fixtureOpener(archive), Limits: test.limits,
			}).Acquire(context.Background(), test.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Acquire() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSnapshotterEnforcesEntryCountLimit(t *testing.T) {
	archive := tarGzFixture(t, []archiveEntry{
		{name: "tool", body: []byte("exact"), typeflag: tar.TypeReg},
		{name: "other", body: []byte("other"), typeflag: tar.TypeReg},
	})
	_, err := (Snapshotter{
		Open: fixtureOpener(archive), Limits: SnapshotLimits{Entries: 1},
	}).Acquire(context.Background(), SnapshotRequest{
		URL: "fixture://tool", SHA256: digest(archive), Format: "tar.gz",
		Executable: "tool", ExtractAll: true,
	})
	if err == nil || !strings.Contains(err.Error(), "entry count limit") {
		t.Fatalf("Acquire() error = %v, want entry count limit", err)
	}
}

func TestSnapshotterRequiresHTTPSWithoutInjectedOpener(t *testing.T) {
	_, err := (Snapshotter{}).Acquire(context.Background(), SnapshotRequest{
		URL: "http://example.com/tool.zip", SHA256: strings.Repeat("0", 64),
		Format: "zip", Executable: "tool",
	})
	if err == nil || !strings.Contains(err.Error(), "credential-free HTTPS") {
		t.Fatalf("Acquire() error = %v, want HTTPS rejection", err)
	}
}

func TestSnapshotCloseRefusesUnownedDirectory(t *testing.T) {
	archive := tarGzFixture(t, []archiveEntry{{
		name: "tool", body: []byte("exact"), typeflag: tar.TypeReg,
	}})
	snapshot, err := (Snapshotter{Open: fixtureOpener(archive)}).Acquire(
		context.Background(), SnapshotRequest{
			URL: "fixture://tool", SHA256: digest(archive), Format: "tar.gz",
			Executable: "tool",
		},
	)
	if err != nil {
		t.Fatalf("Acquire(): %v", err)
	}
	if err := os.WriteFile(snapshot.ownershipPath, []byte("foreign\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(marker): %v", err)
	}
	if err := snapshot.Close(); err == nil || !strings.Contains(err.Error(), "ownership") {
		t.Fatalf("Close() error = %v, want ownership rejection", err)
	}
	if _, err := os.Stat(snapshot.Root()); err != nil {
		t.Fatalf("unowned root was removed: %v", err)
	}
	if err := os.RemoveAll(snapshot.Root()); err != nil {
		t.Fatalf("RemoveAll(test cleanup): %v", err)
	}
}

func fixtureOpener(content []byte) OpenSnapshotURL {
	return func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(content)), nil
	}
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func tarGzFixture(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		size := int64(len(entry.body))
		if entry.typeflag == tar.TypeDir {
			size = 0
		}
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: entry.name, Typeflag: entry.typeflag, Linkname: entry.linkname,
			Mode: 0o755, Size: size,
		}); err != nil {
			t.Fatalf("WriteHeader(): %v", err)
		}
		if size > 0 {
			if _, err := tarWriter.Write(entry.body); err != nil {
				t.Fatalf("Write(): %v", err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("tar Close(): %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzip Close(): %v", err)
	}
	return output.Bytes()
}

func zipFixture(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	values := make([]zipArchiveEntry, 0, len(entries))
	for name, body := range entries {
		values = append(values, zipArchiveEntry{name: name, body: body})
	}
	return zipEntriesFixture(t, values)
}

type zipArchiveEntry struct {
	name string
	body []byte
	mode os.FileMode
}

func zipEntriesFixture(t *testing.T, entries []zipArchiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("Create(%s): %v", entry.name, err)
		}
		if _, err := file.Write(entry.body); err != nil {
			t.Fatalf("Write(%s): %v", entry.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(zip): %v", err)
	}
	return output.Bytes()
}

func assertDirectoryEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", root, err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary root leaked entries: %v", entries)
	}
}

func TestSnapshotPathRejectsEscape(t *testing.T) {
	snapshot := &Snapshot{root: filepath.Clean(t.TempDir())}
	if got := snapshot.Path("../escape"); got != "" {
		t.Fatalf("Path(escape) = %q, want empty", got)
	}
}
