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

GuestRuntime은 생성·시작 후 host에서 guest-owned 경로를 직접 수정하지 않고,
bounded `limactl shell --tty=false ... -- mds plan --all --format json`
argv로 guest-local CLI에 handoff한다. guest plan이 host와 정확히 같은 CLI
revision과 embedded catalog revision을 보고할 때만 Lima runtime을 ready로
판정한다.

현재 host apply 입력에는 검토된 Linux archive와 checksum이 포함되지 않으므로
GuestRuntime은 binary를 다운로드·복사·설치했다고 가장하지 않는다. guest-local
`mds`가 없거나 revision이 다르면 필요한 두 revision을 담은
`action-required`로 중단한다. 사용자는 같은 release/commit의
`linux_<arch>` archive를 게시된 checksum으로 검증해 guest 안에 설치한 뒤
동일한 host apply를 다시 실행한다. binary가 catalog를 embed하므로 별도 moving
catalog download는 사용하지 않는다.

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
아니면 mutation 전에 `action-required`다.

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

Windows feature 활성화, reboot 또는 최초 Linux user 생성이 필요하면
`action-required`로 종료한다. 자동 reboot나 계정 생성을 시도하지 않는다.

1. Windows가 요구하면 사용자가 직접 reboot한다.
2. `wsl.exe --distribution Ubuntu-26.04`를 한 번 실행해 Linux user를 만든다.
3. PowerShell을 다시 열고 `plan --component wsl`을 재실행한다.
4. digest가 같아도 action과 target preimage를 다시 검토한다.
5. 현재 plan의 exact digest로 `apply --component wsl`을 재실행한다.

WSL lifecycle도 host에서 guest-owned 경로를 직접 수정하지 않는다. 준비 후
GuestRuntime은 bounded
`wsl.exe --distribution Ubuntu-26.04 --exec mds plan ...` argv로 handoff하고,
host와 같은 CLI 및 embedded catalog revision일 때만 ready로 판정한다.
검토된 Linux archive/checksum이 host apply 입력에 없으므로 missing/stale
guest-local `mds`는 자동 전송하지 않고 `action-required`로 보고한다. 사용자가
같은 release의 `linux_<arch>` binary를 게시된 checksum으로 검증해 설치한 뒤
host apply를 재실행해야 한다. guest shell의
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

현재 Windows host, WSL Ubuntu와 Lima Ubuntu의 actual evidence는 없다. 이
상태에서 해당 target을 verified로 기록하거나 fixture로 대체하지 않는다.
