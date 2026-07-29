package state

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Paths struct {
	Root       string
	TargetRoot string
	Lock       string
	Journal    string
	Receipts   string
}

func NewPaths(root, targetID string) (Paths, error) {
	if root == "" {
		return Paths{}, errors.New("state root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve state root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return Paths{}, errors.New("filesystem root cannot be used as state root")
	}
	if strings.TrimSpace(targetID) == "" {
		return Paths{}, errors.New("target ID is required")
	}
	targetRoot := filepath.Join(absolute, targetDirectory(targetID))
	return Paths{
		Root:       absolute,
		TargetRoot: targetRoot,
		Lock:       filepath.Join(targetRoot, "writer.lock"),
		Journal:    filepath.Join(targetRoot, "journal.jsonl"),
		Receipts:   filepath.Join(targetRoot, "receipts"),
	}, nil
}

func (paths Paths) Ensure() error {
	for _, path := range []string{paths.Root, paths.TargetRoot, paths.Receipts} {
		if err := ensureDirectory(path); err != nil {
			return err
		}
	}
	for _, path := range []string{paths.Lock, paths.Journal} {
		if err := ensureRegularOrMissing(path); err != nil {
			return err
		}
	}
	return nil
}

func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("state path %s is a symlink", path)
		}
		if !info.IsDir() {
			return fmt.Errorf("state path %s is not a directory", path)
		}
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("inspect state path %s: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create state directory %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("restrict state directory %s: %w", path, err)
	}
	return nil
}

func ensureRegularOrMissing(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect state file %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("state file %s must be regular and not a symlink", path)
	}
	return nil
}

func targetDirectory(targetID string) string {
	readable := strings.NewReplacer(
		":", "_",
		"/", "_",
		"\\", "_",
		" ", "_",
	).Replace(targetID)
	sum := sha256.Sum256([]byte(targetID))
	return readable + "-" + hex.EncodeToString(sum[:6])
}

func ReceiptFilename(digest string) string {
	return strings.ReplaceAll(digest, ":", "-") + ".json"
}
