package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	fixtureOnce   sync.Once
	fixtureParent string
	fixtureDist   string
	fixtureErr    error
)

func TestMain(testingMain *testing.M) {
	code := testingMain.Run()
	if fixtureParent != "" {
		_ = os.RemoveAll(fixtureParent)
	}
	os.Exit(code)
}

func TestBuildProducesDeterministicStrictRelease(t *testing.T) {
	root := repositoryRoot(t)
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	options := Options{
		SourceRoot: root,
		Version:    "0.1.0",
		Commit:     "0123456789abcdef0123456789abcdef01234567",
		Date:       time.Date(2026, 7, 29, 12, 34, 56, 0, time.UTC),
	}

	options.OutputDir = first
	if err := Build(context.Background(), options); err != nil {
		t.Fatalf("first Build() error = %v", err)
	}
	options.OutputDir = second
	if err := Build(context.Background(), options); err != nil {
		t.Fatalf("second Build() error = %v", err)
	}

	firstManifest, err := Verify(first)
	if err != nil {
		t.Fatalf("Verify(first) error = %v", err)
	}
	secondManifest, err := Verify(second)
	if err != nil {
		t.Fatalf("Verify(second) error = %v", err)
	}
	if !reflect.DeepEqual(firstManifest, secondManifest) {
		t.Fatalf("manifests differ:\nfirst: %#v\nsecond: %#v", firstManifest, secondManifest)
	}
	for _, name := range []string{"checksums.txt", "release-manifest.json"} {
		firstBytes, readErr := os.ReadFile(filepath.Join(first, name))
		if readErr != nil {
			t.Fatalf("read first %s: %v", name, readErr)
		}
		secondBytes, readErr := os.ReadFile(filepath.Join(second, name))
		if readErr != nil {
			t.Fatalf("read second %s: %v", name, readErr)
		}
		if string(firstBytes) != string(secondBytes) {
			t.Fatalf("%s is not byte-identical across builds", name)
		}
	}

	if firstManifest.SchemaVersion != SchemaVersion ||
		firstManifest.Version != options.Version ||
		firstManifest.Commit != options.Commit ||
		firstManifest.Date != options.Date.Format(time.RFC3339) {
		t.Fatalf("manifest identity = %#v", firstManifest)
	}
	if got, want := len(firstManifest.Artifacts), 6; got != want {
		t.Fatalf("artifact count = %d, want %d", got, want)
	}
	if got, want := len(firstManifest.Bootstraps), 2; got != want {
		t.Fatalf("bootstrap count = %d, want %d", got, want)
	}

	if runtime.GOOS == "windows" {
		t.Skip("released Windows binary execution is covered by the release workflow smoke job")
	}
	nativeBinary := extractArchiveBinary(
		t,
		filepath.Join(first, fmt.Sprintf("mds_0.1.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)),
	)
	output, runErr := runVersion(nativeBinary)
	if runErr != nil {
		t.Fatalf("run released binary --version: %v\n%s", runErr, output)
	}
	for _, value := range []string{options.Version, options.Commit, options.Date.Format(time.RFC3339)} {
		if !strings.Contains(output, value) {
			t.Fatalf("released --version = %q, want %q", output, value)
		}
	}
}

func TestVerifyRejectsExtraFilesAndUnknownManifestFields(t *testing.T) {
	dist := buildFixtureRelease(t)
	if err := os.WriteFile(filepath.Join(dist, "unexpected.txt"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dist); err == nil || !strings.Contains(err.Error(), "unexpected release file") {
		t.Fatalf("Verify(extra file) error = %v", err)
	}

	dist = buildFixtureRelease(t)
	path := filepath.Join(dist, "release-manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["credential"] = "must-not-be-accepted"
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dist); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Verify(unknown manifest field) error = %v", err)
	}
}

func TestVerifyRejectsArchiveExtraDuplicateAndSymlinkEntries(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*testing.T, string)
		message string
	}{
		{
			name: "extra tar entry",
			mutate: func(t *testing.T, path string) {
				writeTarGz(t, path, []tar.Header{
					{Name: "mds", Mode: 0o755, Size: 3, Typeflag: tar.TypeReg},
					{Name: "README", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg},
				})
			},
			message: "exactly one entry",
		},
		{
			name: "duplicate zip entry",
			mutate: func(t *testing.T, path string) {
				writeZip(t, path, []zipEntry{
					{name: "mds.exe", mode: 0o755},
					{name: "mds.exe", mode: 0o755},
				})
			},
			message: "exactly one entry",
		},
		{
			name: "tar symlink",
			mutate: func(t *testing.T, path string) {
				writeTarGz(t, path, []tar.Header{{
					Name: "mds", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "/tmp/evil",
				}})
			},
			message: "regular file",
		},
		{
			name: "zip symlink",
			mutate: func(t *testing.T, path string) {
				writeZip(t, path, []zipEntry{{name: "mds.exe", mode: os.ModeSymlink | 0o777}})
			},
			message: "regular file",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dist := buildFixtureRelease(t)
			var archive string
			if strings.Contains(test.name, "zip") {
				archive = filepath.Join(dist, "mds_0.1.0_windows_amd64.zip")
			} else {
				archive = filepath.Join(dist, "mds_0.1.0_darwin_amd64.tar.gz")
			}
			test.mutate(t, archive)
			refreshArtifactIdentity(t, dist, filepath.Base(archive))

			if _, err := Verify(dist); err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Verify() error = %v, want containing %q", err, test.message)
			}
		})
	}
}

func TestVerifyRejectsChecksumAndManifestIdentityMismatch(t *testing.T) {
	dist := buildFixtureRelease(t)
	checksums := filepath.Join(dist, "checksums.txt")
	data, err := os.ReadFile(checksums)
	if err != nil {
		t.Fatal(err)
	}
	if data[0] == '0' {
		data[0] = '1'
	} else {
		data[0] = '0'
	}
	if err := os.WriteFile(checksums, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dist); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Verify(checksum mismatch) error = %v", err)
	}
}

func buildFixtureRelease(t *testing.T) string {
	t.Helper()
	fixtureOnce.Do(func() {
		fixtureParent, fixtureErr = os.MkdirTemp("", "mds-release-test-fixture-")
		if fixtureErr != nil {
			return
		}
		fixtureDist = filepath.Join(fixtureParent, "dist")
		fixtureErr = Build(context.Background(), Options{
			SourceRoot: repositoryRoot(t),
			OutputDir:  fixtureDist,
			Version:    "0.1.0",
			Commit:     "0123456789abcdef0123456789abcdef01234567",
			Date:       time.Date(2026, 7, 29, 12, 34, 56, 0, time.UTC),
		})
	})
	if fixtureErr != nil {
		t.Fatalf("build fixture release: %v", fixtureErr)
	}
	dist := filepath.Join(t.TempDir(), "dist")
	if err := os.Mkdir(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(fixtureDist)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, readErr := os.ReadFile(filepath.Join(fixtureDist, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(dist, entry.Name()), data, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	return dist
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func extractArchiveBinary(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), header.Name)
	target, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, reader); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	return output
}

type zipEntry struct {
	name string
	mode os.FileMode
}

func writeZip(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		header.SetMode(entry.mode)
		part, createErr := writer.CreateHeader(header)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if entry.mode.IsRegular() {
			if _, writeErr := part.Write([]byte("bin")); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTarGz(t *testing.T, path string, headers []tar.Header) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	for index := range headers {
		header := headers[index]
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte("bin")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func runVersion(path string) (string, error) {
	output, err := exec.Command(path, "--version").CombinedOutput()
	return string(output), err
}

func refreshArtifactIdentity(t *testing.T, dist, name string) {
	t.Helper()
	manifestPath := filepath.Join(dist, "release-manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	digest, size := testFileIdentity(t, filepath.Join(dist, name))
	for index := range manifest.Artifacts {
		if manifest.Artifacts[index].Name == name {
			manifest.Artifacts[index].SHA256 = digest
			manifest.Artifacts[index].Size = size
		}
	}
	data, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	checksums := make(map[string]string)
	for _, artifact := range manifest.Artifacts {
		checksums[artifact.Name] = artifact.SHA256
	}
	for _, bootstrap := range manifest.Bootstraps {
		checksums[bootstrap.Name] = bootstrap.SHA256
	}
	names := make([]string, 0, len(checksums))
	for checksumName := range checksums {
		names = append(names, checksumName)
	}
	sort.Strings(names)
	var output strings.Builder
	for _, checksumName := range names {
		fmt.Fprintf(&output, "%s  %s\n", checksums[checksumName], checksumName)
	}
	if err := os.WriteFile(filepath.Join(dist, "checksums.txt"), []byte(output.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testFileIdentity(t *testing.T, path string) (string, int64) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), size
}
