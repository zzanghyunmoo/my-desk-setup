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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	exactartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

type Vendor struct {
	Client   *http.Client
	Home     string
	Platform string
	Arch     string
}

func (vendor Vendor) downloadNPMTarball(
	ctx context.Context,
	artifact catalog.NPMArtifact,
) (string, func() error, error) {
	client, err := ReviewedHTTPClient(
		vendor.Client,
		artifact.Tarball,
		5*time.Minute,
	)
	if err != nil {
		return "", nil, fmt.Errorf("validate npm tarball URL: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		artifact.Tarball,
		nil,
	)
	if err != nil {
		return "", nil, fmt.Errorf("create npm tarball request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", nil, fmt.Errorf("download npm tarball: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf(
			"download npm tarball: HTTP %s",
			response.Status,
		)
	}
	if response.Request == nil ||
		response.Request.URL.String() != artifact.Tarball {
		return "", nil, errors.New(
			"npm tarball redirected away from the reviewed canonical URL",
		)
	}
	temporaryDirectory, err := os.MkdirTemp("", "mds-npm-*")
	if err != nil {
		return "", nil, fmt.Errorf("create npm temporary directory: %w", err)
	}
	cleanup := func() error {
		if err := os.RemoveAll(temporaryDirectory); err != nil {
			return fmt.Errorf("remove npm temporary directory: %w", err)
		}
		return nil
	}
	path := filepath.Join(temporaryDirectory, "package.tgz")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", nil, errors.Join(
			fmt.Errorf("create npm tarball: %w", err),
			cleanup(),
		)
	}
	_, verifyErr := exactartifact.CopyAndVerify(
		response.Body,
		file,
		artifact.SHA256,
		artifact.Integrity,
		exactartifact.MaxDownloadBytes,
	)
	if verifyErr == nil {
		verifyErr = file.Sync()
	}
	closeErr := file.Close()
	if verifyErr != nil {
		verifyErr = fmt.Errorf("verify npm tarball: %w", verifyErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close npm tarball: %w", closeErr)
	}
	if writeErr := errors.Join(verifyErr, closeErr); writeErr != nil {
		return "", nil, errors.Join(writeErr, cleanup())
	}
	return path, cleanup, nil
}

func (vendor Vendor) Install(
	ctx context.Context,
	component catalog.Component,
	lock catalog.LockEntry,
) (returnErr error) {
	key := vendor.Platform + "-" + vendor.Arch
	artifact, exists := lock.Artifacts[key]
	if !exists {
		return fmt.Errorf("component %s has no artifact for %s", component.ID, key)
	}
	if vendor.Home == "" {
		return errors.New("home directory is required for vendor installation")
	}
	client, err := reviewedReleaseHTTPClient(
		vendor.Client,
		artifact.URL,
		5*time.Minute,
	)
	if err != nil {
		return fmt.Errorf("validate reviewed artifact URL: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return fmt.Errorf("create artifact request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download reviewed artifact: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download reviewed artifact: HTTP %s", response.Status)
	}
	if response.Request == nil || !safeReleaseRedirectURL(response.Request.URL) {
		return errors.New(
			"artifact redirect did not remain credential-free HTTPS",
		)
	}
	temporaryDirectory, err := os.MkdirTemp("", "mds-artifact-*")
	if err != nil {
		return fmt.Errorf("create artifact temporary directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(temporaryDirectory); err != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("remove artifact temporary directory: %w", err),
			)
		}
	}()
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
	if artifact.ExecutableSHA256 != "" {
		if _, err := exactVendorExecutableDigest(sourcePath, artifact); err != nil {
			return err
		}
	}
	name, err := vendorExecutableName(component, artifact, vendor.Platform)
	if err != nil {
		return err
	}
	return installExecutable(sourcePath, filepath.Join(vendor.Home, ".local", "bin", name))
}

func exactVendorExecutableDigest(
	path string,
	artifact catalog.Artifact,
) (string, error) {
	digest, err := exactartifact.SHA256File(path)
	if err != nil {
		return "", fmt.Errorf("hash extracted executable: %w", err)
	}
	if artifact.ExecutableSHA256 != "" && digest != artifact.ExecutableSHA256 {
		return digest, fmt.Errorf(
			"executable digest mismatch: expected %s got %s",
			artifact.ExecutableSHA256,
			digest,
		)
	}
	return digest, nil
}

func vendorExecutableName(
	component catalog.Component,
	artifact catalog.Artifact,
	platform string,
) (string, error) {
	name := filepath.Base(artifact.Executable)
	if component.Kind == "agent" {
		if len(component.Verification.Command) == 0 {
			return "", fmt.Errorf("agent %s has no verification command", component.ID)
		}
		name = component.Verification.Command[0]
		if !adapters.ValidExecutableName(name) {
			return "", fmt.Errorf("agent %s has invalid executable name %q", component.ID, name)
		}
	}
	if platform == "windows" {
		if !strings.HasSuffix(strings.ToLower(name), ".exe") {
			name += ".exe"
		}
	} else {
		name = strings.TrimSuffix(name, ".exe")
	}
	return name, nil
}

func ReviewedHTTPClient(
	base *http.Client,
	reviewed string,
	maxTimeout time.Duration,
) (*http.Client, error) {
	expected, err := url.ParseRequestURI(reviewed)
	if err != nil || expected.Scheme != "https" || expected.Host == "" ||
		expected.User != nil || expected.RawQuery != "" ||
		expected.Fragment != "" {
		return nil, errors.New(
			"artifact requires an absolute credential-free HTTPS URL without a query or fragment",
		)
	}
	if maxTimeout <= 0 {
		return nil, errors.New("artifact client timeout must be positive")
	}
	client := http.Client{Timeout: maxTimeout}
	if base != nil {
		client = *base
		if client.Timeout <= 0 || client.Timeout > maxTimeout {
			client.Timeout = maxTimeout
		}
	}
	previousCheck := client.CheckRedirect
	client.CheckRedirect = func(
		request *http.Request,
		via []*http.Request,
	) error {
		if request.URL.User != nil ||
			request.URL.Scheme != expected.Scheme ||
			!strings.EqualFold(request.URL.Host, expected.Host) {
			return errors.New("cross-origin artifact redirect is not allowed")
		}
		if previousCheck != nil {
			return previousCheck(request, via)
		}
		if len(via) >= 10 {
			return errors.New("too many artifact redirects")
		}
		return nil
	}
	return &client, nil
}

func reviewedReleaseHTTPClient(
	base *http.Client,
	reviewed string,
	maxTimeout time.Duration,
) (*http.Client, error) {
	if _, err := ReviewedHTTPClient(nil, reviewed, maxTimeout); err != nil {
		return nil, err
	}
	client := http.Client{Timeout: maxTimeout}
	if base != nil {
		client = *base
		if client.Timeout <= 0 || client.Timeout > maxTimeout {
			client.Timeout = maxTimeout
		}
	}
	client.Jar = nil
	previousCheck := client.CheckRedirect
	client.CheckRedirect = func(
		request *http.Request,
		via []*http.Request,
	) error {
		if !safeReleaseRedirectURL(request.URL) {
			return errors.New(
				"release artifact redirect must remain credential-free HTTPS",
			)
		}
		if len(via) > 3 {
			return errors.New("too many release artifact redirects")
		}
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		request.Header.Del("Proxy-Authorization")
		if previousCheck != nil {
			return previousCheck(request, via)
		}
		return nil
	}
	return &client, nil
}

func safeReleaseRedirectURL(value *url.URL) bool {
	return value != nil &&
		value.Scheme == "https" &&
		value.Host != "" &&
		value.User == nil &&
		value.Fragment == ""
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
	defer func() {
		_ = archive.Close()
	}()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return "", fmt.Errorf("open artifact gzip stream: %w", err)
	}
	defer func() {
		_ = compressed.Close()
	}()
	reader := tar.NewReader(io.LimitReader(compressed, exactartifact.MaxDownloadBytes+1))
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
		if header.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("artifact executable %s is not regular", executable)
		}
		path := filepath.Join(destination, "executable")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return "", fmt.Errorf("create extracted executable: %w", err)
		}
		written, copyErr := io.Copy(
			file,
			io.LimitReader(reader, exactartifact.MaxDownloadBytes+1),
		)
		closeErr := file.Close()
		if copyErr != nil {
			copyErr = fmt.Errorf("extract executable: %w", copyErr)
		}
		if closeErr != nil {
			closeErr = fmt.Errorf("close extracted executable: %w", closeErr)
		}
		if writeErr := errors.Join(copyErr, closeErr); writeErr != nil {
			return "", writeErr
		}
		if written > exactartifact.MaxDownloadBytes {
			return "", fmt.Errorf(
				"extracted executable exceeds %d bytes",
				exactartifact.MaxDownloadBytes,
			)
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
	written, copyErr := io.Copy(
		io.MultiWriter(file, hash),
		io.LimitReader(reader, exactartifact.MaxDownloadBytes+1),
	)
	closeErr := file.Close()
	if copyErr != nil {
		copyErr = fmt.Errorf("download artifact: %w", copyErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close artifact: %w", closeErr)
	}
	if writeErr := errors.Join(copyErr, closeErr); writeErr != nil {
		return writeErr
	}
	if written > exactartifact.MaxDownloadBytes {
		return fmt.Errorf(
			"artifact exceeds %d bytes",
			exactartifact.MaxDownloadBytes,
		)
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
	defer func() {
		_ = archive.Close()
	}()
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
		path := filepath.Join(destination, "executable")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return "", errors.Join(
				fmt.Errorf("create extracted executable: %w", err),
				wrapError("close artifact executable", reader.Close()),
			)
		}
		written, copyErr := io.Copy(
			file,
			io.LimitReader(reader, exactartifact.MaxDownloadBytes+1),
		)
		closeErr := file.Close()
		readerCloseErr := reader.Close()
		if copyErr != nil {
			copyErr = fmt.Errorf("extract executable: %w", copyErr)
		}
		if closeErr != nil {
			closeErr = fmt.Errorf("close extracted executable: %w", closeErr)
		}
		if writeErr := errors.Join(
			copyErr,
			closeErr,
			wrapError("close artifact executable", readerCloseErr),
		); writeErr != nil {
			return "", writeErr
		}
		if written > exactartifact.MaxDownloadBytes {
			return "", fmt.Errorf(
				"extracted executable exceeds %d bytes",
				exactartifact.MaxDownloadBytes,
			)
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
	defer func() {
		_ = sourceFile.Close()
	}()
	temporary, err := os.CreateTemp(directory, ".mds-executable-*")
	if err != nil {
		return fmt.Errorf("create executable temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryOpen := true
	cleanup := func() error {
		var cleanupErrors []error
		if temporaryOpen {
			temporaryOpen = false
			if err := temporary.Close(); err != nil {
				cleanupErrors = append(
					cleanupErrors,
					fmt.Errorf(
						"close executable temporary file during cleanup: %w",
						err,
					),
				)
			}
		}
		if err := os.Remove(temporaryPath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(
				cleanupErrors,
				fmt.Errorf("remove executable temporary file: %w", err),
			)
		}
		return errors.Join(cleanupErrors...)
	}
	failWithCleanup := func(operationErr error) error {
		return errors.Join(operationErr, cleanup())
	}
	if err := temporary.Chmod(0o700); err != nil {
		return failWithCleanup(
			fmt.Errorf("chmod executable temporary file: %w", err),
		)
	}
	if _, err := io.Copy(temporary, sourceFile); err != nil {
		return failWithCleanup(fmt.Errorf("copy executable: %w", err))
	}
	if err := temporary.Sync(); err != nil {
		return failWithCleanup(fmt.Errorf("sync executable: %w", err))
	}
	temporaryOpen = false
	if err := temporary.Close(); err != nil {
		return errors.Join(
			fmt.Errorf("close executable temporary file: %w", err),
			cleanup(),
		)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return errors.Join(
			fmt.Errorf("publish executable: %w", err),
			cleanup(),
		)
	}
	return nil
}

func wrapError(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
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
