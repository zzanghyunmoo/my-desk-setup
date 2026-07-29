package host

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

// Desktop uses the native app probe on macOS and exact WinGet inventory on
// Windows. It never launches an app or authenticates.
type Desktop struct {
	Platform string
	Port     transport.Port
	Delegate adapters.Component
}

func (desktop Desktop) Observe(
	ctx context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	if desktop.Port == nil || desktop.Delegate == nil {
		return adapters.Observation{}, errors.New("desktop adapter requires port and delegate")
	}
	if desktop.Platform == "darwin" {
		applications := map[string]string{
			"notion-desktop": "Notion",
			"linear-desktop": "Linear",
			"slack":          "Slack",
			"kakaotalk":      "KakaoTalk",
			"chrome":         "Google Chrome",
		}
		name := applications[action.ComponentID]
		if name == "" {
			return adapters.Observation{}, fmt.Errorf(
				"unknown macOS desktop component %q",
				action.ComponentID,
			)
		}
		if _, err := desktop.Port.Run(ctx, transport.Command{
			Executable: "open",
			Arguments:  []string{"-Ra", name},
		}); err != nil {
			return adapters.Observation{State: adapters.StateAbsent}, nil
		}
		return adapters.Observation{
			State: adapters.StateReady, InstalledVersion: "manager-owned",
		}, nil
	}
	if desktop.Platform != "windows" {
		return adapters.Observation{}, fmt.Errorf(
			"desktop adapter does not support platform %q",
			desktop.Platform,
		)
	}
	result, err := desktop.Port.Run(ctx, transport.Command{
		Executable: "winget",
		Arguments: []string{
			"list", "--id", action.Package, "--exact",
			"--disable-interactivity", "--accept-source-agreements",
		},
	})
	if err != nil {
		return adapters.Observation{State: adapters.StateAbsent}, nil
	}
	output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	if !strings.Contains(strings.ToLower(output), strings.ToLower(action.Package)) {
		return adapters.Observation{State: adapters.StateAbsent}, nil
	}
	return adapters.Observation{
		State: adapters.StateReady, InstalledVersion: "manager-owned",
	}, nil
}

func (desktop Desktop) Apply(
	ctx context.Context,
	action planning.Action,
) error {
	return desktop.Delegate.Apply(ctx, action)
}

func (desktop Desktop) Verify(
	ctx context.Context,
	action planning.Action,
) error {
	observation, err := desktop.Observe(ctx, action)
	if err != nil {
		return err
	}
	if observation.State != adapters.StateReady {
		return fmt.Errorf("desktop package %s is not installed", action.Package)
	}
	return nil
}
