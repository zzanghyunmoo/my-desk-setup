package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSymbolPanelIsPublishedForEveryIDESliceOnly(t *testing.T) {
	for _, set := range []pluginSet{
		idePluginSet,
		jvmPluginSet,
		dotnetPluginSet,
		allIDEPluginSets,
	} {
		files := configurationForPluginSet(set)
		if files["lua/configs/symbols.lua"] != renderSymbolConfig() {
			t.Fatalf("slice %d omits the managed symbol panel", set)
		}
		if !strings.Contains(files["lua/configs/lspconfig.lua"], `require("configs.symbols").setup()`) {
			t.Fatalf("slice %d does not set up the managed symbol panel", set)
		}
		assertRenderedLuaParses(t, files)
	}

	if _, exists := configurationForPluginSet(basePluginSet)["lua/configs/symbols.lua"]; exists {
		t.Fatal("NvChad-only configuration published an LSP symbol panel")
	}
}

func TestSymbolPanelUsesDocumentSymbolsAndDeclarationKinds(t *testing.T) {
	content := renderSymbolConfig()
	for _, token := range []string{
		"textDocument/documentSymbol",
		"vim.lsp.buf_request_all",
		"vim.lsp.util.show_document",
		`[5] = "class"`,
		`[6] = "method"`,
		`[7] = "property"`,
		`[8] = "field"`,
		`[9] = "constructor"`,
		`[11] = "interface"`,
		`[12] = "function"`,
		`[13] = "variable"`,
		`[14] = "constant"`,
		`[23] = "struct"`,
		"MdsSymbolsOpen",
		"MdsSymbolsToggle",
		"MdsSymbolsRefresh",
	} {
		if !strings.Contains(content, token) {
			t.Fatalf("managed symbol panel omits %q", token)
		}
	}
}

func TestSymbolPanelRendersHierarchyAndJumpsFromTreeColumn(t *testing.T) {
	nvim := requireHeadlessNeovim(t)
	fixtureRoot := t.TempDir()
	symbolsPath := filepath.Join(fixtureRoot, "symbols.lua")
	harnessPath := filepath.Join(fixtureRoot, "harness.lua")
	for path, content := range map[string]string{
		symbolsPath: renderSymbolConfig(),
		harnessPath: headlessSymbolPanelHarness,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(nvim, "--clean", "--headless", "-u", "NONE",
		"-c", "luafile "+harnessPath, "-c", "cquit")
	command.Env = append(os.Environ(),
		"MDS_TEST_SYMBOLS="+symbolsPath,
		"NVIM_LOG_FILE="+filepath.Join(fixtureRoot, "nvim.log"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("headless managed symbol panel harness: %v\n%s", err, output)
	}
}

const headlessSymbolPanelHarness = `local source_buf = vim.api.nvim_get_current_buf()
vim.bo[source_buf].swapfile = false
vim.api.nvim_buf_set_name(source_buf, "/tmp/widget.cpp")
vim.bo[source_buf].filetype = "cpp"
local source_win = vim.api.nvim_get_current_win()

local tree_buf = vim.api.nvim_create_buf(false, true)
vim.bo[tree_buf].filetype = "NvimTree"
local tree_win = vim.api.nvim_open_win(tree_buf, false, {
  split = "left",
  win = source_win,
  width = 28,
})
vim.api.nvim_win_set_width(tree_win, 28)

local active_source_buf = source_buf
local clients = {}
local callbacks = {}
local cancelled = 0
vim.lsp.get_clients = function(options)
  if not options then return {} end
  assert(options.bufnr == active_source_buf, vim.inspect({ requested = options.bufnr, active = active_source_buf }))
  assert(options.method == "textDocument/documentSymbol")
  return clients
end
vim.lsp.buf_request_all = function(bufnr, method, params, callback)
  assert(bufnr == active_source_buf)
  assert(method == "textDocument/documentSymbol")
  assert(params.textDocument.uri == vim.uri_from_bufnr(active_source_buf))
  table.insert(callbacks, callback)
  return function() cancelled = cancelled + 1 end
end
local shown
vim.lsp.util.show_document = function(location, encoding, options)
  shown = { location = location, encoding = encoding, options = options }
  return true
end

local symbols = dofile(assert(vim.env.MDS_TEST_SYMBOLS))
symbols.setup()
assert(symbols.open() == false, "headless symbol panel opened without an explicit force")
local original_list_uis = vim.api.nvim_list_uis
vim.api.nvim_list_uis = function() return { {} } end
vim.api.nvim_exec_autocmds("FileType", { pattern = "NvimTree" })
local automatically_opened_buf
assert(vim.wait(500, function()
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    local buf = vim.api.nvim_win_get_buf(win)
    if vim.bo[buf].filetype == "mds-symbols" then automatically_opened_buf = buf end
  end
  return automatically_opened_buf ~= nil
end), "FileType did not open the symbol panel")
local automatic_lines = vim.api.nvim_buf_get_lines(automatically_opened_buf, 0, -1, false)
assert(automatic_lines[2] == "[No LSP document symbols]", vim.inspect(automatic_lines))
clients = {
  { id = 7, offset_encoding = "utf-16" },
  { id = 8, offset_encoding = "utf-8" },
}
vim.api.nvim_exec_autocmds("LspAttach", { buffer = source_buf, data = { client_id = 7 } })
assert(vim.wait(500, function() return #callbacks == 1 end), "LspAttach did not refresh the symbol panel")
vim.api.nvim_list_uis = original_list_uis

callbacks[1]({ [7] = { result = {
  {
    name = "Widget", kind = 5,
    range = { start = { line = 0, character = 0 }, ["end"] = { line = 8, character = 1 } },
    selectionRange = { start = { line = 0, character = 6 }, ["end"] = { line = 0, character = 12 } },
    children = {
      {
        name = "value", kind = 8,
        range = { start = { line = 2, character = 2 }, ["end"] = { line = 2, character = 12 } },
        selectionRange = { start = { line = 2, character = 6 }, ["end"] = { line = 2, character = 11 } },
      },
    },
  },
  {
    name = "run", kind = 12,
    range = { start = { line = 10, character = 0 }, ["end"] = { line = 12, character = 1 } },
    selectionRange = { start = { line = 10, character = 4 }, ["end"] = { line = 10, character = 7 } },
  },
  {
    name = "count", kind = 13,
    range = { start = { line = 14, character = 0 }, ["end"] = { line = 14, character = 14 } },
    selectionRange = { start = { line = 14, character = 4 }, ["end"] = { line = 14, character = 9 } },
  },
} }, [8] = { result = {
  {
    name = "Widget", kind = 5,
    range = { start = { line = 0, character = 0 }, ["end"] = { line = 8, character = 1 } },
    selectionRange = { start = { line = 0, character = 6 }, ["end"] = { line = 0, character = 12 } },
    children = {
      {
        name = "label", kind = 7,
        range = { start = { line = 3, character = 2 }, ["end"] = { line = 3, character = 14 } },
      },
    },
  },
  {
    name = "external", kind = 12,
    location = {
      uri = "file:///tmp/external.cpp",
      range = { start = { line = 30, character = 1 }, ["end"] = { line = 30, character = 9 } },
    },
  },
} } })

local panel_win
local panel_buf
for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
  local buf = vim.api.nvim_win_get_buf(win)
  if vim.bo[buf].filetype == "mds-symbols" then
    panel_win = win
    panel_buf = buf
  end
end
assert(panel_win and panel_buf, "symbol panel window is missing")
local tree_row, tree_col = unpack(vim.fn.win_screenpos(tree_win))
local panel_row, panel_col = unpack(vim.fn.win_screenpos(panel_win))
assert(panel_row > tree_row, vim.inspect({ tree_row, panel_row }))
assert(panel_col == tree_col, vim.inspect({ tree_col, panel_col }))
assert(vim.api.nvim_win_get_width(panel_win) == vim.api.nvim_win_get_width(tree_win))

local lines = vim.api.nvim_buf_get_lines(panel_buf, 0, -1, false)
assert(lines[1] == "Symbols: widget.cpp", vim.inspect(lines))
assert(lines[2] == "[class] Widget", vim.inspect(lines))
assert(lines[3] == "  [field] value", vim.inspect(lines))
assert(lines[4] == "  [property] label", vim.inspect(lines))
assert(lines[5] == "[function] run", vim.inspect(lines))
assert(lines[6] == "[variable] count", vim.inspect(lines))
assert(lines[7] == "[function] external", vim.inspect(lines))

vim.api.nvim_set_current_win(panel_win)
vim.api.nvim_win_set_cursor(panel_win, { 5, 0 })
symbols.jump()
assert(vim.api.nvim_get_current_win() == source_win)
assert(shown.encoding == "utf-16", vim.inspect(shown))
assert(shown.options.focus == true and shown.options.reuse_win == true)
assert(shown.location.uri == vim.uri_from_bufnr(source_buf))
assert(shown.location.range.start.line == 10)

vim.api.nvim_set_current_win(panel_win)
vim.api.nvim_win_set_cursor(panel_win, { 7, 0 })
symbols.jump()
assert(shown.encoding == "utf-8", vim.inspect(shown))
assert(shown.location.uri == "file:///tmp/external.cpp")
assert(shown.location.range.start.line == 30)

symbols.refresh()
assert(#callbacks == 2, #callbacks)
callbacks[2]({ [7] = { result = {
  {
    name = "fresh", kind = 12,
    range = { start = { line = 20, character = 0 }, ["end"] = { line = 20, character = 5 } },
  },
} } })
callbacks[1]({ [7] = { result = {
  {
    name = "stale", kind = 12,
    range = { start = { line = 21, character = 0 }, ["end"] = { line = 21, character = 5 } },
  },
} } })
lines = vim.api.nvim_buf_get_lines(panel_buf, 0, -1, false)
assert(lines[2] == "[function] fresh", vim.inspect(lines))
assert(cancelled >= 1, cancelled)

symbols.refresh()
assert(#callbacks == 3, #callbacks)
callbacks[3]({ [7] = { result = {} } })
lines = vim.api.nvim_buf_get_lines(panel_buf, 0, -1, false)
assert(lines[2] == "[No symbols]", vim.inspect(lines))

symbols.refresh()
assert(#callbacks == 4, #callbacks)
callbacks[4]({ [7] = { error = { code = -32603, message = "failed" } } })
lines = vim.api.nvim_buf_get_lines(panel_buf, 0, -1, false)
assert(lines[2] == "[Symbol request failed]", vim.inspect(lines))

clients = {}
symbols.refresh()
lines = vim.api.nvim_buf_get_lines(panel_buf, 0, -1, false)
assert(lines[2] == "[No LSP document symbols]", vim.inspect(lines))

symbols.close(true)
assert(not vim.api.nvim_win_is_valid(panel_win), "manual close left the panel open")
vim.api.nvim_list_uis = function() return { {} } end
assert(symbols.open() == false, "suppressed panel reopened without an explicit command")
vim.api.nvim_exec_autocmds("BufWinEnter", { buffer = tree_buf })
vim.api.nvim_exec_autocmds("FileType", { pattern = "NvimTree" })
vim.wait(250, function() return false end)
for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
  assert(vim.bo[vim.api.nvim_win_get_buf(win)].filetype ~= "mds-symbols",
    "automatic tree events defeated manual suppression")
end
vim.cmd "MdsSymbolsOpen"
panel_win = nil
panel_buf = nil
assert(vim.wait(500, function()
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    local buf = vim.api.nvim_win_get_buf(win)
    if vim.bo[buf].filetype == "mds-symbols" then panel_win, panel_buf = win, buf end
  end
  return panel_win ~= nil
end), "MdsSymbolsOpen did not reopen the panel")
vim.api.nvim_list_uis = original_list_uis

local alternate_buf = vim.api.nvim_create_buf(false, false)
vim.bo[alternate_buf].swapfile = false
vim.api.nvim_buf_set_name(alternate_buf, "/tmp/alternate.go")
vim.bo[alternate_buf].filetype = "go"
active_source_buf = alternate_buf
local alternate_win = vim.api.nvim_open_win(alternate_buf, true, {
  split = "right",
  win = source_win,
  width = 20,
})
vim.api.nvim_exec_autocmds("BufEnter", { buffer = alternate_buf })
vim.api.nvim_set_current_win(tree_win)
assert(symbols.open(true), "split source refresh failed")
lines = vim.api.nvim_buf_get_lines(panel_buf, 0, -1, false)
assert(lines[1] == "Symbols: alternate.go", vim.inspect(lines))
vim.api.nvim_win_close(alternate_win, true)
active_source_buf = source_buf
vim.api.nvim_set_current_win(source_win)
assert(symbols.open(true), "original source refresh failed")

vim.api.nvim_set_current_win(tree_win)
vim.cmd "tabnew"
local second_source_buf = vim.api.nvim_get_current_buf()
vim.bo[second_source_buf].swapfile = false
vim.api.nvim_buf_set_name(second_source_buf, "/tmp/lib.rs")
vim.bo[second_source_buf].filetype = "rust"
active_source_buf = second_source_buf
clients = { { id = 7, offset_encoding = "utf-8" } }
local second_tree_buf = vim.api.nvim_create_buf(false, true)
vim.bo[second_tree_buf].filetype = "NvimTree"
local second_tree_win = vim.api.nvim_open_win(second_tree_buf, false, {
  split = "left",
  win = vim.api.nvim_get_current_win(),
  width = 28,
})
local first_panel_win = panel_win
assert(symbols.open(true), "second-tab symbol panel did not open")
local second_panel_win
for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
  if vim.bo[vim.api.nvim_win_get_buf(win)].filetype == "mds-symbols" then second_panel_win = win end
end
assert(second_panel_win, "second-tab symbol panel window is missing")
assert(not vim.api.nvim_win_is_valid(first_panel_win), "first-tab panel remained orphaned")
assert(vim.api.nvim_win_get_tabpage(second_panel_win) == vim.api.nvim_get_current_tabpage())

active_source_buf = source_buf
clients = {}
vim.api.nvim_list_uis = function() return { {} } end
vim.cmd "tabprevious"
local returned_panel_win
assert(vim.wait(500, function()
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    if vim.bo[vim.api.nvim_win_get_buf(win)].filetype == "mds-symbols" then returned_panel_win = win end
  end
  return returned_panel_win ~= nil
end), "tree-focused tab did not restore its symbol panel")
vim.api.nvim_list_uis = original_list_uis
assert(not vim.api.nvim_win_is_valid(second_panel_win), "second-tab panel remained orphaned")

vim.api.nvim_list_uis = function() return { {} } end
vim.cmd "tabnew"
local special_buf = vim.api.nvim_get_current_buf()
vim.bo[special_buf].buftype = "nofile"
vim.bo[special_buf].filetype = "terminal-test"
local special_tree_buf = vim.api.nvim_create_buf(false, true)
vim.bo[special_tree_buf].filetype = "NvimTree"
vim.api.nvim_open_win(special_tree_buf, false, {
  split = "left",
  win = vim.api.nvim_get_current_win(),
  width = 28,
})
local special_panel_win
local special_panel_buf
assert(vim.wait(500, function()
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    local buf = vim.api.nvim_win_get_buf(win)
    if vim.bo[buf].filetype == "mds-symbols" then special_panel_win, special_panel_buf = win, buf end
  end
  return special_panel_win ~= nil
end), "special-buffer tab did not open a placeholder panel")
lines = vim.api.nvim_buf_get_lines(special_panel_buf, 0, -1, false)
assert(lines[1] == "Symbols" and lines[2] == "[No source buffer]", vim.inspect(lines))
assert(not vim.api.nvim_win_is_valid(returned_panel_win), "special-buffer tab retained the previous tab's panel")

vim.cmd "tabclose"
vim.cmd "tabfirst"
active_source_buf = source_buf
clients = {}
returned_panel_win = nil
assert(vim.wait(500, function()
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    if vim.bo[vim.api.nvim_win_get_buf(win)].filetype == "mds-symbols" then returned_panel_win = win end
  end
  return returned_panel_win ~= nil
end), "source tab did not restore its symbol panel")
vim.api.nvim_list_uis = original_list_uis

local available_height = vim.api.nvim_win_get_height(tree_win)
  + vim.api.nvim_win_get_height(returned_panel_win)
vim.api.nvim_win_set_height(returned_panel_win, available_height - 2)
vim.api.nvim_exec_autocmds("WinResized", {})
assert(vim.wait(500, function()
  return vim.api.nvim_win_is_valid(returned_panel_win)
    and vim.api.nvim_win_get_height(tree_win) >= 6
    and vim.api.nvim_win_get_height(returned_panel_win) <= 12
end), "resize handling allowed the symbol panel to collapse the tree")

symbols.close(false)
assert(not vim.api.nvim_win_is_valid(returned_panel_win), "automatic close left the panel open")
vim.api.nvim_list_uis = function() return { {} } end
vim.api.nvim_exec_autocmds("VimResized", {})
returned_panel_win = nil
assert(vim.wait(500, function()
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    if vim.bo[vim.api.nvim_win_get_buf(win)].filetype == "mds-symbols" then returned_panel_win = win end
  end
  return returned_panel_win ~= nil
end), "symbol panel did not return when resize constraints recovered")
vim.api.nvim_list_uis = original_list_uis

vim.api.nvim_win_close(tree_win, true)
assert(vim.wait(500, function() return not vim.api.nvim_win_is_valid(returned_panel_win) end), "orphan symbol panel")
vim.cmd "qa!"
`
