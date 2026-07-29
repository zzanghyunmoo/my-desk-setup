# AGENTS.md

이 저장소에서 작업하는 모든 에이전트가 따르는 프로젝트 규칙이다.

## 작업 흐름

- `main`에 직접 기능 commit 또는 push를 하지 않는다.
- Linear 티켓을 확인한 뒤 별도 브랜치에서 작업하고 PR로 반영한다.
- 계획과 work evidence는 상위 워크스페이스의 `docs/` 규약을 따른다.
- 기존 사용자 변경과 복구 지점을 보존하고, 파괴적 작업은 명시 승인 뒤 수행한다.

## 구현 원칙

- Go CLI는 OS별 side effect와 순수한 planning/resolution 로직을 분리한다.
- all/profile/component/interactive 선택은 하나의 resolver를 공유한다.
- 인증, login, token 저장은 실행하지 않고 사용자가 수행할 명령만 안내한다.
- 지원하지 않거나 검증할 수 없는 상태를 성공으로 표시하지 않는다.
- 정상 `apply`는 잠금 버전을 몰래 올리지 않으며 명시적 `update`만 버전을
  이동한다.

## 검증

변경에 맞는 `go test ./...`, `go vet ./...`, `go build ./cmd/mds`를 기본으로
실행한다. 실제 OS나 VM이 필요한 검증은 실행 대상과 결과 또는 미실행 이유를
work evidence에 기록한다.
