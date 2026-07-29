package transport

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestCommandEnvironmentDropsInheritedCredentials(t *testing.T) {
	t.Setenv("PATH", "/safe/bin")
	t.Setenv("HOME", "/safe/home")
	t.Setenv("MDS_TEST_API_TOKEN", "must-not-leak")

	environment, err := commandEnvironment(nil)
	if err != nil {
		t.Fatalf("commandEnvironment(): %v", err)
	}

	if !slices.Contains(environment, "PATH=/safe/bin") {
		t.Fatalf("environment = %q, want safe PATH", environment)
	}
	if !slices.Contains(environment, "HOME=/safe/home") {
		t.Fatalf("environment = %q, want safe HOME", environment)
	}
	for _, entry := range environment {
		if strings.Contains(entry, "MDS_TEST_API_TOKEN") ||
			strings.Contains(entry, "must-not-leak") {
			t.Fatalf("environment leaked inherited credential: %q", entry)
		}
	}
}

func TestCommandEnvironmentAppliesSortedOverridesWithoutDuplicates(t *testing.T) {
	t.Setenv("PATH", "/inherited/bin")
	t.Setenv("HOME", "/safe/home")

	environment, err := commandEnvironment(map[string]string{
		"Z_OPTION": "last",
		"PATH":     "/managed/bin",
		"A_OPTION": "first",
	})
	if err != nil {
		t.Fatalf("commandEnvironment(): %v", err)
	}

	if !slices.IsSorted(environment) {
		t.Fatalf("environment = %q, want sorted entries", environment)
	}
	for _, want := range []string{
		"A_OPTION=first",
		"HOME=/safe/home",
		"PATH=/managed/bin",
		"Z_OPTION=last",
	} {
		if !slices.Contains(environment, want) {
			t.Fatalf("environment = %q, missing %q", environment, want)
		}
	}
	pathEntries := 0
	for _, entry := range environment {
		if strings.HasPrefix(entry, "PATH=") {
			pathEntries++
		}
		if entry == "PATH=/inherited/bin" {
			t.Fatalf("environment retained overridden PATH: %q", environment)
		}
	}
	if pathEntries != 1 {
		t.Fatalf("environment = %q, want one PATH entry", environment)
	}
}

func TestCommandEnvironmentRejectsCredentialOverrides(t *testing.T) {
	_, err := commandEnvironment(map[string]string{
		"OPENAI_API_KEY": "must-not-pass",
	})
	if err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("commandEnvironment() error = %v, want credential rejection", err)
	}
}

func TestExecutorDoesNotPassInheritedCredentials(t *testing.T) {
	t.Setenv("MDS_TEST_API_TOKEN", "must-not-leak")

	result, err := (Executor{}).Run(
		context.Background(),
		os.Args[0],
		[]string{"-test.run=TestExecutorEnvironmentHelper"},
		map[string]string{"MDS_EXECUTOR_HELPER": "1"},
		"",
		DefaultTimeout,
		DefaultOutputLimit,
	)
	if err != nil {
		t.Fatalf("Executor.Run(): %v\nstderr: %s", err, result.Stderr)
	}
}

func TestExecutorEnvironmentHelper(t *testing.T) {
	if os.Getenv("MDS_EXECUTOR_HELPER") != "1" {
		return
	}
	if value := os.Getenv("MDS_TEST_API_TOKEN"); value != "" {
		_, _ = fmt.Fprintf(os.Stderr, "inherited credential leaked: %q", value)
		os.Exit(1)
	}
}

func TestLimitedBufferTruncatesWithoutShortWrite(t *testing.T) {
	buffer := newLimitedBuffer(4)
	input := []byte("abcdefgh")
	written, err := buffer.Write(input)
	if err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if written != len(input) {
		t.Fatalf("Write() = %d, want %d", written, len(input))
	}
	if got := buffer.String(); !strings.Contains(got, "abcd") ||
		!strings.Contains(got, "[output truncated]") {
		t.Fatalf("String() = %q, want prefix and truncation marker", got)
	}
}
