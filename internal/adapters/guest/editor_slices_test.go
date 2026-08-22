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
		"DOTNET_WATCH_SUPPRESS_LAUNCH_BROWSER", "VSTEST_HOST_DEBUG", "-getProperty:TargetPath",
		"Debug .NET tests", "dap.run(dap.configurations.cs[index])", "dotnet_projects", "stdout = capture, stderr = capture",
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
	}{
		{
			name: "dotnet build restores exact selected project", set: dotnetPluginSet,
			action: "build", filetype: "cs", wantExecutable: "/managed/dotnet",
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
		},
		{
			name: "dotnet watch binds project after run verb", set: dotnetPluginSet,
			action: "watch", filetype: "cs", wantExecutable: "setsid",
			prepare:       prepareDotNetWebAction,
			wantArguments: []string{"watch", "run", "--launch-profile", "Web", "--urls", "http://127.0.0.1:0"},
			wantOrdered:   []string{"/managed/dotnet", "watch", "run", "--project", "$PROJECT", "--launch-profile", "Web", "--urls", "http://127.0.0.1:0"},
			wantProject:   true,
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
			var captured struct {
				Argv    []string `json:"argv"`
				Project string   `json:"project"`
				DAP     string   `json:"dap"`
			}
			if err := json.Unmarshal(content, &captured); err != nil {
				t.Fatalf("decode capture %q: %v", content, err)
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
		})
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
	launchSettings := `{"profiles":{"Web":{"commandName":"Project","applicationUrl":"http://127.0.0.1:0"}}}`
	if err := os.WriteFile(filepath.Join(properties, "launchSettings.json"), []byte(launchSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	return project
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
