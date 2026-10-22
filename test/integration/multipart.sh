#!/usr/bin/env bash
# Multipart: aws s3 cp of large files with both transfer clients (CRT sends
# aws-chunked parts with a CRC64NVME trailer and expects a full-object
# checksum), the low-level s3api flow, DeleteObjects, rclone multipart uploads
# and server-side multipart copies; then checks that no blob leaked.
source "$(dirname "$0")/lib.sh"
VOL=bin-test-multipart
docker volume rm -f "$VOL" >/dev/null 2>&1
start_server -v "$VOL:/data"
trap 'stop_server; docker volume rm -f "$VOL" >/dev/null 2>&1' EXIT
blob_files() { docker run --rm -v "$VOL:/data" "$CURL_IMAGE" sh -c 'find /data/blobs -type f | wc -l' 2>/dev/null | tr -d ' '; }

random_file big.bin $((40 * 1024 * 1024))
random_file part1.bin $((5 * 1024 * 1024))
random_file part2.bin $((1024 * 1024))
aws_ s3 mb s3://mpu >/dev/null

# --- aws s3 cp ---------------------------------------------------------------------
check "s3 cp 40 MiB upload (CRT, multipart)" "upload:" "$(aws_ s3 cp big.bin s3://mpu/big.bin)"
check "  ... stored as 5 parts" '-5\"' "$(aws_ s3api head-object --bucket mpu --key big.bin)"
check "  ... full-object CRC64NVME" '"ChecksumType": "FULL_OBJECT"' "$(aws_ s3api head-object --bucket mpu --key big.bin --checksum-mode ENABLED)"
check "s3 cp 40 MiB download (CRT, ranged)" "download:" "$(aws_ s3 cp s3://mpu/big.bin big-crt.down)"
check "  ... content matches" "SAME" "$(same big.bin big-crt.down)"
check "s3 cp 40 MiB upload (classic)" "upload:" "$(AWS_TRANSFER=classic aws_ s3 cp big.bin s3://mpu/big-classic.bin)"
check "s3 cp 40 MiB download (classic)" "download:" "$(AWS_TRANSFER=classic aws_ s3 cp s3://mpu/big-classic.bin big-classic.down)"
check "  ... content matches" "SAME" "$(same big.bin big-classic.down)"
check "get-object --checksum-mode validates the full-object CRC" '"ContentLength": 41943040' \
  "$(aws_ s3api get-object --bucket mpu --key big.bin --checksum-mode ENABLED big-checked.down)"
check "get-object --part-number 2" '"PartsCount": 5' "$(aws_ s3api get-object --bucket mpu --key big.bin --part-number 2 part2.down)"
check "  ... is bytes 8-16 MiB" "SAME" "$(dd if="$WORK/big.bin" of="$WORK/part2.expected" bs=1M skip=8 count=8 2>/dev/null; same part2.expected part2.down)"

# --- low-level s3api flow ------------------------------------------------------------
UPLOAD=$(aws_ s3api create-multipart-upload --bucket mpu --key manual.bin --query UploadId --output text)
check "create-multipart-upload" "" "$UPLOAD"
E1=$(aws_ s3api upload-part --bucket mpu --key manual.bin --upload-id "$UPLOAD" --part-number 1 --body part1.bin --query ETag --output text)
E2=$(aws_ s3api upload-part --bucket mpu --key manual.bin --upload-id "$UPLOAD" --part-number 2 --body part2.bin --query ETag --output text)
check "list-parts" '"PartNumber": 2' "$(aws_ s3api list-parts --bucket mpu --key manual.bin --upload-id "$UPLOAD")"
check "list-multipart-uploads" "$UPLOAD" "$(aws_ s3api list-multipart-uploads --bucket mpu)"
printf '{"Parts":[{"PartNumber":1,"ETag":%s},{"PartNumber":2,"ETag":%s}]}' \
  "$(printf '%s' "$E1" | sed 's/"/\\"/g; s/.*/"&"/')" "$(printf '%s' "$E2" | sed 's/"/\\"/g; s/.*/"&"/')" >"$WORK/parts.json"
EXPECTED=$( (printf '%s' "$E1"; printf '%s' "$E2") | tr -d '"' | xxd -r -p | md5_of /dev/stdin)
check "complete-multipart-upload ETag = md5(md5s)-2" "$EXPECTED-2" \
  "$(aws_ s3api complete-multipart-upload --bucket mpu --key manual.bin --upload-id "$UPLOAD" --multipart-upload file://parts.json)"
check "upload is gone after completion" "(NoSuchUpload)" \
  "$(aws_ s3api abort-multipart-upload --bucket mpu --key manual.bin --upload-id "$UPLOAD")"
ABORTED=$(aws_ s3api create-multipart-upload --bucket mpu --key aborted.bin --query UploadId --output text)
aws_ s3api upload-part --bucket mpu --key aborted.bin --upload-id "$ABORTED" --part-number 1 --body part2.bin >/dev/null
check "abort-multipart-upload" "" "$(aws_ s3api abort-multipart-upload --bucket mpu --key aborted.bin --upload-id "$ABORTED")"
check "  ... no uploads left" "" "$(aws_ s3api list-multipart-uploads --bucket mpu --query Uploads --output text | grep -v None)"

# --- DeleteObjects -------------------------------------------------------------------
aws_ s3api put-object --bucket mpu --key del/a --body part2.bin >/dev/null
aws_ s3api put-object --bucket mpu --key del/b --body part2.bin >/dev/null
check "delete-objects" '"Key": "del/b"' "$(aws_ s3api delete-objects --bucket mpu --delete '{"Objects":[{"Key":"del/a"},{"Key":"del/b"},{"Key":"del/missing"}]}')"
check "  ... deleted" "None" "$(aws_ s3api list-objects-v2 --bucket mpu --prefix del/ --query Contents --output text)"

# --- rclone --------------------------------------------------------------------------------
check "rclone multipart upload (5 MiB chunks)" "" "$(rclone_ copyto big.bin ref:mpu/rclone/big.bin --s3-upload-cutoff 5M --s3-chunk-size 5M)"
check "  ... stored as 8 parts" '-8\"' "$(aws_ s3api head-object --bucket mpu --key rclone/big.bin)"
check "rclone download" "" "$(rclone_ copyto ref:mpu/rclone/big.bin rclone-big.down)"
check "  ... content matches" "SAME" "$(same big.bin rclone-big.down)"
check "rclone md5sum of a multipart object" "$(md5_of "$WORK/big.bin")" "$(rclone_ md5sum ref:mpu/rclone/big.bin)"
check "rclone server-side multipart copy (UploadPartCopy)" "" \
  "$(rclone_ copyto ref:mpu/rclone/big.bin ref:mpu/rclone/copy.bin --s3-copy-cutoff 5M)"
check "  ... copy stored as parts" '-8\"' "$(aws_ s3api head-object --bucket mpu --key rclone/copy.bin)"
rclone_ copyto ref:mpu/rclone/copy.bin rclone-copy.down >/dev/null
check "  ... copy content matches" "SAME" "$(same big.bin rclone-copy.down)"

# --- no leaked blobs -------------------------------------------------------------------
rclone_ purge ref:mpu >/dev/null
check "bucket purged" "(404)" "$(aws_ s3api head-bucket --bucket mpu)"
check "no blob left on disk" "0" "$(blob_files)"

finish
