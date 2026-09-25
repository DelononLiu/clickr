#!/usr/bin/env bash
# 构建（若需要）→ 部署到 Windows → **校验部署的确实是这次构建的那一份**。
#
# 这个脚本存在的唯一理由，是把一个我曾经做错过的检查机械化：
# 那次打完补丁只 build 到了别处、却把旧 exe 复制了过去，而我的校验是
# 「源文件与目标文件 md5 一致」—— 那只证明复制没出错，证明不了源是新的。
# 用户测了半天的旧版，我还以为修好了。
#
# 现在改成让**产物自报身份**：拷完之后直接问它 dist/clickr-debug.exe -version，
# 与 dist/.buildstamp 比对。不一致就非零退出。
#
# 用法：
#   ./scripts/deploy.sh              # 只部署（不碰正在跑的实例）
#   ./scripts/deploy.sh --restart    # 部署后重启程序
#   ./scripts/deploy.sh --skip-build # 不重新构建，直接部署 dist/ 里的现成产物
set -euo pipefail

cd "$(dirname "$0")/.."

APP=clickr
DIST=dist
WIN_DIR="${CLICKR_WIN_DIR:-/mnt/c/Users/long2015/clickr}"
PID_FILE="${CLICKR_PID_FILE:-}"
RESTART=0
SKIP_BUILD=0
for a in "$@"; do
	case "$a" in
		--restart)    RESTART=1 ;;
		--skip-build) SKIP_BUILD=1 ;;
		*) echo "用法: $0 [--restart] [--skip-build]" >&2; exit 2 ;;
	esac
done

[ "$SKIP_BUILD" = 1 ] || ./scripts/build.sh >/dev/null
[ -n "$PID_FILE" ] || PID_FILE="$WIN_DIR/.pid"
EXPECT="$(cat "$DIST/.buildstamp")"
echo "本次构建戳: $EXPECT"

# stop_app 结束**我们启动的那个**实例。
#
# 优先按记录的 PID 杀，而不是按映像名。原因是实测发现的一个隐患：
# 这台机器上已经存在另一个叫 clickr 的程序
# （%LocalAppData%\com.clickr.app，Tauri 应用）。
# 按映像名（taskkill /F /IM clickr.exe）哪天会把它一起杀掉。
# 认 PID 就不会误伤。
#
# 另外这里**必须复核**，不能吞错：残留实例带着全局鼠标钩子，会和新实例抢划词事件。
# 之前写的是 `taskkill ... || true`，结果改名后旧映像名的进程一直活着，
# 锁住旧 exe 与旧日志，删除和覆盖都失败却不报错。
stop_app() {
	if [ -f "$PID_FILE" ]; then
		pid="$(tr -d '\r\n ' < "$PID_FILE" 2>/dev/null || true)"
		[ -n "$pid" ] && taskkill.exe /F /PID "$pid" >/dev/null 2>&1 || true
	fi

	# 兜底：没有 PID 记录时（例如程序是用户自己双击起来的）只能按映像名。
	# 这时如果机器上有同名的别的程序，会一起被杀 —— 所以要说出来。
	if tasklist.exe /FO CSV 2>/dev/null | tr -d '\r' | grep -qi "\"$APP.exe\""; then
		echo "注意：没有 PID 记录，改按映像名结束 $APP.exe（可能误伤同名的其他程序）"
		taskkill.exe /F /IM "$APP.exe" >/dev/null 2>&1 || true
	fi

	if tasklist.exe /FO CSV 2>/dev/null | tr -d '\r' | grep -qi "\"$APP.exe\""; then
		echo "错误：$APP.exe 仍在运行，停不掉；继续部署会拿到被锁住的旧文件" >&2
		return 1
	fi
}

if [ "$RESTART" = 1 ]; then
	stop_app
fi

# Windows 会锁住正在运行的 exe，所以要等一下再拷
mkdir -p "$WIN_DIR"
for i in $(seq 1 25); do
	cp -f "$DIST/$APP.exe" "$WIN_DIR/$APP.exe" 2>/dev/null && break
	sleep 1
done
cp -f "$DIST/$APP-debug.exe" "$WIN_DIR/$APP-debug.exe"

# 关键一步：让产物自己报身份
ACTUAL="$(cd "$WIN_DIR" && "./$APP-debug.exe" -version 2>&1 | tr -d '\r' | sed 's/.*build=//')"
if [ "$ACTUAL" != "$EXPECT" ]; then
	echo "❌ 部署校验失败：产物自报 $ACTUAL，期望 $EXPECT" >&2
	exit 1
fi
echo "✅ 部署校验通过（$ACTUAL）→ $WIN_DIR"

if [ "$RESTART" = 1 ]; then
	# 用 WMI 创建进程：它由 WMI 服务创建，不是 WSL interop 的子进程，
	# 因此不会随终端/WSL 会话退出而被杀。
	PS1="$(wslpath -w "$WIN_DIR" 2>/dev/null || echo 'C:\Users\long2015\clickr')"
	cat > "$WIN_DIR/.launch.ps1" <<PS
\$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = '"$PS1\\$APP.exe"' }
if (\$r.ReturnValue -eq 0) { Write-Output "PID=\$(\$r.ProcessId)" } else { Write-Output "FAILED=\$(\$r.ReturnValue)" }
PS
	out="$(powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$PS1\\.launch.ps1" | tr -d '\r')"
	echo "$out"
	# 记下 PID：下次 stop_app 就能精确地只杀我们启动的那个
	if printf '%s' "$out" | grep -q '^PID='; then
		printf '%s' "$out" | sed -n 's/^PID=//p' > "$PID_FILE"
	fi
else
	echo "（未重启；要重启加 --restart）"
fi
