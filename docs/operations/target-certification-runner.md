# Actual-target certification runner 준비

이 문서는 `.github/workflows/target-certification.yml`의 `Actual target`
job을 실행할 네 전용 self-hosted runner를 준비하는 운영 절차다. runner 등록에
필요한 GitHub 인증과 registration token 입력은 사용자가 직접 한다. `mds`와 이
저장소의 스크립트는 runner 등록, 로그인 또는 credential 보관을 자동화하지 않는다.

## 고정 target과 runner

runner 하나는 아래 표의 custom label 하나와 target 하나만 담당한다. 동일
runner에 둘 이상의 `mds-*` custom label을 부여하지 않는다.

| Target ID | 실행 위치 | Custom label | Production `mds` path | Released certifier path | Guest ownership record |
| --- | --- | --- | --- | --- | --- |
| `macos-host:local` | 실제 macOS host | `mds-macos-host` | `/usr/local/bin/mds` | `/usr/local/bin/mds-evidence` | 없음 |
| `windows-host:local` | 실제 Windows host | `mds-windows-host` | `C:/ProgramData/my-desk-setup/bin/mds.exe` | `C:/ProgramData/my-desk-setup/bin/mds-evidence.exe` | 없음 |
| `wsl-guest:Ubuntu-26.04` | 해당 WSL guest 내부 | `mds-wsl-guest` | `/usr/local/bin/mds` | `/usr/local/bin/mds-evidence` | `wsl-Ubuntu-26.04.json` |
| `lima-guest:mds` | 해당 Lima guest 내부 | `mds-lima-guest` | `/usr/local/bin/mds` | `/usr/local/bin/mds-evidence` | `lima-mds.json` |

workflow는 target ID와 label의 exact pair 및 위 두 고정 production path를 다시
선택한다. Machine-local path는 dispatch input으로 받지 않는다. Raw guest nonce는
owner-only host ownership record 밖으로 내보내지 않으며 runner process
environment에도 설정하지 않는다.

네 certification profile은 해당 target에서 자동 설치 가능한 v1 catalog
component 전체를 선택한다. WSL amd64 profile은 Flutter를 포함한다. Lima arm64는
공식 Linux arm64 Flutter artifact가 없어 그 component만 `action-required`로 명시
제외한다. Host profile도 desktop app, terminal, guest lifecycle과 host coding
agent 중 자동화 가능한 전체를 포함한다. 일반 `all`/`owner` profile의
수동·platform-limited 상태 계약은 그대로 유지한다.

## 1. GitHub 보호 경계

repository 관리자가 다음 외부 상태를 먼저 만든다.

1. `main`과 certification에 사용할 ref, `v*` tag를 protected ruleset으로
   설정하고 tag update/delete를 금지한다.
2. repository environment `target-certification`을 만든다.
3. environment에 required reviewer와 protected branch/tag deployment rule을
   설정한다.
4. environment secret은 등록하지 않는다.
5. 각 job 직전에 이 개인 repository의 repository-level `--ephemeral`
   runner를 등록하고 표의 custom label 하나만 부여한다. 개인 repository에는
   organization/enterprise runner group을 전제하지 않는다.

`Actual target` job은 non-fork `workflow_dispatch`, `github.ref_protected`,
`target-certification` reviewer 승인을 모두 요구한다. Dispatcher는 commit이나
raw guest creation nonce나 machine-local path를 입력할 수 없고, 네 dispatch는 같은 canonical
`cert-<UTC YYYYMMDDThhmmssZ>-<commit8>` cohort를 사용한다. Cohort timestamp는
첫 dispatch 직전에 GitHub 서버 시각으로 발급한다.

```sh
scripts/certification-clock.sh cohort '<approved-full-commit-sha>'
```

네 capture는 5분의 promotion clock skew를 제외하고 이 시각부터 4시간 안에
끝나야 같은 release promotion에 포함된다. 각 runner는 capture 전에
`scripts/certification-clock.sh verify`로 GitHub 서버 UTC와 로컬 UTC의 절대
오차가 60초 이하인지 확인하며, 이를 넘으면 runner 시계를 동기화한 뒤 새
dispatch를 시작한다.

## 2. 전용 OS 계정과 one-job ephemeral runner

각 target에서 interactive 개발 계정과 분리된 계정을 만든다. 권장 계정명과
작업 root는 다음과 같다.

- macOS host: `mds-cert-macos`
- Windows host: `mds-cert-windows`
- WSL guest: `mds-cert-wsl`
- Lima guest: `mds-cert-lima`
- 작업 root: 계정 전용 GitHub Actions runner directory와 `_work`

계정에는 blanket administrator나 passwordless `sudoers` 권한을 주지 않는다.
API key, browser profile, SSH agent, cloud credential, 개인 Git config와
repository secret을 복사하지 않는다. guest의 system package와 Docker daemon
준비처럼 service job에서 prompt할 수 없는 prerequisite는 dispatch 전에
운영자가 별도로 완료한다. Docker 기능 검증에 필요한 target-local group
membership은 reviewed prerequisite로만 허용한다. GitHub runner 자체의
repository-scoped registration material은 해당 한 job 동안만 owner-only
runner directory에 둔다.

Host production binary 설치, reviewed `plan`/`apply`, local guest seed와
ownership record 생성, host actual-target runner 실행은 모두 동일한 전용 host
계정과 동일한 home에서 수행한다. macOS/Lima wave는 `mds-cert-macos`,
Windows/WSL wave는 `mds-cert-windows`가 이 경계를 소유한다. 다른 interactive
계정에서 만든 guest나 ownership record를 전용 계정으로 복사해 인증하지 않는다.

GitHub의 현재 self-hosted runner 설치 문서에서 target OS/architecture의 exact
archive와 checksum을 확인한 뒤 사용자가 runner를 등록한다. 등록 시 repository
URL, `--ephemeral`, 기본 `self-hosted` label, 표의 custom label 하나와 전용 work
directory를 선택한다. `--no-default-labels`는 workflow의 `self-hosted` 요구와
충돌하므로 사용하지 않는다. Registration token 입력과 실제 등록 명령 실행은 사용자가
직접 하고 이 runbook, shell history 또는 repository에 기록하지 않는다.
Persistent `svc.sh`/Windows service 등록은 사용하지 않는다. 전용 계정의
foreground one-job process 또는 운영자가 만든 one-shot service로 한 job만
받은 뒤 process가 종료돼야 한다.

Windows runner는 등록 전에 현재 지원되는 Git for Windows를 설치하고, runner
전용 계정의 `PATH`에서 `git.exe`와 Git Bash의 `bash.exe`가 모두 발견되는지
확인한다. Workflow checkout과 actual-target step은 각각 Git과 Bash를 필요로 한다.
Gitleaks 설치 step은 Windows PowerShell 5.1(`powershell.exe`)을 명시적으로
사용하므로 PowerShell 7(`pwsh`)은 필수 prerequisite가 아니다. Runner process를
시작할 같은 계정과 환경에서 다음 preflight가 모두 성공해야 한다.

```powershell
git --version
bash --version
$PSVersionTable.PSVersion
bash -lc 'git --version && grep --version && curl --version'
```

job 시작 전에 다음을 확인한다.

- runner process 계정과 work directory owner가 표의 전용 계정이다.
- Windows runner는 위 Git/Bash/curl/Windows PowerShell preflight를 통과했다.
- runner UTC와 GitHub 서버 UTC의 절대 오차가 60초 이하다.
- runner에 다른 `mds-*` label이 없다.
- work directory에 기존 checkout이나 credential file이 없다.
- release manifest의 target별 `mds-evidence` asset을 표의 고정 path에 설치했고
  그 raw asset SHA-256을 dispatch input과 대조했다.
- host와 guest runner process 모두 raw guest creation nonce 환경값이 없다.
- guest runner process는 section 4의 read-only preparation이 끝날 때까지 시작하지 않는다.

job 종료 뒤 runner가 GitHub에서 자동 deregister됐는지 확인한다. Process를
종료하고 owner-only runner directory의 registration credential, `_diag`와
`_work`를 scrub한 뒤 다음 job은 새 directory와 새 registration으로 시작한다.
실패·`blocked` bundle은 필요하면 권한이 제한된 상태에서 진단한 뒤 업로드하지
않고 같은 scrub 절차를 적용한다.

## 3. Guest committed ownership 확인

이 단계는 WSL/Lima runner에만 적용한다. ownership record는 guest가 아니라
guest를 만든 host의 `mds` 실행 계정 아래에 있다.

```text
~/.local/state/my-desk-setup/guest-ownership/<provider>-<name>.json
```

Lima는 `provider=lima`, `name=mds`; WSL은 `provider=wsl`,
`name=Ubuntu-26.04`다. host에서 record가 regular non-symlink file이고 mode
`0600`인지 확인한 뒤 JSON을 검증한다.

```sh
record="$HOME/.local/state/my-desk-setup/guest-ownership/lima-mds.json"
test -f "$record" && test ! -L "$record"
test "$(stat -f '%Lp' "$record" 2>/dev/null || stat -c '%a' "$record")" = 600
jq -e '
  .schema_version == "mds.guest-ownership/v3" and
  .provider == "lima" and
  .name == "mds" and
  .phase == "committed" and
  (.image_url | type == "string" and length > 0) and
  (.image_sha256 | test("^[0-9a-f]{64}$")) and
  (.creation_nonce | test("^[0-9a-f]{64}$"))
' "$record" >/dev/null
```

위 POSIX 절차는 macOS host의 Lima record에 적용한다. WSL record는 Windows
host의 사용자 profile에 있으므로 PowerShell에서 별도로 검사한다.

```powershell
$record = Join-Path $env:USERPROFILE `
  ".local\state\my-desk-setup\guest-ownership\wsl-Ubuntu-26.04.json"
$item = Get-Item -LiteralPath $record -Force -ErrorAction Stop
if ($item.PSIsContainer -or
    (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
  throw "WSL ownership record must be a regular non-reparse file"
}

$current = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$allowed = @(
  $current,
  "S-1-5-18",       # LocalSystem
  "S-1-5-32-544"    # Builtin Administrators
)
$acl = Get-Acl -LiteralPath $record
$ownerAccount = [Security.Principal.NTAccount]$acl.Owner
$owner = $ownerAccount.Translate(
  [Security.Principal.SecurityIdentifier]
).Value
if ($owner -ne $current) {
  throw "WSL ownership record must be owned by the mds host user"
}
foreach ($rule in $acl.Access) {
  $sid = $rule.IdentityReference.Translate(
    [Security.Principal.SecurityIdentifier]
  ).Value
  if ($rule.AccessControlType -eq
      [Security.AccessControl.AccessControlType]::Allow -and
      $allowed -notcontains $sid) {
    throw "WSL ownership record ACL grants an unexpected principal"
  }
}

$ownership = Get-Content -LiteralPath $record -Raw |
  ConvertFrom-Json -ErrorAction Stop
if ($ownership.schema_version -ne "mds.guest-ownership/v3" -or
    $ownership.provider -ne "wsl" -or
    $ownership.name -ne "Ubuntu-26.04" -or
    $ownership.phase -ne "committed" -or
    [string]::IsNullOrWhiteSpace($ownership.image_url) -or
    $ownership.image_sha256 -cnotmatch "^[0-9a-f]{64}$" -or
    $ownership.creation_nonce -cnotmatch "^[0-9a-f]{64}$") {
  throw "WSL ownership record identity is invalid"
}

$markerScript = @'
set -eu
path=/etc/mds/image-identity-v1
[ -f "$path" ] && [ ! -L "$path" ] || exit 74
metadata=$(/usr/bin/stat -c '%u:%g:%a' "$path")
case "$metadata" in
  0:0:600|0:0:640|0:0:644) ;;
  *) exit 74 ;;
esac
/bin/cat "$path"
'@
$markerText = & wsl.exe --distribution Ubuntu-26.04 --user root `
  --exec /bin/sh -eu -c $markerScript
if ($LASTEXITCODE -ne 0) {
  throw "cannot read WSL root-owned image identity marker"
}
$marker = @{}
foreach ($line in $markerText) {
  $key, $value = $line -split "=", 2
  if ($key -and $value) { $marker[$key] = $value }
}
$domain = [Text.Encoding]::UTF8.GetBytes(
  "mds.guest-creation-nonce/v1" + [char]0
)
[byte[]]$nonceBytes = for ($index = 0;
    $index -lt $ownership.creation_nonce.Length; $index += 2) {
  [Convert]::ToByte($ownership.creation_nonce.Substring($index, 2), 16)
}
$material = New-Object byte[] ($domain.Length + $nonceBytes.Length)
[Array]::Copy($domain, 0, $material, 0, $domain.Length)
[Array]::Copy($nonceBytes, 0, $material, $domain.Length, $nonceBytes.Length)
$sha = [Security.Cryptography.SHA256]::Create()
$expectedCommitment = "sha256:" + (($sha.ComputeHash($material) |
  ForEach-Object { $_.ToString("x2") }) -join "")
$sha.Dispose()

if ($marker.schema -ne "mds.guest-image/v3" -or
    $marker.image_revision -cne
      ("sha256:" + $ownership.image_sha256) -or
    $marker.image_provenance -cne $ownership.image_url -or
    $marker.creation_nonce_commitment -cne $expectedCommitment) {
  throw "WSL live marker does not match committed host ownership"
}
```

ACL은 POSIX mode `0600`으로 해석하지 않는다. 현재 host user가 owner이고
현재 user, LocalSystem, Builtin Administrators 외 principal에 Allow rule이
없는지 검사한다. creation nonce 값은 terminal, ticket, PR, log 또는 clipboard
history에 남기지 않는다.

guest의 `/etc/mds/image-identity-v1` v3도 root-owned regular non-symlink
file이고 mode `0600`, `0640` 또는 `0644`여야 한다. root로 읽어 record의
`image_url`, `sha256:<image_sha256>` 및 owner-only raw nonce에서 계산한
`creation_nonce_commitment`와 exact match인지
확인한다. 하나라도 다르면 runner를 시작하지 말고 same-name replacement
guest 또는 stale ownership conflict를 먼저 해결한다.

## 4. Read-only preparation과 guest commitment

먼저 host 전용 계정에서 section 3의 ownership record와 live marker 검증을
완료한다. 같은 host account의 production `mds doctor --format json`을
macOS에서는 `--target macos-host:local --profile certification-macos-host`,
Windows에서는 `--target windows-host:local --profile
certification-windows-host`로 실행해 guest ownership action이 `ready`인지도
확인한다. Host plan은 guest commitment를 출력하지 않으며 commitment source로
사용하지 않는다.

guest의 exact target identity 환경(`WSL_DISTRO_NAME=Ubuntu-26.04` 또는
`LIMA_INSTANCE=mds`)에서 mutation 전에 다음 read-only preparation을 실행한다.

```sh
scripts/prepare-target-certification.sh \
  --mds-evidence /usr/local/bin/mds-evidence \
  --expected-mds-evidence-sha256 '<release-certifier-sha256>' \
  --mds /usr/local/bin/mds \
  --target lima-guest:mds \
  --expected-binary-sha256 '<release-binary-sha256>' \
  --profile certification-lima-guest > preparation.json
```

`prepare`는 release certifier와 production binary를 각각 private snapshot으로
고정하고 root-owned guest marker의 공개 domain-separated commitment를 읽는다. 같은
runtime probe로 read-only plan을 실행하며
`mds.certification-preparation/v1` JSON에 exact CLI revision, catalog revision,
target fingerprint, binary SHA-256와 plan digest만 출력한다. Apply, evidence
upload와 인증은 수행하지 않는다.

`mds-evidence prepare`가 출력한 top-level
`guest_creation_nonce_commitment`가 dispatch에 사용하는 canonical 공개
commitment다. Operator와 environment reviewer는 host doctor가 record↔live marker
검증을 통과한 동일 guest인지 확인하고, preparation의 나머지 identity를 release
manifest와 대조한 뒤 `plan_digest`와 guest target일 때만
`guest_creation_nonce_commitment`를 workflow dispatch input으로 전달한다. Certify는
mutation 직전에 같은 v3 marker를 다시 읽어 그 commitment와 exact match를
강제한다. Host target은 commitment input을 비워 둔다. Commitment는
`sha256:<64-lowercase-hex>` 공개 identity이므로 GitHub metadata에 남아도 되지만 raw
nonce나 개인 경로는 노출하지 않는다.

## 5. Dispatch 전 점검

environment reviewer는 승인 전에 다음 값을 release manifest, reviewed plan과
대조한다.

- protected ref의 `github.sha`와 네 target이 공유할 immutable cohort
- target ID와 custom runner label exact pair
- target ID가 선택하는 위 고정 production `mds` path와 그 regular non-symlink 상태
- release manifest의 on-disk binary SHA-256
- target ID가 선택하는 고정 released `mds-evidence` path, regular non-symlink
  상태와 release manifest SHA-256
- certifier가 production path를 no-follow/reparse 거부로 한 번 열어 만든
  owner-only private snapshot의 SHA-256과, 모든 subprocess가 그 snapshot만
  실행한다는 경계
- `mds-evidence prepare` JSON의 exact CLI revision, catalog revision, target
  fingerprint, binary SHA-256와 plan digest
- guest라면 host doctor가 확인한 committed record↔live marker 일치와 guest
  preparation commitment 및 certify 직전 v3 marker commitment의 exact match
- runner work directory가 이전 job의 untracked file 없이 clean한 상태

runner에는 실제 target과 production binary가 준비돼 있어야 한다. 인증되지
않은 component가 있으면 bundle은 정직하게 `blocked`가 되고 upload/promotion
대상이 아니다. Reviewer는 이를 `verified`로 바꾸지 않는다.

Verified bundle 업로드 직전에는 exact file set/checksum 검증과 Gitleaks,
credential-shaped key, raw nonce field-name 검사를 수행한다. Raw nonce는 runner
environment나 workflow input에 존재하지 않으며 evidence에는 commitment만 허용한다.

Release promotion은 선택된 네 verified bundle을 고정 timestamp와 entry
metadata를 가진 결정론적 ZIP으로 다시 묶는다. Promotion report에는 원본 Actions
artifact 이름, release evidence archive 이름과 SHA-256을 기록한다. 네 archive는
promotion report와 함께 GitHub Release asset으로 게시되고 재다운로드 byte
검증을 통과해야 하므로 30일 Actions artifact retention 뒤에도 release provenance를
재검증할 수 있다. Downloader는 원본 Actions artifact를 32 MiB, 각 exact evidence
entry를 8 MiB로 제한한다. Publisher는 첫 GitHub write 전에 release, report와 네
archive를 owner-only staging에 snapshot하고 그 same bytes를 의미 검증한 뒤
업로드·재다운로드 비교한다.

## 6. Guest 재생성과 nonce rotation

guest를 다시 만들거나 host ownership record의 creation nonce가 바뀌면 기존
one-job runner process를 즉시 중지한다.

1. 새 guest의 root-owned marker와 새 committed host record를 먼저 대조한다.
2. host doctor로 새 record↔marker identity를 확인하고 guest에서 read-only
   `prepare`를 다시 실행해 새 공개 commitment를 얻는다.
3. 새 preparation JSON의 plan digest와 commitment로 새 ephemeral runner를
   등록·dispatch한다.
4. old commitment나 plan digest를 새 dispatch에 재사용하지 않았는지 확인한다.
5. 새 commit 또는 새 ownership identity에서 certification을 다시 수행한다.

old guest, old nonce/commitment, 이전 commit 또는 다른 cohort의 evidence는 새 target
identity의 certification으로 재사용하지 않는다. Job마다 GitHub deregistration,
owner-only runner directory scrub을 사용자가 확인한다.
