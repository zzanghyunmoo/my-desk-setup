package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefactorConfigIsPublishedForEveryIDESliceOnly(t *testing.T) {
	for _, set := range []pluginSet{
		idePluginSet,
		jvmPluginSet,
		dotnetPluginSet,
		allIDEPluginSets,
	} {
		files := configurationForPluginSet(set)
		if files["lua/configs/refactor.lua"] != renderRefactorConfig() {
			t.Fatalf("slice %d omits the managed refactor router", set)
		}
		pluginSpec := files["lua/plugins/init.lua"]
		for _, token := range []string{
			`"ThePrimeagen/refactoring.nvim"`,
			`"lewis6991/async.nvim"`,
			`"<leader>rv"`,
			`"<leader>rm"`,
			`"<leader>rf"`,
			`"<leader>rc"`,
			`mode = "x"`,
			`expr = true`,
		} {
			if !strings.Contains(pluginSpec, token) {
				t.Fatalf("slice %d refactor plugin spec omits %q", set, token)
			}
		}
		assertRenderedLuaParses(t, files)
	}

	if _, exists := configurationForPluginSet(basePluginSet)["lua/configs/refactor.lua"]; exists {
		t.Fatal("NvChad-only configuration published the IDE refactor router")
	}
}

func TestRefactorRouterUsesLanguageAppropriateProviders(t *testing.T) {
	nvim := requireHeadlessNeovim(t)
	fixtureRoot := t.TempDir()
	refactorPath := filepath.Join(fixtureRoot, "refactor.lua")
	harnessPath := filepath.Join(fixtureRoot, "harness.lua")
	for path, content := range map[string]string{
		refactorPath: renderRefactorConfig(),
		harnessPath:  headlessRefactorHarness,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE",
		"-c", "luafile "+harnessPath, "-c", "cquit")
	command.Env = append(os.Environ(), "MDS_TEST_REFACTOR="+refactorPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("headless managed refactor harness: %v\n%s", err, output)
	}
}

func TestRefactorParsersFollowLanguageSlices(t *testing.T) {
	for set, expected := range map[pluginSet][]string{
		idePluginSet:     {"cpp", "go", "python", "rust"},
		jvmPluginSet:     {"java"},
		dotnetPluginSet:  {"c_sharp"},
		allIDEPluginSets: {"c_sharp", "cpp", "go", "java", "python", "rust"},
	} {
		actual := refactorParsers(set)
		if strings.Join(actual, ",") != strings.Join(expected, ",") {
			t.Fatalf("refactorParsers(%d) = %v, want %v", set, actual, expected)
		}
	}
}

func TestInspectRefactorParsersRequiresRegularManagedFiles(t *testing.T) {
	home := t.TempDir()
	ready, detail, err := inspectRefactorParsers(home, dotnetPluginSet)
	if err != nil || ready || !strings.Contains(detail, "directory is missing") {
		t.Fatalf("missing parser directory = (%v, %q, %v)", ready, detail, err)
	}

	parserRoot := filepath.Join(managedTreesitterRoot(home), "parser")
	if err := os.MkdirAll(parserRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	parserPath := filepath.Join(parserRoot, "c_sharp.so")
	if err := os.WriteFile(parserPath, []byte("parser"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, detail, err = inspectRefactorParsers(home, dotnetPluginSet)
	if err != nil || !ready || detail != "" {
		t.Fatalf("regular parser = (%v, %q, %v)", ready, detail, err)
	}

	if err := os.Remove(parserPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "outside.so"), parserPath); err != nil {
		t.Fatal(err)
	}
	if ready, _, err = inspectRefactorParsers(home, dotnetPluginSet); err == nil || ready {
		t.Fatalf("symlinked parser = (%v, %v), want unsafe-path error", ready, err)
	}
}

func TestInspectRefactorParsersRejectsSymlinkedManagedDirectory(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	managedParent := filepath.Join(home, ".local", "share", "mds", "nvim")
	if err := os.MkdirAll(managedParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, managedTreesitterRoot(home)); err != nil {
		t.Fatal(err)
	}
	if ready, _, err := inspectRefactorParsers(home, idePluginSet); err == nil || ready {
		t.Fatalf("symlinked parser root = (%v, %v), want unsafe-path error", ready, err)
	}
}

const headlessRefactorHarness = `local calls = { lsp = {}, plugin = {}, notifications = {} }

package.preload["refactoring"] = function()
  return {
    extract_var = function()
      table.insert(calls.plugin, "variable")
      return "plugin-variable"
    end,
    extract_func = function()
      table.insert(calls.plugin, "function")
      return "plugin-function"
    end,
  }
end

vim.lsp.buf.code_action = function(options)
  table.insert(calls.lsp, options)
end
vim.notify = function(message)
  table.insert(calls.notifications, message)
end

local refactor = dofile(assert(vim.env.MDS_TEST_REFACTOR))
local function invoke(filetype, kind)
  vim.bo.filetype = filetype
  return refactor.extract(kind)
end

assert(invoke("cs", "variable") == "plugin-variable")
assert(invoke("cs", "method") == "plugin-function")
assert(#calls.plugin == 2)

assert(invoke("rust", "function") == "<Ignore>")
local rust_function = calls.lsp[#calls.lsp]
assert(rust_function.context.only[1] == "refactor.extract")
assert(rust_function.apply == true)
assert(rust_function.filter({ kind = "refactor.extract", title = "Extract into function" }))
assert(not rust_function.filter({ kind = "refactor.extract", title = "Extract into variable" }))

assert(invoke("go", "method") == "<Ignore>")
local method = calls.lsp[#calls.lsp]
assert(method.filter({ kind = "refactor.extract.method", title = "Extract method" }))
assert(not method.filter({ kind = "refactor.extract.function", title = "Extract function" }))

assert(invoke("java", "class") == "<Ignore>")
local class = calls.lsp[#calls.lsp]
assert(class.filter({ kind = "refactor.extract", title = "Extract base class" }))
assert(not class.filter({ kind = "refactor.extract", title = "Extract method" }))

assert(invoke("python", "function") == "plugin-function")
assert(invoke("cpp", "method") == "plugin-function")

local lsp_count = #calls.lsp
assert(invoke("cs", "function") == "<Ignore>")
assert(invoke("rust", "class") == "<Ignore>")
assert(#calls.lsp == lsp_count)
assert(vim.wait(500, function() return #calls.notifications == 2 end))
assert(calls.notifications[1]:find("C#", 1, true), vim.inspect(calls.notifications))
assert(calls.notifications[2]:find("Rust", 1, true), vim.inspect(calls.notifications))

vim.cmd "enew"
vim.bo.filetype = "rust"
vim.api.nvim_buf_set_lines(0, 0, -1, false, {
  "fn main() {",
  "    println!(\"Hello, world!\");",
  "}",
})
local literal = {
  type = function() return "string_literal" end,
  range = function() return 1, 13, 1, 28 end,
  parent = function() return nil end,
}
vim.treesitter.get_parser = function()
  return { parse = function() end }
end
vim.treesitter.get_node = function()
  return {
    type = function() return "string_content" end,
    parent = function() return literal end,
  }
end
vim.keymap.set("x", "<leader>rv", function() return refactor.extract "variable" end, { expr = true })
vim.ui.input = function(_, callback) callback "message" end
vim.api.nvim_win_set_cursor(0, { 2, 0 })
vim.fn.feedkeys(vim.keycode('f"lvi"<leader>rv'), "mx")
assert(vim.wait(500, function()
  return vim.deep_equal(vim.api.nvim_buf_get_lines(0, 0, -1, false), {
    "fn main() {",
    "    let message = \"Hello, world!\";",
    "    println!(\"{}\", message);",
    "}",
  })
end), "Rust macro string Extract Variable failed: " .. vim.inspect(vim.api.nvim_buf_get_lines(0, 0, -1, false)))

vim.cmd "enew!"
vim.bo.filetype = "cpp"
vim.api.nvim_buf_set_lines(0, 0, -1, false, {
  "#include <iostream>",
  "",
  "int main(int argc, char **argv) {",
  "  std::cout << \"hello world!\" << std::endl;",
  "  return 0;",
  "}",
})
local cpp_literal = {
  type = function() return "string_literal" end,
  range = function() return 3, 15, 3, 29 end,
  parent = function() return nil end,
}
vim.treesitter.get_node = function()
  return {
    type = function() return "string_content" end,
    parent = function() return cpp_literal end,
  }
end
vim.ui.input = function(_, callback) callback "msg" end
vim.api.nvim_win_set_cursor(0, { 4, 0 })
vim.fn.feedkeys(vim.keycode('f"lvi"<leader>rv'), "mx")
assert(vim.wait(500, function()
  return vim.deep_equal(vim.api.nvim_buf_get_lines(0, 0, -1, false), {
    "#include <iostream>",
    "",
    "int main(int argc, char **argv) {",
    "  const auto msg = \"hello world!\";",
    "  std::cout << msg << std::endl;",
    "  return 0;",
    "}",
  })
end), "C++ string Extract Variable failed: " .. vim.inspect(vim.api.nvim_buf_get_lines(0, 0, -1, false)))

vim.cmd "qa!"
`
