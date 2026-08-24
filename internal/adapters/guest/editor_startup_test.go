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

func TestTrustCommandsLoadBeforeFirstProjectFile(t *testing.T) {
	pluginSpec := renderPluginSpec(jvmPluginSet)
	if !strings.Contains(pluginSpec, `event = { "VimEnter", "BufReadPost", "BufNewFile" }`) {
		t.Fatal("managed LSP configuration must expose workspace trust commands at startup")
	}
}

func TestManagedInitLoadsDirectoryStartupModule(t *testing.T) {
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

vim.api.nvim_create_user_command("NvimTreeFocus", function()
  tree_calls = tree_calls + 1
end, {})

local original_list_uis = vim.api.nvim_list_uis
local original_argc = vim.fn.argc
local original_argv = vim.fn.argv
vim.api.nvim_list_uis = function() return { {} } end

local function load_with_args(argument_count, argument)
  vim.fn.argc = function() return argument_count end
  vim.fn.argv = function() return argument end
  dofile(startup)
  vim.api.nvim_exec_autocmds("VimEnter", {})
end

vim.api.nvim_set_current_dir(project_root)
load_with_args(0, nil)
assert(vim.wait(1000, function() return tree_calls == 1 end), "bare start did not focus NvimTree")
assert(vim.uv.cwd() == project_root, vim.uv.cwd())

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

for extension, expected in pairs({
  cpp = "cpp",
  go = "go",
  py = "python",
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
