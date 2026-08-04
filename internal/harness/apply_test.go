package harness

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestApplyRunsOneExactIsolatedChildInvocation(t *testing.T) {
	request := previewRequest(t, []string{"opencode", "claude-code", "codex"})
	expectedDigest := strings.Repeat("a", 64)
	port := &recordingPort{}
	port.result = applyProcessResult(
		t, request, expectedDigest,
		[]string{"native:opencode", "native:claude-code"},
	)

	got, err := (Runner{Port: port}).Apply(
		context.Background(), request, expectedDigest,
	)
	if err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if port.calls != 1 {
		t.Fatalf("child calls = %d, want exactly one", port.calls)
	}
	wantArguments := []string{
		request.Entrypoint,
		"setup", "--profile", "mds-host", "--agents",
		"claude-code,codex,opencode", "--root", request.StateRoot, "--json",
		"--apply", "--digest", expectedDigest,
	}
	if port.command.Executable != request.NodeExecutable ||
		!reflect.DeepEqual(port.command.Arguments, wantArguments) ||
		port.command.EnvironmentMode != transport.EnvironmentReplace {
		t.Fatalf("command = %+v, want exact isolated apply", port.command)
	}
	for _, argument := range port.command.Arguments {
		for _, forbidden := range []string{"auth", "login", "sh", "bash", "cmd.exe", "powershell"} {
			if argument == forbidden {
				t.Fatalf("apply argv contains forbidden %q: %q", forbidden, port.command.Arguments)
			}
		}
	}
	if got.Digest != expectedDigest || got.Status != "ready" ||
		!reflect.DeepEqual(got.CompletedActionIDs, []string{
			"native:claude-code", "native:opencode",
		}) {
		t.Fatalf("Apply() = %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal(apply result): %v", err)
	}
	for _, forbidden := range []string{
		request.Home, request.StateRoot, request.Entrypoint,
		"must-not-leak", "/private/",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("apply result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestApplySupportsCanonicalEmptyAndSelectedAgentSets(t *testing.T) {
	for _, agents := range [][]string{
		nil,
		{"claude-code"},
		{"claude-code", "codex", "opencode"},
	} {
		t.Run(strings.Join(agents, "-")+"empty", func(t *testing.T) {
			request := previewRequest(t, agents)
			digest := strings.Repeat("a", 64)
			port := &recordingPort{result: applyProcessResult(t, request, digest, nil)}
			got, err := (Runner{Port: port}).Apply(context.Background(), request, digest)
			if err != nil {
				t.Fatalf("Apply(): %v", err)
			}
			if !reflect.DeepEqual(got.SelectedAgents, sortedAgentIDs(request.AgentExecutables)) {
				t.Fatalf("selected agents = %q", got.SelectedAgents)
			}
			wantArgument := "none"
			if len(agents) > 0 {
				wantArgument = strings.Join(sortedAgentIDs(request.AgentExecutables), ",")
			}
			if port.command.Arguments[5] != wantArgument {
				t.Fatalf("--agents = %q, want %q", port.command.Arguments[5], wantArgument)
			}
		})
	}
}

func TestApplyRequiresExactDigestAndRejectsPreviewMismatch(t *testing.T) {
	request := previewRequest(t, nil)
	for _, digest := range []string{"", "sha256:" + strings.Repeat("a", 64), strings.Repeat("A", 64)} {
		port := &recordingPort{}
		_, err := (Runner{Port: port}).Apply(context.Background(), request, digest)
		assertHarnessErrorCode(t, err, "expected-digest")
		if port.calls != 0 {
			t.Fatalf("invalid digest executed child: %d calls", port.calls)
		}
	}

	port := &recordingPort{result: applyProcessResult(
		t, request, strings.Repeat("a", 64), nil,
	)}
	_, err := (Runner{Port: port}).Apply(
		context.Background(), request, strings.Repeat("d", 64),
	)
	assertHarnessErrorCode(t, err, "digest-mismatch")
}

func TestApplyMapsStalePartialTimeoutAndProcessFailures(t *testing.T) {
	request := previewRequest(t, nil)
	digest := strings.Repeat("a", 64)
	tests := []struct {
		name string
		exit int
		err  error
		want string
	}{
		{name: "stale", exit: 4, want: "stale-preview"},
		{name: "partial", exit: 5, want: "partial-unready"},
		{name: "timeout", exit: -1, err: context.DeadlineExceeded, want: "timeout"},
		{name: "process", exit: 9, want: "process"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			port := &recordingPort{result: transport.Result{
				Executable: request.NodeExecutable, ExitCode: test.exit,
				Stderr: "token=must-not-leak /private/home",
			}}
			if test.err != nil {
				port.err = test.err
			} else {
				port.err = &transport.CommandError{Result: port.result}
			}
			_, err := (Runner{Port: port}).Apply(context.Background(), request, digest)
			assertHarnessErrorCode(t, err, test.want)
			if strings.Contains(err.Error(), "must-not-leak") || strings.Contains(err.Error(), "/private/") {
				t.Fatalf("error leaked child output: %v", err)
			}
		})
	}
}

func TestApplyRejectsUnknownMalformedTruncatedAndInconsistentJSON(t *testing.T) {
	request := previewRequest(t, nil)
	digest := strings.Repeat("a", 64)
	valid := applyProcessResult(t, request, digest, nil)
	var unknown map[string]any
	if err := json.Unmarshal([]byte(valid.Stdout), &unknown); err != nil {
		t.Fatalf("Unmarshal(valid apply): %v", err)
	}
	unknown["secret"] = "must-not-leak"
	unknownJSON, err := json.Marshal(unknown)
	if err != nil {
		t.Fatalf("Marshal(unknown): %v", err)
	}
	inconsistent := applyProcessResult(t, request, digest, nil)
	var inconsistentValue map[string]any
	if err := json.Unmarshal([]byte(inconsistent.Stdout), &inconsistentValue); err != nil {
		t.Fatalf("Unmarshal(inconsistent): %v", err)
	}
	inconsistentValue["state"] = "partial-unready"
	inconsistentJSON, _ := json.Marshal(inconsistentValue)
	tests := []struct {
		name   string
		stdout string
		want   string
	}{
		{name: "unknown", stdout: string(unknownJSON), want: "invalid-json"},
		{name: "malformed", stdout: "{not-json", want: "invalid-json"},
		{name: "truncated", stdout: "{}\n[output truncated]\n", want: "output-limit"},
		{name: "inconsistent", stdout: string(inconsistentJSON), want: "contract"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			port := &recordingPort{result: transport.Result{
				Executable: request.NodeExecutable, ExitCode: 0, Stdout: test.stdout,
			}}
			_, err := (Runner{Port: port}).Apply(context.Background(), request, digest)
			assertHarnessErrorCode(t, err, test.want)
		})
	}
}

func applyProcessResult(
	t *testing.T,
	request Request,
	digest string,
	completed []string,
) transport.Result {
	t.Helper()
	preview := previewResult(t, request, "preview", 2, nil, nil)
	var previewEnvelope map[string]any
	if err := json.Unmarshal([]byte(preview.Stdout), &previewEnvelope); err != nil {
		t.Fatalf("Unmarshal(preview): %v", err)
	}
	previewValue := previewEnvelope["preview"].(map[string]any)
	previewValue["digest"] = digest
	previewValue["plan"].(map[string]any)["digest"] = digest
	payload := map[string]any{
		"apply": map[string]any{
			"status": "ready", "completedActionIds": completed,
		},
		"command": "setup", "exitCode": 0,
		"preview": previewValue, "state": "ready",
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal(apply): %v", err)
	}
	return transport.Result{
		Executable: request.NodeExecutable, ExitCode: 0,
		Stdout: string(encoded) + "\n",
	}
}

func assertHarnessErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	var childErr *Error
	if !errors.As(err, &childErr) || childErr.Code != want {
		t.Fatalf("error = %#v, want harness code %q", err, want)
	}
}
