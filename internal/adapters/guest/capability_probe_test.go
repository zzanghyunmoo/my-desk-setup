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
	outcome := probe.probeDotNetDAP(
		context.Background(), "app", "/fixture/Probe.cs", "/fixture/Probe.dll",
		"/fixture/App.csproj", "inspected", "fixture.dotnet.app", 6,
	)
	if outcome.status != capability.StatusPass || outcome.dap == nil ||
		outcome.dap.StoppedSourceID != "fixture.dotnet.app" || outcome.dap.StoppedLine != 6 {
		t.Fatalf("probeDotNetDAP() = %+v, want exact structural pass", outcome)
	}
	if port.command.Environment["MDS_DAP_SOURCE"] != "/fixture/Probe.cs" ||
		port.command.Environment["MDS_DAP_LINE"] != "6" ||
		port.command.Environment["MDS_DAP_VARIABLE"] != "inspected" {
		t.Fatalf("DAP probe environment = %+v, want exact source contract", port.command.Environment)
	}

	port.result.Stdout = `MDS_DAP_RESULT={"breakpoint_verified":true,"stopped_at_source":false,"stack_observed":true,"scopes_observed":true,"known_variable_present":true,"continued":true,"stepped_in":true,"stepped_over":true,"terminated":true}` + "\n"
	outcome = probe.probeDotNetDAP(
		context.Background(), "app", "/fixture/Probe.cs", "/fixture/Probe.dll",
		"/fixture/App.csproj", "inspected", "fixture.dotnet.app", 6,
	)
	if outcome.status == capability.StatusPass {
		t.Fatalf("DAP result with mismatched source passed: %+v", outcome)
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
