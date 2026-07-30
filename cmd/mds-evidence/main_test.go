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
