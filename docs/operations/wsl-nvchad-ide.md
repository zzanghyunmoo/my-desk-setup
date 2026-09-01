# WSL NvChad IDE

Create and inspect the reproducible C++, Rust, Go, and Python environment from WSL:

```sh
mds plan --target wsl-guest:Ubuntu-26.04 --profile nvim-ide
mds apply --target wsl-guest:Ubuntu-26.04 --profile nvim-ide --plan-digest <digest>
mds doctor --target wsl-guest:Ubuntu-26.04 --profile nvim-ide
```

`nvim-ide` installs pinned Neovim, the NvChad starter, lazy.nvim and the complete plugin lock, Rust, Go, Python, Bun/Pyright, the pinned rust-analyzer release, and the WSL packages for clangd, clang-format, clang-tidy, lldb, gopls, delve, Ruff, and debugpy. It does not initiate any login flow. Selecting `nvchad` without `nvim-ide-tools` installs only the starter and does not publish language-tool configuration.

When NvimTree is open, the managed LSP symbol panel opens below it and shows the current file's classes, structs, interfaces, constructors, functions, methods, fields, properties, constants, and variables. Press `<CR>` or `o` to jump to a declaration, `q` to close the panel, or use `:MdsSymbolsToggle` and `:MdsSymbolsRefresh`. The panel uses each language server's `textDocument/documentSymbol` response and does not require a ctags index.

`doctor` is ready only when the managed NvChad configuration, the exact pinned plugin directory set, every plugin checkout revision, and the C++, Rust, Go, and Python language, formatter, lint, and debugger probes all pass. A non-ready result means the target is not certified; preserve the diagnostic, rerun `plan` and the reviewed-digest `apply`, then run `doctor` again. Repository tests and headless Neovim checks do not certify a real WSL target; after changing the managed editor or language-server graph, confirm it with a clean WSL `apply` and `doctor` run.

If `~/.config/nvim` is yours already, normal apply intentionally refuses to replace it. Review the plan and then use:

```sh
mds apply --target wsl-guest:Ubuntu-26.04 --profile nvim-ide --adopt-nvchad --plan-digest <digest>
```

The old directory is retained beside the config directory as `.nvim-mds-backup-<UTC timestamp>-<unique suffix>` before mds writes its managed configuration. Do not delete that backup until the new environment is confirmed.
