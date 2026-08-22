package doctor

import (
	"context"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

type readyComponent struct{}

func (readyComponent) Observe(context.Context, planning.Action) (adapters.Observation, error) {
	return adapters.Observation{State: adapters.StateReady, InstalledVersion: "exact"}, nil
}

func (readyComponent) Apply(context.Context, planning.Action) error  { return nil }
func (readyComponent) Verify(context.Context, planning.Action) error { return nil }

func TestRunFailsClosedWhenSelectedIDEHasNoCapabilityProber(t *testing.T) {
	plan := planning.Plan{Actions: []planning.Action{{
		ID: "lima-guest:mds/nvim-jvm", ComponentID: "nvim-jvm",
		Status: planning.ActionPlanned, Version: "exact",
	}}}
	report, err := Run(context.Background(), plan, readyComponent{})
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if report.Ready || report.Capabilities == nil || report.Capabilities.Ready {
		t.Fatalf("report = %+v, want blocked capability receipt", report)
	}
}
