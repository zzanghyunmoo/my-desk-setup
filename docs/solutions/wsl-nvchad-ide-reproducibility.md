# Reproducing a WSL NvChad IDE

## Problem

Manual WSL installations can mask version drift: a system `go`, `python`, or `nvim` may satisfy a broad version probe while differing from the reviewed version that mds is expected to own. Ubuntu 26.04 also does not publish a `pyright` apt package.

## Solution

- Use `nvim-ide` to resolve the C++, Go, Python, NvChad, and IDE-tool graph.
- Resolve pinned mise and Bun tools through mds-managed launchers before running verification, so the probe executes the managed executable.
- Install Neovim through the reviewed vendor artifact; install Pyright from its integrity-checked npm tarball through Bun.
- Keep the editor transition opt-in. `--adopt-nvchad` relocates an existing configuration to `.nvim-mds-backup-<timestamp>` and then publishes the marked managed tree.

## Verification

On WSL Ubuntu 26.04, a clean-home `mds apply --profile nvim-ide` completed with all actions ready, including Go 1.26.5, Neovim 0.11.5, Pyright 1.1.411, Python 3.14.6, and the C++/Go/Python IDE layer.

## Follow-up Rule

When a binary comes from a non-system installer, its ready check must resolve the manager-owned launcher or artifact path. Do not let an unrelated system executable make a pinned component look ready.
