package packages

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/managedfile"
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
		if !adapters.ValidExecutableName(executable) {
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
				"exec " + adapters.ShellSingleQuote(source) + ` "$@"`,
				"",
			}, "\n"),
		})
	}
	return specs, nil
}

func observeLaunchers(specs []launcherSpec) adapters.Observation {
	for _, spec := range specs {
		inspection := managedfile.Inspect(spec.path, spec.content)
		switch inspection.State {
		case managedfile.StateMissing:
			return adapters.Observation{State: adapters.StateAbsent}
		case managedfile.StateConflict:
			return adapters.Observation{
				State: adapters.StateConflict,
				Detail: managedLauncherConflictDetail(
					inspection.Conflict,
					inspection.Err,
				),
			}
		}
	}
	return adapters.Observation{State: adapters.StateReady}
}

func publishLaunchers(specs []launcherSpec) error {
	for _, spec := range specs {
		if err := managedfile.Publish(spec.path, spec.content); err != nil {
			var conflict *managedfile.ConflictError
			if errors.As(err, &conflict) {
				return errors.New(
					managedLauncherConflictDetail(conflict.Kind, conflict.Err),
				)
			}
			return fmt.Errorf("publish launcher without overwrite: %w", err)
		}
	}
	return nil
}

func managedLauncherConflictDetail(
	conflict managedfile.ConflictKind,
	err error,
) string {
	switch conflict {
	case managedfile.ConflictInspect:
		return "inspect managed launcher: " + err.Error()
	case managedfile.ConflictNonRegular:
		return "managed launcher destination is not a regular file"
	default:
		return "existing launcher is user-owned; it will not be overwritten"
	}
}
