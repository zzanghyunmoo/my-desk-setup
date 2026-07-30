package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
)

func TestBinaryPreservesJSONErrorContractAndExitClass(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mds")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mds: %v\n%s", err, output)
	}

	command := exec.Command(
		binary,
		"plan",
		"--all",
		"--component", "wezterm",
		"--format", "json",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) ||
		exitError.ExitCode() != cli.ExitInvalidInput {
		t.Fatalf(
			"mds exit=%v stderr=%q, want %d",
			err,
			stderr.String(),
			cli.ExitInvalidInput,
		)
	}
	if stdout.Len() != 0 {
		t.Fatalf("mds stdout=%q, want empty", stdout.String())
	}
	decoder := json.NewDecoder(bytes.NewReader(stderr.Bytes()))
	decoder.DisallowUnknownFields()
	var envelope cli.ErrorEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v\n%s", err, stderr.String())
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("error envelope has trailing output: %v\n%s", err, stderr.String())
	}
	if envelope.SchemaVersion != cli.ErrorSchema ||
		envelope.Code != "invalid-input" {
		t.Fatalf("error envelope = %+v", envelope)
	}
}
