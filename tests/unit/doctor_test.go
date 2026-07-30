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

func TestDoctorIsReadOnlyAndDoesNotInspectAuth(t *testing.T) {
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
			{
				ID: "lima-guest:mds/manual", ComponentID: "manual",
				Status: planning.ActionActionRequired, Version: "manual",
				Reason: "Complete the target-local manual step.",
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
	if report.Ready || len(report.Checks) != 3 {
		t.Fatalf("report = %+v, want mixed readiness", report)
	}
	if report.Checks[0].Status != "ready" ||
		report.Checks[0].VerifiedVersion != "0.144.6" {
		t.Fatalf("ready check = %+v", report.Checks[0])
	}
	if report.Checks[1].Status != "unsupported" {
		t.Fatalf("unsupported check = %+v", report.Checks[1])
	}
	if report.Checks[2].Status != "action-required" {
		t.Fatalf("action-required check = %+v", report.Checks[2])
	}
	if adapter.applyCalls != 0 || adapter.verifyCalls != 1 {
		t.Fatalf(
			"doctor call counts: apply=%d verify=%d, want apply=0 verify=1",
			adapter.applyCalls,
			adapter.verifyCalls,
		)
	}
	for _, action := range append(adapter.observed, adapter.verified...) {
		for _, argv := range action.Verification {
			for _, argument := range argv {
				if argument == "auth" || argument == "login" {
					t.Fatalf("doctor received authentication command: %v", argv)
				}
			}
		}
	}
}

func TestDoctorRejectsReadyObservationWhenFunctionalVerificationFails(t *testing.T) {
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	plan := planning.Plan{
		SchemaVersion:   planning.PlanSchema,
		CatalogRevision: "sha256:catalog",
		Target: target.Facts{
			ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		},
		Actions: []planning.Action{
			{
				ID: "lima-guest:mds/docker-engine", ComponentID: "docker-engine",
				Status: planning.ActionPlanned, Version: "manager-owned",
				Verification: [][]string{
					{"docker", "version"},
					{"docker", "info", "--format", "{{.ServerVersion}}"},
				},
			},
		},
	}
	adapter := &doctorAdapter{
		observations: map[string]adapters.Observation{
			"docker-engine": {
				State: adapters.StateReady, InstalledVersion: "28.3.3",
			},
		},
		verifyErrors: map[string]error{
			"docker-engine": errors.New("docker daemon is unavailable"),
		},
	}

	report, err := doctor.Run(context.Background(), plan, adapter)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if report.Ready || len(report.Checks) != 1 {
		t.Fatalf("report = %+v, want one unready check", report)
	}
	check := report.Checks[0]
	if check.Status != "unready" ||
		check.ReasonCode != "functional-verification-failed" ||
		check.VerifiedVersion != "" {
		t.Fatalf("check = %+v, want unverified functional failure", check)
	}
	if check.InstalledVersion != "28.3.3" ||
		check.Reason != "docker daemon is unavailable" {
		t.Fatalf("check = %+v, want observed version and functional error", check)
	}
	if adapter.applyCalls != 0 || adapter.verifyCalls != 1 {
		t.Fatalf(
			"doctor call counts: apply=%d verify=%d, want apply=0 verify=1",
			adapter.applyCalls,
			adapter.verifyCalls,
		)
	}
}

type doctorAdapter struct {
	observations map[string]adapters.Observation
	observed     []planning.Action
	verified     []planning.Action
	verifyErrors map[string]error
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

func (adapter *doctorAdapter) Verify(
	_ context.Context,
	action planning.Action,
) error {
	adapter.verifyCalls++
	adapter.verified = append(adapter.verified, action)
	return adapter.verifyErrors[action.ComponentID]
}
