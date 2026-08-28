#!/bin/zsh

set -euo pipefail

SCRIPT_DIR="${0:A:h}"
PROJECT_ROOT="$SCRIPT_DIR"
typeset -a clean_targets
clean_targets=(
	"$PROJECT_ROOT/bin"
	"$PROJECT_ROOT/dist"
)

clean_target() {
	local target="$1"

	case "$target" in
		"$PROJECT_ROOT/bin"|"$PROJECT_ROOT/dist") ;;
		*)
			print -u2 "錯誤：拒絕清除非預期路徑：$target"
			return 1
			;;
	esac

	if [[ ! -e "$target" ]]; then
		print "  略過（不存在）：$target"
		return 0
	fi

	/bin/rm -rf -- "$target"
	print "  已清除：$target"
}

if [[ ! -f "$PROJECT_ROOT/go.mod" ]]; then
	print -u2 "錯誤：$PROJECT_ROOT 不是 NR-Intern 專案目錄。"
	exit 1
fi

print "開始清除 NR-Intern 可重建產物..."
for target in "${clean_targets[@]}"; do
	clean_target "$target"
done
print "清除完成；data/ 與設定檔未受影響。"
