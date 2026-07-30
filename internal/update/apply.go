package update

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/execution"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
)

func Apply(
	ctx context.Context,
	plan Plan,
	expectedDigest,
	catalogRoot,
	stateRoot string,
	runner execution.Runner,
) (result Result, resultErr error) {
	if err := Verify(plan, expectedDigest); err != nil {
		return Result{}, err
	}
	if err := ValidateCatalogRoot(catalogRoot); err != nil {
		return Result{}, err
	}
	if runner.ObserveTarget == nil {
		return Result{}, fmt.Errorf("target observer is required")
	}
	targetPaths, err := state.NewPaths(
		stateRoot,
		plan.TargetPlan.Target.ID.String(),
	)
	if err != nil {
		return Result{}, err
	}
	if err := targetPaths.EnsureRoot(); err != nil {
		return Result{}, err
	}
	catalogLockPath, err := state.CatalogLockPath(stateRoot, catalogRoot)
	if err != nil {
		return Result{}, err
	}
	catalogLease, err := state.Acquire(catalogLockPath)
	if err != nil {
		return Result{}, fmt.Errorf("acquire catalog writer lease: %w", err)
	}
	defer mergeLeaseRelease(
		&result,
		&resultErr,
		"catalog",
		catalogLease,
	)
	if err := targetPaths.Ensure(); err != nil {
		return Result{}, err
	}
	targetLease, err := state.Acquire(targetPaths.Lock)
	if err != nil {
		return Result{}, fmt.Errorf("acquire target writer lease: %w", err)
	}
	defer mergeLeaseRelease(
		&result,
		&resultErr,
		"target",
		targetLease,
	)

	current, err := catalog.Load(catalogRoot)
	if err != nil {
		return Result{}, stale(fmt.Errorf(
			"catalog changed after review and can no longer be loaded",
		))
	}
	currentRevision, err := catalog.Revision(current)
	if err != nil {
		return Result{}, fmt.Errorf("current catalog revision: %w", err)
	}
	intentPath := transactionIntentPath(catalogLockPath)
	intent, intentExists, err := readTransactionIntent(intentPath)
	if err != nil {
		return Result{}, err
	}
	if intentExists && !intent.matches(plan) {
		if intent.UpdateDigest == plan.Digest {
			return Result{}, errors.New(
				"stored update intent does not match the reviewed plan",
			)
		}
		if intent.Phase != intentPhaseComplete {
			return Result{}, stale(fmt.Errorf(
				"another update transaction is incomplete; rerun its reviewed digest %s before starting a new update",
				intent.UpdateDigest,
			))
		}
		intentExists = false
	}

	currentIsBefore := currentRevision == plan.BeforeCatalogRevision &&
		reflect.DeepEqual(current.Lock.Versions[plan.LockKey], plan.Old)
	currentIsAfter := currentRevision == plan.AfterCatalogRevision &&
		reflect.DeepEqual(current.Lock.Versions[plan.LockKey], plan.New)
	if !intentExists {
		if !currentIsBefore {
			return Result{}, stale(fmt.Errorf(
				"catalog preimage changed: planned=%s current=%s",
				plan.BeforeCatalogRevision,
				currentRevision,
			))
		}
		updated, err := updatedEnvironment(current, plan)
		if err != nil {
			return Result{}, err
		}
		if err := validateTargetPreimage(ctx, runner, plan); err != nil {
			return Result{}, err
		}
		intent = newTransactionIntent(plan, current.Lock, updated.Lock)
		if err := writeTransactionIntent(intentPath, intent); err != nil {
			return Result{}, err
		}
		intentExists = true
	} else {
		switch {
		case currentIsBefore:
		case currentIsAfter && intent.Phase != intentPhasePrepared:
		default:
			return Result{}, stale(fmt.Errorf(
				"catalog no longer matches the resumable update transaction",
			))
		}
		if _, err := updatedEnvironmentFromIntent(
			current,
			plan,
			intent,
			currentIsAfter,
		); err != nil {
			return Result{}, err
		}
		if err := validateTargetPreimage(ctx, runner, plan); err != nil {
			return Result{}, err
		}
	}
	if !intentExists {
		return Result{}, errors.New("update intent was not prepared")
	}

	receipt, err := runner.ApplyWithTargetLease(
		ctx,
		plan.TargetPlan,
		plan.TargetPlan.Digest,
		stateRoot,
		targetLease,
	)
	if err != nil {
		return Result{}, err
	}
	intent.Receipt = &receipt
	if !receipt.Complete {
		intent.Phase = intentPhasePrepared
		if err := writeTransactionIntent(intentPath, intent); err != nil {
			return Result{}, err
		}
		return Result{
			SchemaVersion: ResultSchema, UpdateDigest: plan.Digest,
			CatalogRevision: plan.BeforeCatalogRevision, Receipt: receipt,
		}, nil
	}

	intent.Phase = intentPhaseTarget
	if err := writeTransactionIntent(intentPath, intent); err != nil {
		return Result{}, err
	}
	if currentIsBefore {
		if err := writeLock(catalogRoot, intent.NewLock); err != nil {
			return Result{}, err
		}
	}
	intent.Phase = intentPhaseComplete
	if err := writeTransactionIntent(intentPath, intent); err != nil {
		return Result{}, err
	}
	return Result{
		SchemaVersion: ResultSchema, UpdateDigest: plan.Digest,
		CatalogRevision: plan.AfterCatalogRevision, Receipt: receipt,
	}, nil
}

func validateTargetPreimage(
	ctx context.Context,
	runner execution.Runner,
	plan Plan,
) error {
	observed, err := runner.ObserveTarget(ctx, plan.TargetPlan.Target)
	if err != nil {
		return fmt.Errorf("observe update target preimage: %w", err)
	}
	plannedFingerprint, err := plan.TargetPlan.Target.Fingerprint()
	if err != nil {
		return err
	}
	observedFingerprint, err := observed.Fingerprint()
	if err != nil {
		return err
	}
	if plannedFingerprint != observedFingerprint {
		return stale(fmt.Errorf(
			"target preimage changed: planned=%s observed=%s",
			plannedFingerprint,
			observedFingerprint,
		))
	}
	return nil
}

func updatedEnvironment(
	current catalog.Environment,
	plan Plan,
) (catalog.Environment, error) {
	updated, err := cloneEnvironment(current)
	if err != nil {
		return catalog.Environment{}, err
	}
	updated.Lock.Versions[plan.LockKey] = plan.New
	return validateUpdatedEnvironment(updated, plan)
}

func updatedEnvironmentFromIntent(
	current catalog.Environment,
	plan Plan,
	intent transactionIntent,
	currentIsAfter bool,
) (catalog.Environment, error) {
	updated, err := cloneEnvironment(current)
	if err != nil {
		return catalog.Environment{}, err
	}
	if !currentIsAfter {
		updated.Lock = intent.NewLock
	}
	return validateUpdatedEnvironment(updated, plan)
}

func validateUpdatedEnvironment(
	updated catalog.Environment,
	plan Plan,
) (catalog.Environment, error) {
	if err := catalog.Validate(updated); err != nil {
		return catalog.Environment{}, stale(fmt.Errorf(
			"updated catalog validation no longer matches the reviewed plan: %w",
			err,
		))
	}
	updatedRevision, err := catalog.Revision(updated)
	if err != nil {
		return catalog.Environment{}, err
	}
	if updatedRevision != plan.AfterCatalogRevision {
		return catalog.Environment{}, stale(fmt.Errorf(
			"updated catalog revision mismatch: planned=%s actual=%s",
			plan.AfterCatalogRevision,
			updatedRevision,
		))
	}
	component, err := pinnedComponent(updated, plan.ComponentID)
	if err != nil {
		return catalog.Environment{}, stale(err)
	}
	matrix, err := buildCompatibilityMatrix(
		updated,
		component,
		plan.TargetPlan.Target.CLIRevision,
		updatedRevision,
	)
	if err != nil {
		return catalog.Environment{}, stale(err)
	}
	if !reflect.DeepEqual(matrix, plan.CompatibilityMatrix) {
		return catalog.Environment{}, stale(errors.New(
			"compatibility matrix changed before lock publication",
		))
	}
	return updated, nil
}

func mergeLeaseRelease(
	result *Result,
	resultErr *error,
	label string,
	lease *state.Lock,
) {
	if err := lease.Release(); err != nil {
		*result = Result{}
		*resultErr = errors.Join(
			*resultErr,
			fmt.Errorf("release %s writer lease: %w", label, err),
		)
	}
}
