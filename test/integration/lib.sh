# Shared helpers for the integration scripts. Source this file; do not run it.
#
# Every script starts a bin container from $BIN_IMAGE and drives it with real
# clients (aws-cli, rclone, curl) running in Docker, so the only host
# requirements are bash, Docker and coreutils. Works on Linux, macOS and Git
# Bash on Windows.
#
# Environment:
#   BIN_IMAGE   image under test (default bin:dev; build it with "make image")
#   WORK        scratch directory (default: a new temporary directory)

set -uo pipefail
export MSYS_NO_PATHCONV=1 # Git Bash: do not rewrite /paths passed to docker

IMAGE=${BIN_IMAGE:-bin:dev}
NET=bin-test
HOST=bin-test
AK=testaccesskey
SK=testsecretkey12345

# Client versions are pinned so results are reproducible.
AWS_CLI_IMAGE=amazon/aws-cli:2.38.4
RCLONE_IMAGE=rclone/rclone:1.75.2
CURL_IMAGE=curlimages/curl:8.23.0

WORK=${WORK:-$(mktemp -d)}
mkdir -p "$WORK"

# native converts a path for programs outside Git Bash (Docker Desktop, Windows
# executables). Elsewhere it returns the path unchanged.
native() {
  if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else printf '%s' "$1"; fi
}
WORK_NATIVE=$(native "$WORK")

# --- server ----------------------------------------------------------------------

# start_server [docker run options...] starts bin and waits until it is healthy.
start_server() {
  docker network create "$NET" >/dev/null 2>&1 || true
  docker rm -f "$HOST" >/dev/null 2>&1
  docker run -d --name "$HOST" --network "$NET" "$@" \
    -e BIN_ROOT_ACCESS_KEY="$AK" -e BIN_ROOT_SECRET_KEY="$SK" "$IMAGE" >/dev/null || exit 1
  trap stop_server EXIT
  wait_ready
}

wait_ready() {
  for _ in $(seq 1 30); do
    if [ "$(curl_ -s -o /dev/null -w '%{http_code}' "http://$HOST:9001/api/v1/health")" = 200 ]; then
      return 0
    fi
    sleep 1
  done
  echo "bin did not become healthy; container log:" >&2
  docker logs "$HOST" >&2
  exit 1
}

stop_server() { docker rm -f "$HOST" >/dev/null 2>&1; }

# --- clients ---------------------------------------------------------------------

# aws_as <access key> <secret key> <region> <aws args...>
# Set AWS_TRANSFER=classic to use the Python transfer client instead of CRT.
aws_as() {
  local config=()
  if [ "${AWS_TRANSFER:-}" = classic ]; then
    printf '[default]\ns3 =\n  preferred_transfer_client = classic\n' >"$WORK/aws-classic.config"
    config=(-e AWS_CONFIG_FILE=/work/aws-classic.config)
  fi
  docker run --rm --network "$NET" -v "$WORK_NATIVE:/work" -w /work \
    -e AWS_ACCESS_KEY_ID="$1" -e AWS_SECRET_ACCESS_KEY="$2" -e AWS_DEFAULT_REGION="$3" \
    -e AWS_MAX_ATTEMPTS=1 "${config[@]}" \
    "$AWS_CLI_IMAGE" --endpoint-url "http://$HOST:9000" "${@:4}" 2>&1
}
aws_() { aws_as "$AK" "$SK" us-east-1 "$@"; }

# rclone_as <secret key> <rclone args...>; NOTICE lines are dropped unless
# RCLONE_KEEP_NOTICE is set (some results, like "0 differences", are notices).
rclone_as() {
  docker run --rm --network "$NET" -v "$WORK_NATIVE:/work" -w /work \
    -e RCLONE_CONFIG_REF_TYPE=s3 -e RCLONE_CONFIG_REF_PROVIDER=Other \
    -e RCLONE_CONFIG_REF_ENDPOINT="http://$HOST:9000" -e RCLONE_CONFIG_REF_REGION=us-east-1 \
    -e RCLONE_CONFIG_REF_ACCESS_KEY_ID="$AK" -e RCLONE_CONFIG_REF_SECRET_ACCESS_KEY="$1" \
    "$RCLONE_IMAGE" --retries 1 --low-level-retries 1 "${@:2}" 2>&1 |
    if [ -n "${RCLONE_KEEP_NOTICE:-}" ]; then cat; else grep -v NOTICE; fi
}
rclone_() { rclone_as "$SK" "$@"; }

curl_() { docker run --rm --network "$NET" "$CURL_IMAGE" "$@"; }

# --- assertions --------------------------------------------------------------------

PASS=0
FAIL=0

# check <label> <expected substring> <actual output>
check() {
  if [[ "$3" == *"$2"* ]]; then
    echo "PASS  $1"
    PASS=$((PASS + 1))
  else
    echo "FAIL  $1"
    echo "      expected: $2"
    echo "      got:      ${3:0:400}"
    FAIL=$((FAIL + 1))
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum <"$1"; else shasum -a 256 <"$1"; fi | cut -c1-64
}
md5_of() {
  if command -v md5sum >/dev/null 2>&1; then md5sum <"$1"; else md5 -q "$1"; fi | cut -c1-32
}

# same <file> <file> (relative to $WORK) prints SAME or DIFFERENT.
same() {
  if [ "$(sha256_of "$WORK/$1")" = "$(sha256_of "$WORK/$2")" ]; then echo SAME; else echo DIFFERENT; fi
}

# random_file <name> <bytes> writes random data into $WORK/<name>.
random_file() { head -c "$2" /dev/urandom >"$WORK/$1"; }

# finish prints the server's errors and a summary, and fails if any check failed.
finish() {
  local errors
  errors=$(docker logs "$HOST" 2>&1 | grep -E ' (ERROR|WARN) |"level":"(ERROR|WARN)"' | head -n 5)
  if [ -n "$errors" ]; then
    echo "--- server warnings and errors"
    echo "$errors"
  fi
  echo "passed=$PASS failed=$FAIL"
  [ "$FAIL" -eq 0 ]
}
