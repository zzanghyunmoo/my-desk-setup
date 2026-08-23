package guest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/capability"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestCapabilityProbeWithoutExecutionStaysBlocked(t *testing.T) {
	receipt := (CapabilityProbe{}).ProbeCapabilities(context.Background(), planning.Plan{
		Actions: []planning.Action{{ComponentID: "nvim-jvm"}},
	})
	if receipt.Ready || len(receipt.Checks) == 0 {
		t.Fatalf("receipt = %+v, want non-empty blocked receipt", receipt)
	}
	for _, check := range receipt.Checks {
		if check.Status == capability.StatusPass {
			t.Fatalf("unexecuted capability %s passed", check.ID)
		}
	}
}

func TestLSPInitializeRequiresParsedProtocolResponse(t *testing.T) {
	launcher := filepath.Join(t.TempDir(), "language-server")
	if err := os.WriteFile(launcher, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	response := []byte(`{"jsonrpc":"2.0","id":1,"result":{"capabilities":{}}}`)
	framed := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(response), response)
	port := &capabilityProbePort{result: transport.Result{Stdout: framed}}
	probe := CapabilityProbe{Home: t.TempDir(), Port: port}
	outcome := probe.probeLSP(context.Background(), launcher, []string{"--stdio"}, t.TempDir())
	if outcome.status != capability.StatusPass {
		t.Fatalf("probeLSP() = %+v, want pass", outcome)
	}
	if port.command.Executable != launcher ||
		!strings.Contains(string(port.command.Stdin), `"method":"initialize"`) {
		t.Fatalf("probe command = %+v, want actual initialize request", port.command)
	}

	port.result.Stdout = "not an LSP frame"
	port.err = errors.New("fixture process failed")
	outcome = probe.probeLSP(context.Background(), launcher, nil, t.TempDir())
	if outcome.status == capability.StatusPass {
		t.Fatalf("malformed response passed: %+v", outcome)
	}
}

func TestPrepareCapabilityFixturesCopiesMutableIsolatedSources(t *testing.T) {
	root, err := prepareCapabilityFixtures([]string{"nvim-dotnet"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "dotnet-webapi", "Program.cs")
	if err := os.WriteFile(path, []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("fixture copy is not mutable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "jvm-java-spring")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unselected JVM fixture exists: %v", err)
	}
}

func TestDotNetDAPProbeRequiresAdapterReportedStructuralResult(t *testing.T) {
	port := &capabilityProbePort{result: transport.Result{Stdout: `MDS_DAP_RESULT={"breakpoint_verified":true,"stopped_at_source":true,"stack_observed":true,"scopes_observed":true,"known_variable_present":true,"continued":true,"stepped_in":true,"stepped_over":true,"terminated":true}` + "\n"}}
	probe := CapabilityProbe{Home: t.TempDir(), Port: port}
	fixtureRoot := t.TempDir()
	source := filepath.Join(fixtureRoot, "Probe.cs")
	project := filepath.Join(fixtureRoot, "App.csproj")
	outcome := probe.probeDotNetDAP(
		context.Background(), "app", source, filepath.Join(fixtureRoot, "Probe.dll"),
		project, "inspected", "fixture.dotnet.app", 6,
	)
	if outcome.status != capability.StatusPass || outcome.dap == nil ||
		outcome.dap.StoppedSourceID != "fixture.dotnet.app" || outcome.dap.StoppedLine != 6 {
		t.Fatalf("probeDotNetDAP() = %+v, want exact structural pass", outcome)
	}
	if port.command.Environment["MDS_DAP_SOURCE"] != source ||
		port.command.Environment["MDS_DAP_LINE"] != "6" ||
		port.command.Environment["MDS_DAP_VARIABLE"] != "inspected" {
		t.Fatalf("DAP probe environment = %+v, want exact source contract", port.command.Environment)
	}

	outcome = probe.probeDotNetDAP(
		context.Background(), "server", filepath.Join(fixtureRoot, "Program.cs"), filepath.Join(fixtureRoot, "WebApi.dll"),
		filepath.Join(fixtureRoot, "WebApi.csproj"), "builder", "fixture.dotnet.server", 2,
	)
	if outcome.status != capability.StatusPass || port.command.Environment["MDS_DAP_MODE"] != "server" {
		t.Fatalf("server DAP probe = %+v, environment = %+v", outcome, port.command.Environment)
	}
	for _, token := range []string{
		`configuration.args = { "--urls", "http://127.0.0.1:0" }`,
		`MDS_CAPABILITY_PROBE = "1"`,
		`ASPNETCORE_URLS = "http://127.0.0.1:0"`,
		`cwd = vim.fs.dirname(assert(vim.env.MDS_DAP_PROJECT))`,
	} {
		if !strings.Contains(dotNetDAPProbeLua, token) {
			t.Fatalf("server DAP probe script omits %q", token)
		}
	}

	port.result.Stdout = `MDS_DAP_RESULT={"breakpoint_verified":true,"stopped_at_source":false,"stack_observed":true,"scopes_observed":true,"known_variable_present":true,"continued":true,"stepped_in":true,"stepped_over":true,"terminated":true}` + "\n"
	outcome = probe.probeDotNetDAP(
		context.Background(), "app", source, filepath.Join(fixtureRoot, "Probe.dll"),
		project, "inspected", "fixture.dotnet.app", 6,
	)
	if outcome.status == capability.StatusPass {
		t.Fatalf("DAP result with mismatched source passed: %+v", outcome)
	}
}

func TestJVMDAPTestProbeRerunsOnlyTheSelectedTestTask(t *testing.T) {
	script, err := ideFixtures.ReadFile("fixtures/probes/jvm-dap.lua")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)
	for _, token := range []string{
		`"--debug-jvm", "--rerun"`,
		`vim.system({ "setsid", root .. "/gradlew"`,
		`pcall(vim.uv.kill, -test_task.pid, 15)`,
		`pcall(vim.uv.kill, -test_task.pid, 9)`,
		`return listening or test_result ~= nil`,
		`vim.wait(60000, function() return test_result ~= nil end, 50)`,
		`Gradle test JVM exited before opening the debug socket`,
		`Gradle test task failed after debugging with exit code`,
		`result.terminated and not result.error`,
	} {
		if !strings.Contains(content, token) {
			t.Fatalf("JVM DAP test probe omits %q", token)
		}
	}
	if strings.Contains(content, `"--rerun-tasks"`) {
		t.Fatal("JVM DAP test probe reruns compilation tasks before opening the debug socket")
	}
}

func TestDotNetServerDAPFixtureIncludesProjectRelativeResource(t *testing.T) {
	content, err := ideFixtures.ReadFile("fixtures/dotnet-webapi/probe-resource.txt")
	if err != nil {
		t.Fatalf("read ASP.NET DAP fixture resource: %v", err)
	}
	if strings.TrimSpace(string(content)) != "hello-api" {
		t.Fatalf("ASP.NET DAP fixture resource = %q", content)
	}
}

type capabilityProbePort struct {
	command transport.Command
	result  transport.Result
	err     error
}

func (port *capabilityProbePort) Run(
	_ context.Context,
	command transport.Command,
) (transport.Result, error) {
	port.command = command
	return port.result, port.err
}
