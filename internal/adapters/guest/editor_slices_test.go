package guest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func TestPluginSetForActionRequiresNormalizedKnownSlices(t *testing.T) {
	action := planning.Action{ComponentID: "nvchad", Inputs: map[string]string{
		planning.EditorSlicesInput: "dotnet,jvm,legacy",
	}}
	set, err := pluginSetForAction(action)
	if err != nil || set != allIDEPluginSets {
		t.Fatalf("pluginSetForAction(full) = %v, %v", set, err)
	}
	for _, value := range []string{"jvm,dotnet", "jvm,jvm", "latest", "core,jvm"} {
		action.Inputs[planning.EditorSlicesInput] = value
		if _, err := pluginSetForAction(action); err == nil {
			t.Fatalf("pluginSetForAction(%q) accepted non-canonical slices", value)
		}
	}
}

func TestSliceConfigurationFullIsExactUnion(t *testing.T) {
	full := configurationForPluginSet(allIDEPluginSets)
	if full["lua/plugins/init.lua"] != renderPluginSpec(allIDEPluginSets) {
		t.Fatal("full plugin specification was not rendered by the shared renderer")
	}
	for _, token := range []string{
		"conform.nvim", "nvim-jdtls", "roslyn.nvim", "kotlin_lsp", "spring_boot",
	} {
		content := full["lua/plugins/init.lua"] + full["lua/configs/lspconfig.lua"]
		if !strings.Contains(content, token) {
			t.Fatalf("full slice configuration omits %q", token)
		}
	}
	for set, forbidden := range map[pluginSet][]string{
		idePluginSet:    {"nvim-jdtls", "roslyn.nvim"},
		jvmPluginSet:    {"conform.nvim", "roslyn.nvim"},
		dotnetPluginSet: {"conform.nvim", "nvim-jdtls"},
	} {
		content := configurationForPluginSet(set)["lua/plugins/init.lua"]
		for _, token := range forbidden {
			if strings.Contains(content, token) {
				t.Fatalf("slice %d unexpectedly contains %q", set, token)
			}
		}
	}
}

func TestJVMPluginLoadsDAPSetupForKotlinBuffers(t *testing.T) {
	pluginSpec := renderPluginSpec(jvmPluginSet)
	if !strings.Contains(pluginSpec, `ft = { "java", "kotlin" }`) {
		t.Fatal("managed JVM plugin must load its DAP setup for Kotlin-only projects")
	}
}

func TestManagedInitOpensProjectTreeForDirectoryFirstSessions(t *testing.T) {
	init := renderManagedInit()
	for _, token := range []string{
		`vim.api.nvim_create_autocmd("VimEnter"`,
		`vim.fn.argc()`,
		`vim.fn.isdirectory(argument)`,
		`vim.api.nvim_set_current_dir(project_directory)`,
		`vim.cmd "NvimTreeFocus"`,
	} {
		if !strings.Contains(init, token) {
			t.Fatalf("managed init omits directory-first project explorer behavior %q", token)
		}
	}
}

func TestExpectedPluginPinsAreExactSliceUnion(t *testing.T) {
	names := func(set pluginSet) []string {
		pins := expectedPluginPins(set)
		result := make([]string, 0, len(pins))
		for _, pin := range pins {
			result = append(result, pin.Name)
		}
		sort.Strings(result)
		return result
	}
	full := names(allIDEPluginSets)
	if !reflect.DeepEqual(full, names(idePluginSet|jvmPluginSet|dotnetPluginSet)) {
		t.Fatal("full plugin pin union is not deterministic")
	}
	for set, required := range map[pluginSet]string{
		jvmPluginSet: "nvim-jdtls", dotnetPluginSet: "roslyn.nvim",
	} {
		if !containsString(names(set), required) {
			t.Fatalf("slice %d omits exact plugin %s", set, required)
		}
	}
}

func TestActionConfigurationBindsRuntimeTreesAndWorkspaceTrust(t *testing.T) {
	action := planning.Action{
		ComponentID: "nvim-jvm",
		Inputs: map[string]string{
			planning.EditorSlicesInput: "jvm",
		},
	}
	for id, executable := range map[string]string{
		"jdt-language-server":          "bin/jdtls",
		"java-debug-server":            "extension/server/debug.jar",
		"java-test-server":             "extension/server/test.jar",
		"kotlin-debug-adapter":         "adapter/bin/kotlin-debug-adapter",
		"kotlin-language-server":       "extension/server/bin/intellij-server",
		"spring-tools-language-server": "extension/language-server/spring.jar",
	} {
		reference := runtimeTreeReference{
			ComponentID: id, ArchiveSHA256: strings.Repeat("a", 64),
			ManifestSHA256: strings.Repeat("b", 64), LauncherSHA256: strings.Repeat("c", 64),
			Executable: executable, RequiredPaths: []string{executable},
		}
		encoded, err := json.Marshal(reference)
		if err != nil {
			t.Fatal(err)
		}
		action.Inputs[planning.RuntimeTreeInputPrefix+id] = string(encoded)
	}
	files, set, err := configurationForAction(action)
	if err != nil || set != jvmPluginSet {
		t.Fatalf("configurationForAction() = %v, %v", set, err)
	}
	assertRenderedLuaParses(t, files)
	combined := files["lua/configs/lspconfig.lua"] + files["lua/configs/jvm.lua"] +
		files["lua/configs/trust.lua"] + files["lua/configs/actions.lua"]
	for _, token := range []string{
		strings.Repeat("b", 64) + "-" + strings.Repeat("a", 64),
		"MdsTrustWorkspace", "trust.is_trusted", ".mds-runtime-tree-current",
		"hotcodereplace", "gradlew", "spring_boot", "kotlin_lsp", "vim.system",
		"clear_env = true", "MdsProjectAction", "setsid", "-Dsts.lsp.client=vscode",
		"enableJdtClasspath = false", "executeCommand = { dynamicRegistration = false }",
	} {
		if !strings.Contains(combined, token) {
			t.Fatalf("managed JVM configuration omits %q", token)
		}
	}
	if strings.Contains(files["lua/configs/lspconfig.lua"], `filetypes = { "java",`) {
		t.Fatal("standalone Spring language server must not compete with jdtls for Java buffers")
	}

	action.Inputs[planning.RuntimeTreeInputPrefix+"jdt-language-server"] = `{"component_id":"other"}`
	if _, _, err := configurationForAction(action); err == nil {
		t.Fatal("configuration accepted a mismatched runtime-tree identity")
	}
}

func TestDotNetActionConfigurationUsesExactRoslynRazorAndDAPTrees(t *testing.T) {
	action := planning.Action{
		ComponentID: "nvim-dotnet",
		Inputs:      map[string]string{planning.EditorSlicesInput: "dotnet"},
	}
	for id, executable := range map[string]string{
		"netcoredbg":             "netcoredbg/netcoredbg",
		"roslyn-language-server": "tools/net10.0/linux-arm64/roslyn-language-server",
	} {
		reference := runtimeTreeReference{
			ComponentID: id, ArchiveSHA256: strings.Repeat("d", 64),
			ManifestSHA256: strings.Repeat("e", 64), LauncherSHA256: strings.Repeat("f", 64),
			Executable: executable, RequiredPaths: []string{executable},
		}
		if id == "roslyn-language-server" {
			reference.RequiredPaths = append(
				reference.RequiredPaths,
				"tools/net10.0/linux-arm64/roslyn-language-server.dll",
			)
		}
		encoded, err := json.Marshal(reference)
		if err != nil {
			t.Fatal(err)
		}
		action.Inputs[planning.RuntimeTreeInputPrefix+id] = string(encoded)
	}
	files, set, err := configurationForAction(action)
	if err != nil || set != dotnetPluginSet {
		t.Fatalf("configurationForAction() = %v, %v", set, err)
	}
	assertRenderedLuaParses(t, files)
	combined := files["lua/configs/lspconfig.lua"] + files["lua/configs/dotnet.lua"] +
		files["lua/configs/trust.lua"] + files["lua/configs/actions.lua"]
	for _, token := range []string{
		"require(\"roslyn\").setup", "roslyn.target", "tools/net10.0/linux-arm64/roslyn-language-server.dll",
		"cmd = { dotnet, roslyn, \"--stdio\" }", "netcoredbg", "--interpreter=vscode",
		"http://127.0.0.1:0", "managed_executable \"dotnet\"", "managed_launcher(\"vscode-html-language-server\")",
		"launchSettings.json", "ASP.NET launch profile", "non-loopback",
		"profile_loopback_urls(profile.environment)", "resolved.environment.ASPNETCORE_URLS = nil",
		"DOTNET_WATCH_SUPPRESS_LAUNCH_BROWSER", "VSTEST_HOST_DEBUG", "-getProperty:TargetPath",
		"Debug .NET tests", "configuration.mds_root = root", "dotnet_projects", "stdout = capture, stderr = capture",
		"local function launch_arguments()", "if not vim.g.mds_dotnet_launch_profile then return {} end", "args = launch_arguments",
		"MdsUntrustWorkspace", "MdsWorkspaceUntrusted", "pcall(client.stop, client, true)",
		"debug_root = root", "action_environment(spec)", "result.ASPNETCORE_URLS = \"http://127.0.0.1:0\"",
		"binding_environment_key", "^KESTREL__ENDPOINTS__", "require(\"configs.dotnet\").stop(root)",
		"dap.sessions()", "session.config.mds_root == root", "dap.set_session(session)",
		"MDS requires a valid Project launch profile for ASP.NET actions", "local argv = vim.list_extend({ \"setsid\" }",
		"vim.system({ \"setsid\", dotnet, \"test\", project }", "pcall(vim.uv.kill, -task.pid, 15)",
		"launch_valid(root, generation)", "mds_generation = generation", "mds_trust_guard",
		"project_candidates = candidates", "spec.project_candidates or dotnet_projects(root)",
	} {
		if !strings.Contains(combined, token) {
			t.Fatalf("managed .NET configuration omits %q", token)
		}
	}
	if strings.Contains(combined, "--no-restore") || strings.Contains(combined, "--no-build") {
		t.Fatal("managed .NET actions must restore and build a clean checkout")
	}
	pluginSpec := files["lua/plugins/init.lua"]
	for _, token := range []string{
		`event = { "BufReadPre", "BufNewFile" }`, `mise, "which", "dotnet"`,
		"DOTNET_ROOT", `config = function() require("configs.dotnet").setup() end`,
	} {
		if !strings.Contains(pluginSpec, token) {
			t.Fatalf("managed Roslyn plugin specification omits %q", token)
		}
	}
	if strings.Contains(pluginSpec, `"seblyng/roslyn.nvim", ft =`) {
		t.Fatal("Roslyn must load before FileType so the first Razor buffer uses the managed command")
	}
	for _, forbidden := range []string{"omnisharp", "rzls", "latest"} {
		if strings.Contains(strings.ToLower(combined), forbidden) {
			t.Fatalf("managed .NET configuration contains forbidden fallback %q", forbidden)
		}
	}
}

func TestProjectActionsExecuteExactCleanCheckoutAndDebugSelections(t *testing.T) {
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("headless Neovim is unavailable")
	}
	for _, test := range []struct {
		name           string
		set            pluginSet
		action         string
		filetype       string
		prepare        func(*testing.T, string) string
		wantExecutable string
		wantArguments  []string
		wantOrdered    []string
		wantDAP        string
		wantProject    bool
		wantRejected   bool
		wantSafeEnv    bool
	}{
		{
			name: "dotnet build restores exact selected project", set: dotnetPluginSet,
			action: "build", filetype: "cs", wantExecutable: "setsid",
			prepare: func(t *testing.T, root string) string {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "global.json"), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				first := filepath.Join(root, "App.csproj")
				second := filepath.Join(root, "tests", "App.Tests.csproj")
				if err := os.MkdirAll(filepath.Dir(second), 0o700); err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{first, second} {
					if err := os.WriteFile(path, []byte("<Project />\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				return second
			},
			wantArguments: []string{"build"}, wantProject: true,
		},
		{
			name: "dotnet run binds project before launch arguments", set: dotnetPluginSet,
			action: "run", filetype: "cs", wantExecutable: "setsid",
			prepare:       prepareDotNetWebAction,
			wantArguments: []string{"run", "--launch-profile", "Web", "--urls", "http://127.0.0.1:0"},
			wantOrdered:   []string{"/managed/dotnet", "run", "--project", "$PROJECT", "--launch-profile", "Web", "--urls", "http://127.0.0.1:0"},
			wantProject:   true,
			wantSafeEnv:   true,
		},
		{
			name: "dotnet watch binds project after run verb", set: dotnetPluginSet,
			action: "watch", filetype: "cs", wantExecutable: "setsid",
			prepare:       prepareDotNetWebAction,
			wantArguments: []string{"watch", "run", "--launch-profile", "Web", "--urls", "http://127.0.0.1:0"},
			wantOrdered:   []string{"/managed/dotnet", "watch", "run", "--project", "$PROJECT", "--launch-profile", "Web", "--urls", "http://127.0.0.1:0"},
			wantProject:   true,
			wantSafeEnv:   true,
		},
		{
			name: "dotnet launch profile requires explicit direct project selection", set: dotnetPluginSet,
			action: "run", filetype: "cs", wantExecutable: "setsid",
			prepare:       prepareDotNetMultiProjectWebAction,
			wantArguments: []string{"run", "--launch-profile", "Web", "--urls", "http://127.0.0.1:0"},
			wantOrdered:   []string{"/managed/dotnet", "run", "--project", "$PROJECT", "--launch-profile", "Web", "--urls", "http://127.0.0.1:0"},
			wantProject:   true,
		},
		{
			name: "dotnet web action rejects missing launch profile", set: dotnetPluginSet,
			action: "debug-app", filetype: "cs", prepare: prepareDotNetWebWithoutProfile,
			wantRejected: true,
		},
		{
			name: "dotnet web action rejects case-variant non-loopback URL", set: dotnetPluginSet,
			action: "run", filetype: "cs", prepare: prepareDotNetWebWithUnsafeProfile,
			wantRejected: true,
		},
		{
			name: "jvm debug attaches automatically", set: jvmPluginSet,
			action: "debug-app", filetype: "java", wantExecutable: "setsid",
			prepare: func(t *testing.T, root string) string {
				t.Helper()
				gradlew := filepath.Join(root, "gradlew")
				if err := os.WriteFile(gradlew, []byte("#!/bin/sh\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				return ""
			},
			wantArguments: []string{"--no-daemon", "bootRun", "--debug-jvm"},
			wantDAP:       "java",
		},
		{
			name: "jvm test debug reruns only the selected task", set: jvmPluginSet,
			action: "debug-test", filetype: "java", wantExecutable: "setsid",
			prepare: func(t *testing.T, root string) string {
				t.Helper()
				gradlew := filepath.Join(root, "gradlew")
				if err := os.WriteFile(gradlew, []byte("#!/bin/sh\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				return ""
			},
			wantArguments: []string{"--no-daemon", "test", "--debug-jvm", "--rerun"},
			wantDAP:       "java",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			nvimLog := filepath.Join(t.TempDir(), "nvim.log")
			wantedProject := test.prepare(t, root)
			actionsPath := filepath.Join(t.TempDir(), "actions.lua")
			capturePath := filepath.Join(t.TempDir(), "capture.json")
			if err := os.WriteFile(actionsPath, []byte(renderProjectActions(test.set)), 0o600); err != nil {
				t.Fatal(err)
			}
			harnessPath := filepath.Join(t.TempDir(), "harness.lua")
			if err := os.WriteFile(harnessPath, []byte(headlessProjectActionHarness), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE", "-c", "luafile "+harnessPath)
			command.Env = append(os.Environ(),
				"MDS_TEST_ROOT="+root,
				"MDS_TEST_ACTION="+test.action,
				"MDS_TEST_FILETYPE="+test.filetype,
				"MDS_TEST_PROJECT="+wantedProject,
				"MDS_TEST_ACTIONS="+actionsPath,
				"MDS_TEST_CAPTURE="+capturePath,
				"NVIM_LOG_FILE="+nvimLog,
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("headless action harness: %v\n%s", err, output)
			}
			content, err := os.ReadFile(capturePath)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantRejected && string(content) == "[]" {
				return
			}
			var captured struct {
				Argv    []string          `json:"argv"`
				Project string            `json:"project"`
				DAP     string            `json:"dap"`
				Env     map[string]string `json:"env"`
			}
			if err := json.Unmarshal(content, &captured); err != nil {
				t.Fatalf("decode capture %q: %v", content, err)
			}
			if test.wantRejected {
				if len(captured.Argv) != 0 || captured.DAP != "" {
					t.Fatalf("rejected action launched argv=%v DAP=%q", captured.Argv, captured.DAP)
				}
				return
			}
			if len(captured.Argv) == 0 || captured.Argv[0] != test.wantExecutable {
				t.Fatalf("argv = %v, want executable %s", captured.Argv, test.wantExecutable)
			}
			for _, argument := range test.wantArguments {
				if !slices.Contains(captured.Argv, argument) {
					t.Fatalf("argv = %v, want argument %s", captured.Argv, argument)
				}
			}
			if slices.Contains(captured.Argv, "--no-restore") || slices.Contains(captured.Argv, "--no-build") {
				t.Fatalf("clean checkout action retained a no-restore/build flag: %v", captured.Argv)
			}
			if test.wantProject && !slices.Contains(captured.Argv, wantedProject) {
				t.Fatalf("selected project = %q argv=%v, want %q", captured.Project, captured.Argv, wantedProject)
			}
			if captured.Project != "" && captured.Project != wantedProject {
				t.Fatalf("captured project = %q, want %q", captured.Project, wantedProject)
			}
			if len(test.wantOrdered) > 0 {
				want := slices.Clone(test.wantOrdered)
				for index, argument := range want {
					if argument == "$PROJECT" {
						want[index] = wantedProject
					}
				}
				if !slices.Equal(captured.Argv[1:], want) {
					t.Fatalf("argv after setsid = %v, want %v", captured.Argv[1:], want)
				}
			}
			if captured.DAP != test.wantDAP {
				t.Fatalf("DAP adapter = %q, want %q", captured.DAP, test.wantDAP)
			}
			if test.wantSafeEnv {
				if captured.Env["MDS_ENV"] != "kept" || captured.Env["ASPNETCORE_URLS"] != "http://127.0.0.1:0" {
					t.Fatalf("managed ASP.NET environment = %#v", captured.Env)
				}
				if _, exists := captured.Env["aspnetcore_urls"]; exists {
					t.Fatal("managed ASP.NET environment retained a case-variant URL override")
				}
				for key := range captured.Env {
					normalized := strings.ToUpper(key)
					if normalized == "ASPNETCORE_HTTP_PORTS" || normalized == "DOTNET_URLS" ||
						strings.Contains(normalized, "KESTREL__ENDPOINTS__") {
						t.Fatalf("managed ASP.NET environment retained binding override %q", key)
					}
				}
			}
		})
	}
}

func TestWorkspaceTrustRevocationIsRootScoped(t *testing.T) {
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("headless Neovim is unavailable")
	}
	rootA := filepath.Join(t.TempDir(), "a")
	rootB := filepath.Join(t.TempDir(), "b")
	for _, root := range []string{rootA, rootB} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	rootA, err = filepath.EvalSymlinks(rootA)
	if err != nil {
		t.Fatal(err)
	}
	rootB, err = filepath.EvalSymlinks(rootB)
	if err != nil {
		t.Fatal(err)
	}
	fixtureRoot := t.TempDir()
	trustPath := filepath.Join(fixtureRoot, "trust.lua")
	actionsPath := filepath.Join(fixtureRoot, "actions.lua")
	harnessPath := filepath.Join(fixtureRoot, "harness.lua")
	capturePath := filepath.Join(fixtureRoot, "capture.json")
	for path, content := range map[string]string{
		trustPath:   workspaceTrustLua,
		actionsPath: renderProjectActions(dotnetPluginSet),
		harnessPath: headlessTrustRevocationHarness,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE", "-c", "luafile "+harnessPath)
	command.Env = append(os.Environ(),
		"MDS_TEST_ROOT_A="+rootA,
		"MDS_TEST_ROOT_B="+rootB,
		"MDS_TEST_TRUST="+trustPath,
		"MDS_TEST_ACTIONS="+actionsPath,
		"MDS_TEST_CAPTURE="+capturePath,
		"XDG_STATE_HOME="+filepath.Join(fixtureRoot, "state"),
		"NVIM_LOG_FILE="+filepath.Join(fixtureRoot, "nvim.log"),
	)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("headless trust revocation harness: %v\n%s", commandErr, output)
	}
	content, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read trust revocation capture: %v\n%s", err, output)
	}
	var captured struct {
		RootATrusted  bool   `json:"root_a_trusted"`
		RootBTrusted  bool   `json:"root_b_trusted"`
		LSPStopped    string `json:"lsp_stopped"`
		DAPTerminated string `json:"dap_terminated"`
		DAPFocused    string `json:"dap_focused"`
		DotNetStopped string `json:"dotnet_stopped"`
	}
	if err := json.Unmarshal(content, &captured); err != nil {
		t.Fatalf("decode capture %q: %v", content, err)
	}
	if captured.RootATrusted || !captured.RootBTrusted || captured.LSPStopped != rootA ||
		captured.DAPTerminated != rootA || captured.DAPFocused != rootB || captured.DotNetStopped != rootA {
		t.Fatalf("root-scoped revocation = %#v", captured)
	}
}

func TestWorkspaceTrustRevocationCancelsScheduledJVMAttach(t *testing.T) {
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("headless Neovim is unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gradlew"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixtureRoot := t.TempDir()
	actionsPath := filepath.Join(fixtureRoot, "actions.lua")
	harnessPath := filepath.Join(fixtureRoot, "harness.lua")
	capturePath := filepath.Join(fixtureRoot, "capture.json")
	for path, content := range map[string]string{
		actionsPath: renderProjectActions(jvmPluginSet),
		harnessPath: headlessScheduledAttachRevocationHarness,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE", "-c", "luafile "+harnessPath)
	command.Env = append(os.Environ(),
		"MDS_TEST_ROOT="+root,
		"MDS_TEST_ACTIONS="+actionsPath,
		"MDS_TEST_CAPTURE="+capturePath,
		"NVIM_LOG_FILE="+filepath.Join(fixtureRoot, "nvim.log"),
	)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("headless scheduled attach revocation harness: %v\n%s", commandErr, output)
	}
	content, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read scheduled attach capture: %v\n%s", err, output)
	}
	var captured struct {
		DAPRuns         int `json:"dap_runs"`
		GroupTerminated int `json:"group_terminated"`
		GroupKilled     int `json:"group_killed"`
	}
	if err := json.Unmarshal(content, &captured); err != nil {
		t.Fatalf("decode capture %q: %v", content, err)
	}
	if captured.DAPRuns != 0 || captured.GroupTerminated != 2 || captured.GroupKilled != 2 {
		t.Fatalf("revoked scheduled JVM attach = %#v", captured)
	}
}

func TestWorkspaceTrustRevocationCancelsPendingDotNetTestAttach(t *testing.T) {
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("headless Neovim is unavailable")
	}
	root := t.TempDir()
	project := filepath.Join(root, "Probe.csproj")
	if err := os.WriteFile(project, []byte(`<Project Sdk="Microsoft.NET.Sdk"></Project>`), 0o600); err != nil {
		t.Fatal(err)
	}
	references := map[string]runtimeTreeReference{
		"netcoredbg": {
			ComponentID: "netcoredbg", ArchiveSHA256: strings.Repeat("d", 64),
			ManifestSHA256: strings.Repeat("e", 64), LauncherSHA256: strings.Repeat("f", 64),
			Executable: "netcoredbg/netcoredbg", RequiredPaths: []string{"netcoredbg/netcoredbg"},
		},
		"roslyn-language-server": {
			ComponentID: "roslyn-language-server", ArchiveSHA256: strings.Repeat("d", 64),
			ManifestSHA256: strings.Repeat("e", 64), LauncherSHA256: strings.Repeat("f", 64),
			Executable:    "tools/net10.0/linux-arm64/roslyn-language-server",
			RequiredPaths: []string{"tools/net10.0/linux-arm64/roslyn-language-server"},
		},
	}
	fixtureRoot := t.TempDir()
	dotnetPath := filepath.Join(fixtureRoot, "dotnet.lua")
	actionsPath := filepath.Join(fixtureRoot, "actions.lua")
	harnessPath := filepath.Join(fixtureRoot, "harness.lua")
	capturePath := filepath.Join(fixtureRoot, "capture.json")
	for path, content := range map[string]string{
		dotnetPath:  renderDotNetConfig(references),
		actionsPath: renderProjectActions(dotnetPluginSet),
		harnessPath: headlessPendingDotNetAttachRevocationHarness,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE", "-c", "luafile "+harnessPath)
	command.Env = append(os.Environ(),
		"MDS_TEST_ROOT="+root,
		"MDS_TEST_PROJECT="+project,
		"MDS_TEST_DOTNET="+dotnetPath,
		"MDS_TEST_ACTIONS="+actionsPath,
		"MDS_TEST_CAPTURE="+capturePath,
		"NVIM_LOG_FILE="+filepath.Join(fixtureRoot, "nvim.log"),
	)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("headless pending .NET attach revocation harness: %v\n%s", commandErr, output)
	}
	content, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read pending .NET attach capture: %v\n%s", err, output)
	}
	var captured struct {
		AttachSucceeded bool `json:"attach_succeeded"`
		GroupKilled     bool `json:"group_killed"`
		Trusted         bool `json:"trusted"`
	}
	if err := json.Unmarshal(content, &captured); err != nil {
		t.Fatalf("decode capture %q: %v", content, err)
	}
	if captured.AttachSucceeded || !captured.GroupKilled || captured.Trusted {
		t.Fatalf("revoked pending .NET test attach = %#v", captured)
	}
}

func TestASPNetDebugConfigurationEnforcesManagedLoopback(t *testing.T) {
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("headless Neovim is unavailable")
	}
	root := t.TempDir()
	project := prepareDotNetWebAction(t, root)
	references := map[string]runtimeTreeReference{}
	for id, executable := range map[string]string{
		"netcoredbg":             "netcoredbg/netcoredbg",
		"roslyn-language-server": "tools/net10.0/linux-arm64/roslyn-language-server",
	} {
		references[id] = runtimeTreeReference{
			ComponentID: id, ArchiveSHA256: strings.Repeat("d", 64),
			ManifestSHA256: strings.Repeat("e", 64), LauncherSHA256: strings.Repeat("f", 64),
			Executable: executable, RequiredPaths: []string{executable},
		}
	}
	fixtureRoot := t.TempDir()
	dotnetPath := filepath.Join(fixtureRoot, "dotnet.lua")
	actionsPath := filepath.Join(fixtureRoot, "actions.lua")
	harnessPath := filepath.Join(fixtureRoot, "harness.lua")
	capturePath := filepath.Join(fixtureRoot, "capture.json")
	for path, content := range map[string]string{
		dotnetPath:  renderDotNetConfig(references),
		actionsPath: renderProjectActions(dotnetPluginSet),
		harnessPath: headlessASPNetDebugHarness,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE", "-c", "luafile "+harnessPath)
	command.Env = append(os.Environ(),
		"MDS_TEST_ROOT="+root,
		"MDS_TEST_PROJECT="+project,
		"MDS_TEST_DOTNET="+dotnetPath,
		"MDS_TEST_ACTIONS="+actionsPath,
		"MDS_TEST_CAPTURE="+capturePath,
		"NVIM_LOG_FILE="+filepath.Join(fixtureRoot, "nvim.log"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("headless ASP.NET debug harness: %v\n%s", err, output)
	}
	content, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		Cwd     string            `json:"cwd"`
		Root    string            `json:"root"`
		Project string            `json:"project"`
	}
	if err := json.Unmarshal(content, &captured); err != nil {
		t.Fatalf("decode capture %q: %v", content, err)
	}
	if !slices.Equal(captured.Args, []string{"--urls", "http://127.0.0.1:0"}) ||
		captured.Env["ASPNETCORE_URLS"] != "http://127.0.0.1:0" || captured.Env["MDS_ENV"] != "kept" ||
		captured.Cwd != root || captured.Root != root || captured.Project != project {
		t.Fatalf("managed ASP.NET debug configuration = %#v", captured)
	}
	for key := range captured.Env {
		normalized := strings.ToUpper(key)
		if normalized == "ASPNETCORE_HTTP_PORTS" || normalized == "DOTNET_URLS" ||
			strings.Contains(normalized, "KESTREL__ENDPOINTS__") || key == "aspnetcore_urls" {
			t.Fatalf("managed ASP.NET debug retained binding override %q", key)
		}
	}
}

func prepareDotNetWebAction(t *testing.T, root string) string {
	t.Helper()
	project := filepath.Join(root, "App.csproj")
	if err := os.WriteFile(project, []byte("<Project />\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	properties := filepath.Join(root, "Properties")
	if err := os.MkdirAll(properties, 0o700); err != nil {
		t.Fatal(err)
	}
	launchSettings := `{"profiles":{"Web":{"commandName":"Project","applicationUrl":"http://127.0.0.1:0","environmentVariables":{"MDS_ENV":"kept","aspnetcore_urls":"http://localhost:8080","DOTNET_URLS":"http://localhost:8082","aspnetcore_http_ports":"8080","Kestrel__Endpoints__Public__Url":"http://0.0.0.0:8081","ASPNETCORE_Kestrel__Endpoints__Prefixed__Url":"http://0.0.0.0:8083"}}}}`
	if err := os.WriteFile(filepath.Join(properties, "launchSettings.json"), []byte(launchSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	return project
}

func prepareDotNetWebWithoutProfile(t *testing.T, root string) string {
	t.Helper()
	project := filepath.Join(root, "App.csproj")
	if err := os.WriteFile(project, []byte(`<Project Sdk="Microsoft.NET.Sdk.Web" />`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return project
}

func prepareDotNetWebWithUnsafeProfile(t *testing.T, root string) string {
	t.Helper()
	project := filepath.Join(root, "App.csproj")
	if err := os.WriteFile(project, []byte(`<Project Sdk="Microsoft.NET.Sdk.Web" />`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	properties := filepath.Join(root, "Properties")
	if err := os.MkdirAll(properties, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"profiles":{"Web":{"commandName":"Project","environmentVariables":{"aspnetcore_urls":"http://0.0.0.0:8080"}}}}`
	if err := os.WriteFile(filepath.Join(properties, "launchSettings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	return project
}

func prepareDotNetMultiProjectWebAction(t *testing.T, root string) string {
	t.Helper()
	first := filepath.Join(root, "Api.csproj")
	second := filepath.Join(root, "Web.csproj")
	for _, project := range []string{first, second} {
		if err := os.WriteFile(project, []byte("<Project />\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	properties := filepath.Join(root, "Properties")
	if err := os.MkdirAll(properties, 0o700); err != nil {
		t.Fatal(err)
	}
	launchSettings := `{"profiles":{"Web":{"commandName":"Project","applicationUrl":"http://127.0.0.1:0"}}}`
	if err := os.WriteFile(filepath.Join(properties, "launchSettings.json"), []byte(launchSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	return second
}

const headlessProjectActionHarness = `local root = assert(vim.env.MDS_TEST_ROOT)
local wanted_action = assert(vim.env.MDS_TEST_ACTION)
local wanted_project = vim.env.MDS_TEST_PROJECT
local record = {}

package.preload["configs.trust"] = function()
  return {
    roots = function() return { root } end,
    is_trusted = function(candidate) return candidate == root end,
    managed_executable = function(name) assert(name == "dotnet"); return "/managed/dotnet" end,
  }
end
package.preload["dap"] = function()
  return {
    configurations = { cs = { { marker = "app" }, { marker = "test" } } },
    run = function(config)
      record.dap = config.type or config.marker
      record.project = vim.g.mds_dotnet_project
    end,
  }
end

vim.bo.filetype = assert(vim.env.MDS_TEST_FILETYPE)
vim.ui.select = function(items, options, callback)
  if options.prompt == "MDS project action" then callback(wanted_action); return end
  if options.prompt == ".NET project" then
    for _, item in ipairs(items) do
      if item == wanted_project then record.project = item; callback(item); return end
    end
    error("wanted project was not offered")
  end
  callback(items[1])
end
vim.uv.new_tcp = function()
  return { bind = function() return 0 end, close = function() end }
end
vim.system = function(argv, options, callback)
  record.argv = vim.deepcopy(argv)
  record.cwd = options.cwd
  record.env = vim.deepcopy(options.env or {})
  if options.stdout then
    vim.schedule(function()
      options.stdout(nil, "Listening for transport dt_socket at address: 5005\n")
    end)
  end
  return { pid = 1234, kill = function() end }
end

local actions = dofile(assert(vim.env.MDS_TEST_ACTIONS))
actions.open()
vim.defer_fn(function()
  local handle = assert(io.open(assert(vim.env.MDS_TEST_CAPTURE), "wb"))
  handle:write(vim.json.encode(record))
  handle:close()
  vim.cmd "qa!"
end, 250)
`

const headlessTrustRevocationHarness = `local root_a = assert(vim.env.MDS_TEST_ROOT_A)
local root_b = assert(vim.env.MDS_TEST_ROOT_B)
local record = {}
local state_dir = vim.fn.stdpath "state" .. "/mds"
vim.fn.mkdir(state_dir, "p", 448)
local state = assert(io.open(state_dir .. "/workspace-trust.json", "wb"))
state:write(vim.json.encode({ [vim.fn.sha256(root_a)] = true, [vim.fn.sha256(root_b)] = true }) .. "\n")
state:close()

vim.lsp.get_clients = function()
  return {
    { root_dir = root_a, stop = function() record.lsp_stopped = root_a end },
    { root_dir = root_b, stop = function() record.lsp_wrong = root_b end },
  }
end

local trust = dofile(assert(vim.env.MDS_TEST_TRUST))
package.preload["configs.trust"] = function() return trust end
package.preload["configs.dotnet"] = function()
  return { stop = function(root) record.dotnet_stopped = root end }
end

local session_a = { config = { mds_root = root_a }, closed = false }
local session_b = { config = { mds_root = root_b }, closed = false }
local current = session_b
package.preload["dap"] = function()
  return {
    session = function() return current end,
    sessions = function() return { [1] = session_a, [2] = session_b } end,
    set_session = function(session) current = session; record.dap_focused = session.config.mds_root end,
    terminate = function() record.dap_terminated = current.config.mds_root end,
  }
end

local actions = dofile(assert(vim.env.MDS_TEST_ACTIONS))
actions.setup()
assert(trust.untrust(root_a))
record.root_a_trusted = trust.is_trusted(root_a)
record.root_b_trusted = trust.is_trusted(root_b)
vim.lsp.get_clients = function() return {} end
local capture = assert(io.open(assert(vim.env.MDS_TEST_CAPTURE), "wb"))
capture:write(vim.json.encode(record))
capture:close()
vim.cmd "qa!"
`

const headlessScheduledAttachRevocationHarness = `local root = assert(vim.env.MDS_TEST_ROOT)
local record = { dap_runs = 0, group_terminated = 0, group_killed = 0 }
local trusted = true
local next_pid = 1233

package.preload["configs.trust"] = function()
  return {
    roots = function() return { root } end,
    is_trusted = function(candidate) return trusted and candidate == root end,
    managed_executable = function(name) return name end,
  }
end
package.preload["configs.dotnet"] = function() return { stop = function() end } end
local dap = {
  listeners = { before = { event_initialized = {} } },
  run = function() record.dap_runs = record.dap_runs + 1 end,
  session = function() return nil end,
  sessions = function() return {} end,
  set_session = function() end,
  terminate = function() end,
}
package.preload["dap"] = function() return dap end
vim.ui.select = function(items, options, callback)
  if options.prompt == "MDS project action" then callback("debug-test"); return end
  callback(items[1])
end
vim.uv.new_tcp = function()
  return { bind = function() return 0 end, close = function() end }
end
vim.uv.kill = function(pid, signal)
  if pid < 0 and signal == 15 then record.group_terminated = record.group_terminated + 1 end
  if pid < 0 and signal == 9 then record.group_killed = record.group_killed + 1 end
end
vim.system = function(_, options, callback)
  next_pid = next_pid + 1
  local pid = next_pid
  local completed = false
  vim.schedule(function()
    options.stdout(nil, "Listening for transport dt_socket at address: 5005\n")
  end)
  return {
    pid = pid,
    kill = function(_, signal)
      if signal == 15 and not completed then
        completed = true
        vim.schedule(function() callback({ code = 143 }) end)
      end
    end,
  }
end

local actions = dofile(assert(vim.env.MDS_TEST_ACTIONS))
actions.setup()
actions.open()
actions.open()
trusted = false
vim.api.nvim_exec_autocmds("User", { pattern = "MdsWorkspaceUntrusted", data = { root = root } })
vim.defer_fn(function()
  local capture = assert(io.open(assert(vim.env.MDS_TEST_CAPTURE), "wb"))
  capture:write(vim.json.encode(record))
  capture:close()
  vim.cmd "qa!"
end, 2250)
`

const headlessPendingDotNetAttachRevocationHarness = `local root = assert(vim.env.MDS_TEST_ROOT)
local project = assert(vim.env.MDS_TEST_PROJECT)
local record = { attach_succeeded = false, group_killed = false }
local trusted = true

package.preload["configs.trust"] = function()
  return {
    roots = function() return { root } end,
    is_trusted = function(candidate) return trusted and candidate == root end,
    managed_executable = function() return "/managed/dotnet" end,
    runtime = function(component, _, executable) return "/managed/" .. component .. "/" .. executable end,
  }
end
package.preload["roslyn"] = function() return { setup = function() end } end
vim.lsp.config = function() end
vim.lsp.enable = function() end
vim.uv.kill = function(pid)
  if pid == -4321 then record.group_killed = true end
end

local dap = { adapters = {}, configurations = {}, listeners = { before = { event_initialized = {} } } }
dap.run = function(configuration)
  local ok = pcall(configuration.processId)
  record.attach_succeeded = ok
end
dap.session = function() return nil end
dap.sessions = function() return {} end
dap.set_session = function() end
dap.terminate = function() end
package.preload["dap"] = function() return dap end

local task = { pid = 4321, kill = function() end }
vim.system = function(_, options)
  vim.schedule(function()
    trusted = false
    vim.api.nvim_exec_autocmds("User", { pattern = "MdsWorkspaceUntrusted", data = { root = root } })
    options.stdout(nil, "Process Id: 9988\n")
  end)
  return task
end
vim.ui.select = function(items, options, callback)
  if options.prompt == "MDS project action" then callback("debug-test"); return end
  if options.prompt == ".NET project" then callback(project); return end
  callback(items[1])
end

local dotnet = dofile(assert(vim.env.MDS_TEST_DOTNET))
dotnet.setup()
local actions = dofile(assert(vim.env.MDS_TEST_ACTIONS))
actions.setup()
actions.open()
record.trusted = trusted
local capture = assert(io.open(assert(vim.env.MDS_TEST_CAPTURE), "wb"))
capture:write(vim.json.encode(record))
capture:close()
vim.cmd "qa!"
`

const headlessASPNetDebugHarness = `local root = assert(vim.env.MDS_TEST_ROOT)
local project = assert(vim.env.MDS_TEST_PROJECT)
local record = {}

package.preload["configs.trust"] = function()
  return {
    roots = function() return { root } end,
    is_trusted = function(candidate) return candidate == root end,
    managed_executable = function() return "/managed/dotnet" end,
    runtime = function(component, _, executable) return "/managed/" .. component .. "/" .. executable end,
  }
end
package.preload["roslyn"] = function() return { setup = function() end } end
vim.lsp.config = function() end
vim.lsp.enable = function() end

local dap = { adapters = {}, configurations = {} }
dap.run = function(configuration)
  record.args = configuration.args()
  record.env = configuration.env()
  record.cwd = configuration.cwd()
  record.root = configuration.mds_root
  record.project = vim.g.mds_dotnet_project
end
dap.terminate = function() end
dap.sessions = function() return {} end
dap.session = function() return nil end
dap.set_session = function() end
package.preload["dap"] = function() return dap end

local dotnet = dofile(assert(vim.env.MDS_TEST_DOTNET))
dotnet.setup()
local actions = dofile(assert(vim.env.MDS_TEST_ACTIONS))
vim.bo.filetype = "cs"
vim.ui.select = function(items, options, callback)
  if options.prompt == "MDS project action" then callback("debug-app"); return end
  if options.prompt == ".NET project" then callback(project); return end
  callback(items[1])
end
actions.open()
vim.defer_fn(function()
  local capture = assert(io.open(assert(vim.env.MDS_TEST_CAPTURE), "wb"))
  capture:write(vim.json.encode(record))
  capture:close()
  vim.cmd "qa!"
end, 200)
`

func assertRenderedLuaParses(t *testing.T, files map[string]string) {
	t.Helper()
	luac, err := exec.LookPath("luac")
	if err != nil {
		return
	}
	root := t.TempDir()
	for relativePath, content := range files {
		if filepath.Ext(relativePath) != ".lua" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(luac, "-p", path).CombinedOutput(); err != nil {
			t.Fatalf("parse rendered Lua %s: %v\n%s", relativePath, err, output)
		}
	}
}

func TestLanguageSlicesFailClosedWithoutEveryExactRuntimeTree(t *testing.T) {
	for _, action := range []planning.Action{
		{ComponentID: "nvim-jvm", Inputs: map[string]string{planning.EditorSlicesInput: "jvm"}},
		{ComponentID: "nvim-dotnet", Inputs: map[string]string{planning.EditorSlicesInput: "dotnet"}},
	} {
		if _, _, err := configurationForAction(action); err == nil || !strings.Contains(err.Error(), "missing exact runtime tree") {
			t.Fatalf("configurationForAction(%s) error = %v", action.ComponentID, err)
		}
	}
}

func containsString(values []string, wanted string) bool {
	return slices.Contains(values, wanted)
}
