<p align="center"><a href="README.md">English</a> | 한국어</p>

<p align="center"><sub>이 문서는 영어 원본(<a href="README.md">README.md</a>)의 번역본입니다. 내용이 다를 경우 영어 원본이 우선하며, 번역은 원본보다 늦을 수 있습니다. 아래에서 링크하는 <code>docs/</code> 문서들은 영어로만 제공됩니다.</sub></p>

<p align="center">
  <img src="assets/icon-round.png" alt="oh-my-graph logo" width="128" />
</p>

<h1 align="center">oh-my-graph</h1>

<p align="center"><em>목표를 설명하세요 — 그래프는 Claude subscription 위에서 실행됩니다.</em></p>

<p align="center">
  <a href="https://github.com/jitokim/oh-my-graph/releases"><img src="https://img.shields.io/github/v/release/jitokim/oh-my-graph?include_prereleases&amp;label=release&amp;color=blue" alt="Latest release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT license" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.25-00ADD8?logo=go&amp;logoColor=white" alt="Go 1.25" /></a>
  <img src="https://img.shields.io/badge/runs%20on-Claude%20subscription-ff8a65?logo=anthropic&amp;logoColor=white" alt="Runs on your Claude subscription" />
</p>

<p align="center">
  <img src="assets/hero.png" alt="oh-my-graph" width="100%" />
</p>

> 노드 런타임이 Anthropic API가 아니라 — 직접 로그인한 `claude` CLI인,
> graph-native 멀티 에이전트 오케스트레이터.

<a id="what-it-is"></a>

## 무엇인가

특화된 에이전트들을 DAG로 엮는 graph engineering은 지금까지 Anthropic API,
Agent SDK, 그리고 종량제 `ANTHROPIC_API_KEY`를 강요해 왔습니다. 기존의
graph-native 오케스트레이터는 전부 토큰 단위로 과금됩니다.

oh-my-graph는 그러지 않습니다. 할 일을 DAG로 YAML에 기술하면, 각 노드는 이미
결제 중인 Max/Pro 플랜 위에서 순수한 `claude -p` 서브프로세스로 실행됩니다.
**이것은 공짜라는 뜻이 아닙니다.** 구독을 소모하고, run에는 실제 가격이 있고,
ledger가 노드마다 그 값을 출력합니다 — 주장하는 것은 *두 번째* 종량제 청구서가
없다는 것이지, 작업이 공짜라는 것이 아닙니다. 이것이 코드로 어떻게 강제되는지는
[Bring your own login](#bring-your-own-login)에, 가장 가까운 이웃들 — conductor,
OMK, open-multi-agent — 과의 비교는
[docs/PRIOR-ART.md](docs/PRIOR-ART.md)에 있습니다.

<p align="center">
  <img src="assets/live-view.png" alt="실제 oh-my-graph run의 web live view: 왼쪽은 노드 출력 피드, 오른쪽은 passed/running/pending 노드가 표시된 DAG 맵, 헤더에는 실시간 비용과 경과 시간" width="100%" />
</p>
<p align="center"><em>실행 중의 live view — 실제 dogfood run을 라이브로 캡처한 화면: 왼쪽은 노드 출력 피드, 오른쪽은 DAG 맵, 헤더에는 비용과 경과 시간.</em></p>

<a id="quickstart"></a>
<a id="example"></a>

## 빠른 시작

```sh
go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest

# 바이너리에 임베드된 예제 그래프를 ./graphs/에 풀어 놓습니다:
oh-my-graph init

# Zero config — 목표를 말하면 auto가 그래프를 설계합니다:
oh-my-graph auto "lint this repo and summarize the findings" --input repo=$PWD

# 또는 기본 제공 그래프 실행 — 가장 저렴한 실제 end-to-end 체크(몇 센트):
mkdir -p /tmp/omg-smoke
oh-my-graph run graphs/haiku-smoke.yaml --input dir=/tmp/omg-smoke
```

`ANTHROPIC_API_KEY`는 필요 없습니다 — 로그인된 `claude` subscription으로
실행되며, 셸에 그 키가 설정되어 있다면 각 노드가 실행되기 전에 그 노드의
서브프로세스 환경에서 삭제됩니다. `auto`에 `--plan-only`를 붙이면 플랜을 사서
읽기만 하고 노드는 하나도 실행하지 않습니다.

그래프가 실행되는 동안에는 노드별 라이브 라인이 보이고, 끝나면 비용 ledger가
나옵니다:

```text
Run 20260729-101532 — PASS, 2 node(s)
NODE             VERDICT              SESSION                   COST(USD)  DETAIL
---------------------------------------------------------------------------------
critique         PASS (exit-only)     a1b2c3d4-e5f6-47a8-9…        0.0034
write            PASS (verified)      f9e8d7c6-b5a4-4321-8…        0.0091
---------------------------------------------------------------------------------
TOTAL COST: $0.0125
```

모든 `PASS`는 **어떻게** 통과했는지를 함께 말합니다. "엔진이 당신의 빌드를 실제로
돌렸고 exit 0이었다"와 "모델이 PASS라고 말했다"는 같은 주장이 아니고, 같은
단어로 찍혀서도 안 되기 때문입니다. 위의 `exit-only`는 프로세스 종료 코드 외에는
아무것도 확인되지 않았다는 뜻입니다. 닫힌 4원소 qualifier 집합과 각각에서 엔진이
실제로 한 일은
[Reading the ledger](docs/EXAMPLES.md#reading-the-ledger--what-a-pass-says)에
있습니다.

미리 빌드된 바이너리, 체크섬 검증, 그리고 `init`이 정확히 무엇을 쓰고 무엇을
덮어쓰지 않는지: [docs/INSTALL.md](docs/INSTALL.md).

## 그래프는 transcript가 아니라 파일입니다

DAG는 버전 관리하고, pull request에서 리뷰하고, 다시 재생할 수 있는 YAML 파일로
존재합니다 — 매번 같은 토폴로지, 같은 tool ceiling, 같은 프롬프트. 호출할 때마다
에이전트가 즉석에서 플랜을 새로 짜는 것과는 정반대입니다.

```yaml
name: dev-review-pr
inputs: [repo]
concurrency: 4
nodes:
  - id: dev
    cwd: "{{ inputs.repo }}"
    prompt: Implement the change and summarize what you did.
    allowed_tools: [Read, Edit, Write, "Bash(git *)"]
    permission_mode: dontAsk

  - id: e2e
    depends_on: [dev]
    cwd: "{{ inputs.repo }}"
    handoff: session          # e2e resumes dev's session — it already knows what dev did
    prompt: Run make local. Reply with the bare word PASS, or FAIL and what broke.
    success_check:
      result_matches: '^[*_`\s]*PASS[*_`\s]*$'   # what the node said
      verify: { command: "make local" }          # what the engine saw
    retry: { max: 1, on: [nonzero_exit, verify_failed] }

  - id: review
    depends_on: [e2e]
    permission_mode: plan     # read-only
    allowed_tools: [Read, "Bash(git diff*)"]
    prompt: "Review the diff. e2e said: {{ artifacts.e2e | inline }}"
```

엣지는 인라인 `depends_on` id입니다 — 별도의 엣지 목록이 없습니다 — 그리고
병렬성은 **창발적**입니다: 부모를 공유하되 서로 의존하지 않는 노드들은 상한까지
동시에 실행됩니다. `allowed_tools`는 힌트가 아니라 그 노드의 권한 자체입니다.
그리고 실패는 당신이 유지보수하는 glue 코드가 아니라 일급 문법입니다: 근거 검사,
원인별 `retry`, 그래프 레벨 `on_fail`, 경계가 있는 `feedback:` 리뷰 루프, 사람의
승인을 위해 run을 멈추는 `type: gate` 노드, 그리고 run을 실패시키는 대신
*일시정지*시키는 구독 세션 한도 — `resume --retry-failed`가 나중에 실행되지 못한
작업만 정확히 마저 끝냅니다.

모든 subcommand와 플래그, 노드 필드별 레시피 — `auto` 심화, goal cycle, 플랜된
노드를 당신의 Claude Code 에이전트·스킬에 매핑하는 방식 포함 — 는
[docs/EXAMPLES.md](docs/EXAMPLES.md)에 있습니다. 권위 있는 스펙은 DESIGN.md입니다.

## 지켜보기, 그리고 남는 것

`run`, `auto`, `resume`은 위의 live view를 leg가 지속되는 동안 `127.0.0.1`에
서빙하고 브라우저에서 엽니다(`--no-web`으로 끌 수 있음). run id 없이 실행한
`oh-my-graph serve`는 모든 run을 한눈에 보는 **dashboard**로, run마다 live
mini-DAG 카드가 하나씩 뜹니다. 끝난 뒤에는 `runs list` / `show` / `watch`가
평문으로 읽어 줍니다.

모든 run은 `~/.oh-my-graph/runs/<run-id>/`에 영속화됩니다(`OMG_HOME`으로 베이스
위치 변경 가능): snapshot(`state.json`)과, 외부 consumer도 tail 할 수 있는
append-only `events.jsonl`. 이 레이아웃과, `runs list`가 출력하는 여섯 개의 run
status, 그리고 살아 있는 run을 프로세스가 죽은 run과 어떻게 구별하는지는 문서화된
안정적 계약입니다 — [docs/RUN-FEED.md](docs/RUN-FEED.md). 또한 노드는 session
persistence가 **켜진** 채 실행되므로, 모든 노드가 `~/.claude/projects`에 평범한
claude 세션으로 남고 그 transcript를 읽는 어떤 도구든 그대로 집어갈 수 있습니다.

## 스스로를 배포합니다

여기서의 dogfooding은 데모가 아닙니다: 이 저장소는 그 안에 담긴 도구가 만듭니다.
기능, 수정, 문서, 릴리스가 자기 자신의 그래프로 작성됩니다 — claude 노드가
브랜치에서 구현하고, 형제 노드들이 체크와 리뷰를 돌리고, 마지막 노드가 draft PR을
엽니다. [`graphs/`](graphs/)의 템플릿은 샘플이 아닙니다. `self-dev.yaml`,
`adr-driven-dev.yaml`, `apply-flags.yaml`이 바로 그 파이프라인입니다. 그대로 믿지
마세요: 이 주장을 셀 수 있게 만드는 커밋 trailer와 한 줄짜리 감사 명령, 그리고 그
분모는 [CONTRIBUTING.md § Attribution](CONTRIBUTING.md#attribution)에 있고, 전체
dogfooding run은
[docs/EXAMPLES.md](docs/EXAMPLES.md#dogfooding-developing-oh-my-graph-with-oh-my-graph)에서
차례로 따라갑니다.

## 믿기 전에 읽어야 할 경계 하나

`auto`는 LLM이 쓴 플랜을 당신의 머신에서 무인으로 실행합니다. oh-my-graph는
플랜된 노드가 무엇을 호출할 수 있는지를 제한합니다 — `permission_mode:
bypassPermissions` 금지, planner가 작성한 엔진 셸 금지, 고정된 tool allowlist,
그리고 `Bash(git *)`를 선언한 노드가 당신 settings의 상시 `Bash(*)`를 빌려 쓸 수
없게 하는 `--setting-sources ""`(`--help`를 읽은 것이 아니라 실제 `claude`에
대고 측정했습니다). **하지만 이것은 sandbox가 아니라 축소입니다.** MCP가 실제로
닫히는지는 측정된 적이 없고, slash-command 표면은 이 메커니즘들로 열거되지
않으며, ceiling 전체가 특정 CLI 버전의 동작에 기대고 있습니다. `auto`는 수정되어도
괜찮은 디렉토리에서 실행하세요.

계층별 입장과 그 뒤의 모든 측정은 [SECURITY.md](SECURITY.md)에, 나머지 정직한
빈틈들과 플랫폼 지원 매트릭스(macOS·Linux 지원, WSL first-class, 네이티브
Windows는 best-effort), 그리고 의도적으로 보류한 목록은
[docs/LIMITATIONS.md](docs/LIMITATIONS.md)에 있습니다.

<a id="bring-your-own-login"></a>

## Bring your own login

oh-my-graph는 자격 증명을 배포하지 않고, 인증을 프록시하지 않으며, 공유
서비스로 실행되지도 않습니다. 이미 로그인된 **본인의** `claude` 세션을
재사용합니다 — 직접 `claude -p`를 실행하는 것, 혹은
[claude-squad](https://github.com/smtg-ai/claude-squad)와 같은 위치입니다.
개인용, 로컬 도구입니다. 노드는 이미 결제 중인 Max/Pro 플랜 안에서 실행되며,
종량제 키는 개입하지 않습니다.

이 보장을 실제로 지키기 위해, 모든 노드 서브프로세스는 환경에서
`ANTHROPIC_API_KEY`와 `ANTHROPIC_AUTH_TOKEN`이 **삭제된** 상태로 시작합니다 —
이 변수들은 `claude`를 조용히 종량제 API 과금으로 전환시킵니다. 이 scrub은 하나의
공유 정책(`internal/childenv`)이며, 정책 자체와 네 개의 exec seam 각각에서 유닛
테스트로 검증됩니다. oh-my-graph는 (OAuth를 비활성화하는) `--bare`를 절대 쓰지
않고, Agent SDK도 절대 건드리지 않습니다. 전체 입장:
[SECURITY.md](SECURITY.md).

## 나머지는 어디에 있는가

| 알고 싶은 것 | 읽을 곳 |
|---|---|
| 모든 subcommand와 플래그, 워크스루, 노드 필드별 레시피, `auto` 심화 | [docs/EXAMPLES.md](docs/EXAMPLES.md) |
| 미리 빌드된 바이너리, 체크섬 검증, `init`이 푸는 것 | [docs/INSTALL.md](docs/INSTALL.md) |
| 정직한 빈틈들, 플랫폼 지원, 보류된 것 | [docs/LIMITATIONS.md](docs/LIMITATIONS.md) |
| run 디렉토리, `events.jsonl`, run status와 liveness | [docs/RUN-FEED.md](docs/RUN-FEED.md) |
| ToS 입장, auto의 tool ceiling, 무엇이 측정되었는가 | [SECURITY.md](SECURITY.md) |
| 권위 있는 스펙 | [DESIGN.md](DESIGN.md) |
| 빌드·테스트, attribution trailer, 릴리스 체크리스트 | [CONTRIBUTING.md](CONTRIBUTING.md) |
| conductor, OMK, open-multi-agent와의 비교 | [docs/PRIOR-ART.md](docs/PRIOR-ART.md) |
| Claude Code 세션 안에서 구동하기 | [plugin/README.md](plugin/README.md) |

참고: [fleetops](https://github.com/jitokim/fleetops) — 같은
`~/.claude/projects` transcript를 fleet 단위로 읽는 자매 프로젝트입니다.

## 라이선스

[MIT](LICENSE) © jitokim
