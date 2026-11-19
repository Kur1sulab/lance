#!/bin/sh
# LANCE 守护进程启动脚本（Linux / macOS / Docker ENTRYPOINT）
#
# 使用方法：
#   ./start.sh                       前台运行（Ctrl-C 停止）
#   nohup ./start.sh >lance.log 2>&1 &   后台常驻
#   ./start.sh -addr :9000           额外参数原样传给 lance
#
# 此脚本只做一件事：运行 lance，进程退出后根据退出码决定是否重新启动。
#
#   0      用户正常停止     → 循环结束
#   75     程序请求重启  → 立即重新执行（在界面点击"一键更新"或"回滚"）
#   其他  异常退出          → 退避后重新执行(1→2→4…最长 60 秒)
#
# 下载、SHA256 校验和验证、版本替换特意不在此处进行。这段逻辑要在 sh 和 bat 中各写一份，
# 这恰恰是最不能出错的部分。一旦被替换成无法执行的二进制文件，此脚本就会忠实地
# 反复弹出，用户只能直接登录机器手动恢复。因此校验·替换全部放在 Go（selfupdate 包）中，
# 让 lance 在启动时自行处理，脚本保持简单。
set -u

cd "$(dirname "$0")" || exit 1

BIN=./lance
[ -x "$BIN" ] || { echo "[lance] 找不到可执行文件：$BIN" >&2; exit 1; }

RESTART_CODE=75
MAX_DELAY=60

child=0
stopping=0

# 向 lance 主体传递停止信号。
#
# 在 Docker 中这是必需的。docker stop 只会把 SIGTERM 发送给 PID 1（即本脚本），
# 不会发送给子进程。若不传递，lance 收不到信号，无法优雅退出，
# 10 秒后被 SIGKILL 强制终止，正在执行的任务会中途中断。
forward() {
	stopping=1
	if [ "$child" -ne 0 ]; then
		kill -TERM "$child" 2>/dev/null || true
	fi
}
trap forward INT TERM

delay=1
while :; do
	"$BIN" "$@" &
	child=$!

	# 信号会中断 wait，使其返回大于 128 的值。此时子进程仍在进行优雅退出，因此
	# 需要再 wait 一次，才能获得真正的退出码。
	wait "$child"
	code=$?
	if [ "$code" -gt 128 ]; then
		wait "$child"
		code=$?
	fi
	child=0

	if [ "$stopping" -eq 1 ]; then
		echo "[lance] 已停止"
		exit 0
	fi

	case "$code" in
		0)
			echo "[lance] 正常退出"
			exit 0
			;;
		"$RESTART_CODE")
			# 更新/回滚已就绪。再次执行后，lance 将在启动时完成版本切换（参见 selfupdate.Bootstrap）。
			echo "[lance] 请求重启（应用新版本）…"
			delay=1
			;;
		*)
			echo "[lance] 异常退出 (code=$code)，${delay}s 后重启" >&2
			sleep "$delay"
			delay=$((delay * 2))
			[ "$delay" -gt "$MAX_DELAY" ] && delay=$MAX_DELAY
			;;
	esac
done
