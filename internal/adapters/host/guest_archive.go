package host

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"

	"github.com/zzanghyunmoo/my-desk-setup/internal/safefile"
)

const maxGuestBootstrapArchiveBytes int64 = 256 << 20

func loadGuestBootstrapArchive(
	path,
	expectedSHA256 string,
) (snapshot []byte, resultErr error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New(
			"guest bootstrap archive must use an absolute path",
		)
	}
	file, size, err := safefile.OpenRegularNoFollow(path)
	if err != nil {
		return nil, errors.New(
			"guest bootstrap archive cannot be opened safely",
		)
	}
	defer func() {
		if err := file.Close(); err != nil && resultErr == nil {
			snapshot = nil
			resultErr = errors.New(
				"guest bootstrap archive cannot be closed safely",
			)
		}
	}()
	if size < 0 || size > maxGuestBootstrapArchiveBytes {
		return nil, errors.New(
			"guest bootstrap archive exceeds the 256 MiB limit",
		)
	}
	snapshot, err = io.ReadAll(io.LimitReader(
		file,
		maxGuestBootstrapArchiveBytes+1,
	))
	if err != nil {
		return nil, errors.New(
			"guest bootstrap archive cannot be read safely",
		)
	}
	if int64(len(snapshot)) > maxGuestBootstrapArchiveBytes {
		return nil, errors.New(
			"guest bootstrap archive exceeds the 256 MiB limit",
		)
	}
	sum := sha256.Sum256(snapshot)
	if hex.EncodeToString(sum[:]) != expectedSHA256 {
		return nil, errors.New(
			"guest bootstrap archive does not match the embedded SHA-256",
		)
	}
	return snapshot, nil
}
