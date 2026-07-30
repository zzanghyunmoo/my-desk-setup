package release

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion = "mds.release/v1"

	manifestName  = "release-manifest.json"
	checksumsName = "checksums.txt"
)

var (
	versionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)
	commitPattern   = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Options struct {
	SourceRoot string
	OutputDir  string
	Version    string
	Commit     string
	Date       time.Time
}

type Artifact struct {
	Name          string `json:"name"`
	OS            string `json:"os"`
	Architecture  string `json:"architecture"`
	ArchiveFormat string `json:"archive_format"`
	Binary        string `json:"binary"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
}

type Bootstrap struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	SchemaVersion string      `json:"schema_version"`
	Version       string      `json:"version"`
	Commit        string      `json:"commit"`
	Date          string      `json:"date"`
	Artifacts     []Artifact  `json:"artifacts"`
	Bootstraps    []Bootstrap `json:"bootstraps"`
}

type target struct {
	os           string
	architecture string
	format       string
	binary       string
}

var releaseTargets = []target{
	{os: "darwin", architecture: "amd64", format: "tar.gz", binary: "mds"},
	{os: "darwin", architecture: "arm64", format: "tar.gz", binary: "mds"},
	{os: "linux", architecture: "amd64", format: "tar.gz", binary: "mds"},
	{os: "linux", architecture: "arm64", format: "tar.gz", binary: "mds"},
	{os: "windows", architecture: "amd64", format: "zip", binary: "mds.exe"},
	{os: "windows", architecture: "arm64", format: "zip", binary: "mds.exe"},
}

var releaseBootstraps = []struct {
	name string
	os   string
}{
	{name: "macos.sh", os: "darwin"},
	{name: "windows.ps1", os: "windows"},
}

func Build(ctx context.Context, options Options) (returnErr error) {
	if err := validateOptions(options); err != nil {
		return err
	}
	sourceRoot, err := filepath.Abs(options.SourceRoot)
	if err != nil {
		return fmt.Errorf("resolve source root: %w", err)
	}
	outputDir, err := filepath.Abs(options.OutputDir)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	if _, err := os.Lstat(outputDir); err == nil {
		return fmt.Errorf("output path already exists: %s", outputDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect output path: %w", err)
	}
	parent := filepath.Dir(outputDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create output parent: %w", err)
	}

	staging, err := os.MkdirTemp(parent, "."+filepath.Base(outputDir)+".staging-")
	if err != nil {
		return fmt.Errorf("create release staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(staging); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("remove release staging directory: %w", err)
		}
	}()
	binaries, err := os.MkdirTemp(parent, ".mds-release-binaries-")
	if err != nil {
		return fmt.Errorf("create binary staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(binaries); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("remove binary staging directory: %w", err)
		}
	}()

	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		Version:       options.Version,
		Commit:        options.Commit,
		Date:          options.Date.UTC().Format(time.RFC3339),
		Artifacts:     make([]Artifact, 0, len(releaseTargets)),
		Bootstraps:    make([]Bootstrap, 0, len(releaseBootstraps)),
	}
	for _, releaseTarget := range releaseTargets {
		binaryPath := filepath.Join(
			binaries,
			releaseTarget.os+"-"+releaseTarget.architecture+"-"+releaseTarget.binary,
		)
		if err := buildBinary(ctx, sourceRoot, binaryPath, releaseTarget, manifest); err != nil {
			return err
		}
		name := artifactName(manifest.Version, releaseTarget)
		archivePath := filepath.Join(staging, name)
		switch releaseTarget.format {
		case "tar.gz":
			err = writeTarArchive(archivePath, binaryPath, releaseTarget.binary)
		case "zip":
			err = writeZipArchive(archivePath, binaryPath, releaseTarget.binary)
		default:
			err = fmt.Errorf("unsupported archive format %q", releaseTarget.format)
		}
		if err != nil {
			return fmt.Errorf("archive %s: %w", name, err)
		}
		digest, size, err := fileIdentity(archivePath)
		if err != nil {
			return err
		}
		manifest.Artifacts = append(manifest.Artifacts, Artifact{
			Name:          name,
			OS:            releaseTarget.os,
			Architecture:  releaseTarget.architecture,
			ArchiveFormat: releaseTarget.format,
			Binary:        releaseTarget.binary,
			SHA256:        digest,
			Size:          size,
		})
	}
	for _, expected := range releaseBootstraps {
		source := filepath.Join(sourceRoot, "bootstrap", expected.name)
		destination := filepath.Join(staging, expected.name)
		if err := copyRegularFile(source, destination); err != nil {
			return fmt.Errorf("stage bootstrap %s: %w", expected.name, err)
		}
		digest, size, err := fileIdentity(destination)
		if err != nil {
			return err
		}
		manifest.Bootstraps = append(manifest.Bootstraps, Bootstrap{
			Name: expected.name, OS: expected.os, SHA256: digest, Size: size,
		})
	}
	if err := writeManifest(staging, manifest); err != nil {
		return err
	}
	if err := writeChecksums(staging, manifest); err != nil {
		return err
	}
	if _, err := Verify(staging); err != nil {
		return fmt.Errorf("verify staged release: %w", err)
	}
	if err := os.Rename(staging, outputDir); err != nil {
		return fmt.Errorf("publish release directory: %w", err)
	}
	return nil
}

func Verify(directory string) (Manifest, error) {
	var manifest Manifest
	root, err := filepath.Abs(directory)
	if err != nil {
		return manifest, fmt.Errorf("resolve release directory: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return manifest, fmt.Errorf("inspect release directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return manifest, errors.New("release path must be a real directory")
	}
	manifest, err = readManifest(filepath.Join(root, manifestName))
	if err != nil {
		return Manifest{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	expectedFiles := map[string]bool{
		manifestName: true, checksumsName: true,
	}
	for _, artifact := range manifest.Artifacts {
		expectedFiles[artifact.Name] = true
	}
	for _, bootstrap := range manifest.Bootstraps {
		expectedFiles[bootstrap.Name] = true
	}
	if err := verifyExactFiles(root, expectedFiles); err != nil {
		return Manifest{}, err
	}
	checksums, err := readChecksums(filepath.Join(root, checksumsName))
	if err != nil {
		return Manifest{}, err
	}
	if len(checksums) != len(manifest.Artifacts)+len(manifest.Bootstraps) {
		return Manifest{}, fmt.Errorf(
			"checksums file has %d entries, want %d",
			len(checksums),
			len(manifest.Artifacts)+len(manifest.Bootstraps),
		)
	}
	for _, artifact := range manifest.Artifacts {
		if err := verifyFileIdentity(root, artifact.Name, artifact.SHA256, artifact.Size, checksums); err != nil {
			return Manifest{}, err
		}
		if err := verifyArchive(filepath.Join(root, artifact.Name), artifact); err != nil {
			return Manifest{}, fmt.Errorf("verify %s: %w", artifact.Name, err)
		}
	}
	for _, bootstrap := range manifest.Bootstraps {
		if err := verifyFileIdentity(root, bootstrap.Name, bootstrap.SHA256, bootstrap.Size, checksums); err != nil {
			return Manifest{}, err
		}
	}
	return manifest, nil
}

func validateOptions(options Options) error {
	if strings.TrimSpace(options.SourceRoot) == "" {
		return errors.New("source root is required")
	}
	if strings.TrimSpace(options.OutputDir) == "" {
		return errors.New("output directory is required")
	}
	if !versionPattern.MatchString(options.Version) {
		return fmt.Errorf("invalid release version %q", options.Version)
	}
	if !commitPattern.MatchString(options.Commit) {
		return fmt.Errorf("invalid release commit %q", options.Commit)
	}
	if options.Date.IsZero() || options.Date.Nanosecond() != 0 {
		return errors.New("release date must be an exact whole-second timestamp")
	}
	return nil
}

func buildBinary(
	ctx context.Context,
	sourceRoot string,
	output string,
	releaseTarget target,
	manifest Manifest,
) error {
	ldflags := strings.Join([]string{
		"-s", "-w",
		"-X", "github.com/zzanghyunmoo/my-desk-setup/internal/version.Version=" + manifest.Version,
		"-X", "github.com/zzanghyunmoo/my-desk-setup/internal/version.Commit=" + manifest.Commit,
		"-X", "github.com/zzanghyunmoo/my-desk-setup/internal/version.Date=" + manifest.Date,
	}, " ")
	command := exec.CommandContext(
		ctx,
		"go", "build",
		"-trimpath",
		"-buildvcs=false",
		"-ldflags", ldflags,
		"-o", output,
		"./cmd/mds",
	)
	command.Dir = sourceRoot
	command.Env = buildEnvironment(os.Environ(), map[string]string{
		"CGO_ENABLED": "0",
		"GOARCH":      releaseTarget.architecture,
		"GOFLAGS":     "",
		"GOOS":        releaseTarget.os,
	})
	outputBytes, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"build %s/%s: %w: %s",
			releaseTarget.os,
			releaseTarget.architecture,
			err,
			strings.TrimSpace(string(outputBytes)),
		)
	}
	return nil
}

func buildEnvironment(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, item := range base {
		key, _, found := strings.Cut(item, "=")
		if found {
			values[key] = item
		}
	}
	for key, value := range overrides {
		values[key] = key + "=" + value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, values[key])
	}
	return environment
}

func artifactName(version string, releaseTarget target) string {
	extension := ".tar.gz"
	if releaseTarget.format == "zip" {
		extension = ".zip"
	}
	return fmt.Sprintf(
		"mds_%s_%s_%s%s",
		version,
		releaseTarget.os,
		releaseTarget.architecture,
		extension,
	)
}

func writeTarArchive(path, binaryPath, binaryName string) (returnErr error) {
	binary, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("open binary: %w", err)
	}
	defer binary.Close()
	info, err := binary.Stat()
	if err != nil {
		return fmt.Errorf("inspect binary: %w", err)
	}
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer func() {
		if err := output.Close(); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close archive: %w", err)
		}
	}()
	compressed, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	compressed.Header.ModTime = time.Unix(0, 0).UTC()
	compressed.Header.OS = 255
	writer := tar.NewWriter(compressed)
	header := &tar.Header{
		Name:       binaryName,
		Mode:       0o755,
		Size:       info.Size(),
		ModTime:    time.Unix(0, 0).UTC(),
		AccessTime: time.Time{},
		ChangeTime: time.Time{},
		Typeflag:   tar.TypeReg,
		Format:     tar.FormatUSTAR,
	}
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header: %w", err)
	}
	if _, err := io.Copy(writer, binary); err != nil {
		return fmt.Errorf("write binary to tar: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := compressed.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}
	return nil
}

func writeZipArchive(path, binaryPath, binaryName string) (returnErr error) {
	binary, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("open binary: %w", err)
	}
	defer binary.Close()
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer func() {
		if err := output.Close(); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close archive: %w", err)
		}
	}()
	writer := zip.NewWriter(output)
	header := &zip.FileHeader{
		Name:     binaryName,
		Method:   zip.Deflate,
		Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	header.SetMode(0o755)
	part, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("write zip header: %w", err)
	}
	if _, err := io.Copy(part, binary); err != nil {
		return fmt.Errorf("write binary to zip: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close zip writer: %w", err)
	}
	return nil
}

func copyRegularFile(source, destination string) (returnErr error) {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("source must be a regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer func() {
		if err := output.Close(); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close destination: %w", err)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}
	return nil
}

func writeManifest(root string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode release manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, manifestName), data, 0o644); err != nil {
		return fmt.Errorf("write release manifest: %w", err)
	}
	return nil
}

func writeChecksums(root string, manifest Manifest) error {
	values := make(map[string]string, len(manifest.Artifacts)+len(manifest.Bootstraps))
	for _, artifact := range manifest.Artifacts {
		values[artifact.Name] = artifact.SHA256
	}
	for _, bootstrap := range manifest.Bootstraps {
		values[bootstrap.Name] = bootstrap.SHA256
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var contents strings.Builder
	for _, name := range names {
		fmt.Fprintf(&contents, "%s  %s\n", values[name], name)
	}
	if err := os.WriteFile(
		filepath.Join(root, checksumsName),
		[]byte(contents.String()),
		0o644,
	); err != nil {
		return fmt.Errorf("write checksums: %w", err)
	}
	return nil
}

func readManifest(path string) (Manifest, error) {
	var manifest Manifest
	file, err := os.Open(path)
	if err != nil {
		return manifest, fmt.Errorf("open release manifest: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, errors.New("decode release manifest: multiple JSON values")
		}
		return Manifest{}, fmt.Errorf("decode release manifest trailing data: %w", err)
	}
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported release schema %q", manifest.SchemaVersion)
	}
	if !versionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("invalid manifest version %q", manifest.Version)
	}
	if !commitPattern.MatchString(manifest.Commit) {
		return fmt.Errorf("invalid manifest commit %q", manifest.Commit)
	}
	date, err := time.Parse(time.RFC3339, manifest.Date)
	if err != nil || date.UTC().Format(time.RFC3339) != manifest.Date {
		return fmt.Errorf("invalid canonical manifest date %q", manifest.Date)
	}
	if len(manifest.Artifacts) != len(releaseTargets) {
		return fmt.Errorf(
			"manifest has %d artifacts, want %d",
			len(manifest.Artifacts),
			len(releaseTargets),
		)
	}
	for index, releaseTarget := range releaseTargets {
		artifact := manifest.Artifacts[index]
		expectedName := artifactName(manifest.Version, releaseTarget)
		if artifact.Name != expectedName ||
			artifact.OS != releaseTarget.os ||
			artifact.Architecture != releaseTarget.architecture ||
			artifact.ArchiveFormat != releaseTarget.format ||
			artifact.Binary != releaseTarget.binary {
			return fmt.Errorf(
				"manifest artifact %d does not match expected %s/%s identity",
				index,
				releaseTarget.os,
				releaseTarget.architecture,
			)
		}
		if !checksumPattern.MatchString(artifact.SHA256) || artifact.Size <= 0 {
			return fmt.Errorf("manifest artifact %q has invalid file identity", artifact.Name)
		}
	}
	if len(manifest.Bootstraps) != len(releaseBootstraps) {
		return fmt.Errorf(
			"manifest has %d bootstraps, want %d",
			len(manifest.Bootstraps),
			len(releaseBootstraps),
		)
	}
	for index, expected := range releaseBootstraps {
		bootstrap := manifest.Bootstraps[index]
		if bootstrap.Name != expected.name || bootstrap.OS != expected.os {
			return fmt.Errorf("manifest bootstrap %d does not match expected identity", index)
		}
		if !checksumPattern.MatchString(bootstrap.SHA256) || bootstrap.Size <= 0 {
			return fmt.Errorf("manifest bootstrap %q has invalid file identity", bootstrap.Name)
		}
	}
	return nil
}

func verifyExactFiles(root string, expected map[string]bool) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read release directory: %w", err)
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !expected[entry.Name()] {
			return fmt.Errorf("unexpected release file %q", entry.Name())
		}
		info, err := os.Lstat(filepath.Join(root, entry.Name()))
		if err != nil {
			return fmt.Errorf("inspect release file %q: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release file %q must be a regular file", entry.Name())
		}
		seen[entry.Name()] = true
	}
	for name := range expected {
		if !seen[name] {
			return fmt.Errorf("missing release file %q", name)
		}
	}
	return nil
}

func readChecksums(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' || bytes.Contains(data, []byte{'\r'}) {
		return nil, errors.New("checksums must be non-empty canonical LF-terminated text")
	}
	values := make(map[string]string)
	var previous string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		digest, name, found := strings.Cut(line, "  ")
		if !found ||
			!checksumPattern.MatchString(digest) ||
			name == "" ||
			name != filepath.Base(name) ||
			strings.Contains(name, `\`) {
			return nil, fmt.Errorf("invalid checksum line %q", line)
		}
		if previous != "" && name <= previous {
			return nil, errors.New("checksum entries must be unique and sorted")
		}
		if _, exists := values[name]; exists {
			return nil, fmt.Errorf("duplicate checksum entry %q", name)
		}
		values[name] = digest
		previous = name
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	return values, nil
}

func verifyFileIdentity(
	root,
	name,
	expectedDigest string,
	expectedSize int64,
	checksums map[string]string,
) error {
	checksum, exists := checksums[name]
	if !exists {
		return fmt.Errorf("missing checksum for %q", name)
	}
	if checksum != expectedDigest {
		return fmt.Errorf("checksum identity mismatch for %q", name)
	}
	digest, size, err := fileIdentity(filepath.Join(root, name))
	if err != nil {
		return err
	}
	if digest != expectedDigest {
		return fmt.Errorf("checksum mismatch for %q: expected %s, got %s", name, expectedDigest, digest)
	}
	if size != expectedSize {
		return fmt.Errorf("size mismatch for %q: expected %d, got %d", name, expectedSize, size)
	}
	return nil
}

func fileIdentity(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func verifyArchive(path string, artifact Artifact) error {
	switch artifact.ArchiveFormat {
	case "tar.gz":
		return verifyTarArchive(path, artifact.Binary)
	case "zip":
		return verifyZipArchive(path, artifact.Binary)
	default:
		return fmt.Errorf("unsupported archive format %q", artifact.ArchiveFormat)
	}
}

func verifyTarArchive(path, binaryName string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var entries int
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		entries++
		if entries > 1 {
			return errors.New("archive must contain exactly one entry")
		}
		if header.Name != binaryName {
			return fmt.Errorf("archive binary name = %q, want %q", header.Name, binaryName)
		}
		if header.Typeflag != tar.TypeReg || header.Size <= 0 {
			return errors.New("archive binary must be a non-empty regular file")
		}
	}
	if entries != 1 {
		return errors.New("archive must contain exactly one entry")
	}
	return nil
}

func verifyZipArchive(path, binaryName string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer reader.Close()
	if len(reader.File) != 1 {
		return errors.New("archive must contain exactly one entry")
	}
	entry := reader.File[0]
	if entry.Name != binaryName {
		return fmt.Errorf("archive binary name = %q, want %q", entry.Name, binaryName)
	}
	if !entry.Mode().IsRegular() || entry.UncompressedSize64 == 0 {
		return errors.New("archive binary must be a non-empty regular file")
	}
	return nil
}
