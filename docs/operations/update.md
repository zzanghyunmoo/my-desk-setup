# Update Operations

## Version movement 경계

normal `plan`, `apply`와 `doctor`는 committed lock을 사용한다.

- upstream `latest`를 조회하지 않는다.
- newer release를 발견해도 version을 올리지 않는다.
- manager-owned package의 implicit upgrade를 성공 조건으로 사용하지 않는다.
- agent auto-updater를 normal apply 경로로 허용하지 않는다.

pinned component의 version 이동은 `mds update`에서만 수행한다. update는
candidate, old/new lock diff, resulting target plan과 exact update digest를
검토한 뒤에만 mutation한다.

## Writable catalog

binary의 embedded catalog는 read-only이므로 update에는 writable checkout의
`--catalog`가 반드시 필요하다.

```sh
git status --short
mds update \
  --catalog ./catalog \
  --component typescript \
  --format json
```

`--component` discovery는 현재 지원하는 provider에서 exact candidate를
조회한다. npm provider는 metadata의 package/version에 대응하는 canonical
`registry.npmjs.org` tarball URL만 허용하고, tarball을 bounded download해
metadata SRI를 검증한 뒤 exact SHA-256을 계산한다. rate limit, DNS failure,
metadata/content substitution, unsupported provider와 no-change candidate는
lock/state를 바꾸지 않고 실패한다.

모든 metadata/provenance/artifact URL은 userinfo·query·fragment가 없는
absolute HTTPS여야 한다. redirect, timeout과 response-size cap 초과는 typed
`unreachable`/`invalid` failure로 닫고 persisted diagnostic에는 credential
형태의 값이나 raw URL을 남기지 않는다.

## Reviewed candidate

재현 가능한 적용에는 exact candidate JSON을 권장한다.

```json
{
  "component_id": "typescript",
  "version": "6.0.3",
  "source": "npm registry",
  "provenance": "https://www.npmjs.com/package/typescript/v/6.0.3",
  "npm": {
    "tarball": "https://registry.npmjs.org/typescript/-/typescript-6.0.3.tgz",
    "integrity": "sha512-y2TvuxSZPDyQakkFRPZHKFm+KKVqIisdg9/CZwm9ftvKXLP8NRWj38/ODjNbr43SsoXqNuAisEf1GdCxqWcdBw==",
    "sha256": "33cd0ee1beaa8c9e9d15a9da836c62ddea4c34a42d7c2d349dbc80d94165d22a"
  }
}
```

reviewed vendor release는 platform별 artifact URL, SHA-256, format과 executable
정보도 포함한다. provenance와 artifact URL은 absolute HTTPS여야 하고 SHA-256은
64자리 hexadecimal이어야 한다. unknown field와 1 MiB를 넘는 candidate file은
거부한다.

`source`와 `provenance`는 candidate에서 lock으로 그대로 보존된다. `npm`의
tarball, SRI, SHA-256도 new lock entry와 update digest에 포함되므로 review 뒤
어느 하나가 바뀌면 기존 digest는 stale이다.

```sh
mds update \
  --catalog ./catalog \
  --candidate ./candidate.json \
  --format json
```

출력 `mds.update/v2`에서 다음을 검토한다.

- `component_id`, `lock_key`
- `before_catalog_revision`, `after_catalog_revision`
- `old`, `new` version/provenance/artifacts
- `target_plan` action, blocker와 plan digest
- `compatibility_matrix`의 모든 supported target/`amd64`/`arm64` plan digest와
  vendor artifact key
- top-level update `digest`

preview에는 `--plan-digest`를 주지 않는다.

## Exact update 적용

같은 catalog, candidate와 target에서 preview한 exact digest를 사용한다.

```sh
mds update \
  --catalog ./catalog \
  --candidate ./candidate.json \
  --plan-digest 'sha256:<reviewed-update-digest>' \
  --format json
```

mutation 전에 다음 preimage를 모두 재검증한다.

- update payload digest
- current catalog revision
- old lock entry
- current target fingerprint
- resulting target plan digest
- supported target/architecture compatibility matrix

어느 하나라도 달라지면 candidate와 current state로 preview부터 다시 한다.
stale digest를 강제로 우회하지 않는다.

적용 결과는 `mds.update-result/v1`으로 update digest, 새 catalog revision과
target receipt를 제공한다. Git commit은 자동으로 만들지 않는다.

update는 state root의 catalog-scoped writer lease를 먼저 획득하고 target writer
lease를 다음에 획득한다. 두 lease 아래에서 catalog와 target preimage를 다시
검증한 뒤 old/new lock과 transaction intent를 기록한다. candidate target을
먼저 reconcile·verify하고 new lock을 마지막에 atomic publish하므로 경쟁에서
진 update는 catalog lock이나 target을 변경하지 않는다.

mise-managed component는 v1 update 대상이 아니다. 해당 update는
`versions.lock.yaml`, `mise.toml`, `mise.lock`의 3-file atomic transaction이
구현되기 전까지 preview/apply 모두 fail closed한다. 새 버전은 세 파일을 함께
리뷰해 수동 commit하고, 변경된 catalog revision으로 plan을 다시 만든다.

전역 lock은 현재 머신 한 대만을 위한 상태가 아니다. preview와 apply는
candidate component가 지원되는 macOS host, Windows host, WSL guest와 Lima
guest 각각에 대해 `amd64`/`arm64` 계획을 만든다. `vendor` installer이면 모든
eligible OS/architecture artifact URL과 checksum이 새 lock에 있어야 한다.
하나라도 빠지거나 apply 시 matrix가 달라지면 lock file을 게시하지 않는다.

```sh
git diff -- catalog/locks/versions.lock.yaml
mds doctor --component typescript --format json
```

lock diff, receipt와 doctor 결과를 검토한 뒤 사용자가 별도 Git workflow로
commit한다.

## Normal apply는 update가 아니다

아래 명령은 현재 lock을 적용할 뿐 version을 이동하지 않는다.

```sh
mds plan --component typescript --format json
mds apply \
  --component typescript \
  --plan-digest 'sha256:<reviewed-plan-digest>' \
  --format json
```

normal apply가 lock file을 쓰거나 upstream metadata를 조회하면 invariant
위반이다.

normal apply와 update apply는 committed/reviewed lock의 exact npm tarball을
redirect 없이 bounded download하고 SRI와 SHA-256을 모두 검증한다. 검증이
끝난 local `.tgz` 경로만 `bun add --global`에 전달하며 `package@version`을
registry에서 다시 resolve하지 않는다. 검증 실패 시 Bun은 실행되지 않는다.

## 실패와 재개

update 실패 뒤에는 다음 세 identity를 따로 확인한다.

1. catalog lock이 가리키는 requested version
2. target에서 관찰한 installed version
3. receipt의 verified version

기존 complete receipt를 삭제하지 않고 partial result와 transaction intent를
함께 보존한다. target mutation 뒤 lock publication 전에 중단되면 동일
`--catalog`, component/candidate와 `--plan-digest`로 같은 command를 다시
실행한다. 재실행은 old/new lock과 actual target을 관찰해 duplicate mutation
없이 lock publication을 완료하거나 typed recovery action을 반환한다. intent와
입력이 다르거나 catalog가 외부에서 바뀌었다면 old digest를 재사용하지 말고
conflict를 해소한 뒤 새 preview를 만든다.

user-owned NvChad config나 launcher는 explicit update에서도 자동 교체하지
않는다. `mds` ownership marker가 있는 managed path만 replace 대상이다.

## 인증 제외

update는 version과 local readiness만 다룬다. 다음은 update candidate,
verification, receipt와 evidence에 넣지 않는다.

- login command
- API key, token, cookie, password
- organization/account access probe
- Docker registry auth

업데이트 뒤 재인증이 필요한지는 각 upstream 도구와 사용자가 판단한다.
