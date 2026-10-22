#!/usr/bin/env bash
# Listings: pagination past 1000 keys (V1 and V2), folders, unicode keys,
# sync in both directions, filtered recursive deletes, rclone check and purge.
source "$(dirname "$0")/lib.sh"
rm -rf "$WORK/tree" "$WORK/back"
mkdir -p "$WORK/tree/many" "$WORK/tree/docs" "$WORK/tree/deep/1/2/3" "$WORK/back"
for i in $(seq -w 1 1500); do printf 'file %s\n' "$i" >"$WORK/tree/many/f$i.txt"; done
printf 'spaces\n' >"$WORK/tree/docs/a b.txt"
printf 'unicode\n' >"$WORK/tree/docs/ñandú +1.txt"
printf 'top\n' >"$WORK/tree/top.txt"
printf 'deep\n' >"$WORK/tree/deep/1/2/3/f.txt"
random_file tree/docs/random.bin 300000
TOTAL=$(find "$WORK/tree" -type f | wc -l | tr -d ' ')
start_server
aws_ s3 mb s3://lst >/dev/null

check "aws s3 cp --recursive ($TOTAL files)" "upload:" "$(aws_ s3 cp --recursive tree s3://lst/tree/ --only-show-errors; echo upload:)"
check "aws s3 ls shows folders" "PRE many/" "$(aws_ s3 ls s3://lst/tree/)"
check "aws s3 ls --recursive counts every key" "$TOTAL" "$(aws_ s3 ls --recursive s3://lst/ | wc -l)"
check "list-objects-v2 paginates past 1000" "$TOTAL" "$(aws_ s3api list-objects-v2 --bucket lst --query 'length(Contents)')"
check "list-objects (v1) paginates past 1000" "$TOTAL" "$(aws_ s3api list-objects --bucket lst --query 'length(Contents)')"
check "unicode key listed and decoded" "docs/ñandú +1.txt" "$(aws_ s3 ls --recursive s3://lst/tree/docs/)"
check "delimiter at depth" "PRE 3/" "$(aws_ s3 ls s3://lst/tree/deep/1/2/)"
check "aws s3 sync up again uploads nothing" "nothing" "$(out=$(aws_ s3 sync tree s3://lst/tree/); [ -z "$out" ] && echo nothing || echo "$out")"
check "aws s3 sync down" "download:" "$(aws_ s3 sync s3://lst/tree/ back/ --only-show-errors; echo download:)"
check "  ... same tree" "same" "$(diff -r "$WORK/tree" "$WORK/back" >/dev/null && echo same || echo different)"
check "aws s3 rm --recursive with filters" "delete:" "$(aws_ s3 rm --recursive s3://lst/tree/many/ --exclude '*' --include 'f00*' | head -n 1)"
check "  ... 99 keys removed" "$((TOTAL - 99))" "$(aws_ s3api list-objects-v2 --bucket lst --query 'length(Contents)')"

check "rclone ls" "tree/docs/random.bin" "$(rclone_ ls ref:lst)"
check "rclone lsd" "tree" "$(rclone_ lsd ref:lst)"
check "rclone lsjson -R count" "$((TOTAL - 99))" "$(rclone_ lsjson -R --files-only ref:lst | grep -c '"Path"')"
check "rclone md5sum uses the ETag" "$(md5_of "$WORK/tree/docs/random.bin")" "$(rclone_ md5sum ref:lst/tree/docs/random.bin)"
check "rclone sync" "" "$(rclone_ sync tree ref:lst/rtree)"
check "rclone check: 0 differences" "0 differences found" "$(RCLONE_KEEP_NOTICE=1 rclone_ check tree ref:lst/rtree)"
check "rclone size" "($((TOTAL * 2 - 99)))" "$(rclone_ size ref:lst)"
check "rclone purge" "" "$(rclone_ purge ref:lst)"
check "  ... bucket gone" "(NoSuchBucket)" "$(aws_ s3api list-objects-v2 --bucket lst)"

finish
