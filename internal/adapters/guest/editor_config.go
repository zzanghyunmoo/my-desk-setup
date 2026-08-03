package guest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
)

var ideConfiguration = map[string]string{
	"init.lua": `vim.g.base46_cache = vim.fn.stdpath "data" .. "/base46/"
vim.g.mapleader = " "

local lazypath = vim.fn.stdpath "data" .. "/lazy/lazy.nvim"
local lazy_commit = "306a05526ada86a7b30af95c5cc81ffba93fef97"

local function checked_system(argv)
  local output = vim.fn.system(argv)
  if vim.v.shell_error ~= 0 then error(table.concat(argv, " ") .. ": " .. output) end
end

if not vim.uv.fs_stat(lazypath) then
  checked_system { "git", "clone", "--filter=blob:none", "--no-checkout", "https://github.com/folke/lazy.nvim.git", lazypath }
end
vim.fn.system { "git", "-C", lazypath, "cat-file", "-e", lazy_commit .. "^{commit}" }
if vim.v.shell_error ~= 0 then
  checked_system { "git", "-C", lazypath, "fetch", "--depth", "1", "origin", lazy_commit }
end
checked_system { "git", "-C", lazypath, "checkout", "--detach", lazy_commit }
vim.opt.rtp:prepend(lazypath)

local lazy_config = require "configs.lazy"
require("lazy").setup({
  {
    "NvChad/NvChad",
    lazy = false,
    commit = "add44b952d631981614bbb8cfc6f7002f296dfe6",
    import = "nvchad.plugins",
  },
  { import = "plugins" },
}, lazy_config)

dofile(vim.g.base46_cache .. "defaults")
dofile(vim.g.base46_cache .. "statusline")
require "options"
require "autocmds"
vim.schedule(function() require "mappings" end)
`,
	"lazy-lock.json": `{
  "LuaSnip": { "branch": "master", "commit": "0abc8f390b278c3b4aabc4c004ac8a088b65cf24" },
  "NvChad": { "branch": "v2.5", "commit": "add44b952d631981614bbb8cfc6f7002f296dfe6" },
  "base46": { "branch": "v3.0", "commit": "2ffb8eda35cfb3547d0d7dce30f4f2f0f674c4d7" },
  "cmp-async-path": { "branch": "main", "commit": "98185a91d49ff5dd249aebf2f7456e18063fa2a0" },
  "cmp-buffer": { "branch": "main", "commit": "b74fab3656eea9de20a9b8116afa3cfc4ec09657" },
  "cmp-nvim-lsp": { "branch": "main", "commit": "cbc7b02bb99fae35cb42f514762b89b5126651ef" },
  "cmp-nvim-lua": { "branch": "main", "commit": "e3a22cb071eb9d6508a156306b102c45cd2d573d" },
  "cmp_luasnip": { "branch": "master", "commit": "98d9cb5c2c38532bd9bdb481067b20fea8f32e90" },
  "conform.nvim": { "branch": "master", "commit": "619363c30309d29ffa631e67c8183f2a72caa373" },
  "friendly-snippets": { "branch": "main", "commit": "6cd7280adead7f586db6fccbd15d2cac7e2188b9" },
  "gitsigns.nvim": { "branch": "main", "commit": "31d6fb2d618bca1482b9f274751ead5f03461408" },
  "indent-blankline.nvim": { "branch": "master", "commit": "d28a3f70721c79e3c5f6693057ae929f3d9c0a03" },
  "lazy.nvim": { "branch": "main", "commit": "306a05526ada86a7b30af95c5cc81ffba93fef97" },
  "mason.nvim": { "branch": "main", "commit": "2a6940af80375532e5e9e7c1f2fc6319a1b7a69d" },
  "menu": { "branch": "main", "commit": "7a0a4a2896b715c066cfbe320bdc048091874cc6" },
  "minty": { "branch": "main", "commit": "aafc9e8e0afe6bf57580858a2849578d8d8db9e0" },
  "nvim-autopairs": { "branch": "master", "commit": "7b9923abad60b903ece7c52940e1321d39eccc79" },
  "nvim-cmp": { "branch": "main", "commit": "2ffe79f1f021def8dd1fcd81deb16f1bb0d989f3" },
  "nvim-dap": { "branch": "master", "commit": "9e848e09a697ee95302a3ef2dd43fd6eb709e570" },
  "nvim-dap-ui": { "branch": "master", "commit": "cc9dd33aade7f20bae414d0cba163bc60d4d4b43" },
  "nvim-dap-virtual-text": { "branch": "master", "commit": "fbdb48c2ed45f4a8293d0d483f7730d24467ccb6" },
  "nvim-lint": { "branch": "master", "commit": "a219b2c9e5b4765e5c845aba119dad55806fcaf1" },
  "nvim-lspconfig": { "branch": "master", "commit": "1c0d8f70dbc8827263eedc3cf7021ceba0f68689" },
  "nvim-nio": { "branch": "master", "commit": "edcc181a875301dd21840189aa2f2f9ad69fc172" },
  "nvim-tree.lua": { "branch": "master", "commit": "4213bd6eabac38b16dd6615002b6243b23cf3bf6" },
  "nvim-treesitter": { "branch": "main", "commit": "7b6cc8949f9999c5ed91436cbe24aa5f99c42025" },
  "nvim-web-devicons": { "branch": "master", "commit": "2ae6958df7ced50baac5035cec0c15799eedfbf7" },
  "plenary.nvim": { "branch": "master", "commit": "74b06c6c75e4eeb3108ec01852001636d85a932b" },
  "telescope.nvim": { "branch": "master", "commit": "427b576c16792edad01a92b89721d923c19ad60f" },
  "ui": { "branch": "v3.0", "commit": "f22719026b94d02ce44c66c0059b89d927d942a0" },
  "volt": { "branch": "main", "commit": "620de1321f275ec9d80028c68d1b88b409c0c8b1" },
  "which-key.nvim": { "branch": "main", "commit": "3aab2147e74890957785941f0c1ad87d0a44c15a" }
}
`,
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
  { "stevearc/conform.nvim", commit = "619363c30309d29ffa631e67c8183f2a72caa373", event = { "BufWritePre" }, opts = require "configs.conform" },
  { "neovim/nvim-lspconfig", commit = "1c0d8f70dbc8827263eedc3cf7021ceba0f68689", event = { "BufReadPost", "BufNewFile" }, config = function() require "configs.lspconfig" end },
  { "mfussenegger/nvim-lint", commit = "a219b2c9e5b4765e5c845aba119dad55806fcaf1", event = { "BufReadPre", "BufNewFile" }, config = function() require "configs.lint" end },
  { "mfussenegger/nvim-dap", commit = "9e848e09a697ee95302a3ef2dd43fd6eb709e570" },
  { "rcarriga/nvim-dap-ui", commit = "cc9dd33aade7f20bae414d0cba163bc60d4d4b43", event = "VeryLazy", dependencies = { { "mfussenegger/nvim-dap", commit = "9e848e09a697ee95302a3ef2dd43fd6eb709e570" }, { "nvim-neotest/nvim-nio", commit = "edcc181a875301dd21840189aa2f2f9ad69fc172" } }, config = function() require "configs.dap" end },
  { "theHamsta/nvim-dap-virtual-text", commit = "fbdb48c2ed45f4a8293d0d483f7730d24467ccb6", opts = {} },
}
`,
}

func writeIDEConfiguration(root string) error {
	for relativePath, content := range ideConfiguration {
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create managed Neovim config directory: %w", err)
		}
		if err := durable.WriteFile(path, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write managed Neovim config %s: %w", relativePath, err)
		}
	}
	return nil
}
