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
	go build -trimpath -o "$BUILD_STAGE/$output_name" "./src/cmd/$command_name"
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
go run ./src/cmd/release "${release_arguments[@]}"

print "跨平台建置完成：$DIST_OUTPUT_DIR"
