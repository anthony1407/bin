#!/usr/bin/env bash
# Versioning and CORS: aws s3api and rclone against a versioned bucket (puts,
# version reads, delete markers, permanent deletes, restore by copy, listings,
# suspension), CORS rules set with aws s3api and checked with real preflights
# from curl, and the console endpoints for both.
source "$(dirname "$0")/lib.sh"
# aws_field <name> reads the first "name": "value" from aws-cli's indented JSON.
aws_field() { sed -n 's/.*"'"$1"'": *"\([^"]*\)".*/\1/p' | head -n 1; }
start_server
printf 'version one\n' >"$WORK/v1.txt"
printf 'version two\n' >"$WORK/v2.txt"
aws_ s3 mb s3://verb >/dev/null

# --- versioning with aws s3api ----------------------------------------------------
check "versioning starts unset" "" "$(aws_ s3api get-bucket-versioning --bucket verb)"
check "put-bucket-versioning Enabled" "" "$(aws_ s3api put-bucket-versioning --bucket verb --versioning-configuration Status=Enabled)"
check "  ... get-bucket-versioning" '"Status": "Enabled"' "$(aws_ s3api get-bucket-versioning --bucket verb)"
out1=$(aws_ s3api put-object --bucket verb --key doc.txt --body v1.txt)
check "put returns a version id" '"VersionId"' "$out1"
V1=$(printf '%s' "$out1" | aws_field VersionId)
V2=$(aws_ s3api put-object --bucket verb --key doc.txt --body v2.txt | aws_field VersionId)
check "  ... a different one each time" "different" "$([ -n "$V1" ] && [ "$V1" != "$V2" ] && echo different)"
check "get latest" '"VersionId": "'"$V2"'"' "$(aws_ s3api get-object --bucket verb --key doc.txt latest.down)"
check "  ... content" "SAME" "$(same v2.txt latest.down)"
check "get-object --version-id (old)" '"VersionId": "'"$V1"'"' "$(aws_ s3api get-object --bucket verb --key doc.txt --version-id "$V1" old.down)"
check "  ... content" "SAME" "$(same v1.txt old.down)"
check "head-object --version-id" '"ContentLength": 12' "$(aws_ s3api head-object --bucket verb --key doc.txt --version-id "$V1")"
check "unknown version" "(NoSuchVersion)" \
  "$(aws_ s3api get-object --bucket verb --key doc.txt --version-id 00000000-0000-4000-8000-000000000000 x.down)"
check "malformed version" "(InvalidArgument)" "$(aws_ s3api get-object --bucket verb --key doc.txt --version-id nope x.down)"

del=$(aws_ s3api delete-object --bucket verb --key doc.txt)
check "delete creates a delete marker" '"DeleteMarker": true' "$del"
MARKER=$(printf '%s' "$del" | aws_field VersionId)
check "  ... the key is gone" "(NoSuchKey)" "$(aws_ s3api get-object --bucket verb --key doc.txt x.down)"
check "  ... s3 ls no longer shows it" "" "$(aws_ s3 ls s3://verb/)"
versions=$(aws_ s3api list-object-versions --bucket verb)
check "list-object-versions: the marker is latest" '"VersionId": "'"$MARKER"'"' "$versions"
check "  ... both versions are kept" "$V1" "$versions"
check "  ... ... and $V2" "$V2" "$versions"
check "paged listing (max-items 1) ends" '"VersionId"' \
  "$(aws_ s3api list-object-versions --bucket verb --max-items 1 --page-size 1)"

check "remove the marker: delete-object --version-id" '"DeleteMarker": true' \
  "$(aws_ s3api delete-object --bucket verb --key doc.txt --version-id "$MARKER")"
check "  ... the object is back" "SAME" "$(aws_ s3api get-object --bucket verb --key doc.txt back.down >/dev/null; same v2.txt back.down)"
check "restore v1 by copy-object" '"CopySourceVersionId": "'"$V1"'"' \
  "$(aws_ s3api copy-object --bucket verb --key doc.txt --copy-source "verb/doc.txt?versionId=$V1")"
check "  ... v1 content is current" "SAME" "$(aws_ s3api get-object --bucket verb --key doc.txt r.down >/dev/null; same v1.txt r.down)"
check "delete one version for good" '"VersionId": "'"$V2"'"' "$(aws_ s3api delete-object --bucket verb --key doc.txt --version-id "$V2")"
check "  ... gone from the listing" "absent" \
  "$(aws_ s3api list-object-versions --bucket verb | grep -q "$V2" && echo present || echo absent)"
check "delete-objects with a version id" '"VersionId": "'"$V1"'"' \
  "$(aws_ s3api delete-objects --bucket verb --delete "Objects=[{Key=doc.txt,VersionId=$V1}]")"
check "versions keep the bucket" "(BucketNotEmpty)" "$(aws_ s3api delete-bucket --bucket verb)"

# --- versioning with rclone -------------------------------------------------------
rclone_ copyto "/work/v1.txt" ref:verb/r.txt >/dev/null
# Same size and, often, the same second: without --ignore-times rclone may
# take v2 for unchanged and skip the upload.
rclone_ copyto --ignore-times "/work/v2.txt" ref:verb/r.txt >/dev/null
check "rclone lsf shows the current object" "r.txt" "$(rclone_ lsf ref:verb)"
check "rclone --s3-versions shows the old version" "r-v" "$(rclone_ lsf --s3-versions ref:verb)"
check "rclone cat reads the latest" "version two" "$(rclone_ cat ref:verb/r.txt)"
rclone_ backend cleanup-hidden ref:verb >/dev/null
check "rclone backend cleanup-hidden removes old versions" "only current" \
  "$(rclone_ lsf --s3-versions ref:verb | grep -q 'r-v' && echo old-left || echo 'only current')"

# --- suspended ----------------------------------------------------------------------
check "put-bucket-versioning Suspended" "" "$(aws_ s3api put-bucket-versioning --bucket verb --versioning-configuration Status=Suspended)"
aws_ s3api put-object --bucket verb --key s.txt --body v1.txt >/dev/null
check "suspended puts are the null version" '"VersionId": "null"' "$(aws_ s3api head-object --bucket verb --key s.txt --version-id null)"
aws_ s3api put-object --bucket verb --key s.txt --body v2.txt >/dev/null
check "  ... and replace each other" "1" \
  "$(aws_ s3api list-object-versions --bucket verb --prefix s.txt --query 'length(Versions)' --output text)"

# --- console ------------------------------------------------------------------------
console_login
check "console: versioning shown on the bucket" '"versioning":"Suspended"' "$(api GET /buckets/verb)"
check "console: enable" '"versioning":"Enabled"' "$(api PUT /buckets/verb/versioning '{"status":"Enabled"}')"
check "console: version history" '"isLatest":true' "$(api GET '/buckets/verb/versions?key=s.txt')"
check "console: force delete removes every version" "status=204" "$(api DELETE '/buckets/verb?force=true')"

# --- CORS -------------------------------------------------------------------------------
aws_ s3 mb s3://web >/dev/null
check "get-bucket-cors without rules" "(NoSuchCORSConfiguration)" "$(aws_ s3api get-bucket-cors --bucket web)"
preflight() { # origin method [request headers]
  curl_ -s -i -X OPTIONS -H "Origin: $1" -H "Access-Control-Request-Method: $2" ${3:+-H "Access-Control-Request-Headers: $3"} \
    "http://$HOST:$S3_PORT/web/upload.bin"
}
check "preflight before any rule" "403" "$(preflight https://app.example.com PUT | head -n 1)"
cat >"$WORK/cors.json" <<'EOF'
{"CORSRules": [
  {"ID": "app", "AllowedOrigins": ["https://app.example.com"], "AllowedMethods": ["GET", "PUT"],
   "AllowedHeaders": ["*"], "ExposeHeaders": ["ETag"], "MaxAgeSeconds": 600},
  {"AllowedOrigins": ["*"], "AllowedMethods": ["GET"]}
]}
EOF
check "put-bucket-cors" "" "$(aws_ s3api put-bucket-cors --bucket web --cors-configuration file:///work/cors.json)"
check "get-bucket-cors" '"MaxAgeSeconds": 600' "$(aws_ s3api get-bucket-cors --bucket web)"
pf=$(preflight https://app.example.com PUT 'content-type,x-amz-date')
check "preflight allowed" "200" "$(printf '%s' "$pf" | head -n 1)"
check "  ... allow origin" "access-control-allow-origin: https://app.example.com" "$(printf '%s' "$pf" | tr 'A-Z' 'a-z')"
check "  ... allow methods" "access-control-allow-methods: get, put" "$(printf '%s' "$pf" | tr 'A-Z' 'a-z')"
check "  ... max age" "access-control-max-age: 600" "$(printf '%s' "$pf" | tr 'A-Z' 'a-z')"
check "preflight from another site, PUT" "403" "$(preflight https://evil.example PUT | head -n 1)"
check "preflight from another site, GET (wildcard rule)" "access-control-allow-origin: *" \
  "$(preflight https://evil.example GET | tr 'A-Z' 'a-z')"
check "actual request carries the headers" "access-control-expose-headers: etag" \
  "$(curl_ -s -i -H 'Origin: https://app.example.com' "http://$HOST:$S3_PORT/web/nothing" | tr 'A-Z' 'a-z')"
check "invalid rule refused" "(InvalidRequest)" \
  "$(aws_ s3api put-bucket-cors --bucket web --cors-configuration '{"CORSRules":[{"AllowedOrigins":["*"],"AllowedMethods":["PATCH"]}]}')"
check "console: rules on the bucket" '"allowedOrigins":["https://app.example.com"]' "$(api GET /buckets/web)"
check "console: clear rules" '"cors":[]' "$(api PUT /buckets/web/cors '{"rules":[]}')"
check "  ... preflight refused again" "403" "$(preflight https://app.example.com GET | head -n 1)"
check "console: invalid rule" "invalid_cors" "$(api PUT /buckets/web/cors '{"rules":[{"allowedOrigins":["*"],"allowedMethods":["PATCH"]}]}')"
aws_ s3api put-bucket-cors --bucket web --cors-configuration file:///work/cors.json >/dev/null
check "delete-bucket-cors" "(NoSuchCORSConfiguration)" \
  "$(aws_ s3api delete-bucket-cors --bucket web; aws_ s3api get-bucket-cors --bucket web)"

finish
