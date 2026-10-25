#!/usr/bin/env bash
# Runs every compatibility script against $BIN_IMAGE and fails if any fails.
# The crash tests are separate (crash.sh) because they need Go on the host.
set -uo pipefail
DIR=$(dirname "$0")
failed=()
for script in cli sigv4 buckets objects list multipart admin import dropzone identity; do
  echo "=== $script"
  WORK=$(mktemp -d) bash "$DIR/$script.sh" || failed+=("$script")
done
if [ ${#failed[@]} -gt 0 ]; then
  echo "FAILED: ${failed[*]}"
  exit 1
fi
echo "all integration scripts passed"
