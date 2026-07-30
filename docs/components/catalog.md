# Component Catalog

## Catalog 계약

`catalog/`은 설치 의도의 canonical source다.

```text
catalog/
├── components/        # component, dependency, target status, verifier
├── mise.toml          # strict mise tool declaration
├── mise.lock          # official mise platform artifact lock
├── profiles/          # named component selection
├── locks/             # exact version, provenance, artifact checksum
├── targets/           # Ubuntu 26.04 guest image identity
└── schema/            # closed JSON Schema
```

YAML은 unknown field, duplicate ID/capability, missing dependency, cycle,
target-ineligible dependency, invalid installer와 missing lock을 fail closed한다.
Bun-managed pinned lock은 package/version에 대응하는 official npm canonical
tarball URL, canonical SHA-512 SRI와 lowercase SHA-256을 모두 요구한다.
component와 capability는 단일 owner를 가진다. catalog와 lock의 canonical JSON
SHA-256뿐 아니라 `mise.toml`과 `mise.lock`을 LF로 정규화한 exact content도
plan의 catalog revision에 포함된다. checkout의 CRLF 또는 단독 CR은 loader에서
LF로 정규화되며, 의미 있는 content가 바뀐 plan digest는 재사용할 수 없다.

`catalog/mise.toml`은 strict lock mode를 사용하고 `catalog/mise.lock`은 각
tool의 eligible `linux-x64`/`linux-arm64` cell마다 exact artifact URL과
SHA-256 또는 구체적인 unavailable reason 중 정확히 하나를 가져야 한다.
loader는 두 mise 파일을 `versions.lock.yaml`의 tool/version/platform
URL·SHA-256과 다시 대조한다.
Flutter Linux arm64처럼 공식 artifact가 없는 cell은 비공식 mirror로 채우거나
설치 성공으로 가장하지 않고 `action-required`로 남긴다.

## 선택 방식

```sh
mds plan --all
mds plan --profile owner
mds plan --component notion-cli --component linear-cli
mds plan --interactive
mds catalog --format json
```

네 방식은 같은 resolver를 사용하며 정확히 하나만 선택할 수 있다.

- `--all`: 현재 target에서 `supported` 또는 `action-required`인 component만
  선택한다.
- `--profile`: 저장소에 선언한 cross-target 의도를 선택한다. 현재 target에서
  지원하지 않는 항목도 honest `unsupported` action으로 나타날 수 있다.
- `--component`: component ID 또는 capability를 선택하고 dependency만
  확장한다.
- `--interactive`: UI에서 고른 ID를 `--component`와 같은 Selection으로
  정규화한다.

예를 들어 `notion-cli`만 선택하면 guest의 Notion `ntn`과 dependency만
계획한다. `notion-desktop` action은 추가하지 않는다.

## Target ownership

| 영역 | Component ID | Target |
| --- | --- | --- |
| terminal GUI | `wezterm` | macOS/Windows host |
| desktop apps | `notion-desktop`, `linear-desktop`, `slack`, `kakaotalk`, `chrome` | host |
| guest runtime | `lima`, `wsl` | 각각 macOS/Windows host |
| iOS 예외 | `xcode` | macOS host, manual |
| base CLI | `base-cli` | WSL/Lima guest |
| language | `java`, `kotlin`, `go`, `python`, `typescript`, `c-toolchain`, `flutter` | guest |
| build/runtime | `mise`, `bun`, `gradle`, `uv` | guest 중심 |
| editor/terminal | `neovim`, `nvchad`, `herdr` | guest |
| coding agents | `claude-code`, `opencode`, `codex` | host와 guest |
| collaboration CLI | `atlassian-cli`, `notion-cli`, `linear-cli`, `gh`, `glab` | guest |
| containers | `docker-engine` | guest |

`atlassian-cli`는 Jira와 Confluence capability를 함께 제공하는 Atlassian
`acli`다. `notion-cli`는 `ntn`, `linear-cli`는
`@schpet/linear-cli`를 사용한다.

Docker component는 Engine, CLI와 Compose plugin을 guest에 설치한다. host의
Docker Desktop이나 외부 Docker socket은 이 component를 만족시키지 않는다.

## Component 상태

모든 component는 각 target cell에 상태를 명시한다.

| Catalog 상태 | Plan/운영 의미 |
| --- | --- |
| `supported` | typed adapter가 install/observe/verify를 소유 |
| `unsupported` | 해당 target 소유가 아니거나 v1 미지원 |
| `action-required` | 사람이 완료해야 할 수동 단계 |

Windows의 Linear Desktop과 KakaoTalk은 검증된 unattended package ID가 없어
`action-required`다. Xcode는 App Store 설치, license와 signing이 수동이다.
이 항목은 `all` 결과에서 사라지지 않으며 complete receipt를 만들지 않는다.

## Version policy

| Mode | 의미 |
| --- | --- |
| `pinned` | committed lock의 exact version/provenance/checksum 사용 |
| `manager` | Homebrew, WinGet 또는 apt가 소유하며 manager-owned로 보고 |
| `manual` | 사람이 설치·승인하며 `mds`가 성공으로 가장하지 않음 |

normal `plan`과 `apply`는 upstream latest metadata를 조회하거나 lock을 올리지
않는다. pinned version 이동은 reviewable candidate, old/new lock diff와 exact
update digest를 사용하는 `mds update`로만 수행한다. update에는 writable
repository checkout의 `--catalog`가 필요하며 embedded catalog는 읽기 전용이다.
v1 update transaction은 `versions.lock.yaml`, `mise.toml`, `mise.lock`을 한
번에 게시하지 못하므로 mise가 관리하는 component의 update는 mutation 전에
명시적으로 거부한다.

Bun package는 query/fragment가 없는 reviewed HTTPS URL의 tarball을 redirect
없이 bounded download한 뒤 SRI와 SHA-256을 모두 검증하고 local `.tgz`로
설치한다. `package@version` registry re-resolution은 normal/update apply 어느
쪽에서도 사용하지 않는다.

AI agent launcher는 upstream auto-update를 끄는 managed 환경을 제공하지만
로그인이나 token은 다루지 않는다. 사용자는 설치 뒤 각 agent에서 직접
인증한다.

## User-owned config

NvChad-derived Neovim config와 managed agent launcher는 ownership marker가
있는 경로만 교체할 수 있다. marker가 없는 기존 file/directory는
`user-owned-or-version-conflict`로 보고한다.

`doctor`나 normal `apply`는 user-owned config를 overwrite하지 않는다.
명시적 update도 `mds`가 소유한 path만 exact revision으로 교체한다.

## Profile

`owner`는 전체 개인 환경 의도를, `minimal`은 작은 개발 core를 표현한다.
profile은 target별 imperative script가 아니라 component ID 집합이다. profile
변경은 resolver 입력만 바꾸며 component adapter 구현을 복제하지 않는다.

현재 profile과 exact version은 다음 파일에서 확인한다.

```sh
sed -n '1,240p' catalog/profiles/owner.yaml
sed -n '1,240p' catalog/profiles/minimal.yaml
sed -n '1,320p' catalog/locks/versions.lock.yaml
sed -n '1,240p' catalog/mise.toml
sed -n '1,320p' catalog/mise.lock
```

계획 검토 시에는 선언만 보지 말고 실제 target 결과를 확인한다.

```sh
mds plan --profile owner --format json
mds doctor --profile owner --format json
```

`doctor`는 local readiness만 확인하며 login, organization access나 Docker
registry auth를 확인하지 않는다.
