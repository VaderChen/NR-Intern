#!/bin/zsh

set -euo pipefail

SCRIPT_DIR="${0:A:h}"
PROJECT_ROOT="$SCRIPT_DIR"
BUILD_SCRIPT="$PROJECT_ROOT/build.command"
DESKTOP_BINARY="$PROJECT_ROOT/bin/nr-intern-desktop"
DEFAULT_CONFIG="$PROJECT_ROOT/configs/ai-agent/config.example.json"
CONFIG_PATH="${NR_INTERN_CONFIG:-$DEFAULT_CONFIG}"

if [[ ! -f "$CONFIG_PATH" ]]; then
	print -u2 "錯誤：找不到設定檔：$CONFIG_PATH"
	print -u2 "可透過 NR_INTERN_CONFIG 指定其他設定檔。"
	exit 1
fi

if [[ ! -x "$BUILD_SCRIPT" ]]; then
	print -u2 "錯誤：build.command 不存在或無法執行。"
	exit 1
fi

"$BUILD_SCRIPT"

if [[ ! -x "$DESKTOP_BINARY" ]]; then
	print -u2 "錯誤：建置完成後仍找不到桌面程式：$DESKTOP_BINARY"
	exit 1
fi

cd "$PROJECT_ROOT"
print "啟動 NR-Intern 桌面 Console..."
print "設定檔：$CONFIG_PATH"
exec "$DESKTOP_BINARY" -config "$CONFIG_PATH" -open=false "$@"
