package update

import (
	"context"
	"fmt"
	"reflect"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/execution"
)

func Apply(
	ctx context.Context,
	plan Plan,
	expectedDigest,
	catalogRoot,
	stateRoot string,
	runner execution.Runner,
) (Result, error) {
	if err := Verify(plan, expectedDigest); err != nil {
		return Result{}, err
	}
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
	if runner.ObserveTarget == nil {
		return Result{}, fmt.Errorf("target observer is required")
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
	receipt, err := runner.Apply(
		ctx,
		plan.TargetPlan,
		plan.TargetPlan.Digest,
		stateRoot,
	)
	if err != nil {
		return Result{}, err
	}
	return Result{
		SchemaVersion: ResultSchema, UpdateDigest: plan.Digest,
		CatalogRevision: plan.AfterCatalogRevision, Receipt: receipt,
	}, nil
}
