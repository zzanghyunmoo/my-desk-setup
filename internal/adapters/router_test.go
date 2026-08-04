package adapters

import (
	"context"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

type routerPreflightFixture struct {
	cleanups int
}

func (*routerPreflightFixture) Observe(
	context.Context,
	planning.Action,
) (Observation, error) {
	return Observation{State: StateReady}, nil
}

func (*routerPreflightFixture) Apply(context.Context, planning.Action) error  { return nil }
func (*routerPreflightFixture) Verify(context.Context, planning.Action) error { return nil }

func (fixture *routerPreflightFixture) Preflight(
	context.Context,
	planning.Plan,
) (func() error, error) {
	return func() error {
		fixture.cleanups++
		return nil
	}, nil
}

func TestRouterPreflightCleansEarlierInputsWhenLaterRoutingFails(t *testing.T) {
	fixture := &routerPreflightFixture{}
	router := Router{ByID: map[string]Component{"a": fixture}}
	cleanup, err := router.Preflight(context.Background(), planning.Plan{
		Actions: []planning.Action{
			{ComponentID: "a"},
			{ComponentID: "missing"},
		},
	})
	if err == nil || cleanup != nil {
		t.Fatalf("Preflight() hasCleanup=%t err=%v", cleanup != nil, err)
	}
	if fixture.cleanups != 1 {
		t.Fatalf("cleanup count = %d, want 1", fixture.cleanups)
	}
}
