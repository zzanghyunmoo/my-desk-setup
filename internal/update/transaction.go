package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
)

const (
	intentSchema        = "mds.update-intent/v1"
	intentPhasePrepared = "prepared"
	intentPhaseTarget   = "target-reconciled"
	intentPhaseComplete = "complete"
	maxIntentBytes      = 4 << 20
)

type transactionIntent struct {
	SchemaVersion         string              `json:"schema_version"`
	UpdateDigest          string              `json:"update_digest"`
	TargetPlanDigest      string              `json:"target_plan_digest"`
	ComponentID           string              `json:"component_id"`
	LockKey               string              `json:"lock_key"`
	BeforeCatalogRevision string              `json:"before_catalog_revision"`
	AfterCatalogRevision  string              `json:"after_catalog_revision"`
	OldLock               catalog.VersionLock `json:"old_lock"`
	NewLock               catalog.VersionLock `json:"new_lock"`
	Plan                  Plan                `json:"plan"`
	Phase                 string              `json:"phase"`
	Receipt               *state.Receipt      `json:"receipt,omitempty"`
}

func newTransactionIntent(
	plan Plan,
	oldLock,
	newLock catalog.VersionLock,
) transactionIntent {
	return transactionIntent{
		SchemaVersion: intentSchema,
		UpdateDigest:  plan.Digest, TargetPlanDigest: plan.TargetPlan.Digest,
		ComponentID: plan.ComponentID, LockKey: plan.LockKey,
		BeforeCatalogRevision: plan.BeforeCatalogRevision,
		AfterCatalogRevision:  plan.AfterCatalogRevision,
		OldLock:               oldLock, NewLock: newLock,
		Plan: plan, Phase: intentPhasePrepared,
	}
}

func (intent transactionIntent) matches(plan Plan) bool {
	return intent.SchemaVersion == intentSchema &&
		intent.UpdateDigest == plan.Digest &&
		intent.TargetPlanDigest == plan.TargetPlan.Digest &&
		intent.ComponentID == plan.ComponentID &&
		intent.LockKey == plan.LockKey &&
		intent.BeforeCatalogRevision == plan.BeforeCatalogRevision &&
		intent.AfterCatalogRevision == plan.AfterCatalogRevision &&
		reflect.DeepEqual(intent.OldLock.Versions[plan.LockKey], plan.Old) &&
		reflect.DeepEqual(intent.NewLock.Versions[plan.LockKey], plan.New) &&
		reflect.DeepEqual(intent.Plan, plan)
}

func (intent transactionIntent) validPhase() bool {
	switch intent.Phase {
	case intentPhasePrepared, intentPhaseTarget, intentPhaseComplete:
		return true
	default:
		return false
	}
}

func transactionIntentPath(catalogLockPath string) string {
	return strings.TrimSuffix(
		catalogLockPath,
		".writer.lock",
	) + ".update-intent.json"
}

func readTransactionIntent(path string) (
	transactionIntent,
	bool,
	error,
) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return transactionIntent{}, false, nil
	}
	if err != nil {
		return transactionIntent{}, false, fmt.Errorf(
			"inspect update intent: %w",
			err,
		)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return transactionIntent{}, false, errors.New(
			"update intent must be regular and not a symlink",
		)
	}
	file, err := os.Open(path)
	if err != nil {
		return transactionIntent{}, false, fmt.Errorf(
			"open update intent: %w",
			err,
		)
	}
	defer func() {
		_ = file.Close()
	}()
	content, err := io.ReadAll(io.LimitReader(file, maxIntentBytes+1))
	if err != nil {
		return transactionIntent{}, false, fmt.Errorf(
			"read update intent: %w",
			err,
		)
	}
	if len(content) > maxIntentBytes {
		return transactionIntent{}, false, errors.New(
			"update intent exceeds 4 MiB",
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var intent transactionIntent
	if err := decoder.Decode(&intent); err != nil {
		return transactionIntent{}, false, fmt.Errorf(
			"decode update intent: %w",
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return transactionIntent{}, false, errors.New(
			"update intent contains trailing JSON",
		)
	}
	if !intent.validPhase() {
		return transactionIntent{}, false, fmt.Errorf(
			"update intent has unsupported phase %q",
			intent.Phase,
		)
	}
	if err := verify(intent.Plan, intent.UpdateDigest); err != nil {
		return transactionIntent{}, false, fmt.Errorf(
			"verify stored update intent plan: %w",
			err,
		)
	}
	if !intent.matches(intent.Plan) {
		return transactionIntent{}, false, errors.New(
			"stored update intent identity is inconsistent",
		)
	}
	return intent, true, nil
}

func ResumePlan(
	catalogRoot,
	stateRoot,
	expectedDigest string,
) (Plan, catalog.Environment, bool, error) {
	if expectedDigest == "" {
		return Plan{}, catalog.Environment{}, false, nil
	}
	if err := ValidateCatalogRoot(catalogRoot); err != nil {
		return Plan{}, catalog.Environment{}, false, err
	}
	catalogLockPath, err := state.CatalogLockPath(stateRoot, catalogRoot)
	if err != nil {
		return Plan{}, catalog.Environment{}, false, err
	}
	intent, exists, err := readTransactionIntent(
		transactionIntentPath(catalogLockPath),
	)
	if err != nil {
		return Plan{}, catalog.Environment{}, false, err
	}
	if !exists || intent.UpdateDigest != expectedDigest {
		return Plan{}, catalog.Environment{}, false, nil
	}
	plan := intent.Plan
	if err := Verify(plan, expectedDigest); err != nil {
		return Plan{}, catalog.Environment{}, false, err
	}
	current, err := catalog.Load(catalogRoot)
	if err != nil {
		return Plan{}, catalog.Environment{}, false, stale(errors.New(
			"catalog changed after review and can no longer be loaded",
		))
	}
	revision, err := catalog.Revision(current)
	if err != nil {
		return Plan{}, catalog.Environment{}, false, err
	}
	currentIsBefore := revision == plan.BeforeCatalogRevision &&
		reflect.DeepEqual(current.Lock.Versions[plan.LockKey], plan.Old)
	currentIsAfter := revision == plan.AfterCatalogRevision &&
		reflect.DeepEqual(current.Lock.Versions[plan.LockKey], plan.New)
	switch {
	case currentIsBefore:
	case currentIsAfter && intent.Phase != intentPhasePrepared:
	default:
		return Plan{}, catalog.Environment{}, false, stale(errors.New(
			"catalog no longer matches the resumable update transaction",
		))
	}
	updated, err := updatedEnvironmentFromIntent(
		current,
		plan,
		intent,
		currentIsAfter,
	)
	if err != nil {
		return Plan{}, catalog.Environment{}, false, err
	}
	return plan, updated, true, nil
}

func writeTransactionIntent(
	path string,
	intent transactionIntent,
) error {
	if !intent.validPhase() {
		return fmt.Errorf("cannot write update intent phase %q", intent.Phase)
	}
	info, err := os.Lstat(path)
	if err == nil &&
		(info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return errors.New("update intent must be regular and not a symlink")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect update intent: %w", err)
	}
	encoded, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return fmt.Errorf("encode update intent: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := durable.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("publish update intent durably: %w", err)
	}
	return nil
}
