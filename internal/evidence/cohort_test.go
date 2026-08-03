package evidence

import (
	"testing"
	"time"
)

func TestCertificationCohortIsStrictAndCommitBounded(t *testing.T) {
	if err := ValidateCertificationCohort(fixtureCohort); err != nil {
		t.Fatalf("ValidateCertificationCohort(): %v", err)
	}
	prefix, err := CertificationCohortCommitPrefix(fixtureCohort)
	if err != nil || prefix != fixtureCommit[:8] {
		t.Fatalf("cohort commit prefix = %q error=%v", prefix, err)
	}
	timestamp, err := CertificationCohortTimestamp(fixtureCohort)
	if err != nil ||
		!timestamp.Equal(time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("cohort timestamp = %s error=%v", timestamp, err)
	}
	for _, invalid := range []string{
		"",
		"cert-20260730T000000Z-0123456",
		"cert-20260730T000000+0900-01234567",
		"cert-20261330T000000Z-01234567",
		"cert-20260730T000000Z-0123456G",
	} {
		if err := ValidateCertificationCohort(invalid); err == nil {
			t.Fatalf("ValidateCertificationCohort(%q) succeeded", invalid)
		}
	}
}

func TestVerifyRejectsDifferentCertificationCohort(t *testing.T) {
	bundle := certifyFixture(t, true)
	_, err := Verify(bundle, VerifyOptions{
		ExpectedCohort: "cert-20260730T010000Z-01234567",
	})
	assertErrorContains(t, err, "wrong certification cohort")
}

func TestVerifyRejectsCohortFromAnotherCommit(t *testing.T) {
	bundle := certifyFixture(t, true)
	rewriteManifest(t, bundle, func(manifest *Manifest) {
		manifest.Cohort = "cert-20260730T000000Z-deadbeef"
	})
	_, err := Verify(bundle, VerifyOptions{})
	assertErrorContains(t, err, "does not match the production CLI commit")
}

func TestVerifyFreshnessUsesManifestCaptureCompletionTime(t *testing.T) {
	bundle := certifyFixture(t, true)
	captured := time.Unix(1<<40, 0).UTC()
	if _, err := Verify(bundle, VerifyOptions{
		Now: captured.Add(24 * time.Hour), MaxAge: 24 * time.Hour,
	}); err != nil {
		t.Fatalf("Verify(exact freshness boundary): %v", err)
	}
	_, err := Verify(bundle, VerifyOptions{
		Now:    captured.Add(24*time.Hour + time.Second),
		MaxAge: 24 * time.Hour,
	})
	assertErrorContains(t, err, "timestamp is stale")
}
