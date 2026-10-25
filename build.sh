#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"
# Restore the pinned public graphics runtime and compile Go helpers if missing.
# These steps operate exclusively on generated, gitignored directories.
./toolchain/bootstrap.sh
exec go run ./cmd/mary-skelter-builder "$@"
