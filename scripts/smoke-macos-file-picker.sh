#!/bin/sh
set -eu

if [ "$(uname -s)" != Darwin ]; then
  printf '%s\n' '略過：原生選檔 Smoke 僅適用 macOS。'
  exit 0
fi
task_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$task_root"
webview_module="$(go list -m -f '{{.Dir}}' github.com/webview/webview_go)"
smoke_dir="$(mktemp -d "${TMPDIR:-/tmp}/nr-intern-picker-smoke.XXXXXX")"
trap 'rm -rf -- "$smoke_dir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

clang -fblocks -c src/desktop/window/file_picker_darwin.m -o "$smoke_dir/picker.o"
clang++ -std=c++11 -DWEBVIEW_COCOA -DWEBVIEW_STATIC \
  -Wno-deprecated-literal-operator -Wno-auto-var-id \
  -I"$webview_module/libs/webview/include" \
  scripts/smoke-macos-file-picker.mm "$smoke_dir/picker.o" \
  -framework Cocoa -framework WebKit -o "$smoke_dir/smoke"
"$smoke_dir/smoke"
