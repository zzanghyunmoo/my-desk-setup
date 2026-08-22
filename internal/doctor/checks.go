package doctor

import (
	"context"
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/capability"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func Run(
	ctx context.Context,
	plan planning.Plan,
	adapter adapters.Component,
) (Report, error) {
	if adapter == nil {
		return Report{}, fmt.Errorf("doctor component adapter is required")
	}
	report := Report{
		SchemaVersion:   SchemaVersion,
		CatalogRevision: plan.CatalogRevision,
		Target:          plan.Target,
		Ready:           true,
		Checks:          make([]Check, 0, len(plan.Actions)),
	}
	for _, action := range plan.Actions {
		check := Check{
			ActionID: action.ID, ComponentID: action.ComponentID,
			RequestedVersion: action.Version,
		}
		switch action.Status {
		case planning.ActionUnsupported:
			check.Status = "unsupported"
			check.ReasonCode = "unsupported"
			check.Reason = action.Reason
			check.RecoveryHint = "choose a supported target"
			report.Ready = false
		case planning.ActionActionRequired:
			check.Status = "action-required"
			check.ReasonCode = "action-required"
			check.Reason = action.Reason
			check.RecoveryHint = "complete the documented manual step"
			report.Ready = false
		default:
			observation, err := adapter.Observe(ctx, action)
			if err != nil {
				check.Status = "unready"
				check.ReasonCode = "observation-failed"
				check.Reason = err.Error()
				check.RecoveryHint = "fix the local probe and rerun doctor"
				report.Ready = false
				break
			}
			check.InstalledVersion = observation.InstalledVersion
			switch observation.State {
			case adapters.StateReady:
				if err := adapter.Verify(ctx, action); err != nil {
					check.Status = "unready"
					check.ReasonCode = "functional-verification-failed"
					check.Reason = err.Error()
					check.RecoveryHint = "fix the functional probe and rerun doctor"
					report.Ready = false
					break
				}
				check.Status = "ready"
				check.ReasonCode = "ready"
				check.VerifiedVersion = observation.InstalledVersion
			case adapters.StateAbsent:
				check.Status = "unready"
				check.ReasonCode = "not-installed"
				check.Reason = observation.Detail
				check.RecoveryHint = "review a plan digest and run mds apply"
				report.Ready = false
			case adapters.StateConflict:
				check.Status = "conflict"
				check.ReasonCode = "user-owned-or-version-conflict"
				check.Reason = observation.Detail
				check.RecoveryHint = "preserve user-owned state or use explicit update"
				report.Ready = false
			default:
				check.Status = "unready"
				check.ReasonCode = "unknown-observation"
				check.Reason = "adapter returned an unknown observation state"
				report.Ready = false
			}
		}
		report.Checks = append(report.Checks, check)
	}
	components := make([]string, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		components = append(components, action.ComponentID)
	}
	if expected := capability.ExpectedIDs(components); len(expected) > 0 {
		if prober, ok := adapter.(adapters.CapabilityProber); ok {
			receipt := prober.ProbeCapabilities(ctx, plan)
			report.Capabilities = &receipt
		} else {
			checks := make([]capability.CapabilityCheck, 0, len(capability.Expected(components)))
			for _, specification := range capability.Expected(components) {
				checks = append(checks, capability.NewCheck(
					specification.ID, specification.Kind, specification.ComponentID,
					capability.StatusBlocked, "probe-unavailable", "",
				))
			}
			receipt := capability.Aggregate(expected, checks)
			report.Capabilities = &receipt
		}
		if !capability.MatchesExpected(components, report.Capabilities) {
			report.Ready = false
		}
	}
	return report, nil
}
