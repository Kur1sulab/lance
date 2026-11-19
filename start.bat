@echo off
rem 把控制台切到 UTF-8。否则本文件里的中文在 GBK 终端会乱码。
chcp 65001 >nul 2>&1
rem LANCE 后台守护启动脚本（Windows）
rem
rem 使用方法：
rem   start.bat                  前台运行（Ctrl-C 停止）
rem   start.bat -addr :9000      其余参数原样传给 lance
rem
rem 这个脚本只做一件事：启动 lance.exe，进程退出后按退出码决定要不要再拉起来。
rem
rem   0      用户正常停止       -> 结束循环
rem   75     程序要求重启       -> 立即重跑（界面里点了"一键更新"或"回滚"）
rem   其他   异常退出           -> 退避后重跑（1->2->4…最多 60 秒）
rem
rem 下载、SHA256 校验、版本替换都不在这里做，全部由 lance 启动时自己完成
rem （selfupdate 包）。脚本保持简单。细节见 start.sh 顶部说明。

setlocal enabledelayedexpansion
cd /d "%~dp0"

set "BIN=lance.exe"
if not exist "%BIN%" (
	echo [lance] 找不到可执行文件: %BIN% 1>&2
	exit /b 1
)

set "RESTART_CODE=75"
set "MAX_DELAY=60"
set /a delay=1

:loop
"%BIN%" %*
set "code=!ERRORLEVEL!"

if "!code!"=="0" (
	echo [lance] 正常退出
	exit /b 0
)

if "!code!"=="%RESTART_CODE%" (
	rem 更新/回滚已就绪。重启后 lance 会在启动时完成版本替换。
	echo [lance] 收到重启请求: 应用新版本…
	set /a delay=1
	goto loop
)

echo [lance] 异常退出 ^(code=!code!^), !delay!s 后重启 1>&2
rem timeout 在重定向的控制台里会失败，所以用 ping 代替（延迟 N 秒需要 N+1 次）。
set /a pings=!delay!+1
ping -n !pings! 127.0.0.1 >nul 2>&1
set /a delay=!delay!*2
if !delay! gtr %MAX_DELAY% set /a delay=%MAX_DELAY%
goto loop
