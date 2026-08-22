package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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

func TestCommandEnvironmentRejectsCredentialShapedValues(t *testing.T) {
	_, err := commandEnvironment(map[string]string{
		"MDS_OPTION": "ghp_canary123",
	})
	if err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("commandEnvironment() error = %v, want credential-shaped value rejection", err)
	}
}

func TestGuestArgvReplaceModeClearsAmbientGuestEnvironment(t *testing.T) {
	executable, arguments := guestArgv(Command{
		Executable:      "/managed/tool",
		Environment:     map[string]string{"HOME": "/isolated/home"},
		EnvironmentMode: EnvironmentReplace,
	})
	if executable != "env" {
		t.Fatalf("executable = %q, want env", executable)
	}
	want := []string{"-i", "HOME=/isolated/home", "/managed/tool"}
	if !slices.Equal(arguments, want) {
		t.Fatalf("arguments = %q, want exact replace argv %q", arguments, want)
	}
}

func TestCommandEnvironmentReplaceModeDoesNotInheritSafeAmbientValues(t *testing.T) {
	t.Setenv("PATH", "/ambient/bin")
	t.Setenv("HOME", "/ambient/home")
	t.Setenv("LANG", "ambient")

	environment, err := commandEnvironmentFor(
		map[string]string{
			"HOME": "/isolated/home",
			"PATH": "/trusted/bin",
		},
		EnvironmentReplace,
	)
	if err != nil {
		t.Fatalf("commandEnvironmentFor(): %v", err)
	}
	if got, want := environment, []string{
		"HOME=/isolated/home",
		"PATH=/trusted/bin",
	}; !slices.Equal(got, want) {
		t.Fatalf("environment = %q, want %q", got, want)
	}
}

func TestExecutorRunCommandReplaceModeIsExact(t *testing.T) {
	t.Setenv("HOME", "/ambient/home")
	t.Setenv("LANG", "ambient")
	result, err := (Executor{}).RunCommand(context.Background(), Command{
		Executable:      os.Args[0],
		Arguments:       []string{"-test.run=TestExecutorReplaceEnvironmentHelper"},
		Environment:     map[string]string{"MDS_ISOLATED_HELPER": "1"},
		EnvironmentMode: EnvironmentReplace,
	})
	if err != nil {
		t.Fatalf("RunCommand(): %v\nstderr: %s", err, result.Stderr)
	}
}

func TestExecutorReplaceEnvironmentHelper(t *testing.T) {
	if os.Getenv("MDS_ISOLATED_HELPER") != "1" {
		return
	}
	for _, key := range []string{"HOME", "LANG"} {
		if value := os.Getenv(key); value != "" {
			_, _ = fmt.Fprintf(os.Stderr, "ambient %s leaked", key)
			os.Exit(1)
		}
	}
}

func TestExecutorDoesNotPassInheritedCredentials(t *testing.T) {
	t.Setenv("MDS_TEST_API_TOKEN", "must-not-leak")

	result, err := (Executor{}).Run(
		context.Background(),
		os.Args[0],
		[]string{"-test.run=TestExecutorEnvironmentHelper"},
		nil,
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

func TestExecutorPassesBoundedCommandStdin(t *testing.T) {
	result, err := (Executor{}).Run(
		context.Background(),
		os.Args[0],
		[]string{"-test.run=TestExecutorStdinHelper"},
		[]byte("reviewed bootstrap input"),
		map[string]string{"MDS_STDIN_HELPER": "1"},
		"",
		DefaultTimeout,
		DefaultOutputLimit,
	)
	if err != nil {
		t.Fatalf("Executor.Run(): %v\nstderr: %s", err, result.Stderr)
	}
	if result.Stdout != "reviewed bootstrap input" {
		t.Fatalf("stdout = %q, want passed stdin", result.Stdout)
	}
}

func TestExecutorTimeoutTerminatesProcessGroupAndDiscoverableDetachedChild(t *testing.T) {
	mutationPath := filepath.Join(t.TempDir(), "late-mutation")
	started := time.Now()
	_, err := (Executor{}).Run(
		context.Background(),
		os.Args[0],
		[]string{"-test.run=TestExecutorProcessTreeHelper"},
		nil,
		map[string]string{
			"MDS_TREE_HELPER":        "parent",
			"MDS_TREE_MUTATION_PATH": mutationPath,
		},
		"",
		150*time.Millisecond,
		DefaultOutputLimit,
	)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Executor.Run() error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Executor.Run() returned after %s, want bounded timeout", elapsed)
	}
	time.Sleep(900 * time.Millisecond)
	if _, err := os.Stat(mutationPath); !os.IsNotExist(err) {
		t.Fatalf("descendant mutation survived timeout: %v", err)
	}
}

func TestExecutorProcessTreeHelper(t *testing.T) {
	switch os.Getenv("MDS_TREE_HELPER") {
	case "parent":
		command := exec.Command(
			os.Args[0],
			"-test.run=TestExecutorProcessTreeHelper",
		)
		command.Env = append(os.Environ(), "MDS_TREE_HELPER=child")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		detachTestProcess(command)
		if err := command.Start(); err != nil {
			os.Exit(2)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "child":
		time.Sleep(600 * time.Millisecond)
		if err := os.WriteFile(
			os.Getenv("MDS_TREE_MUTATION_PATH"),
			[]byte("survived"),
			0o600,
		); err != nil {
			os.Exit(3)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}

func TestExecutorStdinHelper(t *testing.T) {
	if os.Getenv("MDS_STDIN_HELPER") != "1" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(data)
	os.Exit(0)
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

func TestCommandErrorIncludesBoundedRedactedDiagnostics(t *testing.T) {
	secret := strings.Repeat("x", DiagnosticLimit)
	err := (&CommandError{Result: Result{
		Executable: "installer",
		ExitCode:   23,
		Stderr: "token=do-not-leak " +
			"https://user:password@example.com/download?signature=do-not-leak " +
			secret,
	}}).Error()
	for _, forbidden := range []string{"do-not-leak", "user:password"} {
		if strings.Contains(err, forbidden) {
			t.Fatalf("diagnostic leaked %q: %s", forbidden, err)
		}
	}
	if !strings.Contains(err, "token=[redacted]") ||
		!strings.Contains(err, "signature=%5Bredacted%5D") ||
		!strings.Contains(err, "[diagnostic truncated]") {
		t.Fatalf("diagnostic = %q, want redaction and bound", err)
	}
}
