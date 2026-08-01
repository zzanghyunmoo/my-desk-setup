---
title: WSL NvChad IDE - Plan
date: 2026-08-01
type: feat
topic: wsl-nvchad-ide
artifact_contract: ce-unified-plan/v1
artifact_readiness: implementation-ready
product_contract_source: ce-brainstorm
execution: code
linear_issue: ZZA-102
---

# WSL NvChad IDE - Plan

## Goal Capsule

**Objective:** Reproduce a C++, Go, and Python NvChad IDE in a supported WSL guest through `mds apply`.

**Product authority:** The user's requested WSL development environment and the repository's managed-ownership, pinned-version, and no-auth-execution rules.

**Open blockers:** The existing user-owned `~/.config/nvim` requires a deliberate, recoverable transition into mds ownership.

## Product Contract

## Summary

`mds apply` will prepare the language tooling and a managed NvChad configuration needed for completion, navigation, debugging, linting, and formatting in C++, Go, and Python on WSL.

## Problem Frame

The current WSL setup was assembled manually. It has working language servers and debugging tools, but a fresh guest cannot reproduce that result through the catalog, and a normal apply correctly refuses to overwrite the existing user-owned Neovim configuration.

## Key Decisions

- **Managed adoption is opt-in.** Normal apply continues to protect user-owned `~/.config/nvim`; an explicit adoption path creates a durable backup before mds publishes its managed configuration.
- **The IDE is a profile-level capability.** One focused guest profile selects the existing C++/Go/Python runtimes, Neovim, NvChad, and the language-tool layer, rather than relying on an implicit owner profile.
- **Tooling must be verifiable.** Each managed language layer exposes reviewed identity and functional checks so apply reports a real failure instead of claiming an unverified IDE is ready.

## Requirements

**Reproducible IDE**

- R1. A WSL-targeted profile selects C++/Go/Python tooling, Neovim, NvChad, and IDE support without unrelated desktop or agent components.
- R2. The selected environment installs language-server, formatter, linter, and debugger capabilities for C++, Go, and Python through reviewed catalog actions.
- R3. The managed NvChad configuration enables those capabilities using the installed command names and remains pinned to the reviewed NvChad revision.

**Ownership and safety**

- R4. A normal `mds apply` never overwrites a user-owned Neovim configuration.
- R5. The explicit adoption path preserves the full prior configuration in a discoverable backup before publishing managed state.
- R6. The implementation does not perform editor, package-manager, or AI-agent login flows.

**Evidence and handoff**

- R7. Automated tests cover normal ownership refusal, explicit adoption, catalog resolution, and the new managed tool behavior.
- R8. The delivery records the exact WSL apply/doctor result and any environment prerequisite that prevents a live run.

## Key Flows

- F1. **Fresh WSL guest:** The user plans and applies the IDE profile; mds installs the selected language and editor components, then verifies the supported checks.
- F2. **Existing NvChad user:** The user invokes the explicit adoption command; mds moves the prior config to a timestamped backup, writes its ownership marker, and verifies the new configuration.
- F3. **Normal repeat apply:** mds recognizes its marker and pinned revision, leaves matching state intact, and reports ready only after verification succeeds.

## Acceptance Examples

- AE1. **Covers R4, R5.** Given a `~/.config/nvim` directory without an mds marker, ordinary apply reports a conflict and leaves it unchanged; explicit adoption produces a backup and a ready managed configuration.
- AE2. **Covers R1-R3.** Given a supported WSL guest with required system privilege, applying the IDE profile produces successful checks for the declared language-tool capabilities and headless Neovim startup.

## Scope Boundaries

- Windows-host editor configuration is outside this change; the target is WSL and the profile must make that boundary clear.
- Login, token storage, and credential refresh remain user actions.
- Arbitrary user Neovim customizations are preserved as a backup, not merged into the managed NvChad tree.

## Dependencies / Assumptions

- The WSL guest has network access and a user-refreshed `sudo` credential when apt-managed packages are needed.
- The reviewed NvChad starter revision remains fetchable from its source.
- Existing catalog install mechanisms can safely express every required language tool, or planning will add the narrow missing adapter boundary.

## Sources / Research

- `catalog/components/guest.yaml`
- `catalog/profiles/owner.yaml`
- `internal/adapters/guest/editor.go`
- `internal/cli/apply.go`
- `tests/adapters/guest_components_runtime_test.go`

## Implementation Contract

1. Add the `nvim-ide` guest profile and the `nvim-ide-tools` capability layer in `catalog/components/guest.yaml`. Keep language-server ownership unique by assigning Pyright to its own pinned component.
2. Lock Pyright's reviewed npm tarball in `catalog/locks/versions.lock.yaml`; install it through the existing verified Bun artifact flow because Ubuntu 26.04 has no `pyright` apt package.
3. Use vendor-pinned Neovim and managed launchers for mise/Bun tools so verification resolves the version installed by mds, not an unrelated system executable.
4. Extend the guest editor adapter with explicit NvChad adoption. A user-owned configuration is renamed to a timestamped backup only after `--adopt-nvchad`; routine apply remains non-destructive.
5. Generate NvChad LSP, formatting, linting, DAP, and plugin configuration for clangd, gopls, and Pyright. Point Pyright at Bun's managed `pyright-langserver` executable.
6. Preserve normal apply/update semantics by threading adoption only through `apply`, never `doctor` or `update`.

## Verification Contract

- Automated: `go test ./...`, `go vet ./...`, and `go build ./cmd/mds`; isolate documented Windows-host failures outside the changed behavior.
- Targeted: package exact-version and managed-adoption regression tests; catalog/golden plan tests.
- WSL manual gate: create a plan and apply `--profile nvim-ide` in a clean temporary WSL home; require every selected action to report `ready`.

## Execution Evidence

- A clean `Ubuntu-26.04` run completed `mds apply --profile nvim-ide` with digest `sha256:d300d9e275712047a01a84e179f550b3c0b33349490594f9c802e1c54e53d6c3`.
- The resulting report marked base CLI, C toolchain, mise, Go 1.26.5, Neovim 0.11.5, pinned NvChad, Bun 1.3.14, Pyright 1.1.411, Python 3.14.6, and `nvim-ide-tools` ready.
- Ubuntu 26.04 package metadata was checked before cataloging: `gopls`, `delve`, `clangd`, and `python3-debugpy` exist; Pyright is supplied through its reviewed npm artifact instead of a nonexistent apt package.

## Definition of Done

- `nvim-ide` selects only the intended WSL IDE graph.
- Normal apply preserves user-owned Neovim; explicit adoption retains a discoverable backup.
- A clean WSL apply completes with every action ready and no login/auth operation.
- Tests, review findings, and the PR describe the remaining host-specific test limitation accurately.
