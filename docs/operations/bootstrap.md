# Bootstrap Operations

## 전제

bootstrap은 production release의 Go binary를 host에 설치하는 작은
OS-native 진입점이다. moving installer나 `latest` tag를 신뢰하지 않는다.

Release directory에는 정확히 다음 파일이 있어야 한다.

```text
mds_<version>_darwin_amd64.tar.gz
mds_<version>_darwin_arm64.tar.gz
mds_<version>_windows_amd64.zip
mds_<version>_windows_arm64.zip
mds_<version>_linux_amd64.tar.gz
mds_<version>_linux_arm64.tar.gz
macos.sh
windows.ps1
checksums.txt
release-manifest.json
```

`release-manifest.json`은 release version, source commit, release timestamp와 각
artifact identity를 기록한다. 여기에는 embedded catalog revision과 각 archive
안의 실제 binary SHA-256도 포함된다. `checksums.txt`는 publish된 archive의
SHA-256을 제공한다. manifest가 가리키지 않거나 archive 또는 내부 binary
checksum이 맞지 않는 archive를 bootstrap 또는 target certification에 사용하지
않는다. 정식 tag release에는 같은 identity에 묶인
`release-promotion.json`도 영구 asset으로 포함된다.

embedded catalog revision은 component/profile/version lock뿐 아니라
`catalog/targets/ubuntu-26.04.yaml`의 Lima와 WSL image URL/SHA-256도 포함한다.
따라서 target image가 바뀌면 release catalog revision과 관련 plan digest가
함께 바뀐다. host의 `lima`/`wsl` plan action은 선택된 architecture의
`image_url`, `image_sha256`, `image_kind`, `guest_distribution`을 명시해
사용자가 mutation 전에 확인할 수 있게 한다.

macOS와 Windows host binary에는 같은 release의 Linux `amd64`/`arm64`
archive URL과 SHA-256이 함께 embed된다. guest bootstrap은 이 exact identity만
사용하며 `latest`나 별도 moving catalog를 조회하지 않는다.

### Maintainer build

source commit과 whole-second RFC3339 release timestamp를 명시한다. output
directory는 기존에 없어야 하며 기본값은 `dist`다.

```sh
MDS_VERSION='0.1.0' \
MDS_COMMIT='<40-or-64-character-commit-sha>' \
MDS_DATE='<RFC3339-release-timestamp>' \
scripts/build-release.sh ./dist
```

builder는 `mds.release/v1` manifest, 두 bootstrap과 여섯 OS/architecture
archive를 staging directory에서 만든 뒤 자체 검증에 성공해야 output
directory를 publish한다.

## Release 검증

Release asset을 같은 directory에 받은 뒤 repository verifier를 실행한다.

```sh
scripts/verify-release.sh ./dist
```

verifier가 없는 clean host에서는 최소한 `checksums.txt`를 로컬에서 검증하고,
사용할 archive의 이름, version, OS/architecture와 manifest entry가 일치하는지
확인한다.

```sh
shasum -a 256 -c checksums.txt
```

PowerShell에서는 다음과 같이 archive SHA-256을 확인할 수 있다.

```powershell
(Get-FileHash -Algorithm SHA256 .\mds_<version>_windows_amd64.zip).Hash
```

checksum 값 자체도 신뢰한 release publication 경로에서 얻어야 한다. 임의의
본문이나 실패한 CI artifact에서 복사한 checksum은 검증 근거가 아니다.

## macOS clean host

같은 release의 `macos.sh`와 macOS archive checksum을 사용한다.

```sh
export MDS_VERSION='<exact-version>'
export MDS_SHA256='<darwin-archive-sha256>'
sh ./macos.sh

"$HOME/.local/bin/mds" --version
"$HOME/.local/bin/mds" plan --all --format json
```

bootstrap은 `~/.local/bin/mds`에 설치하고 인증을 실행하지 않는다. 최초
`plan` JSON의 target, selection, `action-required`, catalog revision과
digest를 보관해 검토한다.

검토한 plan과 같은 selection으로 적용한다.

```sh
"$HOME/.local/bin/mds" apply \
  --all \
  --plan-digest 'sha256:<reviewed-host-plan-digest>' \
  --format json
```

`xcode` 같은 manual component가 있으면 apply는 나머지 독립 node를 처리한
뒤 incomplete/action-required receipt와 non-zero exit를 낼 수 있다. 이를
전체 실패나 전체 성공으로 바꾸지 않는다.

### Lima Ubuntu 준비

Lima만 별도로 검토하고 준비할 수도 있다.

```sh
"$HOME/.local/bin/mds" plan --component lima --format json
"$HOME/.local/bin/mds" apply \
  --component lima \
  --plan-digest 'sha256:<reviewed-lima-plan-digest>' \
  --format json
```

adapter는 pinned Ubuntu 26.04 image와 digest로 `mds`라는 Lima instance를
생성하거나 시작한다. 생성 후 host에서 다음 상태를 확인한다.

```sh
limactl list
limactl shell --tty=false mds -- true
```

GuestRuntime은 생성·시작 후 host에서 guest-owned 경로를 직접 수정하지 않는다.
guest-local `mds`가 없거나 revision이 다르면 host binary에 embed된 같은
release의 Linux archive URL과 SHA-256을 고정 argv와 bounded stdin installer로
guest에 전달한다. guest 내부 installer가 HTTPS download, SHA-256 검증,
`~/.local/bin/mds` atomic install과 owner-only marker 기록을 수행한다.
user-owned binary, symlink 또는 marker 없는 기존 binary는 덮어쓰지 않고
`action-required`로 중단한다.

설치 뒤 GuestRuntime은 bounded
`limactl shell --tty=false ... -- /bin/sh -c <fixed-handoff> ...`
argv로 guest-local CLI를 실행한다. guest plan이 host와 정확히 같은 CLI
revision과 embedded catalog revision을 보고할 때만 Lima runtime을 ready로
판정한다. release metadata가 없는 development host binary는 자동 download를
시도하지 않고 정확한 Linux artifact identity가 필요하다고 보고한다.

게스트 shell에서 actual target과 guest selection을 다시 계획하고 적용한다.

```sh
mds --version
mds plan --all --format json
mds apply --all \
  --plan-digest 'sha256:<reviewed-guest-plan-digest>' \
  --format json
mds doctor --all --format json
```

`LIMA_INSTANCE=mds`가 있는 Lima shell에서 local target은
`lima-guest:mds`로 발견된다. Docker 단계는 guest의 systemd가 active가
아니면 mutation 전에 `action-required`다. APT나 Docker 설치 전
`/usr/bin/sudo -n true`가 실패하면 mds는 password를 받지 않고 다음 조치를
요청한다.

```sh
sudo -v
mds plan --all --format json
mds apply --all \
  --plan-digest 'sha256:<newly-reviewed-guest-plan-digest>' \
  --format json
```

Docker 설치 후 `docker` 그룹 가입은 root-equivalent daemon 권한을 주므로
mds가 자동 실행하지 않는다. 사용자가 action-required에 표시된
`sudo usermod -aG docker <guest-user>`를 검토해 직접 실행하고 guest shell을
재시작한 뒤 다시 계획·적용한다.

## Windows clean host

같은 release의 `windows.ps1`과 Windows archive checksum을 사용한다.

```powershell
$env:MDS_VERSION = "<exact-version>"
$env:MDS_SHA256 = "<windows-archive-sha256>"
& .\windows.ps1

$mds = "$env:LOCALAPPDATA\my-desk-setup\bin\mds.exe"
& $mds --version
& $mds plan --all --format json
```

검토한 digest로 현재 Windows host에 적용한다.

```powershell
& $mds apply `
  --all `
  --plan-digest "sha256:<reviewed-host-plan-digest>" `
  --format json
```

### WSL reboot와 first-run 재개

WSL만 별도로 계획할 수 있다.

```powershell
& $mds plan --component wsl --format json
& $mds apply `
  --component wsl `
  --plan-digest "sha256:<reviewed-wsl-plan-digest>" `
  --format json
```

GuestRuntime은 `catalog/targets/ubuntu-26.04.yaml`에 고정된
architecture별 공식 `.wsl` URL과 SHA-256을 사용한다. host process가 새
temporary file로 image를 streaming download하고 checksum을 검증한 뒤에만
`wsl.exe --install --from-file ... --name Ubuntu-26.04 --no-launch`를
실행한다. 이름만 지정하는 moving `wsl --install --distribution` 경로는
사용하지 않으며 checksum mismatch는 설치 전에 hard failure다.

Windows feature 활성화, reboot 또는 최초 Linux user 생성이 필요하면
`action-required`로 종료한다. 자동 reboot나 계정 생성을 시도하지 않는다.

1. Windows가 요구하면 사용자가 직접 reboot한다.
2. `wsl.exe --distribution Ubuntu-26.04`를 한 번 실행해 Linux user를 만든다.
3. PowerShell을 다시 열고 `plan --component wsl`을 재실행한다.
4. digest가 같아도 action과 target preimage를 다시 검토한다.
5. 현재 plan의 exact digest로 `apply --component wsl`을 재실행한다.

WSL lifecycle도 host에서 guest-owned 경로를 직접 수정하지 않는다. 준비 후
missing/stale guest-local `mds`는 Lima와 같은 guest-local checksum verifier로
자동 수렴시킨다. 이어서 bounded
`wsl.exe --distribution Ubuntu-26.04 --exec /bin/sh -c <fixed-handoff> ...`
argv로 handoff하고 host와 같은 CLI 및 embedded catalog revision일 때만
ready로 판정한다. guest shell의
`WSL_DISTRO_NAME=Ubuntu-26.04`를 통해 target은
`wsl-guest:Ubuntu-26.04`로 발견되며, 이후 guest 내부에서 `plan`, `apply`,
`doctor`를 실행한다.

## 선택 설치

전체 환경이 필요하지 않으면 component나 profile을 선택한다.

```sh
mds plan --profile minimal --format json
mds plan \
  --component neovim \
  --component notion-cli \
  --component codex \
  --format json
```

dependency는 자동 확장되지만 target-ineligible desktop app은 CLI 선택에
따라오지 않는다.

## 인증

bootstrap, apply와 doctor 이후의 service 인증은 사용자가 직접 수행한다.
bootstrap에 token, API key, browser cookie나 auth command를 추가하지 않는다.

## Target evidence

fixture smoke와 실제 target certification은 분리한다. production archive를
host bootstrap 또는 checksum-verified guest install로 실제 실행한 결과만
actual evidence 후보가 된다.

- `implemented`: 코드/fixture lane에만 사용하며 actual bundle status로 금지
- `blocked`: 실제 target에서 prerequisite/readiness가 남음
- `verified`: 해당 실제 target의 모든 필수 probe 통과

actual bundle은 `mds.target-evidence/v1`,
`capture_kind: actual-target`이어야 한다. production `mds`의 absolute,
regular, non-symlink path와 아직 존재하지 않는 output directory를 사용한다.
선택 방식은 `--all`, `--profile` 또는 하나 이상의 `--component` 중 하나다.

```sh
scripts/certify-target.sh \
  --mds "$HOME/.local/bin/mds" \
  --target macos-host:local \
  --output ./target-evidence/macos-host \
  --expected-binary-sha256 '<release-binary-sha256>' \
  --all
```

certifier는 production binary의 read-only `plan`과 `doctor`만 실행한다.
readiness가 남으면 `blocked` bundle을 보존하고 non-zero로 종료한다. 이
종료를 무시해 `verified`로 게시하지 않는다.

bundle을 publication identity와 함께 다시 검증한다.

```sh
scripts/verify-target-evidence.sh \
  --bundle ./target-evidence/macos-host \
  --expected-cli-revision '<exact-cli-revision>' \
  --expected-catalog-revision 'sha256:<expected-catalog-revision>' \
  --expected-plan-digest 'sha256:<reviewed-plan-digest>' \
  --expected-target macos-host:local \
  --expected-binary-sha256 '<release-binary-sha256>' \
  --max-age 24h \
  --require-publication-acceptable
```

`<exact-cli-revision>`은 production `mds --version`의 `mds version ` 뒤에
나오는 `0.1.0 (commit=<sha>, date=<RFC3339>)` identity다. verifier는 bundle의
exact file set, checksum, secret-free material, CLI/catalog/plan/target
identity, on-disk binary SHA-256과 recomputed status를 확인한다.

`--require-publication-acceptable`은 `blocked`를 `verified`로 바꾸지 않는다.
완전한 표준 target에서 모든 남은 결과가 사용자가 직접 해결해야 하는
`action-required`일 때만 정직한 manual exception으로 허용한다. planned
`unready`/`conflict`, `unsupported`, 불완전 target은 거부한다. 모든 component의
완전한 실제 검증이 필요한 별도 검사에서는 `--require-verified`를 사용한다.

tag workflow는 exact commit의 성공한 target-certification run만 GitHub Actions
API로 조회한다. artifact 이름은
`target-evidence-<kind>-<commit>-<run-id>-<attempt>`이며 네 표준 target별로
정확히 하나여야 한다. 다운로드한 bundle은 Gitleaks 검사를 거친 뒤 release와
재결합해 promotion한다. promotion report는 publish 단계에서 한 번 더 검증해
stable `release-promotion.json` asset으로 게시한다.

actual-target job은 `target-certification` 보호 environment와 다음 네
allowlisted label만 사용한다.

- `mds-macos-host`
- `mds-windows-host`
- `mds-wsl-guest`
- `mds-lima-guest`

target ID와 runner label은 workflow 안에서 exact pair로 다시 검증한다.
self-hosted runner는 target별 전용 OS 계정과 전용 작업 디렉터리에서 실행하고,
저장된 API key, browser session, SSH agent, cloud credential 또는 repository
secret을 두지 않는다. workflow의 자동 `GITHUB_TOKEN`은 `contents: read`로만
제한하고 environment에는 secret을 등록하지 않는다. 보호 environment reviewer는
requested commit, target, binary checksum과 runner label을 확인한 뒤 실행을
허용한다. 모든 third-party action은 full commit SHA로 고정한다.

현재 Windows host, WSL Ubuntu와 Lima Ubuntu의 actual evidence는 없다. 이
상태에서 해당 target을 verified로 기록하거나 fixture로 대체하지 않는다.
