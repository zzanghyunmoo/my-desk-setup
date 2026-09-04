package guest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

var refactorParserSets = map[pluginSet][]string{
	idePluginSet:    {"cpp", "go", "python", "rust"},
	jvmPluginSet:    {"java"},
	dotnetPluginSet: {"c_sharp"},
}

func refactorParsers(set pluginSet) []string {
	seen := make(map[string]bool)
	for slice, parsers := range refactorParserSets {
		if set&slice == 0 {
			continue
		}
		for _, parser := range parsers {
			seen[parser] = true
		}
	}
	result := make([]string, 0, len(seen))
	for parser := range seen {
		result = append(result, parser)
	}
	sort.Strings(result)
	return result
}

func renderRefactorPluginSpec(set pluginSet) string {
	quoted := make([]string, 0, len(refactorParsers(set)))
	for _, parser := range refactorParsers(set) {
		quoted = append(quoted, strconv.Quote(parser))
	}
	return fmt.Sprintf(`  { "nvim-treesitter/nvim-treesitter",
    opts = function(_, opts)
      opts.ensure_installed = vim.list_extend(opts.ensure_installed or {}, { %s })
      opts.install_dir = vim.fn.expand "~/.local/share/mds/nvim/treesitter"
      return opts
    end,
  },
  { "ThePrimeagen/refactoring.nvim",
    dependencies = { "nvim-treesitter/nvim-treesitter", "lewis6991/async.nvim" },
    init = function() vim.treesitter.language.register("c_sharp", "cs") end,
    keys = {
      { "<leader>rv", function() return require("configs.refactor").extract "variable" end, mode = "x", desc = "Extract variable", expr = true },
      { "<leader>rm", function() return require("configs.refactor").extract "method" end, mode = "x", desc = "Extract method", expr = true },
      { "<leader>rf", function() return require("configs.refactor").extract "function" end, mode = "x", desc = "Extract function", expr = true },
      { "<leader>rc", function() return require("configs.refactor").extract "class" end, mode = "x", desc = "Extract class", expr = true },
    },
    opts = {},
  },`, strings.Join(quoted, ", "))
}

func renderRefactorConfig() string {
	return `local M = {}

local routes = {
  cpp = { variable = "plugin-variable", method = "plugin-function", ["function"] = "plugin-function", class = "lsp" },
  rust = { variable = "rust-variable", ["function"] = "lsp" },
  python = { variable = "plugin-variable", method = "plugin-function", ["function"] = "plugin-function", class = "lsp" },
  go = { variable = "lsp", method = "lsp", ["function"] = "lsp" },
  java = { variable = "plugin-variable", method = "plugin-function", class = "lsp" },
  cs = { variable = "plugin-variable", method = "plugin-function", class = "lsp" },
}

local language_names = {
  cpp = "C++",
  rust = "Rust",
  python = "Python",
  go = "Go",
  java = "Java",
  cs = "C#",
}

local labels = {
  variable = "Variable",
  method = "Method",
  ["function"] = "Function",
  class = "Class",
}

local function notify(message, level)
  vim.schedule(function() vim.notify(message, level or vim.log.levels.INFO, { title = "MDS refactor" }) end)
end

local function matches(kind, action)
  local expected_kind = "refactor.extract." .. kind
  if action.kind == expected_kind or (action.kind and vim.startswith(action.kind, expected_kind .. ".")) then
    return true
  end
  local title = type(action.title) == "string" and action.title:lower() or ""
  return title:find(kind, 1, true) ~= nil
end

local function lsp_extract(kind)
  vim.lsp.buf.code_action {
    context = { only = { "refactor.extract" } },
    filter = function(action) return matches(kind, action) end,
    apply = true,
  }
  return "<Ignore>"
end

local function plugin_extract(operation)
  local ok, refactoring = pcall(require, "refactoring")
  if not ok then
    notify("refactoring.nvim을 불러오지 못했습니다", vim.log.levels.ERROR)
    return "<Ignore>"
  end
  if operation == "plugin-variable" then return refactoring.extract_var() end
  return refactoring.extract_func()
end

local rust_literal_types = {
  string_literal = true,
  raw_string_literal = true,
  byte_string_literal = true,
  char_literal = true,
  byte_literal = true,
}

local rust_format_macros = {
  print = true,
  println = true,
  eprint = true,
  eprintln = true,
  format = true,
}

local function position_before_or_equal(left_row, left_col, right_row, right_col)
  return left_row < right_row or (left_row == right_row and left_col <= right_col)
end

local function expand_rust_literal(bufnr, start_row, start_col, end_row, end_col)
  pcall(function() vim.treesitter.get_parser(bufnr):parse() end)
  local ok, node = pcall(vim.treesitter.get_node, {
    bufnr = bufnr,
    pos = { start_row, start_col },
  })
  if not ok then return start_row, start_col, end_row, end_col end
  while node do
    if rust_literal_types[node:type()] then
      local node_start_row, node_start_col, node_end_row, node_end_col = node:range()
      if position_before_or_equal(node_start_row, node_start_col, start_row, start_col)
        and position_before_or_equal(end_row, end_col, node_end_row, node_end_col)
      then
        return node_start_row, node_start_col, node_end_row, node_end_col
      end
    end
    node = node:parent()
  end
  return start_row, start_col, end_row, end_col
end

local function rust_extract_variable()
  local mode = vim.fn.mode()
  if mode ~= "v" then
    notify("Rust Extract Variable은 문자 단위 Visual 선택에서 사용해야 합니다", vim.log.levels.WARN)
    return "<Ignore>"
  end

  local positions = vim.fn.getregionpos(vim.fn.getpos "v", vim.fn.getpos ".", { type = mode })
  if #positions == 0 then
    notify("추출할 Rust 표현식을 선택하지 못했습니다", vim.log.levels.WARN)
    return "<Ignore>"
  end
  local first = positions[1][1]
  local last = positions[#positions][2]
  local start_row, start_col = first[2] - 1, first[3] - 1
  local end_row, end_col = last[2] - 1, last[3]
  local bufnr = vim.api.nvim_get_current_buf()
  start_row, start_col, end_row, end_col = expand_rust_literal(
    bufnr,
    start_row,
    start_col,
    end_row,
    end_col
  )
  local changedtick = vim.api.nvim_buf_get_changedtick(bufnr)
  local selected = vim.api.nvim_buf_get_text(bufnr, start_row, start_col, end_row, end_col, {})
  local expression = vim.trim(table.concat(selected, "\n"))
  if expression == "" then
    notify("추출할 Rust 표현식이 비어 있습니다", vim.log.levels.WARN)
    return "<Ignore>"
  end

  vim.schedule(function()
    vim.ui.input({ prompt = "Variable name: ", default = "value" }, function(name)
      if name == nil then return end
      name = vim.trim(name)
      if not name:match "^[_%a][_%w]*$" then
        notify("올바른 Rust 변수 이름을 입력하세요", vim.log.levels.ERROR)
        return
      end
      if not vim.api.nvim_buf_is_valid(bufnr) or vim.api.nvim_buf_get_changedtick(bufnr) ~= changedtick then
        notify("버퍼가 변경되어 Rust Extract Variable을 취소했습니다", vim.log.levels.WARN)
        return
      end

      local original = vim.api.nvim_buf_get_lines(bufnr, start_row, end_row + 1, false)
      local prefix = original[1]:sub(1, start_col)
      local suffix = original[#original]:sub(end_col + 1)
      local replacement = name
      local macro_name = prefix:match "([_%a][_%w]*)!%(%s*$"
      if rust_format_macros[macro_name] then
        if not suffix:match "^%s*%)" or expression:find("[{}]") then
          notify("인자가 있는 Rust 포맷 문자열은 안전하게 변수로 추출할 수 없습니다", vim.log.levels.WARN)
          return
        end
        replacement = '"{}", ' .. name
      end
      local indent = original[1]:match "^%s*" or ""
      local declaration = vim.split(
        indent .. "let " .. name .. " = " .. expression .. ";",
        "\n",
        { plain = true }
      )
      table.insert(declaration, prefix .. replacement .. suffix)
      vim.api.nvim_buf_set_lines(bufnr, start_row, end_row + 1, false, declaration)
      notify("Rust variable extracted")
    end)
  end)
  return "<Ignore>"
end

function M.extract(kind)
  local filetype = vim.bo.filetype
  local route = routes[filetype] and routes[filetype][kind]
  if not route then
    local language = language_names[filetype] or filetype
    notify(string.format("%s에서는 Extract %s를 지원하지 않습니다", language, labels[kind] or kind))
    return "<Ignore>"
  end
  if route == "lsp" then return lsp_extract(kind) end
  if route == "rust-variable" then return rust_extract_variable() end
  return plugin_extract(route)
end

return M
`
}

func managedTreesitterRoot(home string) string {
	return filepath.Join(home, ".local", "share", "mds", "nvim", "treesitter")
}

func inspectRefactorParsers(home string, set pluginSet) (bool, string, error) {
	root := managedTreesitterRoot(home)
	parserRoot := filepath.Join(root, "parser")
	exists, err := inspectDirectoryBelow(home, parserRoot)
	if err != nil {
		return false, "", err
	}
	if !exists {
		return false, "managed Tree-sitter parser directory is missing", nil
	}
	for _, parser := range refactorParsers(set) {
		path := filepath.Join(parserRoot, parser+".so")
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, "Tree-sitter parser is missing " + parser, nil
		}
		if err != nil {
			return false, "", fmt.Errorf("inspect Tree-sitter parser %s: %w", parser, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, "", fmt.Errorf("Tree-sitter parser %s is not a regular file", parser)
		}
	}
	return true, "", nil
}

func ensureRefactorParsers(
	ctx context.Context,
	home string,
	port transport.Port,
	set pluginSet,
) error {
	parsers := refactorParsers(set)
	if len(parsers) == 0 {
		return nil
	}
	if err := ensureDirectoryBelow(home, managedTreesitterRoot(home)); err != nil {
		return fmt.Errorf("prepare managed Tree-sitter directory: %w", err)
	}
	nvim, err := managedNeovimExecutable(home)
	if err != nil {
		return err
	}
	quoted := make([]string, 0, len(parsers))
	for _, parser := range parsers {
		quoted = append(quoted, strconv.Quote(parser))
	}
	install := fmt.Sprintf(
		`lua local ok = require("nvim-treesitter").install({ %s }, { force = true, summary = true }):wait(300000); if not ok then error("managed Tree-sitter parser installation failed") end`,
		strings.Join(quoted, ", "),
	)
	result, err := port.Run(ctx, transport.Command{
		Executable:  nvim,
		Arguments:   []string{"--headless", "+" + install, "+qa"},
		Environment: managedEditorEnvironment(home),
		Timeout:     6 * time.Minute,
		OutputLimit: transport.DefaultOutputLimit,
	})
	if err != nil {
		return fmt.Errorf("install managed Tree-sitter parsers: %w", err)
	}
	if diagnostic := neovimFailureDiagnostic(result); diagnostic != "" {
		return fmt.Errorf("install managed Tree-sitter parsers: %s", diagnostic)
	}
	ready, detail, err := inspectRefactorParsers(home, set)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New(detail)
	}
	return nil
}
