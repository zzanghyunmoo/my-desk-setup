package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/zzanghyunmoo/my-desk-setup/internal/safefile"
)

const maxProductionBinaryBytes int64 = 256 << 20

type productionBinarySnapshot struct {
	Path   string
	SHA256 string
	root   string
}

func snapshotProductionBinary(path string) (productionBinarySnapshot, error) {
	source, size, err := safefile.OpenRegularNoFollow(path)
	if err != nil {
		return productionBinarySnapshot{}, fmt.Errorf(
			"open mds binary safely: %w",
			err,
		)
	}
	defer source.Close()
	if runtime.GOOS != "windows" {
		info, statErr := source.Stat()
		if statErr != nil {
			return productionBinarySnapshot{}, fmt.Errorf(
				"inspect mds binary execute mode: %w",
				statErr,
			)
		}
		if info.Mode().Perm()&0o111 == 0 {
			return productionBinarySnapshot{}, errors.New(
				"mds binary is not executable",
			)
		}
	}
	if size < 0 || size > maxProductionBinaryBytes {
		return productionBinarySnapshot{}, errors.New(
			"mds binary exceeds the 256 MiB limit",
		)
	}

	root, err := os.MkdirTemp("", "mds-certification-binary-*")
	if err != nil {
		return productionBinarySnapshot{}, fmt.Errorf(
			"create private mds binary snapshot directory: %w",
			err,
		)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(root)
		}
	}()
	if err := os.Chmod(root, 0o700); err != nil {
		return productionBinarySnapshot{}, fmt.Errorf(
			"restrict mds binary snapshot directory: %w",
			err,
		)
	}

	name := "mds"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	snapshotPath := filepath.Join(root, name)
	destination, err := os.OpenFile(
		snapshotPath,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o700,
	)
	if err != nil {
		return productionBinarySnapshot{}, fmt.Errorf(
			"create private mds binary snapshot: %w",
			err,
		)
	}

	hash := sha256.New()
	written, copyErr := io.Copy(
		io.MultiWriter(destination, hash),
		io.LimitReader(source, maxProductionBinaryBytes+1),
	)
	syncErr := destination.Sync()
	closeErr := destination.Close()
	if copyErr != nil {
		return productionBinarySnapshot{}, fmt.Errorf(
			"copy mds binary snapshot: %w",
			copyErr,
		)
	}
	if syncErr != nil {
		return productionBinarySnapshot{}, fmt.Errorf(
			"sync mds binary snapshot: %w",
			syncErr,
		)
	}
	if closeErr != nil {
		return productionBinarySnapshot{}, fmt.Errorf(
			"close mds binary snapshot: %w",
			closeErr,
		)
	}
	if written != size || written > maxProductionBinaryBytes {
		return productionBinarySnapshot{}, errors.New(
			"mds binary changed while its private snapshot was created",
		)
	}
	if err := os.Chmod(snapshotPath, 0o700); err != nil {
		return productionBinarySnapshot{}, fmt.Errorf(
			"restrict mds binary snapshot: %w",
			err,
		)
	}

	cleanup = false
	return productionBinarySnapshot{
		Path:   snapshotPath,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
		root:   root,
	}, nil
}

func (snapshot productionBinarySnapshot) Remove() {
	_ = os.RemoveAll(snapshot.root)
}

func hashRegularFile(path string) (string, error) {
	snapshot, err := snapshotProductionBinary(path)
	if err != nil {
		return "", err
	}
	defer snapshot.Remove()
	return snapshot.SHA256, nil
}
