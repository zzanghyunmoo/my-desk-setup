// Package managedfile owns the filesystem lifecycle for exact-content files
// published by adapters.
package managedfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// State describes whether a managed file is missing, exactly managed, or in
// conflict with filesystem content that the application does not own.
type State uint8

const (
	StateMissing State = iota
	StateReady
	StateConflict
)

// ConflictKind identifies why an existing path cannot be treated as managed.
type ConflictKind uint8

const (
	ConflictNone ConflictKind = iota
	ConflictInspect
	ConflictNonRegular
	ConflictRead
	ConflictContent
)

// Inspection is the ownership result for an expected exact-content file.
type Inspection struct {
	State    State
	Conflict ConflictKind
	Err      error
}

// ConflictError reports an ownership conflict discovered while publishing.
type ConflictError struct {
	Kind ConflictKind
	Err  error
}

func (err *ConflictError) Error() string {
	switch err.Kind {
	case ConflictInspect:
		return "inspect managed file: " + err.Err.Error()
	case ConflictNonRegular:
		return "managed file destination is not a regular file"
	case ConflictRead:
		return "read managed file: " + err.Err.Error()
	case ConflictContent:
		return "existing managed file is user-owned; it will not be overwritten"
	default:
		return "managed file ownership conflict"
	}
}

func (err *ConflictError) Unwrap() error {
	return err.Err
}

// Inspect checks ownership without following symlinks. Only a regular file
// with byte-for-byte expected content is considered managed.
func Inspect(path, expected string) Inspection {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Inspection{State: StateMissing}
	}
	if err != nil {
		return Inspection{
			State:    StateConflict,
			Conflict: ConflictInspect,
			Err:      err,
		}
	}
	if !info.Mode().IsRegular() {
		return Inspection{
			State:    StateConflict,
			Conflict: ConflictNonRegular,
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Inspection{
			State:    StateConflict,
			Conflict: ConflictRead,
			Err:      err,
		}
	}
	if string(content) != expected {
		return Inspection{
			State:    StateConflict,
			Conflict: ConflictContent,
		}
	}
	return Inspection{State: StateReady}
}

// Publish creates an executable managed file without replacing any path that
// appears between inspection and publication.
func Publish(path, expected string) error {
	inspection := Inspect(path, expected)
	switch inspection.State {
	case StateReady:
		return nil
	case StateConflict:
		return &ConflictError{
			Kind: inspection.Conflict,
			Err:  inspection.Err,
		}
	}

	directory := filepath.Dir(path)
	if err := ensureDirectory(directory); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".mds-managed-file-*")
	if err != nil {
		return fmt.Errorf("create managed file temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o700); err != nil {
		cleanup()
		return fmt.Errorf("chmod managed file: %w", err)
	}
	if _, err := temporary.WriteString(expected); err != nil {
		cleanup()
		return fmt.Errorf("write managed file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync managed file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close managed file: %w", err)
	}
	if err := os.Link(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("publish managed file without overwrite: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove managed file temporary file: %w", err)
	}
	return nil
}

func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create managed file directory: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect managed file directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed file directory is a symlink: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("managed file directory is not a directory: %s", path)
	}
	return nil
}
