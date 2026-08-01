package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

type Hooks struct {
	AfterApply func(planning.Action) error
}

type Runner struct {
	Adapter       adapters.Component
	ObserveTarget func(context.Context, target.Facts) (target.Facts, error)
	Now           func() time.Time
	Hooks         Hooks
}

func (runner Runner) Apply(
	ctx context.Context,
	plan planning.Plan,
	expectedDigest,
	stateRoot string,
) (state.Receipt, error) {
	return runner.apply(ctx, plan, expectedDigest, stateRoot, nil)
}

func (runner Runner) ApplyWithTargetLease(
	ctx context.Context,
	plan planning.Plan,
	expectedDigest,
	stateRoot string,
	targetLease *state.Lock,
) (state.Receipt, error) {
	if targetLease == nil {
		return state.Receipt{}, errors.New("target writer lease is required")
	}
	return runner.apply(
		ctx,
		plan,
		expectedDigest,
		stateRoot,
		targetLease,
	)
}

func (runner Runner) apply(
	ctx context.Context,
	plan planning.Plan,
	expectedDigest,
	stateRoot string,
	targetLease *state.Lock,
) (result state.Receipt, resultErr error) {
	if err := VerifyPlan(plan, expectedDigest); err != nil {
		return state.Receipt{}, err
	}
	if runner.Adapter == nil {
		return state.Receipt{}, fmt.Errorf("component adapter is required")
	}
	if runner.Now == nil {
		return state.Receipt{}, fmt.Errorf("clock is required")
	}
	if runner.ObserveTarget == nil {
		return state.Receipt{}, fmt.Errorf("target observer is required")
	}
	actualFacts, err := runner.ObserveTarget(ctx, plan.Target)
	if err != nil {
		return state.Receipt{}, fmt.Errorf("observe target preimage: %w", err)
	}
	plannedFingerprint, err := plan.Target.Fingerprint()
	if err != nil {
		return state.Receipt{}, fmt.Errorf("fingerprint planned target: %w", err)
	}
	actualFingerprint, err := actualFacts.Fingerprint()
	if err != nil {
		return state.Receipt{}, fmt.Errorf("fingerprint observed target: %w", err)
	}
	if plannedFingerprint != actualFingerprint {
		return state.Receipt{}, &StalePlanError{Cause: fmt.Errorf(
			"target preimage changed: planned %s observed %s",
			plannedFingerprint,
			actualFingerprint,
		)}
	}
	if err := actualFacts.ApplyPreflight(); err != nil {
		return state.Receipt{}, &adapters.ActionRequiredError{
			Reason: transport.SanitizeDiagnostic(err.Error()) +
				"; restore target readiness and rerun the same reviewed digest",
		}
	}

	paths, err := state.NewPaths(stateRoot, plan.Target.ID.String())
	if err != nil {
		return state.Receipt{}, err
	}
	if err := paths.Ensure(); err != nil {
		return state.Receipt{}, err
	}
	if targetLease != nil {
		if !targetLease.Holds(paths.Lock) {
			return state.Receipt{}, fmt.Errorf(
				"target writer lease does not hold %s",
				paths.Lock,
			)
		}
	} else {
		targetLease, err = state.Acquire(paths.Lock)
		if err != nil {
			return state.Receipt{}, err
		}
		defer func() {
			if err := targetLease.Release(); err != nil {
				result = state.Receipt{}
				resultErr = errors.Join(
					resultErr,
					fmt.Errorf("release target writer lease: %w", err),
				)
			}
		}()
	}

	startedAt := runner.Now().UTC()
	journal := state.NewJournal(paths.Journal)
	statuses := make(map[string]string, len(plan.Actions))
	outcomes := make([]state.ActionOutcome, 0, len(plan.Actions))

	for _, action := range plan.Actions {
		outcome := state.ActionOutcome{
			ActionID: action.ID, RequestedVersion: action.Version,
		}
		if action.Status == planning.ActionUnsupported ||
			action.Status == planning.ActionActionRequired {
			outcome.Status = string(action.Status)
			outcome.ReasonCode = string(action.Status)
			outcome.Reason = action.Reason
			statuses[action.ID] = outcome.Status
			outcomes = append(outcomes, outcome)
			continue
		}
		if dependency := blockedDependency(action, statuses); dependency != "" {
			outcome.Status = "blocked"
			outcome.ReasonCode = "dependency-not-ready"
			outcome.Reason = "dependency is not ready: " + dependency
			statuses[action.ID] = outcome.Status
			outcomes = append(outcomes, outcome)
			continue
		}

		observation, observeErr := runner.Adapter.Observe(ctx, action)
		if observeErr != nil {
			outcome.Status = "failed"
			outcome.ReasonCode = "observation-failed"
			outcome.Reason = "observe: " + transport.SanitizeDiagnostic(observeErr.Error())
			statuses[action.ID] = outcome.Status
			outcomes = append(outcomes, outcome)
			continue
		}
		outcome.InstalledVersion = observation.InstalledVersion
		if observation.State == adapters.StateConflict {
			outcome.Status = "failed"
			outcome.ReasonCode = "user-owned-or-version-conflict"
			outcome.Reason = "conflict: " + transport.SanitizeDiagnostic(observation.Detail)
			statuses[action.ID] = outcome.Status
			outcomes = append(outcomes, outcome)
			continue
		}
		if observation.State == adapters.StateReady {
			if err := runner.Adapter.Verify(ctx, action); err == nil {
				outcome.Status = "ready"
				outcome.ReasonCode = "ready"
				outcome.Noop = true
				outcome.VerifiedVersion = observation.InstalledVersion
				statuses[action.ID] = outcome.Status
				outcomes = append(outcomes, outcome)
				if err := journal.Append(state.JournalEvent{
					At: runner.Now().UTC(), PlanDigest: plan.Digest,
					ActionID: action.ID, Phase: "verified-noop",
				}); err != nil {
					return state.Receipt{}, err
				}
				continue
			}
		}

		if err := journal.Append(state.JournalEvent{
			At: runner.Now().UTC(), PlanDigest: plan.Digest,
			ActionID: action.ID, Phase: "apply-started",
		}); err != nil {
			return state.Receipt{}, err
		}
		if err := runner.Adapter.Apply(ctx, action); err != nil {
			var actionRequired *adapters.ActionRequiredError
			journalPhase := "apply-failed"
			if errors.As(err, &actionRequired) {
				outcome.Status = "action-required"
				outcome.ReasonCode = "action-required"
				outcome.Reason = actionRequired.Reason
				journalPhase = "apply-action-required"
			} else {
				outcome.Status = "failed"
				outcome.ReasonCode = "apply-failed"
				outcome.Reason = "apply: " + transport.SanitizeDiagnostic(err.Error())
			}
			statuses[action.ID] = outcome.Status
			outcomes = append(outcomes, outcome)
			if journalErr := journal.Append(state.JournalEvent{
				At: runner.Now().UTC(), PlanDigest: plan.Digest,
				ActionID: action.ID, Phase: journalPhase,
				ReasonCode: outcome.ReasonCode,
				Detail:     transport.SanitizeDiagnostic(err.Error()),
			}); journalErr != nil {
				return state.Receipt{}, journalErr
			}
			continue
		}
		if runner.Hooks.AfterApply != nil {
			if err := runner.Hooks.AfterApply(action); err != nil {
				return state.Receipt{}, err
			}
		}
		if err := journal.Append(state.JournalEvent{
			At: runner.Now().UTC(), PlanDigest: plan.Digest,
			ActionID: action.ID, Phase: "applied",
		}); err != nil {
			return state.Receipt{}, err
		}
		if err := runner.Adapter.Verify(ctx, action); err != nil {
			outcome.Status = "failed"
			outcome.ReasonCode = "functional-verification-failed"
			outcome.Reason = "verify: " + transport.SanitizeDiagnostic(err.Error())
			statuses[action.ID] = outcome.Status
			outcomes = append(outcomes, outcome)
			if journalErr := journal.Append(state.JournalEvent{
				At: runner.Now().UTC(), PlanDigest: plan.Digest,
				ActionID: action.ID, Phase: "verify-failed",
				ReasonCode: outcome.ReasonCode,
				Detail:     transport.SanitizeDiagnostic(err.Error()),
			}); journalErr != nil {
				return state.Receipt{}, journalErr
			}
			continue
		}
		finalObservation, err := runner.Adapter.Observe(ctx, action)
		if err != nil || finalObservation.State != adapters.StateReady {
			outcome.Status = "failed"
			outcome.ReasonCode = "post-verification-not-ready"
			if err != nil {
				outcome.Reason = "post-verify observation: " + transport.SanitizeDiagnostic(err.Error())
			} else {
				outcome.Reason = "post-verify observation is " + string(finalObservation.State)
				if finalObservation.Detail != "" {
					outcome.Reason += ": " + finalObservation.Detail
				}
			}
			statuses[action.ID] = outcome.Status
			outcomes = append(outcomes, outcome)
			continue
		}
		outcome.Status = "ready"
		outcome.ReasonCode = "ready"
		outcome.InstalledVersion = finalObservation.InstalledVersion
		outcome.VerifiedVersion = finalObservation.InstalledVersion
		statuses[action.ID] = outcome.Status
		outcomes = append(outcomes, outcome)
		if err := journal.Append(state.JournalEvent{
			At: runner.Now().UTC(), PlanDigest: plan.Digest,
			ActionID: action.ID, Phase: "verified",
		}); err != nil {
			return state.Receipt{}, err
		}
	}

	receipt := state.Receipt{
		SchemaVersion: state.ReceiptSchema,
		PlanDigest:    plan.Digest, CatalogRevision: plan.CatalogRevision,
		TargetID: plan.Target.ID.String(), Complete: complete(outcomes),
		StartedAt: startedAt, FinishedAt: runner.Now().UTC(), Outcomes: outcomes,
	}
	if _, err := state.WriteReceipt(paths.Receipts, receipt); err != nil {
		return state.Receipt{}, err
	}
	return receipt, nil
}

func complete(outcomes []state.ActionOutcome) bool {
	for _, outcome := range outcomes {
		if outcome.Status != "ready" {
			return false
		}
	}
	return true
}
