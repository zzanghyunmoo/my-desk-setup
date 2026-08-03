package guest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/managedfile"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
)

const (
	lazyPluginCommit      = "306a05526ada86a7b30af95c5cc81ffba93fef97"
	editorOwnershipSchema = "mds.ownership/v1"
)

type pluginSet uint8

const (
	basePluginSet pluginSet = iota
	idePluginSet
)

type pluginPin struct {
	Name   string
	Branch string
	Commit string
	Set    pluginSet
}

var pluginPins = []pluginPin{
	{Name: "LuaSnip", Branch: "master", Commit: "0abc8f390b278c3b4aabc4c004ac8a088b65cf24"},
	{Name: "NvChad", Branch: "v2.5", Commit: "add44b952d631981614bbb8cfc6f7002f296dfe6"},
	{Name: "base46", Branch: "v3.0", Commit: "2ffb8eda35cfb3547d0d7dce30f4f2f0f674c4d7"},
	{Name: "cmp-async-path", Branch: "main", Commit: "98185a91d49ff5dd249aebf2f7456e18063fa2a0"},
	{Name: "cmp-buffer", Branch: "main", Commit: "b74fab3656eea9de20a9b8116afa3cfc4ec09657"},
	{Name: "cmp-nvim-lsp", Branch: "main", Commit: "cbc7b02bb99fae35cb42f514762b89b5126651ef"},
	{Name: "cmp-nvim-lua", Branch: "main", Commit: "e3a22cb071eb9d6508a156306b102c45cd2d573d"},
	{Name: "cmp_luasnip", Branch: "master", Commit: "98d9cb5c2c38532bd9bdb481067b20fea8f32e90"},
	{Name: "conform.nvim", Branch: "master", Commit: "619363c30309d29ffa631e67c8183f2a72caa373", Set: idePluginSet},
	{Name: "friendly-snippets", Branch: "main", Commit: "6cd7280adead7f586db6fccbd15d2cac7e2188b9"},
	{Name: "gitsigns.nvim", Branch: "main", Commit: "31d6fb2d618bca1482b9f274751ead5f03461408"},
	{Name: "indent-blankline.nvim", Branch: "master", Commit: "d28a3f70721c79e3c5f6693057ae929f3d9c0a03"},
	{Name: "mason.nvim", Branch: "main", Commit: "2a6940af80375532e5e9e7c1f2fc6319a1b7a69d"},
	{Name: "menu", Branch: "main", Commit: "7a0a4a2896b715c066cfbe320bdc048091874cc6"},
	{Name: "minty", Branch: "main", Commit: "aafc9e8e0afe6bf57580858a2849578d8d8db9e0"},
	{Name: "nvim-autopairs", Branch: "master", Commit: "7b9923abad60b903ece7c52940e1321d39eccc79"},
	{Name: "nvim-cmp", Branch: "main", Commit: "2ffe79f1f021def8dd1fcd81deb16f1bb0d989f3"},
	{Name: "nvim-dap", Branch: "master", Commit: "9e848e09a697ee95302a3ef2dd43fd6eb709e570", Set: idePluginSet},
	{Name: "nvim-dap-ui", Branch: "master", Commit: "cc9dd33aade7f20bae414d0cba163bc60d4d4b43", Set: idePluginSet},
	{Name: "nvim-dap-virtual-text", Branch: "master", Commit: "fbdb48c2ed45f4a8293d0d483f7730d24467ccb6", Set: idePluginSet},
	{Name: "nvim-lint", Branch: "master", Commit: "a219b2c9e5b4765e5c845aba119dad55806fcaf1", Set: idePluginSet},
	{Name: "nvim-lspconfig", Branch: "master", Commit: "1c0d8f70dbc8827263eedc3cf7021ceba0f68689"},
	{Name: "nvim-nio", Branch: "master", Commit: "edcc181a875301dd21840189aa2f2f9ad69fc172", Set: idePluginSet},
	{Name: "nvim-tree.lua", Branch: "master", Commit: "4213bd6eabac38b16dd6615002b6243b23cf3bf6"},
	{Name: "nvim-treesitter", Branch: "main", Commit: "7b6cc8949f9999c5ed91436cbe24aa5f99c42025"},
	{Name: "nvim-web-devicons", Branch: "master", Commit: "2ae6958df7ced50baac5035cec0c15799eedfbf7"},
	{Name: "plenary.nvim", Branch: "master", Commit: "74b06c6c75e4eeb3108ec01852001636d85a932b"},
	{Name: "telescope.nvim", Branch: "master", Commit: "427b576c16792edad01a92b89721d923c19ad60f"},
	{Name: "ui", Branch: "v3.0", Commit: "f22719026b94d02ce44c66c0059b89d927d942a0"},
	{Name: "volt", Branch: "main", Commit: "620de1321f275ec9d80028c68d1b88b409c0c8b1"},
	{Name: "which-key.nvim", Branch: "main", Commit: "3aab2147e74890957785941f0c1ad87d0a44c15a"},
}

type lazyLockEntry struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

var (
	managedLazyLock      = renderLazyLock()
	managedPluginGraphID = pluginGraphID(managedLazyLock)
	basePluginSpec       = "return {}\n"
	idePluginSpec        = `return {
  { "stevearc/conform.nvim", event = { "BufWritePre" }, opts = require "configs.conform" },
  { "neovim/nvim-lspconfig", event = { "BufReadPost", "BufNewFile" }, config = function() require "configs.lspconfig" end },
  { "mfussenegger/nvim-lint", event = { "BufReadPre", "BufNewFile" }, config = function() require "configs.lint" end },
  { "mfussenegger/nvim-dap" },
  { "rcarriga/nvim-dap-ui", event = "VeryLazy", dependencies = { "mfussenegger/nvim-dap", "nvim-neotest/nvim-nio" }, config = function() require "configs.dap" end },
  { "theHamsta/nvim-dap-virtual-text", opts = {} },
}
`
	editorConfiguration = map[string]string{
		"init.lua":             renderManagedInit(),
		"lazy-lock.json":       managedLazyLock,
		"lua/plugins/init.lua": basePluginSpec,
	}
	ideConfiguration = map[string]string{
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
dap.adapters.python = { type = "executable", command = "/usr/bin/python3", args = { "-m", "debugpy.adapter" } }
dap.configurations.python = {{ type = "python", name = "Launch file", request = "launch", program = "${file}", pythonPath = "/usr/bin/python3" }}
for _, event in ipairs({ "attach", "launch" }) do
  dap.listeners.before[event].dapui_config = function() dapui.open() end
end
dap.listeners.before.event_terminated.dapui_config = function() dapui.close() end
dap.listeners.before.event_exited.dapui_config = function() dapui.close() end
`,
		"lua/plugins/init.lua": idePluginSpec,
	}
)

func renderLazyLock() string {
	entries := make(map[string]lazyLockEntry, len(pluginPins))
	for _, pin := range pluginPins {
		entries[pin.Name] = lazyLockEntry{Branch: pin.Branch, Commit: pin.Commit}
	}
	content, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("encode managed lazy lock: %v", err))
	}
	return string(content) + "\n"
}

func pluginGraphID(lock string) string {
	digest := sha256.Sum256([]byte(lock))
	return hex.EncodeToString(digest[:16])
}

func renderManagedInit() string {
	return fmt.Sprintf(`vim.g.base46_cache = vim.fn.stdpath "data" .. "/base46/"
vim.g.mapleader = " "

local lazypath = vim.fn.expand("~/.local/share/mds/nvim/l/%s")
if not vim.uv.fs_stat(lazypath) then error("mds managed lazy.nvim checkout is missing") end
vim.opt.rtp:prepend(lazypath)

local lazy_config = require "configs.lazy"
lazy_config.root = vim.fn.expand("~/.local/share/mds/nvim/p/%s")
require("lazy").setup({
  { "NvChad/NvChad", lazy = false, import = "nvchad.plugins" },
  { import = "plugins" },
}, lazy_config)

dofile(vim.g.base46_cache .. "defaults")
dofile(vim.g.base46_cache .. "statusline")
require "options"
require "autocmds"
vim.schedule(function() require "mappings" end)
`, lazyPluginCommit[:12], managedPluginGraphID)
}

func writeEditorConfiguration(root string) error {
	return writeConfigurationFiles(root, editorConfiguration)
}

func writeIDEConfiguration(root string) error {
	return writeConfigurationFiles(root, ideConfiguration)
}

func writeConfigurationFiles(root string, files map[string]string) error {
	for relativePath, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		if err := ensureDirectoryBelow(root, filepath.Dir(path)); err != nil {
			return fmt.Errorf("create managed Neovim config directory: %w", err)
		}
		if err := writeConfigurationFileIfChanged(path, content); err != nil {
			return fmt.Errorf("write managed Neovim config %s: %w", relativePath, err)
		}
	}
	return nil
}

func writeConfigurationFileIfChanged(path, content string) error {
	inspection := managedfile.Inspect(path, content)
	switch inspection.State {
	case managedfile.StateReady:
		return nil
	case managedfile.StateConflict:
		if inspection.Conflict != managedfile.ConflictContent {
			return &managedfile.ConflictError{
				Kind: inspection.Conflict,
				Err:  inspection.Err,
			}
		}
	}
	return durable.WriteFile(path, []byte(content), 0o600)
}

var basePluginPins = func() []pluginPin {
	pins := make([]pluginPin, 0, len(pluginPins))
	for _, pin := range pluginPins {
		if pin.Set == basePluginSet {
			pins = append(pins, pin)
		}
	}
	return pins
}()

func expectedPluginPins(set pluginSet) []pluginPin {
	if set == idePluginSet {
		return pluginPins
	}
	return basePluginPins
}

func inspectEditorConfiguration(root string) (bool, bool, string, error) {
	for _, relativePath := range []string{"init.lua", "lazy-lock.json"} {
		ready, detail, err := inspectConfigurationFile(
			root,
			relativePath,
			editorConfiguration[relativePath],
		)
		if err != nil || !ready {
			return false, false, detail, err
		}
	}
	pluginPath := "lua/plugins/init.lua"
	path := filepath.Join(root, filepath.FromSlash(pluginPath))
	parentExists, err := inspectDirectoryBelow(root, filepath.Dir(path))
	if err != nil {
		return false, false, "", err
	}
	if !parentExists {
		return false, false, "managed plugin specification parent is missing", nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, "managed plugin specification is missing", nil
	}
	if err != nil {
		return false, false, "", fmt.Errorf("inspect managed plugin specification: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, false, "", errors.New("managed plugin specification is not a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, false, "", fmt.Errorf("read managed plugin specification: %w", err)
	}
	switch {
	case bytes.Equal(content, []byte(basePluginSpec)):
		return false, true, "", nil
	case bytes.Equal(content, []byte(idePluginSpec)):
		return true, true, "", nil
	default:
		return false, false, "managed plugin specification differs", nil
	}
}

func inspectConfigurationFile(
	root,
	relativePath,
	expected string,
) (bool, string, error) {
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	parentExists, err := inspectDirectoryBelow(root, filepath.Dir(path))
	if err != nil {
		return false, "", err
	}
	if !parentExists {
		return false, "managed configuration parent is missing " + relativePath, nil
	}
	inspection := managedfile.Inspect(path, expected)
	switch inspection.State {
	case managedfile.StateReady:
		return true, "", nil
	case managedfile.StateMissing:
		return false, "managed configuration is missing " + relativePath, nil
	case managedfile.StateConflict:
		switch inspection.Conflict {
		case managedfile.ConflictContent:
			return false, "managed configuration differs at " + relativePath, nil
		case managedfile.ConflictNonRegular:
			return false, "", fmt.Errorf(
				"managed configuration %s is not a regular file",
				relativePath,
			)
		case managedfile.ConflictRead:
			return false, "", fmt.Errorf(
				"read managed configuration %s: %w",
				relativePath,
				inspection.Err,
			)
		default:
			return false, "", fmt.Errorf(
				"inspect managed configuration %s: %w",
				relativePath,
				inspection.Err,
			)
		}
	default:
		return false, "", fmt.Errorf("unknown managed configuration state for %s", relativePath)
	}
}
