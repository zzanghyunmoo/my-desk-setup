package guest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// inspectDirectoryBelow checks every existing component without following a
// symlink. The home directory is a trust boundary for all guest-local managed
// paths.
func inspectDirectoryBelow(root, target string) (bool, error) {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return false, fmt.Errorf("managed directory %s escapes root %s", target, root)
	}
	current := root
	components := []string{}
	if relative != "." {
		components = strings.Split(relative, string(os.PathSeparator))
	}
	for index := -1; index < len(components); index++ {
		if index >= 0 {
			current = filepath.Join(current, components[index])
		}
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return false, nil
		}
		if statErr != nil {
			return false, fmt.Errorf("inspect managed directory %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false, fmt.Errorf("managed directory %s is not a regular directory", current)
		}
	}
	return true, nil
}

func ensureDirectoryBelow(root, target string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("managed directory %s escapes root %s", target, root)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect managed root %s: %w", root, err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("managed root %s is not a regular directory", root)
	}
	if relative == "." {
		return nil
	}
	current := root
	for _, component := range strings.Split(relative, string(os.PathSeparator)) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			if err := os.Mkdir(current, 0o700); err != nil {
				return fmt.Errorf("create managed directory %s: %w", current, err)
			}
		case statErr != nil:
			return fmt.Errorf("inspect managed directory %s: %w", current, statErr)
		case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
			return fmt.Errorf("managed directory %s is not a regular directory", current)
		}
	}
	return nil
}

func readRegularFile(path, description string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", description)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", description, err)
	}
	return content, nil
}
