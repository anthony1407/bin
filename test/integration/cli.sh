#!/usr/bin/env bash
# CLI and logs inside the container: --version, --help, `docker exec bin`,
# the startup banner, warnings for misspelled settings, one readable line per
# request, quiet health probes, graceful shutdown, and the JSON log format.
source "$(dirname "$0")/lib.sh"

check "docker run IMAGE --version" "bin version " "$(docker run --rm "$IMAGE" --version)"
check "docker run IMAGE --help lists the settings" "BIN_ROOT_ACCESS_KEY" "$(docker run --rm "$IMAGE" --help)"
check "unknown command fails with a hint" 'run "bin --help"' "$(docker run --rm "$IMAGE" frobnicate 2>&1; echo "exit=$?")"
check "missing credentials are explained" "BIN_ROOT_ACCESS_KEY is required" "$(docker run --rm "$IMAGE" 2>&1)"

start_server -e BIN_ROOT_SECRET=typo
check "docker exec CONTAINER bin --version" "bin version " "$(docker exec "$HOST" bin --version)"
aws_ s3 ls >/dev/null
aws_ s3api head-bucket --bucket missing-bucket >/dev/null
sleep 1
LOG=$(docker logs "$HOST" 2>&1)
check "banner shows the version" "Version:   " "$LOG"
check "banner shows the S3 API URLs" "S3 API:    http://" "$LOG"
check "banner shows the console URLs" "Console:   http://" "$LOG"
check "misspelled setting is reported" "BIN_ROOT_SECRET is not a bin setting and is ignored; did you mean BIN_ROOT_SECRET_KEY?" "$LOG"
check "request line with operation" "GET / -> 200" "$(echo "$LOG" | grep 'op=ListBuckets')"
check "request line with the S3 error code" "error=NoSuchBucket" "$(echo "$LOG" | grep 'HEAD /missing-bucket -> 404')"
check "health probes are not logged" "none" "$(echo "$LOG" | grep -q '/api/v1/health' && echo logged || echo none)"
docker stop -t 20 "$HOST" >/dev/null
LOG=$(docker logs "$HOST" 2>&1)
check "shutdown names the signal" "shutting down: received SIGTERM" "$LOG"
check "shutdown completes" "bin stopped" "$LOG"

start_server -e BIN_LOG_FORMAT=json
aws_ s3 ls >/dev/null
LOG=$(docker logs "$HOST" 2>&1)
check "json: startup record" '"msg":"bin started"' "$LOG"
check "json: request record" '"op":"ListBuckets"' "$LOG"

finish
