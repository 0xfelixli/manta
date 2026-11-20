#!/usr/bin/env bash
set -euo pipefail

MANTA_BIN="${MANTA_BIN:-manta}"
PROXY_URL="${MANTA_PROXY_URL:-http://127.0.0.1:7890}"
TEST_URL="${MANTA_TEST_URL:-https://www.gstatic.com/generate_204}"

echo "=== manta status ==="
"$MANTA_BIN" status

echo ""
echo "=== proxy connectivity check ($PROXY_URL -> $TEST_URL) ==="
http_code=$(curl -x "$PROXY_URL" -sS --max-time 8 -o /dev/null -w "%{http_code}" "$TEST_URL")
if [[ "$http_code" == "204" || "$http_code" == "200" ]]; then
  echo "OK: HTTP $http_code"
  exit 0
else
  echo "FAIL: HTTP $http_code" >&2
  exit 1
fi
