# Lima NvChad JVM and .NET IDE

Apple Silicon Mac의 `mds` Lima guest 안에서 하나의 managed NvChad 설정으로
Java·Kotlin·Spring Boot와 C#·ASP.NET Core·Razor·Blazor를 사용한다. 이 구성은
로그인이나 credential을 요구하지 않으며 기존 user-owned `~/.config/nvim`을
자동으로 덮어쓰지 않는다.

## 설치

Lima guest 안에서 필요한 profile을 선택하고 출력된 digest를 검토한 다음 같은
선택으로 apply한다.

```sh
LIMA_INSTANCE=mds mds plan --target lima-guest:mds --profile nvim-full --format json
LIMA_INSTANCE=mds mds apply --target lima-guest:mds --profile nvim-full \
  --plan-digest '<reviewed-sha256-digest>' --format json
LIMA_INSTANCE=mds mds doctor --target lima-guest:mds --profile nvim-full --format json
```

`LIMA_INSTANCE`는 guest 내부에서 stable target identity를 fail-closed로 지정한다. 이미
로그인 환경에 같은 값이 export되어 있다면 각 명령의 prefix는 생략할 수 있다.

- `nvim-jvm`: Java 25, Kotlin 2.3, Gradle 9.6, jdtls, Kotlin LSP, Spring Tools와
  Java/Kotlin debugger를 설치한다.
- `nvim-dotnet`: .NET SDK 10, Roslyn/Razor LSP, Bun으로 고정된 HTML LSP와
  NetCoreDbg를 설치한다. Razor와 Blazor의 HTML projection은 이름이 `html`인
  이 managed language server로 전달된다.
- `nvim-full`: 기존 C++·Go·Python IDE와 위 두 profile의 exact union이다.

기존 NvChad 설정을 명시적으로 넘길 때만 reviewed apply에 `--adopt-nvchad`를
추가한다. 원래 설정은 timestamp가 붙은 backup directory로 보존된다.

## 프로젝트 사용

처음 프로젝트를 열면 build script와 launch profile을 실행하기 전에
`:MdsTrustWorkspace`로 canonical project root를 한 번 승인한다. 승인 취소나
미승인 상태에서는 project import와 실행을 시작하지 않는다.
신뢰를 철회하려면 해당 프로젝트에서 `:MdsUntrustWorkspace`를 실행한다. 철회 즉시
그 root의 project action, watch, DAP와 project-import LSP를 종료하고 다음 실행부터
다시 승인을 요구한다. readiness를 기다리는 JVM attach와 .NET testhost attach도
root별 launch generation으로 취소되므로 철회 뒤 지연 실행되지 않는다.

`<leader>pa` 또는 `:MdsProjectAction`은 공통 순서로 다음 작업을 제공한다.

1. build
2. test
3. run
4. watch
5. debug-app
6. debug-test

Gradle 프로젝트는 repository의 `gradlew`를 사용한다. .NET 프로젝트가 여러
개이면 stable picker에서 exact `.csproj`를 선택하며 build와 debug 전 restore를
포함한다. ASP.NET Core 실행은 managed `dotnet`과 loopback URL만 허용하는
`launchSettings.json` profile을 사용한다. `applicationUrl`뿐 아니라
`ASPNETCORE_URLS`도 loopback인지 확인하며 managed run/watch/debug는 loopback
명령행 URL을 최종 적용한다. Web SDK project에 유효한 `commandName: Project`
profile이 없으면 run/watch/debug를 시작하지 않는다. profile의 대소문자 변형
`ASPNETCORE_*`와 `Kestrel__Endpoints__*` binding override는 제거한 뒤 managed
loopback 값을 적용한다. 장기 실행 작업과 debug test runner는 독립 process group으로
시작하며 `:MdsProjectCancel` 또는 신뢰 철회 시 TERM 뒤 bounded KILL로 자식까지
종료한다.

Java DAP는 jdtls의 exact debug/test bundle을, Kotlin DAP는 pinned Kotlin debug
adapter를 사용한다. Gradle debug action은 loopback debug port를 확보하고 JVM의
listen readiness를 확인한 뒤 해당 adapter를 자동 attach한다. .NET application과
ASP.NET server debugging은 선택한 project의 `TargetPath`, cwd, environment와
강제 loopback URL을 DAP configuration에 반영한다. `debug-test`는
`VSTEST_HOST_DEBUG=1`로 시작한 testhost PID에 NetCoreDbg를 attach한다. Doctor는
console application, ASP.NET server와 test 각각에서 구조적 breakpoint 결과를
요구한다.

## 복구와 확인

managed config나 runtime tree가 drift되면 같은 selection으로 새 plan digest를
검토하고 apply한다. 정상 repeat apply는 모든 action이 `noop: true`여야 한다.
`doctor`는 선택된 slice에서 plan이 정한 exact capability 집합을 사용한다. LSP,
Razor/Blazor mixed document, build/test/run/watch 또는 DAP probe가 누락·실패·timeout
이면 component version이 맞더라도 non-ready다. 표시된 component와 exact runtime
identity를 먼저 복구하며 credential이나 login 상태를 readiness 근거로 사용하지
않는다.
