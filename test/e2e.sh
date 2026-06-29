#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="${1:-"$ROOT/bin/object-server"}"
DATA="$ROOT/test/data"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/homework1-e2e.XXXXXX")"
PID=""

cleanup() {
	if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
		kill -INT "$PID" 2>/dev/null || true
		wait "$PID" 2>/dev/null || true
	fi
	rm -rf "$WORK"
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	if [[ -f "$WORK/server.log" ]]; then
		echo "Server log:" >&2
		cat "$WORK/server.log" >&2
	fi
	exit 1
}

request() {
	local expected_status="$1"
	shift

	local actual_status
	actual_status="$(curl --silent --show-error \
		--dump-header "$WORK/headers" \
		--output "$WORK/body" \
		--write-out '%{http_code}' \
		"$@")"
	[[ "$actual_status" == "$expected_status" ]] ||
		fail "expected HTTP $expected_status, received $actual_status for $*"
}

assert_json_id() {
	local expected_id="$1"
	local actual
	actual="$(tr -d '\r\n' <"$WORK/body")"
	[[ "$actual" == "{\"id\":\"$expected_id\"}" ]] ||
		fail "expected JSON id $expected_id, received $actual"
}

assert_location() {
	local expected="$1"
	tr -d '\r' <"$WORK/headers" | grep -Fqx "Location: $expected" ||
		fail "expected Location header $expected"
}

assert_body_file() {
	local expected_file="$1"
	cmp --silent "$expected_file" "$WORK/body" ||
		fail "response body does not match $expected_file"
}

wait_for_server() {
	local base_url="$1"
	for _ in {1..100}; do
		if curl --silent --output /dev/null "$base_url/not-a-route" 2>/dev/null; then
			return
		fi
		if ! kill -0 "$PID" 2>/dev/null; then
			fail "server exited during startup"
		fi
		sleep 0.05
	done
	fail "server did not become ready"
}

stop_server() {
	kill -INT "$PID"
	if ! wait "$PID"; then
		fail "server did not shut down cleanly"
	fi
	PID=""
}

run_cases() {
	local backend="$1"
	local port="$2"
	local base_url="http://127.0.0.1:$port"
	local bucket_a="$base_url/objects/bucket-a"
	local bucket_b="$base_url/objects/bucket-b"

	echo "Testing $backend backend"
	OBJECT_STORE_PORT="$port" \
		OBJECT_STORE_BACKEND="$backend" \
		OBJECT_STORE_DATA_DIR="$WORK/disk-data" \
		"$BINARY" >"$WORK/server.log" 2>&1 &
	PID=$!
	wait_for_server "$base_url"

	# Missing objects return 404.
	request 404 "$bucket_a/missing"

	# Create and retrieve an object.
	request 201 -X PUT -H "Content-Type: text/plain" \
		--data-binary "@$DATA/object-a.txt" "$bucket_a/original"
	assert_json_id "original"
	request 200 "$bucket_a/original"
	assert_body_file "$DATA/object-a.txt"

	# An existing object ID is immutable, even when different content is supplied.
	request 409 -X PUT -H "Content-Type: text/plain" \
		--data-binary "@$DATA/object-b.txt" "$bucket_a/original"
	assert_json_id "original"
	assert_location "/objects/bucket-a/original"

	# Duplicate content under a fresh ID is rejected within the same bucket.
	request 409 -X PUT -H "Content-Type: text/plain" \
		--data-binary "@$DATA/object-a.txt" "$bucket_a/duplicate"
	assert_json_id "original"
	assert_location "/objects/bucket-a/original"

	# Identical content is allowed in a different bucket.
	request 201 -X PUT -H "Content-Type: text/plain" \
		--data-binary "@$DATA/object-a.txt" "$bucket_b/duplicate"
	assert_json_id "duplicate"
	request 200 "$bucket_b/duplicate"
	assert_body_file "$DATA/object-a.txt"

	# Delete succeeds once, and subsequent reads and deletes return 404.
	request 200 -X DELETE "$bucket_a/original"
	request 404 "$bucket_a/original"
	request 404 -X DELETE "$bucket_a/original"

	stop_server
	echo "Passed $backend backend"
}

[[ -x "$BINARY" ]] || fail "server binary is not executable: $BINARY"
[[ -f "$DATA/object-a.txt" && -f "$DATA/object-b.txt" ]] ||
	fail "test fixtures are missing from $DATA"

BASE_PORT=$((20000 + ($$ % 10000)))
run_cases memory "$BASE_PORT"
run_cases disk "$((BASE_PORT + 1))"

echo "All end-to-end tests passed"
