# WSL NvChad IDE

Create and inspect the reproducible C++, Go, and Python environment from WSL:

```sh
mds plan --target wsl-guest:Ubuntu-26.04 --profile nvim-ide
mds apply --target wsl-guest:Ubuntu-26.04 --profile nvim-ide --plan-digest <digest>
```

`nvim-ide` installs pinned Neovim, the NvChad starter, lazy.nvim and the complete plugin lock, Go, Python, Bun/Pyright, and the WSL packages for clangd, clang-format, clang-tidy, lldb, gopls, delve, Ruff, and debugpy. It does not initiate any login flow. Selecting `nvchad` without `nvim-ide-tools` installs only the starter and does not publish language-tool configuration.

If `~/.config/nvim` is yours already, normal apply intentionally refuses to replace it. Review the plan and then use:

```sh
mds apply --target wsl-guest:Ubuntu-26.04 --profile nvim-ide --adopt-nvchad --plan-digest <digest>
```

The old directory is retained beside the config directory as `.nvim-mds-backup-<UTC timestamp>-<unique suffix>` before mds writes its managed configuration. Do not delete that backup until the new environment is confirmed.
