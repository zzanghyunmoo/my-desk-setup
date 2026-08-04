package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

type recordingPort struct {
	command transport.Command
	result  transport.Result
	err     error
	calls   int
}

func (port *recordingPort) Run(
	_ context.Context,
	command transport.Command,
) (transport.Result, error) {
	port.calls++
	port.command = command
	return port.result, port.err
}

func TestPreviewRunsFixedIsolatedMDSHostCommand(t *testing.T) {
	request := previewRequest(t, []string{"opencode", "claude-code", "codex"})
	port := &recordingPort{}
	port.result = previewResult(t, request, "preview", 2, nil, nil)
	port.err = &transport.CommandError{Result: port.result}

	got, err := (Runner{Port: port}).Preview(context.Background(), request)
	if err != nil {
		t.Fatalf("Preview(): %v", err)
	}
	wantAgents := []string{"claude-code", "codex", "opencode"}
	if !reflect.DeepEqual(got.SelectedAgents, wantAgents) || got.Readiness != "preview" ||
		got.Digest != strings.Repeat("a", 64) || got.CatalogRevision != strings.Repeat("b", 64) {
		t.Fatalf("Preview() = %+v", got)
	}
	wantArguments := []string{
		request.Entrypoint,
		"setup", "--profile", "mds-host", "--agents",
		"claude-code,codex,opencode", "--root", request.StateRoot, "--json",
	}
	if port.command.Executable != request.NodeExecutable ||
		!reflect.DeepEqual(port.command.Arguments, wantArguments) {
		t.Fatalf("command = %+v, want fixed argv %q", port.command, wantArguments)
	}
	if port.command.EnvironmentMode != transport.EnvironmentReplace {
		t.Fatalf("environment mode = %q, want replace", port.command.EnvironmentMode)
	}
	for _, argument := range port.command.Arguments {
		for _, forbidden := range []string{
			"--apply", "--digest", "auth", "login", "sh", "bash", "cmd.exe", "powershell",
		} {
			if argument == forbidden {
				t.Fatalf("child argv contains forbidden %q: %q", forbidden, port.command.Arguments)
			}
		}
	}
	for _, key := range []string{"OPENAI_API_KEY", "GITHUB_TOKEN", "AWS_PROFILE", "CI_JOB_TOKEN"} {
		if _, exists := port.command.Environment[key]; exists {
			t.Fatalf("isolated environment contains %s", key)
		}
	}
	pathValue := port.command.Environment["PATH"]
	for _, executable := range request.AgentExecutables {
		if !pathListContains(pathValue, filepath.Dir(executable), request.Platform) {
			t.Fatalf("trusted PATH %q omits %q", pathValue, filepath.Dir(executable))
		}
	}
	if !pathListContains(pathValue, filepath.Dir(request.NodeExecutable), request.Platform) {
		t.Fatalf("trusted PATH %q omits Node directory", pathValue)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal(result): %v", err)
	}
	for _, forbidden := range []string{request.Home, request.StateRoot, request.Entrypoint, "must-not-leak"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("returned result serialized private value %q: %s", forbidden, encoded)
		}
	}
}

func TestPreviewEmptyAgentSetRunsCanonicalNoneSelection(t *testing.T) {
	request := previewRequest(t, nil)
	port := &recordingPort{}
	port.result = previewResult(t, request, "preview", 2, nil, nil)
	port.err = &transport.CommandError{Result: port.result}

	first, err := (Runner{Port: port}).Preview(context.Background(), request)
	if err != nil {
		t.Fatalf("Preview(first): %v", err)
	}
	if got := port.command.Arguments[5]; got != "none" {
		t.Fatalf("--agents value = %q, want none", got)
	}
	port.result = previewResult(t, request, "preview", 2, nil, nil)
	port.err = &transport.CommandError{Result: port.result}
	second, err := (Runner{Port: port}).Preview(context.Background(), request)
	if err != nil {
		t.Fatalf("Preview(second): %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("empty preview unstable:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestPreviewReturnsSecretFreeBlockedSummaryForExitThree(t *testing.T) {
	request := previewRequest(t, []string{"codex"})
	port := &recordingPort{}
	port.result = previewResult(
		t, request, "blocked", 3,
		[]string{"agent:codex", "native:codex"},
		[]string{"capability:optional"},
	)
	port.err = &transport.CommandError{Result: port.result}
	got, err := (Runner{Port: port}).Preview(context.Background(), request)
	if err != nil {
		t.Fatalf("Preview(): %v", err)
	}
	if got.Digest != "" || got.Readiness != "blocked" ||
		!reflect.DeepEqual(got.Blockers, []string{"agent:codex", "native:codex"}) {
		t.Fatalf("blocked result = %+v", got)
	}
}

func TestPreviewRejectsMalformedUnknownAndInconsistentOutput(t *testing.T) {
	request := previewRequest(t, nil)
	unknownPreview := previewResult(t, request, "preview", 2, nil, nil)
	var unknownEnvelope map[string]any
	if err := json.Unmarshal([]byte(unknownPreview.Stdout), &unknownEnvelope); err != nil {
		t.Fatalf("Unmarshal(valid preview): %v", err)
	}
	unknownEnvelope["preview"].(map[string]any)["unknownField"] = "must-not-leak"
	unknownPreviewJSON, err := json.Marshal(unknownEnvelope)
	if err != nil {
		t.Fatalf("Marshal(unknown preview): %v", err)
	}
	tests := []struct {
		name   string
		stdout string
		exit   int
		want   string
	}{
		{name: "malformed", stdout: "{not-json", exit: 2, want: "invalid-json"},
		{name: "unknown", stdout: `{"command":"setup","state":"preview","exitCode":2,"preview":{},"secret":"must-not-leak"}`, exit: 2, want: "invalid-json"},
		{name: "unknown preview", stdout: string(unknownPreviewJSON), exit: 2, want: "invalid-json"},
		{name: "truncated", stdout: "{}\n[output truncated]\n", exit: 2, want: "output-limit"},
		{name: "exit mismatch", stdout: string(previewResult(t, request, "preview", 3, nil, nil).Stdout), exit: 2, want: "contract"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			port := &recordingPort{result: transport.Result{
				Executable: request.NodeExecutable, Stdout: test.stdout, ExitCode: test.exit,
			}}
			port.err = &transport.CommandError{Result: port.result}
			_, err := (Runner{Port: port}).Preview(context.Background(), request)
			var childErr *Error
			if !errors.As(err, &childErr) || childErr.Code != test.want ||
				strings.Contains(err.Error(), "must-not-leak") {
				t.Fatalf("Preview() error = %#v, want code %q", err, test.want)
			}
		})
	}
}

func TestPreviewConfigurationDigestBindsChildPlanDigest(t *testing.T) {
	request := previewRequest(t, nil)
	firstPort := &recordingPort{}
	firstPort.result = previewResult(t, request, "preview", 2, nil, nil)
	firstPort.err = &transport.CommandError{Result: firstPort.result}
	first, err := (Runner{Port: firstPort}).Preview(context.Background(), request)
	if err != nil {
		t.Fatalf("Preview(first): %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(firstPort.result.Stdout), &envelope); err != nil {
		t.Fatalf("Unmarshal(preview): %v", err)
	}
	preview := envelope["preview"].(map[string]any)
	preview["digest"] = strings.Repeat("d", 64)
	preview["plan"].(map[string]any)["digest"] = strings.Repeat("d", 64)
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal(changed preview): %v", err)
	}
	secondPort := &recordingPort{result: transport.Result{
		Executable: request.NodeExecutable, Stdout: string(encoded), ExitCode: 2,
	}}
	secondPort.err = &transport.CommandError{Result: secondPort.result}
	second, err := (Runner{Port: secondPort}).Preview(context.Background(), request)
	if err != nil {
		t.Fatalf("Preview(second): %v", err)
	}
	if first.ConfigDigest == second.ConfigDigest {
		t.Fatalf("config digest did not bind child plan digest: %q", first.ConfigDigest)
	}
}

func TestPreviewMapsTimeoutToDeterministicError(t *testing.T) {
	request := previewRequest(t, nil)
	port := &recordingPort{err: context.DeadlineExceeded}
	_, err := (Runner{Port: port}).Preview(context.Background(), request)
	var childErr *Error
	if !errors.As(err, &childErr) || childErr.Code != "timeout" {
		t.Fatalf("Preview() error = %#v, want timeout", err)
	}
}

func TestPreviewRejectsNonRegularOrUnknownAgentExecutable(t *testing.T) {
	request := previewRequest(t, nil)
	request.AgentExecutables = map[string]string{"unknown": request.NodeExecutable}
	_, err := (Runner{Port: &recordingPort{}}).Preview(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "selected-agent") {
		t.Fatalf("Preview() error = %v, want selected-agent", err)
	}

	request = previewRequest(t, nil)
	request.AgentExecutables = map[string]string{"codex": t.TempDir()}
	_, err = (Runner{Port: &recordingPort{}}).Preview(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("Preview() error = %v, want executable", err)
	}
}

func previewRequest(t *testing.T, agents []string) Request {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatalf("Mkdir(bin): %v", err)
	}
	writeExecutable := func(name string) string {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte(name+" fixture\n"), 0o700); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
		return path
	}
	agentExecutables := make(map[string]string, len(agents))
	for _, id := range agents {
		command := id
		if id == "claude-code" {
			command = "claude"
		}
		agentExecutables[id] = writeExecutable(command)
	}
	home := filepath.Join(root, "private-home")
	configRoot := filepath.Join(home, ".config")
	tempRoot := filepath.Join(root, "tmp")
	stateRoot := filepath.Join(home, ".local", "state", "omh")
	for _, directory := range []string{home, configRoot, tempRoot, stateRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", directory, err)
		}
	}
	return Request{
		NodeExecutable:   writeExecutable("node"),
		Entrypoint:       writeExecutable("main.js"),
		StateRoot:        stateRoot,
		Home:             home,
		ConfigRoot:       configRoot,
		TempRoot:         tempRoot,
		Platform:         "darwin",
		Locale:           "C.UTF-8",
		AgentExecutables: agentExecutables,
		Timeout:          2 * time.Second,
	}
}

func previewResult(
	t *testing.T,
	request Request,
	readiness string,
	exitCode int,
	blockers,
	optionalGaps []string,
) transport.Result {
	t.Helper()
	agents := sortedAgentIDs(request.AgentExecutables)
	agentStates := make([]map[string]any, 0, len(agents))
	capabilities := make([]map[string]any, 0, len(agents)*len(exactWorkflows))
	addons := make([]map[string]any, 0, 2)
	for _, id := range agents {
		command := id
		if id == "claude-code" {
			command = "claude"
		}
		agentStates = append(agentStates, map[string]any{
			"id": id, "command": command, "expectedVersion": "fixture",
			"executablePath": request.AgentExecutables[id], "state": "ready",
			"ownership": "external", "detail": "caller-provided executable matches",
		})
		for _, workflow := range exactWorkflows {
			capabilities = append(capabilities, map[string]any{
				"id": workflow, "runtimeId": id, "state": "pending", "sourceId": "fixture",
			})
		}
		if id == "opencode" || id == "codex" {
			addons = append(addons, map[string]any{
				"agentId": id, "detail": "fixture", "fingerprint": strings.Repeat("c", 64),
				"id": "omo", "state": "installable", "version": "4.19.2",
			})
		}
	}
	var plan any
	var digest any
	if readiness == "preview" {
		digest = strings.Repeat("a", 64)
		plan = map[string]any{
			"$schema": "../contracts/apply-plan.schema.json", "schemaVersion": "2.0.0",
			"kind": "apply-plan", "catalogRevision": strings.Repeat("b", 64),
			"desiredState":  map[string]any{"profileId": "mds-host", "selectedAgents": agents, "runtimeAddons": []any{}},
			"platform":      map[string]any{"arch": "arm64", "os": "darwin"},
			"observedState": map[string]any{"fixture": true}, "preflights": []any{}, "actions": []any{},
			"digest": strings.Repeat("a", 64),
		}
	}
	payload := map[string]any{
		"command": "setup", "state": readiness, "exitCode": exitCode,
		"preview": map[string]any{
			"schemaVersion": "2.0.0", "kind": "environment-preview",
			"stateRoot": request.StateRoot, "receiptPath": filepath.Join(request.StateRoot, "receipts", "environment.json"),
			"profileId": "mds-host", "catalogRevision": strings.Repeat("b", 64),
			"selectedAgents": agents, "agents": agentStates, "packages": []any{},
			"capabilities": capabilities, "addons": addons, "preflights": []any{},
			"optionalGaps": optionalGaps, "blockers": blockers, "plan": plan,
			"digest": digest, "readiness": readiness, "remediation": "private fixture",
			"instanceId": nil,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal(preview): %v", err)
	}
	return transport.Result{
		Executable: request.NodeExecutable, Arguments: nil,
		Stdout: string(encoded) + "\n", ExitCode: exitCode,
	}
}

func pathListContains(value, wanted, platform string) bool {
	separator := ":"
	if platform == "windows" {
		separator = ";"
	}
	for _, entry := range strings.Split(value, separator) {
		if entry == wanted {
			return true
		}
	}
	return false
}
