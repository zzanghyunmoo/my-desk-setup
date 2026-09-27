---
title: "Windows VS Code 검사에서 GUI가 실행되는 문제"
date: "2026-09-09"
category: "runtime-errors"
module: "Windows host VS Code extensions"
problem_type: runtime_error
component: tooling
symptoms:
  - "game-development doctor가 JSON 없이 대기하며 VS Code GUI를 실행함"
  - "확장 검사에서 CLI 대신 Code.exe 앱 프로세스가 실행됨"
root_cause: wrong_api
resolution_type: code_fix
severity: high
tags: ["windows", "vscode", "electron", "doctor"]
---

# Windows VS Code 검사에서 GUI가 실행되는 문제

## Problem

Windows host의 확장 adapter가 `Code.exe`에 확장 관리 인자만 전달했다.
명령줄 도구가 아니라 Electron 앱이 실행되어 read-only 검사가 GUI 수명에
묶였다. 이 문서의 수정은 로컬 작업 브랜치에서 검증했으며 아직 배포되지 않았다.

## Symptoms

- 전체 profile 검사가 60초 실행 제한 안에 결과를 반환하지 않았다.
- 해당 MDS 프로세스의 자식으로 `Code.exe --list-extensions --show-versions`와
  GUI renderer/GPU 프로세스가 관찰됐다.
- 공식 `code` launcher로 같은 확장 목록 요청을 실행하면 정상 종료했다.

## What Didn't Work

검사 시간을 늘리거나 앱 실행 파일의 존재만 확인하는 것으로는 CLI 진입점
선택 문제가 해결되지 않는다. 기존 fixture는 실행 파일 경로만 확인했으므로
실제 GUI 실행과 명령줄 실행을 구분하지 못했다.

## Solution

[확장 adapter](../../../internal/adapters/host/vscode_extensions.go)의 공통 command
생성 경로에서 설치된 `bin/code.cmd`가 지정한 `cli.js` 위치를 읽는다.
버전별 하위 폴더가 있는 설치와 기존 폴더 구조를 모두 처리한다.

`Code.exe`에 그 스크립트를 첫 인자로 전달하고, 해당 자식 프로세스에만
`ELECTRON_RUN_AS_NODE=1`과 빈 `VSCODE_DEV`를 적용한다. shell에서 launcher 내용을
실행하거나 문자열로 인자를 보간하지 않는다. 정규화한 문자열 경로가 설치 폴더를
벗어나거나, 참조 파일이 없거나 일반 파일이 아니면 conflict로 종료하며 GUI로
대체하지 않는다.

기존에 선택한 VS Code 설치와 그 내부의 vendor 관리 링크는 신뢰한다. 이 검사는
링크의 최종 물리 경로, 설치 파일의 서명·무결성 또는 악성으로 변조된 설치를
검증하는 보안 경계가 아니다. 기존 설치의 실행 권한 범위를 확장하지 않는다.

기존 확장 버전 충돌을 자동으로 덮어쓰지 않는 정책은 변경하지 않았다.
이번 실제 설정에서는 사용자의 명시적 진행 요청에 따라 확장을 catalog pin으로
정렬한 다음 수정본의 `doctor`로 다시 검증했다.

## Why This Works

공식 Windows launcher와 같은 Node CLI 진입점을 사용하므로 확장 관리 결과를
stdout과 종료 코드로 받을 수 있다. 실행 및 조회가 같은 command 생성 경로를
사용해 어느 한쪽만 GUI를 실행하는 회귀를 방지한다.

## Prevention

- [회귀 테스트](../../../internal/adapters/host/vscode_extensions_test.go)는 일반 및
  버전별 설치 구조에서 목록 조회와 설치가 모두 CLI 모드인지 확인한다.
- launcher가 없는 설치를 확장 미설치로 잘못 취급하거나 GUI로 실행하지 않는다.
- 지원하지 않는 launcher 문법, 문자열 경로 이탈, script 누락·디렉터리도 조회·설치 모두
  conflict로 차단하며 외부 명령을 실행하지 않는지 회귀 테스트로 확인한다.
- 수정 전 두 테스트가 실패했고, 수정 후 host adapter 전체 테스트가 통과했다.
- 실제 Windows에서 `mds doctor --profile game-development --format json`이
  exit 0, `ready: true`, 7개 ready check를 반환했다.
- Unity CLI beta.8, Editor 6000.3.23f1, C# 2.151.28, C# Dev Kit 3.32.194,
  Unity 확장 1.3.1을 확인했다. `go vet ./...`, CLI build와 diff 검사도 통과했다.
- 전체 Windows 테스트는 별도 symlink 권한 및 bootstrap shell/environment 문제로
  실패했다. native PowerShell 재실행에서도 권한 오류가 재현됐다. 이를 해결하려고
  테스트를 약화하거나 전역 보안 설정을 변경하지 않았다. race 검사는 cgo 부재로
  실행하지 못했다. 이 기록은 전체 release 인증이 아니다.
- 설치 검사의 ready는 로그인이나 Unity 라이선스 활성화를 의미하지 않는다.

## Related Issues

- [Unity 개발 환경 도입 이슈](https://github.com/zzanghyunmoo/my-desk-setup/issues/21)
- [Component catalog의 소유권 및 profile 계약](../../components/catalog.md)
