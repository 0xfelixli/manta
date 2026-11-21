#!/usr/bin/env bash
set -euo pipefail

PORT="${MANTA_PORT:-7890}"
echo "=== manta status ==="
manta status --ip --json || true

echo "=== proxy connectivity test ==="
curl -x "http://127.0.0.1:${PORT}" -sS --max-time 8 -o /dev/null \
  -w "HTTP %{http_code} in %{time_total}s\n" \
  https://www.gstatic.com/generate_204
