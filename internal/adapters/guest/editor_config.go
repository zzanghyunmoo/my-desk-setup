package guest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/managedfile"
	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

const (
	lazyPluginCommit      = "306a05526ada86a7b30af95c5cc81ffba93fef97"
	editorOwnershipSchema = "mds.ownership/v1"
)

type pluginSet uint8

const (
	basePluginSet pluginSet = 0
	idePluginSet  pluginSet = 1 << iota
	jvmPluginSet
	dotnetPluginSet
	allIDEPluginSets = idePluginSet | jvmPluginSet | dotnetPluginSet
)

type pluginPin struct {
	Name       string
	Repository string
	Branch     string
	Commit     string
	Set        pluginSet
}

var pluginPins = []pluginPin{
	{Name: "LuaSnip", Repository: "https://github.com/L3MON4D3/LuaSnip.git", Branch: "master", Commit: "0abc8f390b278c3b4aabc4c004ac8a088b65cf24"},
	{Name: "NvChad", Repository: "https://github.com/NvChad/NvChad.git", Branch: "v2.5", Commit: "add44b952d631981614bbb8cfc6f7002f296dfe6"},
	{Name: "base46", Repository: "https://github.com/nvchad/base46.git", Branch: "v3.0", Commit: "2ffb8eda35cfb3547d0d7dce30f4f2f0f674c4d7"},
	{Name: "cmp-async-path", Repository: "https://codeberg.org/FelipeLema/cmp-async-path.git", Branch: "main", Commit: "98185a91d49ff5dd249aebf2f7456e18063fa2a0"},
	{Name: "cmp-buffer", Repository: "https://github.com/hrsh7th/cmp-buffer.git", Branch: "main", Commit: "b74fab3656eea9de20a9b8116afa3cfc4ec09657"},
	{Name: "cmp-nvim-lsp", Repository: "https://github.com/hrsh7th/cmp-nvim-lsp.git", Branch: "main", Commit: "cbc7b02bb99fae35cb42f514762b89b5126651ef"},
	{Name: "cmp-nvim-lua", Repository: "https://github.com/hrsh7th/cmp-nvim-lua.git", Branch: "main", Commit: "e3a22cb071eb9d6508a156306b102c45cd2d573d"},
	{Name: "cmp_luasnip", Repository: "https://github.com/saadparwaiz1/cmp_luasnip.git", Branch: "master", Commit: "98d9cb5c2c38532bd9bdb481067b20fea8f32e90"},
	{Name: "conform.nvim", Repository: "https://github.com/stevearc/conform.nvim.git", Branch: "master", Commit: "619363c30309d29ffa631e67c8183f2a72caa373", Set: idePluginSet},
	{Name: "friendly-snippets", Repository: "https://github.com/rafamadriz/friendly-snippets.git", Branch: "main", Commit: "6cd7280adead7f586db6fccbd15d2cac7e2188b9"},
	{Name: "gitsigns.nvim", Repository: "https://github.com/lewis6991/gitsigns.nvim.git", Branch: "main", Commit: "31d6fb2d618bca1482b9f274751ead5f03461408"},
	{Name: "indent-blankline.nvim", Repository: "https://github.com/lukas-reineke/indent-blankline.nvim.git", Branch: "master", Commit: "d28a3f70721c79e3c5f6693057ae929f3d9c0a03"},
	{Name: "mason.nvim", Repository: "https://github.com/mason-org/mason.nvim.git", Branch: "main", Commit: "2a6940af80375532e5e9e7c1f2fc6319a1b7a69d"},
	{Name: "menu", Repository: "https://github.com/nvzone/menu.git", Branch: "main", Commit: "7a0a4a2896b715c066cfbe320bdc048091874cc6"},
	{Name: "minty", Repository: "https://github.com/nvzone/minty.git", Branch: "main", Commit: "aafc9e8e0afe6bf57580858a2849578d8d8db9e0"},
	{Name: "nvim-autopairs", Repository: "https://github.com/windwp/nvim-autopairs.git", Branch: "master", Commit: "7b9923abad60b903ece7c52940e1321d39eccc79"},
	{Name: "nvim-cmp", Repository: "https://github.com/hrsh7th/nvim-cmp.git", Branch: "main", Commit: "2ffe79f1f021def8dd1fcd81deb16f1bb0d989f3"},
	{Name: "nvim-dap", Repository: "https://github.com/mfussenegger/nvim-dap.git", Branch: "master", Commit: "9e848e09a697ee95302a3ef2dd43fd6eb709e570", Set: allIDEPluginSets},
	{Name: "nvim-dap-ui", Repository: "https://github.com/rcarriga/nvim-dap-ui.git", Branch: "master", Commit: "cc9dd33aade7f20bae414d0cba163bc60d4d4b43", Set: allIDEPluginSets},
	{Name: "nvim-dap-virtual-text", Repository: "https://github.com/theHamsta/nvim-dap-virtual-text.git", Branch: "master", Commit: "fbdb48c2ed45f4a8293d0d483f7730d24467ccb6", Set: allIDEPluginSets},
	{Name: "nvim-jdtls", Repository: "https://github.com/mfussenegger/nvim-jdtls.git", Branch: "master", Commit: "6e9d953f0b82bccdb834cfde0e893f3119c22592", Set: jvmPluginSet},
	{Name: "nvim-lint", Repository: "https://github.com/mfussenegger/nvim-lint.git", Branch: "master", Commit: "a219b2c9e5b4765e5c845aba119dad55806fcaf1", Set: idePluginSet},
	{Name: "nvim-lspconfig", Repository: "https://github.com/neovim/nvim-lspconfig.git", Branch: "master", Commit: "1c0d8f70dbc8827263eedc3cf7021ceba0f68689"},
	{Name: "nvim-nio", Repository: "https://github.com/nvim-neotest/nvim-nio.git", Branch: "master", Commit: "edcc181a875301dd21840189aa2f2f9ad69fc172", Set: allIDEPluginSets},
	{Name: "nvim-tree.lua", Repository: "https://github.com/nvim-tree/nvim-tree.lua.git", Branch: "master", Commit: "4213bd6eabac38b16dd6615002b6243b23cf3bf6"},
	{Name: "nvim-treesitter", Repository: "https://github.com/nvim-treesitter/nvim-treesitter.git", Branch: "main", Commit: "7b6cc8949f9999c5ed91436cbe24aa5f99c42025"},
	{Name: "nvim-web-devicons", Repository: "https://github.com/nvim-tree/nvim-web-devicons.git", Branch: "master", Commit: "2ae6958df7ced50baac5035cec0c15799eedfbf7"},
	{Name: "plenary.nvim", Repository: "https://github.com/nvim-lua/plenary.nvim.git", Branch: "master", Commit: "74b06c6c75e4eeb3108ec01852001636d85a932b"},
	{Name: "roslyn.nvim", Repository: "https://github.com/seblyng/roslyn.nvim.git", Branch: "main", Commit: "de9a98d61ed3fd01b5016eea5fe9e32f1a4c7cfb", Set: dotnetPluginSet},
	{Name: "telescope.nvim", Repository: "https://github.com/nvim-telescope/telescope.nvim.git", Branch: "master", Commit: "427b576c16792edad01a92b89721d923c19ad60f"},
	{Name: "ui", Repository: "https://github.com/nvchad/ui.git", Branch: "v3.0", Commit: "f22719026b94d02ce44c66c0059b89d927d942a0"},
	{Name: "volt", Repository: "https://github.com/nvzone/volt.git", Branch: "main", Commit: "620de1321f275ec9d80028c68d1b88b409c0c8b1"},
	{Name: "which-key.nvim", Repository: "https://github.com/folke/which-key.nvim.git", Branch: "main", Commit: "3aab2147e74890957785941f0c1ad87d0a44c15a"},
}

type lazyLockEntry struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

type runtimeTreeReference = planning.RuntimeTreeReference

var (
	managedLazyLock      = renderLazyLock()
	managedPluginSources = renderPluginSources()
	managedPluginGraphID = pluginGraphID(managedLazyLock + "\n" + managedPluginSources)
	basePluginSpec       = "return {}\n"
	idePluginSpec        = renderPluginSpec(idePluginSet)
	editorConfiguration  = map[string]string{
		"init.lua":                renderManagedInit(),
		"lazy-lock.json":          managedLazyLock,
		"lua/configs/startup.lua": renderStartupConfig(),
		"lua/plugins/init.lua":    basePluginSpec,
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

func pluginSetForAction(action planning.Action) (pluginSet, error) {
	value := action.Inputs[planning.EditorSlicesInput]
	if value == "" {
		switch action.ComponentID {
		case "nvim-ide-tools":
			return idePluginSet, nil
		case "nvim-jvm":
			return jvmPluginSet, nil
		case "nvim-dotnet":
			return dotnetPluginSet, nil
		default:
			return basePluginSet, nil
		}
	}
	if value == "core" {
		return basePluginSet, nil
	}
	var set pluginSet
	seen := make(map[string]bool)
	parts := strings.Split(value, ",")
	if !sort.StringsAreSorted(parts) {
		return basePluginSet, errors.New("editor slices are not normalized")
	}
	for _, part := range parts {
		if part == "" || seen[part] {
			return basePluginSet, errors.New("editor slices contain an empty or duplicate value")
		}
		seen[part] = true
		switch part {
		case "legacy":
			set |= idePluginSet
		case "jvm":
			set |= jvmPluginSet
		case "dotnet":
			set |= dotnetPluginSet
		default:
			return basePluginSet, fmt.Errorf("unknown editor slice %q", part)
		}
	}
	return set, nil
}

func runtimeTreeReferences(action planning.Action) (map[string]runtimeTreeReference, error) {
	result := make(map[string]runtimeTreeReference)
	for key, value := range action.Inputs {
		if !strings.HasPrefix(key, planning.RuntimeTreeInputPrefix) {
			continue
		}
		componentID := strings.TrimPrefix(key, planning.RuntimeTreeInputPrefix)
		var reference runtimeTreeReference
		if json.Unmarshal([]byte(value), &reference) != nil ||
			reference.ComponentID != componentID ||
			artifact.ValidateSHA256(reference.ArchiveSHA256) != nil ||
			artifact.ValidateSHA256(reference.ManifestSHA256) != nil ||
			artifact.ValidateSHA256(reference.LauncherSHA256) != nil ||
			reference.Executable == "" || filepath.IsAbs(reference.Executable) ||
			strings.Contains(reference.Executable, `\`) {
			return nil, fmt.Errorf("invalid runtime tree identity for %s", componentID)
		}
		cleaned := filepath.ToSlash(filepath.Clean(reference.Executable))
		if cleaned != reference.Executable || cleaned == "." || strings.HasPrefix(cleaned, "../") {
			return nil, fmt.Errorf("invalid runtime tree launcher for %s", componentID)
		}
		if !slices.Contains(reference.RequiredPaths, reference.Executable) {
			return nil, fmt.Errorf("runtime tree identity for %s omits launcher", componentID)
		}
		result[componentID] = reference
	}
	return result, nil
}

func requireRuntimeTreeReferences(set pluginSet, references map[string]runtimeTreeReference) error {
	required := make([]string, 0, 8)
	if set&jvmPluginSet != 0 {
		required = append(required,
			"java-debug-server", "java-test-server", "jdt-language-server",
			"kotlin-debug-adapter", "kotlin-language-server", "spring-tools-language-server",
		)
	}
	if set&dotnetPluginSet != 0 {
		required = append(required,
			"netcoredbg", "roslyn-language-server",
		)
	}
	sort.Strings(required)
	for _, componentID := range required {
		if references[componentID].ComponentID == "" {
			return fmt.Errorf("missing exact runtime tree identity for %s", componentID)
		}
	}
	return nil
}

func renderPluginSpec(set pluginSet) string {
	if set == basePluginSet {
		return basePluginSpec
	}
	lines := []string{
		`  { "neovim/nvim-lspconfig", event = { "VimEnter", "BufReadPost", "BufNewFile" }, config = function() require "configs.lspconfig" end },`,
		`  { "mfussenegger/nvim-dap" },`,
		`  { "rcarriga/nvim-dap-ui", event = "VeryLazy", dependencies = { "mfussenegger/nvim-dap", "nvim-neotest/nvim-nio" }, config = function() require "configs.dap" end },`,
		`  { "theHamsta/nvim-dap-virtual-text", opts = {} },`,
	}
	if set&idePluginSet != 0 {
		lines = append(lines,
			`  { "stevearc/conform.nvim", event = { "BufWritePre" }, opts = require "configs.conform" },`,
			`  { "mfussenegger/nvim-lint", event = { "BufReadPre", "BufNewFile" }, config = function() require "configs.lint" end },`,
		)
	}
	if set&jvmPluginSet != 0 {
		lines = append(lines, `  { "mfussenegger/nvim-jdtls", ft = { "java", "kotlin" }, config = function() require("configs.jvm").setup() end },`)
	}
	if set&dotnetPluginSet != 0 {
		lines = append(lines, `  { "seblyng/roslyn.nvim", event = { "BufReadPre", "BufNewFile" },
    init = function()
      local mise = vim.fn.expand "~/.local/bin/mise"
      local result = vim.system({ mise, "which", "dotnet" }, { text = true }):wait()
      local dotnet = result.code == 0 and vim.trim(result.stdout or "") or ""
      local stat = dotnet ~= "" and vim.uv.fs_stat(dotnet) or nil
      if not stat or stat.type ~= "file" then error("managed .NET runtime root is unavailable") end
      vim.env.DOTNET_ROOT = vim.fs.dirname(dotnet)
    end,
    config = function() require("configs.dotnet").setup() end,
  },`)
	}
	return "return {\n" + strings.Join(lines, "\n") + "\n}\n"
}

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

func renderPluginSources() string {
	type source struct {
		Name       string `json:"name"`
		Repository string `json:"repository"`
	}
	sources := make([]source, 0, len(pluginPins))
	for _, pin := range pluginPins {
		sources = append(sources, source{Name: pin.Name, Repository: pin.Repository})
	}
	content, err := json.MarshalIndent(sources, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("encode managed plugin sources: %v", err))
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
require "configs.startup"
`, lazyPluginCommit[:12], managedPluginGraphID)
}

func renderStartupConfig() string {
	return `local M = {}

vim.api.nvim_create_autocmd({ "BufReadPost", "BufNewFile" }, {
  group = vim.api.nvim_create_augroup("mds-directory-filetype", { clear = true }),
  callback = function(event)
    if vim.bo[event.buf].filetype ~= "" then return end
    local filename = event.file
    if not filename or filename == "" then filename = vim.api.nvim_buf_get_name(event.buf) end
    local detected_filetype = vim.filetype.match { filename = filename }
    if not detected_filetype then return end

    vim.schedule(function()
      if not vim.api.nvim_buf_is_valid(event.buf) or
          not vim.api.nvim_buf_is_loaded(event.buf) or
          vim.api.nvim_buf_get_name(event.buf) ~= filename or
          vim.bo[event.buf].filetype ~= "" then
        return
      end
      vim.api.nvim_buf_call(event.buf, function()
        vim.api.nvim_cmd({ cmd = "setfiletype", args = { detected_filetype } }, {})
      end)
    end)
  end,
})

vim.api.nvim_create_autocmd("VimEnter", {
  once = true,
  callback = function()
    if #vim.api.nvim_list_uis() == 0 then return end

    local argument_count = vim.fn.argc()
    if argument_count > 1 then return end

    local project_directory
    if argument_count == 0 then
      if vim.api.nvim_buf_get_name(0) ~= "" then return end
      project_directory = vim.fn.getcwd()
    else
      local argument = vim.fn.argv(0)
      if vim.fn.isdirectory(argument) ~= 1 then return end
      project_directory = vim.fn.fnamemodify(argument, ":p")
    end

    vim.api.nvim_set_current_dir(project_directory)
    vim.schedule(function() vim.cmd "NvimTreeFocus" end)
  end,
})

return M
`
}

func writeEditorConfiguration(root string) error {
	return writeConfigurationFiles(root, editorConfiguration)
}

func configurationForPluginSet(set pluginSet) map[string]string {
	files := maps.Clone(editorConfiguration)
	if set == basePluginSet {
		return files
	}
	files["lua/plugins/init.lua"] = renderPluginSpec(set)
	files["lua/configs/lspconfig.lua"] = renderSliceLSPConfig(set, nil)
	files["lua/configs/dap.lua"] = ideConfiguration["lua/configs/dap.lua"]
	if set&idePluginSet != 0 {
		files["lua/configs/conform.lua"] = ideConfiguration["lua/configs/conform.lua"]
		files["lua/configs/lint.lua"] = ideConfiguration["lua/configs/lint.lua"]
	}
	return files
}

func configurationForAction(action planning.Action) (map[string]string, pluginSet, error) {
	set, err := pluginSetForAction(action)
	if err != nil {
		return nil, basePluginSet, err
	}
	references, err := runtimeTreeReferences(action)
	if err != nil {
		return nil, basePluginSet, err
	}
	if err := requireRuntimeTreeReferences(set, references); err != nil {
		return nil, basePluginSet, err
	}
	files := configurationForPluginSet(set)
	if set != basePluginSet {
		files["lua/configs/lspconfig.lua"] = renderSliceLSPConfig(set, references)
		files["lua/configs/trust.lua"] = workspaceTrustLua
	}
	if set&(jvmPluginSet|dotnetPluginSet) != 0 {
		files["lua/configs/actions.lua"] = renderProjectActions(set)
	}
	if set&jvmPluginSet != 0 {
		files["lua/configs/jvm.lua"] = renderJVMConfig(references)
	}
	if set&dotnetPluginSet != 0 {
		files["lua/configs/dotnet.lua"] = renderDotNetConfig(references)
	}
	return files, set, nil
}

func renderSliceLSPConfig(set pluginSet, references map[string]runtimeTreeReference) string {
	var blocks []string
	if set&idePluginSet != 0 {
		blocks = append(blocks, `  clangd = { cmd = { "clangd", "--background-index", "--clang-tidy" } },
  gopls = { settings = { gopls = { staticcheck = true } } },
  pyright = {
    cmd = { vim.fn.expand("~/.local/share/bun/bin/pyright-langserver"), "--stdio" },
    settings = { python = { analysis = { typeCheckingMode = "standard" } } },
  },`)
	}
	if set&jvmPluginSet != 0 {
		blocks = append(blocks, fmt.Sprintf(`  kotlin_lsp = { cmd = { %s, "--stdio" }, filetypes = { "kotlin" }, root_dir = trusted_root },
  spring_boot = {
    cmd = { trust.managed_executable("java"), "-Dsts.lsp.client=vscode", "-jar", %s },
    filetypes = { "spring-boot-properties", "spring-boot-properties-yaml" },
    root_dir = trusted_root,
	init_options = { enableJdtClasspath = false },
	capabilities = vim.tbl_deep_extend("force", vim.lsp.protocol.make_client_capabilities(), {
	  workspace = { executeCommand = { dynamicRegistration = false } },
	}),
  },`,
			runtimeLuaExpression(references["kotlin-language-server"], "mds-kotlin-lsp"),
			runtimeLuaExpression(references["spring-tools-language-server"], "mds-spring-boot-ls"),
		))
	}
	if set&dotnetPluginSet != 0 {
		blocks = append(blocks, `  html = {
    cmd = { trust.managed_launcher("vscode-html-language-server"), "--stdio" },
    filetypes = { "html", "razor" },
    root_dir = trusted_root,
  },`)
	}
	trust := "local trusted_root = nil\n"
	if set&(jvmPluginSet|dotnetPluginSet) != 0 {
		trust = `local trust = require "configs.trust"
local function trusted_root(bufnr, callback)
  local root = trust.nearest(bufnr)
  callback(root and trust.is_trusted(root) and root or nil)
end
require("configs.actions").setup()
`
		if set&jvmPluginSet != 0 {
			trust += `vim.filetype.add { pattern = {
  [".*/application.*%.properties"] = "spring-boot-properties",
  [".*/bootstrap.*%.properties"] = "spring-boot-properties",
  [".*/application.*%.ya?ml"] = "spring-boot-properties-yaml",
  [".*/bootstrap.*%.ya?ml"] = "spring-boot-properties-yaml",
} }
`
		}
		if set&dotnetPluginSet != 0 {
			trust += `vim.filetype.add { extension = { razor = "razor", cshtml = "razor" } }
`
		}
	}
	return `require("nvchad.configs.lspconfig").defaults()

` + trust + `

local servers = {
` + strings.Join(blocks, "\n") + `
}

for name, config in pairs(servers) do
  vim.lsp.config(name, config)
  vim.lsp.enable(name)
end
`
}

func runtimeLuaExpression(reference runtimeTreeReference, fallback string) string {
	return runtimeLuaPathExpression(reference, reference.Executable, fallback)
}

func runtimeLuaPathExpression(
	reference runtimeTreeReference,
	executable,
	fallback string,
) string {
	if reference.ComponentID == "" {
		return fmt.Sprintf("%q", fallback)
	}
	identity := reference.ManifestSHA256 + "-" + reference.ArchiveSHA256
	return fmt.Sprintf(
		`require("configs.trust").runtime(%q, %q, %q)`,
		reference.ComponentID,
		identity,
		filepath.ToSlash(executable),
	)
}

func writeActionConfiguration(root string, action planning.Action) error {
	files, _, err := configurationForAction(action)
	if err != nil {
		return err
	}
	return writeConfigurationFiles(root, files)
}

func inspectActionConfiguration(home string, action planning.Action) (bool, string, error) {
	files, _, err := configurationForAction(action)
	if err != nil {
		return false, "", err
	}
	root, err := managedEditorRoot(home)
	if errors.Is(err, os.ErrNotExist) {
		return false, "managed NvChad starter is missing", nil
	}
	if err != nil {
		return false, "", err
	}
	for relativePath, expected := range files {
		ready, detail, err := inspectConfigurationFile(root, relativePath, expected)
		if err != nil || !ready {
			return false, detail, err
		}
	}
	return true, "", nil
}

func repairEditorConfiguration(root string) error {
	includeIDE, ready, _, err := inspectPluginSpecification(root)
	if err != nil {
		return err
	}
	if !ready || !includeIDE {
		return writeEditorConfiguration(root)
	}
	files := make(map[string]string, len(editorConfiguration)-1)
	for relativePath, content := range editorConfiguration {
		if relativePath != "lua/plugins/init.lua" {
			files[relativePath] = content
		}
	}
	return writeConfigurationFiles(root, files)
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

func expectedPluginPins(set pluginSet) []pluginPin {
	result := make([]pluginPin, 0, len(pluginPins))
	for _, pin := range pluginPins {
		if pin.Set == basePluginSet || pin.Set&set != 0 {
			result = append(result, pin)
		}
	}
	return result
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
	includeIDE, ready, detail, err := inspectPluginSpecification(root)
	if err != nil || !ready {
		return false, false, detail, err
	}
	return includeIDE, true, "", nil
}

func inspectPluginSpecification(root string) (bool, bool, string, error) {
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
