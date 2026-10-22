#!/usr/bin/env bash
# Objects: put/get/head/copy/delete with every checksum algorithm, ranges,
# conditions and metadata; aws s3 cp with both transfer clients; presigned
# downloads; rclone transfers. Downloads are compared by SHA-256.
source "$(dirname "$0")/lib.sh"
start_server
printf 'hello bin\n' >"$WORK/hello.txt"
random_file seven.bin $((7 * 1024 * 1024)) # below the 8 MiB multipart threshold
random_file small.bin $((300 * 1024))
aws_ s3 mb s3://objs >/dev/null

# --- aws s3api -------------------------------------------------------------------
check "put-object echoes the default CRC64NVME" '"ChecksumCRC64NVME": "YF6KHApiEII="' \
  "$(aws_ s3api put-object --bucket objs --key hello.txt --body hello.txt --metadata author=bin)"
check "default content type" '"ContentType": "binary/octet-stream"' "$(aws_ s3api head-object --bucket objs --key hello.txt)"
check "user metadata" '"author": "bin"' "$(aws_ s3api head-object --bucket objs --key hello.txt)"
for alg in SHA256 CRC32 CRC32C SHA1; do
  check "put-object --checksum-algorithm $alg" '"ETag"' \
    "$(aws_ s3api put-object --bucket objs --key "sum-$alg.bin" --body small.bin --checksum-algorithm $alg)"
  check "get-object --checksum-mode ENABLED ($alg, validated by botocore)" '"ContentLength": 307200' \
    "$(aws_ s3api get-object --bucket objs --key "sum-$alg.bin" --checksum-mode ENABLED "out-$alg.down")"
  check "  ... content matches" "SAME" "$(same small.bin "out-$alg.down")"
done
check "range bytes=0-3" '"ContentRange": "bytes 0-3/10"' \
  "$(aws_ s3api get-object --bucket objs --key hello.txt --range bytes=0-3 range.down)"
check "  ... range content" "hell" "$(cat "$WORK/range.down")"
check "unsatisfiable range" "(InvalidRange)" "$(aws_ s3api get-object --bucket objs --key hello.txt --range bytes=50- x.down)"
check "If-Match mismatch" "(PreconditionFailed)" "$(aws_ s3api get-object --bucket objs --key hello.txt --if-match '"nomatch"' x.down)"
check "head-object missing" "(404)" "$(aws_ s3api head-object --bucket objs --key missing)"
check "get-object missing" "(NoSuchKey)" "$(aws_ s3api get-object --bucket objs --key missing x.down)"
check "unicode key round trip" '"ETag"' "$(aws_ s3api put-object --bucket objs --key 'carpeta con espacios/ñandú +1.txt' --body hello.txt)"
check "  ... head" '"ContentLength": 10' "$(aws_ s3api head-object --bucket objs --key 'carpeta con espacios/ñandú +1.txt')"
check "copy-object keeps the ETag" '"ETag": "\"fa82155eb1e21d28ee0e4a0bf1f24488\""' \
  "$(aws_ s3api copy-object --bucket objs --key copy.txt --copy-source objs/hello.txt)"
check "copy-object REPLACE" '"ETag"' "$(aws_ s3api copy-object --bucket objs --key copy.txt --copy-source objs/copy.txt \
  --metadata-directive REPLACE --content-type text/x-bin --metadata fresh=yes)"
check "  ... replaced metadata" '"fresh": "yes"' "$(aws_ s3api head-object --bucket objs --key copy.txt)"
check "self copy without REPLACE" "(InvalidRequest)" "$(aws_ s3api copy-object --bucket objs --key copy.txt --copy-source objs/copy.txt)"
check "copy missing source" "(NoSuchKey)" "$(aws_ s3api copy-object --bucket objs --key c2 --copy-source objs/missing)"
check "delete-bucket not empty" "(BucketNotEmpty)" "$(aws_ s3api delete-bucket --bucket objs)"
check "delete-object" "" "$(aws_ s3api delete-object --bucket objs --key copy.txt)"
check "  ... gone" "(404)" "$(aws_ s3api head-object --bucket objs --key copy.txt)"

# --- aws s3 cp: CRT and classic transfer clients --------------------------------------
check "s3 cp upload 7 MiB (CRT)" "upload:" "$(aws_ s3 cp seven.bin s3://objs/high/seven.bin)"
check "s3 cp download 7 MiB (CRT)" "download:" "$(aws_ s3 cp s3://objs/high/seven.bin seven-crt.down)"
check "  ... content matches" "SAME" "$(same seven.bin seven-crt.down)"
check "s3 cp upload 7 MiB (classic)" "upload:" "$(AWS_TRANSFER=classic aws_ s3 cp seven.bin s3://objs/high/seven-classic.bin)"
check "s3 cp download 7 MiB (classic)" "download:" "$(AWS_TRANSFER=classic aws_ s3 cp s3://objs/high/seven-classic.bin seven-classic.down)"
check "  ... content matches" "SAME" "$(same seven.bin seven-classic.down)"

# --- presigned URL -----------------------------------------------------------------
URL=$(aws_ s3 presign s3://objs/hello.txt --expires-in 300)
check "presigned GET with curl" "hello bin" "$(curl_ -s "$URL")"

# --- rclone --------------------------------------------------------------------------
check "rclone upload (single PUT)" "" "$(rclone_ copyto small.bin ref:objs/rclone/small.bin)"
check "rclone download" "" "$(rclone_ copyto ref:objs/rclone/small.bin rclone-small.down)"
check "  ... content matches" "SAME" "$(same small.bin rclone-small.down)"
check "rclone server-side copy" "" "$(rclone_ copyto ref:objs/rclone/small.bin ref:objs/rclone/copy.bin)"
rclone_ copyto ref:objs/rclone/copy.bin rclone-copy.down >/dev/null
check "  ... copy readable" "SAME" "$(same small.bin rclone-copy.down)"
check "rclone deletefile" "" "$(rclone_ deletefile ref:objs/rclone/copy.bin)"

finish
