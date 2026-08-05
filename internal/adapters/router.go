package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func (router Router) Preflight(
	ctx context.Context,
	plan planning.Plan,
) (func() error, error) {
	cleanups := make([]func() error, 0)
	for _, action := range plan.Actions {
		component, err := router.adapter(action.ComponentID)
		if err != nil {
			cleanupErr := cleanupAll(cleanups)()
			return nil, errors.Join(err, cleanupErr)
		}
		preflighter, ok := component.(PlanPreflighter)
		if !ok {
			continue
		}
		cleanup, err := preflighter.Preflight(ctx, plan)
		if err != nil {
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}
			cleanupErr := cleanupAll(cleanups)()
			return nil, errors.Join(err, cleanupErr)
		}
		if cleanup != nil {
			cleanups = append(cleanups, cleanup)
		}
	}
	return cleanupAll(cleanups), nil
}

func cleanupAll(cleanups []func() error) func() error {
	return func() error {
		var result error
		for index := len(cleanups) - 1; index >= 0; index-- {
			result = errors.Join(result, cleanups[index]())
		}
		return result
	}
}

type Router struct {
	Default Component
	ByID    map[string]Component
}

func (router Router) Observe(
	ctx context.Context,
	action planning.Action,
) (Observation, error) {
	adapter, err := router.adapter(action.ComponentID)
	if err != nil {
		return Observation{}, err
	}
	return adapter.Observe(ctx, action)
}

func (router Router) Apply(ctx context.Context, action planning.Action) error {
	adapter, err := router.adapter(action.ComponentID)
	if err != nil {
		return err
	}
	return adapter.Apply(ctx, action)
}

func (router Router) Verify(ctx context.Context, action planning.Action) error {
	adapter, err := router.adapter(action.ComponentID)
	if err != nil {
		return err
	}
	return adapter.Verify(ctx, action)
}

func (router Router) adapter(componentID string) (Component, error) {
	if adapter := router.ByID[componentID]; adapter != nil {
		return adapter, nil
	}
	if router.Default == nil {
		return nil, fmt.Errorf("no adapter registered for component %q", componentID)
	}
	return router.Default, nil
}
