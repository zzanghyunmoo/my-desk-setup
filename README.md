# my-desk-setup

`my-desk-setup`(`mds`)은 macOS·Windows 호스트와 Ubuntu 26.04 LTS
Linux 게스트에 개인 개발 환경을 재현하는 Go 기반 control plane이다.

- macOS는 Lima, Windows는 WSL2로 표준 게스트를 준비한다.
- 호스트는 GUI·플랫폼 도구·호스트 코딩 에이전트를 소유한다.
- 게스트는 CLI, 언어·빌드 도구, Neovim, 코딩 에이전트와 Docker Engine을
  소유한다.
- 전체, profile, component, interactive 선택은 하나의 resolver와 같은
  plan digest 계약을 사용한다.
- 로그인, OAuth, token과 credential 보관은 자동화하지 않는다. 인증은 설치
  뒤 사용자가 각 도구에서 직접 수행한다.

## 현재 상태

ZZA-100 기능 브랜치에서 `plan`, `apply`, `doctor`, `update`, `catalog`,
`version`과 release 및 certification 도구를 구현하고 검증하는 중이다. 아래
항목은 아직 완료로 간주하지 않는다.

- 현재 U11/final review head에는 macOS host, Windows host, WSL Ubuntu와
  Lima Ubuntu 어느 target도 실제 evidence가 없다. `80f866a`의 macOS
  `blocked` bundle은 이전 commit의 역사적 진단일 뿐 현재 head 인증이 아니다.
- fixture와 CI smoke는 실제 머신 evidence를 대신하지 않는다.
- 기존 `settings` 원격은 복구 bundle을 남긴 뒤 `my-desk-setup` orphan
  `main`으로 전환했다. 현재 source 변경은 `zza-100/bootstrap` PR에서
  진행하며 workspace submodule pointer는 merge된 release commit 검증 뒤
  이동한다.
- 따라서 현재 브랜치나 artifact를 네 target 전체가 검증된 최종 release로
  표현하지 않는다.

세부 경계는
[환경 control plane](docs/architecture/environment-control-plane.md),
[component catalog](docs/components/catalog.md),
[bootstrap 운영](docs/operations/bootstrap.md),
[update 운영](docs/operations/update.md),
[automation 계약](docs/operations/automation.md),
[recovery 운영](docs/operations/recovery.md)을 참고한다.

## CLI 빠른 시작

소스 checkout에서 현재 CLI를 확인한다.

```sh
go test ./...
go build -o ./mds ./cmd/mds
./mds --version
./mds version --format json
./mds catalog --format json
```

설치 전에 하나의 선택 방식으로 read-only plan을 만든다.

```sh
./mds plan --all --format json
./mds plan --profile owner --format json
./mds plan --component codex --format json
./mds plan --interactive
```

`--all`, `--profile`, `--component`, `--interactive` 중 정확히 하나만 사용한다.
JSON의 target, action, blocker와 `digest`를 검토한 뒤 동일한 선택과 digest로
현재 target에 적용한다.

```sh
./mds apply \
  --component codex \
  --plan-digest 'sha256:<reviewed-plan-digest>' \
  --format json

./mds doctor --component codex --format json
```

`plan`과 `doctor`는 read-only다. `apply`는 현재 로컬 target에만 실행되며
digest와 stable target preimage가 달라지면 첫 mutation 전에 중단한다.
reachability나 systemd active 같은 volatile readiness는 apply 직전 별도
preflight로 다시 확인하고 stale plan과 구분한다.

## 호스트와 게스트

호스트 `--all`은 해당 호스트에서 가능한 GUI, 플랫폼 도구와 코딩 에이전트를
선택한다. `owner` profile은 호스트와 게스트의 전체 의도를 표현하므로 현재
target에서 지원하지 않는 항목도 `unsupported`로 명시할 수 있다.

macOS에서 Lima guest `mds`, Windows에서 WSL distribution `Ubuntu-26.04`를
준비하는 host plan/apply는 guest lifecycle과 같은 release의 Linux `mds`
handoff까지만 소유한다. 그 뒤 게스트 안에서 별도의 guest-local plan과
digest를 검토하고 적용한다.

```sh
# Lima 또는 WSL 게스트 내부
mds plan --all --format json
mds apply --all --plan-digest 'sha256:<reviewed-guest-plan-digest>' --format json
mds doctor --all --format json
```

Docker Desktop과 host Docker engine은 v1 범위가 아니다. Docker Engine,
CLI와 Compose plugin은 guest-local systemd service로 설치하고 검증한다.

## Release bootstrap

Release의 `checksums.txt`와 `release-manifest.json`을 먼저 검증하고, 같은
release에 포함된 `macos.sh` 또는 `windows.ps1`을 사용한다. bootstrap은 exact
release version과 archive SHA-256을 요구하며 임의의 `latest`를 실행하지 않는다.
tag publication은 네 표준 actual target의 exact commit/catalog/plan/binary
evidence를 통과해야 하며, 결과는 영구 asset `release-promotion.json`으로
release에 함께 포함된다.

```sh
MDS_VERSION='<exact-version>' \
MDS_SHA256='<archive-sha256-from-checksums>' \
sh ./macos.sh
```

```powershell
$env:MDS_VERSION = "<exact-version>"
$env:MDS_SHA256 = "<archive-sha256-from-checksums>"
& .\windows.ps1
```

자세한 clean-host와 reboot/first-run 재개 절차는
[bootstrap 운영 문서](docs/operations/bootstrap.md)에 있다. 실제 target
certification을 위한 전용 runner는
[runner 준비 runbook](docs/operations/target-certification-runner.md)의
보호 경계와 nonce rotation 절차를 따른다.

## 인증 경계

`mds`는 설치와 로컬 readiness만 다룬다. 다음 작업은 사용자가 직접 한다.

- Claude Code, OpenCode, Codex 로그인
- `gh`, `glab`, Atlassian `acli`, Notion `ntn`, Linear CLI 인증
- Slack, KakaoTalk, Notion, Linear, Chrome 계정 로그인
- Docker registry 로그인
- Xcode license, Apple ID, signing 설정

`doctor`와 target evidence는 auth 상태를 조회하거나 credential을 저장하지
않는다.

## 개발 흐름

`main`에 직접 기능 변경을 넣지 않는다. Linear 티켓에 대응하는 브랜치에서
작업하고 검증과 리뷰를 거쳐 PR로 병합한다.
