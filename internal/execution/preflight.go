package execution

import (
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func VerifyPlan(plan planning.Plan, expectedDigest string) error {
	if expectedDigest == "" {
		return fmt.Errorf("expected plan digest is required")
	}
	recomputed, err := planning.Digest(plan)
	if err != nil {
		return err
	}
	if plan.Digest != recomputed {
		return fmt.Errorf("plan payload is stale: recorded %s recomputed %s", plan.Digest, recomputed)
	}
	if expectedDigest != recomputed {
		return fmt.Errorf("plan digest mismatch: expected %s got %s", expectedDigest, recomputed)
	}
	return nil
}
