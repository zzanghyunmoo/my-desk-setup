package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestJVMPluginLoadsTrustControlledDAPSetupForKotlinBuffers(t *testing.T) {
	pluginSpec := renderPluginSpec(jvmPluginSet)
	if !strings.Contains(pluginSpec, `ft = { "java", "kotlin" }`) {
		t.Fatal("managed JVM plugin must load its DAP setup for Kotlin-only projects")
	}
	jvmConfig := renderJVMConfig(map[string]runtimeTreeReference{
		"jdt-language-server":          testRuntimeTreeReference("jdt-language-server", "bin/jdtls"),
		"java-debug-server":            testRuntimeTreeReference("java-debug-server", "extension/server/debug.jar"),
		"java-test-server":             testRuntimeTreeReference("java-test-server", "extension/server/test.jar"),
		"spring-tools-language-server": testRuntimeTreeReference("spring-tools-language-server", "extension/language-server/spring.jar"),
		"kotlin-debug-adapter":         testRuntimeTreeReference("kotlin-debug-adapter", "adapter/bin/kotlin-debug-adapter"),
	})
	if !strings.Contains(jvmConfig, `dap.adapters.kotlin =`) {
		t.Fatal("managed JVM setup must register the pinned Kotlin adapter")
	}
	if strings.Contains(jvmConfig, `dap.configurations.kotlin`) {
		t.Fatal("Kotlin launch configurations must remain behind MdsProjectAction workspace trust")
	}
}

func TestJVMSetupRegistersOnlyPinnedKotlinAdapter(t *testing.T) {
	nvim := requireHeadlessNeovim(t)
	fixtureRoot := t.TempDir()
	jvmPath := filepath.Join(fixtureRoot, "jvm.lua")
	harnessPath := filepath.Join(fixtureRoot, "harness.lua")
	references := map[string]runtimeTreeReference{
		"jdt-language-server":          testRuntimeTreeReference("jdt-language-server", "bin/jdtls"),
		"java-debug-server":            testRuntimeTreeReference("java-debug-server", "extension/server/debug.jar"),
		"java-test-server":             testRuntimeTreeReference("java-test-server", "extension/server/test.jar"),
		"spring-tools-language-server": testRuntimeTreeReference("spring-tools-language-server", "extension/language-server/spring.jar"),
		"kotlin-debug-adapter":         testRuntimeTreeReference("kotlin-debug-adapter", "adapter/bin/kotlin-debug-adapter"),
	}
	for path, content := range map[string]string{
		jvmPath: renderJVMConfig(references),
		harnessPath: `package.preload["configs.trust"] = function()
  return {
    runtime = function(component, _, executable)
      return "/managed/" .. component .. "/" .. executable
    end,
    is_trusted = function() return true end,
  }
end
local dap = { adapters = {}, configurations = {} }
package.preload["dap"] = function() return dap end
vim.bo.filetype = "kotlin"
local jvm = dofile(assert(vim.env.MDS_TEST_JVM))
jvm.setup()
assert(vim.deep_equal(dap.adapters.kotlin, {
  type = "executable",
  command = "/managed/kotlin-debug-adapter/adapter/bin/kotlin-debug-adapter",
}), vim.inspect(dap.adapters.kotlin))
assert(dap.configurations.kotlin == nil, vim.inspect(dap.configurations.kotlin))
vim.cmd "qa!"
`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE",
		"-c", "luafile "+harnessPath, "-c", "cquit")
	command.Env = append(os.Environ(),
		"MDS_TEST_JVM="+jvmPath,
		"NVIM_LOG_FILE="+filepath.Join(fixtureRoot, "nvim.log"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("headless managed JVM harness: %v\n%s", err, output)
	}
}

func TestJVMPrepareDebugLoadsAJavaSourceBufferBeforeRegisteringDAP(t *testing.T) {
	nvim := requireHeadlessNeovim(t)
	fixtureRoot := t.TempDir()
	projectRoot := filepath.Join(fixtureRoot, "project")
	source := filepath.Join(projectRoot, "src", "main", "java", "App.java")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "gradlew"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("class App {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	jvmPath := filepath.Join(fixtureRoot, "jvm.lua")
	harnessPath := filepath.Join(fixtureRoot, "harness.lua")
	references := map[string]runtimeTreeReference{
		"jdt-language-server":          testRuntimeTreeReference("jdt-language-server", "bin/jdtls"),
		"java-debug-server":            testRuntimeTreeReference("java-debug-server", "extension/server/debug.jar"),
		"java-test-server":             testRuntimeTreeReference("java-test-server", "extension/server/test.jar"),
		"spring-tools-language-server": testRuntimeTreeReference("spring-tools-language-server", "extension/language-server/spring.jar"),
		"kotlin-debug-adapter":         testRuntimeTreeReference("kotlin-debug-adapter", "adapter/bin/kotlin-debug-adapter"),
	}
	for path, content := range map[string]string{
		jvmPath: renderJVMConfig(references),
		harnessPath: `local source = assert(vim.env.MDS_TEST_SOURCE)
vim.opt_global.swapfile = false
local attached
local notifications = {}
vim.notify = function(message)
  table.insert(notifications, message)
end
package.preload["configs.trust"] = function()
  return {
    runtime = function(component, _, executable)
      return "/managed/" .. component .. "/" .. executable
    end,
    is_trusted = function() return true end,
  }
end
local dap = { adapters = {}, configurations = {} }
package.preload["dap"] = function() return dap end
vim.lsp.get_clients = function(options)
  options = options or {}
  if attached and options.bufnr == attached then return { { name = "jdtls" } } end
  return {}
end
package.preload["jdtls"] = function()
  return {
    start_or_attach = function(_, options, start_options)
      assert(options.dap.hotcodereplace == "auto")
      attached = assert(start_options.bufnr)
      dap.adapters.java = function() end
    end,
    test_nearest_method = function() end,
    test_class = function() end,
  }
end
vim.bo.filetype = "NvimTree"
local jvm = dofile(assert(vim.env.MDS_TEST_JVM))
local ready
jvm.prepare_debug("java", source, function(value) ready = value end)
assert(vim.wait(1000, function() return ready ~= nil end), "Java debug preparation timed out")
assert(ready == true, vim.inspect(notifications))
assert(vim.uv.fs_realpath(vim.api.nvim_buf_get_name(attached)) == vim.uv.fs_realpath(source))
assert(type(dap.adapters.java) == "function")
vim.cmd "qa!"
`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE",
		"-c", "luafile "+harnessPath, "-c", "cquit")
	command.Env = append(os.Environ(),
		"MDS_TEST_JVM="+jvmPath,
		"MDS_TEST_SOURCE="+source,
		"NVIM_LOG_FILE="+filepath.Join(fixtureRoot, "nvim.log"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("headless Java debug preparation harness: %v\n%s", err, output)
	}
}

func TestTrustCommandsLoadBeforeFirstProjectFile(t *testing.T) {
	pluginSpec := renderPluginSpec(jvmPluginSet)
	if !strings.Contains(pluginSpec, `event = { "VimEnter", "BufReadPost", "BufNewFile" }`) {
		t.Fatal("managed LSP configuration must expose workspace trust commands at startup")
	}
}

func TestManagedInitLoadsDirectoryStartupModule(t *testing.T) {
	if !strings.Contains(renderManagedInit(), `require("base46").load_all_highlights()`) {
		t.Fatal("managed init must generate the base46 cache before loading it")
	}
	if !strings.Contains(renderManagedInit(), `require "configs.startup"`) {
		t.Fatal("managed init must load directory-first startup behavior")
	}
	if startup := editorConfiguration["lua/configs/startup.lua"]; startup != renderStartupConfig() {
		t.Fatal("managed editor configuration omits the rendered startup module")
	}
}

func TestManagedStartupCallbacksExecute(t *testing.T) {
	nvim := requireHeadlessNeovim(t)
	projectRoot := t.TempDir()
	otherRoot := t.TempDir()
	var err error
	projectRoot, err = filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	otherRoot, err = filepath.EvalSymlinks(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	fixtureRoot := t.TempDir()
	startupPath := filepath.Join(fixtureRoot, "startup.lua")
	harnessPath := filepath.Join(fixtureRoot, "harness.lua")
	for path, content := range map[string]string{
		startupPath: renderStartupConfig(),
		harnessPath: headlessManagedStartupHarness,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE",
		"-c", "luafile "+harnessPath, "-c", "cquit")
	command.Env = append(os.Environ(),
		"MDS_TEST_PROJECT_ROOT="+projectRoot,
		"MDS_TEST_OTHER_ROOT="+otherRoot,
		"MDS_TEST_STARTUP="+startupPath,
		"NVIM_LOG_FILE="+filepath.Join(fixtureRoot, "nvim.log"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("headless managed startup harness: %v\n%s", err, output)
	}
}

func TestWorkspaceTrustSeparatesVirtualBuffersFromNewProjectFiles(t *testing.T) {
	nvim := requireHeadlessNeovim(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Test.csproj"), []byte("<Project />\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "settings.gradle.kts"), []byte("rootProject.name = \"nested\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	nested, err = filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatal(err)
	}
	fixtureRoot := t.TempDir()
	trustPath := filepath.Join(fixtureRoot, "trust.lua")
	harnessPath := filepath.Join(fixtureRoot, "harness.lua")
	for path, content := range map[string]string{
		trustPath: workspaceTrustLua,
		harnessPath: `local root = assert(vim.env.MDS_TEST_ROOT)
local nested = assert(vim.env.MDS_TEST_NESTED)
vim.api.nvim_set_current_dir(root)
vim.api.nvim_buf_set_name(0, root .. "/NvimTree_1")
local trust = dofile(assert(vim.env.MDS_TEST_TRUST))
local roots = trust.roots(0)
assert(#roots == 1 and roots[1] == root, vim.inspect(roots))
assert(vim.fn.exists(":MdsTrustWorkspace") == 2)
local original_confirm = vim.fn.confirm
vim.fn.confirm = function() return 1 end
vim.cmd "MdsTrustWorkspace"
vim.fn.confirm = original_confirm
assert(trust.is_trusted(root))
vim.api.nvim_buf_set_name(0, nested .. "/Program.kt")
assert(trust.nearest(0) == nested)
assert(not trust.is_trusted(nested))
vim.cmd "qa!"
`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE",
		"-c", "luafile "+harnessPath, "-c", "cquit")
	command.Env = append(os.Environ(),
		"MDS_TEST_ROOT="+root,
		"MDS_TEST_NESTED="+nested,
		"MDS_TEST_TRUST="+trustPath,
		"XDG_STATE_HOME="+filepath.Join(fixtureRoot, "state"),
		"NVIM_LOG_FILE="+filepath.Join(fixtureRoot, "nvim.log"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("headless virtual project buffer trust harness: %v\n%s", err, output)
	}
}

func testRuntimeTreeReference(componentID, executable string) runtimeTreeReference {
	return runtimeTreeReference{
		ComponentID:   componentID,
		ArchiveSHA256: strings.Repeat("a", 64), ManifestSHA256: strings.Repeat("b", 64),
		LauncherSHA256: strings.Repeat("c", 64), Executable: executable,
		RequiredPaths: []string{executable},
	}
}

const headlessManagedStartupHarness = `local project_root = assert(vim.env.MDS_TEST_PROJECT_ROOT)
local other_root = assert(vim.env.MDS_TEST_OTHER_ROOT)
local startup = assert(vim.env.MDS_TEST_STARTUP)
local tree_calls = 0
vim.opt.directory = project_root

vim.api.nvim_create_user_command("NvimTreeFocus", function()
  tree_calls = tree_calls + 1
end, {})

local original_list_uis = vim.api.nvim_list_uis
local original_argc = vim.fn.argc
local original_argv = vim.fn.argv
vim.api.nvim_list_uis = function() return { {} } end

local function load_with_args(argument_count, argument, read_from_stdin)
  vim.fn.argc = function() return argument_count end
  vim.fn.argv = function() return argument end
  dofile(startup)
  if read_from_stdin then vim.api.nvim_exec_autocmds("StdinReadPre", {}) end
  vim.api.nvim_exec_autocmds("VimEnter", {})
end

vim.api.nvim_set_current_dir(project_root)
load_with_args(0, nil)
assert(vim.wait(1000, function() return tree_calls == 1 end), "bare start did not focus NvimTree")
assert(vim.uv.cwd() == project_root, vim.uv.cwd())

local named = vim.api.nvim_create_buf(true, false)
vim.api.nvim_set_current_buf(named)
vim.api.nvim_buf_set_name(named, project_root .. "/README.md")
load_with_args(0, nil)
vim.wait(20)
assert(tree_calls == 1, "named zero-argument start focused NvimTree")

local stdin_buffer = vim.api.nvim_create_buf(true, false)
vim.api.nvim_set_current_buf(stdin_buffer)
vim.api.nvim_set_current_dir(other_root)
load_with_args(0, nil, true)
vim.wait(20)
assert(tree_calls == 1, "stdin start focused NvimTree")
assert(vim.uv.cwd() == other_root, vim.uv.cwd())

vim.api.nvim_set_current_dir(other_root)
load_with_args(1, project_root)
assert(vim.wait(1000, function() return tree_calls == 2 end), "directory start did not focus NvimTree")
assert(vim.uv.cwd() == project_root, vim.uv.cwd())

vim.api.nvim_set_current_dir(other_root)
load_with_args(1, project_root .. "/Program.cs")
vim.wait(20)
assert(tree_calls == 2, "file start focused NvimTree")
assert(vim.uv.cwd() == other_root, vim.uv.cwd())

load_with_args(2, project_root)
vim.wait(20)
assert(tree_calls == 2, "multi-argument start focused NvimTree")

vim.api.nvim_list_uis = function() return {} end
load_with_args(0, nil)
vim.wait(20)
assert(tree_calls == 2, "headless start focused NvimTree")

vim.api.nvim_list_uis = original_list_uis
vim.fn.argc = original_argc
vim.fn.argv = original_argv
dofile(startup)
local recover_filetype = assert(vim.api.nvim_get_autocmds({
  event = "BufNewFile", group = "mds-directory-filetype",
})[1].callback)

local existing_path = project_root .. "/existing.cpp"
vim.fn.writefile({ "int main() { return 0; }" }, existing_path)
local existing = vim.api.nvim_create_buf(true, false)
vim.api.nvim_buf_set_name(existing, existing_path)
vim.api.nvim_exec_autocmds("BufReadPost", { buffer = existing })
vim.wait(20)
assert(vim.bo[existing].filetype == "cpp", vim.bo[existing].filetype)

for extension, expected in pairs({
  cpp = "cpp",
  go = "go",
  py = "python",
  rs = "rust",
  java = "java",
  cs = "cs",
}) do
  local buffer = vim.api.nvim_create_buf(true, false)
  local filename = project_root .. "/probe." .. extension
  vim.api.nvim_buf_set_name(buffer, filename)
  recover_filetype({ buf = buffer, file = filename })
  vim.wait(20)
  assert(vim.bo[buffer].filetype == expected, extension .. ":" .. vim.bo[buffer].filetype)
end

local empty_event = vim.api.nvim_create_buf(true, false)
vim.api.nvim_buf_set_name(empty_event, project_root .. "/empty-event.py")
recover_filetype({ buf = empty_event, file = "" })
vim.wait(20)
assert(vim.bo[empty_event].filetype == "python", vim.bo[empty_event].filetype)

local relative_event = vim.api.nvim_create_buf(true, false)
vim.api.nvim_buf_set_name(relative_event, project_root .. "/relative.go")
recover_filetype({ buf = relative_event, file = "relative.go" })
vim.wait(20)
assert(vim.bo[relative_event].filetype == "go", vim.bo[relative_event].filetype)

local unmatched = vim.api.nvim_create_buf(true, false)
vim.api.nvim_buf_set_name(unmatched, project_root .. "/probe.mds-unknown")
recover_filetype({ buf = unmatched, file = vim.api.nvim_buf_get_name(unmatched) })
vim.wait(20)
assert(vim.bo[unmatched].filetype == "", vim.bo[unmatched].filetype)

local deleted = vim.api.nvim_create_buf(true, false)
local deleted_name = project_root .. "/deleted.cs"
vim.api.nvim_buf_set_name(deleted, deleted_name)
recover_filetype({ buf = deleted, file = deleted_name })
vim.api.nvim_buf_delete(deleted, { force = true })
vim.wait(20)
assert(not vim.api.nvim_buf_is_valid(deleted))

local unloaded = vim.api.nvim_create_buf(true, false)
local unloaded_name = project_root .. "/unloaded.java"
vim.api.nvim_buf_set_name(unloaded, unloaded_name)
recover_filetype({ buf = unloaded, file = unloaded_name })
vim.api.nvim_buf_delete(unloaded, { unload = true })
vim.wait(20)
assert(not vim.api.nvim_buf_is_loaded(unloaded))

local claimed = vim.api.nvim_create_buf(true, false)
local claimed_name = project_root .. "/claimed.cs"
vim.api.nvim_buf_set_name(claimed, claimed_name)
recover_filetype({ buf = claimed, file = claimed_name })
vim.bo[claimed].filetype = "text"
vim.wait(20)
assert(vim.bo[claimed].filetype == "text")

local detected = vim.api.nvim_create_buf(true, false)
vim.api.nvim_buf_set_name(detected, project_root .. "/already.cs")
vim.bo[detected].filetype = "text"
recover_filetype({ buf = detected, file = vim.api.nvim_buf_get_name(detected) })
vim.wait(20)
assert(vim.bo[detected].filetype == "text")

local renamed = vim.api.nvim_create_buf(true, false)
local previous_name = project_root .. "/before.cs"
vim.api.nvim_buf_set_name(renamed, previous_name)
recover_filetype({ buf = renamed, file = previous_name })
vim.api.nvim_buf_set_name(renamed, project_root .. "/after.go")
vim.wait(20)
assert(vim.bo[renamed].filetype == "", vim.bo[renamed].filetype)

vim.cmd "qa!"
`
