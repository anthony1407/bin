#!/usr/bin/env bash
# CLI and logs inside the container: --version, --help, `docker exec bin`,
# the startup banner, warnings for misspelled settings, one readable line per
# request, quiet health probes, graceful shutdown, and the JSON log format.
source "$(dirname "$0")/lib.sh"

check "docker run IMAGE --version" "bin version " "$(docker run --rm "$IMAGE" --version)"
check "docker run IMAGE --help lists the settings" "BIN_ROOT_ACCESS_KEY" "$(docker run --rm "$IMAGE" --help)"
check "unknown command fails with a hint" 'run "bin --help"' "$(docker run --rm "$IMAGE" frobnicate 2>&1; echo "exit=$?")"
check "missing credentials are explained" "BIN_ROOT_ACCESS_KEY (or BIN_ROOT_USER, BIN_ACCESS_KEY) is required" "$(docker run --rm "$IMAGE" 2>&1)"
check "--help lists the credential aliases" "also accepted: BIN_ROOT_USER, BIN_ACCESS_KEY" "$(docker run --rm "$IMAGE" --help)"
for pair in "BIN_ROOT_USER BIN_ROOT_PASSWORD" "BIN_ACCESS_KEY BIN_SECRET_KEY"; do
  set -- $pair
  docker rm -f bin-alias >/dev/null 2>&1
  docker run -d --name bin-alias --network "$NET" -e "$1=$AK" -e "$2=$SK" "$IMAGE" >/dev/null
  sleep 2
  check "credentials via $1 / $2" "200" \
    "$(curl_ -s -o /dev/null -w '%{http_code}' "http://bin-alias:9001/api/v1/health")"
  docker rm -f bin-alias >/dev/null 2>&1
done
check "conflicting aliases are rejected" "are set to different values" \
  "$(docker run --rm -e BIN_ROOT_USER=alice -e BIN_ACCESS_KEY=bob -e BIN_ROOT_PASSWORD=long-secret "$IMAGE" 2>&1)"

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

# Custom ports, MinIO style: bin server /data --address ... --console-address ...
SERVER_ARGS=(server /data --address :9100 --console-address :9101)
S3_PORT=9100
CONSOLE_PORT=9101
start_server
check "custom ports: S3 API answers on 9100" "make_bucket: ports" "$(aws_ s3 mb s3://ports)"
check "custom ports: console answers on 9101" "200" \
  "$(curl_ -s -o /dev/null -w '%{http_code}' "http://$HOST:9101/api/v1/health")"
check "custom ports: banner shows them" "http://127.0.0.1:9101" "$(docker logs "$HOST" 2>&1)"
check "custom ports: nothing listens on 9000" "000" \
  "$(curl_ -s -o /dev/null -w '%{http_code}' "http://$HOST:9000/")"
check "API health endpoint on the custom port" "200" \
  "$(curl_ -s -o /dev/null -w '%{http_code}' "http://$HOST:9100/_health/live")"
check "bin healthcheck finds the custom console port" "healthy" \
  "$(docker exec "$HOST" bin healthcheck && echo healthy)"
SERVER_ARGS=(server --address :9300 --console-address :9300)
check "same port for both is rejected" "must not be the same address" \
  "$(docker run --rm -e BIN_ROOT_ACCESS_KEY="$AK" -e BIN_ROOT_SECRET_KEY="$SK" "$IMAGE" "${SERVER_ARGS[@]}" 2>&1)"

finish
