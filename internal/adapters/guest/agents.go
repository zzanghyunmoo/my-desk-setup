package guest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/managedfile"
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
	inspection := managedfile.Inspect(path, content)
	switch inspection.State {
	case managedfile.StateMissing:
		observation, observeErr := agent.Delegate.Observe(ctx, action)
		if observeErr != nil {
			return adapters.Observation{}, observeErr
		}
		if observation.State == adapters.StateConflict {
			return observation, nil
		}
		return adapters.Observation{State: adapters.StateAbsent}, nil
	case managedfile.StateConflict:
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: agentLauncherConflictDetail(inspection.Conflict),
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
	if err := managedfile.Publish(path, content); err != nil {
		var conflict *managedfile.ConflictError
		if errors.As(err, &conflict) {
			return errors.New("existing agent launcher will not be overwritten")
		}
		return fmt.Errorf("publish agent launcher: %w", err)
	}
	return nil
}

func agentLauncherConflictDetail(conflict managedfile.ConflictKind) string {
	if conflict == managedfile.ConflictContent {
		return "existing agent launcher is user-owned; it will not be overwritten"
	}
	return "agent launcher exists but is not a readable managed file"
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
	if !adapters.ValidExecutableName(executable) {
		return "", "", fmt.Errorf("invalid agent executable %q", executable)
	}
	underlying := filepath.Join(agent.Home, ".local", "share", "bun", "bin", executable)
	content := strings.Join([]string{
		"#!/bin/sh",
		"# Managed by my-desk-setup. Authentication remains user-owned.",
		"export DISABLE_AUTOUPDATER=1",
		"export OPENCODE_DISABLE_AUTOUPDATE=1",
		"exec " + adapters.ShellSingleQuote(underlying) + ` "$@"`,
		"",
	}, "\n")
	return filepath.Join(agent.Home, ".local", "bin", executable), content, nil
}
