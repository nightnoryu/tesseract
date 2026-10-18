#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
output=$(realpath -m "${1:-.}")
target=${2:-all}
case "$target" in
  all|backend|web) ;;
  *) printf 'Unknown API generation target: %s\n' "$target" >&2; exit 1 ;;
esac
if [[ "$target" == all || "$target" == backend ]]; then
  mkdir -p "$output/backend/internal/generated/api"
  ogen --target "$output/backend/internal/generated/api" --package api --clean api/openapi.yaml
fi
if [[ "$target" == all || "$target" == web ]]; then
  mkdir -p "$output/web/src/api"
  pnpm --dir web exec openapi-typescript ../api/openapi.yaml --output "$output/web/src/api/schema.d.ts"
fi
