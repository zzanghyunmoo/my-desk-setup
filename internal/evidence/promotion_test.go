package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func TestCertifyRejectsUnexpectedBinarySHA256BeforeExecution(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "mds")
	if err := os.WriteFile(
		binary,
		[]byte("not the release binary"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "evidence")
	_, err := Certify(context.Background(), CertifyRequest{
		MDSPath: binary, TargetID: "lima-guest:mds", OutputDir: output,
		Cohort: fixtureCohort, All: true,
		ExpectedBinarySHA256: strings.Repeat("f", 64),
		ExpectedPlanDigest:   "sha256:" + strings.Repeat("e", 64),
		Getenv: func(key string) string {
			if key == "MDS_EXPECTED_GUEST_CREATION_NONCE" {
				return strings.Repeat("a", 64)
			}
			return ""
		},
	})
	if err == nil || !strings.Contains(err.Error(), "binary checksum mismatch") {
		t.Fatalf("Certify(binary mismatch) error = %v", err)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("evidence output exists after binary mismatch: %v", statErr)
	}
}

func TestCertifyRequiresReviewedBinaryAndPlanBeforeExecution(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "mds")
	if err := os.WriteFile(binary, []byte("must not execute"), 0o700); err != nil {
		t.Fatal(err)
	}
	binarySHA256 := fileSHA256Fixture(t, binary)
	for _, test := range []struct {
		name           string
		expectedBinary string
		expectedPlan   string
		want           string
	}{
		{
			name:         "missing binary identity",
			expectedPlan: "sha256:" + strings.Repeat("e", 64),
			want:         "binary SHA-256 is required",
		},
		{
			name:           "missing plan identity",
			expectedBinary: binarySHA256,
			want:           "plan digest is required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-"))
			_, err := Certify(context.Background(), CertifyRequest{
				MDSPath: binary, TargetID: "lima-guest:mds", OutputDir: output,
				Cohort: fixtureCohort, All: true,
				ExpectedBinarySHA256: test.expectedBinary,
				ExpectedPlanDigest:   test.expectedPlan,
			})
			assertErrorContains(t, err, test.want)
			if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
				t.Fatalf("evidence output exists after identity rejection: %v", statErr)
			}
		})
	}
}

func TestVerifyBindsExactBinarySHA256(t *testing.T) {
	bundle := certifyFixture(t, true)
	manifest, err := Verify(bundle, VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify(): %v", err)
	}
	if len(manifest.BinarySHA256) != 64 {
		t.Fatalf("binary_sha256 = %q", manifest.BinarySHA256)
	}
	_, err = Verify(bundle, VerifyOptions{
		ExpectedBinarySHA256: strings.Repeat("0", 64),
	})
	assertErrorContains(t, err, "binary")
}

func TestPublicationPolicyAcceptsOnlyHonestManualActionRequired(t *testing.T) {
	manual := certifyFixture(t, true)
	rewriteAsManualActionRequired(t, manual)
	manifest, err := Verify(manual, VerifyOptions{
		RequirePublicationAcceptable: true,
	})
	if err != nil {
		t.Fatalf("Verify(manual action-required): %v", err)
	}
	if manifest.Status != StatusBlocked {
		t.Fatalf("status = %q, want blocked", manifest.Status)
	}

	tests := []struct {
		name   string
		bundle func(*testing.T) string
		mutate func(*testing.T, string)
	}{
		{
			name:   "planned unready",
			bundle: func(t *testing.T) string { return certifyFixture(t, false) },
		},
		{
			name:   "planned conflict",
			bundle: func(t *testing.T) string { return certifyFixture(t, false) },
			mutate: rewriteAsConflict,
		},
		{
			name:   "unsupported",
			bundle: func(t *testing.T) string { return certifyFixture(t, true) },
			mutate: rewriteAsUnsupported,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := test.bundle(t)
			if test.mutate != nil {
				test.mutate(t, bundle)
			}
			_, err := Verify(bundle, VerifyOptions{
				RequirePublicationAcceptable: true,
			})
			assertErrorContains(t, err, "publication")
		})
	}
}

func rewriteAsManualActionRequired(t *testing.T, bundle string) {
	t.Helper()
	rewriteEvidenceOutcome(
		t,
		bundle,
		planning.ActionActionRequired,
		"manual installation remains",
		"action-required",
		"action-required",
	)
}

func rewriteAsConflict(t *testing.T, bundle string) {
	t.Helper()
	rewriteEvidenceOutcome(
		t,
		bundle,
		planning.ActionPlanned,
		"",
		"conflict",
		"version-conflict",
	)
}

func rewriteAsUnsupported(t *testing.T, bundle string) {
	t.Helper()
	rewriteEvidenceOutcome(
		t,
		bundle,
		planning.ActionUnsupported,
		"target is unsupported",
		"unsupported",
		"unsupported",
	)
}

func rewriteEvidenceOutcome(
	t *testing.T,
	bundle string,
	actionStatus planning.ActionStatus,
	actionReason,
	checkStatus,
	reasonCode string,
) {
	t.Helper()
	var plan planning.Plan
	readJSON(t, bundle+"/"+PlanFile, &plan)
	plan.Actions[0].Status = actionStatus
	plan.Actions[0].Reason = actionReason
	plan.Blockers = nil
	if actionStatus != planning.ActionPlanned {
		plan.Blockers = []planning.Blocker{{
			ActionID: plan.Actions[0].ID,
			Status:   actionStatus,
			Reason:   actionReason,
		}}
	}
	var err error
	plan.Digest, err = planning.Digest(plan)
	if err != nil {
		t.Fatalf("Digest(): %v", err)
	}

	var snapshot DoctorSnapshot
	readJSON(t, bundle+"/"+DoctorFile, &snapshot)
	snapshot.Ready = false
	snapshot.Checks[0].Status = checkStatus
	snapshot.Checks[0].ReasonCode = reasonCode
	snapshot.Checks[0].InstalledVersion = ""
	snapshot.Checks[0].VerifiedVersion = ""

	var manifest Manifest
	readJSON(t, bundle+"/"+ManifestFile, &manifest)
	manifest.Status = StatusBlocked
	manifest.PlanDigest = plan.Digest
	manifest.Components = append([]ComponentCheck(nil), snapshot.Checks...)
	if manifest.ApplyReceipt == nil {
		t.Fatal("fixture manifest is missing apply receipt")
	}
	manifest.ApplyReceipt.PlanDigest = plan.Digest
	manifest.ApplyReceipt.Complete = false
	manifest.ApplyReceipt.Outcomes[0].Status = string(actionStatus)
	if actionStatus == planning.ActionPlanned {
		manifest.ApplyReceipt.Outcomes[0].Status = "failed"
	}
	manifest.ApplyReceipt.Outcomes[0].Noop = false
	manifest.ApplyReceipt.Outcomes[0].VerifiedVersion = ""
	manifest.RepeatReceipt = nil

	writeJSON(t, bundle+"/"+PlanFile, plan)
	writeJSON(t, bundle+"/"+DoctorFile, snapshot)
	writeJSON(t, bundle+"/"+ManifestFile, manifest)
	writeChecksums(t, bundle)
}
