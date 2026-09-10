#!/usr/bin/env bash
# Prove Meeting Intelligence end to end against the REAL Claude summariser.
#
#   channel conversation -> plugin -> ssh forced command on mother -> Claude
#   -> parsed sections -> stored in honcochat -> readable in the UI's API
#   -> notification -> authorization enforced
#
# Run this ON ubuntu-3, after install-on-ubuntu3.sh.
#
# It refuses to pass on anything but real model output: if the summariser is
# unreachable, or the plugin is pointed at a local command instead of mother,
# or the output carries test-harness markers, the run FAILS. There is no mode
# in which this script reports success without Claude having answered.
#
# It creates two throwaway users (mi-verify-a / mi-verify-b) and one private
# channel. It never deletes data and never touches a Mattermost-owned table.
set -uo pipefail

API="${API:-http://127.0.0.1:8065/api/v4}"
PLUG="${PLUG:-http://127.0.0.1:8065/plugins/com.honco.workspace/api/v1}"
MMCTL="${MMCTL:-$HOME/honco-chat/build/mmctl}"
PSQL="${PSQL:-psql -h 127.0.0.1 -p 5433 -U honco -d honcochat -tAc}"
MOTHER_HOST="${MOTHER_HOST:-192.168.2.150}"
MOTHER_PORT="${MOTHER_PORT:-31013}"
TEAM_NAME="${TEAM_NAME:-}"

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); printf "  PASS  %s\n" "$1"; }
bad(){ FAIL=$((FAIL+1)); printf "  FAIL  %s  (%s)\n" "$1" "${2:-}"; }
want(){ if [ "$2" = "$3" ]; then ok "$1 (=$3)"; else bad "$1" "expected $2, got $3"; fi; }
jid(){ grep -oE '"id":"[a-z0-9]{26}"' | head -1 | cut -d'"' -f4; }

# A password for two throwaway accounts, generated here and never printed.
TESTPW="$(openssl rand -base64 24 | tr -d '\n/+=' | head -c 24)Aa1!"

login(){ curl -s -D - -o /dev/null -H 'Content-Type: application/json' -X POST "$API/users/login" \
  -d "{\"login_id\":\"$1\",\"password\":\"$TESTPW\"}" | grep -i '^token:' | awk '{print $2}' | tr -d '\r'; }
code(){ curl -s -o /tmp/miv.$$ -w '%{http_code}' "$@"; }
col(){ $PSQL "SELECT coalesce($1,'') FROM honco_meeting_summaries WHERE meeting_id='$2';"; }

echo "=== 0. preflight: this must be a host that can reach mother ==="
if timeout 8 bash -c "cat < /dev/null > /dev/tcp/$MOTHER_HOST/$MOTHER_PORT" 2>/dev/null; then
    ok "$MOTHER_HOST:$MOTHER_PORT is reachable"
else
    bad "$MOTHER_HOST:$MOTHER_PORT unreachable" "run this on a host on the Honco LAN"
    echo; echo "ABORTING: without the summariser there is nothing real to verify."
    exit 1
fi

# If SummarizerCommand is set, the plugin is NOT talking to mother. That is
# exactly the shape a stub takes, so refuse rather than report a false pass.
SUMCMD=$("$MMCTL" --local config show --json 2>/dev/null | python3 -c "
import json,sys
try: print(json.load(sys.stdin)['PluginSettings']['Plugins']['com.honco.workspace'].get('summarizercommand',''))
except Exception: print('')
")
if [ -z "$SUMCMD" ]; then
    ok "SummarizerCommand is empty, so generation goes over SSH to mother"
else
    bad "SummarizerCommand is set" "clear it; this run would not be using Claude"
    echo; echo "ABORTING: refusing to certify a local command as real Claude output."
    exit 1
fi

echo
echo "=== 1. fixtures: two users, a private channel, a registered meeting ==="
for u in mi-verify-a mi-verify-b; do
  "$MMCTL" --local user create --username "$u" --email "$u@honco.test" \
      --password "$TESTPW" --email-verified >/dev/null 2>&1 \
   || "$MMCTL" --local user change-password "$u" --password "$TESTPW" >/dev/null 2>&1
done

if [ -z "$TEAM_NAME" ]; then
  TEAM_NAME=$($PSQL "SELECT name FROM teams WHERE deleteat=0 ORDER BY createat LIMIT 1;")
fi
[ -n "$TEAM_NAME" ] || { echo "  no team found; set TEAM_NAME" >&2; exit 1; }
echo "  team: $TEAM_NAME"
for u in mi-verify-a mi-verify-b; do "$MMCTL" --local team users add "$TEAM_NAME" "$u" >/dev/null 2>&1; done

TA=$(login mi-verify-a); TB=$(login mi-verify-b)
[ -n "$TA" ] || { echo "  could not log in as mi-verify-a" >&2; exit 1; }
AID=$(curl -s -H "Authorization: Bearer $TA" "$API/users/me" | jid)
BID=$(curl -s -H "Authorization: Bearer $TB" "$API/users/me" | jid)
TEAM=$(curl -s -H "Authorization: Bearer $TA" "$API/teams/name/$TEAM_NAME" | jid)

SUF=$(openssl rand -hex 4)
CH=$(curl -s -H "Authorization: Bearer $TA" -H 'Content-Type: application/json' -X POST "$API/channels" \
   -d "{\"team_id\":\"$TEAM\",\"name\":\"mi-verify-$SUF\",\"display_name\":\"MI verify $SUF\",\"type\":\"P\"}" | jid)
curl -s -o /dev/null -H "Authorization: Bearer $TA" -H 'Content-Type: application/json' \
   -X POST "$API/channels/$CH/members" -d "{\"user_id\":\"$BID\"}"
echo "  channel: $CH"

MEET_SECRET_FILE="${MEET_SECRET_FILE:-$HOME/.honco-meet-service-secret}"
# shellcheck source=/dev/null
[ -f "$MEET_SECRET_FILE" ] && . "$MEET_SECRET_FILE"
M=$(curl -s -X POST "$PLUG/meetings/register" -H 'Content-Type: application/json' \
   -H "X-Honco-Service-Secret: ${MEET_SECRET:-}" \
   -d "{\"room_name\":\"honco-miverify-$SUF\",\"channel_id\":\"$CH\",\"creator_id\":\"$AID\",\"topic\":\"Release planning\"}" \
   | grep -oE '"meeting_id":"[a-z0-9]{26}"' | cut -d'"' -f4)
[ -n "$M" ] && ok "meeting registered ($M)" || { bad "meeting registration" "check MeetServiceSecret"; exit 1; }
sleep 1

echo
echo "=== 2. a real conversation in the channel ==="
say(){ curl -s -o /dev/null -H "Authorization: Bearer $1" -H 'Content-Type: application/json' \
   -X POST "$API/posts" -d "{\"channel_id\":\"$CH\",\"message\":$2}"; }
say "$TA" '"Right, lets go through the release. The database migration is the risky part."'
say "$TB" '"Agreed. I reran it last night and it took 40 minutes, much slower than staging."'
say "$TA" '"That is too slow for the maintenance window. Can we batch it?"'
say "$TB" '"Batching should work. I can have it ready by Thursday."'
say "$TA" '"Decision: we slip the release to the 21st and Bob batches the migration."'
say "$TB" '"Works for me. I will also ask QA for two extra days."'
say "$TA" '"So Bob owns the batched migration by Thursday, and I tell the customer today."'
say "$TB" '"One open question: who writes the rollback plan?"'
sleep 2
want "eight messages posted" 8 "$($PSQL "SELECT count(*) FROM posts WHERE channelid='$CH' AND deleteat=0 AND type='';")"

echo
echo "=== 3. generate the summary with the real Claude backend ==="
want "generation accepted" 202 "$(code -X POST "$PLUG/meetings/$M/summary" -H "Authorization: Bearer $TA" -H 'Content-Type: application/json' -d '{"force":true}')"
ST=pending
for i in $(seq 1 60); do
  sleep 5
  ST=$($PSQL "SELECT status FROM honco_meeting_summaries WHERE meeting_id='$M';")
  [ "$ST" != "pending" ] && [ -n "$ST" ] && break
done
echo "  status after $((i*5))s: $ST"
if [ "$ST" != "ready" ]; then
  bad "real generation completed" "status=$ST  err=$(col error_message "$M")"
  echo; echo "================= MEETING INTELLIGENCE (REAL): $PASS passed, $((FAIL)) failed ================="
  echo "Feature 4 stays PARTIAL: Claude did not produce a summary."
  exit 1
fi
ok "real generation completed"

echo
echo "=== 4. the output is genuinely from Claude, not a harness ==="
RAW=$(col raw_output "$M")
if printf '%s' "$RAW" | grep -qE 'STUB-|STUB_ECHO|harness'; then
  bad "output is real" "test-harness markers present"
else
  ok "no test-harness markers anywhere in the output"
fi
[ ${#RAW} -ge 40 ] && ok "model returned substantive output (${#RAW} chars)" || bad "output length" "${#RAW} chars"

# Claude read the actual conversation if it can name what was discussed.
TOPICAL=0
for term in migration release Thursday batch QA rollback 21st; do
  printf '%s' "$RAW" | grep -qi "$term" && TOPICAL=$((TOPICAL+1))
done
[ "$TOPICAL" -ge 2 ] && ok "the summary reflects the actual conversation ($TOPICAL topical terms)" \
                     || bad "summary does not reflect the conversation" "$TOPICAL topical terms"

echo
echo "=== 5. sections parsed and stored ==="
for f in summary participants; do
  V=$(col "$f" "$M"); [ -n "$V" ] && ok "stored: $f" || bad "stored: $f" "empty"
done
for f in key_points decisions action_items; do
  V=$(col "$f" "$M")
  [ -n "$V" ] && ok "stored: $f" || echo "  NOTE  $f is empty -- the model did not emit that heading"
done
want "message_count recorded" 8 "$($PSQL "SELECT message_count FROM honco_meeting_summaries WHERE meeting_id='$M';")"
want "exactly one summary row" 1 "$($PSQL "SELECT count(*) FROM honco_meeting_summaries WHERE meeting_id='$M';")"

echo
echo "--- the real summary, for your review ---"
$PSQL "SELECT summary FROM honco_meeting_summaries WHERE meeting_id='$M';" | sed 's/^/    /'
echo "    --- decisions ---"; col decisions "$M" | sed 's/^/    /'
echo "    --- action items ---"; col action_items "$M" | sed 's/^/    /'
echo "    --- participants ---"; col participants "$M" | sed 's/^/    /'
echo "----------------------------------------"

echo
echo "=== 6. visible to a channel member, refused to everyone else ==="
want "member can read it" 200 "$(code -X GET "$PLUG/meetings/$M/summary" -H "Authorization: Bearer $TB")"
want "unauthenticated is refused" 401 "$(code -X GET "$PLUG/meetings/$M/summary")"
"$MMCTL" --local user create --username mi-verify-out --email mi-verify-out@honco.test \
    --password "$TESTPW" --email-verified >/dev/null 2>&1
"$MMCTL" --local team users add "$TEAM_NAME" mi-verify-out >/dev/null 2>&1
TO=$(login mi-verify-out)
want "same team but not in the channel is refused" 404 "$(code -X GET "$PLUG/meetings/$M/summary" -H "Authorization: Bearer $TO")"

echo
echo "=== 7. retry does not regenerate ==="
U0=$($PSQL "SELECT updated_at FROM honco_meeting_summaries WHERE meeting_id='$M';")
want "retry returns the stored summary" 200 "$(code -X POST "$PLUG/meetings/$M/summary" -H "Authorization: Bearer $TA" -H 'Content-Type: application/json' -d '{}')"
sleep 3
want "not regenerated" "$U0" "$($PSQL "SELECT updated_at FROM honco_meeting_summaries WHERE meeting_id='$M';")"

echo
echo "=== 8. the requester was notified, once ==="
want "one summary-ready notification" 1 "$($PSQL "SELECT count(*) FROM honco_notifications WHERE kind='meeting_summary_ready' AND subject_id='$M';")"
want "no duplicate dedupe keys" 0 "$($PSQL "SELECT count(*) FROM (SELECT dedupe_key FROM honco_notifications GROUP BY dedupe_key HAVING count(*)>1) d;")"

echo
echo "=== 9. nothing sensitive reached the log ==="
LOG="${LOG:-$HOME/honco-chat/logs/mattermost.log}"
for needle in "slip the release to the 21st" "rollback plan"; do
  N=$(grep -cF "$needle" "$LOG" 2>/dev/null; true)
  want "absent from the log: '$needle'" 0 "${N:-0}"
done

rm -f /tmp/miv.$$
echo
echo "================= MEETING INTELLIGENCE (REAL CLAUDE): $PASS passed, $FAIL failed ================="
if [ "$FAIL" = "0" ]; then
  echo "Feature 4 is DONE: a real summary came from Claude on $MOTHER_HOST and is stored and readable."
else
  echo "Feature 4 stays PARTIAL."
  exit 1
fi
echo
echo "Cleanup is yours to decide: mi-verify-a / mi-verify-b / mi-verify-out and"
echo "channel mi-verify-$SUF were created by this run and were left in place."
