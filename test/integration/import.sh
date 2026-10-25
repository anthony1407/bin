#!/usr/bin/env bash
# Import from another S3 server: a second bin container plays the remote. The
# console lists its buckets, copies them (content, content type and user
# metadata kept), reports job progress, skips buckets that exist locally, and
# refuses loopback and metadata addresses without echoing what they answer.
source "$(dirname "$0")/lib.sh"
REMOTE=bin-test-remote
start_server
docker rm -f "$REMOTE" >/dev/null 2>&1
docker run -d --name "$REMOTE" --network "$NET" \
  -e BIN_ROOT_ACCESS_KEY=remoteaccess -e BIN_ROOT_SECRET_KEY=remotesecret123 "$IMAGE" >/dev/null || exit 1
trap 'stop_server; docker rm -f "$REMOTE" >/dev/null 2>&1' EXIT
HOST=$REMOTE wait_ready

# remote_aws <aws args...> runs aws-cli against the remote server.
remote_aws() { HOST=$REMOTE aws_as remoteaccess remotesecret123 us-east-1 "$@"; }

random_file photo.jpg 300000
printf 'readme\n' >"$WORK/readme.txt"
remote_aws s3 mb s3://photos >/dev/null
remote_aws s3 mb s3://docs >/dev/null
remote_aws s3 mb s3://taken >/dev/null
remote_aws s3 cp /work/photo.jpg s3://photos/2026/photo.jpg --content-type image/jpeg --metadata camera=x100 >/dev/null
remote_aws s3 cp /work/readme.txt s3://docs/readme.txt --content-type text/plain >/dev/null
check "remote has data" "photo.jpg" "$(remote_aws s3 ls --recursive s3://photos)"

console_login
REMOTE_JSON='"endpoint":"http://'"$REMOTE"':9000","accessKey":"remoteaccess","secretKey":"remotesecret123","region":"us-east-1"'
check "list remote buckets" '"name":"photos"' "$(api POST /import/test "{$REMOTE_JSON}")"
check "wrong remote secret: no remote error text" '"error":"remote_auth"' \
  "$(api POST /import/test '{"endpoint":"http://'"$REMOTE"':9000","accessKey":"remoteaccess","secretKey":"wrong-secret-1"}')"
check "loopback endpoint refused" '"error":"endpoint_not_allowed"' \
  "$(api POST /import/test '{"endpoint":"http://127.0.0.1:9000","accessKey":"a","secretKey":"b"}')"
check "metadata endpoint refused" '"error":"endpoint_not_allowed"' \
  "$(api POST /import/test '{"endpoint":"http://169.254.169.254","accessKey":"a","secretKey":"b"}')"
check "endpoint with a path refused" '"error":"invalid_endpoint"' \
  "$(api POST /import/test '{"endpoint":"http://'"$REMOTE"':9000/photos","accessKey":"a","secretKey":"b"}')"

api POST /buckets '{"name":"taken"}' >/dev/null
START=$(api POST /import/start "{$REMOTE_JSON,\"buckets\":[\"photos\",\"docs\",\"taken\"]}")
check "import started" "status=200" "$START"
check "existing local bucket skipped" '"bucket":"taken","reason"' "$START"

# Wait for both jobs to finish.
for _ in $(seq 1 30); do
  JOBS=$(api GET /import/jobs)
  [[ "$JOBS" != *'"state":"running"'* && "$JOBS" != *'"state":"queued"'* ]] && break
  sleep 1
done
check "jobs finished" '"state":"done"' "$JOBS"
check "job counts objects" '"copied":1' "$JOBS"
check "no failed objects" "none" "$(echo "$JOBS" | grep -q '"failed":[1-9]' && echo some || echo none)"

aws_ s3 cp s3://photos/2026/photo.jpg /work/photo.copy >/dev/null
check "content copied byte for byte" "SAME" "$(same photo.jpg photo.copy)"
HEAD=$(aws_ s3api head-object --bucket photos --key 2026/photo.jpg)
check "content type kept" '"ContentType": "image/jpeg"' "$HEAD"
check "user metadata kept" '"camera": "x100"' "$HEAD"
check "second bucket copied" "readme.txt" "$(aws_ s3 ls s3://docs)"
check "import is in the audit log" '"action":"ImportBuckets"' "$(api GET '/audit?q=ImportBuckets')"

finish
