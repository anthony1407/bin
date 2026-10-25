#!/usr/bin/env bash
# Dropzones: an upload link lets someone without an account upload into one
# bucket folder. Uploads never overwrite, the size limit holds, the token is
# needed (and never in a URL the server logs), the console sees each upload
# with its IP and browser, and deleting the link stops it at once.
source "$(dirname "$0")/lib.sh"
# The test clients reach bin from the Docker network, as a reverse proxy would.
start_server -e BIN_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
console_login
api POST /buckets '{"name":"inbox"}' >/dev/null

DZ=$(api POST /dropzones '{"bucket":"inbox","prefix":"clients","note":"Send the signed contract","expiresIn":3600,"maxBytes":200000}')
check "create link" "status=201" "$DZ"
DZ_ID=$(echo "$DZ" | json_field id)
TOKEN=$(echo "$DZ" | json_field url | sed 's/.*#//')
check "link points to the upload page, token after #" "/upload#" "$(echo "$DZ" | json_field url)"
check "upload page is served without a session" '<div id="root">' "$(curl_ -s "http://$HOST:$CONSOLE_PORT/upload")"

# upload <name> <file in $WORK> [token]: what the public page sends.
upload() {
  docker run --rm --network "$NET" -v "$WORK_NATIVE:/work" "$CURL_IMAGE" -s -w '\nstatus=%{http_code}' \
    -X PUT -H "X-Dropzone-Token: ${3-$TOKEN}" -H 'User-Agent: Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0' \
    --data-binary "@/work/$2" "http://$HOST:$CONSOLE_PORT/api/v1/public/dropzone/files?name=$1"
}

check "public info hides the bucket" '"note":"Send the signed contract"' \
  "$(curl_ -s -H "X-Dropzone-Token: $TOKEN" "http://$HOST:$CONSOLE_PORT/api/v1/public/dropzone")"
random_file contract.pdf 150000
check "upload without a session" '"name":"contract.pdf"' "$(upload contract.pdf contract.pdf)"
check "same name is kept apart" '"name":"contract (1).pdf"' "$(upload contract.pdf contract.pdf)"
check "path in the name is dropped" '"name":"passwd"' "$(upload '..%2F..%2Fetc%2Fpasswd' contract.pdf)"
random_file big.bin 300000
check "over the per-file limit" "status=413" "$(upload big.bin big.bin)"
check "wrong token" "status=404" "$(upload x.pdf contract.pdf wrong-token)"
check "no token" "status=404" "$(upload x.pdf contract.pdf '')"

aws_ s3 cp "s3://inbox/clients/contract.pdf" /work/contract.copy >/dev/null
check "stored byte for byte" "SAME" "$(same contract.pdf contract.copy)"
check "stored under the link's folder" "clients/contract (1).pdf" "$(aws_ s3 ls --recursive s3://inbox)"

LIST=$(api GET /dropzones)
check "console counts what came in" '"uploads":3' "$LIST"
UPLOADS=$(api GET "/dropzones/$DZ_ID/uploads")
check "uploader browser recorded" 'Firefox/140.0' "$UPLOADS"
check "uploader IP recorded" '"remote":"' "$UPLOADS"
docker run --rm --network "$NET" -v "$WORK_NATIVE:/work" "$CURL_IMAGE" -s -o /dev/null -X PUT \
  -H "X-Dropzone-Token: $TOKEN" -H 'X-Forwarded-For: 203.0.113.7' --data-binary @/work/contract.pdf \
  "http://$HOST:$CONSOLE_PORT/api/v1/public/dropzone/files?name=behind-proxy.pdf"
check "client IP from a trusted proxy's X-Forwarded-For" '"name":"behind-proxy.pdf","size":150000,"uploadedAt":"' "$(api GET "/dropzones/$DZ_ID/uploads")"
check "real client IP recorded" '"remote":"203.0.113.7"' "$(api GET "/dropzones/$DZ_ID/uploads")"
check "token never in the access log" "absent" \
  "$(docker logs "$HOST" 2>&1 | grep -qF "$TOKEN" && echo present || echo absent)"
check "uploads in the audit log" '"action":"DropzoneUpload"' "$(api GET '/audit?q=DropzoneUpload')"

check "delete link" "status=204" "$(api DELETE "/dropzones/$DZ_ID")"
check "deleted link stops at once" "status=404" "$(upload late.pdf contract.pdf)"
check "files stay after the link is gone" "contract.pdf" "$(aws_ s3 ls --recursive s3://inbox)"

finish
