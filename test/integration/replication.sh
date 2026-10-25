#!/usr/bin/env bash
# Replication rules between two bin containers: push (offsite backup) and
# pull, copying only what changed, mirroring deletes only for objects the
# rule copied, and reporting each run on the rule.
source "$(dirname "$0")/lib.sh"
REMOTE=bin-test-remote
start_server
docker rm -f "$REMOTE" >/dev/null 2>&1
docker run -d --name "$REMOTE" --network "$NET" \
  -e BIN_ROOT_ACCESS_KEY=remoteaccess -e BIN_ROOT_SECRET_KEY=remotesecret123 "$IMAGE" >/dev/null || exit 1
trap 'stop_server; docker rm -f "$REMOTE" >/dev/null 2>&1' EXIT
HOST=$REMOTE wait_ready
remote_aws() { HOST=$REMOTE aws_as remoteaccess remotesecret123 us-east-1 "$@"; }
console_login

# run_rule <id>: Sync now, then wait until the run is recorded.
run_rule() {
  api POST "/replication/rules/$1/run" >/dev/null
  for _ in $(seq 1 30); do
    sleep 1
    RULES=$(api GET /replication/rules)
    [[ "$RULES" != *'"running":true'* ]] && return
  done
}

# --- push -------------------------------------------------------------------------
remote_aws s3 mb s3://offsite >/dev/null
printf 'not ours\n' >"$WORK/manual.txt"
remote_aws s3 cp /work/manual.txt s3://offsite/bin/manual.txt >/dev/null
aws_ s3 mb s3://photos >/dev/null
random_file a.jpg 200000
printf 'bee\n' >"$WORK/b.txt"
aws_ s3 cp /work/a.jpg s3://photos/a.jpg --content-type image/jpeg >/dev/null
aws_ s3 cp /work/b.txt s3://photos/dir/b.txt >/dev/null

REMOTE_JSON='"endpoint":"http://'"$REMOTE"':9000","accessKey":"remoteaccess","secretKey":"remotesecret123","region":"us-east-1"'
PUSH=$(api POST /replication/rules '{"name":"offsite","direction":"push","bucket":"photos","prefix":"",'"$REMOTE_JSON"',
  "remoteBucket":"offsite","remotePrefix":"bin","intervalSeconds":3600,"mirrorDeletes":true,"enabled":false}')
check "create push rule" "status=201" "$PUSH"
check "secret never sent back" "absent" "$(echo "$PUSH" | grep -q remotesecret123 && echo present || echo absent)"
PUSH_ID=$(echo "$PUSH" | json_field id)

run_rule "$PUSH_ID"
check "push: first run copies everything" '"lastCopied":2' "$RULES"
remote_aws s3 cp s3://offsite/bin/a.jpg /work/a.copy >/dev/null
check "push: content byte for byte" "SAME" "$(same a.jpg a.copy)"
check "push: content type kept" '"ContentType": "image/jpeg"' "$(remote_aws s3api head-object --bucket offsite --key bin/a.jpg)"
run_rule "$PUSH_ID"
check "push: unchanged objects are skipped" '"lastCopied":0' "$RULES"

aws_ s3 rm s3://photos/dir/b.txt >/dev/null
printf 'new\n' >"$WORK/c.txt"
aws_ s3 cp /work/c.txt s3://photos/c.txt >/dev/null
run_rule "$PUSH_ID"
check "push: new object copied, deleted one mirrored" '"lastCopied":1,"lastDeleted":1' "$RULES"
LISTING=$(remote_aws s3 ls --recursive s3://offsite)
check "push: deleted object gone from the remote" "absent" "$([[ "$LISTING" == *dir/b.txt* ]] && echo present || echo absent)"
check "push: objects the rule did not copy are left alone" "bin/manual.txt" "$LISTING"

# --- pull -------------------------------------------------------------------------
remote_aws s3 mb s3://feed >/dev/null
remote_aws s3 cp /work/b.txt s3://feed/report.txt >/dev/null
aws_ s3 mb s3://inbox >/dev/null
PULL=$(api POST /replication/rules '{"name":"partner feed","direction":"pull","bucket":"inbox","prefix":"partner",'"$REMOTE_JSON"',
  "remoteBucket":"feed","remotePrefix":"","intervalSeconds":60,"mirrorDeletes":false,"enabled":false}')
PULL_ID=$(echo "$PULL" | json_field id)
run_rule "$PULL_ID"
check "pull: remote object copied into the local folder" "partner/report.txt" "$(aws_ s3 ls --recursive s3://inbox)"

check "bad remote bucket name refused, field named" '"field":"remoteBucket"' \
  "$(api POST /replication/rules '{"name":"x","direction":"push","bucket":"photos",'"$REMOTE_JSON"',"remoteBucket":"X","intervalSeconds":60}')"
check "loopback remote refused" '"field":"endpoint"' \
  "$(api POST /replication/rules '{"name":"x","direction":"push","bucket":"photos","endpoint":"http://127.0.0.1:9000","accessKey":"a","secretKey":"b","remoteBucket":"offsite","intervalSeconds":60}')"
check "runs are in the audit log" '"action":"RunReplication"' "$(api GET '/audit?q=RunReplication')"
check "delete rule" "status=204" "$(api DELETE "/replication/rules/$PUSH_ID")"

finish
