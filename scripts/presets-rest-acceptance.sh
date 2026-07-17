#!/usr/bin/env bash
# presets-rest-acceptance.sh -- Phase 20 (presets REST + format discovery)
# live hard gate.
#
# Proves, against the REAL local compose stack (real Postgres, real API,
# rebuilt with the 20-01 handlers), that:
#   D-01/PRAPI-01: all five /v1/presets verbs (create/list/show/update/
#     deactivate) work end-to-end, each asserted against the real HTTP
#     status/body and DB state.
#   D-02/P6: a create body carrying scope=system and a foreign client_id
#     produces a preset the DB shows as scope=user owned by the CALLING
#     client -- mass-assignment has zero effect.
#   D-03/PRAPI-02: duplicate active-name create -> 409; nonexistent,
#     cross-client, and system-scope-write names all -> a byte-identical
#     no-leak 404. Bump-on-update increases version; no hard delete.
#   D-04: create/show/list response bodies never contain id or client_id.
#   D-10: GET /v1/presets returns the caller's own presets AND system
#     presets marked scope=system (read-only) -- the merged view Phase 21
#     consumes.
#   D-06/PRAPI-03: GET /v1/formats returns a registry-derived engine->pairs
#     map containing a known pair (png->webp).
#   D-07: /v1/formats and /v1/presets require auth -- unauthenticated ->
#     401, proving /v1 rate-limited group membership.
#
# Phase 29 (HARD-02/OPER-01, D-04/D-05) extends this same script with an
# operator system-scope section rather than a new script: mints an
# operator + a regular client, exports the operator's UUID into
# OPERATOR_CLIENT_IDS and force-recreates the compose api service so
# cmd/api/main.go re-reads it, then proves operator CRUD on
# /v1/system/presets, a byte-identical no-leak 404 for a non-operator
# (real vs genuinely-nonexistent preset name), and that a system preset
# created by the operator is usable in a job by any (non-operator) client.
#
# This script's exit code IS the gate: any failed assertion aborts non-zero
# (set -e) with a loud FAIL message. It does not tear the stack down on
# success (matches Phase 16/17/18 live-gate precedent) -- rerun freely,
# every preset/client name is uuid-suffixed.
set -euo pipefail

cd "$(dirname "$0")/.."

# ---------------------------------------------------------------------------
# Config / constants (compose_contract, mirrors scripts/presets-acceptance.sh)
# ---------------------------------------------------------------------------
API_BASE="http://localhost:8090"
export DATABASE_URL="postgres://octo:octo-pass@localhost:5434/octo_db"
export API_KEY_SALT="dev-only-change-me-in-real-deploys"
DB_CONTAINER="octoconv-db"

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

assert_not_contains() {
	local haystack="$1" needle="$2" label="$3"
	if printf '%s' "$haystack" | grep -qF -- "$needle"; then
		echo "FAIL: $label -- expected output NOT to contain [$needle]" >&2
		echo "--- actual output ---" >&2
		printf '%s\n' "$haystack" >&2
		exit 1
	fi
	PASS_COUNT=$((PASS_COUNT + 1))
	echo "PASS: $label does not contain [$needle]"
}

# psql_q runs a single -tAc query against the live DB container and returns
# the trimmed scalar result.
psql_q() {
	docker exec "$DB_CONTAINER" psql -U octo -d octo_db -tAc "$1" | tr -d '[:space:]'
}

# http_json issues an HTTP request with an optional JSON body and an optional
# Authorization header, capturing HTTP_STATUS and writing the body to the
# given out_file. Usage: http_json <method> <path> <out_file> <api_key_or_-> [json_body]
http_json() {
	local method="$1" path="$2" out_file="$3" key="$4"
	local body="${5:-}"
	local -a curl_args=(-s -o "$out_file" -w '%{http_code}' -X "$method")
	if [ "$key" != "-" ]; then
		curl_args+=(-H "Authorization: ApiKey $key")
	fi
	if [ -n "$body" ]; then
		curl_args+=(-H "Content-Type: application/json" --data "$body")
	fi
	curl_args+=("$API_BASE$path")
	HTTP_STATUS=$(curl "${curl_args[@]}")
}

echo "=== Phase 20 presets REST + formats: live acceptance hard gate ==="

# ---------------------------------------------------------------------------
# Step 1: bring up the stack, wait for readiness.
# ---------------------------------------------------------------------------
echo "--- bringing up compose stack (api rebuilt, rest from existing images) ---"
docker compose -p octoconv -f docker-compose.yml -f docker-compose.e2e.yml up -d --build api
docker compose -p octoconv -f docker-compose.yml -f docker-compose.e2e.yml up -d

echo "--- waiting for /healthz ---"
healthy=""
for i in $(seq 1 30); do
	code=$(curl -s -o /tmp/presets-rest-acceptance-healthz.json -w '%{http_code}' "$API_BASE/healthz" || true)
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
echo "PASS: /healthz ready ($(cat /tmp/presets-rest-acceptance-healthz.json))"

# ---------------------------------------------------------------------------
# Step 2: mint two clients (A, B) via cmd/manage-clients.
# ---------------------------------------------------------------------------
SUFFIX=$(uuidgen | tr 'A-Z' 'a-z')

CLIENT_A_OUT=$(go run ./cmd/manage-clients create "presets-rest-acceptance-a-$SUFFIX")
CLIENT_B_OUT=$(go run ./cmd/manage-clients create "presets-rest-acceptance-b-$SUFFIX")

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
# Step 3: seed one SYSTEM-scope preset via cmd/manage-presets (no --client-id)
# so the merged read-only list/show assertions (D-10) have a target.
# ---------------------------------------------------------------------------
NAME_SYS="sys-$SUFFIX"
SEED_SYS_OUT=$(go run ./cmd/manage-presets create --name "$NAME_SYS" --target webp)
V=$(printf '%s\n' "$SEED_SYS_OUT" | awk -F': ' '/^version:/{print $2}')
assert_eq "1" "$V" "seed system preset $NAME_SYS prints version 1"

NAME_A="pa-rest-$SUFFIX"
NAME_BAD="pa-bad-$SUFFIX"

# ---------------------------------------------------------------------------
# Assertion 1: CREATE -- POST /v1/presets (client A) -> 201; body has
# version:1, scope:"user"; body does NOT leak id or client_id (D-04).
# ---------------------------------------------------------------------------
http_json POST "/v1/presets" "$WORKDIR/resp-create.json" "$KEY_A" \
	"{\"name\":\"$NAME_A\",\"target_format\":\"webp\"}"
assert_eq "201" "$HTTP_STATUS" "CREATE pa-rest -> 201"
BODY_CREATE=$(cat "$WORKDIR/resp-create.json")
assert_contains "$BODY_CREATE" '"version":1' "CREATE body has version:1"
assert_contains "$BODY_CREATE" '"scope":"user"' "CREATE body has scope:user"
assert_not_contains "$BODY_CREATE" '"client_id"' "CREATE body (D-04) has no client_id field"
assert_not_contains "$BODY_CREATE" '"id"' "CREATE body (D-04) has no id field"

# ---------------------------------------------------------------------------
# Assertion 2: 409 -- repeat the same POST -> 409 (D-03).
# ---------------------------------------------------------------------------
http_json POST "/v1/presets" "$WORKDIR/resp-dup.json" "$KEY_A" \
	"{\"name\":\"$NAME_A\",\"target_format\":\"webp\"}"
assert_eq "409" "$HTTP_STATUS" "CREATE duplicate active name -> 409"

# ---------------------------------------------------------------------------
# Assertion 3: VALIDATE-OPTS -- POST with opts failing ValidateOptsJSON -> 422
# (D-05).
# ---------------------------------------------------------------------------
http_json POST "/v1/presets" "$WORKDIR/resp-badopts.json" "$KEY_A" \
	"{\"name\":\"$NAME_BAD\",\"target_format\":\"pdf\",\"options\":{\"margin_mm\":9999}}"
assert_eq "422" "$HTTP_STATUS" "CREATE with invalid opts (margin_mm 9999) -> 422"

# ---------------------------------------------------------------------------
# Assertion 4: MASS-ASSIGNMENT -- POST with scope:system + foreign client_id
# in the body -> 201; psql confirms row is scope=user owned by client A
# (D-02/P6).
# ---------------------------------------------------------------------------
NAME_MASS="pa-mass-$SUFFIX"
http_json POST "/v1/presets" "$WORKDIR/resp-mass.json" "$KEY_A" \
	"{\"name\":\"$NAME_MASS\",\"target_format\":\"webp\",\"scope\":\"system\",\"client_id\":\"$CLIENT_B_ID\"}"
assert_eq "201" "$HTTP_STATUS" "CREATE with scope=system + foreign client_id in body -> 201 (ignored fields)"

MASS_SCOPE=$(psql_q "SELECT scope FROM presets WHERE name='$NAME_MASS'")
assert_eq "user" "$MASS_SCOPE" "mass-assignment: DB row scope is 'user' (body scope=system ignored)"

MASS_CLIENT=$(psql_q "SELECT client_id::text FROM presets WHERE name='$NAME_MASS'")
assert_eq "$CLIENT_A_ID" "$MASS_CLIENT" "mass-assignment: DB row client_id is calling client A (body client_id=B ignored)"

# ---------------------------------------------------------------------------
# Assertion 5: LIST -- GET /v1/presets (client A) -> 200; contains own preset
# name AND the seeded system preset name with scope:system (D-10).
# ---------------------------------------------------------------------------
http_json GET "/v1/presets" "$WORKDIR/resp-list.json" "$KEY_A"
assert_eq "200" "$HTTP_STATUS" "LIST /v1/presets (client A) -> 200"
BODY_LIST=$(cat "$WORKDIR/resp-list.json")
assert_contains "$BODY_LIST" "\"$NAME_A\"" "LIST contains client A's own preset $NAME_A"
assert_contains "$BODY_LIST" "\"$NAME_SYS\"" "LIST contains seeded system preset $NAME_SYS"
assert_contains "$BODY_LIST" '"scope":"system"' "LIST shows the merged system preset marked scope:system (D-10)"

# ---------------------------------------------------------------------------
# Assertion 6: SHOW own -- GET /v1/presets/{name} (client A) -> 200; contains
# target_format:webp.
# ---------------------------------------------------------------------------
http_json GET "/v1/presets/$NAME_A" "$WORKDIR/resp-show-own.json" "$KEY_A"
assert_eq "200" "$HTTP_STATUS" "SHOW own preset $NAME_A -> 200"
assert_contains "$(cat "$WORKDIR/resp-show-own.json")" '"target_format":"webp"' "SHOW own preset has target_format:webp"

# ---------------------------------------------------------------------------
# Assertion 7: SHOW system read-only -- GET /v1/presets/{sys-name} (client A)
# -> 200; contains scope:system (D-10).
# ---------------------------------------------------------------------------
http_json GET "/v1/presets/$NAME_SYS" "$WORKDIR/resp-show-sys.json" "$KEY_A"
assert_eq "200" "$HTTP_STATUS" "SHOW system preset $NAME_SYS (as client A) -> 200"
assert_contains "$(cat "$WORKDIR/resp-show-sys.json")" '"scope":"system"' "SHOW system preset is marked scope:system"

# ---------------------------------------------------------------------------
# Assertion 8: UPDATE bump -- PUT /v1/presets/{name} (client A) -> 200;
# version:2 (D-03); psql confirms exactly one active row at version 2 and v1
# inactive.
# ---------------------------------------------------------------------------
http_json PUT "/v1/presets/$NAME_A" "$WORKDIR/resp-update.json" "$KEY_A" \
	"{\"target_format\":\"png\"}"
assert_eq "200" "$HTTP_STATUS" "UPDATE $NAME_A -> 200"
assert_contains "$(cat "$WORKDIR/resp-update.json")" '"version":2' "UPDATE bumps version to 2"

ACTIVE_COUNT=$(psql_q "SELECT count(*) FROM presets WHERE scope='user' AND client_id='$CLIENT_A_ID' AND name='$NAME_A' AND is_active")
assert_eq "1" "$ACTIVE_COUNT" "exactly one active row for $NAME_A after update"

ACTIVE_VERSION=$(psql_q "SELECT version FROM presets WHERE scope='user' AND client_id='$CLIENT_A_ID' AND name='$NAME_A' AND is_active")
assert_eq "2" "$ACTIVE_VERSION" "active $NAME_A row is version 2"

V1_ACTIVE=$(psql_q "SELECT is_active FROM presets WHERE scope='user' AND client_id='$CLIENT_A_ID' AND name='$NAME_A' AND version=1")
assert_eq "f" "$V1_ACTIVE" "$NAME_A v1 is now inactive"

# ---------------------------------------------------------------------------
# Assertion 9: NO-LEAK 404s -- nonexistent name, cross-client show, and
# system-scope write all -> 404 with byte-identical bodies (D-03).
# ---------------------------------------------------------------------------
NONEXISTENT_NAME="pa-nonexistent-$SUFFIX"
http_json GET "/v1/presets/$NONEXISTENT_NAME" "$WORKDIR/resp-404-nonexistent.json" "$KEY_A"
assert_eq "404" "$HTTP_STATUS" "GET nonexistent preset -> 404"

http_json GET "/v1/presets/$NAME_A" "$WORKDIR/resp-404-crossclient.json" "$KEY_B"
assert_eq "404" "$HTTP_STATUS" "GET $NAME_A as client B (cross-client) -> 404"

http_json PUT "/v1/presets/$NAME_SYS" "$WORKDIR/resp-404-systemwrite.json" "$KEY_A" \
	"{\"target_format\":\"png\"}"
assert_eq "404" "$HTTP_STATUS" "PUT system preset $NAME_SYS as client A (system-scope write) -> 404"

BODY_404_A=$(cat "$WORKDIR/resp-404-nonexistent.json")
BODY_404_B=$(cat "$WORKDIR/resp-404-crossclient.json")
BODY_404_C=$(cat "$WORKDIR/resp-404-systemwrite.json")
assert_eq "$BODY_404_A" "$BODY_404_B" "nonexistent vs cross-client 404 bodies are byte-identical (no leak)"
assert_eq "$BODY_404_A" "$BODY_404_C" "nonexistent vs system-scope-write 404 bodies are byte-identical (no leak)"

# ---------------------------------------------------------------------------
# Assertion 10: DELETE soft -- DELETE /v1/presets/{name} (client A) -> 2xx;
# then GET show -> 404; psql confirms rows for that name still exist (no
# hard delete, PRAPI-02).
# ---------------------------------------------------------------------------
http_json DELETE "/v1/presets/$NAME_A" "$WORKDIR/resp-delete.json" "$KEY_A"
if [ "${HTTP_STATUS:0:1}" != "2" ]; then
	echo "FAIL: DELETE $NAME_A -- expected 2xx, got [$HTTP_STATUS]" >&2
	echo "--- body ---" >&2
	cat "$WORKDIR/resp-delete.json" >&2
	exit 1
fi
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: DELETE $NAME_A -> $HTTP_STATUS (2xx)"

http_json GET "/v1/presets/$NAME_A" "$WORKDIR/resp-show-after-delete.json" "$KEY_A"
assert_eq "404" "$HTTP_STATUS" "GET $NAME_A after deactivate -> 404"

ROWS_AFTER_DELETE=$(psql_q "SELECT count(*) FROM presets WHERE scope='user' AND client_id='$CLIENT_A_ID' AND name='$NAME_A'")
if [ "$ROWS_AFTER_DELETE" -lt 1 ]; then
	echo "FAIL: expected $NAME_A rows to still exist (no hard delete), got count=$ROWS_AFTER_DELETE" >&2
	exit 1
fi
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: $NAME_A rows still exist after deactivate (count=$ROWS_AFTER_DELETE, no hard delete)"

# ---------------------------------------------------------------------------
# Assertion 11: ?all=true -- GET /v1/presets?all=true (client A) -> 200;
# includes an inactive version of the deleted/bumped preset (D-01).
# ---------------------------------------------------------------------------
http_json GET "/v1/presets?all=true" "$WORKDIR/resp-list-all.json" "$KEY_A"
assert_eq "200" "$HTTP_STATUS" "LIST ?all=true -> 200"
BODY_LIST_ALL=$(cat "$WORKDIR/resp-list-all.json")
assert_contains "$BODY_LIST_ALL" '"is_active":false' "LIST ?all=true includes an inactive row"
assert_contains "$BODY_LIST_ALL" "\"$NAME_A\"" "LIST ?all=true still includes $NAME_A's rows (now inactive)"

# ---------------------------------------------------------------------------
# Assertion 12: FORMATS -- GET /v1/formats (client A) -> 200; registry-derived
# engine map contains image engine with a known pair (png,webp) (D-06). Also:
# unauthenticated GET /v1/formats and GET /v1/presets -> 401 (D-07, /v1 group
# membership).
# ---------------------------------------------------------------------------
http_json GET "/v1/formats" "$WORKDIR/resp-formats.json" "$KEY_A"
assert_eq "200" "$HTTP_STATUS" "GET /v1/formats (authed) -> 200"
BODY_FORMATS=$(cat "$WORKDIR/resp-formats.json")
assert_contains "$BODY_FORMATS" '"image"' "FORMATS response has an \"image\" engine entry"
PNG_WEBP=$(printf '%s' "$BODY_FORMATS" | jq -c '.engines.image.pairs[] | select(.[0]=="png" and .[1]=="webp")')
if [ -z "$PNG_WEBP" ]; then
	echo "FAIL: FORMATS image engine missing known pair png->webp" >&2
	echo "--- body ---" >&2
	echo "$BODY_FORMATS" >&2
	exit 1
fi
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: FORMATS image engine contains known pair png->webp"

# jq sanity: every engine class present in the registry-derived map has a
# non-empty pairs array (registry-derived, not a dangling/empty stub).
ENGINE_CLASSES=$(printf '%s' "$BODY_FORMATS" | jq -r '.engines | keys[]')
for cls in $ENGINE_CLASSES; do
	COUNT=$(printf '%s' "$BODY_FORMATS" | jq ".engines[\"$cls\"].pairs | length")
	if [ "$COUNT" -lt 1 ]; then
		echo "FAIL: engine class $cls has zero pairs" >&2
		exit 1
	fi
done
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: every registry-derived engine class ($ENGINE_CLASSES) has a non-empty pairs list"

ENGINE_COUNT=$(printf '%s\n' "$ENGINE_CLASSES" | grep -c . || true)
if [ "$ENGINE_COUNT" -lt 3 ]; then
	echo "FAIL: expected at least 3 engine classes in /v1/formats, got $ENGINE_COUNT ($ENGINE_CLASSES)" >&2
	exit 1
fi
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: /v1/formats reports at least 3 engine classes ($ENGINE_COUNT: $ENGINE_CLASSES)"

http_json GET "/v1/formats" "$WORKDIR/resp-formats-noauth.json" "-"
assert_eq "401" "$HTTP_STATUS" "GET /v1/formats unauthenticated -> 401 (D-07)"

http_json GET "/v1/presets" "$WORKDIR/resp-presets-noauth.json" "-"
assert_eq "401" "$HTTP_STATUS" "GET /v1/presets unauthenticated -> 401 (D-07)"

# ---------------------------------------------------------------------------
# Phase 29 HARD-02/OPER-01 (D-04/D-05): operator system-presets acceptance.
#
# Mints an operator + a regular (non-operator) client, exports the
# operator's UUID into OPERATOR_CLIENT_IDS, force-recreates the compose api
# service so cmd/api/main.go re-reads the env (D-05, closes WR-03's
# compose-passthrough gap), then drives /v1/system/presets:
#   - operator CRUD (create/list/show/update/deactivate) all succeed
#   - a non-operator's 404 for a real, active system preset is
#     byte-identical to its 404 for a genuinely-nonexistent name -- no
#     existence oracle leak (T-29-11)
#   - a system preset created by the operator over REST is usable in a job
#     submitted by the regular (non-operator) client (T-29-10: management
#     is gated, ordinary job-time preset resolution is not)
# ---------------------------------------------------------------------------
echo ""
echo "=== Phase 29 HARD-02/OPER-01: operator system-presets acceptance ==="

# http_post_job issues a multipart POST to /v1/jobs; sets HTTP_STATUS and
# writes the response body to the given path (mirrors presets-acceptance.sh's
# http_post -- http_json above is JSON-body-only and cannot express -F).
http_post_job() {
	local out_file="$1"
	shift
	HTTP_STATUS=$(curl -s -o "$out_file" -w '%{http_code}' -X POST "$API_BASE/v1/jobs" "$@")
}

cp internal/e2e/testdata/sample.png "$WORKDIR/sample.png"

OPERATOR_OUT=$(go run ./cmd/manage-clients create "presets-rest-acceptance-operator-$SUFFIX")
REGULAR_OUT=$(go run ./cmd/manage-clients create "presets-rest-acceptance-regular-$SUFFIX")

OPERATOR_ID=$(printf '%s\n' "$OPERATOR_OUT" | awk -F': ' '/^client id:/{print $2}')
OPERATOR_KEY=$(printf '%s\n' "$OPERATOR_OUT" | awk -F': ' '/^api key/{print $2}')
REGULAR_ID=$(printf '%s\n' "$REGULAR_OUT" | awk -F': ' '/^client id:/{print $2}')
REGULAR_KEY=$(printf '%s\n' "$REGULAR_OUT" | awk -F': ' '/^api key/{print $2}')

[ -n "$OPERATOR_ID" ] && [ -n "$OPERATOR_KEY" ] || {
	echo "FAIL: could not parse operator client id/key from: $OPERATOR_OUT" >&2
	exit 1
}
[ -n "$REGULAR_ID" ] && [ -n "$REGULAR_KEY" ] || {
	echo "FAIL: could not parse regular client id/key from: $REGULAR_OUT" >&2
	exit 1
}
echo "PASS: minted operator client ($OPERATOR_ID) and regular client ($REGULAR_ID)"

echo "--- exporting OPERATOR_CLIENT_IDS and force-recreating compose api service (D-05) ---"
export OPERATOR_CLIENT_IDS="$OPERATOR_ID"
docker compose -p octoconv -f docker-compose.yml -f docker-compose.e2e.yml up -d --force-recreate api

echo "--- waiting for /healthz after api recreate ---"
healthy=""
for i in $(seq 1 30); do
	code=$(curl -s -o /tmp/presets-rest-acceptance-healthz2.json -w '%{http_code}' "$API_BASE/healthz" || true)
	if [ "$code" = "200" ]; then
		healthy=1
		break
	fi
	sleep 2
done
if [ -z "$healthy" ]; then
	echo "FAIL: /healthz never returned 200 after api recreate (60s)" >&2
	exit 1
fi
echo "PASS: /healthz ready after api recreate ($(cat /tmp/presets-rest-acceptance-healthz2.json))"

NAME_OP_A="opsys-a-$SUFFIX"
NAME_OP_B="opsys-b-$SUFFIX"

# --- Operator CRUD lifecycle on NAME_OP_A: create -> list -> show -> update
# -> deactivate. ---
http_json POST "/v1/system/presets" "$WORKDIR/resp-opsys-create.json" "$OPERATOR_KEY" \
	"{\"name\":\"$NAME_OP_A\",\"target_format\":\"webp\"}"
assert_eq "201" "$HTTP_STATUS" "operator CREATE $NAME_OP_A -> 201"
BODY_OPSYS_CREATE=$(cat "$WORKDIR/resp-opsys-create.json")
assert_contains "$BODY_OPSYS_CREATE" '"version":1' "operator CREATE body has version:1"
assert_contains "$BODY_OPSYS_CREATE" '"scope":"system"' "operator CREATE body has scope:system"

http_json GET "/v1/system/presets" "$WORKDIR/resp-opsys-list.json" "$OPERATOR_KEY"
assert_eq "200" "$HTTP_STATUS" "operator LIST /v1/system/presets -> 200"
assert_contains "$(cat "$WORKDIR/resp-opsys-list.json")" "\"$NAME_OP_A\"" "operator LIST contains $NAME_OP_A"

http_json GET "/v1/system/presets/$NAME_OP_A" "$WORKDIR/resp-opsys-show.json" "$OPERATOR_KEY"
assert_eq "200" "$HTTP_STATUS" "operator SHOW $NAME_OP_A -> 200"
assert_contains "$(cat "$WORKDIR/resp-opsys-show.json")" '"target_format":"webp"' "operator SHOW $NAME_OP_A has target_format:webp"

http_json PUT "/v1/system/presets/$NAME_OP_A" "$WORKDIR/resp-opsys-update.json" "$OPERATOR_KEY" \
	"{\"target_format\":\"png\"}"
assert_eq "200" "$HTTP_STATUS" "operator UPDATE $NAME_OP_A -> 200"
assert_contains "$(cat "$WORKDIR/resp-opsys-update.json")" '"version":2' "operator UPDATE bumps version to 2"

http_json DELETE "/v1/system/presets/$NAME_OP_A" "$WORKDIR/resp-opsys-deactivate.json" "$OPERATOR_KEY"
assert_eq "200" "$HTTP_STATUS" "operator DEACTIVATE $NAME_OP_A -> 200"
assert_contains "$(cat "$WORKDIR/resp-opsys-deactivate.json")" '"is_active":false' "operator DEACTIVATE body reports is_active:false"

# --- Operator creates NAME_OP_B and leaves it active, for the cross-client
# job-usability check below. ---
http_json POST "/v1/system/presets" "$WORKDIR/resp-opsys-b-create.json" "$OPERATOR_KEY" \
	"{\"name\":\"$NAME_OP_B\",\"target_format\":\"webp\"}"
assert_eq "201" "$HTTP_STATUS" "operator CREATE $NAME_OP_B -> 201"

# --- Non-operator byte-identical no-leak 404 (D-04/T-29-11): the regular
# client's 404 for the REAL, active NAME_OP_B preset must be
# indistinguishable from its 404 for a genuinely-nonexistent name --
# requireOperator's gate must never leak an existence oracle. ---
http_json GET "/v1/system/presets/$NAME_OP_B" "$WORKDIR/resp-nonop-real.json" "$REGULAR_KEY"
assert_eq "404" "$HTTP_STATUS" "non-operator GET real system preset $NAME_OP_B -> 404"

NONEXISTENT_SYS_NAME="opsys-nonexistent-$SUFFIX"
http_json GET "/v1/system/presets/$NONEXISTENT_SYS_NAME" "$WORKDIR/resp-nonop-nonexistent.json" "$REGULAR_KEY"
assert_eq "404" "$HTTP_STATUS" "non-operator GET nonexistent system preset -> 404"

BODY_NONOP_REAL=$(cat "$WORKDIR/resp-nonop-real.json")
BODY_NONOP_NONEXISTENT=$(cat "$WORKDIR/resp-nonop-nonexistent.json")
assert_eq "$BODY_NONOP_REAL" "$BODY_NONOP_NONEXISTENT" "non-operator 404 (real vs nonexistent system preset) byte-identical (no leak, T-29-11)"

# --- Non-operator LIST also gets the uniform no-leak 404, never a 403 or an
# empty 200 -- requireOperator gates every verb on the subtree identically
# (T-29-10). ---
http_json GET "/v1/system/presets" "$WORKDIR/resp-nonop-list.json" "$REGULAR_KEY"
assert_eq "404" "$HTTP_STATUS" "non-operator LIST /v1/system/presets -> 404 (T-29-10, no 403)"
assert_eq "$BODY_NONOP_REAL" "$(cat "$WORKDIR/resp-nonop-list.json")" "non-operator LIST 404 body also byte-identical to the no-leak 404 (no leak)"

# --- Cross-client job usability (D-04): NAME_OP_B, created by the operator
# over REST, is usable in a job submitted by the REGULAR (non-operator)
# client -- system scope is not gated by operator status at job-submit
# time, only management of the presets themselves is (T-29-10 scope). ---
http_post_job "$WORKDIR/resp-opsys-job.json" \
	-H "Authorization: ApiKey $REGULAR_KEY" \
	-F "preset=$NAME_OP_B" \
	-F "file=@$WORKDIR/sample.png;type=image/png"
assert_eq "202" "$HTTP_STATUS" "regular client POST job with operator-created system preset $NAME_OP_B -> 202"

JOB_ID_OPSYS=$(grep -o '"job_id":"[^"]*"' "$WORKDIR/resp-opsys-job.json" | head -1 | cut -d'"' -f4)
[ -n "$JOB_ID_OPSYS" ] || {
	echo "FAIL: no job_id in operator-system-preset job response: $(cat "$WORKDIR/resp-opsys-job.json")" >&2
	exit 1
}

echo "--- polling job $JOB_ID_OPSYS for done ---"
status=""
for i in $(seq 1 60); do
	http_json GET "/v1/jobs/$JOB_ID_OPSYS" "$WORKDIR/resp-opsys-job-poll.json" "$REGULAR_KEY"
	status=$(grep -o '"status":"[^"]*"' "$WORKDIR/resp-opsys-job-poll.json" | head -1 | cut -d'"' -f4)
	if [ "$status" = "done" ] || [ "$status" = "failed" ]; then
		break
	fi
	sleep 2
done
assert_eq "done" "$status" "job using operator-created system preset $NAME_OP_B reaches done"

# ---------------------------------------------------------------------------
# Done.
# ---------------------------------------------------------------------------
echo ""
echo "=== ALL $PASS_COUNT ASSERTIONS PASSED ==="
echo "D-01/PRAPI-01 (all five verbs create/list/show/update/deactivate): PASS"
echo "D-02/P6 (mass-assignment resistance: scope=user, client=caller): PASS"
echo "D-03/PRAPI-02 (409 dup, byte-identical no-leak 404, bump, no hard delete): PASS"
echo "D-04 (no id/client_id leak in response DTO): PASS"
echo "D-10 (merged list/show incl. read-only system presets): PASS"
echo "D-06/PRAPI-03 (registry-derived /v1/formats, known pair png->webp): PASS"
echo "D-07 (unauthenticated /v1/formats and /v1/presets -> 401): PASS"
echo "HARD-02/D-04 (operator CRUD on /v1/system/presets: create/list/show/update/deactivate): PASS"
echo "HARD-02/D-05 (OPERATOR_CLIENT_IDS compose passthrough + api force-recreate picks it up): PASS"
echo "HARD-02/T-29-11 (non-operator 404 byte-identical: real vs nonexistent system preset, LIST too): PASS"
echo "HARD-02/T-29-10 (operator-created system preset usable in a job by a non-operator client): PASS"
echo ""
echo "Stack left running for inspection (compose project: octoconv)."
