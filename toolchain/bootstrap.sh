#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
export GOWORK=off GOPROXY=off GOSUMDB=off CGO_ENABLED=0
case "$(uname -s):$(uname -m)" in
  Linux:x86_64) PLATFORM=linux-amd64; HELPER=bcn-tid-go ;;
  *) echo 'This shell launcher supports Linux x86_64; use build.bat on Windows.' >&2; exit 2 ;;
esac
# If the runtime is installed but not bundled, keep using it. If missing,
# restore ONLY from committed pinned bundles; no pip, network or system Python.
go run ./cmd/mary-skelter-deps restore --platform "$PLATFORM"
# Go helper's third-party source modules are restored from a Git-tracked ZIP.
# This does not access network, Go proxy, pip or the host Python.
go run ./cmd/mary-skelter-deps restore-go
BIN="$ROOT/toolchain/bin/$PLATFORM/$HELPER"
if [[ ! -f "$BIN" ]]; then
  echo '[bootstrap] Compiling native Go graphics helper from checked-in source...'
  mkdir -p "$(dirname "$BIN")"
  (cd tools/bcn-tid-go && GOTOOLCHAIN=local go build -trimpath -buildvcs=false -o "$BIN" .)
fi
QA="$ROOT/toolchain/bin/$PLATFORM/mary-skelter-runtime-qa"
if [[ ! -f "$QA" ]]; then
  echo '[bootstrap] Compiling runtime QA helper from checked-in Go source...'
  go build -trimpath -buildvcs=false -o "$QA" ./cmd/mary-skelter-runtime-qa
fi
