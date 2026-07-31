# Recovery Operations

## 두 recovery 영역

`my-desk-setup` recovery는 서로 다른 두 영역을 다룬다.

1. 저장소 identity transition 전의 Git history와 ref 복구
2. target apply/update 도중의 journal, receipt와 actual state 복구

두 영역 모두 기존 상태를 먼저 보존하고 exact identity를 확인한다. 실패를
지우거나 새 성공으로 덮어써서 복구한 것처럼 만들지 않는다.

## Repository transition recovery

기존 `settings` 저장소를 rename/orphan 전환하기 전에 complete-history bundle과
exact ref inventory를 저장소 밖의 owner-only directory에 만든다.

```sh
MDS_RECOVERY_DIR='<owner-only-directory-outside-checkout>'
mkdir -p "$MDS_RECOVERY_DIR"
chmod 700 "$MDS_RECOVERY_DIR"
git show-ref --head > "$MDS_RECOVERY_DIR/refs-before-transition.txt"
chmod 600 "$MDS_RECOVERY_DIR/refs-before-transition.txt"
git bundle create "$MDS_RECOVERY_DIR/settings-all-refs.bundle" --all HEAD
chmod 600 "$MDS_RECOVERY_DIR/settings-all-refs.bundle"
git bundle verify "$MDS_RECOVERY_DIR/settings-all-refs.bundle"
git bundle list-heads "$MDS_RECOVERY_DIR/settings-all-refs.bundle"
shasum -a 256 "$MDS_RECOVERY_DIR/settings-all-refs.bundle"
```

검증 기록에는 최소한 다음을 남긴다.

- bundle SHA-256
- `HEAD`, local branch, remote-tracking branch의 exact ref와 commit SHA
- `git bundle verify`의 complete-history 결과
- source repository remote URL과 default branch
- bundle file과 parent directory의 owner-only permission

독립 directory에서 restore rehearsal도 수행한다.

```sh
git clone \
  "$MDS_RECOVERY_DIR/settings-all-refs.bundle" \
  "$MDS_RECOVERY_DIR/recovery-check"
git -C "$MDS_RECOVERY_DIR/recovery-check" fsck --full --strict
git -C "$MDS_RECOVERY_DIR/recovery-check" show-ref --head
awk '{print $1}' "$MDS_RECOVERY_DIR/refs-before-transition.txt" |
  sort -u |
  while read -r sha; do
    git -C "$MDS_RECOVERY_DIR/recovery-check" cat-file -e "$sha"
  done
```

rehearsal의 ref/SHA를 `refs-before-transition.txt`와 대조한다. bundle이
완전하다는 추정이나 파일 존재만으로 gate를 통과하지 않는다.

### 파괴적 승인 경계

다음 동작은 일반 구현 승인, PR 생성 승인 또는 "알아서 approve"로 위임하지
않는다.

- GitHub repository rename
- orphan history 생성/교체
- force-push
- 기존 remote branch 삭제
- root workspace의 submodule URL/path/gitlink 전환

검증한 bundle, exact refs/SHA, 실행할 command, 새 remote, force-push target과
branch 삭제 목록을 한 approval packet으로 제시한다. 현재 turn에서 사용자가
명시적으로 파괴적 전환을 승인하기 전에는 어느 명령도 실행하지 않는다.

현재 repository transition은 승인 전이며 완료되지 않았다. 문서나 release
artifact가 존재해도 이 상태를 완료로 바꾸지 않는다.

## Transition 실패 시 복구

전환 도중 실패하면 추가 rewrite를 중단하고 다음 순서로 복구한다.

1. remote와 local의 현재 exact refs/SHA를 새 inventory로 캡처한다.
2. 원래 bundle SHA-256과 `git bundle verify`를 다시 확인한다.
3. 독립 clone에서 되돌릴 commit/ref가 실제 object인지 확인한다.
4. 원격 복구 command와 영향받는 branch를 새 파괴적 승인 packet으로 만든다.
5. 승인 뒤 exact SHA만 복구한다. moving branch name을 복구 근거로 쓰지 않는다.
6. root gitlink는 child remote 복구가 검증된 뒤 별도로 갱신한다.

root pointer가 child feature head, merge commit 또는 복구할 old commit 중
무엇을 가리키는지 항상 exact SHA로 기록한다.

## Target-local state

기본 state root:

- macOS/guest:
  `${XDG_STATE_HOME:-$HOME/.local/state}/my-desk-setup`
- Windows:
  `%LOCALAPPDATA%\my-desk-setup\state`

target ID마다 별도 hashed directory를 사용하고, catalog 경로별 lease는 state
root에 둔다.

```text
<state-root>/
├── catalog-<catalog-path-hash>.writer.lock
└── <target-id-and-hash>/
    ├── writer.lock
    ├── journal.jsonl
    └── receipts/
        ├── sha256-<plan-digest>.json
        └── sha256-<plan-digest>.partial.json
```

directory는 owner-only, file은 regular non-symlink여야 한다. `--state-root`에
filesystem root, symlink 또는 공유 writable directory를 사용하지 않는다.
writer lock file은 성공 뒤에도 남지만 advisory lease는 process 종료와 함께
자동 해제된다. 파일 존재 자체를 active writer로 해석하거나 수동 삭제하지 않는다.
digest별 `.json`은 마지막 complete receipt이고 `.partial.json`은 현재 incomplete
재시도 결과다. incomplete 결과는 complete receipt를 대체하지 않으며, 같은 digest가
나중에 complete로 끝난 뒤에만 대응하는 partial receipt를 삭제한다.

## Apply 중단과 재개

process 종료, installer 실패 또는 `action-required`가 발생하면 journal과
receipt를 삭제하지 않는다.

1. 실패 당시 receipt와 journal을 읽기 전용으로 보존한다.
2. `mds doctor`로 현재 local state를 다시 관찰한다.
3. 같은 selection으로 `mds plan`을 새로 만든다.
4. catalog revision, target facts, action과 digest를 다시 검토한다.
5. 현재 plan digest로 `mds apply`를 실행한다.

```sh
mds doctor --profile owner --format json
mds plan --profile owner --format json
mds apply \
  --profile owner \
  --plan-digest 'sha256:<reviewed-current-digest>' \
  --format json
```

이미 설치되고 verified인 component는 observation을 통해 no-op으로 수렴한다.
실패 node의 downstream만 다시 차단되며 독립 node 결과는 보존된다.

state file을 직접 수정해 `ready`를 만들지 않는다. receipt는 actual
verification 결과이지 desired-state 입력이 아니다.

## Stale plan

plan 뒤 target facts, catalog/lock 또는 observed preimage가 바뀌면 apply/update는
첫 mutation 전에 중단해야 한다. 이때 old digest를 강제로 재사용하지 않는다.

```sh
mds plan <same-selection> --format json
# 새 action과 digest 검토
mds apply <same-selection> \
  --plan-digest 'sha256:<new-reviewed-digest>' \
  --format json
```

stale 실패 뒤 state directory가 새로 생겼거나 adapter command가 실행됐다면
safety invariant 위반으로 취급하고 release하지 않는다.

## User-owned config conflict

기존 Neovim config, launcher 또는 다른 path에 `mds` ownership marker가 없으면
자동 교체하지 않는다.

1. `doctor`의 `user-owned-or-version-conflict`와 path를 확인한다.
2. 기존 config는 사용자가 직접 backup/merge한다.
3. 사용자가 ownership 전환을 결정하기 전에는 apply/update를 반복하지 않는다.
4. `mds` state나 ownership marker를 수동으로 위조하지 않는다.

## Update 실패

update는 old/new lock, catalog preimage와 update digest를 검증한다. 실패 시
기존 complete receipt와 새 partial receipt를 구분해 보존하고, catalog의 실제
lock도 다시 읽는다.

```sh
git diff -- catalog/locks/versions.lock.yaml
mds doctor --component '<component-id>' --format json
```

lock과 target이 다른 version을 가리키면 update를 재실행하기 전에 candidate,
catalog revision과 actual installed state로 새 update plan을 만든다. Git
commit이나 rollback은 사용자가 소유하며 `mds`가 자동으로 history를 rewrite하지
않는다.

## Evidence recovery

target evidence bundle은 source commit, CLI revision, catalog revision,
plan digest, target fingerprint와 실제 실행한 on-disk binary SHA-256을 함께
보존한다. exact file set은
`manifest.json`, `plan.json`, bounded `doctor.json`, `checksums.txt`다.
fixture와 actual directory를 섞거나 blocked evidence를 verified directory로
복사하지 않는다.

evidence를 다시 수집할 때는 production artifact checksum부터 재검증하고 같은
target에서 전체 certification을 새로 실행한다. verifier 통과는 bundle
무결성을 뜻하며 target outcome이 `blocked`인 경우까지 성공으로 바꾸지 않는다.

publication gate에서는 expected commit+cohort identity와 binary checksum을
모두 명시한다. `blocked`는 `verified`로 정규화하지 않으며 manual exception
없이 promotion을 차단한다.

```sh
scripts/verify-target-evidence.sh \
  --bundle '<bundle-dir>' \
  --expected-cli-revision '<exact-cli-revision>' \
  --expected-catalog-revision 'sha256:<expected-catalog-revision>' \
  --expected-plan-digest 'sha256:<expected-plan-digest>' \
  --expected-target '<target-id>' \
  --expected-binary-sha256 '<release-binary-sha256>' \
  --expected-cohort 'cert-<UTC YYYYMMDDThhmmssZ>-<commit8>' \
  --max-age 24h \
  --require-verified
```

tag promotion은 동일한 commit+cohort의 `macos-host:local`,
`windows-host:local`, `wsl-guest:Ubuntu-26.04`, `lima-guest:mds` verified
bundle을 각각 정확히 하나 요구한다. 누락, 중복, stale,
commit/cohort/catalog/plan/target/binary mismatch가 있으면 새 release
publication을 중단한다. 성공한 `release-promotion.json`은 GitHub Release의
영구 asset이므로 임시 Actions artifact가 만료된 뒤에도 gate 결과를 확인할 수
있다.
