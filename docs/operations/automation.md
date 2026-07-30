# Automation Contract

`mds`의 자동화 표면은 KTD15의 stable automation contract를 따른다. 성공
payload와 실패 payload는 versioned JSON이며, exit code는 호출자가 문자열을
분석하지 않고 다음 동작을 결정할 수 있게 고정한다.

## JSON stream contract

`--format json`을 선택한 명령은 stream을 다음처럼 사용한다.

- stdout: 성공 payload 또는 incomplete receipt/report 하나
- stderr: 실패 시 `mds.error/v1` envelope 하나
- stdout과 stderr 어느 쪽에도 JSON 뒤의 plain-text 문장을 추가하지 않는다.

따라서 `apply`나 `doctor`가 partial 결과를 만들었으면 stdout의 receipt/report를
그대로 보존하면서 stderr의 error envelope를 별도로 처리한다. 두 stream을 합쳐
하나의 JSON document로 해석하지 않는다.

human format은 기존처럼 stderr에 plain-text error를 출력한다. 다만 exit class는
JSON mode와 같다.

## Error envelope

error envelope는 다음 closed field set을 사용한다.

```json
{
  "schema_version": "mds.error/v1",
  "status": "stale",
  "code": "stale-plan",
  "message": "reviewed plan is stale",
  "recovery_hint": "Run mds plan again, review the new digest, and retry.",
  "details": {
    "cause": "plan digest mismatch: expected sha256:old got sha256:new"
  }
}
```

- `schema_version`, `status`, `code`, `message`는 필수다.
- `recovery_hint`는 호출자가 수행할 다음 동작을 설명한다.
- `details.cause`는 진단용 원인이고 분기 계약이 아니다. 자동화는 `code`와 exit
  code를 사용한다.
- 새 top-level field나 새 error code가 필요하면 schema version을 함께 검토한다.

## Stable exit classes

| Exit | Code | Status | 의미 |
| ---: | --- | --- | --- |
| 0 | - | - | 명령이 요청한 상태로 완료됨 |
| 1 | `internal` | `error` | I/O, catalog invariant 또는 예상하지 못한 내부 실패 |
| 2 | `invalid-input` | `error` | flag, selection, target ID 또는 입력 문서가 유효하지 않음 |
| 3 | `stale-plan` | `stale` | 검토한 digest나 plan payload가 현재 생성한 plan과 다름 |
| 4 | `action-required` | `action-required` | receipt/report가 incomplete, unsupported 또는 수동 조치를 요구함 |
| 5 | `unreachable` | `unreachable` | 선택한 target 또는 필수 discovery endpoint에 도달할 수 없음 |

exit 4에서 stdout이 비어 있다고 가정하면 안 된다. 다음처럼 두 stream을 독립
파일로 보존하면 partial result와 recovery 지시를 모두 잃지 않는다.

```sh
mds apply \
  --all \
  --plan-digest 'sha256:<reviewed-digest>' \
  --format json \
  >receipt.json \
  2>error.json
exit_code=$?
```

`exit_code=4`이면 `receipt.json`의 component별 status와 `error.json`의
`recovery_hint`를 함께 확인한다. `exit_code=3`이면 old digest를 우회하지 않고
새 `plan`부터 검토한다.
