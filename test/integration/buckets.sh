#!/usr/bin/env bash
# Buckets: create, list, head, location, versioning, tagging and delete with
# aws-cli and rclone, plus persistence across a container restart.
source "$(dirname "$0")/lib.sh"
VOL=bin-test-buckets
docker volume rm -f "$VOL" >/dev/null 2>&1
start_server -v "$VOL:/data"
trap 'stop_server; docker volume rm -f "$VOL" >/dev/null 2>&1' EXIT

check "aws s3 mb" "make_bucket: photos" "$(aws_ s3 mb s3://photos)"
check "s3api create-bucket" '"Location": "/backups"' "$(aws_ s3api create-bucket --bucket backups)"
check "create existing -> 409" "(BucketAlreadyOwnedByYou)" "$(aws_ s3api create-bucket --bucket photos)"
check "invalid name" "(InvalidBucketName)" "$(aws_ s3api create-bucket --bucket Bad_Name)"
check "wrong location constraint" "(IllegalLocationConstraintException)" \
  "$(aws_ s3api create-bucket --bucket elsewhere --create-bucket-configuration LocationConstraint=eu-west-1)"
check "aws s3 ls" "photos" "$(aws_ s3 ls)"
check "list-buckets owner" '"DisplayName": "bin"' "$(aws_ s3api list-buckets)"
check "head-bucket" '"BucketRegion": "us-east-1"' "$(aws_ s3api head-bucket --bucket photos)"
check "head-bucket missing" "(404)" "$(aws_ s3api head-bucket --bucket missing)"
check "get-bucket-location" '"LocationConstraint": null' "$(aws_ s3api get-bucket-location --bucket photos)"
check "get-bucket-versioning (never enabled)" "" "$(aws_ s3api get-bucket-versioning --bucket photos)"
check "put-bucket-tagging" "" "$(aws_ s3api put-bucket-tagging --bucket photos --tagging 'TagSet=[{Key=team,Value=home}]')"
check "get-bucket-tagging" '"Value": "home"' "$(aws_ s3api get-bucket-tagging --bucket photos)"
check "duplicate tag keys" "(InvalidTag)" \
  "$(aws_ s3api put-bucket-tagging --bucket photos --tagging 'TagSet=[{Key=a,Value=1},{Key=a,Value=2}]')"
check "delete-bucket-tagging" "" "$(aws_ s3api delete-bucket-tagging --bucket photos)"
check "no tags -> NoSuchTagSet" "(NoSuchTagSet)" "$(aws_ s3api get-bucket-tagging --bucket photos)"

check "rclone mkdir" "" "$(rclone_ mkdir ref:rclone-bucket)"
check "rclone mkdir existing (409 tolerated)" "" "$(rclone_ mkdir ref:rclone-bucket)"
check "rclone lsd" "rclone-bucket" "$(rclone_ lsd ref:)"
check "rclone rmdir" "" "$(rclone_ rmdir ref:rclone-bucket)"
check "rclone lsd after rmdir" "photos" "$(rclone_ lsd ref:)"

aws_ s3api put-bucket-tagging --bucket backups --tagging 'TagSet=[{Key=keep,Value=yes}]' >/dev/null
docker restart "$HOST" >/dev/null && wait_ready
check "buckets survive a restart" "backups" "$(aws_ s3 ls)"
check "tags survive a restart" '"Value": "yes"' "$(aws_ s3api get-bucket-tagging --bucket backups)"
check "aws s3 rb" "remove_bucket: photos" "$(aws_ s3 rb s3://photos)"
check "delete missing bucket" "(NoSuchBucket)" "$(aws_ s3api delete-bucket --bucket photos)"

finish
