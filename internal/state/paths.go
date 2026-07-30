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
	absolute, err := absoluteStateRoot(root)
	if err != nil {
		return Paths{}, err
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

func CatalogLockPath(stateRoot, catalogRoot string) (string, error) {
	absoluteState, err := absoluteStateRoot(stateRoot)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(catalogRoot) == "" {
		return "", errors.New("catalog root is required")
	}
	absoluteCatalog, err := filepath.Abs(catalogRoot)
	if err != nil {
		return "", fmt.Errorf("resolve catalog root: %w", err)
	}
	absoluteCatalog = filepath.Clean(absoluteCatalog)
	sum := sha256.Sum256([]byte(absoluteCatalog))
	return filepath.Join(
		absoluteState,
		"catalog-"+hex.EncodeToString(sum[:])+".writer.lock",
	), nil
}

func (paths Paths) Ensure() error {
	if err := paths.EnsureRoot(); err != nil {
		return err
	}
	for _, path := range []string{paths.TargetRoot, paths.Receipts} {
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

func (paths Paths) EnsureRoot() error {
	return ensureDirectory(paths.Root)
}

func absoluteStateRoot(root string) (string, error) {
	if root == "" {
		return "", errors.New("state root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve state root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return "", errors.New("filesystem root cannot be used as state root")
	}
	return absolute, nil
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

func PartialReceiptFilename(digest string) string {
	return strings.ReplaceAll(digest, ":", "-") + ".partial.json"
}
