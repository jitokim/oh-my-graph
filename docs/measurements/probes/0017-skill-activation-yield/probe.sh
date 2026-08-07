#!/bin/bash
# Reconstructed argv of an ACTIVATED PLANNED NODE, from:
#   runner.buildArgs (internal/runner/claude.go:193)
#   toolPolicyFor / narrowedToolsFor / disallowedToolsFor (internal/coordinator/coordinator.go:607,650,671)
#   applySkillActivation + BindSkillStaging (internal/coordinator/skillstage.go)
#   scheduler defaultPermissionMode -> dontAsk
# env scrubbed per internal/childenv.Scrub.
set -u
ARM="$1"; PLUGIN="$2"; PROMPT_FILE="$3"; ALLOWED="${4:-Read,Write}"; TOOLS="${5:-Read,Write,Skill}"
DISALLOWED="${6:-Bash,Edit,MultiEdit,NotebookEdit,WebFetch,WebSearch,Task,Agent}"
Y=/tmp/omg-yield
SID=$(python3 -c 'import uuid;print(uuid.uuid4())')
PROMPT_FILE="$(cd "$(dirname "$PROMPT_FILE")" && pwd)/$(basename "$PROMPT_FILE")"
case "$PLUGIN" in ""|/*) ;; *) PLUGIN="$(cd "$PLUGIN" && pwd)";; esac
PROMPT="$(cat "$PROMPT_FILE")"
rm -f "$Y/work/OMG-PROBE-FIRED.txt"
unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN
cd "$Y/work" || exit 1
ARGS=(-p "$PROMPT" --output-format json --permission-mode dontAsk --setting-sources "")
if [ -n "$PLUGIN" ]; then ARGS+=(--plugin-dir "$PLUGIN"); fi
ARGS+=(--allowedTools "$ALLOWED")
if [ -n "$TOOLS" ]; then ARGS+=(--tools "$TOOLS"); fi
ARGS+=(--strict-mcp-config --disallowedTools "$DISALLOWED" --session-id "$SID")
printf '%s\n' "ARM=$ARM SID=$SID" > "$Y/logs/$ARM.$SID.argv"
printf '%q ' claude "${ARGS[@]}" >> "$Y/logs/$ARM.$SID.argv"
claude "${ARGS[@]}" > "$Y/logs/$ARM.$SID.json" 2> "$Y/logs/$ARM.$SID.err"
MARK=no; [ -f "$Y/work/OMG-PROBE-FIRED.txt" ] && MARK=yes
rm -f "$Y/work/OMG-PROBE-FIRED.txt"
python3 - "$ARM" "$SID" "$MARK" <<'PY'
import json,os,sys,glob
arm,sid,mark=sys.argv[1],sys.argv[2],sys.argv[3]
Y='/tmp/omg-yield'
cost='?'
try:
    env=json.load(open(f'{Y}/logs/{arm}.{sid}.json'))
    cost=env.get('total_cost_usd')
except Exception as e:
    env={}
skills=[]
paths=glob.glob(os.path.expanduser(f'~/.claude/projects/*omg-yield-work*/{sid}.jsonl'))
for p in paths:
    for line in open(p):
        try: o=json.loads(line)
        except: continue
        c=o.get('message',{}).get('content')
        if isinstance(c,list):
            for b in c:
                if isinstance(b,dict) and b.get('type')=='tool_use' and b.get('name')=='Skill':
                    skills.append(b.get('input',{}).get('skill'))
row=dict(arm=arm,sid=sid[:8],skill_tool_use=len(skills),skills=skills,marker=mark,cost=cost,
         transcript=bool(paths))
print(json.dumps(row))
open(f'{Y}/logs/results.jsonl','a').write(json.dumps(row)+'\n')
PY
