# Environment Control Plane

## 목적과 기준 환경

`mds`는 한 저장소의 closed catalog를 macOS·Windows 호스트와 Ubuntu 26.04
LTS 게스트에 적용하는 개인용 환경 control plane이다. v1 target은 다음 네
종류로 닫혀 있다.

| Target ID 예 | 실행 위치 | 소유 범위 |
| --- | --- | --- |
| `macos-host:local` | macOS host | GUI, Lima, Xcode 예외, host 코딩 에이전트 |
| `windows-host:local` | Windows host | GUI, WSL2, host 코딩 에이전트 |
| `lima-guest:mds` | Lima Ubuntu guest | 개발 CLI·toolchain·editor·agents·Docker |
| `wsl-guest:Ubuntu-26.04` | WSL Ubuntu guest | 개발 CLI·toolchain·editor·agents·Docker |

native Linux, 다른 distribution, Proxmox와 Docker Desktop은 v1 target이
아니다. macOS/Windows host가 게스트 lifecycle과 structured transport를
소유하지만 Linux component reconciliation은 같은 release의 `mds`가 게스트
안에서 수행한다. 호스트가 게스트 filesystem을 직접 관리하지 않는다.

## 소유권 경계

```text
macOS host                         Windows host
├─ GUI applications               ├─ GUI applications
├─ WezTerm                        ├─ WezTerm
├─ host coding agents             ├─ host coding agents
├─ Lima lifecycle                 └─ WSL2 lifecycle
└─ Xcode/iOS manual exception
        │                                  │
        ▼                                  ▼
Lima Ubuntu 26.04                 WSL Ubuntu 26.04
├─ CLI and collaboration tools    ├─ CLI and collaboration tools
├─ languages and build tools      ├─ languages and build tools
├─ Herdr + Neovim/NvChad          ├─ Herdr + Neovim/NvChad
├─ coding agents                  ├─ coding agents
└─ Docker Engine/CLI/Compose      └─ Docker Engine/CLI/Compose
```

host coding agents는 호스트에서 orchestration을 할 수 있게 남겨 둔다. 실제
checkout, build, test와 primary editor work의 표준 위치는 Linux guest다.
Flutter의 주 개발 환경도 guest이며 Xcode, Apple signing과 iOS build는
macOS host의 수동 예외다.

Docker Engine은 guest-local systemd service다. `DOCKER_HOST`가 외부 host
socket을 가리키면 conflict로 처리한다. Docker Desktop이나 host engine을
설치 성공으로 대신하지 않는다.

## 단일 계획 흐름

모든 선택 입력은 같은 `Selection`과 resolver로 정규화된다.

1. `--all`, `--profile`, 반복 가능한 `--component`, `--interactive` 중
   하나를 선택한다.
2. embedded catalog 또는 명시한 `--catalog`를 strict schema로 읽는다.
3. 현재 또는 명시한 target facts를 관찰한다.
4. dependency expansion과 target eligibility를 계산한다.
5. stable `mds.plan/v1` JSON과 canonical SHA-256 digest를 만든다.
6. 사용자가 action, blocker, requested version과 digest를 검토한다.
7. `apply`가 같은 선택과 `--plan-digest`로 계획을 다시 계산한다.
8. digest와 target preimage를 첫 mutation 전에 재검증한다.
9. component별 Observe → Apply → Verify → Observe 결과를 journal과
   receipt에 기록한다.

`plan`에는 wall-clock 값이 들어가지 않는다. 같은 catalog/lock, target facts와
selection은 같은 ordered action과 digest를 만든다. `apply`는 다른 target을
원격으로 바꾸는 명령이 아니며 현재 실행 중인 로컬 target만 변경한다.

## 명령과 mutation 경계

| 명령 | 기본 경계 | 결과 |
| --- | --- | --- |
| `plan` | read-only | `mds.plan/v1`, action과 digest |
| `apply` | exact digest 뒤 mutation | target-local `mds.receipt/v1` |
| `doctor` | read-only, no-auth observation and functional verification | `mds.doctor/v1` |
| `update` preview | read-only | `mds.update/v1`, old/new lock diff와 digest |
| `update --plan-digest` | exact update mutation | lock write와 target receipt |

`doctor`는 executable, local config, version과 integration readiness를
관찰한다. login, token, organization access와 remote account permission은
확인하지 않는다. auth가 없다는 이유로 local 설치 readiness를 실패시키지
않는다.

## 상태와 재개

기본 state root는 Unix 계열에서
`${XDG_STATE_HOME:-$HOME/.local/state}/my-desk-setup`, Windows에서
`%LOCALAPPDATA%\my-desk-setup\state`다. target ID별 directory에는 single-writer
lock, append-only journal과 digest별 receipt가 있다. writer lock은 owner-only
stable file의 OS advisory lease이며 process 종료 시 OS가 자동 해제한다.

- journal은 action 시작, 성공, verification과 실패 지점을 기록한다.
- receipt는 requested, installed, verified version과 component outcome을
  구분한다.
- 같은 desired state를 다시 적용할 때 실제 상태가 이미 ready면 installer를
  재실행하지 않고 no-op verification으로 수렴한다.
- 실패한 node의 downstream만 `blocked`가 되고 독립 node는 계속된다.
- state는 원하는 상태의 원본이 아니다. 재개 전 actual state를 다시 관찰한다.
- update는 catalog-scoped lease를 먼저, target lease를 다음으로 획득하고 두 lease를
  catalog lock 게시와 target reconcile이 모두 끝날 때까지 유지한다.

## Safety invariants

아래 조건은 release와 target certification의 필수 gate다.

- `plan`과 `doctor`는 target, catalog, state와 repository를 변경하지 않는다.
- stale digest, catalog/lock preimage와 target preimage는 첫 mutation 전에
  실패한다.
- receipt 없는 config나 launcher는 user-owned다. 명시적 managed ownership
  없이 덮어쓰거나 자동 backup/삭제하지 않는다.
- command runner는 executable과 argv를 분리한다. shell string, unbounded
  stdout/stderr와 inherited credential environment를 사용하지 않는다.
- command, probe와 evidence는 timeout과 bounded output을 사용한다.
- plan, journal, receipt, log와 evidence에는 token, cookie, password,
  auth 상태와 개인 absolute path가 없어야 한다.
- `unsupported`, `action-required`, `blocked`와 `unverifiable`을 success로
  숨기지 않는다.

이 invariant를 구현·테스트하지 못한 build는 실제 target 성공으로 인증할 수
없다.

## Release와 target evidence

Release는 OS/architecture별 archive, `checksums.txt`,
`release-manifest.json`과 두 host bootstrap을 하나의 identity로 묶는다.
manifest는 source commit과 catalog revision뿐 아니라 archive 안의 실제
`mds` binary SHA-256도 기록한다. manifest와 archive 내부 binary checksum이
모두 검증된 production archive만 certification에 사용한다.

Evidence 상태는 다음 의미로만 사용한다.

| 상태 | 의미 |
| --- | --- |
| `implemented` | 코드/fixture lane의 상태이며 actual evidence bundle에는 사용할 수 없음 |
| `blocked` | 실제 target에서 certification을 시도했으나 prerequisite나 readiness가 남음 |
| `verified` | production artifact가 해당 실제 target의 필수 probe를 모두 통과함 |

actual bundle은 `mds.target-evidence/v1`, `capture_kind: actual-target`이며
status로 `blocked` 또는 `verified`만 허용한다. `implemented`를 actual bundle에
기록하면 verifier가 거부해야 한다.

fixture, fake adapter, hosted CI 또는 다른 target 결과로 실제 target을
`verified`로 승격하지 않는다. evidence verifier가 bundle 구조와 digest를
검증했다는 사실도 내부 target outcome이 `blocked`인 것을 `verified`로 바꾸지
않는다.

tag publication 앞의 promotion gate는 exact tag commit으로 실행한 GitHub
Actions actual-target artifact를 찾아 다음 네 표준 target을 정확히 하나씩
요구한다.

- `macos-host:local`
- `windows-host:local`
- `wsl-guest:Ubuntu-26.04`
- `lima-guest:mds`

각 bundle은 CLI commit, catalog revision, plan digest, target ID와 실제
실행한 on-disk binary SHA-256을 release manifest에 다시 결합해 검증한다.
증거가 없거나 오래됐거나 중복됐거나 identity가 다르면 publication은
fail closed다. `verified`는 그대로 통과한다. `blocked`는 status를 바꾸지
않으며, target identity가 완전하고 ready가 아닌 모든 outcome이 사용자의
정직한 `action-required`인 경우에만 publication-acceptable이다. planned
`unready`/`conflict`, `unsupported` 또는 불완전 target은 승격을 차단한다.

promotion 결과는 deterministic `mds.release-promotion/v1` 보고서로 만들고
publish 직전에 release manifest와 다시 대조한 뒤
`release-promotion.json`이라는 영구 GitHub Release asset으로 함께 게시한다.

현재 Windows host, WSL Ubuntu와 Lima Ubuntu의 실제 evidence는 없다. 이
dependency가 해소되기 전에는 네 target 전체가 verified라고 주장할 수 없다.

## 인증과 파괴적 작업

인증은 control plane 밖의 사용자 작업이다. `mds`에는 auth/login 명령,
credential schema, token probe와 secrets vault가 없다.

저장소 rename, orphan rewrite, force-push, remote branch 삭제와 workspace
submodule 전환도 일반 `apply` 범위가 아니다. complete-history recovery
bundle과 exact ref/SHA inventory를 검증한 뒤 현재 turn의 별도 파괴적 승인을
받아야 한다. 현재 이 repository transition은 승인 전이며 완료되지 않았다.
