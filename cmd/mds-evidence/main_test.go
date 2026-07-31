package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCertifyRequiresExplicitBinaryTargetAndOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"certify", "--all"}, &stdout, &stderr)

	if exitCode == 0 {
		t.Fatal("run() exit code = 0, want required-argument failure")
	}
	if !strings.Contains(stderr.String(), "mds") {
		t.Fatalf("stderr = %q, want missing mds", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestVerifyRequiresBundle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"verify", "--require-verified"}, &stdout, &stderr)

	if exitCode == 0 || !strings.Contains(stderr.String(), "bundle") {
		t.Fatalf("exit=%d stderr=%q, want bundle failure", exitCode, stderr.String())
	}
}

func TestRequireVerifiedRequiresExternalReleaseExpectations(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run(
		[]string{"verify", "--bundle", "unused", "--require-verified"},
		&stdout,
		&stderr,
	)

	if exitCode == 0 ||
		!strings.Contains(stderr.String(), "requires expected CLI") {
		t.Fatalf(
			"exit=%d stderr=%q, want external expectation failure",
			exitCode,
			stderr.String(),
		)
	}
}

func TestCertifyRejectsLegacyNonceFlagWithoutEchoingRawValue(t *testing.T) {
	rawNonce := strings.Repeat("a", 64)
	var stdout, stderr bytes.Buffer
	exitCode := run(
		[]string{
			"certify",
			"--expected-guest-creation-nonce",
			rawNonce,
		},
		&stdout,
		&stderr,
	)
	if exitCode == 0 ||
		!strings.Contains(stderr.String(), "unknown flag") {
		t.Fatalf(
			"exit=%d stderr=%q, want legacy flag rejection",
			exitCode,
			stderr.String(),
		)
	}
	if strings.Contains(stderr.String(), rawNonce) ||
		strings.Contains(stdout.String(), rawNonce) {
		t.Fatal("legacy nonce flag rejection echoed raw nonce")
	}
}

func TestCertifyHelpDoesNotExposeRawNonceFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"certify", "--help"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), "expected-guest-creation-nonce") {
		t.Fatalf("certify help exposes removed raw nonce flag:\n%s", stdout.String())
	}
}

func TestCertifyRequiresImmutableCohort(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run(
		[]string{
			"certify",
			"--mds", "/unused/mds",
			"--target", "macos-host:local",
			"--output", "/unused/evidence",
			"--all",
		},
		&stdout,
		&stderr,
	)
	if exitCode == 0 || !strings.Contains(stderr.String(), "cohort") {
		t.Fatalf("exit=%d stderr=%q, want cohort failure", exitCode, stderr.String())
	}
}
