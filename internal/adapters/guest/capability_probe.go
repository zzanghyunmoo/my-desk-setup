package guest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/capability"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const capabilityProbeTimeout = 4 * time.Minute

var runtimeGenerationPattern = regexp.MustCompile(`^g-[a-z0-9][a-z0-9-]{0,126}$`)

// CapabilityProbe is the production, guest-local authority for the bounded
// JVM and .NET capability receipt emitted by doctor. Every check starts
// blocked and is promoted only after this process executes the exact plan
// input on an isolated copy of an embedded fixture.
type CapabilityProbe struct {
	Home   string
	Port   transport.Port
	Target target.Facts
}

type probeOutcome struct {
	status      capability.Status
	reason      string
	attribution string
	dap         *capability.DAPOutcome
}

func pass(reason string) probeOutcome {
	return probeOutcome{status: capability.StatusPass, reason: reason}
}

func blocked(reason, detail string) probeOutcome {
	return probeOutcome{
		status: capability.StatusBlocked, reason: reason,
		attribution: transport.SanitizeDiagnostic(detail),
	}
}

func failed(reason string, err error) probeOutcome {
	status := capability.StatusFailed
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timed out") {
		status = capability.StatusTimeout
	}
	return probeOutcome{
		status: status, reason: reason,
		attribution: transport.SanitizeDiagnostic(err.Error()),
	}
}

func (probe CapabilityProbe) ProbeCapabilities(
	ctx context.Context,
	plan planning.Plan,
) capability.Receipt {
	components := planComponentIDs(plan)
	expected := capability.Expected(components)
	outcomes := make(map[string]probeOutcome, len(expected))
	for _, specification := range expected {
		outcomes[specification.ID] = blocked("probe-not-run", "capability probe did not run")
	}

	if probe.Home == "" || probe.Port == nil {
		return aggregateProbe(expected, components, outcomes)
	}
	if action, ok := planAction(plan, "nvim-jvm"); ok {
		outcomes["artifact.jvm"] = probe.probeArtifacts(action, jvmRuntimeComponents())
		outcomes["config.jvm"] = probe.probeConfiguration(ctx, action)
	}
	if action, ok := planAction(plan, "nvim-dotnet"); ok {
		outcomes["artifact.dotnet"] = probe.probeArtifacts(action, dotnetRuntimeComponents())
		outcomes["config.dotnet"] = probe.probeConfiguration(ctx, action)
	}

	workspace, workspaceErr := prepareCapabilityFixtures(components)
	if workspaceErr == nil {
		defer os.RemoveAll(workspace)
		if _, ok := planAction(plan, "nvim-jvm"); ok {
			probe.probeJVM(ctx, plan, workspace, outcomes)
		}
		if action, ok := planAction(plan, "nvim-dotnet"); ok {
			probe.probeDotNet(ctx, action, workspace, outcomes)
		}
	} else {
		for _, specification := range expected {
			if specification.Kind != capability.KindArtifact &&
				specification.Kind != capability.KindConfiguration &&
				specification.Kind != capability.KindActualTarget {
				outcomes[specification.ID] = failed("fixture-prepare-failed", workspaceErr)
			}
		}
	}

	for _, componentID := range []string{"nvim-jvm", "nvim-dotnet"} {
		actualID := map[string]string{
			"nvim-jvm": "actual.lima.jvm", "nvim-dotnet": "actual.lima.dotnet",
		}[componentID]
		if _, selected := outcomes[actualID]; !selected {
			continue
		}
		if probe.Target.ID.Kind != target.KindLimaGuest ||
			probe.Target.ID.String() != plan.Target.ID.String() {
			outcomes[actualID] = blocked("wrong-actual-target", "probe did not execute in the planned Lima guest")
			continue
		}
		if componentChecksPassed(expected, outcomes, componentID, actualID) {
			outcomes[actualID] = pass("actual-lima-probed")
		} else {
			outcomes[actualID] = blocked("component-probe-incomplete", "one or more required target probes did not pass")
		}
	}
	return aggregateProbe(expected, components, outcomes)
}

func aggregateProbe(
	expected []capability.ExpectedCheck,
	components []string,
	outcomes map[string]probeOutcome,
) capability.Receipt {
	checks := make([]capability.CapabilityCheck, 0, len(expected))
	for _, specification := range expected {
		outcome := outcomes[specification.ID]
		if specification.Kind == capability.KindDAP {
			check := capability.NewDAPCheck(
				specification.ID, specification.ComponentID,
				outcome.status, outcome.reason, capability.DAPOutcome{},
			)
			if outcome.dap != nil {
				check.DAP = outcome.dap
			} else if outcome.status != capability.StatusPass {
				check.DAP = nil
			}
			check.Attribution = outcome.attribution
			checks = append(checks, check)
			continue
		}
		checks = append(checks, capability.NewCheck(
			specification.ID, specification.Kind, specification.ComponentID,
			outcome.status, outcome.reason, outcome.attribution,
		))
	}
	return capability.Aggregate(capability.ExpectedIDs(components), checks)
}

func planComponentIDs(plan planning.Plan) []string {
	result := make([]string, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		result = append(result, action.ComponentID)
	}
	return result
}

func planAction(plan planning.Plan, componentID string) (planning.Action, bool) {
	for _, action := range plan.Actions {
		if action.ComponentID == componentID {
			return action, true
		}
	}
	return planning.Action{}, false
}

func componentChecksPassed(
	expected []capability.ExpectedCheck,
	outcomes map[string]probeOutcome,
	componentID, actualID string,
) bool {
	for _, specification := range expected {
		if specification.ComponentID == componentID && specification.ID != actualID &&
			outcomes[specification.ID].status != capability.StatusPass {
			return false
		}
	}
	return true
}

func jvmRuntimeComponents() []string {
	return []string{
		"java-debug-server", "java-test-server", "jdt-language-server",
		"kotlin-debug-adapter", "kotlin-language-server", "spring-tools-language-server",
	}
}

func dotnetRuntimeComponents() []string {
	return []string{"netcoredbg", "roslyn-language-server"}
}

func (probe CapabilityProbe) probeArtifacts(
	action planning.Action,
	componentIDs []string,
) probeOutcome {
	references, err := runtimeTreeReferences(action)
	if err != nil {
		return failed("runtime-identity-invalid", err)
	}
	for _, componentID := range componentIDs {
		reference, ok := references[componentID]
		if !ok {
			return blocked("runtime-identity-missing", componentID)
		}
		for _, relative := range reference.RequiredPaths {
			if _, err := probe.runtimePayloadPath(reference, relative); err != nil {
				return failed("runtime-payload-invalid", err)
			}
		}
	}
	return pass("exact-runtime-trees-ready")
}

func (probe CapabilityProbe) runtimePayloadPath(
	reference runtimeTreeReference,
	relative string,
) (string, error) {
	identity := reference.ManifestSHA256 + "-" + reference.ArchiveSHA256
	root := filepath.Join(
		probe.Home, ".local", "share", "mds", "runtime-trees",
		reference.ComponentID, identity,
	)
	content, err := os.ReadFile(filepath.Join(root, ".mds-runtime-tree-current"))
	if err != nil {
		return "", fmt.Errorf("read runtime generation for %s: %w", reference.ComponentID, err)
	}
	generation := strings.TrimSpace(string(content))
	if !runtimeGenerationPattern.MatchString(generation) {
		return "", fmt.Errorf("runtime generation for %s is invalid", reference.ComponentID)
	}
	clean := filepath.ToSlash(filepath.Clean(relative))
	if clean != relative || clean == "." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("runtime path for %s is invalid", reference.ComponentID)
	}
	path := filepath.Join(root, "generations", generation, "payload", filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		if err == nil {
			err = errors.New("payload is not a regular file")
		}
		return "", fmt.Errorf("inspect runtime payload for %s: %w", reference.ComponentID, err)
	}
	return path, nil
}

func (probe CapabilityProbe) probeConfiguration(
	ctx context.Context,
	action planning.Action,
) probeOutcome {
	ready, detail, err := inspectActionConfiguration(probe.Home, action)
	if err != nil {
		return failed("configuration-inspection-failed", err)
	}
	if !ready {
		return blocked("configuration-not-ready", detail)
	}
	set, err := pluginSetForAction(action)
	if err != nil {
		return failed("plugin-set-invalid", err)
	}
	ready, detail, err = inspectPluginRuntime(ctx, probe.Home, probe.Port, set)
	if err != nil {
		return failed("plugin-inspection-failed", err)
	}
	if !ready {
		return blocked("plugin-runtime-not-ready", detail)
	}
	return pass("managed-configuration-ready")
}

func prepareCapabilityFixtures(components []string) (string, error) {
	if err := validateIDEFixtures(); err != nil {
		return "", err
	}
	root, err := os.MkdirTemp("", "mds-capability-*")
	if err != nil {
		return "", err
	}
	wantJVM, wantDotNet := false, false
	for _, componentID := range components {
		wantJVM = wantJVM || componentID == "nvim-jvm"
		wantDotNet = wantDotNet || componentID == "nvim-dotnet"
	}
	for _, definition := range ideFixtureDefinitions {
		if (strings.HasPrefix(definition.ID, "jvm-") && !wantJVM) ||
			(strings.HasPrefix(definition.ID, "dotnet-") && !wantDotNet) {
			continue
		}
		source, err := fs.Sub(ideFixtures, definition.Root)
		if err != nil {
			_ = os.RemoveAll(root)
			return "", err
		}
		destination := filepath.Join(root, definition.ID)
		if err := os.CopyFS(destination, source); err != nil {
			_ = os.RemoveAll(root)
			return "", err
		}
		err = filepath.WalkDir(destination, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			mode := os.FileMode(0o600)
			if entry.Name() == "gradlew" {
				mode = 0o700
			}
			return os.Chmod(path, mode)
		})
		if err != nil {
			_ = os.RemoveAll(root)
			return "", err
		}
	}
	return root, nil
}

func (probe CapabilityProbe) probeJVM(
	ctx context.Context,
	plan planning.Plan,
	workspace string,
	outcomes map[string]probeOutcome,
) {
	javaRoot := filepath.Join(workspace, "jvm-java-spring")
	kotlinRoot := filepath.Join(workspace, "jvm-kotlin-spring")
	outcomes["action.jvm.build"] = probe.runAll(ctx, []transport.Command{
		probe.gradleCommand(javaRoot, "assemble"),
		probe.gradleCommand(kotlinRoot, "assemble"),
	}, "fixture-build-passed")
	outcomes["action.jvm.test"] = probe.runAll(ctx, []transport.Command{
		probe.gradleCommand(javaRoot, "test"),
		probe.gradleCommand(kotlinRoot, "test"),
	}, "fixture-tests-passed")
	javaRun := probe.gradleCommand(javaRoot, "bootRun", "--args=--server.port=0")
	javaRun.Environment["MDS_CAPABILITY_PROBE"] = "1"
	kotlinRun := probe.gradleCommand(kotlinRoot, "bootRun", "--args=--server.port=0")
	kotlinRun.Environment["MDS_CAPABILITY_PROBE"] = "1"
	outcomes["action.jvm.run"] = probe.runAll(
		ctx, []transport.Command{javaRun, kotlinRun}, "spring-boot-endpoints-passed",
	)
	if action, ok := planAction(plan, "nvim-jvm"); ok {
		references, err := runtimeTreeReferences(action)
		if err == nil {
			jdtReference := references["jdt-language-server"]
			if executable, pathErr := probe.runtimePayloadPath(jdtReference, jdtReference.Executable); pathErr == nil {
				outcomes["lsp.java"] = probe.probeLSP(
					ctx, executable,
					[]string{"-data", filepath.Join(workspace, "jdtls-workspace")}, javaRoot,
				)
			} else {
				outcomes["lsp.java"] = failed("lsp-launcher-invalid", pathErr)
			}
			outcomes["lsp.kotlin"] = probe.probeNvimClients(
				ctx, kotlinRoot,
				filepath.Join(kotlinRoot, "src", "main", "kotlin", "dev", "mds", "DebugProbe.kt"),
				"kotlin-lsp-attached", "kotlin_lsp",
			)
			outcomes["lsp.spring"] = probe.probeNvimClients(
				ctx, javaRoot,
				filepath.Join(javaRoot, "src", "main", "resources", "application.properties"),
				"spring-lsp-attached", "spring_boot",
			)
		} else {
			for _, id := range []string{"lsp.java", "lsp.kotlin", "lsp.spring"} {
				outcomes[id] = failed("runtime-identity-invalid", err)
			}
		}
		outcomes["dap.java"] = probe.probeJVMDAP(
			ctx, "java", "",
			javaRoot,
			filepath.Join(javaRoot, "src", "main", "java", "dev", "mds", "DebugProbe.java"),
			"dev.mds.DebugProbe", "", "controller",
			"fixture.java", 10,
		)
		kotlinDebug := references["kotlin-debug-adapter"]
		if adapter, pathErr := probe.runtimePayloadPath(kotlinDebug, kotlinDebug.Executable); pathErr == nil {
			outcomes["dap.kotlin"] = probe.probeJVMDAP(
				ctx, "kotlin", adapter,
				kotlinRoot,
				filepath.Join(kotlinRoot, "src", "main", "kotlin", "dev", "mds", "DebugProbe.kt"),
				"dev.mds.DebugProbeKt", "", "controller",
				"fixture.kotlin", 5,
			)
		} else {
			outcomes["dap.kotlin"] = failed("dap-launcher-invalid", pathErr)
		}
	}
}

func (probe CapabilityProbe) probeDotNet(
	ctx context.Context,
	action planning.Action,
	workspace string,
	outcomes map[string]probeOutcome,
) {
	dotnet := filepath.Join(probe.Home, ".local", "share", "mise", "shims", "dotnet")
	projects := []string{
		filepath.Join(workspace, "dotnet-console-test", "Mds.Console.csproj"),
		filepath.Join(workspace, "dotnet-webapi", "Mds.WebApi.csproj"),
		filepath.Join(workspace, "dotnet-mvc-razor", "Mds.Mvc.csproj"),
		filepath.Join(workspace, "dotnet-blazor", "Mds.Blazor.csproj"),
	}
	commands := make([]transport.Command, 0, len(projects))
	for _, project := range projects {
		commands = append(commands, probe.toolCommand(dotnet, filepath.Dir(project), "build", project))
	}
	outcomes["action.dotnet.build"] = probe.runAll(ctx, commands, "fixture-build-passed")
	testProject := filepath.Join(workspace, "dotnet-console-test", "tests", "Mds.Console.Tests.csproj")
	outcomes["action.dotnet.test"] = probe.runAll(ctx, []transport.Command{
		probe.toolCommand(dotnet, filepath.Dir(testProject), "test", testProject),
	}, "fixture-tests-passed")
	webProject := filepath.Join(workspace, "dotnet-webapi", "Mds.WebApi.csproj")
	webRoot := filepath.Dir(webProject)
	runCommand := probe.toolCommand(
		dotnet, webRoot, "run", "--project", webProject,
		"--launch-profile", "Mds.WebApi", "--urls", "http://127.0.0.1:0",
	)
	runCommand.Environment["MDS_CAPABILITY_PROBE"] = "1"
	outcomes["action.dotnet.run"] = probe.runAll(
		ctx, []transport.Command{runCommand}, "aspnet-endpoint-passed",
	)
	watchCommand := probe.toolCommand(
		dotnet, webRoot, "watch", "run", "--project", webProject,
		"--launch-profile", "Mds.WebApi", "--urls", "http://127.0.0.1:0",
	)
	watchCommand.Environment["MDS_CAPABILITY_PROBE"] = "1"
	watchCommand.Environment["DOTNET_WATCH_SUPPRESS_LAUNCH_BROWSER"] = "1"
	watchCommand.Environment["DOTNET_WATCH_RESTART_ON_RUDE_EDIT"] = "0"
	watchCommand.Timeout = time.Minute
	outcomes["action.dotnet.watch"] = probe.runLongRunningMarker(
		ctx, watchCommand, "MDS_ASPNET_ENDPOINT_READY", "dotnet-watch-endpoint-passed",
	)
	consoleRoot := filepath.Join(workspace, "dotnet-console-test")
	outcomes["dap.dotnet.app"] = probe.probeDotNetDAP(
		ctx, "app",
		filepath.Join(consoleRoot, "Probe.cs"),
		filepath.Join(consoleRoot, "bin", "Debug", "net10.0", "Mds.Console.dll"),
		filepath.Join(consoleRoot, "Mds.Console.csproj"),
		"inspected", "fixture.dotnet.app", 6,
	)
	outcomes["dap.dotnet.test"] = probe.probeDotNetDAP(
		ctx, "test",
		filepath.Join(consoleRoot, "tests", "ProbeTests.cs"),
		"",
		filepath.Join(consoleRoot, "tests", "Mds.Console.Tests.csproj"),
		"name", "fixture.dotnet.test", 6,
	)

	if _, err := runtimeTreeReferences(action); err != nil {
		outcomes["lsp.csharp"] = failed("runtime-identity-invalid", err)
		return
	}
	outcomes["lsp.csharp"] = probe.probeNvimClients(
		ctx, consoleRoot, filepath.Join(consoleRoot, "Probe.cs"),
		"roslyn-lsp-attached", "roslyn",
	)
	if outcomes["lsp.csharp"].status == capability.StatusPass {
		mvcRoot := filepath.Join(workspace, "dotnet-mvc-razor")
		outcomes["mixed.razor"] = probe.probeMixedDocument(
			ctx, mvcRoot, filepath.Join(mvcRoot, "Pages", "Index.cshtml"),
		)
		blazorRoot := filepath.Join(workspace, "dotnet-blazor")
		outcomes["mixed.blazor"] = probe.probeMixedDocument(
			ctx, blazorRoot, filepath.Join(blazorRoot, "Components", "Pages", "Counter.razor"),
		)
	}
}

func (probe CapabilityProbe) probeJVMDAP(
	ctx context.Context,
	adapterType, adapter, root, source, mainClass, projectName, knownVariable,
	sourceID string,
	line int,
) probeOutcome {
	script, err := ideFixtures.ReadFile("fixtures/probes/jvm-dap.lua")
	if err != nil {
		return failed("dap-script-failed", err)
	}
	isolation, err := os.MkdirTemp("", "mds-jvm-dap-probe-*")
	if err != nil {
		return failed("dap-isolation-failed", err)
	}
	defer os.RemoveAll(isolation)
	scriptPath := filepath.Join(isolation, "probe.lua")
	if err := os.WriteFile(scriptPath, script, 0o600); err != nil {
		return failed("dap-script-failed", err)
	}
	nvim := filepath.Join(probe.Home, ".local", "bin", "nvim")
	command := probe.toolCommand(
		nvim, root, "--headless", "-u",
		filepath.Join(probe.Home, ".config", "nvim", "init.lua"),
		"-c", "luafile "+scriptPath,
	)
	command.Environment["XDG_STATE_HOME"] = filepath.Join(isolation, "state")
	command.Environment["XDG_CACHE_HOME"] = filepath.Join(isolation, "cache")
	command.Environment["MDS_TRUST_ROOT"] = root
	command.Environment["MDS_DAP_TYPE"] = adapterType
	command.Environment["MDS_DAP_ADAPTER"] = adapter
	command.Environment["MDS_DAP_ROOT"] = root
	command.Environment["MDS_DAP_SOURCE"] = source
	command.Environment["MDS_DAP_LINE"] = strconv.Itoa(line)
	command.Environment["MDS_DAP_VARIABLE"] = knownVariable
	command.Environment["MDS_DAP_MAIN"] = mainClass
	command.Environment["MDS_DAP_PROJECT"] = projectName
	command.Timeout = capabilityProbeTimeout
	result, runErr := probe.Port.Run(ctx, command)
	output := result.Stdout + result.Stderr
	marker := "MDS_DAP_RESULT="
	index := strings.LastIndex(output, marker)
	if index < 0 {
		if runErr != nil {
			return failed("dap-probe-failed", runErr)
		}
		return failed("dap-probe-missing", errors.New("structural DAP result is missing"))
	}
	encoded, _, _ := strings.Cut(output[index+len(marker):], "\n")
	var dap capability.DAPOutcome
	if err := json.Unmarshal([]byte(encoded), &dap); err != nil {
		return failed("dap-result-invalid", err)
	}
	dap.StoppedSourceID = sourceID
	dap.StoppedLine = line
	if runErr != nil || !dapComplete(dap) {
		if runErr == nil {
			runErr = errors.New("structural DAP result is incomplete")
		}
		return failed("dap-probe-failed", runErr)
	}
	return probeOutcome{
		status: capability.StatusPass, reason: "structural-dap-probe-passed", dap: &dap,
	}
}

func dapComplete(outcome capability.DAPOutcome) bool {
	return outcome.BreakpointVerified && outcome.StoppedAtSource &&
		outcome.StoppedSourceID != "" && outcome.StoppedLine > 0 &&
		outcome.StackObserved && outcome.ScopesObserved &&
		outcome.KnownVariablePresent && outcome.Continued &&
		outcome.SteppedIn && outcome.SteppedOver && outcome.Terminated
}

func (probe CapabilityProbe) probeDotNetDAP(
	ctx context.Context,
	mode, source, program, project, knownVariable, sourceID string,
	line int,
) probeOutcome {
	isolation, err := os.MkdirTemp("", "mds-dap-probe-*")
	if err != nil {
		return failed("dap-isolation-failed", err)
	}
	defer os.RemoveAll(isolation)
	scriptPath := filepath.Join(isolation, "probe.lua")
	if err := os.WriteFile(scriptPath, []byte(dotNetDAPProbeLua), 0o600); err != nil {
		return failed("dap-script-failed", err)
	}
	nvim := filepath.Join(probe.Home, ".local", "bin", "nvim")
	command := probe.toolCommand(
		nvim, filepath.Dir(project), "--headless", "-u",
		filepath.Join(probe.Home, ".config", "nvim", "init.lua"),
		"-c", "luafile "+scriptPath,
	)
	command.Environment["XDG_STATE_HOME"] = filepath.Join(isolation, "state")
	command.Environment["XDG_CACHE_HOME"] = filepath.Join(isolation, "cache")
	command.Environment["MDS_DAP_MODE"] = mode
	command.Environment["MDS_DAP_SOURCE"] = source
	command.Environment["MDS_DAP_PROGRAM"] = program
	command.Environment["MDS_DAP_PROJECT"] = project
	command.Environment["MDS_DAP_LINE"] = strconv.Itoa(line)
	command.Environment["MDS_DAP_VARIABLE"] = knownVariable
	command.Timeout = 2 * time.Minute
	result, runErr := probe.Port.Run(ctx, command)
	output := result.Stdout + result.Stderr
	marker := "MDS_DAP_RESULT="
	index := strings.LastIndex(output, marker)
	if index < 0 {
		if runErr != nil {
			return failed("dap-probe-failed", runErr)
		}
		return failed("dap-probe-missing", errors.New("structural DAP result is missing"))
	}
	encoded, _, _ := strings.Cut(output[index+len(marker):], "\n")
	var dap capability.DAPOutcome
	if err := json.Unmarshal([]byte(encoded), &dap); err != nil {
		return failed("dap-result-invalid", err)
	}
	dap.StoppedSourceID = sourceID
	dap.StoppedLine = line
	if runErr != nil || !dapComplete(dap) {
		if runErr == nil {
			runErr = errors.New("structural DAP result is incomplete")
		}
		return failed("dap-probe-failed", runErr)
	}
	return probeOutcome{
		status: capability.StatusPass, reason: "structural-dap-probe-passed", dap: &dap,
	}
}

const dotNetDAPProbeLua = `local source = assert(vim.uv.fs_realpath(assert(vim.env.MDS_DAP_SOURCE)))
local mode = assert(vim.env.MDS_DAP_MODE)
local line = assert(tonumber(vim.env.MDS_DAP_LINE))
local known_variable = assert(vim.env.MDS_DAP_VARIABLE)
vim.cmd("edit " .. vim.fn.fnameescape(source))
local dap = require "dap"
local outcome = {
  breakpoint_verified = false, stopped_at_source = false,
  stack_observed = false, scopes_observed = false,
  known_variable_present = false, continued = false,
  stepped_over = false, stepped_in = false,
  terminated = false,
}
local stops, inspecting, finished = 0, false, false
local function finish()
  if finished then return end
  finished = true
  local ready = true
  for key, value in pairs(outcome) do
    if key ~= "error" and value ~= true then ready = false end
  end
  io.stdout:write("MDS_DAP_RESULT=" .. vim.json.encode(outcome) .. "\n")
  if not ready then vim.cmd "cquit 1" end
  vim.cmd "qa!"
end
local function abort(message)
  outcome.error = message
  pcall(dap.terminate)
  vim.defer_fn(finish, 500)
end
local function inspect_stop(session, body, done)
  local buffer_breakpoints = require("dap.breakpoints").get(0)[vim.api.nvim_get_current_buf()] or {}
  for _, breakpoint in ipairs(buffer_breakpoints) do
    if breakpoint.line == line and breakpoint.state and breakpoint.state.verified == true then
      outcome.breakpoint_verified = true
    end
  end
  session:request("stackTrace", { threadId = body.threadId, startFrame = 0, levels = 20 }, function(stack_error, stack)
    if stack_error or not stack or not stack.stackFrames or #stack.stackFrames == 0 then
      abort "stackTrace failed"
      return
    end
    outcome.stack_observed = true
    local frame = stack.stackFrames[1]
    if frame.source and frame.source.path then
      local stopped_path = vim.uv.fs_realpath(frame.source.path) or frame.source.path
      outcome.stopped_at_source = stopped_path == source and frame.line == line
    end
    session:request("scopes", { frameId = frame.id }, function(scope_error, scope_result)
      if scope_error or not scope_result or not scope_result.scopes or #scope_result.scopes == 0 then
        abort "scopes failed"
        return
      end
      outcome.scopes_observed = true
      local pending = 0
      for _, scope in ipairs(scope_result.scopes) do
        if scope.variablesReference and scope.variablesReference > 0 then
          pending = pending + 1
          session:request("variables", { variablesReference = scope.variablesReference }, function(_, variables_result)
            if variables_result and variables_result.variables then
              for _, variable in ipairs(variables_result.variables) do
                if variable.name == known_variable then outcome.known_variable_present = true end
              end
            end
            pending = pending - 1
            if pending == 0 then done() end
          end)
        end
      end
      if pending == 0 then done() end
    end)
  end)
end
dap.listeners.after.event_breakpoint["mds-probe"] = function(_, body)
  if body and body.breakpoint and body.breakpoint.verified == true then
    outcome.breakpoint_verified = true
  end
end
dap.listeners.after.event_continued["mds-probe"] = function() outcome.continued = true end
dap.listeners.after.event_stopped["mds-probe"] = function(session, body)
  if inspecting then return end
  stops = stops + 1
  if stops == 1 then
    inspecting = true
    inspect_stop(session, body, function()
      inspecting = false
      vim.schedule(dap.step_over)
    end)
  elseif stops == 2 then
    outcome.stepped_over = true
    vim.schedule(dap.step_into)
  elseif stops == 3 then
    outcome.stepped_in = true
    outcome.continued = true
    vim.schedule(dap.continue)
  else
    outcome.continued = true
    vim.schedule(dap.continue)
  end
end
dap.listeners.after.event_terminated["mds-probe"] = function()
  outcome.terminated = true; vim.defer_fn(finish, 200)
end
dap.listeners.after.event_exited["mds-probe"] = function()
  outcome.terminated = true; vim.defer_fn(finish, 200)
end
vim.defer_fn(function()
  if not vim.wait(30000, function() return dap.adapters.coreclr ~= nil end, 100) then finish(); return end
  vim.api.nvim_win_set_cursor(0, { line, 0 })
  dap.set_breakpoint()
  if mode == "test" then
    vim.g.mds_dotnet_project = assert(vim.env.MDS_DAP_PROJECT)
    dap.run(assert(dap.configurations.cs and dap.configurations.cs[2]))
  else
    dap.run {
      type = "coreclr", request = "launch", name = "MDS .NET probe",
      program = assert(vim.env.MDS_DAP_PROGRAM), cwd = vim.fs.dirname(vim.env.MDS_DAP_PROGRAM),
    }
  end
end, 1000)
vim.defer_fn(function()
  if outcome.terminated then return end
  outcome.error = "debug probe timed out"
  pcall(dap.terminate)
  vim.defer_fn(finish, 1000)
end, 90000)
`

func (probe CapabilityProbe) probeMixedDocument(
	ctx context.Context,
	root, document string,
) probeOutcome {
	return probe.probeNvimClients(
		ctx, root, document, "roslyn-html-cohost-attached", "roslyn", "html",
	)
}

func (probe CapabilityProbe) probeNvimClients(
	ctx context.Context,
	root, document, reason string,
	clientNames ...string,
) probeOutcome {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return failed("nvim-client-root-invalid", err)
	}
	isolation, err := os.MkdirTemp("", "mds-nvim-probe-*")
	if err != nil {
		return failed("nvim-client-isolation-failed", err)
	}
	defer os.RemoveAll(isolation)
	stateRoot := filepath.Join(isolation, "state")
	trustDirectory := filepath.Join(stateRoot, "nvim", "mds")
	if err := os.MkdirAll(trustDirectory, 0o700); err != nil {
		return failed("nvim-client-trust-failed", err)
	}
	rootDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	trustBytes, err := json.Marshal(map[string]bool{rootDigest: true})
	if err != nil {
		return failed("nvim-client-trust-failed", err)
	}
	if err := os.WriteFile(
		filepath.Join(trustDirectory, "workspace-trust.json"),
		append(trustBytes, '\n'),
		0o600,
	); err != nil {
		return failed("nvim-client-trust-failed", err)
	}
	quotedNames := make([]string, 0, len(clientNames))
	for _, name := range clientNames {
		quotedNames = append(quotedNames, strconv.Quote(name))
	}
	nvim := filepath.Join(probe.Home, ".local", "bin", "nvim")
	script := fmt.Sprintf(`local wanted = {%s}
local ready = vim.wait(150000, function()
  local clients = {}
  for _, client in ipairs(vim.lsp.get_clients()) do
    if client.initialized then clients[client.name] = true end
  end
  for _, name in ipairs(wanted) do if clients[name] ~= true then return false end end
  return true
end, 100)
if ready then io.stdout:write("MDS_NVIM_CLIENTS_READY\\n") else error("required LSP clients did not attach") end`,
		strings.Join(quotedNames, ", "))
	command := probe.toolCommand(
		nvim, root, "--headless", "-u", filepath.Join(probe.Home, ".config", "nvim", "init.lua"),
		document, "-c", "lua "+script, "-c", "qa!",
	)
	command.Environment["XDG_STATE_HOME"] = stateRoot
	command.Environment["XDG_CACHE_HOME"] = filepath.Join(isolation, "cache")
	command.Timeout = 3 * time.Minute
	result, runErr := probe.Port.Run(ctx, command)
	if strings.Contains(result.Stdout+result.Stderr, "MDS_NVIM_CLIENTS_READY") {
		return pass(reason)
	}
	if runErr != nil {
		return failed("nvim-client-probe-failed", runErr)
	}
	diagnostic := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	if len(diagnostic) > 4096 {
		diagnostic = diagnostic[len(diagnostic)-4096:]
	}
	return failed(
		"nvim-client-probe-missing",
		fmt.Errorf("required LSP readiness marker is missing: %s", diagnostic),
	)
}

func (probe CapabilityProbe) gradleCommand(root, task string, arguments ...string) transport.Command {
	commandArguments := []string{"--no-daemon", task}
	commandArguments = append(commandArguments, arguments...)
	return probe.toolCommand(filepath.Join(root, "gradlew"), root, commandArguments...)
}

func (probe CapabilityProbe) toolCommand(
	executable, root string,
	arguments ...string,
) transport.Command {
	return transport.Command{
		Executable: executable, Arguments: arguments, WorkingDirectory: root,
		Environment: map[string]string{
			"HOME":   probe.Home,
			"PATH":   filepath.Join(probe.Home, ".local", "share", "mise", "shims") + ":/usr/local/bin:/usr/bin:/bin",
			"TMPDIR": os.TempDir(),
		},
		EnvironmentMode: transport.EnvironmentReplace,
		Timeout:         capabilityProbeTimeout,
		OutputLimit:     256 << 10,
	}
}

func (probe CapabilityProbe) runAll(
	ctx context.Context,
	commands []transport.Command,
	reason string,
) probeOutcome {
	for _, command := range commands {
		if _, err := probe.Port.Run(ctx, command); err != nil {
			return failed("project-action-failed", err)
		}
	}
	return pass(reason)
}

func (probe CapabilityProbe) runLongRunningMarker(
	ctx context.Context,
	command transport.Command,
	marker, reason string,
) probeOutcome {
	result, err := probe.Port.Run(ctx, command)
	if strings.Contains(result.Stdout+result.Stderr, marker) {
		return pass(reason)
	}
	if err != nil {
		return failed("project-action-failed", err)
	}
	return failed("project-action-marker-missing", errors.New("readiness marker is missing"))
}

func (probe CapabilityProbe) probeLSP(
	ctx context.Context,
	executable string,
	arguments []string,
	root string,
) probeOutcome {
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = errors.New("language server launcher is not a regular file")
		}
		return failed("lsp-launcher-invalid", err)
	}
	rootURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(root)}).String()
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"processId": os.Getpid(), "rootUri": rootURL,
			"capabilities": map[string]any{},
			"clientInfo":   map[string]string{"name": "mds-capability-probe", "version": "1"},
		},
	})
	if err != nil {
		return failed("lsp-request-invalid", err)
	}
	stdin := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(request), request))
	command := probe.toolCommand(executable, root, arguments...)
	command.Stdin = stdin
	command.Timeout = 20 * time.Second
	result, runErr := probe.Port.Run(ctx, command)
	if lspInitializeResponse([]byte(result.Stdout)) {
		return pass("stdio-initialize-response")
	}
	if runErr != nil {
		return failed("lsp-initialize-failed", runErr)
	}
	diagnostic := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	if len(diagnostic) > 4096 {
		diagnostic = diagnostic[len(diagnostic)-4096:]
	}
	return failed(
		"lsp-initialize-missing",
		fmt.Errorf("language server returned no initialize response: %s", diagnostic),
	)
}

func lspInitializeResponse(stream []byte) bool {
	for len(stream) > 0 {
		headerEnd := bytes.Index(stream, []byte("\r\n\r\n"))
		if headerEnd < 0 {
			return false
		}
		length := -1
		for _, line := range strings.Split(string(stream[:headerEnd]), "\r\n") {
			name, value, ok := strings.Cut(line, ":")
			if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
				length, _ = strconv.Atoi(strings.TrimSpace(value))
			}
		}
		stream = stream[headerEnd+4:]
		if length < 0 || length > len(stream) {
			return false
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(stream[:length], &message) == nil &&
			string(message.ID) == "1" && len(message.Result) > 0 && len(message.Error) == 0 {
			return true
		}
		stream = stream[length:]
	}
	return false
}
