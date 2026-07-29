package packages

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

const maxArtifactSize = 512 << 20

type Vendor struct {
	Client   *http.Client
	Home     string
	Platform string
	Arch     string
}

func (vendor Vendor) Install(
	ctx context.Context,
	component catalog.Component,
	lock catalog.LockEntry,
) error {
	key := vendor.Platform + "-" + vendor.Arch
	artifact, exists := lock.Artifacts[key]
	if !exists {
		return fmt.Errorf("component %s has no artifact for %s", component.ID, key)
	}
	if vendor.Home == "" {
		return errors.New("home directory is required for vendor installation")
	}
	client := vendor.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return fmt.Errorf("create artifact request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download %s: %w", artifact.URL, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %s", artifact.URL, response.Status)
	}
	temporaryDirectory, err := os.MkdirTemp("", "mds-artifact-*")
	if err != nil {
		return fmt.Errorf("create artifact temporary directory: %w", err)
	}
	defer os.RemoveAll(temporaryDirectory)
	downloadPath := filepath.Join(temporaryDirectory, "artifact")
	if err := DownloadAndVerify(
		response.Body,
		downloadPath,
		artifact.SHA256,
	); err != nil {
		return err
	}

	sourcePath := downloadPath
	switch artifact.Format {
	case "zip":
		sourcePath, err = extractZipExecutable(
			downloadPath,
			temporaryDirectory,
			artifact.Executable,
		)
		if err != nil {
			return err
		}
	case "tar.gz":
		sourcePath, err = extractTarGzExecutable(
			downloadPath,
			temporaryDirectory,
			artifact.Executable,
		)
		if err != nil {
			return err
		}
	}
	name := filepath.Base(artifact.Executable)
	if vendor.Platform != "windows" {
		name = strings.TrimSuffix(name, ".exe")
	}
	return installExecutable(sourcePath, filepath.Join(vendor.Home, ".local", "bin", name))
}

func extractTarGzExecutable(
	archivePath,
	destination,
	executable string,
) (string, error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open artifact tar.gz: %w", err)
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return "", fmt.Errorf("open artifact gzip stream: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(io.LimitReader(compressed, maxArtifactSize+1))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read artifact tar.gz: %w", err)
		}
		if filepath.ToSlash(header.Name) != filepath.ToSlash(executable) {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return "", fmt.Errorf("artifact executable %s is not regular", executable)
		}
		path := filepath.Join(destination, "executable")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return "", fmt.Errorf("create extracted executable: %w", err)
		}
		written, copyErr := io.Copy(file, io.LimitReader(reader, maxArtifactSize+1))
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("extract executable: %w", copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close extracted executable: %w", closeErr)
		}
		if written > maxArtifactSize {
			return "", fmt.Errorf("extracted executable exceeds %d bytes", maxArtifactSize)
		}
		return path, nil
	}
	return "", fmt.Errorf("artifact does not contain executable %s", executable)
}

func DownloadAndVerify(reader io.Reader, path, expected string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create artifact file: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(reader, maxArtifactSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("download artifact: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close artifact: %w", closeErr)
	}
	if written > maxArtifactSize {
		return fmt.Errorf("artifact exceeds %d bytes", maxArtifactSize)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("artifact checksum mismatch: expected %s got %s", expected, actual)
	}
	return nil
}

func extractZipExecutable(
	archivePath,
	destination,
	executable string,
) (string, error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open artifact zip: %w", err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if filepath.ToSlash(entry.Name) != filepath.ToSlash(executable) {
			continue
		}
		if entry.FileInfo().IsDir() {
			return "", fmt.Errorf("artifact executable %s is a directory", executable)
		}
		reader, err := entry.Open()
		if err != nil {
			return "", fmt.Errorf("open artifact executable: %w", err)
		}
		defer reader.Close()
		path := filepath.Join(destination, "executable")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return "", fmt.Errorf("create extracted executable: %w", err)
		}
		written, copyErr := io.Copy(file, io.LimitReader(reader, maxArtifactSize+1))
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("extract executable: %w", copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close extracted executable: %w", closeErr)
		}
		if written > maxArtifactSize {
			return "", fmt.Errorf("extracted executable exceeds %d bytes", maxArtifactSize)
		}
		return path, nil
	}
	return "", fmt.Errorf("artifact does not contain executable %s", executable)
}

func installExecutable(source, destination string) error {
	directory := filepath.Dir(destination)
	if err := ensureSafeDirectory(directory); err != nil {
		return err
	}
	if err := ensureSafeExecutableOrMissing(destination); err != nil {
		return err
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source executable: %w", err)
	}
	defer sourceFile.Close()
	temporary, err := os.CreateTemp(directory, ".mds-executable-*")
	if err != nil {
		return fmt.Errorf("create executable temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o700); err != nil {
		cleanup()
		return fmt.Errorf("chmod executable temporary file: %w", err)
	}
	if _, err := io.Copy(temporary, sourceFile); err != nil {
		cleanup()
		return fmt.Errorf("copy executable: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync executable: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close executable temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("publish executable: %w", err)
	}
	return nil
}

func ensureSafeDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create executable directory: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect executable directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("executable directory %s must be a directory and not a symlink", path)
	}
	return nil
}

func ensureSafeExecutableOrMissing(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect executable destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("executable destination %s must be regular and not a symlink", path)
	}
	return nil
}
