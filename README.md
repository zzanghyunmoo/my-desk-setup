# my-desk-setup

`my-desk-setup`은 macOS·Windows 호스트와 표준 Linux 게스트에 개인 개발
환경을 재현하는 Go 기반 bootstrap control plane이다.

## 상태

현재 저장소는 ZZA-100 구현을 시작하기 위한 최소 Go baseline이다. 실제 설치
카탈로그와 `plan`, `apply`, `doctor`, `update` 동작은 기능 브랜치와 PR을 통해
추가한다.

## 설계 원칙

- 전체 설치와 선택 설치는 하나의 resolver를 사용한다.
- 호스트는 GUI·플랫폼 도구와 Linux guest lifecycle을 소유한다.
- 실제 checkout, build, test, editor 작업은 Linux guest를 표준으로 한다.
- 인증과 credential 저장은 자동화하지 않고 사용자가 직접 수행한다.
- 설치 계획과 결과는 결정적이고 재개 가능하며 검증 가능한 기록으로 남긴다.

## 빠른 확인

```sh
go test ./...
go build ./cmd/mds
go run ./cmd/mds --version
```

## 개발 흐름

`main`에 직접 기능 변경을 넣지 않는다. Linear 티켓에 대응하는 브랜치에서
작업하고 검증과 리뷰를 거쳐 PR로 병합한다.
