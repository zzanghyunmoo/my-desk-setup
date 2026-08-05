package artifact

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultSnapshotEntries         = 20_000
	defaultSnapshotCompressedBytes = MaxDownloadBytes
	defaultSnapshotFileBytes       = 256 << 20
	defaultSnapshotTotalBytes      = 1 << 30
	ownershipFilename              = ".mds-snapshot-owner"
)

// OpenSnapshotURL is an injection seam for deterministic, network-free tests.
// Production callers should leave it nil so Acquire enforces HTTPS itself.
type OpenSnapshotURL func(context.Context, string) (io.ReadCloser, error)

type SnapshotLimits struct {
	CompressedBytes int64
	FileBytes       int64
	TotalBytes      int64
	Entries         int
}

type SnapshotRequest struct {
	URL              string
	SHA256           string
	Format           string
	Executable       string
	ExecutableSHA256 string
	// Alias publishes a private executable copy with a stable command name.
	Alias      string
	ExtractAll bool
}

type Snapshotter struct {
	Open     OpenSnapshotURL
	TempRoot string
	Limits   SnapshotLimits
	Client   *http.Client
}

// Snapshot owns exactly one private child directory created by Acquire.
// Paths are intentionally omitted from serialized artifact identities.
type Snapshot struct {
	ArchiveSHA256    string `json:"archive_sha256"`
	ExecutableSHA256 string `json:"executable_sha256"`

	ownedRoot     string
	root          string
	executable    string
	ownershipPath string
	ownerToken    string
	mu            sync.Mutex
	closed        bool
}

func (snapshot *Snapshot) Root() string {
	if snapshot == nil {
		return ""
	}
	return snapshot.root
}

func (snapshot *Snapshot) Executable() string {
	if snapshot == nil {
		return ""
	}
	return snapshot.executable
}

// Path resolves a canonical archive-relative regular-file path. An unsafe
// relative path returns an empty string rather than escaping the snapshot.
func (snapshot *Snapshot) Path(relative string) string {
	if snapshot == nil || snapshot.root == "" {
		return ""
	}
	canonical, err := safeArchivePath(relative)
	if err != nil {
		return ""
	}
	return filepath.Join(snapshot.root, filepath.FromSlash(canonical))
}

func (snapshot *Snapshot) Close() error {
	if snapshot == nil {
		return nil
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if snapshot.closed {
		return nil
	}
	marker, err := os.ReadFile(snapshot.ownershipPath)
	if err != nil {
		return fmt.Errorf("verify snapshot cleanup ownership: %w", err)
	}
	want := []byte(snapshot.ownerToken + "\n")
	if len(marker) != len(want) || subtle.ConstantTimeCompare(marker, want) != 1 {
		return errors.New("verify snapshot cleanup ownership: marker mismatch")
	}
	if err := os.RemoveAll(snapshot.ownedRoot); err != nil {
		return fmt.Errorf("remove owned snapshot root: %w", err)
	}
	snapshot.closed = true
	return nil
}

func (snapshotter Snapshotter) Acquire(
	ctx context.Context,
	request SnapshotRequest,
) (_ *Snapshot, returnErr error) {
	if err := validateSnapshotRequest(request); err != nil {
		return nil, err
	}
	limits, err := normalizeSnapshotLimits(snapshotter.Limits)
	if err != nil {
		return nil, err
	}
	opener := snapshotter.Open
	if opener == nil {
		if err := validateProductionSnapshotURL(request.URL); err != nil {
			return nil, err
		}
		opener = snapshotter.productionOpener(limits.CompressedBytes)
	}

	ownedRoot, err := os.MkdirTemp(snapshotter.TempRoot, "mds-snapshot-*")
	if err != nil {
		return nil, fmt.Errorf("create snapshot root: %w", err)
	}
	if err := os.Chmod(ownedRoot, 0o700); err != nil {
		_ = os.RemoveAll(ownedRoot)
		return nil, fmt.Errorf("protect snapshot root: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			returnErr = errors.Join(returnErr, cleanupFailedSnapshot(ownedRoot))
		}
	}()

	ownerToken, err := randomOwnerToken()
	if err != nil {
		return nil, err
	}
	ownershipPath := filepath.Join(ownedRoot, ownershipFilename)
	if err := os.WriteFile(ownershipPath, []byte(ownerToken+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write snapshot ownership marker: %w", err)
	}
	archivePath := filepath.Join(ownedRoot, "archive")
	archiveDigest, err := downloadSnapshotArchive(
		ctx,
		opener,
		request.URL,
		archivePath,
		limits.CompressedBytes,
	)
	if err != nil {
		return nil, err
	}
	if archiveDigest != request.SHA256 {
		return nil, fmt.Errorf(
			"archive digest mismatch: expected %s got %s",
			request.SHA256,
			archiveDigest,
		)
	}

	contentRoot := filepath.Join(ownedRoot, "content")
	if err := os.Mkdir(contentRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create snapshot content root: %w", err)
	}
	var executablePath string
	switch request.Format {
	case "tar.gz":
		executablePath, err = extractSnapshotTarGz(
			ctx, archivePath, contentRoot, request, limits,
		)
	case "zip":
		executablePath, err = extractSnapshotZip(
			ctx, archivePath, contentRoot, request, limits,
		)
	default:
		return nil, fmt.Errorf("unsupported archive format %q", request.Format)
	}
	if err != nil {
		return nil, err
	}
	executableDigest, err := SHA256File(executablePath)
	if err != nil {
		return nil, fmt.Errorf("hash snapshot executable: %w", err)
	}
	if request.ExecutableSHA256 != "" && executableDigest != request.ExecutableSHA256 {
		return nil, fmt.Errorf(
			"executable digest mismatch: expected %s got %s",
			request.ExecutableSHA256,
			executableDigest,
		)
	}
	if request.Alias != "" {
		executablePath, err = publishExecutableAlias(
			ctx,
			contentRoot,
			executablePath,
			request.Alias,
		)
		if err != nil {
			return nil, err
		}
	}
	if err := os.Chmod(executablePath, 0o700); err != nil {
		return nil, fmt.Errorf("protect snapshot executable: %w", err)
	}

	snapshot := &Snapshot{
		ArchiveSHA256:    archiveDigest,
		ExecutableSHA256: executableDigest,
		ownedRoot:        ownedRoot,
		root:             contentRoot,
		executable:       executablePath,
		ownershipPath:    ownershipPath,
		ownerToken:       ownerToken,
	}
	succeeded = true
	return snapshot, nil
}

func validateSnapshotRequest(request SnapshotRequest) error {
	if request.URL == "" {
		return errors.New("snapshot URL is required")
	}
	if err := ValidateSHA256(request.SHA256); err != nil {
		return fmt.Errorf("snapshot archive SHA-256: %w", err)
	}
	if request.ExecutableSHA256 != "" {
		if err := ValidateSHA256(request.ExecutableSHA256); err != nil {
			return fmt.Errorf("snapshot executable SHA-256: %w", err)
		}
	}
	if request.Format != "tar.gz" && request.Format != "zip" {
		return fmt.Errorf("unsupported archive format %q", request.Format)
	}
	if _, err := safeArchivePath(request.Executable); err != nil {
		return fmt.Errorf("invalid executable path: %w", err)
	}
	if request.Alias != "" {
		if request.Alias != filepath.Base(request.Alias) ||
			strings.ContainsAny(request.Alias, "/\\\x00") ||
			request.Alias == "." || request.Alias == ".." {
			return fmt.Errorf("invalid executable alias %q", request.Alias)
		}
	}
	return nil
}

func normalizeSnapshotLimits(value SnapshotLimits) (SnapshotLimits, error) {
	if value.CompressedBytes == 0 {
		value.CompressedBytes = defaultSnapshotCompressedBytes
	}
	if value.FileBytes == 0 {
		value.FileBytes = defaultSnapshotFileBytes
	}
	if value.TotalBytes == 0 {
		value.TotalBytes = defaultSnapshotTotalBytes
	}
	if value.Entries == 0 {
		value.Entries = defaultSnapshotEntries
	}
	if value.CompressedBytes < 0 || value.FileBytes < 0 ||
		value.TotalBytes < 0 {
		return SnapshotLimits{}, errors.New("snapshot byte limits must be positive")
	}
	if value.Entries < 0 {
		return SnapshotLimits{}, errors.New("snapshot entry count limit must be positive")
	}
	return value, nil
}

func validateProductionSnapshotURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New(
			"snapshot requires an absolute credential-free HTTPS URL without a query or fragment",
		)
	}
	return nil
}

func (snapshotter Snapshotter) productionOpener(
	compressedLimit int64,
) OpenSnapshotURL {
	return func(ctx context.Context, value string) (io.ReadCloser, error) {
		client := &http.Client{Timeout: 5 * time.Minute}
		if snapshotter.Client != nil {
			copy := *snapshotter.Client
			client = &copy
			client.Jar = nil
			if client.Timeout <= 0 || client.Timeout > 5*time.Minute {
				client.Timeout = 5 * time.Minute
			}
		}
		priorRedirect := client.CheckRedirect
		client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
			if request.URL == nil || request.URL.Scheme != "https" ||
				request.URL.Host == "" || request.URL.User != nil ||
				request.URL.Fragment != "" {
				return errors.New("snapshot redirect is not credential-free HTTPS")
			}
			request.Header.Del("Authorization")
			request.Header.Del("Cookie")
			request.Header.Del("Proxy-Authorization")
			if len(via) > 3 {
				return errors.New("too many snapshot redirects")
			}
			if priorRedirect != nil {
				return priorRedirect(request, via)
			}
			return nil
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
		if err != nil {
			return nil, fmt.Errorf("create snapshot request: %w", err)
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("download snapshot: %w", err)
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			return nil, fmt.Errorf("download snapshot: HTTP %s", response.Status)
		}
		if response.ContentLength > compressedLimit {
			_ = response.Body.Close()
			return nil, fmt.Errorf("snapshot exceeds compressed size limit %d", compressedLimit)
		}
		return response.Body, nil
	}
}

func randomOwnerToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return "", fmt.Errorf("create snapshot ownership token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func downloadSnapshotArchive(
	ctx context.Context,
	opener OpenSnapshotURL,
	value,
	destination string,
	limit int64,
) (string, error) {
	reader, err := opener(ctx, value)
	if err != nil {
		return "", fmt.Errorf("open snapshot URL: %w", err)
	}
	defer reader.Close()
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create snapshot archive: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(
		io.MultiWriter(file, hash),
		io.LimitReader(contextReader{ctx: ctx, reader: reader}, limit+1),
	)
	if copyErr == nil && written > limit {
		copyErr = fmt.Errorf("snapshot exceeds compressed size limit %d", limit)
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		copyErr = fmt.Errorf("copy snapshot archive: %w", copyErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close snapshot archive: %w", closeErr)
	}
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func safeArchivePath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') ||
		strings.ContainsRune(value, '\\') || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("unsafe archive path %q", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
		strings.Contains(clean, ":") {
		return "", fmt.Errorf("unsafe archive path %q", value)
	}
	return clean, nil
}

type extractionBudget struct {
	limits SnapshotLimits
	seen   map[string]struct{}
	count  int
	total  int64
}

func newExtractionBudget(limits SnapshotLimits) *extractionBudget {
	return &extractionBudget{limits: limits, seen: make(map[string]struct{})}
}

func (budget *extractionBudget) admit(name string, size int64) (string, error) {
	canonical, err := safeArchivePath(name)
	if err != nil {
		return "", err
	}
	if _, duplicate := budget.seen[canonical]; duplicate {
		return "", fmt.Errorf("duplicate archive path %q", canonical)
	}
	budget.seen[canonical] = struct{}{}
	budget.count++
	if budget.count > budget.limits.Entries {
		return "", fmt.Errorf("archive exceeds entry count limit %d", budget.limits.Entries)
	}
	if size < 0 || size > budget.limits.FileBytes {
		return "", fmt.Errorf("archive file exceeds file size limit %d", budget.limits.FileBytes)
	}
	if size > budget.limits.TotalBytes-budget.total {
		return "", fmt.Errorf("archive exceeds total extracted size limit %d", budget.limits.TotalBytes)
	}
	budget.total += size
	return canonical, nil
}

func extractSnapshotTarGz(
	ctx context.Context,
	archivePath,
	contentRoot string,
	request SnapshotRequest,
	limits SnapshotLimits,
) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open snapshot tar.gz: %w", err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("open snapshot gzip stream: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	budget := newExtractionBudget(limits)
	executablePath := ""
	for {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("extract snapshot tar.gz: %w", err)
		}
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return "", fmt.Errorf("read snapshot tar.gz: %w", nextErr)
		}
		size := header.Size
		if header.Typeflag == tar.TypeDir {
			size = 0
		}
		canonical, err := budget.admit(header.Name, size)
		if err != nil {
			return "", err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if request.ExtractAll {
				if err := os.MkdirAll(
					filepath.Join(contentRoot, filepath.FromSlash(canonical)),
					0o700,
				); err != nil {
					return "", fmt.Errorf("create snapshot directory: %w", err)
				}
			}
		case tar.TypeReg, tar.TypeRegA:
			if !request.ExtractAll && canonical != request.Executable {
				continue
			}
			destination := filepath.Join(contentRoot, filepath.FromSlash(canonical))
			if err := writeSnapshotFile(ctx, destination, reader, header.Size); err != nil {
				return "", err
			}
			if canonical == request.Executable {
				executablePath = destination
			}
		default:
			if !request.ExtractAll && canonical != request.Executable {
				continue
			}
			return "", fmt.Errorf(
				"unsupported archive entry %q with type %d",
				canonical,
				header.Typeflag,
			)
		}
	}
	if executablePath == "" {
		return "", fmt.Errorf("archive does not contain executable %s", request.Executable)
	}
	return executablePath, nil
}

func extractSnapshotZip(
	ctx context.Context,
	archivePath,
	contentRoot string,
	request SnapshotRequest,
	limits SnapshotLimits,
) (string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open snapshot zip: %w", err)
	}
	defer reader.Close()
	budget := newExtractionBudget(limits)
	executablePath := ""
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("extract snapshot zip: %w", err)
		}
		mode := entry.Mode()
		isDirectory := entry.FileInfo().IsDir()
		size := int64(entry.UncompressedSize64)
		if isDirectory {
			size = 0
		}
		canonical, err := budget.admit(entry.Name, size)
		if err != nil {
			return "", err
		}
		if !isDirectory && !mode.IsRegular() {
			if !request.ExtractAll && canonical != request.Executable {
				continue
			}
			return "", fmt.Errorf("unsupported archive entry %q", canonical)
		}
		if isDirectory {
			if request.ExtractAll {
				if err := os.MkdirAll(
					filepath.Join(contentRoot, filepath.FromSlash(canonical)),
					0o700,
				); err != nil {
					return "", fmt.Errorf("create snapshot directory: %w", err)
				}
			}
			continue
		}
		if !request.ExtractAll && canonical != request.Executable {
			continue
		}
		source, err := entry.Open()
		if err != nil {
			return "", fmt.Errorf("open snapshot zip entry %q: %w", canonical, err)
		}
		destination := filepath.Join(contentRoot, filepath.FromSlash(canonical))
		writeErr := writeSnapshotFile(ctx, destination, source, size)
		closeErr := source.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return "", err
		}
		if canonical == request.Executable {
			executablePath = destination
		}
	}
	if executablePath == "" {
		return "", fmt.Errorf("archive does not contain executable %s", request.Executable)
	}
	return executablePath, nil
}

func writeSnapshotFile(
	ctx context.Context,
	destination string,
	source io.Reader,
	expected int64,
) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create snapshot file parent: %w", err)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create snapshot file: %w", err)
	}
	written, copyErr := io.Copy(
		file,
		io.LimitReader(contextReader{ctx: ctx, reader: source}, expected+1),
	)
	if copyErr == nil && written != expected {
		copyErr = fmt.Errorf("snapshot entry size mismatch: expected %d got %d", expected, written)
	}
	// Extracted entries are ephemeral children of an already-synced,
	// digest-verified archive. Syncing every member makes small packages with
	// thousands of files take minutes without adding a durability guarantee the
	// temporary snapshot needs.
	closeErr := file.Close()
	if copyErr != nil {
		copyErr = fmt.Errorf("write snapshot file: %w", copyErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close snapshot file: %w", closeErr)
	}
	return errors.Join(copyErr, closeErr)
}

func publishExecutableAlias(
	ctx context.Context,
	root,
	source,
	alias string,
) (string, error) {
	directory := filepath.Join(root, ".mds-bin")
	if err := os.Mkdir(directory, 0o700); err != nil {
		return "", fmt.Errorf("create snapshot executable directory: %w", err)
	}
	destination := filepath.Join(directory, alias)
	input, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open snapshot executable: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return "", fmt.Errorf("stat snapshot executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("snapshot executable is not a regular file")
	}
	if err := writeSnapshotFile(ctx, destination, input, info.Size()); err != nil {
		return "", fmt.Errorf("publish snapshot executable alias: %w", err)
	}
	return destination, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func cleanupFailedSnapshot(root string) error {
	if root == "" {
		return nil
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("clean failed snapshot: %w", err)
	}
	return nil
}

// SHA256File returns the canonical lowercase digest of a regular file without
// following a symlink at its final path.
func SHA256File(value string) (string, error) {
	info, err := os.Lstat(value)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("snapshot executable is not a regular file")
	}
	file, err := os.Open(value)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
