# Actual-target certification runner 준비

이 문서는 `.github/workflows/target-certification.yml`의 `Actual target`
job을 실행할 네 전용 self-hosted runner를 준비하는 운영 절차다. runner 등록에
필요한 GitHub 인증과 registration token 입력은 사용자가 직접 한다. `mds`와 이
저장소의 스크립트는 runner 등록, 로그인 또는 credential 보관을 자동화하지 않는다.

## 고정 target과 runner

runner 하나는 아래 표의 custom label 하나와 target 하나만 담당한다. 동일
runner에 둘 이상의 `mds-*` custom label을 부여하지 않는다.

| Target ID | 실행 위치 | Custom label | Guest ownership record |
| --- | --- | --- | --- |
| `macos-host:local` | 실제 macOS host | `mds-macos-host` | 없음 |
| `windows-host:local` | 실제 Windows host | `mds-windows-host` | 없음 |
| `wsl-guest:Ubuntu-26.04` | 해당 WSL guest 내부 | `mds-wsl-guest` | `wsl-Ubuntu-26.04.json` |
| `lima-guest:mds` | 해당 Lima guest 내부 | `mds-lima-guest` | `lima-mds.json` |

workflow는 target ID와 label의 exact pair를 다시 확인한다. host runner에는
`MDS_EXPECTED_GUEST_CREATION_NONCE`를 설정하지 않는다. guest runner에는
host committed ownership record와 일치하는 값이 없으면 job을 실행하지 않는다.

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
guest creation nonce를 입력할 수 없고, 네 dispatch는 같은 canonical
`cert-<UTC YYYYMMDDThhmmssZ>-<commit8>` cohort를 사용한다. Cohort timestamp는
네 dispatch 시작 시각이며, 네 capture는 5분의 clock skew를 제외하고 이 시각부터
4시간 안에 끝나야 같은 release promotion에 포함된다.

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

GitHub의 현재 self-hosted runner 설치 문서에서 target OS/architecture의 exact
archive와 checksum을 확인한 뒤 사용자가 runner를 등록한다. 등록 시 repository
URL, `--ephemeral`, 기본 `self-hosted` label, 표의 custom label 하나와 전용 work
directory를 선택한다. `--no-default-labels`는 workflow의 `self-hosted` 요구와
충돌하므로 사용하지 않는다. Registration token 입력과 실제 등록 명령 실행은 사용자가
직접 하고 이 runbook, shell history 또는 repository에 기록하지 않는다.
Persistent `svc.sh`/Windows service 등록은 사용하지 않는다. 전용 계정의
foreground one-job process 또는 운영자가 만든 one-shot service로 한 job만
받은 뒤 process가 종료돼야 한다.

job 시작 전에 다음을 확인한다.

- runner process 계정과 work directory owner가 표의 전용 계정이다.
- runner에 다른 `mds-*` label이 없다.
- work directory에 기존 checkout이나 credential file이 없다.
- host runner process에는 `MDS_EXPECTED_GUEST_CREATION_NONCE`가 없다.
- guest runner process는 다음 절차가 끝날 때까지 시작하지 않는다.

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
if ($marker.schema -ne "mds.guest-image/v2" -or
    $marker.image_revision -cne
      ("sha256:" + $ownership.image_sha256) -or
    $marker.image_provenance -cne $ownership.image_url -or
    $marker.creation_nonce -cne $ownership.creation_nonce) {
  throw "WSL live marker does not match committed host ownership"
}
```

ACL은 POSIX mode `0600`으로 해석하지 않는다. 현재 host user가 owner이고
현재 user, LocalSystem, Builtin Administrators 외 principal에 Allow rule이
없는지 검사한다. creation nonce 값은 terminal, ticket, PR, log 또는 clipboard
history에 남기지 않는다.

guest의 `/etc/mds/image-identity-v1`도 root-owned regular non-symlink
file이고 mode `0600`, `0640` 또는 `0644`여야 한다. root로 읽어 record의
`image_url`, `sha256:<image_sha256>`, `creation_nonce`와 exact match인지
확인한다. 하나라도 다르면 runner를 시작하지 말고 same-name replacement
guest 또는 stale ownership conflict를 먼저 해결한다.

## 4. Guest one-job runner에 nonce 주입

guest의 one-shot GitHub runner process를 시작하는 systemd unit을
`<runner-one-shot-unit>`이라 한다. Host record에서 확인한 64자 lowercase
creation nonce를 해당 cohort/job에만 쓰는 root-owned drop-in에 설정한다.

```ini
# /etc/systemd/system/<runner-one-shot-unit>.service.d/mds-identity.conf
[Service]
Environment="MDS_EXPECTED_GUEST_CREATION_NONCE=<64-lowercase-hex>"
```

directory는 root 소유 `0755`, 파일은 root 소유 `0600`으로 만들고 다음 순서로
반영한다.

1. runner process가 실행 중이지 않은지 확인한다.
2. root 권한 editor로 drop-in을 작성한다.
3. owner/mode와 값의 `^[0-9a-f]{64}$` 형식을 root 권한으로 검사하되 값을
   stdout에 출력하지 않는다.
4. `systemctl daemon-reload` 뒤 one-shot runner를 시작한다.
5. 전용 계정 process가 값을 상속했는지만 root 권한으로 검사하고 실제 nonce는
   log에 출력하지 않는다.
6. job 종료와 deregistration 확인 뒤 unit/drop-in과 process environment에서
   nonce를 제거하고 runner directory를 scrub한다.

workflow는 guest target에서 이 값이 없거나 형식이 틀리면 capture 전에
실패한다. host target에서 값이 발견돼도 실패한다. 값은 workflow input,
repository/environment secret 또는 job-level `env`로 대체하지 않는다.

## 5. Dispatch 전 점검

environment reviewer는 승인 전에 다음 값을 release manifest, reviewed plan과
대조한다.

- protected ref의 `github.sha`와 네 target이 공유할 immutable cohort
- target ID와 custom runner label exact pair
- production `mds`의 absolute regular non-symlink path
- release manifest의 on-disk binary SHA-256
- exact CLI revision, catalog revision과 target-eligible plan digest
- guest라면 committed host record와 live marker의 provider/name/image/nonce
  일치
- runner work directory가 이전 job의 untracked file 없이 clean한 상태

runner에는 실제 target과 production binary가 준비돼 있어야 한다. 인증되지
않은 component가 있으면 bundle은 정직하게 `blocked`가 되고 upload/promotion
대상이 아니다. Reviewer는 이를 `verified`로 바꾸지 않는다.

## 6. Guest 재생성과 nonce rotation

guest를 다시 만들거나 host ownership record의 creation nonce가 바뀌면 기존
one-job runner process를 즉시 중지한다.

1. 새 guest의 root-owned marker와 새 committed host record를 먼저 대조한다.
2. old nonce가 든 one-shot drop-in을 새 nonce로 교체한다.
3. `daemon-reload` 뒤 새 ephemeral runner를 등록·실행한다.
4. old nonce가 process environment나 backup drop-in에 남지 않았는지 root
   권한으로 확인한다.
5. 새 commit에서 certification을 다시 수행한다.

old guest, old nonce, 이전 commit 또는 다른 cohort의 evidence는 새 target
identity의 certification으로 재사용하지 않는다. Job마다 GitHub deregistration,
one-shot unit/drop-in 제거와 owner-only runner directory scrub을 사용자가
확인한다.
