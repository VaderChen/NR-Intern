#!/bin/zsh

set -euo pipefail

SCRIPT_DIR="${0:A:h}"
PROJECT_ROOT="$SCRIPT_DIR"
COMMAND_ROOT="$PROJECT_ROOT/src/cmd"
NATIVE_OUTPUT_DIR="$PROJECT_ROOT/bin"
DIST_OUTPUT_DIR="$PROJECT_ROOT/dist"
RELEASE_TARGETS="${NR_INTERN_BUILD_TARGETS:-windows/amd64,windows/arm64,darwin/arm64}"
RELEASE_VERSION="${NR_INTERN_VERSION:-}"
MAC_ICON_PATH="${NR_INTERN_MAC_ICON_PATH:-assets/app-icon.icns}"
WINDOWS_ICON_PATH="${NR_INTERN_WINDOWS_ICON_PATH:-assets/app-icon.ico}"
# run.command 會沿用本檔建立本機執行檔，因此開發機未安裝 WiX 時仍須可啟動。
# 正式發行可設為 required；有 wix 時 optional 仍會正常產生兩種 MSI。
MSI_MODE="${NR_INTERN_MSI_MODE:-optional}"
BUILD_STAGE=""

cleanup_stage() {
	if [[ -n "$BUILD_STAGE" && -d "$BUILD_STAGE" ]]; then
		/bin/rm -rf -- "$BUILD_STAGE"
	fi
}
trap cleanup_stage EXIT INT TERM

dist_output_in_use() {
	local candidate="$1"
	local running_command
	if [[ -x /usr/sbin/lsof ]] && /usr/sbin/lsof -nP +D "$candidate" 2>/dev/null |
		/usr/bin/awk 'NR > 1 { found = 1 } END { exit !found }'; then
		return 0
	fi
	while IFS= read -r running_command; do
		if [[ "$running_command" == "$candidate/"* ]]; then
			return 0
		fi
	done < <(/bin/ps -axo command=)
	return 1
}

reset_dist_output() {
	local expected_dist="$PROJECT_ROOT/dist"
	local output_path
	if [[ -z "$DIST_OUTPUT_DIR" || "$DIST_OUTPUT_DIR" != "$expected_dist" || "$DIST_OUTPUT_DIR" == "$PROJECT_ROOT" || "$DIST_OUTPUT_DIR" == "/" ]]; then
		print -u2 "錯誤：拒絕清理非預期的 dist 目錄：$DIST_OUTPUT_DIR"
		exit 1
	fi
	if [[ -L "$DIST_OUTPUT_DIR" ]]; then
		print -u2 "錯誤：dist 不可為符號連結：$DIST_OUTPUT_DIR"
		exit 1
	fi
	if [[ -e "$DIST_OUTPUT_DIR" && ! -d "$DIST_OUTPUT_DIR" ]]; then
		print -u2 "錯誤：dist 路徑存在但不是目錄：$DIST_OUTPUT_DIR"
		exit 1
	fi
	mkdir -p "$DIST_OUTPUT_DIR"
	for output_path in "$DIST_OUTPUT_DIR"/*(DN); do
		if dist_output_in_use "$output_path"; then
			print "保留執行中的發行目錄：$output_path"
			continue
		fi
		/bin/rm -rf -- "$output_path"
	done
	print "已清理發行輸出目錄：$DIST_OUTPUT_DIR"
}

if ! command -v go >/dev/null 2>&1; then
	print -u2 "錯誤：找不到 Go，請先安裝 Go 並確認 go 指令位於 PATH。"
	exit 1
fi

if [[ ! -f "$PROJECT_ROOT/go.mod" || ! -d "$COMMAND_ROOT" ]]; then
	print -u2 "錯誤：$PROJECT_ROOT 不是完整的 NR-Intern 專案目錄。"
	exit 1
fi

if [[ -z "$RELEASE_VERSION" ]]; then
	RELEASE_VERSION="$(TZ=Asia/Taipei date '+1.%y.%m%d build %H%M')"
fi

reset_dist_output
BUILD_STAGE="$(mktemp -d "${TMPDIR:-/tmp}/nr-intern-build.XXXXXX")"
typeset -a built_files
built_files=()

cd "$PROJECT_ROOT"
print "開始建置 NR-Intern 本機執行檔..."

for command_dir in "$COMMAND_ROOT"/*; do
	[[ -d "$command_dir" ]] || continue

	command_name="${command_dir:t}"
	output_name="nr-intern-$command_name"
	print "  建置 $command_name"
	# 專案可能位於另一套 VCS 的工作副本中（例如 Git 專案放在 SVN 目錄下）。
	# 發行版本由本腳本注入，不依賴 Go 自動寫入 VCS 資訊。
	go build -buildvcs=false -trimpath -o "$BUILD_STAGE/$output_name" "./src/cmd/$command_name"
	built_files+=("$output_name")
done

if (( ${#built_files[@]} == 0 )); then
	print -u2 "錯誤：$COMMAND_ROOT 中沒有可建置的命令。"
	exit 1
fi

mkdir -p "$NATIVE_OUTPUT_DIR"
for output_name in "${built_files[@]}"; do
	/bin/mv -f -- "$BUILD_STAGE/$output_name" "$NATIVE_OUTPUT_DIR/$output_name"
	/bin/chmod 755 "$NATIVE_OUTPUT_DIR/$output_name"
done

print "本機執行檔建置完成："
for output_name in "${built_files[@]}"; do
	print "  $NATIVE_OUTPUT_DIR/$output_name"
done

typeset -a release_arguments
release_arguments=(
	-output "$DIST_OUTPUT_DIR"
	-targets "$RELEASE_TARGETS"
	-version "$RELEASE_VERSION"
	-msi "$MSI_MODE"
	-mac-icon "$MAC_ICON_PATH"
	-windows-icon "$WINDOWS_ICON_PATH"
)

print "開始建置跨平台發行檔：$RELEASE_TARGETS"
print "發行版本：$RELEASE_VERSION"
go run -buildvcs=false ./src/cmd/release "${release_arguments[@]}"

print "跨平台建置完成：$DIST_OUTPUT_DIR"
