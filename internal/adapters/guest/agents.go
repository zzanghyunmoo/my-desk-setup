package guest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

// Agent installs an exact Bun-managed agent and publishes a stable launcher
// that disables agent-owned background updates. It never runs authentication.
type Agent struct {
	Home     string
	Delegate adapters.Component
}

func (agent Agent) Observe(
	ctx context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	if agent.Delegate == nil {
		return adapters.Observation{}, errors.New("agent delegate is required")
	}
	path, content, err := agent.launcher(action)
	if err != nil {
		return adapters.Observation{}, err
	}
	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		observation, observeErr := agent.Delegate.Observe(ctx, action)
		if observeErr != nil {
			return adapters.Observation{}, observeErr
		}
		if observation.State == adapters.StateConflict {
			return observation, nil
		}
		return adapters.Observation{State: adapters.StateAbsent}, nil
	}
	if err != nil {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "agent launcher exists but is not a readable managed file",
		}, nil
	}
	if string(existing) != content {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "existing agent launcher is user-owned; it will not be overwritten",
		}, nil
	}
	return agent.Delegate.Observe(ctx, action)
}

func (agent Agent) Apply(
	ctx context.Context,
	action planning.Action,
) error {
	if agent.Delegate == nil {
		return errors.New("agent delegate is required")
	}
	observation, err := agent.Delegate.Observe(ctx, action)
	if err != nil {
		return err
	}
	switch observation.State {
	case adapters.StateAbsent:
		if err := agent.Delegate.Apply(ctx, action); err != nil {
			return err
		}
	case adapters.StateConflict:
		return fmt.Errorf("agent package conflict: %s", observation.Detail)
	}
	path, content, err := agent.launcher(action)
	if err != nil {
		return err
	}
	if err := ensureDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("inspect agent launcher: %w", err)
		}
		existing, readErr := os.ReadFile(path)
		if readErr == nil && string(existing) == content {
			return nil
		}
		return errors.New("existing agent launcher will not be overwritten")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".mds-agent-*")
	if err != nil {
		return fmt.Errorf("create agent launcher temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o700); err != nil {
		cleanup()
		return fmt.Errorf("chmod agent launcher: %w", err)
	}
	if _, err := temporary.WriteString(content); err != nil {
		cleanup()
		return fmt.Errorf("write agent launcher: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync agent launcher: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close agent launcher: %w", err)
	}
	if err := os.Link(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("publish agent launcher: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove agent launcher temporary file: %w", err)
	}
	return nil
}

func (agent Agent) Verify(
	ctx context.Context,
	action planning.Action,
) error {
	observation, err := agent.Observe(ctx, action)
	if err != nil {
		return err
	}
	if observation.State != adapters.StateReady {
		return fmt.Errorf("agent launcher is not ready: %s", observation.Detail)
	}
	return agent.Delegate.Verify(ctx, action)
}

func (agent Agent) launcher(action planning.Action) (string, string, error) {
	if agent.Home == "" {
		return "", "", errors.New("agent home directory is required")
	}
	if len(action.Verification) == 0 || len(action.Verification[0]) == 0 {
		return "", "", errors.New("agent verification command is required")
	}
	executable := action.Verification[0][0]
	if strings.ContainsAny(executable, `/\`) || executable == "." || executable == ".." {
		return "", "", fmt.Errorf("invalid agent executable %q", executable)
	}
	underlying := filepath.Join(agent.Home, ".local", "share", "bun", "bin", executable)
	content := strings.Join([]string{
		"#!/bin/sh",
		"# Managed by my-desk-setup. Authentication remains user-owned.",
		"export DISABLE_AUTOUPDATER=1",
		"export OPENCODE_DISABLE_AUTOUPDATE=1",
		"exec " + shellSingleQuote(underlying) + ` "$@"`,
		"",
	}, "\n")
	return filepath.Join(agent.Home, ".local", "bin", executable), content, nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
