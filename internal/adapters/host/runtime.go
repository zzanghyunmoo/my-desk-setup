package host

import (
	"context"
	"errors"
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const defaultGuestName = "mds"

// GuestRuntime owns only host-side WSL/Lima lifecycle. Linux component
// reconciliation still runs through mds inside the guest.
type GuestRuntime struct {
	HostOS       string
	Architecture string
	Port         transport.Port
	Delegate     adapters.Component
	Spec         guest.Spec
}

func (runtime GuestRuntime) Observe(
	ctx context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	if runtime.Port == nil || runtime.Delegate == nil {
		return adapters.Observation{}, errors.New("guest runtime requires port and delegate")
	}
	base, err := runtime.Delegate.Observe(ctx, action)
	if err != nil || base.State != adapters.StateReady {
		return base, err
	}
	switch action.ComponentID {
	case "lima":
		instances, err := runtime.limaInstances(ctx)
		if err != nil {
			return adapters.Observation{
				State: adapters.StateConflict, Detail: err.Error(),
			}, nil
		}
		for _, instance := range instances {
			if instance.ID.Name != defaultGuestName {
				continue
			}
			if instance.Reachable {
				return base, nil
			}
			return adapters.Observation{
				State:  adapters.StateAbsent,
				Detail: "managed Lima guest exists but is stopped",
			}, nil
		}
		return adapters.Observation{
			State: adapters.StateAbsent, Detail: "managed Lima guest is absent",
		}, nil
	case "wsl":
		distributions, err := runtime.wslDistributions(ctx)
		if err != nil {
			return adapters.Observation{
				State: adapters.StateConflict, Detail: err.Error(),
			}, nil
		}
		if !hasTarget(distributions, target.KindWSLGuest, runtime.Spec.WSLDistribution) {
			return adapters.Observation{
				State: adapters.StateAbsent, Detail: "managed Ubuntu WSL guest is absent",
			}, nil
		}
		if _, err := runtime.Port.Run(ctx, transport.Command{
			Executable: "wsl.exe",
			Arguments: []string{
				"--distribution", runtime.Spec.WSLDistribution,
				"--exec", "true",
			},
		}); err != nil {
			return adapters.Observation{
				State:  adapters.StateAbsent,
				Detail: "managed Ubuntu WSL guest requires first-run completion",
			}, nil
		}
		return base, nil
	default:
		return adapters.Observation{}, fmt.Errorf(
			"unsupported guest runtime component %q",
			action.ComponentID,
		)
	}
}

func (runtime GuestRuntime) Apply(
	ctx context.Context,
	action planning.Action,
) error {
	if runtime.Port == nil || runtime.Delegate == nil {
		return errors.New("guest runtime requires port and delegate")
	}
	switch action.ComponentID {
	case "lima":
		return runtime.applyLima(ctx, action)
	case "wsl":
		return runtime.applyWSL(ctx, action)
	default:
		return fmt.Errorf("unsupported guest runtime component %q", action.ComponentID)
	}
}

func (runtime GuestRuntime) Verify(
	ctx context.Context,
	action planning.Action,
) error {
	if err := runtime.Delegate.Verify(ctx, action); err != nil {
		return err
	}
	observation, err := runtime.Observe(ctx, action)
	if err != nil {
		return err
	}
	if observation.State != adapters.StateReady {
		return fmt.Errorf("guest runtime is not ready: %s", observation.Detail)
	}
	return nil
}

func (runtime GuestRuntime) applyLima(
	ctx context.Context,
	action planning.Action,
) error {
	base, err := runtime.Delegate.Observe(ctx, action)
	if err != nil {
		return err
	}
	if base.State == adapters.StateConflict {
		return errors.New(base.Detail)
	}
	if base.State == adapters.StateAbsent {
		if err := runtime.Delegate.Apply(ctx, action); err != nil {
			return err
		}
	}
	instances, err := runtime.limaInstances(ctx)
	if err != nil {
		return err
	}
	for _, instance := range instances {
		if instance.ID.Name != defaultGuestName {
			continue
		}
		if instance.Reachable {
			return nil
		}
		_, err := runtime.Port.Run(ctx, transport.Command{
			Executable: "limactl",
			Arguments:  []string{"start", defaultGuestName},
		})
		return err
	}
	image, exists := runtime.Spec.Images[runtime.Architecture]
	if !exists {
		return fmt.Errorf("Ubuntu guest has no image for %q", runtime.Architecture)
	}
	for _, command := range []transport.Command{
		{
			Executable: "limactl",
			Arguments: []string{
				"create", "--name", defaultGuestName,
				"--set", ".images[0].location=" + image.URL,
				"--set", ".images[0].digest=sha256:" + image.SHA256,
			},
		},
		{
			Executable: "limactl",
			Arguments:  []string{"start", defaultGuestName},
		},
	} {
		if _, err := runtime.Port.Run(ctx, command); err != nil {
			return fmt.Errorf("prepare Lima Ubuntu guest: %w", err)
		}
	}
	return nil
}

func (runtime GuestRuntime) applyWSL(
	ctx context.Context,
	action planning.Action,
) error {
	base, err := runtime.Delegate.Observe(ctx, action)
	if err != nil {
		return err
	}
	if base.State == adapters.StateConflict {
		return errors.New(base.Detail)
	}
	if base.State == adapters.StateAbsent {
		if _, err := runtime.Port.Run(ctx, transport.Command{
			Executable: "wsl.exe",
			Arguments:  []string{"--install", "--no-distribution"},
		}); err != nil {
			return &adapters.ActionRequiredError{
				Reason: "enable WSL, reboot Windows if requested, then rerun the same apply",
			}
		}
	}
	distributions, err := runtime.wslDistributions(ctx)
	if err != nil {
		return err
	}
	if !hasTarget(distributions, target.KindWSLGuest, runtime.Spec.WSLDistribution) {
		if _, err := runtime.Port.Run(ctx, transport.Command{
			Executable: "wsl.exe",
			Arguments: []string{
				"--install", "--distribution", runtime.Spec.WSLDistribution,
				"--no-launch",
			},
		}); err != nil {
			return &adapters.ActionRequiredError{
				Reason: "finish WSL reboot/installation, then rerun the same apply",
			}
		}
	}
	if _, err := runtime.Port.Run(ctx, transport.Command{
		Executable: "wsl.exe",
		Arguments: []string{
			"--distribution", runtime.Spec.WSLDistribution,
			"--exec", "true",
		},
	}); err != nil {
		return &adapters.ActionRequiredError{
			Reason: "launch Ubuntu once to create the Linux user, then rerun the same apply",
		}
	}
	return nil
}

func (runtime GuestRuntime) limaInstances(ctx context.Context) ([]target.Facts, error) {
	result, err := runtime.Port.Run(ctx, transport.Command{
		Executable: "limactl",
		Arguments:  []string{"list", "--json"},
	})
	if err != nil {
		return nil, fmt.Errorf("list Lima guests: %w", err)
	}
	return target.ParseLimaInstances([]byte(result.Stdout))
}

func (runtime GuestRuntime) wslDistributions(ctx context.Context) ([]target.Facts, error) {
	result, err := runtime.Port.Run(ctx, transport.Command{
		Executable: "wsl.exe",
		Arguments:  []string{"--list", "--quiet"},
	})
	if err != nil {
		return nil, fmt.Errorf("list WSL guests: %w", err)
	}
	return target.ParseWSLDistributions([]byte(result.Stdout))
}

func hasTarget(facts []target.Facts, kind target.Kind, name string) bool {
	for _, item := range facts {
		if item.ID.Kind == kind && item.ID.Name == name {
			return true
		}
	}
	return false
}
