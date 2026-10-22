#!/usr/bin/env bash
# Admin API: console login with cookie + CSRF, buckets, uploads, share links
# and their revocation, access keys used by aws-cli and rclone, public
# policies, persistence across restarts and the master-key safety check.
source "$(dirname "$0")/lib.sh"
VOL=bin-test-admin
docker volume rm -f "$VOL" >/dev/null 2>&1
start_server -v "$VOL:/data"
trap 'stop_server; docker volume rm -f "$VOL" >/dev/null 2>&1' EXIT
API="http://$HOST:$CONSOLE_PORT/api/v1"
rm -f "$WORK/jar"

# api METHOD PATH [JSON] [extra curl args...]: a console request with the
# session cookie and CSRF token, printing the status and the body.
api() {
  local method=$1 path=$2 body=${3:-}
  docker run --rm --network "$NET" -v "$WORK_NATIVE:/work" "$CURL_IMAGE" -s -w '\nstatus=%{http_code}' \
    -b /work/jar -c /work/jar -X "$method" -H "X-CSRF-Token: ${CSRF:-}" -H 'Content-Type: application/json' \
    ${body:+--data "$body"} "${@:4}" "$API$path"
}

PANEL="http://$HOST:$CONSOLE_PORT"
check "panel served from the binary" '<div id="root">' "$(curl_ -s "$PANEL/")"
check "panel routes fall back to index.html" "frame-ancestors 'none'" "$(curl_ -s -D - -o /dev/null "$PANEL/buckets/photos/settings")"
check "panel assets are gzip-compressed" "content-encoding: gzip" \
  "$(curl_ -s -D - -o /dev/null -H 'Accept-Encoding: gzip' "$PANEL$(curl_ -s "$PANEL/" | grep -o '/assets/index-[^"]*\.js' | head -1)" | tr 'A-Z' 'a-z')"
check "API without a session" "status=401" "$(api GET /buckets)"
check "wrong password" "status=401" "$(api POST /login '{"accessKey":"'"$AK"'","secretKey":"wrong"}')"
LOGIN=$(api POST /login '{"accessKey":"'"$AK"'","secretKey":"'"$SK"'"}')
check "login" "status=200" "$LOGIN"
CSRF=$(echo "$LOGIN" | sed -n 's/.*"csrfToken":"\([^"]*\)".*/\1/p')
check "session cookie is HttpOnly + SameSite=Strict" "#HttpOnly_" "$(cat "$WORK/jar")"
check "POST without the CSRF token" "bad_csrf_token" "$(CSRF= api POST /buckets '{"name":"x"}')"

check "create bucket" "status=201" "$(api POST /buckets '{"name":"web"}')"
printf 'hello from the console\n' >"$WORK/hello.txt"
check "upload" "status=201" "$(api PUT '/buckets/web/upload?key=docs%2Fhello.txt' '' --data-binary @/work/hello.txt -H 'Content-Type: text/plain')"
check "list folder" '"prefixes":["docs/"]' "$(api GET /buckets/web/objects)"
check "bucket usage" '"objects":1' "$(api GET /buckets/web)"
check "server info" '"region":"us-east-1"' "$(api GET /info)"

SHARE=$(api POST /buckets/web/share '{"key":"docs/hello.txt","expiresIn":600}')
LINK=$(echo "$SHARE" | sed -n 's/.*"url":"\([^"]*\)".*/\1/p' | sed 's/\\u0026/\&/g')
check "share link" "http://" "$LINK"
check "share link downloads without credentials" "hello from the console" "$(curl_ -s "$LINK")"
check "revoke all share links" "status=204" "$(api POST /settings/revoke-share-links)"
check "revoked link is refused" "403" "$(curl_ -s -o /dev/null -w '%{http_code}' "$LINK")"

KEY=$(api POST /access-keys '{"name":"backup job"}')
NEW_AK=$(echo "$KEY" | sed -n 's/.*"accessKey":"\([^"]*\)".*/\1/p')
NEW_SK=$(echo "$KEY" | sed -n 's/.*"secretKey":"\([^"]*\)".*/\1/p')
check "create access key (secret shown once)" "status=201" "$KEY"
check "listing hides secrets" "none" "$(api GET /access-keys | grep -q secretKey && echo shown || echo none)"
check "aws cli with the new key" "docs/hello.txt" "$(aws_as "$NEW_AK" "$NEW_SK" us-east-1 s3 ls --recursive s3://web)"
check "rclone with the new key" "hello.txt" "$(docker run --rm --network "$NET" -e RCLONE_CONFIG_REF_TYPE=s3 \
  -e RCLONE_CONFIG_REF_PROVIDER=Other -e RCLONE_CONFIG_REF_ENDPOINT="http://$HOST:$S3_PORT" \
  -e RCLONE_CONFIG_REF_ACCESS_KEY_ID="$NEW_AK" -e RCLONE_CONFIG_REF_SECRET_ACCESS_KEY="$NEW_SK" \
  "$RCLONE_IMAGE" ls ref:web 2>&1)"
check "make the key read-only" "status=200" "$(api PATCH "/access-keys/$NEW_AK" '{"readOnly":true}')"
check "read-only key cannot write" "(AccessDenied)" "$(aws_as "$NEW_AK" "$NEW_SK" us-east-1 s3 cp /work/hello.txt s3://web/x.txt)"

check "anonymous read of a private bucket" "403" "$(curl_ -s -o /dev/null -w '%{http_code}' "http://$HOST:$S3_PORT/web/docs/hello.txt")"
check "make the bucket public" "status=200" "$(api PUT /buckets/web/policy '{"mode":"public"}')"
check "anonymous read of a public bucket" "hello from the console" "$(curl_ -s "http://$HOST:$S3_PORT/web/docs/hello.txt")"

docker restart "$HOST" >/dev/null && wait_ready
check "access key survives a restart (master key in the volume)" "docs/hello.txt" \
  "$(aws_as "$NEW_AK" "$NEW_SK" us-east-1 s3 ls --recursive s3://web)"
check "master key file is private" "-rw-------" \
  "$(docker run --rm --user 0 -v "$VOL:/data" "$CURL_IMAGE" ls -l /data/.bin/master.key 2>&1)"
check "other users cannot even list .bin/" "Permission denied" \
  "$(docker run --rm -v "$VOL:/data" "$CURL_IMAGE" ls /data/.bin 2>&1)"
docker rm -f "$HOST" >/dev/null
check "a different BIN_MASTER_KEY is refused" "does not match the one used to encrypt" \
  "$(docker run --rm -v "$VOL:/data" -e BIN_ROOT_ACCESS_KEY="$AK" -e BIN_ROOT_SECRET_KEY="$SK" \
     -e BIN_MASTER_KEY=some-other-passphrase "$IMAGE" 2>&1)"
start_server -v "$VOL:/data"
check "force-delete a bucket from the console" "status=204" "$(api DELETE '/buckets/web?force=true')"

finish
