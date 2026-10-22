#!/usr/bin/env bash
# SigV4: valid signatures complete real operations (unicode keys,
# sub-delimiters, signed query strings, presigned URLs); invalid ones are
# rejected with the right S3 error.
source "$(dirname "$0")/lib.sh"
start_server
printf 'hello bin\n' >"$WORK/hello.txt"

check "valid signature (CreateBucket)" "make_bucket: sig" "$(aws_ s3 mb s3://sig)"
check "valid signature (ListBuckets)" "sig" "$(aws_ s3 ls)"
check "signed body sha256 (PutObject)" '"ETag"' "$(aws_ s3api put-object --bucket sig --key hello.txt --body hello.txt)"
aws_ s3api put-object --bucket sig --key 'folder with spaces/ñandú +1.txt' --body hello.txt >/dev/null
aws_ s3api put-object --bucket sig --key "report(final)!=*'.txt" --body hello.txt >/dev/null
check "unicode key in the canonical path" '"ContentLength": 10' \
  "$(aws_ s3api head-object --bucket sig --key 'folder with spaces/ñandú +1.txt')"
check "sub-delimiters in the key" '"ContentLength": 10' \
  "$(aws_ s3api head-object --bucket sig --key "report(final)!=*'.txt")"
check "signed query string (ListObjectsV2)" '"Prefix": "folder with spaces/"' \
  "$(aws_ s3api list-objects-v2 --bucket sig --prefix 'folder with' --delimiter /)"
check "wrong secret" "(SignatureDoesNotMatch)" "$(aws_as "$AK" wrongsecret12345 us-east-1 s3 ls)"
check "unknown access key" "(InvalidAccessKeyId)" "$(aws_as nobody "$SK" us-east-1 s3 ls)"
check "wrong region" "(AuthorizationHeaderMalformed)" "$(aws_as "$AK" "$SK" eu-west-1 s3 ls)"

URL=$(aws_ s3 presign s3://sig/hello.txt --expires-in 300)
check "presigned URL" "200" "$(curl_ -s -o /dev/null -w '%{http_code}' "$URL")"
check "tampered presigned URL" "403" "$(curl_ -s -o /dev/null -w '%{http_code}' "${URL/hello.txt/other.txt}")"
SHORT=$(aws_ s3 presign s3://sig/hello.txt --expires-in 1)
sleep 2
check "expired presigned URL" "403" "$(curl_ -s -o /dev/null -w '%{http_code}' "$SHORT")"
check "anonymous request" "403" "$(curl_ -s -o /dev/null -w '%{http_code}' "http://$HOST:9000/sig/hello.txt")"
check "presigned signature is redacted in the log" "X-Amz-Signature=REDACTED" "$(docker logs "$HOST" 2>&1 | grep -m1 X-Amz-Signature)"

check "rclone: valid signature" "sig" "$(rclone_ lsd ref:)"
check "rclone: wrong secret" "SignatureDoesNotMatch" "$(rclone_as wrongsecret12345 lsd ref:)"

finish
