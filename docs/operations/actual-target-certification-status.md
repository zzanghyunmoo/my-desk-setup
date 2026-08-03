---
schema: mds.actual-target-certification-status/v1
ticket: ZZA-101
release_commit: 61ede4860a9a2484a03693e4feed3cccc32c01c2
release_version: v0.1.0
status: pending
---

# Actual target certification status

PR #2의 merge commit `61ede4860a9a2484a03693e4feed3cccc32c01c2`를
동일한 immutable cohort로 macOS host, Windows host, WSL Ubuntu 26.04 guest와
Lima Ubuntu 26.04 guest에서 인증하고 `v0.1.0` release로 promotion하기 위한
상태 문서다. 인증 결과가 없는 항목을 완료로 표시하지 않는다.

## Target checklist

- [ ] `macos-host:local` verified evidence
- [ ] `windows-host:local` verified evidence
- [ ] `wsl-guest:Ubuntu-26.04` verified evidence
- [ ] `lima-guest:mds` verified evidence

네 bundle은 동일한 commit과 cohort에 결합되고 capture freshness/window 및
exact released `mds`/`mds-evidence` SHA-256 검증을 통과해야 한다.

## Promotion checklist

- [ ] 네 verified bundle을 deterministic promotion archive로 묶는다.
- [ ] `v0.1.0` draft release asset을 업로드하고 재다운로드 byte 검증을 통과한다.
- [ ] release를 publish하고 permanent evidence asset을 확인한다.
- [ ] Notion 기능 현황·티켓, workspace KB/work evidence와 Linear ZZA-101을 닫는다.

## User-owned prerequisites

Runner 등록 token 입력, privileged prerequisite와 component별 인증은 사용자가
직접 수행한다. Raw guest creation nonce, API token, browser session과 machine-local
path는 evidence, PR, ticket 또는 GitHub metadata에 기록하지 않는다.
