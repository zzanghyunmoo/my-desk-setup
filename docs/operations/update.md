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
조회한다. rate limit, DNS failure, unsupported provider와 no-change candidate는
lock/state를 바꾸지 않고 실패한다.

## Reviewed candidate

재현 가능한 적용에는 exact candidate JSON을 권장한다.

```json
{
  "component_id": "typescript",
  "version": "6.0.3",
  "source": "npm registry",
  "provenance": "https://www.npmjs.com/package/typescript/v/6.0.3"
}
```

reviewed vendor release는 platform별 artifact URL, SHA-256, format과 executable
정보도 포함한다. provenance와 artifact URL은 absolute HTTPS여야 하고 SHA-256은
64자리 hexadecimal이어야 한다. unknown field와 1 MiB를 넘는 candidate file은
거부한다.

```sh
mds update \
  --catalog ./catalog \
  --candidate ./candidate.json \
  --format json
```

출력 `mds.update/v1`에서 다음을 검토한다.

- `component_id`, `lock_key`
- `before_catalog_revision`, `after_catalog_revision`
- `old`, `new` version/provenance/artifacts
- `target_plan` action, blocker와 plan digest
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

어느 하나라도 달라지면 candidate와 current state로 preview부터 다시 한다.
stale digest를 강제로 우회하지 않는다.

적용 결과는 `mds.update-result/v1`으로 update digest, 새 catalog revision과
target receipt를 제공한다. Git commit은 자동으로 만들지 않는다.

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

## 실패와 재개

update 실패 뒤에는 다음 세 identity를 따로 확인한다.

1. catalog lock이 가리키는 requested version
2. target에서 관찰한 installed version
3. receipt의 verified version

기존 complete receipt를 삭제하지 않고 partial result와 함께 보존한다. 현재
lock이 이미 변경됐다면 old preview digest를 재사용하지 말고 새 catalog
preimage로 update plan을 다시 만든다.

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
