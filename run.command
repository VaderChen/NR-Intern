#!/bin/zsh

set -euo pipefail

SCRIPT_DIR="${0:A:h}"
PROJECT_ROOT="$SCRIPT_DIR"
BUILD_SCRIPT="$PROJECT_ROOT/build.command"
DESKTOP_BINARY="$PROJECT_ROOT/bin/nr-intern-desktop"
DIST_OUTPUT_DIR="$PROJECT_ROOT/dist"
DEFAULT_CONFIG="$PROJECT_ROOT/configs/ai-agent/config.example.json"
CONFIG_PATH="${NR_INTERN_CONFIG:-$DEFAULT_CONFIG}"
LOG_DIR="$PROJECT_ROOT/data/ai-agent/logs"
DESKTOP_LOG="$LOG_DIR/desktop.log"
RUN_LOG="$LOG_DIR/run.log"
STARTUP_SPLASH_SCRIPT="$PROJECT_ROOT/scripts/macos-startup-splash.js"
STARTUP_SPLASH_ICON="$PROJECT_ROOT/assets/app-icon.png"
STARTUP_SIGNAL=""
STARTUP_SIGNAL_HANDED_OFF=0
STARTUP_VERSION=""

cleanup_startup_signal() {
	if [[ "$STARTUP_SIGNAL_HANDED_OFF" != "1" && -n "$STARTUP_SIGNAL" && -e "$STARTUP_SIGNAL" ]]; then
		/bin/rm -- "$STARTUP_SIGNAL"
	fi
}

resolve_startup_app_name() {
	local service_name data_dir persisted_settings persisted_name
	service_name="$(/usr/bin/plutil -extract service_name raw -o - "$CONFIG_PATH" 2>/dev/null || true)"
	data_dir="$(/usr/bin/plutil -extract data_dir raw -o - "$CONFIG_PATH" 2>/dev/null || true)"
	if [[ -n "${AI_AGENT_DATA_DIR:-}" ]]; then
		data_dir="$AI_AGENT_DATA_DIR"
	fi
	if [[ -n "$data_dir" ]]; then
		if [[ "$data_dir" != /* ]]; then
			data_dir="$PROJECT_ROOT/$data_dir"
		fi
		persisted_settings="$data_dir/service-settings.json"
		if [[ -f "$persisted_settings" ]]; then
			persisted_name="$(/usr/bin/plutil -extract service_name raw -o - "$persisted_settings" 2>/dev/null || true)"
			if [[ -n "$persisted_name" ]]; then
				service_name="$persisted_name"
			fi
		fi
	fi
	if [[ "$service_name" == "聰明的實習生" ]]; then
		service_name="永不休息的實習生"
	fi
	if [[ -n "${AI_AGENT_SERVICE_NAME:-}" ]]; then
		service_name="$AI_AGENT_SERVICE_NAME"
	fi
	service_name="${service_name//$'\r'/ }"
	service_name="${service_name//$'\n'/ }"
	if [[ -z "${service_name//[[:space:]]/}" ]]; then
		service_name="永不休息的實習生"
	fi
	print -r -- "$service_name"
}

resolve_startup_version() {
	if [[ -n "${NR_INTERN_VERSION:-}" ]]; then
		print -r -- "$NR_INTERN_VERSION"
		return
	fi
	TZ=Asia/Taipei date '+1.%y.%m%d build %H%M'
}

prepare_macos_codesign_identity() {
	local identity
	if [[ "$(uname -s)" != "Darwin" || -n "${NR_INTERN_CODESIGN_IDENTITY+x}" ]]; then
		return 0
	fi
	if [[ ! -x /usr/bin/security ]]; then
		return 0
	fi
	identity="$(/usr/bin/security find-identity -v -p codesigning 2>/dev/null |
		/usr/bin/sed -n 's/^[^"]*"\(Developer ID Application:[^"]*\)".*$/\1/p' |
		/usr/bin/head -n 1)"
	if [[ -n "$identity" ]]; then
		export NR_INTERN_CODESIGN_IDENTITY="$identity"
	fi
}

start_startup_splash() {
	local startup_app_name
	if [[ "$(uname -s)" != "Darwin" || ! -f "$STARTUP_SPLASH_SCRIPT" || ! -f "$STARTUP_SPLASH_ICON" ]]; then
		return 0
	fi
	startup_app_name="$(resolve_startup_app_name)"
	STARTUP_SIGNAL="$(mktemp "${TMPDIR:-/tmp}/nr-intern-startup.XXXXXX")"
	/usr/bin/osascript -l JavaScript "$STARTUP_SPLASH_SCRIPT" "$STARTUP_SIGNAL" "$STARTUP_SPLASH_ICON" "$startup_app_name" "$STARTUP_VERSION" \
		</dev/null >>"$RUN_LOG" 2>&1 &
	STARTUP_SPLASH_PID=$!
	disown "$STARTUP_SPLASH_PID" 2>/dev/null || true
}

trap cleanup_startup_signal EXIT INT TERM

close_launch_terminal() {
	local launch_tty
	launch_tty="$(tty 2>/dev/null || true)"
	[[ "$launch_tty" == /dev/* ]] || return 0
	(
		sleep 0.2
		/usr/bin/osascript \
			-e 'on run argv' \
			-e 'set targetTTY to item 1 of argv' \
			-e 'tell application "Terminal"' \
			-e 'repeat with terminalWindow in windows' \
			-e 'repeat with terminalTab in tabs of terminalWindow' \
			-e 'if tty of terminalTab is targetTTY then' \
			-e 'close terminalWindow' \
			-e 'return' \
			-e 'end if' \
			-e 'end repeat' \
			-e 'end repeat' \
			-e 'end tell' \
			-e 'end run' \
			"$launch_tty"
	) >/dev/null 2>&1 &!
}

if [[ ! -f "$CONFIG_PATH" ]]; then
	print -u2 "錯誤：找不到設定檔：$CONFIG_PATH"
	print -u2 "可透過 NR_INTERN_CONFIG 指定其他設定檔。"
	exit 1
fi

if [[ ! -x "$BUILD_SCRIPT" ]]; then
	print -u2 "錯誤：build.command 不存在或無法執行。"
	exit 1
fi

# Finder 啟動的背景子程序必須沿用相同 Developer ID；否則每次重建的
# ad-hoc CDHash 都不同，macOS 會把螢幕錄製授權視為另一個 App。
prepare_macos_codesign_identity

# Finder 以 Terminal 執行 .command；先把完整建置與 UI 啟動流程轉到背景，
# 再關閉承載本腳本的 Terminal，避免建置期間一直顯示 Shell 視窗。
if [[ "${NR_INTERN_KEEP_TERMINAL:-0}" != "1" && "${NR_INTERN_DETACHED_LAUNCH:-0}" != "1" && "${TERM_PROGRAM:-}" == "Apple_Terminal" ]]; then
	mkdir -p "$LOG_DIR"
	nohup /usr/bin/env NR_INTERN_DETACHED_LAUNCH=1 "$PROJECT_ROOT/run.command" "$@" </dev/null >>"$RUN_LOG" 2>&1 &
	DETACHED_PID=$!
	disown "$DETACHED_PID" 2>/dev/null || true
	if ! kill -0 "$DETACHED_PID" 2>/dev/null; then
		print -u2 "錯誤：無法啟動背景建置流程，請查看：$RUN_LOG"
		exit 1
	fi
	close_launch_terminal
	exit 0
fi

mkdir -p "$LOG_DIR"
STARTUP_VERSION="$(resolve_startup_version)"
export NR_INTERN_VERSION="$STARTUP_VERSION"
start_startup_splash

"$BUILD_SCRIPT"

if [[ ! -x "$DESKTOP_BINARY" ]]; then
	print -u2 "錯誤：建置完成後仍找不到桌面程式：$DESKTOP_BINARY"
	exit 1
fi

cd "$PROJECT_ROOT"
print "啟動 NR-Intern 桌面 Console..."
print "設定檔：$CONFIG_PATH"
mkdir -p "$LOG_DIR"
typeset -a startup_signal_arguments
startup_signal_arguments=()
if [[ -n "$STARTUP_SIGNAL" ]]; then
	startup_signal_arguments=(-startup-signal "$STARTUP_SIGNAL")
fi

# macOS 交給 LaunchServices 啟動 App Bundle，保留正確的 lifecycle、圖示與前景
# 啟用；前置啟動畫面會覆蓋 LaunchServices 準備主程式期間的空白時間。
typeset -a mac_apps
mac_apps=("$DIST_OUTPUT_DIR"/*/macos-arm64/NR-Intern.app(N/om))
if (( ${#mac_apps[@]} > 0 )); then
	# 舊版 App 執行中時，build.command 會保留舊目錄；必須使用排序後
	# 最新的 Bundle，否則重啟仍會載入舊版而看不到修正。
	MAC_APP="${mac_apps[-1]}"
	if /usr/bin/open -n -F -i /dev/null -o "$DESKTOP_LOG" --stderr "$DESKTOP_LOG" "$MAC_APP" --args \
		-config "$CONFIG_PATH" -working-dir "$PROJECT_ROOT" -open=false "$@" "${startup_signal_arguments[@]}"; then
		STARTUP_SIGNAL_HANDED_OFF=1
		print "NR-Intern App 啟動中。"
		exit 0
	fi
	print -u2 "警告：App Bundle 啟動失敗，改用桌面執行檔。"
fi

nohup "$DESKTOP_BINARY" -config "$CONFIG_PATH" -open=false "$@" \
	"${startup_signal_arguments[@]}" </dev/null >>"$DESKTOP_LOG" 2>&1 &
DESKTOP_PID=$!
disown "$DESKTOP_PID" 2>/dev/null || true

if ! kill -0 "$DESKTOP_PID" 2>/dev/null; then
	print -u2 "錯誤：桌面程式啟動失敗，請查看：$DESKTOP_LOG"
	exit 1
fi

STARTUP_SIGNAL_HANDED_OFF=1
print "NR-Intern 已啟動（PID $DESKTOP_PID）。"
