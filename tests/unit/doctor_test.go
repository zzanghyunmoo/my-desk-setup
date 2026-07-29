package unit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

func TestDoctorIsObservationOnlyAndDoesNotInspectAuth(t *testing.T) {
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	plan := planning.Plan{
		SchemaVersion:   planning.PlanSchema,
		CatalogRevision: "sha256:catalog",
		Target: target.Facts{
			ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		},
		Actions: []planning.Action{
			{
				ID: "lima-guest:mds/codex", ComponentID: "codex",
				Status: planning.ActionPlanned, Version: "0.144.6",
				Verification: [][]string{
					{"codex", "--version"},
				},
			},
			{
				ID: "lima-guest:mds/xcode", ComponentID: "xcode",
				Status: planning.ActionUnsupported, Version: "manual",
				Reason: "Xcode is only available on macOS.",
			},
		},
	}
	adapter := &doctorAdapter{
		observations: map[string]adapters.Observation{
			"codex": {
				State: adapters.StateReady, InstalledVersion: "0.144.6",
			},
		},
	}
	report, err := doctor.Run(context.Background(), plan, adapter)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if report.Ready || len(report.Checks) != 2 {
		t.Fatalf("report = %+v, want mixed readiness", report)
	}
	if report.Checks[0].Status != "ready" ||
		report.Checks[0].VerifiedVersion != "0.144.6" {
		t.Fatalf("ready check = %+v", report.Checks[0])
	}
	if report.Checks[1].Status != "unsupported" {
		t.Fatalf("unsupported check = %+v", report.Checks[1])
	}
	if adapter.applyCalls != 0 || adapter.verifyCalls != 0 {
		t.Fatalf(
			"doctor mutated or ran functional verification: apply=%d verify=%d",
			adapter.applyCalls,
			adapter.verifyCalls,
		)
	}
	for _, action := range adapter.observed {
		for _, argv := range action.Verification {
			for _, argument := range argv {
				if argument == "auth" || argument == "login" {
					t.Fatalf("doctor observed authentication command: %v", argv)
				}
			}
		}
	}
}

type doctorAdapter struct {
	observations map[string]adapters.Observation
	observed     []planning.Action
	applyCalls   int
	verifyCalls  int
}

func (adapter *doctorAdapter) Observe(
	_ context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	adapter.observed = append(adapter.observed, action)
	observation, exists := adapter.observations[action.ComponentID]
	if !exists {
		return adapters.Observation{}, errors.New("missing fixture observation")
	}
	return observation, nil
}

func (adapter *doctorAdapter) Apply(context.Context, planning.Action) error {
	adapter.applyCalls++
	return nil
}

func (adapter *doctorAdapter) Verify(context.Context, planning.Action) error {
	adapter.verifyCalls++
	return nil
}
