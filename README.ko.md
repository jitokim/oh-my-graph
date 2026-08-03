<p align="center"><a href="README.md">English</a> | 한국어</p>

<p align="center"><sub>이 문서는 영어 원본(<a href="README.md">README.md</a>)의 번역본입니다. 내용이 다를 경우 영어 원본이 우선하며, 번역은 원본보다 늦을 수 있습니다.</sub></p>

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
>
> **oh-my-graph는 실행하고, [fleetops](https://github.com/jitokim/fleetops)는
> 같은 `~/.claude/projects` transcript를 관찰합니다.**

<p align="center">
  <img src="assets/live-view.png" alt="실제 oh-my-graph run의 web live view: 왼쪽은 노드 출력 피드, 오른쪽은 passed/running/pending 노드가 표시된 DAG 맵, 헤더에는 실시간 비용과 경과 시간" width="100%" />
</p>
<p align="center"><em>실행 중의 live view — 실제 dogfood run(ADR-0012 skill-mapping 그래프)을 라이브로 캡처한 화면: 왼쪽은 노드 출력 피드, 오른쪽은 DAG 맵, 헤더에는 비용과 경과 시간.</em></p>

<a id="what-it-is"></a>

## 무엇인가 — 그리고 어떤 빈틈을 채우는가

특화된 에이전트들을 DAG로 엮는 graph engineering은 지금까지
Anthropic API, Agent SDK, 그리고 종량제 `ANTHROPIC_API_KEY`를 강요해
왔습니다. 기존의 graph-native 오케스트레이터는 전부 토큰 단위로 과금됩니다.

**subscription** 기반 `claude` CLI를 구동하는 오케스트레이터는 없습니다.
oh-my-graph가 채우는 빈틈이 바로 그 지점입니다: 할 일을 DAG로 기술하면,
각 노드는 이미 결제 중인 구독 위에서 순수한 `claude -p` 서브프로세스로
실행됩니다 — Max/Pro 플랜 안에서, 종량제 키는 개입하지 않습니다.

가장 가까운 이웃들 — conductor, OMK, open-multi-agent — 과 oh-my-graph의
비교는 [docs/PRIOR-ART.md](docs/PRIOR-ART.md)에 정리되어 있습니다.

<a id="what-it-can-do"></a>

## 무엇을 할 수 있는가

- **엔진.** 그래프는 YAML입니다 — 엣지가 인라인 `depends_on` id로 표현된
  노드 목록 — 그리고 각 노드는 자신만의 tool ceiling(`allowed_tools`,
  `permission_mode`) 아래 실행되는 하나의 `claude -p` 서브프로세스이며,
  병렬성은 동시 실행 상한까지 토폴로지에서 창발합니다. 코드베이스 전체에서
  프로세스를 스폰할 수 있는 객체는 정확히 네 개이고, 그 넷 모두 자식을
  `ANTHROPIC_API_KEY`와 `ANTHROPIC_AUTH_TOKEN`이 삭제된 환경에서
  시작시킵니다
  ([그래프 모델](#the-custom-path--write-the-graph-yourself) ·
  [DESIGN.md](DESIGN.md) ·
  [ADR 0002](docs/adr/0002-verification-is-a-second-exec-seam.md) ·
  [ADR 0005](docs/adr/0005-worktree-provisioning-is-a-third-exec-seam.md) ·
  [ADR 0006](docs/adr/0006-browser-open-is-a-fourth-exec-seam.md)).
- **실패는 일급 문법입니다.** 노드는 자기 보고가 아니라 근거로 통과합니다:
  엔진이 실행하는 `verify` 명령을 포함한 `success_check`, 원인별 `retry`,
  그래프 레벨 `on_fail`, 경계가 있는 `feedback:` 리뷰 루프, `auto`의
  plan→run→assess goal cycle, 사람이 서는 gate, 그리고 run을 실패시키는
  대신 일시정지시키는 구독 세션 한도 — `resume --retry-failed`가 나중에
  실행되지 못한 작업만 정확히 마저 끝냅니다
  ([노드가 선언할 수 있는 나머지](#what-else-a-node-can-declare)).
- **관찰.** run이 진행되는 동안에는 `127.0.0.1`에 서빙되는 web live view,
  끝난 뒤에는 `runs list` / `show` / `watch` — 모두 어떤 consumer든 tail 할
  수 있는 append-only `events.jsonl` 위에서 동작합니다. 여기에 노드별 비용
  ledger와 run 합계가 더해집니다
  ([사용법](#usage) · [docs/RUN-FEED.md](docs/RUN-FEED.md)).
- **당신의 Claude 설정 그대로.** 노드는 당신이 이미 로그인해 쓰는
  `claude -p` 그 자체이므로 그 설정을 그대로 물려받습니다. `agent:`는
  노드를 본인의 Claude Code subagent로 실행하고, `auto`는 플랜된 노드를
  당신의 에이전트와 스킬에 매핑합니다
  ([`auto` 심화](#auto-in-depth)).

<a id="what-makes-it-different"></a>

## 무엇이 다른가

- **그래프는 transcript가 아니라 artifact입니다.** DAG는 버전 관리하고,
  pull request에서 리뷰하고, 다시 재생할 수 있는 YAML 파일로 존재합니다 —
  매번 같은 토폴로지, 같은 tool ceiling, 같은 프롬프트. 호출할 때마다
  에이전트가 즉석에서 플랜을 짜거나 일회용 스크립트를 새로 쓰는 것과는
  정반대입니다.
- **사람이 run 한가운데에 설 수 있습니다.** `type: gate` 노드는 승인을 위해
  run을 멈추고, `oh-my-graph resume`이 이어서 진행합니다 — 터미널에서든
  live view에서 바로든 — 그래서 되돌릴 수 없는 단계는 잘 되기를 바라는 대신
  사람을 기다립니다.
- **실패 의미론은 당신의 glue 코드가 아니라 엔진 안에 있습니다.** 근거
  검사, 원인별 retry, 계속/중단 정책, 경계가 있는 feedback 루프, gate
  일시정지, 실패한 run의 복구는 그래프에 선언하는 동작이지, 그 주변에
  직접 쓰고 유지보수하는 셸이 아닙니다.
- **스스로를 배포합니다.** 여기서의 dogfooding은 데모가 아닙니다: 이
  저장소는 그 안에 담긴 도구가 만듭니다. 기능, 수정, 문서, 릴리스가 자기
  자신의 그래프로 작성됩니다 — claude 노드가 브랜치에서 구현하고, 형제
  노드들이 체크와 리뷰를 돌리고, 마지막 노드가 draft PR을 엽니다. 검증
  가능한 부분은, 2026-08-02 기준: `main`에 머지된 80개의 pull request 중
  23개가 squash 커밋에 Claude co-author trailer를 달고 있습니다 — claude
  세션이 그것들을 썼다는 영수증입니다. 직접 세어 보세요:
  `git log main --first-parent -i --grep="co-authored-by: claude"`
  (24건: 그 23개의 squash 커밋과 최초 커밋). 이 trailer는 파이프라인이 아니라
  모델을 가리키므로, 2026-08-02부터 그래프 레인이 작성한 커밋에는
  `Co-Authored-By: oh-my-graph <graphs@oh-my-graph.dev>`도 함께 붙습니다 —
  authorship의 증명이 아니라 투명성을 위한 관례입니다;
  [CONTRIBUTING.md](CONTRIBUTING.md#attribution) 참고.
  [`graphs/`](graphs/)의 템플릿은 샘플이 아닙니다: `self-dev.yaml`,
  `adr-driven-dev.yaml`, `apply-flags.yaml`이 이 저장소가 스스로를 배포할 때
  쓰는 파이프라인이며, 전체 dogfooding run은
  [docs/EXAMPLES.md](docs/EXAMPLES.md#dogfooding-developing-oh-my-graph-with-oh-my-graph)에서
  차례로 따라갑니다.

<a id="how-to-use-it"></a>

## 어떻게 쓰는가

경로는 둘이고, 둘 다 1분이면 실행됩니다: 평문 목표로부터 `auto`가 그래프를
설계하게 하거나, 정밀한 제어가 필요할 때 YAML을 직접 씁니다.

<a id="quickstart"></a>
<a id="example"></a>
<a id="the-easy-path--install-init-state-a-goal"></a>

### 쉬운 경로 — 설치, `init`, 목표 말하기

```sh
go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest

# Write the example graphs that ship inside the binary into ./graphs/:
oh-my-graph init

# Zero config — describe the goal and let auto plan the graph:
oh-my-graph auto "lint this repo and summarize the findings" --input repo=$PWD

# Or run a shipped graph — the cheapest real smoke test (a few cents):
mkdir -p /tmp/omg-smoke
oh-my-graph run graphs/haiku-smoke.yaml --input dir=/tmp/omg-smoke
```

`go install`은 실행 파일 하나만 복사하므로, `init`이 그 실행 파일에 임베드된
예제 그래프를 `./graphs/`에 풀어 놓습니다 — 디렉토리를
넘기면(`oh-my-graph init <dir>`) `<dir>/graphs/`에 씁니다. 절대 덮어쓰지
않습니다: 대상 파일이 하나라도 이미 존재하면 그 경로를 알려주고 아무것도
쓰지 않습니다.

`ANTHROPIC_API_KEY`는 필요 없습니다 — smoke test는 로그인된 `claude`
subscription으로 실행됩니다. 셸에 해당 키(또는 `ANTHROPIC_AUTH_TOKEN`)가
설정되어 있다면, 각 노드가 실행되기 전에 그 노드의 서브프로세스 환경에서
삭제됩니다(아래 [Bring your own login](#bring-your-own-login) 참고).

`auto`는 zero-config 기본 경로입니다: (동일한 subscription-auth, env-scrub이
적용된 runner를 거치는) claude 호출 한 번이 목표를 그래프 스펙으로 바꾸고,
같은 엔진이 이를 검증하고 실행합니다. 플랜은 실행 전에 출력되고, 생성된
스펙은 `~/.oh-my-graph/runs/<run-id>/graph.json`에 저장됩니다 — JSON은 유효한
YAML이므로 손으로 수정해 `oh-my-graph run`으로 다시 실행할 수 있습니다.
플래너가 만든 노드는 `permission_mode: bypassPermissions`를 절대 쓸 수
없습니다; 정밀한 제어가 필요하다면 여전히 커스텀 YAML이 그 경로입니다.
`auto`의 손잡이들 — goal cycle, 에이전트 매핑, 스킬 매핑 — 은 아래
[`auto` 심화](#auto-in-depth)에 있습니다.

그래프가 실행되는 동안에는 노드별 라이브 라인이 보입니다 —
`▶ write  running…`, 이어서 `✓ write  PASS  $0.0091  4.2s` — 멀티 노드 실행
중에 터미널이 조용해지는 일은 없습니다. 끝나면 ledger를 받습니다: 노드당 한
줄(session id, 비용, verdict, detail)과 총 비용. 위에서 쓴 기본 제공
`graphs/haiku-smoke.yaml`(두 노드: `write` 다음 `critique`, 기본 artifact
handoff로 연결)이 이 전부를 확인하는 가장 저렴한 실제 end-to-end
체크입니다:

```
Running graph "haiku-smoke" (run 20260729-101532)

▶ write  running…
✓ write  PASS  $0.0091  4.2s
▶ critique  running…
✓ critique  PASS  $0.0034  2.1s

Run 20260729-101532 — 2 node(s)
NODE             VERDICT    SESSION                     COST(USD)  DETAIL
------------------------------------------------------------------------------
critique         PASS       a1b2c3d4-e5f6-47a8-9c1…       0.0034
write            PASS       f9e8d7c6-b5a4-4321-8765…      0.0091
------------------------------------------------------------------------------
TOTAL COST: $0.0125
```

stdout이 터미널이면 `run`, `auto`, `resume`은 시작되는 leg의 [web live
view](#usage)를 임시 `127.0.0.1` 포트로 서빙하고 기본 브라우저에서 엽니다.
서버는 정확히 그 leg가 지속되는 동안만 살아 있습니다. 스크립트, 파이프,
CI에서(stdout이 터미널이 아닐 때) — 또는 `--no-web`을 주면 — 아무것도
서빙하거나 열지 않으며 출력도 달라지지 않습니다.

더 많은 워크스루 — auto 모드 심화, dogfooding, fleetops로 관찰하기,
ambient chat — 와 기능별 레시피는
[docs/EXAMPLES.md](docs/EXAMPLES.md)에 있습니다.

<a id="prebuilt-binaries"></a>

### 미리 빌드된 바이너리

태그가 붙은 릴리스마다 [GitHub Releases
페이지](https://github.com/jitokim/oh-my-graph/releases)에 미리 빌드된
바이너리도 함께 올라갑니다 — darwin과 linux, `arm64`와 `amd64` 모두,
`.tar.gz` 아카이브와 그 옆의 `checksums.txt`. Go 툴체인을 두고 싶지 않을 때
`go install` 대신 쓰는 경로입니다. Homebrew tap은 없으며, Windows는 빌드
매트릭스에 없습니다 — 거기서는 소스에서 빌드하세요.

Releases 페이지에서 태그를 고른 다음:

```sh
VERSION=0.3.1 OS=darwin ARCH=arm64   # the tag (without the leading v) and your platform
ARCHIVE="oh-my-graph_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -sSfLO "https://github.com/jitokim/oh-my-graph/releases/download/v${VERSION}/${ARCHIVE}"
curl -sSfLO "https://github.com/jitokim/oh-my-graph/releases/download/v${VERSION}/checksums.txt"
grep " ${ARCHIVE}$" checksums.txt | shasum -a 256 -c -   # on linux: sha256sum -c -
tar xzf "${ARCHIVE}"
./oh-my-graph version
```

`oh-my-graph`를 `PATH` 위로 옮기면 위의 smoke test가 그대로 실행됩니다.

<a id="the-custom-path--write-the-graph-yourself"></a>

### 커스텀 경로 — 그래프를 직접 쓰기

그래프는 YAML입니다: `name`, 선택적인 `inputs`와 `concurrency`, 그리고
`nodes` 목록. 각 노드는 하나의 `claude -p` 서브프로세스입니다. 엣지는
인라인 `depends_on` id입니다 — 별도의 엣지 목록이 없으므로 토폴로지의
source of truth는 하나입니다. 병렬성은 **창발적**입니다: 부모를 공유하되
서로 의존하지 않는 노드들은 상한까지 동시에 실행됩니다.

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
    cwd: "{{ inputs.repo }}"  # a session child works in its parent's tree
    handoff: session          # e2e resumes dev's session — it already knows everything dev just did
    prompt: Run make local and report PASS or FAIL.
    success_check:
      exit_zero: true
      result_matches: "PASS"          # what the node said
      verify: { command: "make local" }  # what the engine saw
    retry: { max: 1, on: [nonzero_exit, verify_failed] }

  - id: review
    depends_on: [e2e]
    permission_mode: plan     # read-only
    prompt: "Review the diff. e2e said: {{ artifacts.e2e | inline }}"
```

시작부터 알아 둘 만한 필드 셋, 각각 YAML 한 줄입니다:

- **`gate:`** — `type: gate` 노드는 사람의 승인을 위해 run을 멈추고,
  `oh-my-graph resume`으로 계속됩니다 ([spec](DESIGN.md#gate-nodes-and-resume-v11)).
- **`feedback:`** — 리뷰어 노드에 붙인 `feedback: { rerun: impl, max: 2 }`는
  리뷰를 펼쳐 놓은 체인 대신 경계가 있는 루프로 만듭니다
  ([ADR 0010](docs/adr/0010-a-feedback-edge-is-a-bounded-runtime-rerun-not-a-static-cycle.md)
  · demo: `graphs/review-loop.yaml`).
- **`worktree:`** — 병렬 편집 레인, lane 이름당 하나의 격리된 git 체크아웃
  ([recipe](docs/EXAMPLES.md#parallel-edit-lanes-with-git-worktrees-worktree)).

전체 필드 목록은 아래 [노드가 선언할 수 있는
나머지](#what-else-a-node-can-declare)에 있으며, 권위 있는 스펙은
DESIGN.md입니다.

<a id="recurring-pipelines--write-it-once"></a>

### 반복 파이프라인 — 한 번만 작성하세요

그래프 파일은 당신의 프롬프트 엔지니어링을 저장해 둔 것입니다. 매일 아침
채팅창에 다시 타이핑했을 목표/포맷/규칙이 담긴 정성 들인 프롬프트가 YAML에
한 번만 들어가고, `oh-my-graph run pipeline.yaml`이 필요할 때마다 그대로
재생합니다 — 일일 분석, 주간 triage, 릴리스 체크 — 이미 내고 있는 구독
요금 안에서. 한 run 안에서는 `handoff: session`이 체인의 컨텍스트를 계속
흐르게 하므로, 다운스트림 프롬프트는 목표와 포맷을 다시 설명하는 대신
한 줄이면 됩니다 — 아래
[Handoff](#handoff--what-a-child-inherits) 참고.

```yaml
name: daily-triage
nodes:
  - id: collect             # the careful goal/format/rules prompt lives here, once
    prompt: >
      Collect today's open issues and failing checks; list each with a
      one-line status.
  - id: analyze
    depends_on: [collect]
    handoff: session        # continues collect's conversation
    prompt: Analyze what you just collected and rank by urgency.
  - id: report
    depends_on: [analyze]
    handoff: session        # the chain keeps flowing
    prompt: Write the ranked findings up as a short report.
```

경계 하나는 분명히 해 둡니다: **run끼리는 서로를 기억하지 않습니다.** 모든
run은 의도적으로 fresh하게 시작합니다
([ADR 0008](docs/adr/0008-cross-run-session-reuse-is-deferred.md)에 cross-run
session 재사용을 보류한 이유가 기록되어 있습니다) — 매일의 일관성은 고정된
프롬프트와 `success_check` / `verify` 게이트에서 나오는 것이지, Claude가
어제를 기억해서가 아닙니다.

<a id="usage"></a>

## 사용법

```
oh-my-graph <init|run|auto|lint|chat|resume|runs|show|watch|serve|version> ...
```

| subcommand | 용도 |
|---|---|
| `init [dir]` | 바이너리에 임베드된 예제 그래프를 `<dir>/graphs/`에 쓰고(`dir` 기본값은 `.`), 쓴 파일을 하나씩 출력. 절대 덮어쓰지 않습니다 — 대상 파일이 하나라도 존재하면 그 경로를 알리며 실패하고 아무것도 쓰지 않습니다. |
| `run <graph.yaml>` | 손으로 작성한 DAG를 실행 — 정밀 제어 경로. `--dry-run`은 검증하고, `--input` interpolation을 해석하고, 플랜을 출력하며, 아무것도 실행하지 않습니다. |
| `auto "<goal>"` | 평문 목표로부터 DAG를 설계한 뒤 같은 엔진으로 실행 — zero-config 기본 경로. `--max-cycles N`은 plan→run→assess를 최대 N번 반복합니다(`--max-goal-budget-usd`는 cycle 사이에 검사되는 soft 지출 상한을 더하며, `--max-cycles`가 2 이상이어야 합니다). |
| `lint <graph.yaml>` | 그래프 파일을 정적으로 검증하고 모든 문제를 한 번에 보고. 읽기 전용, 비용 없음. |
| `chat` | 인터랙티브 REPL(프로토타입): 대화형 턴에는 답하고, 작업형 턴은 그래프로 설계해 실행합니다. |
| `resume <run-id> ((--approve \| --reject) <gate-id> \| --retry-failed)` | run 재개: 일시정지된 gate를 결정하거나, `--retry-failed`로 실패한 run을 복구 — 통과한 노드의 결과는 그대로 유지되고 실패·취소된 노드만 다시 실행됩니다. `--concurrency N`과 `--no-web`을 받습니다. |
| `runs list` | run 목록을 최신순으로 표시: 그래프 이름, 노드 수, 비용, verdict, 그리고 합계. 읽기 전용. |
| `show <run-id>` | 한 run의 노드별 ledger(session, 비용, verdict, 소요 시간)와 합계를 출력. 읽기 전용. |
| `watch <run-id>` | run의 이벤트 스트림을 `tail -f` 스타일의 평문으로 추적. 읽기 전용. |
| `serve [<run-id>]` | run의 web live view, `127.0.0.1`에만 바인딩(기본 포트 8642, `--port`로 변경). 한 가지를 빼면 읽기 전용입니다 — gate에서 일시정지된 run은 페이지에서 바로 승인·거절할 수 있습니다. |
| `version` | 도구 버전을 출력. |

`run`과 `auto`는 `--input k=v`(반복 가능), `--concurrency N`(상한 10),
`--continue-on-fail`을 공유합니다. 둘 다 그래프가 실행되는 동안 노드별
라이브 피드를 출력하고, 이어서 비용 ledger를 출력합니다. 그래프 자신이
그래프 레벨 `on_fail: continue`(기본값 `halt`)로 실패 정책을 선언할 수도
있습니다 — 한 lane의 실패가 다른 lane들의 진행 중인 작업을 취소해서는 안
되는 독립 lane 배치에 맞는 기본값입니다. 플래그와 필드는 OR로 결합됩니다:
어느 쪽이든 continue라고 하면 continue입니다.

`lint`는 구조를 검사하고 — DAG/cycle, 알 수 없는 `depends_on` id,
session-handoff 부모 규칙, verify 블록 — 유효하면 0, 아니면 1로
종료합니다. 유효한 그래프에서도, 해석되지 않을 placeholder 형태의
`{{ ... }}` 토큰에 대해 stderr로 advisory `warning:` 라인을 출력합니다 —
오타 난 필터(`| inlin`), 단수형 `{{ artifact.x }}`, 선언되지 않은 input,
존재하지 않거나 ancestor가 아닌 노드를 가리키는 `artifacts.<id>` — 그리고
`handoff: session` 노드에 대해서는, session-parent와 다른
`cwd`/`worktree`, 또는 `retry` 블록(재시도된 attempt는 cold로 시작)도
포함합니다. warning은 종료 코드를 절대 바꾸지 않습니다. 런타임에는 형식이
잘못된 토큰은 그대로 통과하는 반면(프롬프트에 literal `{{ }}` 텍스트가
정당하게 들어갈 수 있으므로), 바인딩되지 않은 input이나 알 수 없는 노드를
가리키는 올바른 형식의 참조는 interpolation이 실행될 때 해당 노드를
실패시킵니다.
`run --dry-run`은 그 종료 계약과 같은 warning을 공유하며, 추가로 실제
`--input` 값에 대한 `{{ inputs.* }}` 해석까지 증명합니다. 진행 중인 run은
`runs list`에 `RUNNING`으로 표시됩니다(첫 snapshot이 도착하기 전까지는
`-` placeholder로).

<a id="auto-in-depth"></a>

### `auto` 심화 — goal cycle, 에이전트, 스킬

목표가 실제로 달성될 때까지 `auto`가 계속 가기를 원한다면?
`--max-cycles N`(기본값 1)은 한 번의 호출을 최대 N번의 완전한
plan→run→assess cycle 루프로 바꿉니다: 매 run 이후, 도구가 제거된 assessor가
그 run 자신이 기록한 근거에 비추어 목표를 판정하고, 남은 일이 있으면 다음
cycle이 그 주위로 다시 플랜을 짭니다 — 모든 cycle은 동일한 tool ceiling
아래 다시 검증되고, 모든 플랜과 판정은 발생하는 대로 출력되며, 마지막에는
cycle별 지출을 합산한 goal summary가 나옵니다. exit 0은 goal-met 판정과
통과한 최종 run을 둘 다 요구합니다. `--max-goal-budget-usd X`는 cycle
사이에 검사되는 선택적 soft 지출 상한을 더합니다; 단일 cycle run에는 검사할
cycle 경계가 없으므로 `--max-cycles`가 최소 2여야 하며, 아니면 파싱 단계에서
거부됩니다. 정직하게 말해 둡니다: `auto`는 비대화형이므로, 지켜보는 사람
없이 돌린 `--max-cycles 5`는 플래너 호출 다섯 번, 그래프 다섯 개, 판정 다섯
번을 쓸 수 있습니다 — 거버넌스는 확인 프롬프트가 아니라 당신이 타이핑한
상한, cycle별 검증, 그리고 출력된 기록입니다.

자신만의 Claude Code 에이전트(`~/.claude/agents`, `./.claude/agents` —
프로젝트 쪽이 우선)가 있다면, 노드 id가 에이전트 이름과 명확히 일치할 때
`auto`가 플랜된 노드를 그 에이전트로 매핑합니다 — 리뷰 노드가 *당신의*
`code-reviewer`로 실행됩니다. 매칭은 의도적으로 보수적이며(명확한 후보가
정확히 하나일 때만, 노드의 계획된 도구 허용 목록을 넘는 도구를 원하는
에이전트는 안내 문구와 함께 스킵), 모든 매핑은 실행 전에 출력되는 플랜에
표시되고, `--no-agent-mapping`으로 끌 수 있습니다. 트레이드오프도 미리
밝혀 둡니다: 매핑된 노드는 에이전트를 해석하기 위해 완전한 설정 격리
대신 사용자의 설정을 로드합니다 — 선언된 도구 목록은 여전히 강제됩니다.

당신의 Claude Code 스킬(`~/.claude/skills`만 — 프로젝트 디렉토리는 절대
아님)도 `auto` run에 닿습니다. 다만 더 투박한 방식이고, 그대로 밝혀 둡니다:
플랜된 노드는 스킬을 보거나 호출할 수 없으므로(측정으로 확인), 노드 id가
스킬 이름과 명확히 일치하면 그 스킬의 SKILL.md 본문이 플랜 시점에 **그
노드의 프롬프트로 복사됩니다** — 펜스로 감싸고, 출처를 밝히고, 16 KiB로
상한이 걸리며(초과하는 스킬은 잘라내지 않고 안내와 함께 스킵), 그 노드를
호출할 때마다 비용이 들고, Claude Code라면 관련 있을 때만 활성화했을 스킬이
무조건 적용됩니다. 모든 인라이닝은 실행 전에 크기와 SHA-256 prefix를
출력하고, 정확한 텍스트는 — 거기서 전체 해시를 다시 계산할 수 있습니다 —
저장된 `graph.json`에 스냅샷으로 남으며(이후의 스킬 수정은 이미 플랜된 run에
닿지 않습니다), `--no-skill-mapping`으로 끌 수 있습니다.
[docs/EXAMPLES.md](docs/EXAMPLES.md#zero-config-auto-mode-the-headline)에서
플랜 출력, tool ceiling, 라이브 노드 피드를 차례로 다룹니다.

<a id="handoff--what-a-child-inherits"></a>

### Handoff — 자식이 무엇을 물려받는가

엣지는 노드가 *언제* 실행되는지를 말하고, `handoff`는 부모로부터 *무엇을*
물려받는지를 말합니다.

|                    | `artifact` (기본값) | `session` |
|--------------------|----------------------|-----------|
| 자식이 물려받는 것 | 부모의 **최종 응답** — `~/.oh-my-graph/runs/<run-id>/<node-id>.out`에 영속화되고 `{{ artifacts.<id> }}`가 나타나는 자리마다 치환됩니다: 기본은 파일 경로, `\| inline` 필터를 쓰면 응답 텍스트 자체 | 부모의 **claude session** — `--resume`으로 재개됩니다: 응답만이 아니라 부모가 읽고, 하고, 결론 내린 모든 것. 물려받는 것은 대화이지 설정이 아닙니다 — `allowed_tools`, `permission_mode`, `agent`, `cwd`, `budget_usd`는 언제나 자식 자신의 것 |
| 허용되는 부모      | 몇 개든 — fan-in과 fan-out은 artifact의 영역 | 정확히 하나의 `claude-run` 노드(root, fan-in, gate 부모는 로드 시점에 거부됨). 부모의 `cwd`/`worktree`를 공유하며 — 불일치 시 `lint`가 경고 |
| 세션 형태          | 각 노드가 새로운 claude 세션 | 하나의 대화를 이어가는 순차 체인 |

왜 중요한가: `artifact`에서는 부모가 최종 응답에 담지 않은 컨텍스트는
사라집니다 — 자식은 cold로 시작합니다. `session`에서는 자식이 대화
중간부터 이어받으므로, 촘촘한 파이프라인(구현하고, 방금 만든 것을 바로
테스트)에 재설명이 필요 없습니다. session 자식도 자신의 `prompt`는 직접
씁니다 — 물려받는 것은 컨텍스트이지, 지시가 아닙니다.

<a id="what-else-a-node-can-declare"></a>

### 노드가 선언할 수 있는 나머지

샘플에 나온 것 외에도, 노드는 다음을 선택적으로 쓸 수 있습니다(권위 있는
스펙은 DESIGN.md):

- **`agent:`** — 노드를 본인의 Claude Code subagent 중 하나로 실행 — 그
  subagent의 시스템 프롬프트, 도구, 모델 그대로 ([spec](DESIGN.md#node-as-subagent-agent-v11--hand-written-graphs-only) · [recipe](docs/EXAMPLES.md#running-a-node-as-your-own-subagent-agent)).
- **`worktree:`** — 관리되는 git worktree 안의 병렬 편집 레인, lane 이름당
  하나의 격리된 체크아웃 ([spec](DESIGN.md#worktree-isolation-worktree--hand-written-graphs-only) · [recipe](docs/EXAMPLES.md#parallel-edit-lanes-with-git-worktrees-worktree)).
- **`handoff`** — 위의 [Handoff — 자식이 무엇을 물려받는가](#handoff--what-a-child-inherits)
  참고 ([spec](DESIGN.md#handoff--artifact-default-session-opt-in-committed) · [recipe](docs/EXAMPLES.md#artifact-fan-out-vs-session-chain-handoff)).
- **`success_check` / `retry`** — 근거 기반 게이팅(`exit_zero`,
  `result_matches`, 그리고 엔진이 실행하는 `verify` 명령)과 원인별 retry ([spec](DESIGN.md#success-checks--evidence-grounded-verification-v11)).
- **`budget_usd`** — 노드별 비용 상한, 라이브(`--max-budget-usd`)와 사후
  모두 적용 ([spec](DESIGN.md#execution-engine) · [recipe](docs/EXAMPLES.md#budgets-budget_usd)).
- **`timeout`** — 20분 기본값을 대체하는 노드별 wall-clock 상한, 정당하게
  오래 걸리는 작업을 하는 노드를 위한 것 ([spec](DESIGN.md#execution-engine) · [ADR 0007](docs/adr/0007-per-node-execution-limits.md)).
- **`feedback:`** — 펼쳐 놓지 않은 채로 경계가 있는 리뷰 루프: 리뷰어 노드가
  자신의 판정에 실패하면, `feedback: { rerun: impl, max: 2 }`가 `impl`에서
  리뷰어까지의 경로를 다시 실행하고, 그 findings를
  `{{ feedback.review }}`로 재실행에 넘깁니다(첫 패스에서는 비어 있음) —
  최대 `max`번, 매 라운드가 ledger에 비용으로 기록됩니다
  ([spec](DESIGN.md#execution-engine) · [ADR 0010](docs/adr/0010-a-feedback-edge-is-a-bounded-runtime-rerun-not-a-static-cycle.md) · demo: `graphs/review-loop.yaml`).
- **gates** — `type: gate` 노드는 사람의 승인을 위해 run을 일시정지시키며,
  `oh-my-graph resume`으로 계속됩니다 ([spec](DESIGN.md#gate-nodes-and-resume-v11)).
- **실패 복구** — `resume <run-id> --retry-failed`는 실패한 run에서 실패·취소된
  노드만 다시 실행하며, 통과한 노드의 artifact는 dependents를 위해 그대로
  유지됩니다 ([spec](DESIGN.md#gate-nodes-and-resume-v11)).
- **세션 한도는 실패가 아니라 일시정지** — run 도중 구독의 세션 한도에
  도달해도 해당 노드는 실패로 기록되지 않습니다: run은 새 작업 launch를
  멈추고, 진행 중이던 노드는 끝까지 완료시킨 뒤, exit code 2와 함께
  `Resume after 5:20pm with: oh-my-graph resume <run-id> --retry-failed`
  같은 힌트를 출력합니다 — 이 명령이 나중에 실행되지 못한 작업만 정확히
  마저 끝냅니다. 감지는 CLI 메시지에 대한 정직한 문자열 매칭이며(구조화된
  신호가 없음), 문구가 바뀌어 인식하지 못하면 일반 실패로 안전하게
  강등되고 같은 명령으로 여전히 복구됩니다
  ([ADR 0009](docs/adr/0009-a-session-limit-is-a-pause-not-a-failure.md)).

## Claude Code에서 쓰기 (plugin)

위의 CLI가 제품 그 자체입니다. 대신 Claude Code 세션 안에 머물고 싶다면,
[`plugin/`](plugin/)은 `/graph` slash command를 추가하는 얇은
플러그인입니다 — 같은 `oh-my-graph` 바이너리를 셸로 호출할 뿐, 로직을
재구현하지 않습니다 — 여기에 더 낮은 마찰의 진입점으로 graph-engineering
**agent**도 제공합니다: 셸 rc에
`omg () { claude --agent oh-my-graph "$@"; }`를 추가하면, `omg`가 모든
턴이 graph-aware한 세션을 엽니다. 설치와 사용법:
[plugin/README.md](plugin/README.md) ([agent 섹션](plugin/README.md#the-oh-my-graph-agent-ambient-entry-point)).

<a id="run-artifacts-and-the-run-feed"></a>

## run 산출물과 run feed

모든 run은 `~/.oh-my-graph/runs/<run-id>/`에 영속화됩니다(`OMG_HOME`으로
베이스 위치 변경 가능) — 도구를 어디서 실행하든 같은 디렉토리입니다:
schema 버전이 명시된 snapshot(`state.json`)과 append-only 이벤트
스트림(`events.jsonl`)이 저장되고, `runs list` / `show` / `watch` /
`serve`가 이를 다시 읽으며 fleetops 같은 consumer가 tail 할 수 있습니다.
이 레이아웃은 문서화된 안정적 계약입니다 —
[docs/RUN-FEED.md](docs/RUN-FEED.md) 참고.

<a id="bring-your-own-login"></a>

## Bring your own login

oh-my-graph는 자격 증명을 배포하지 않고, 인증을 프록시하지 않으며, 공유
서비스로 실행되지도 않습니다. 이미 로그인된 **본인의** `claude` 세션을
재사용합니다 — 직접 `claude -p`를 실행하는 것, 혹은
[claude-squad](https://github.com/smtg-ai/claude-squad)와 같은 위치입니다.
개인용, 로컬 도구입니다.

이 보장을 실제로 지키기 위해, 모든 노드 서브프로세스는 환경에서
`ANTHROPIC_API_KEY`와 `ANTHROPIC_AUTH_TOKEN`이 **삭제된** 상태로
시작합니다 — 이 변수들은 `claude`를 조용히 종량제 API 과금으로
전환시킵니다. 이 scrub은 유닛 테스트로 검증됩니다
(`internal/runner/claude_test.go`); oh-my-graph는 (OAuth를 비활성화하는)
`--bare`를 절대 쓰지 않고, Agent SDK도 절대 건드리지 않습니다. 전체 입장:
[SECURITY.md](SECURITY.md).

## 플랫폼 지원

지원 대상은 macOS와 Linux입니다; CI는 Linux에서 빌드하고 테스트합니다.
**WSL은 first-class입니다**: WSL 빌드는 곧 Linux 빌드*이며* 완전히 동일한
코드 경로를 탑니다 — 단, `claude` CLI와 `sh`가 배포판 안에 있어야 합니다.
네이티브 Windows는 컴파일되고 실행되지만 best-effort입니다(Windows CI
없음): `verify`는 `sh -c` 대신 `cmd /c`로 실행되고, 취소는 직접 자식
프로세스만 종료하며, env scrub은 대소문자를 구분합니다 — WSL을
권장합니다. 전체 플랫폼 상세, 알려진 제약, 보류(deferred) 목록:
[docs/LIMITATIONS.md](docs/LIMITATIONS.md).

## fleetops와 짝을 이룹니다

노드는 실제 작업 디렉토리에서 session persistence가 **켜진** 채 실행되므로,
모든 노드가 `~/.claude/projects`에 평범한 claude 세션으로 나타납니다.
[fleetops](https://github.com/jitokim/fleetops) — fleet 조종석 — 가 그
transcript들을 관찰합니다. oh-my-graph는 executor이고, fleetops는
대시보드입니다. observability 연동이 공짜로 따라옵니다.

## 개발

```sh
make build      # build the binary
make test       # go test ./... -race
make vet        # go vet ./...
make fmt        # gofmt -w . (formats in place; always exits 0)
make fmt-check  # fails if any file is not gofmt-clean (the CI gate)
```

모든 엔진 로직은 스크립트된 `FakeRunner`로 테스트됩니다 — 테스트 스위트는
실제 `claude`를 절대 스폰하지 않습니다. 실제 claude를 쓰는
smoke(`make smoke`)는 **수동** 단계로, CI에는 절대 포함되지 않습니다 —
그래서 CI는 무료로 유지됩니다.

## 라이선스

[MIT](LICENSE) © jitokim
