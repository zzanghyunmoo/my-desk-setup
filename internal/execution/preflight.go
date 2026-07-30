package execution

import (
	"errors"
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

type StalePlanError struct {
	Cause error
}

func (err *StalePlanError) Error() string {
	if err == nil || err.Cause == nil {
		return "reviewed plan is stale"
	}
	return err.Cause.Error()
}

func (err *StalePlanError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

func IsStalePlan(err error) bool {
	var stale *StalePlanError
	return errors.As(err, &stale)
}

func VerifyPlan(plan planning.Plan, expectedDigest string) error {
	if expectedDigest == "" {
		return fmt.Errorf("expected plan digest is required")
	}
	recomputed, err := planning.Digest(plan)
	if err != nil {
		return err
	}
	if plan.Digest != recomputed {
		return &StalePlanError{Cause: fmt.Errorf(
			"plan payload is stale: recorded %s recomputed %s",
			plan.Digest,
			recomputed,
		)}
	}
	if expectedDigest != recomputed {
		return &StalePlanError{Cause: fmt.Errorf(
			"plan digest mismatch: expected %s got %s",
			expectedDigest,
			recomputed,
		)}
	}
	return nil
}
