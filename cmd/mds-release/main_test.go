package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"publish"}, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "build|verify") {
		t.Fatalf("usage = %q", stderr.String())
	}
}

func TestRunBuildRequiresCanonicalDate(t *testing.T) {
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{
			"build",
			"--version", "0.1.0",
			"--commit", "0123456789abcdef0123456789abcdef01234567",
			"--date", "not-a-date",
		},
		&stderr,
	)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "parse release date") {
		t.Fatalf("error = %q", stderr.String())
	}
}
