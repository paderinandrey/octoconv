#!/usr/bin/env bash
# presets-acceptance.sh -- Phase 18 (presets) live hard gate.
#
# Proves, against the REAL local compose stack (real Postgres, real image
# worker, real API), that:
#   SC1 (PRST-01): all five cmd/manage-presets CLI verbs (create, list, show,
#     update, deactivate) work end-to-end, each asserted against real CLI
#     output / DB state / HTTP response -- not just build-checked.
#   SC2 (D-08): a job created via preset=<name> reaches `done` and persists
#     preset_name/preset_version provenance in the jobs table.
#   SC3 (D-02): a client-scoped preset shadows a system preset of the same
#     name for its owning client; a different client with no override
#     resolves to the system preset.
#   SC4 (D-01/D-03): preset + explicit target/opts together -> 422; a
#     nonexistent preset and a cross-client preset both return
#     byte-identical 422 bodies (no existence leak).
#   SC5 (D-06): a preset whose stored opts fail CURRENT validation is
#     rejected 422 at use time -- stored opts are never trusted.
#
# This script's exit code IS the gate: any failed assertion aborts non-zero
# (set -e) with a loud FAIL message. It does not tear the stack down on
# success (matches the project's Phase 16/17 live-gate precedent) -- rerun
# freely, every preset/client name is uuid-suffixed.
set -euo pipefail

cd "$(dirname "$0")/.."

# ---------------------------------------------------------------------------
# Config / constants (compose_contract, 18-04-PLAN.md)
# ---------------------------------------------------------------------------
API_BASE="http://localhost:8090"
export DATABASE_URL="postgres://octo:octo-pass@localhost:5434/octo_db"
export API_KEY_SALT="dev-only-change-me-in-real-deploys"
DB_CONTAINER="octoconv-db"
WORKER_CONTAINER="octoconv-worker"

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

PASS_COUNT=0

# ---------------------------------------------------------------------------
# Assertion helpers -- every one echoes and exits 1 (loud, non-zero) on
# mismatch; every one echoes a PASS line with the value on success.
# ---------------------------------------------------------------------------
assert_eq() {
	local expected="$1" actual="$2" label="$3"
	if [ "$expected" != "$actual" ]; then
		echo "FAIL: $label -- expected [$expected], got [$actual]" >&2
		exit 1
	fi
	PASS_COUNT=$((PASS_COUNT + 1))
	echo "PASS: $label == $actual"
}

assert_contains() {
	local haystack="$1" needle="$2" label="$3"
	if ! printf '%s' "$haystack" | grep -qF -- "$needle"; then
		echo "FAIL: $label -- expected output to contain [$needle]" >&2
		echo "--- actual output ---" >&2
		printf '%s\n' "$haystack" >&2
		exit 1
	fi
	PASS_COUNT=$((PASS_COUNT + 1))
	echo "PASS: $label contains [$needle]"
}

# psql_q runs a single -tAc query against the live DB container and returns
# the trimmed scalar result.
psql_q() {
	docker exec "$DB_CONTAINER" psql -U octo -d octo_db -tAc "$1" | tr -d '[:space:]'
}

# http_post issues a multipart POST to /v1/jobs; sets HTTP_STATUS and writes
# the response body to the given path.
http_post() {
	local out_file="$1"
	shift
	HTTP_STATUS=$(curl -s -o "$out_file" -w '%{http_code}' -X POST "$API_BASE/v1/jobs" "$@")
}

http_get() {
	local out_file="$1"
	shift
	HTTP_STATUS=$(curl -s -o "$out_file" -w '%{http_code}' "$@")
}

echo "=== Phase 18 presets: live acceptance hard gate ==="

# ---------------------------------------------------------------------------
# Step 1: bring up the stack, wait for readiness.
# ---------------------------------------------------------------------------
echo "--- bringing up compose stack (api rebuilt, rest from existing images) ---"
docker compose -p octoconv -f docker-compose.yml -f docker-compose.e2e.yml up -d --build api
docker compose -p octoconv -f docker-compose.yml -f docker-compose.e2e.yml up -d

echo "--- waiting for /healthz ---"
healthy=""
for i in $(seq 1 30); do
	code=$(curl -s -o /tmp/presets-acceptance-healthz.json -w '%{http_code}' "$API_BASE/healthz" || true)
	if [ "$code" = "200" ]; then
		healthy=1
		break
	fi
	sleep 2
done
if [ -z "$healthy" ]; then
	echo "FAIL: /healthz never returned 200 after 60s" >&2
	exit 1
fi
echo "PASS: /healthz ready ($(cat /tmp/presets-acceptance-healthz.json))"

# ---------------------------------------------------------------------------
# Step 2: fixtures. sample.png/sample.html are the committed e2e fixtures;
# sample.jpg is generated on the fly via the running image worker's vips CLI
# (needed for the shadowing test, since png->png is not a valid libvips pair
# -- from != to).
# ---------------------------------------------------------------------------
cp internal/e2e/testdata/sample.png "$WORKDIR/sample.png"
cp internal/e2e/testdata/sample.html "$WORKDIR/sample.html"

docker cp "$WORKDIR/sample.png" "$WORKER_CONTAINER:/tmp/presets-acceptance-src.png"
docker exec "$WORKER_CONTAINER" vips copy /tmp/presets-acceptance-src.png /tmp/presets-acceptance-src.jpg
docker cp "$WORKER_CONTAINER:/tmp/presets-acceptance-src.jpg" "$WORKDIR/sample.jpg"
file "$WORKDIR/sample.jpg" | grep -qi "JPEG" || {
	echo "FAIL: generated sample.jpg fixture is not a JPEG" >&2
	exit 1
}
echo "PASS: fixtures ready (sample.png, sample.html, sample.jpg)"

# ---------------------------------------------------------------------------
# Step 3: mint two clients (A, B) via cmd/manage-clients.
# ---------------------------------------------------------------------------
SUFFIX=$(uuidgen | tr 'A-Z' 'a-z')

CLIENT_A_OUT=$(go run ./cmd/manage-clients create "presets-acceptance-a-$SUFFIX")
CLIENT_B_OUT=$(go run ./cmd/manage-clients create "presets-acceptance-b-$SUFFIX")

CLIENT_A_ID=$(printf '%s\n' "$CLIENT_A_OUT" | awk -F': ' '/^client id:/{print $2}')
KEY_A=$(printf '%s\n' "$CLIENT_A_OUT" | awk -F': ' '/^api key/{print $2}')
CLIENT_B_ID=$(printf '%s\n' "$CLIENT_B_OUT" | awk -F': ' '/^client id:/{print $2}')
KEY_B=$(printf '%s\n' "$CLIENT_B_OUT" | awk -F': ' '/^api key/{print $2}')

[ -n "$CLIENT_A_ID" ] && [ -n "$KEY_A" ] || {
	echo "FAIL: could not parse client A id/key from: $CLIENT_A_OUT" >&2
	exit 1
}
[ -n "$CLIENT_B_ID" ] && [ -n "$KEY_B" ] || {
	echo "FAIL: could not parse client B id/key from: $CLIENT_B_OUT" >&2
	exit 1
}
echo "PASS: minted client A ($CLIENT_A_ID) and client B ($CLIENT_B_ID)"

# ---------------------------------------------------------------------------
# Step 4: CREATE verb -- system + client scope, capture id/version, assert
# every fresh create prints version 1.
# ---------------------------------------------------------------------------
NAME_P="pa-p-$SUFFIX"
NAME_Q="pa-q-$SUFFIX"
NAME_R="pa-r-$SUFFIX"
NAME_S="pa-s-$SUFFIX"

CREATE_P_SYS_OUT=$(go run ./cmd/manage-presets create --name "$NAME_P" --target webp)
V=$(printf '%s\n' "$CREATE_P_SYS_OUT" | awk -F': ' '/^version:/{print $2}')
assert_eq "1" "$V" "create P (system) prints version 1"

CREATE_P_USER_OUT=$(go run ./cmd/manage-presets create --name "$NAME_P" --client-id "$CLIENT_A_ID" --target png)
V=$(printf '%s\n' "$CREATE_P_USER_OUT" | awk -F': ' '/^version:/{print $2}')
assert_eq "1" "$V" "create P (user, client A) prints version 1"

CREATE_Q_OUT=$(go run ./cmd/manage-presets create --name "$NAME_Q" --target webp)
V=$(printf '%s\n' "$CREATE_Q_OUT" | awk -F': ' '/^version:/{print $2}')
assert_eq "1" "$V" "create Q (system) prints version 1"

CREATE_R_OUT=$(go run ./cmd/manage-presets create --name "$NAME_R" --target webp)
V=$(printf '%s\n' "$CREATE_R_OUT" | awk -F': ' '/^version:/{print $2}')
assert_eq "1" "$V" "create R (system) prints version 1"

CREATE_S_OUT=$(go run ./cmd/manage-presets create --name "$NAME_S" --client-id "$CLIENT_A_ID" --target webp)
V=$(printf '%s\n' "$CREATE_S_OUT" | awk -F': ' '/^version:/{print $2}')
assert_eq "1" "$V" "create S (user-only, client A, no system counterpart) prints version 1"

# ---------------------------------------------------------------------------
# Step 5a: LIST -- system scope shows P/Q/R; client A scope shows the
# user-scope P (target png visible).
# ---------------------------------------------------------------------------
LIST_SYSTEM_OUT=$(go run ./cmd/manage-presets list)
assert_contains "$LIST_SYSTEM_OUT" "$NAME_P" "list (system) contains P"
assert_contains "$LIST_SYSTEM_OUT" "$NAME_Q" "list (system) contains Q"
assert_contains "$LIST_SYSTEM_OUT" "$NAME_R" "list (system) contains R"

LIST_CLIENT_A_OUT=$(go run ./cmd/manage-presets list --client-id "$CLIENT_A_ID")
assert_contains "$LIST_CLIENT_A_OUT" "$NAME_P" "list (client A) contains user-scope P"
assert_contains "$LIST_CLIENT_A_OUT" "png" "list (client A) shows P's user-scope target png"

# ---------------------------------------------------------------------------
# Step 5b: SHOW -- reports target_format and version.
# ---------------------------------------------------------------------------
SHOW_Q_OUT=$(go run ./cmd/manage-presets show --name "$NAME_Q")
assert_contains "$SHOW_Q_OUT" "target_format: webp" "show Q reports target_format webp"
assert_contains "$SHOW_Q_OUT" "version: 1" "show Q reports version 1"

# ---------------------------------------------------------------------------
# Step 5c: UPDATE -- bump-on-update (D-04): prints new version 2; SQL
# confirms exactly one active row at v2, and v1 is now inactive.
# ---------------------------------------------------------------------------
UPDATE_R_OUT=$(go run ./cmd/manage-presets update --name "$NAME_R" --target png)
V=$(printf '%s\n' "$UPDATE_R_OUT" | awk -F': ' '/^new version:/{print $2}')
assert_eq "2" "$V" "update R prints new version 2"

ACTIVE_COUNT=$(psql_q "SELECT count(*) FROM presets WHERE scope='system' AND name='$NAME_R' AND is_active")
assert_eq "1" "$ACTIVE_COUNT" "exactly one active R row after update"

ACTIVE_VERSION=$(psql_q "SELECT version FROM presets WHERE scope='system' AND name='$NAME_R' AND is_active")
assert_eq "2" "$ACTIVE_VERSION" "active R row is version 2"

V1_ACTIVE=$(psql_q "SELECT is_active FROM presets WHERE scope='system' AND name='$NAME_R' AND version=1")
assert_eq "f" "$V1_ACTIVE" "R v1 is now inactive"

# ---------------------------------------------------------------------------
# Step 5d: DEACTIVATE -- zero active rows remain, rows still exist (no hard
# delete), and a subsequent POST with that preset -> 422.
# ---------------------------------------------------------------------------
DEACTIVATE_R_OUT=$(go run ./cmd/manage-presets deactivate --name "$NAME_R")
assert_contains "$DEACTIVATE_R_OUT" "deactivated: $NAME_R" "deactivate R confirms"

ACTIVE_COUNT_AFTER=$(psql_q "SELECT count(*) FROM presets WHERE scope='system' AND name='$NAME_R' AND is_active")
assert_eq "0" "$ACTIVE_COUNT_AFTER" "zero active R rows after deactivate"

TOTAL_R_ROWS=$(psql_q "SELECT count(*) FROM presets WHERE scope='system' AND name='$NAME_R'")
if [ "$TOTAL_R_ROWS" -lt 2 ]; then
	echo "FAIL: expected R rows to still exist (no hard delete), got count=$TOTAL_R_ROWS" >&2
	exit 1
fi
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: R rows still exist after deactivate (count=$TOTAL_R_ROWS, no hard delete)"

http_post "$WORKDIR/resp-r-deactivated.json" \
	-H "Authorization: ApiKey $KEY_A" \
	-F "preset=$NAME_R" \
	-F "file=@$WORKDIR/sample.png;type=image/png"
assert_eq "422" "$HTTP_STATUS" "POST with deactivated preset R -> 422"

# ---------------------------------------------------------------------------
# Step 6: SC2/D-08 provenance -- create a job via preset=Q, poll to done,
# assert preset_name/preset_version persisted.
# ---------------------------------------------------------------------------
http_post "$WORKDIR/resp-provenance.json" \
	-H "Authorization: ApiKey $KEY_A" \
	-F "preset=$NAME_Q" \
	-F "file=@$WORKDIR/sample.png;type=image/png"
assert_eq "202" "$HTTP_STATUS" "POST preset=Q (client A) -> 202"

JOB_ID_Q=$(grep -o '"job_id":"[^"]*"' "$WORKDIR/resp-provenance.json" | head -1 | cut -d'"' -f4)
[ -n "$JOB_ID_Q" ] || {
	echo "FAIL: no job_id in provenance response: $(cat "$WORKDIR/resp-provenance.json")" >&2
	exit 1
}

echo "--- polling job $JOB_ID_Q for done ---"
status=""
for i in $(seq 1 60); do
	http_get "$WORKDIR/resp-poll-q.json" -H "Authorization: ApiKey $KEY_A" "$API_BASE/v1/jobs/$JOB_ID_Q"
	status=$(grep -o '"status":"[^"]*"' "$WORKDIR/resp-poll-q.json" | head -1 | cut -d'"' -f4)
	if [ "$status" = "done" ] || [ "$status" = "failed" ]; then
		break
	fi
	sleep 2
done
assert_eq "done" "$status" "provenance job reaches done"

PROVENANCE=$(psql_q "SELECT preset_name||'/'||preset_version FROM jobs WHERE id='$JOB_ID_Q'")
assert_eq "$NAME_Q/1" "$PROVENANCE" "jobs table records preset provenance Q/1"

# ---------------------------------------------------------------------------
# Step 7: SC3/D-02 shadowing -- preset P resolves to user-scope target (png)
# for client A, and system-scope target (webp) for client B.
# ---------------------------------------------------------------------------
http_post "$WORKDIR/resp-shadow-a.json" \
	-H "Authorization: ApiKey $KEY_A" \
	-F "preset=$NAME_P" \
	-F "file=@$WORKDIR/sample.jpg;type=image/jpeg"
assert_eq "202" "$HTTP_STATUS" "POST preset=P (client A) -> 202"
JOB_ID_P_A=$(grep -o '"job_id":"[^"]*"' "$WORKDIR/resp-shadow-a.json" | head -1 | cut -d'"' -f4)

http_post "$WORKDIR/resp-shadow-b.json" \
	-H "Authorization: ApiKey $KEY_B" \
	-F "preset=$NAME_P" \
	-F "file=@$WORKDIR/sample.jpg;type=image/jpeg"
assert_eq "202" "$HTTP_STATUS" "POST preset=P (client B) -> 202"
JOB_ID_P_B=$(grep -o '"job_id":"[^"]*"' "$WORKDIR/resp-shadow-b.json" | head -1 | cut -d'"' -f4)

TARGET_A=$(psql_q "SELECT target_format FROM jobs WHERE id='$JOB_ID_P_A'")
assert_eq "png" "$TARGET_A" "client A resolves preset P to user-scope target png (shadowing)"

TARGET_B=$(psql_q "SELECT target_format FROM jobs WHERE id='$JOB_ID_P_B'")
assert_eq "webp" "$TARGET_B" "client B resolves preset P to system-scope target webp"

# ---------------------------------------------------------------------------
# Step 8: SC4/D-01 mutual exclusivity -- preset + target together -> 422.
# ---------------------------------------------------------------------------
http_post "$WORKDIR/resp-mutex.json" \
	-H "Authorization: ApiKey $KEY_A" \
	-F "preset=$NAME_Q" \
	-F "target=webp" \
	-F "file=@$WORKDIR/sample.png;type=image/png"
assert_eq "422" "$HTTP_STATUS" "POST preset+target together -> 422"

# ---------------------------------------------------------------------------
# Step 9: SC4/D-03 no-leak -- nonexistent preset and cross-client preset
# (S, client A's user-only preset, no system counterpart) both return
# byte-identical 422 bodies when requested as client B.
# ---------------------------------------------------------------------------
NONEXISTENT_NAME="pa-nonexistent-$SUFFIX"
http_post "$WORKDIR/resp-nonexistent.json" \
	-H "Authorization: ApiKey $KEY_A" \
	-F "preset=$NONEXISTENT_NAME" \
	-F "file=@$WORKDIR/sample.png;type=image/png"
assert_eq "422" "$HTTP_STATUS" "POST nonexistent preset -> 422"

http_post "$WORKDIR/resp-crossclient.json" \
	-H "Authorization: ApiKey $KEY_B" \
	-F "preset=$NAME_S" \
	-F "file=@$WORKDIR/sample.png;type=image/png"
assert_eq "422" "$HTTP_STATUS" "POST cross-client preset S (as client B) -> 422"

BODY_NONEXISTENT=$(cat "$WORKDIR/resp-nonexistent.json")
BODY_CROSSCLIENT=$(cat "$WORKDIR/resp-crossclient.json")
assert_eq '{"error":"unknown or inactive preset"}' "$BODY_NONEXISTENT" "nonexistent-preset body matches the single no-leak error text"
assert_eq "$BODY_NONEXISTENT" "$BODY_CROSSCLIENT" "nonexistent vs cross-client 422 bodies are byte-identical (no leak)"

# ---------------------------------------------------------------------------
# Step 10: SC5/D-06 stored-opts re-validation -- insert a preset whose stored
# opts fail CURRENT validation (margin_mm 9999 is out of [0,50]) directly via
# SQL (bypassing the CLI's D-11 write-time check), then prove POST rejects it.
# ---------------------------------------------------------------------------
NAME_BAD="pa-bad-$SUFFIX"
docker exec "$DB_CONTAINER" psql -U octo -d octo_db -tAc \
	"INSERT INTO presets (name, scope, operation, target_format, options) VALUES ('$NAME_BAD','system','convert','pdf','{\"margin_mm\":9999}'::jsonb)" >/dev/null

http_post "$WORKDIR/resp-badopts.json" \
	-H "Authorization: ApiKey $KEY_A" \
	-F "preset=$NAME_BAD" \
	-F "file=@$WORKDIR/sample.html;type=text/html"
assert_eq "422" "$HTTP_STATUS" "POST preset with stored opts failing current validation -> 422"

# ---------------------------------------------------------------------------
# Done.
# ---------------------------------------------------------------------------
echo ""
echo "=== ALL $PASS_COUNT ASSERTIONS PASSED ==="
echo "SC1 (all five CLI verbs): create/list/show/update/deactivate — PASS"
echo "SC2 (provenance $NAME_Q/1 persisted, job done): PASS"
echo "SC3 (shadowing: A->png, B->webp): PASS"
echo "SC4 (mutual exclusivity 422 + byte-identical no-leak 422): PASS"
echo "SC5 (stored-opts re-validation 422): PASS"
echo ""
echo "Stack left running for inspection (compose project: octoconv)."
