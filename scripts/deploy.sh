#!/usr/bin/env bash
# 构建（若需要）→ 部署到 Windows → **校验部署的确实是这次构建的那一份**。
#
# 这个脚本存在的唯一理由，是把一个我曾经做错过的检查机械化：
# 那次打完补丁只 build 到了别处、却把旧 exe 复制了过去，而我的校验是
# 「源文件与目标文件 md5 一致」—— 那只证明复制没出错，证明不了源是新的。
# 用户测了半天的旧版，我还以为修好了。
#
# 现在改成让**产物自报身份**：拷完之后直接问它 dist/NexusKB-debug.exe -version，
# 与 dist/.buildstamp 比对。不一致就非零退出。
#
# 用法：
#   ./scripts/deploy.sh              # 只部署（不碰正在跑的实例）
#   ./scripts/deploy.sh --restart    # 部署后重启程序
#   ./scripts/deploy.sh --skip-build # 不重新构建，直接部署 dist/ 里的现成产物
set -euo pipefail

cd "$(dirname "$0")/.."

DIST=dist
WIN_DIR="${NKB_WIN_DIR:-/mnt/c/Users/long2015/NexusKB}"
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
EXPECT="$(cat "$DIST/.buildstamp")"
echo "本次构建戳: $EXPECT"

if [ "$RESTART" = 1 ]; then
	taskkill.exe /F /IM NexusKB.exe >/dev/null 2>&1 || true
fi

# Windows 会锁住正在运行的 exe，所以要等一下再拷
mkdir -p "$WIN_DIR"
for i in $(seq 1 25); do
	cp -f "$DIST/NexusKB.exe" "$WIN_DIR/NexusKB.exe" 2>/dev/null && break
	sleep 1
done
cp -f "$DIST/NexusKB-debug.exe" "$WIN_DIR/NexusKB-debug.exe"

# 关键一步：让产物自己报身份
ACTUAL="$(cd "$WIN_DIR" && ./NexusKB-debug.exe -version 2>&1 | tr -d '\r' | sed 's/.*build=//')"
if [ "$ACTUAL" != "$EXPECT" ]; then
	echo "❌ 部署校验失败：产物自报 $ACTUAL，期望 $EXPECT" >&2
	exit 1
fi
echo "✅ 部署校验通过（$ACTUAL）→ $WIN_DIR"

if [ "$RESTART" = 1 ]; then
	# 用 WMI 创建进程：它由 WMI 服务创建，不是 WSL interop 的子进程，
	# 因此不会随终端/WSL 会话退出而被杀。
	PS1="$(wslpath -w "$WIN_DIR" 2>/dev/null || echo 'C:\Users\long2015\NexusKB')"
	cat > "$WIN_DIR/.launch.ps1" <<PS
\$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = '"$PS1\NexusKB.exe"' }
if (\$r.ReturnValue -eq 0) { Write-Output "PID=\$(\$r.ProcessId)" } else { Write-Output "FAILED=\$(\$r.ReturnValue)" }
PS
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$PS1\\.launch.ps1"
else
	echo "（未重启；要重启加 --restart）"
fi
