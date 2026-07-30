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
		return Result{}, fmt.Errorf("load current catalog: %w", err)
	}
	currentRevision, err := catalog.Revision(current)
	if err != nil {
		return Result{}, fmt.Errorf("current catalog revision: %w", err)
	}
	if currentRevision != plan.BeforeCatalogRevision {
		return Result{}, fmt.Errorf(
			"catalog preimage changed: planned=%s current=%s",
			plan.BeforeCatalogRevision,
			currentRevision,
		)
	}
	if !reflect.DeepEqual(current.Lock.Versions[plan.LockKey], plan.Old) {
		return Result{}, fmt.Errorf("lock preimage changed for %s", plan.LockKey)
	}
	observed, err := runner.ObserveTarget(ctx, plan.TargetPlan.Target)
	if err != nil {
		return Result{}, fmt.Errorf("observe update target preimage: %w", err)
	}
	plannedFingerprint, err := plan.TargetPlan.Target.Fingerprint()
	if err != nil {
		return Result{}, err
	}
	observedFingerprint, err := observed.Fingerprint()
	if err != nil {
		return Result{}, err
	}
	if plannedFingerprint != observedFingerprint {
		return Result{}, fmt.Errorf(
			"target preimage changed: planned=%s observed=%s",
			plannedFingerprint,
			observedFingerprint,
		)
	}
	updated, err := cloneEnvironment(current)
	if err != nil {
		return Result{}, err
	}
	updated.Lock.Versions[plan.LockKey] = plan.New
	if err := catalog.Validate(updated); err != nil {
		return Result{}, fmt.Errorf("updated catalog validation: %w", err)
	}
	updatedRevision, err := catalog.Revision(updated)
	if err != nil {
		return Result{}, err
	}
	if updatedRevision != plan.AfterCatalogRevision {
		return Result{}, fmt.Errorf(
			"updated catalog revision mismatch: planned=%s actual=%s",
			plan.AfterCatalogRevision,
			updatedRevision,
		)
	}
	if err := writeLock(catalogRoot, updated.Lock); err != nil {
		return Result{}, err
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
	return Result{
		SchemaVersion: ResultSchema, UpdateDigest: plan.Digest,
		CatalogRevision: plan.AfterCatalogRevision, Receipt: receipt,
	}, nil
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
