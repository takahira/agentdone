#!/usr/bin/env sh
# Demo: agentdone withholds the "done" ping while background work runs, then
# sends one correct, context-rich notification when the agent is really done.
#
# Uses AGENTDONE_STDOUT=1 to print what would be sent (no Slack, no network),
# so it is safe to run anywhere and easy to record with asciinema.
#
#   sh demo/demo.sh
set -eu

cd "$(dirname "$0")/.."
BIN="$(mktemp -d)/agentdone"
go build -o "$BIN" ./cmd/agentdone
# Sandbox HOME (after the build, which needs the real Go cache): the seeded
# turn state goes to <sandbox>/.claude/hooks/state, never the real ~/.claude.
HOME="$(mktemp -d)"
export HOME
# Pin paths to the sandbox HOME: agentdone honours $CLAUDE_CONFIG_DIR, so an
# inherited one would send turn state somewhere other than the STATE_DIR below.
unset CLAUDE_CONFIG_DIR
export AGENTDONE_STDOUT=1
export AGENTDONE_LANG=en # demo shows the English default; AGENTDONE_LANG=ja for Japanese

bold() { printf '\033[1m%s\033[0m\n' "$1"; }
dim() { printf '\033[2m%s\033[0m\n' "$1"; }
iso() { date -u -r "$1" +%Y-%m-%dT%H:%M:%S.000Z 2>/dev/null || date -u -d "@$1" +%Y-%m-%dT%H:%M:%S.000Z; }

SID="demo-$$"
STATE_DIR="$HOME/.claude/hooks/state" # must match internal/state.dir()
mkdir -p "$STATE_DIR"
TURN="$STATE_DIR/$SID.json"
TR="$(mktemp)"
START_S=$(($(date +%s) - 600)) # turn started 10 minutes ago
START_MS=$((START_S * 1000))   # the state file stores milliseconds

# A transcript with realistic token/model/skill at a timestamp inside the turn.
printf '{"type":"assistant","timestamp":"%s","attributionSkill":"code-review","message":{"model":"claude-opus-4-8","usage":{"output_tokens":27300}}}\n' "$(iso $((START_S + 10)))" >"$TR"

seed() { printf '{"start_epoch":%s,"prompt":"run the tests in parallel","session_title":"Refactor the parser"}' "$START_MS" >"$TURN"; }

stop() { # stop <background_tasks_json>; Stop consumes the turn state, so re-seed first.
	seed
	printf '{"hook_event_name":"Stop","session_id":"%s","cwd":"%s","transcript_path":"%s","last_assistant_message":"Aggregated the parallel run and reported back.","background_tasks":%s}\n' \
		"$SID" "$PWD" "$TR" "$1" | "$BIN"
}

echo
bold "1) A background sweep is still running, and the turn ends to wait for it."
dim  "   Naive hooks fire a false \"completed\" ping right here. agentdone stays silent:"
echo "   \$ echo '<Stop, background_tasks:[sweep running]>' | agentdone"
out=$(stop '[{"id":"t1","type":"shell","status":"running","command":"go test ./... -race"}]')
[ -z "$out" ] && printf '   \033[32m→ (no notification — withheld) ✅\033[0m\n' || printf '   %s\n' "$out"

echo
bold "2) The sweep finished; the agent is really done."
dim  "   Now one correct, context-rich notification is sent:"
echo "   \$ echo '<Stop, background_tasks:[]>' | agentdone"
echo "   ---"
stop '[]' | sed 's/^/   /'

rm -f "$TURN" "$TR"
echo
