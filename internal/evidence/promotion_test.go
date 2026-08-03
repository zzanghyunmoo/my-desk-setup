package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		ExpectedGuestCreationNonceCommitment: fixtureNonceCommitment(
			t,
			strings.Repeat("a", 64),
		),
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
				ExpectedGuestCreationNonceCommitment: fixtureNonceCommitment(
					t,
					strings.Repeat("a", 64),
				),
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
