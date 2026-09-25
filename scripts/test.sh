#!/usr/bin/env bash
# 交叉编译测试二进制，在**真 Windows** 上跑。
#
# 为什么不能在 Linux 上跑：渲染依赖 GDI（CreateDIBSection / DrawTextW），
# 还有窗口创建、UIA、MSAA —— 全都要 Windows 会话。
# 所以这里交叉编译出 exe，再借 WSL interop 执行。
#
# 用法：
#   ./scripts/test.sh                      # 全量
#   ./scripts/test.sh -test.run 'TestUIA'  # 只跑一部分（参数透传给 test 二进制）
#   ./scripts/test.sh -test.v              # 详细输出
set -euo pipefail

cd "$(dirname "$0")/.."

APP=kb-sniffer
DIST=dist

# 放一份到 Windows 侧再执行：interop 启动 Windows 进程时，
# 从 Windows 原生路径跑最稳（Linux 路径会被翻译成 UNC，个别 API 会挑剔）。
# 用系统临时目录，别弄脏部署目录。
WIN_DIR="${KBS_TEST_DIR:-/mnt/c/Users/long2015/AppData/Local/Temp/kb-sniffer-test}"

export GOOS=windows
export GOARCH=amd64
export CGO_ENABLED=0

mkdir -p "$DIST" "$WIN_DIR"
go test -c -o "$DIST/$APP.test.exe" .
cp -f "$DIST/$APP.test.exe" "$WIN_DIR/"

cd "$WIN_DIR"
"./$APP.test.exe" -test.timeout 250s "$@"
