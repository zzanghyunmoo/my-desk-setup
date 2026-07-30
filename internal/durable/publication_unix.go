//go:build !windows

package durable

import (
	"os"
	"path/filepath"
)

func renameDurably(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	return SyncDirectory(filepath.Dir(destination))
}

func replaceFileDurably(source, destination string) error {
	return renameDurably(source, destination)
}

func publishFileNoReplaceDurably(source, destination string) error {
	if err := os.Link(source, destination); err != nil {
		return err
	}
	directory := filepath.Dir(destination)
	if err := SyncDirectory(directory); err != nil {
		return err
	}
	if err := os.Remove(source); err != nil {
		return err
	}
	return SyncDirectory(directory)
}

func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = directory.Close()
	}()
	return directory.Sync()
}
