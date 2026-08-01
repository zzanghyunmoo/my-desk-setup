package guest

import (
	"fmt"
	"os"
	"path/filepath"
)

var ideConfiguration = map[string]string{
	"lua/configs/lspconfig.lua": `require("nvchad.configs.lspconfig").defaults()

local servers = {
  clangd = { cmd = { "clangd", "--background-index", "--clang-tidy" } },
  gopls = { settings = { gopls = { staticcheck = true } } },
  pyright = {
    cmd = { vim.fn.expand("~/.local/share/bun/bin/pyright-langserver"), "--stdio" },
    settings = { python = { analysis = { typeCheckingMode = "standard" } } },
  },
}

for name, config in pairs(servers) do
  vim.lsp.config(name, config)
  vim.lsp.enable(name)
end
`,
	"lua/configs/conform.lua": `return {
  formatters_by_ft = {
    c = { "clang_format" },
    cpp = { "clang_format" },
    go = { "gofmt" },
    python = { "ruff_format" },
  },
  format_on_save = { timeout_ms = 2000, lsp_fallback = true },
}
`,
	"lua/configs/lint.lua": `local lint = require "lint"

lint.linters_by_ft = {
  c = { "clangtidy" },
  cpp = { "clangtidy" },
  python = { "ruff" },
}

vim.api.nvim_create_autocmd({ "BufEnter", "BufWritePost", "InsertLeave" }, {
  callback = function() lint.try_lint() end,
})
`,
	"lua/configs/dap.lua": `local dap = require "dap"
local dapui = require "dapui"

dapui.setup()
dap.adapters.lldb = { type = "executable", command = "lldb-dap", name = "lldb" }
dap.configurations.cpp = {{
  name = "Launch executable", type = "lldb", request = "launch",
  program = function() return vim.fn.input("Executable: ", vim.fn.getcwd() .. "/", "file") end,
  cwd = "${workspaceFolder}",
}}
dap.configurations.c = dap.configurations.cpp
	dap.adapters.delve = { type = "server", port = "${port}", executable = { command = "dlv", args = { "dap", "-l", "127.0.0.1:${port}" } } }
dap.configurations.go = {{ type = "delve", name = "Debug file", request = "launch", program = "${file}" }}
dap.adapters.python = { type = "executable", command = "python3", args = { "-m", "debugpy.adapter" } }
dap.configurations.python = {{ type = "python", name = "Launch file", request = "launch", program = "${file}", pythonPath = "python3" }}
for _, event in ipairs({ "attach", "launch" }) do
  dap.listeners.before[event].dapui_config = function() dapui.open() end
end
dap.listeners.before.event_terminated.dapui_config = function() dapui.close() end
dap.listeners.before.event_exited.dapui_config = function() dapui.close() end
`,
	"lua/plugins/init.lua": `return {
  { "stevearc/conform.nvim", event = { "BufWritePre" }, opts = require "configs.conform" },
  { "neovim/nvim-lspconfig", event = { "BufReadPost", "BufNewFile" }, config = function() require "configs.lspconfig" end },
  { "mfussenegger/nvim-lint", event = { "BufReadPre", "BufNewFile" }, config = function() require "configs.lint" end },
  { "mfussenegger/nvim-dap" },
  { "rcarriga/nvim-dap-ui", event = "VeryLazy", dependencies = { "mfussenegger/nvim-dap", "nvim-neotest/nvim-nio" }, config = function() require "configs.dap" end },
  { "theHamsta/nvim-dap-virtual-text", opts = {} },
}
`,
}

func writeIDEConfiguration(root string) error {
	for relativePath, content := range ideConfiguration {
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create managed Neovim config directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write managed Neovim config %s: %w", relativePath, err)
		}
	}
	return nil
}
