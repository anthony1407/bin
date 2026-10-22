#!/usr/bin/env bash
# Durability: SIGKILL bin while 8 clients write, restart it, and verify every
# acknowledged write. Objects are downloaded and compared by SHA-256.
#
# Usage: test/integration/crash.sh [buckets|objects]   (needs Go and Docker)
#
# SIGKILL keeps the OS page cache, so this proves crash consistency and that
# acknowledgements are only sent after a durable commit; it does not simulate
# a power cut.
source "$(dirname "$0")/lib.sh"
KIND=${1:-objects}
PORT=${BIN_TEST_PORT:-19000}
VOL=bin-test-crash
ROOT=$(cd "$(dirname "$0")/../.." && pwd)

EXE="$WORK/crashtest$(go env GOEXE)"
(cd "$ROOT" && go build -o "$(native "$EXE")" ./test/integration/crashtest) || exit 1

docker volume rm -f "$VOL" >/dev/null 2>&1
start_server -p "$PORT:9000" -v "$VOL:/data"
trap 'stop_server; docker volume rm -f "$VOL" >/dev/null 2>&1' EXIT
ACKED=$(native "$WORK/acked-$KIND.txt")

"$EXE" write -kind "$KIND" -endpoint "http://localhost:$PORT" -out "$ACKED" -for 8s &
WRITER=$!
sleep 4
echo "killing bin with SIGKILL..."
docker kill -s KILL "$HOST" >/dev/null
wait $WRITER
docker start "$HOST" >/dev/null && wait_ready

out=$("$EXE" verify -kind "$KIND" -endpoint "http://localhost:$PORT" -out "$ACKED")
echo "$out"
# With nothing acknowledged the test proves nothing (e.g. the server never came up).
acked=$(echo "$out" | sed -n 's/.*acknowledged=\([0-9]*\).*/\1/p' | head -n 1)
check "writes were acknowledged before the kill" "yes" "$([ "${acked:-0}" -gt 0 ] && echo yes || echo "no ($out)")"
check "every acknowledged $KIND write survived" "missing=0" "$out"
if [ "$KIND" = objects ]; then
  check "no acknowledged object is corrupt" "corrupt=0" "$out"
fi
finish
