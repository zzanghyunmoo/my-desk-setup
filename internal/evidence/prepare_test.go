package evidence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

func TestPrepareDerivesExactPlanIdentityWithoutApplying(t *testing.T) {
	bundle := certifyFixture(t, true)
	root := filepath.Dir(bundle)
	applyCountPath := filepath.Join(root, "fixture-apply-count")
	if err := os.Remove(applyCountPath); err != nil {
		t.Fatalf("reset fake apply count: %v", err)
	}
	var plan planning.Plan
	readJSON(t, filepath.Join(root, "fixture-plan.json"), &plan)
	binaryPath := filepath.Join(root, "fake-mds")

	prepared, err := Prepare(context.Background(), PrepareRequest{
		MDSPath:              binaryPath,
		TargetID:             plan.Target.ID.String(),
		ExpectedBinarySHA256: fileSHA256Fixture(t, binaryPath),
		Components:           []string{"go"},
		RuntimeProbe: func(id target.ID) (target.Facts, error) {
			if id != plan.Target.ID {
				return target.Facts{}, errors.New("unexpected target")
			}
			return plan.Target, nil
		},
	})
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	if prepared.SchemaVersion != PreparationSchema ||
		prepared.PlanDigest != plan.Digest ||
		prepared.CatalogRevision != plan.CatalogRevision ||
		prepared.BinarySHA256 != fileSHA256Fixture(t, binaryPath) ||
		prepared.GuestCreationNonceCommitment !=
			plan.Target.ImageCreationNonceCommitment {
		t.Fatalf("preparation = %+v", prepared)
	}
	if _, err := os.Lstat(applyCountPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Prepare executed apply: %v", err)
	}
}

func TestCertificationRequiresReviewedCommitmentOnlyForGuestCapture(t *testing.T) {
	guestID, err := target.ParseID("lima-guest:mds")
	if err != nil {
		t.Fatalf("ParseID(guest): %v", err)
	}
	if err := validateExpectedGuestCreationNonceCommitment(guestID, ""); err == nil {
		t.Fatal("guest capture accepted a missing reviewed commitment")
	}
	commitment := fixtureNonceCommitment(t, strings.Repeat("a", 64))
	if err := validateExpectedGuestCreationNonceCommitment(
		guestID,
		commitment,
	); err != nil {
		t.Fatalf("guest commitment rejected: %v", err)
	}
	hostID, err := target.ParseID("macos-host:local")
	if err != nil {
		t.Fatalf("ParseID(host): %v", err)
	}
	if err := validateExpectedGuestCreationNonceCommitment(
		hostID,
		commitment,
	); err == nil {
		t.Fatal("host capture accepted a guest commitment")
	}
}
