#!/bin/zsh

set -euo pipefail

SOURCE_PNG=""
PNG_OUTPUT=""
ICNS_OUTPUT=""
ICO_OUTPUT=""
WEB_OUTPUT=""
WINDOWS_AMD64_OUTPUT=""
WINDOWS_ARM64_OUTPUT=""
FFMPEG_TOOL=""
ICONUTIL_TOOL=""
LLVM_RC_TOOL=""
LLVM_CVTRES_TOOL=""
BUILD_STAGE=""
RESOURCE_SOURCE=""

cleanup_stage() {
	if [[ -n "$RESOURCE_SOURCE" && -f "$RESOURCE_SOURCE" ]]; then
		rm -f -- "$RESOURCE_SOURCE"
	fi
	if [[ -n "$BUILD_STAGE" && -d "$BUILD_STAGE" ]]; then
		rm -rf -- "$BUILD_STAGE"
	fi
}

usage() {
	print -u2 "用法：$0 --source <PNG> --png-output <PNG> --icns-output <ICNS> --ico-output <ICO> --web-output <PNG> --windows-amd64-output <SYSO> --windows-arm64-output <SYSO> --ffmpeg <執行檔> --iconutil <執行檔> --llvm-rc <執行檔> --llvm-cvtres <執行檔>"
}

take_value() {
	if (( $# < 2 )) || [[ -z "$2" ]]; then
		usage
		exit 2
	fi
}

while (( $# > 0 )); do
	case "$1" in
		--source) take_value "$@"; SOURCE_PNG="$2"; shift 2 ;;
		--png-output) take_value "$@"; PNG_OUTPUT="$2"; shift 2 ;;
		--icns-output) take_value "$@"; ICNS_OUTPUT="$2"; shift 2 ;;
		--ico-output) take_value "$@"; ICO_OUTPUT="$2"; shift 2 ;;
		--web-output) take_value "$@"; WEB_OUTPUT="$2"; shift 2 ;;
		--windows-amd64-output) take_value "$@"; WINDOWS_AMD64_OUTPUT="$2"; shift 2 ;;
		--windows-arm64-output) take_value "$@"; WINDOWS_ARM64_OUTPUT="$2"; shift 2 ;;
		--ffmpeg) take_value "$@"; FFMPEG_TOOL="$2"; shift 2 ;;
		--iconutil) take_value "$@"; ICONUTIL_TOOL="$2"; shift 2 ;;
		--llvm-rc) take_value "$@"; LLVM_RC_TOOL="$2"; shift 2 ;;
		--llvm-cvtres) take_value "$@"; LLVM_CVTRES_TOOL="$2"; shift 2 ;;
		-h|--help) usage; exit 0 ;;
		*) print -u2 "不支援的參數：$1"; usage; exit 2 ;;
	esac
done

typeset -a required_values
required_values=(
	"$SOURCE_PNG" "$PNG_OUTPUT" "$ICNS_OUTPUT" "$ICO_OUTPUT" "$WEB_OUTPUT"
	"$WINDOWS_AMD64_OUTPUT" "$WINDOWS_ARM64_OUTPUT"
	"$FFMPEG_TOOL" "$ICONUTIL_TOOL" "$LLVM_RC_TOOL" "$LLVM_CVTRES_TOOL"
)
for required_value in "${required_values[@]}"; do
	if [[ -z "$required_value" ]]; then
		usage
		exit 2
	fi
done

for relative_value in "${required_values[@]}"; do
	case "$relative_value" in
		/*|[A-Za-z]:[\\/]*|\\\\*)
			print -u2 "路徑必須使用相對位置或 PATH 中的工具名稱：$relative_value"
			exit 2
			;;
	esac
done

if [[ ! -f "$SOURCE_PNG" ]]; then
	print -u2 "找不到來源圖示：$SOURCE_PNG"
	exit 1
fi
for tool_path in "$FFMPEG_TOOL" "$ICONUTIL_TOOL" "$LLVM_RC_TOOL" "$LLVM_CVTRES_TOOL"; do
	if ! command -v "$tool_path" >/dev/null 2>&1; then
		print -u2 "轉檔工具不存在或不可執行：$tool_path"
		exit 1
	fi
done

mkdir -p "${PNG_OUTPUT:h}" "${ICNS_OUTPUT:h}" "${ICO_OUTPUT:h}" "${WEB_OUTPUT:h}" "${WINDOWS_AMD64_OUTPUT:h}" "${WINDOWS_ARM64_OUTPUT:h}"
BUILD_STAGE="$(mktemp -d "${TMPDIR:-.}/nr-intern-icons.XXXXXX")"
trap cleanup_stage EXIT INT TERM

MASTER_ICON="$BUILD_STAGE/app-icon.png"
ICONSET="$BUILD_STAGE/AppIcon.iconset"
RESOURCE_DATA="$BUILD_STAGE/app-icon.res"
mkdir -p "$ICONSET"

"$FFMPEG_TOOL" -hide_banner -loglevel error -y -i "$SOURCE_PNG" -map_metadata -1 -vf "scale=1024:1024:flags=lanczos" -frames:v 1 -c:v png -compression_level 9 -pix_fmt rgba "$MASTER_ICON"
cp "$MASTER_ICON" "$PNG_OUTPUT"
"$FFMPEG_TOOL" -hide_banner -loglevel error -y -i "$MASTER_ICON" -map_metadata -1 -vf "scale=128:128:flags=lanczos" -frames:v 1 -c:v png -compression_level 9 -pix_fmt rgba "$WEB_OUTPUT"
"$FFMPEG_TOOL" -hide_banner -loglevel error -y -i "$MASTER_ICON" -map_metadata -1 -vf "scale=256:256:flags=lanczos" -frames:v 1 "$ICO_OUTPUT"

for icon_size in 16 32 64 128 256 512; do
	"$FFMPEG_TOOL" -hide_banner -loglevel error -y -i "$MASTER_ICON" -map_metadata -1 -vf "scale=${icon_size}:${icon_size}:flags=lanczos" -frames:v 1 -c:v png -compression_level 9 -pix_fmt rgba "$BUILD_STAGE/icon-${icon_size}.png"
done
cp "$BUILD_STAGE/icon-16.png" "$ICONSET/icon_16x16.png"
cp "$BUILD_STAGE/icon-32.png" "$ICONSET/icon_16x16@2x.png"
cp "$BUILD_STAGE/icon-32.png" "$ICONSET/icon_32x32.png"
cp "$BUILD_STAGE/icon-64.png" "$ICONSET/icon_32x32@2x.png"
cp "$BUILD_STAGE/icon-128.png" "$ICONSET/icon_128x128.png"
cp "$BUILD_STAGE/icon-256.png" "$ICONSET/icon_128x128@2x.png"
cp "$BUILD_STAGE/icon-256.png" "$ICONSET/icon_256x256.png"
cp "$BUILD_STAGE/icon-512.png" "$ICONSET/icon_256x256@2x.png"
cp "$BUILD_STAGE/icon-512.png" "$ICONSET/icon_512x512.png"
cp "$MASTER_ICON" "$ICONSET/icon_512x512@2x.png"
"$ICONUTIL_TOOL" -c icns "$ICONSET" -o "$ICNS_OUTPUT"

RESOURCE_SOURCE="${ICO_OUTPUT:h}/.nr-intern-app-icon-${$}.rc"
if [[ "${ICO_OUTPUT:t}" == *\"* ]]; then
	print -u2 "ICO 輸出檔名不可包含雙引號"
	exit 1
fi
printf '1 ICON "%s"\n' "${ICO_OUTPUT:t}" > "$RESOURCE_SOURCE"
"$LLVM_RC_TOOL" /no-preprocess /fo "$RESOURCE_DATA" "$RESOURCE_SOURCE"
"$LLVM_CVTRES_TOOL" /machine:x64 /timestamp:0 "/out:$WINDOWS_AMD64_OUTPUT" "$RESOURCE_DATA"
"$LLVM_CVTRES_TOOL" /machine:arm64 /timestamp:0 "/out:$WINDOWS_ARM64_OUTPUT" "$RESOURCE_DATA"

print "圖示資產已完成。"
