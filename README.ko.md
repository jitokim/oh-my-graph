<p align="center"><a href="README.md">English</a> | 한국어</p>

<p align="center"><sub>이 문서는 영어 원본(<a href="README.md">README.md</a>)의 번역본입니다. 내용이 다를 경우 영어 원본이 우선하며, 번역은 원본보다 늦을 수 있습니다. 아래에서 링크하는 <code>docs/</code> 문서들은 영어로만 제공됩니다.</sub></p>

<p align="center">
  <img src="assets/icon-round.png" alt="oh-my-graph logo" width="128" />
</p>

<h1 align="center">oh-my-graph</h1>

<p align="center"><em>직접 쓴 그래프의 각 노드가, 이미 로그인해 둔 <code>claude</code> 또는 <code>codex</code> CLI 의 진짜 서브프로세스로 돕니다 — 당신의 설정, 당신의 스킬 그대로.</em></p>

<p align="center">
  <a href="https://github.com/jitokim/oh-my-graph/releases"><img src="https://img.shields.io/github/v/release/jitokim/oh-my-graph?include_prereleases&amp;label=release&amp;color=blue" alt="Latest release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT license" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.25-00ADD8?logo=go&amp;logoColor=white" alt="Go 1.25" /></a>
  <img src="https://img.shields.io/badge/runtime-Claude%20%7C%20Codex-ff8a65" alt="Claude and Codex runtimes" />
</p>

<p align="center">
  <img src="assets/hero.png" alt="oh-my-graph" width="100%" />
</p>

> 노드 런타임이 API key가 아니라 — 직접 로그인한 `claude` 또는 `codex`
> CLI인 graph-native 멀티 에이전트 오케스트레이터.

<a id="what-it-is"></a>

## 무엇인가

oh-my-graph에는 직접 model API나 Agent SDK가 필요하지 않습니다. 할 일을 DAG로
YAML에 기술하면 각
노드는 기본값인 `claude`, 또는 run에 선택한 `codex`의 저장된 로그인으로
실행됩니다. **이것은 공짜라는 뜻이 아닙니다.** 플랜 사용량을 소모합니다. Claude는
USD 비용을 보고하고, Codex는 token 사용량을 보고하므로 ledger는 USD를 `$0`으로
꾸미지 않고 `unknown`으로 표시합니다. 이것이 코드로 어떻게 강제되는지는
[Bring your own login](#bring-your-own-login)에, 가장 가까운 이웃들 — conductor,
OMK, open-multi-agent — 과의 비교는
[docs/PRIOR-ART.md](docs/PRIOR-ART.md)에 있습니다.

<p align="center">
  <img src="assets/live-view.png" alt="실제 oh-my-graph run의 실행 중 web live view: 두 연구 갈래가 각각 별도 프로세스로 동시에 진행 중, 왼쪽은 노드 출력 피드, 오른쪽은 passed/running/pending 노드가 표시된 DAG 맵, 헤더에는 실시간 비용과 경과 시간" width="100%" />
</p>
<p align="center"><em>실제 run의 실행 중 화면. 논문 두 편을 읽고 프로토타입하는 두 갈래가 <b>동시에</b> 돕니다 — <code>read-a → code-a</code>는 37초에 끝났고 <code>read-b → code-b</code>는 아직 도는 중입니다. 그래프 어디에도 "병렬로 돌라"고 쓰지 않았고, 서로 기다릴 이유가 없다고만 쓰여 있습니다. 비용과 경과 시간은 헤더에서 실시간으로 움직입니다.</em></p>

<a id="quickstart"></a>
<a id="example"></a>

## 빠른 시작

```sh
# Go 툴체인 없이 미리 빌드된 바이너리로 (정확한 명령: docs/INSTALL.md) — https://github.com/jitokim/oh-my-graph/releases 에서
# oh-my-graph_<version>_<os>_<arch>.tar.gz (darwin/linux × amd64/arm64)를 받아, 옆의 checksums.txt로
# 검증하고, 풀어서 PATH에 두세요. 소스에서 받으려면 Go 1.25+ 와 $(go env GOPATH)/bin 이 PATH에 있어야 합니다:
go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest

# 바이너리에 임베드된 예제 그래프를 ./graphs/에 풀어 놓습니다:
oh-my-graph init

# Zero config — 목표를 말하면 auto가 그래프를 설계합니다.
# 이 목표는 레포를 읽고 요약을 쓸 뿐이라 빌드할 것이 없고,
# --accept-no-build-evidence가 바로 그 사실을 선언합니다. 빌드 시스템이
# 감지되는 디렉터리에서는 이 플래그나 --verify-cmd 없이 `auto`가 시작을
# 거부합니다. 계획된 노드는 빌드를 실행할 수 없고, 그 PASS는 노드 자신의
# 말일 뿐이기 때문입니다:
oh-my-graph auto "lint this repo and summarize the findings" --input repo=$PWD --accept-no-build-evidence

# 구현 목표라면 반대쪽 출구를 씁니다 — 엔진이 플랜의 각 sink에서 당신의 빌드
# 명령을 직접 실행하고 exit code를 스스로 판정합니다:
oh-my-graph auto "fix the failing test" --input repo=$PWD --verify-cmd 'go build ./...'

# Codex는 run 전체에 적용되는 opt-in입니다. global flag는 subcommand 앞에 둡니다:
oh-my-graph --runtime codex auto "lint this repo and summarize the findings" --input repo=$PWD --accept-no-build-evidence

# 또는 기본 제공 그래프 실행 — 가장 저렴한 실제 end-to-end 체크(몇 센트):
mkdir -p /tmp/omg-smoke
oh-my-graph run graphs/haiku-smoke.yaml --input dir=/tmp/omg-smoke
```

선택할 CLI에 한 번 로그인하세요(`claude` 또는 `codex login`). API key는 필요
없습니다. child process에서는 Anthropic/OpenAI API-key 환경 변수를 삭제해 저장된
로그인을 사용합니다. 기본값은 계속 Claude입니다. `--runtime codex`는 run 전체에
적용되어 `state.json`에 저장되고, `resume`과 브라우저 gate 동작도 같은 런타임을
사용합니다. `auto`에 `--plan-only`를 붙이면 플랜을 사서 읽기만 하고 노드는 하나도
실행하지 않습니다.

Codex는 `permission_mode: plan`을 read-only sandbox로, 일반 모드를
`workspace-write`로, `bypassPermissions`를 `danger-full-access`로 매핑합니다.
이 sandbox는 네트워크 경계이기도 해서, Codex 그래프는 push하거나 `gh`를
호출하는 첫 번째 노드에서 멈춥니다.
Codex는 USD를 보고하지 않고 Claude의 `agent:` 선택자도 구현하지 않으므로,
`agent:`와 `--max-goal-budget-usd`는 Codex run이 무언가를 쓰기 전에 거부합니다.
노드의 `budget_usd`는 거부하지 않습니다. 묶을 USD가 없어 그저 적용될 수 없을
뿐이므로, 그래프는 로드되고 노드마다 경고 한 줄이 그 사실과 함께 여전히 그
노드를 지키는 `timeout:`을 알려줍니다.
Claude Code agent mapping과 skill activation은 Claude 전용이며, Codex `auto`는
Codex sandbox isolation을 사용합니다.

그래프가 실행되는 동안에는 노드별 라이브 라인이 보이고, 끝나면 비용 ledger가
나옵니다:

```text
Run 20260729-101532 — 2 node(s)
NODE             VERDICT              SESSION           COST(USD)  DETAIL
-------------------------------------------------------------------------
critique         PASS (exit-only)     a1b2c3d4-e5f6-4…     0.0034
write            PASS (verified)      f9e8d7c6-b5a4-4…     0.0091
-------------------------------------------------------------------------
TOTAL COST: $0.0125
```

모든 `PASS`는 **어떻게** 통과했는지를 함께 말합니다. "엔진이 당신의 빌드를 실제로
돌렸고 exit 0이었다"와 "모델이 PASS라고 말했다"는 같은 주장이 아니고, 같은
단어로 찍혀서도 안 되기 때문입니다. 위의 `exit-only`는 프로세스 종료 코드 외에는
아무것도 확인되지 않았다는 뜻입니다. 닫힌 4원소 qualifier 집합과 각각에서 엔진이
실제로 한 일은
[Reading the ledger](docs/EXAMPLES.md#reading-the-ledger--what-a-pass-says)에
있습니다.

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
    permission_mode: dontAsk  # 선택 사항; 선언하지 않으면 `auto`, 이건 더 엄격한 쪽을 요청하는 것

  - id: e2e
    depends_on: [dev]
    cwd: "{{ inputs.repo }}"
    handoff: session          # e2e resumes dev's session — it already knows what dev did
    prompt: Run make local. Reply with the bare word PASS, or FAIL and what broke.
    success_check:
      exit_zero: true
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
*일시정지*시키는 Claude 구독 세션 한도 — `resume --retry-failed`가 나중에
실행되지 못한 작업만 정확히 마저 끝냅니다.

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
append-only `events.jsonl`. 이 레이아웃과, 살아 있는 run을 프로세스가 죽은 run과
어떻게 구별하는지는 문서화된 안정적 계약입니다 —
[docs/RUN-FEED.md](docs/RUN-FEED.md). `runs list`가 출력하는 여섯 개의 run
status는 나머지 커맨드 표면과 함께
[docs/EXAMPLES.md](docs/EXAMPLES.md#the-command-surface)에 있습니다. 또한 노드는
session persistence가 **켜진** 채 실행됩니다. Claude 노드는
`~/.claude/projects`의 일반 세션으로 남고, Codex 노드는 `codex exec --json`이
내보낸 thread id를 저장해 resume합니다.

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

`auto`는 LLM이 쓴 플랜을 당신의 머신에서 무인으로 실행합니다. oh-my-graph는 선택한
런타임의 메커니즘으로 실행을 제한합니다. Claude는 아래 문서의 측정된 tool
ceiling을 사용하고, Codex는 planned node에서 user config, project rules/AGENTS,
MCP server를 제외한 뒤 read-only 또는 workspace sandbox를 적용합니다. **둘 다
축소이지, 실행을 시작한 repository를 둘러싼 보안 경계는 아닙니다.** `auto`는
수정되어도 괜찮은 디렉토리에서 실행하세요. 그리고 둘 다 planned node에만
적용됩니다. `run`으로 실행하는 손으로 쓴 그래프는 당신의 user config, project
rules/AGENTS 파일, hook, MCP server를 그대로 유지하며,
`--accept-loaded-user-config`는 `auto`에서도 그것들을 원한다고 소리 내어
선언하는 플래그입니다.

그 선을 이름을 밝히고 넘는 선호(preference)가 딱 하나 있습니다. 당신
`~/.claude/settings.json`의 `model` 키 하나만 따로 읽어 planned Claude node에
`--model <value>`로 전달하므로, 그 node는 설정이 차단됐을 때 CLI가 기본값으로
되돌아가는 모델이 아니라 **당신이** 고른 모델로 답합니다 (측정: planned node
187개 중 181개가 아무도 고르지 않은 모델로 실행됨 —
[ADR 0037](docs/adr/0037-a-planned-node-answers-with-the-model-the-operator-chose.md)).
이것으로 node의 capability ceiling은 달라지지 않습니다. 모델 이름은 tool을 주지
않고, 파일을 로드하지 않으며, hook을 실행하지 않습니다. 그 파일에서 다른 것은
아무것도 읽지 않고, `--runtime codex`에서는 아무것도 읽지 않습니다 — codex는
`--ignore-user-config`가 `~/.codex/config.toml`을 막으므로 planned node가 `codex`
자체의 기본 모델로 답합니다 ([docs/LIMITATIONS.md](docs/LIMITATIONS.md)).

그 ceiling 안에서 최근에 바뀐 것이 하나 있고, 계층을 읽기 전에 알아둘 값어치가
있습니다. `permission_mode`를 선언하지 않은 노드는 이제 `--permission-mode auto`로
실행됩니다 — 이전에는 `dontAsk`였습니다. 노드의 allow 규칙 중 어느 것과도 매칭되지
않은 tool 호출이 곧바로 거부되는 대신, CLI 자신의 모델 분류기(classifier)에게
넘어가 승인 또는 거부됩니다. tool 권한 자체는 움직이지 않았습니다 — 선언한
`Bash(git *)`는 그대로 전달되고, 명시적 deny는 여전히 분류기보다 먼저 이기며,
노드의 tool 집합에서 빠진 tool은 여전히 존재하지 않습니다. 더 엄격한 쪽으로
되돌리려면 노드에 `permission_mode: dontAsk`를 적으면 됩니다. 근거와, 이 결정을
뒤집게 될 측정치는
[ADR 0034](docs/adr/0034-an-unmatched-tool-call-meets-a-classifier-not-a-dead-ask.md)에
있습니다.

계층별 입장과 그 뒤의 모든 측정은 [SECURITY.md](SECURITY.md)에, 나머지 정직한
빈틈들과 플랫폼 지원 매트릭스(macOS·Linux 지원, WSL first-class, 네이티브
Windows는 best-effort), 그리고 의도적으로 보류한 목록은
[docs/LIMITATIONS.md](docs/LIMITATIONS.md)에 있습니다.

<a id="bring-your-own-login"></a>

## Bring your own login

oh-my-graph는 자격 증명을 배포하지 않고, 인증을 프록시하지 않으며, 공유
서비스로 실행되지도 않습니다. **본인의** 저장된 `claude` 또는 `codex` 로그인을
재사용하는 개인용 로컬 도구로, 선택한 CLI를 직접 호출하는 것과 같은 위치입니다.

이 보장을 실제로 지키기 위해, 모든 노드 서브프로세스는 환경에서
`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY`, `CODEX_API_KEY`가
**삭제된** 상태로 시작합니다 — 선택한 CLI가 저장된 로그인 대신 API key를 쓰지
않게 하기 위해서입니다. 이 scrub은 하나의
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
