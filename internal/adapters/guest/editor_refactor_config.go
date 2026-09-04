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
  rust = { variable = "lsp", ["function"] = "lsp" },
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

function M.extract(kind)
  local filetype = vim.bo.filetype
  local route = routes[filetype] and routes[filetype][kind]
  if not route then
    local language = language_names[filetype] or filetype
    notify(string.format("%s에서는 Extract %s를 지원하지 않습니다", language, labels[kind] or kind))
    return "<Ignore>"
  end
  if route == "lsp" then return lsp_extract(kind) end
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
