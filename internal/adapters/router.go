package adapters

import (
	"context"
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

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
