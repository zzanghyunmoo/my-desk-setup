package packages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

var homebrewCasks = map[string]bool{
	"chrome":         true,
	"kakaotalk":      true,
	"linear-desktop": true,
	"notion-desktop": true,
	"slack":          true,
	"wezterm":        true,
}

// HomebrewPrerequisite converts a missing host package manager into an
// explicit operator step. It intentionally does not bootstrap Homebrew or
// perform authentication on the user's behalf.
type HomebrewPrerequisite struct {
	Port     transport.Port
	Delegate adapters.Component
}

func (component HomebrewPrerequisite) Observe(
	ctx context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	if action.Installer != "brew" {
		return component.delegate().Observe(ctx, action)
	}
	if err := component.check(ctx); err != nil {
		if commandMissing(err) {
			return adapters.Observation{
				State:  adapters.StateAbsent,
				Detail: "Homebrew prerequisite is missing",
			}, nil
		}
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "inspect Homebrew prerequisite: " + err.Error(),
		}, nil
	}
	return component.delegate().Observe(ctx, action)
}

func (component HomebrewPrerequisite) Apply(
	ctx context.Context,
	action planning.Action,
) error {
	if action.Installer == "brew" {
		if err := component.check(ctx); err != nil {
			if commandMissing(err) {
				return missingHomebrewError()
			}
			return fmt.Errorf("inspect Homebrew prerequisite: %w", err)
		}
	}
	return component.delegate().Apply(ctx, action)
}

func (component HomebrewPrerequisite) Verify(
	ctx context.Context,
	action planning.Action,
) error {
	if action.Installer == "brew" {
		if err := component.check(ctx); err != nil {
			if commandMissing(err) {
				return missingHomebrewError()
			}
			return fmt.Errorf("inspect Homebrew prerequisite: %w", err)
		}
	}
	return component.delegate().Verify(ctx, action)
}

func (component HomebrewPrerequisite) check(ctx context.Context) error {
	if component.Port == nil {
		return errors.New("homebrew prerequisite transport is required")
	}
	_, err := component.Port.Run(ctx, transport.Command{
		Executable: "brew",
		Arguments:  []string{"--version"},
	})
	return err
}

func (component HomebrewPrerequisite) delegate() adapters.Component {
	if component.Delegate == nil {
		return missingComponent{}
	}
	return component.Delegate
}

func commandMissing(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}

func missingHomebrewError() error {
	return &adapters.ActionRequiredError{
		Reason: "install Homebrew from https://brew.sh, ensure `brew` is on PATH, then rerun the same mds apply",
	}
}

type missingComponent struct{}

func (missingComponent) Observe(
	context.Context,
	planning.Action,
) (adapters.Observation, error) {
	return adapters.Observation{}, errors.New("homebrew prerequisite delegate is required")
}

func (missingComponent) Apply(context.Context, planning.Action) error {
	return errors.New("homebrew prerequisite delegate is required")
}

func (missingComponent) Verify(context.Context, planning.Action) error {
	return errors.New("homebrew prerequisite delegate is required")
}

func HomebrewInstall(action planning.Action) (transport.Command, error) {
	if action.Package == "" {
		return transport.Command{}, fmt.Errorf("homebrew package is required for %s", action.ID)
	}
	arguments := []string{"install"}
	if homebrewCasks[action.ComponentID] {
		arguments = append(arguments, "--cask")
	}
	arguments = append(arguments, action.Package)
	return transport.Command{
		Executable: "brew",
		Arguments:  arguments,
		Environment: map[string]string{
			"HOMEBREW_NO_AUTO_UPDATE":     "1",
			"HOMEBREW_NO_INSTALL_UPGRADE": "1",
			"HOMEBREW_NO_ENV_HINTS":       "1",
			"HOMEBREW_NO_ANALYTICS":       "1",
		},
		Timeout: 45 * time.Minute,
	}, nil
}
