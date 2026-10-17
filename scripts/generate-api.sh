#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
output=$(realpath -m "${1:-.}")
mkdir -p "$output/backend/internal/generated/api" "$output/web/src/api"
ogen --target "$output/backend/internal/generated/api" --package api --clean api/openapi.yaml
pnpm --dir web exec openapi-typescript ../api/openapi.yaml --output "$output/web/src/api/schema.d.ts"
