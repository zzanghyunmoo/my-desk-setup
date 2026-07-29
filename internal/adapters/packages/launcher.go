package packages

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

type launcherSpec struct {
	path    string
	content string
}

func (adapter Adapter) launcherSpecs(
	action planning.Action,
	component catalog.Component,
) ([]launcherSpec, error) {
	if component.Kind == "agent" ||
		(action.Installer != "bun" && action.Installer != "mise") {
		return nil, nil
	}
	if adapter.Home == "" {
		return nil, errors.New("home directory is required for managed launchers")
	}
	seen := make(map[string]bool)
	var specs []launcherSpec
	for _, argv := range action.Verification {
		if len(argv) == 0 {
			continue
		}
		executable := argv[0]
		if executable == "bun" || executable == "mise" || seen[executable] {
			continue
		}
		if strings.ContainsAny(executable, `/\`) ||
			executable == "." ||
			executable == ".." {
			return nil, fmt.Errorf("invalid managed executable %q", executable)
		}
		seen[executable] = true
		var source string
		switch action.Installer {
		case "bun":
			source = filepath.Join(
				adapter.Home,
				".local", "share", "bun", "bin", executable,
			)
		case "mise":
			source = filepath.Join(
				adapter.Home,
				".local", "share", "mise", "shims", executable,
			)
		}
		specs = append(specs, launcherSpec{
			path: filepath.Join(adapter.Home, ".local", "bin", executable),
			content: strings.Join([]string{
				"#!/bin/sh",
				"# Managed by my-desk-setup.",
				"exec " + shellSingleQuote(source) + ` "$@"`,
				"",
			}, "\n"),
		})
	}
	return specs, nil
}

func observeLaunchers(specs []launcherSpec) adapters.Observation {
	for _, spec := range specs {
		info, err := os.Lstat(spec.path)
		if errors.Is(err, os.ErrNotExist) {
			return adapters.Observation{State: adapters.StateAbsent}
		}
		if err != nil {
			return adapters.Observation{
				State:  adapters.StateConflict,
				Detail: "inspect managed launcher: " + err.Error(),
			}
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return adapters.Observation{
				State:  adapters.StateConflict,
				Detail: "managed launcher destination is not a regular file",
			}
		}
		content, err := os.ReadFile(spec.path)
		if err != nil || string(content) != spec.content {
			return adapters.Observation{
				State:  adapters.StateConflict,
				Detail: "existing launcher is user-owned; it will not be overwritten",
			}
		}
	}
	return adapters.Observation{State: adapters.StateReady}
}

func publishLaunchers(specs []launcherSpec) error {
	for _, spec := range specs {
		observation := observeLaunchers([]launcherSpec{spec})
		switch observation.State {
		case adapters.StateReady:
			continue
		case adapters.StateConflict:
			return errors.New(observation.Detail)
		}
		if err := ensureSafeDirectory(filepath.Dir(spec.path)); err != nil {
			return err
		}
		temporary, err := os.CreateTemp(filepath.Dir(spec.path), ".mds-launcher-*")
		if err != nil {
			return fmt.Errorf("create launcher temporary file: %w", err)
		}
		temporaryPath := temporary.Name()
		cleanup := func() {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
		if err := temporary.Chmod(0o700); err != nil {
			cleanup()
			return fmt.Errorf("chmod launcher: %w", err)
		}
		if _, err := temporary.WriteString(spec.content); err != nil {
			cleanup()
			return fmt.Errorf("write launcher: %w", err)
		}
		if err := temporary.Sync(); err != nil {
			cleanup()
			return fmt.Errorf("sync launcher: %w", err)
		}
		if err := temporary.Close(); err != nil {
			_ = os.Remove(temporaryPath)
			return fmt.Errorf("close launcher: %w", err)
		}
		if err := os.Link(temporaryPath, spec.path); err != nil {
			_ = os.Remove(temporaryPath)
			return fmt.Errorf("publish launcher without overwrite: %w", err)
		}
		if err := os.Remove(temporaryPath); err != nil {
			return fmt.Errorf("remove launcher temporary file: %w", err)
		}
	}
	return nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
