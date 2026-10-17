#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
bash scripts/generate-api.sh "$tmp"
diff -ru backend/internal/generated/api "$tmp/backend/internal/generated/api"
diff -u web/src/api/schema.d.ts "$tmp/web/src/api/schema.d.ts"
