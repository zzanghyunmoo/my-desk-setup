package release

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
	"github.com/zzanghyunmoo/my-desk-setup/internal/evidence"
	"github.com/zzanghyunmoo/my-desk-setup/internal/safefile"
)

const (
	maximumEvidenceArchiveBytes int64  = 32 << 20
	maximumEvidenceEntryBytes   uint64 = 8 << 20
)

var evidenceArchiveEntries = []string{
	evidence.ChecksumsFile,
	evidence.DoctorFile,
	evidence.ManifestFile,
	evidence.PlanFile,
}

func writeEvidenceArchive(path, bundle string) (returnErr error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create evidence archive: %w", err)
	}
	defer func() {
		if err := file.Close(); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close evidence archive: %w", err)
		}
	}()
	writer := zip.NewWriter(file)
	for _, name := range evidenceArchiveEntries {
		data, err := safefile.ReadRegularNoFollow(
			filepath.Join(bundle, name),
			int64(maximumEvidenceEntryBytes),
		)
		if err != nil {
			_ = writer.Close()
			return fmt.Errorf("snapshot evidence file %s: %w", name, err)
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			return fmt.Errorf("create evidence archive entry %s: %w", name, err)
		}
		if _, err := entry.Write(data); err != nil {
			_ = writer.Close()
			return fmt.Errorf("write evidence archive entry %s: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finalize evidence archive: %w", err)
	}
	return nil
}

func evidenceArchiveBytes(path string) ([]byte, error) {
	data, err := safefile.ReadRegularNoFollow(path, maximumEvidenceArchiveBytes)
	if err != nil {
		return nil, fmt.Errorf("snapshot evidence archive: %w", err)
	}
	return data, nil
}

func bytesSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func verifyArchivedEvidenceBytes(
	archive []byte,
	expectedCLIRevision,
	expectedCatalogRevision,
	expectedCohort string,
	promoted PromotedTarget,
) error {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return fmt.Errorf("open evidence archive: %w", err)
	}
	root, err := extractEvidenceArchive(reader, "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	manifest, err := evidence.Verify(root, evidence.VerifyOptions{
		ExpectedCLIRevision:     expectedCLIRevision,
		ExpectedCatalogRevision: expectedCatalogRevision,
		ExpectedPlanDigest:      promoted.PlanDigest,
		ExpectedTargetID:        promoted.ID,
		ExpectedBinarySHA256:    promoted.BinarySHA256,
		ExpectedCohort:          expectedCohort,
		RequireVerified:         true,
	})
	if err != nil {
		return err
	}
	if string(manifest.CapturedAtUnix) != strconv.FormatInt(
		promoted.CapturedAtUnix,
		10,
	) {
		return errors.New(
			"evidence archive capture timestamp does not match promotion report",
		)
	}
	return nil
}

// ExtractEvidenceArtifact validates and extracts one bounded GitHub Actions
// artifact containing the exact target-evidence file set.
func ExtractEvidenceArtifact(archivePath, outputDir string) (returnErr error) {
	if outputDir == "" {
		return errors.New("evidence output directory is required")
	}
	if _, err := os.Lstat(outputDir); err == nil {
		return fmt.Errorf("evidence output directory already exists: %s", outputDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect evidence output directory: %w", err)
	}
	archive, err := evidenceArchiveBytes(archivePath)
	if err != nil {
		return err
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return fmt.Errorf("open evidence artifact: %w", err)
	}
	parent := filepath.Dir(outputDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create evidence output parent: %w", err)
	}
	extracted, err := extractEvidenceArchive(reader, parent)
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(extracted); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("remove evidence extraction staging: %w", err)
		}
	}()
	if err := durable.PublishDirectory(extracted, outputDir); err != nil {
		return fmt.Errorf("publish evidence artifact: %w", err)
	}
	return nil
}

func extractEvidenceArchive(reader *zip.Reader, parent string) (string, error) {
	expected := make(map[string]bool, len(evidenceArchiveEntries))
	for _, name := range evidenceArchiveEntries {
		expected[name] = true
	}
	if len(reader.File) != len(expected) {
		return "", fmt.Errorf(
			"evidence archive contains %d entries, want %d",
			len(reader.File),
			len(expected),
		)
	}
	pattern := "mds-promotion-evidence-*"
	if parent != "" {
		pattern = ".mds-evidence.staging-*"
	}
	root, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		return "", fmt.Errorf("create evidence extraction directory: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(root)
		}
	}()
	var total uint64
	for _, entry := range reader.File {
		if !expected[entry.Name] ||
			entry.FileInfo().IsDir() ||
			entry.Mode()&os.ModeSymlink != 0 ||
			!entry.Mode().IsRegular() ||
			entry.UncompressedSize64 > maximumEvidenceEntryBytes ||
			total > uint64(maximumEvidenceArchiveBytes)-entry.UncompressedSize64 {
			return "", fmt.Errorf(
				"evidence archive contains invalid or oversized entry %q",
				entry.Name,
			)
		}
		total += entry.UncompressedSize64
		delete(expected, entry.Name)
		source, err := entry.Open()
		if err != nil {
			return "", fmt.Errorf("open evidence archive entry %s: %w", entry.Name, err)
		}
		destination, err := os.OpenFile(
			filepath.Join(root, entry.Name),
			os.O_CREATE|os.O_EXCL|os.O_WRONLY,
			0o600,
		)
		if err != nil {
			_ = source.Close()
			return "", fmt.Errorf(
				"create evidence archive entry %s: %w",
				entry.Name,
				err,
			)
		}
		written, copyErr := io.Copy(
			destination,
			io.LimitReader(source, int64(maximumEvidenceEntryBytes)+1),
		)
		closeDestinationErr := destination.Close()
		closeSourceErr := source.Close()
		if copyErr != nil ||
			written < 0 ||
			uint64(written) != entry.UncompressedSize64 ||
			uint64(written) > maximumEvidenceEntryBytes {
			return "", fmt.Errorf(
				"read evidence archive entry %s within bounds",
				entry.Name,
			)
		}
		if closeDestinationErr != nil || closeSourceErr != nil {
			return "", fmt.Errorf("close evidence archive entry %s", entry.Name)
		}
	}
	if len(expected) != 0 {
		return "", errors.New("evidence archive is missing required entries")
	}
	cleanup = false
	return root, nil
}
