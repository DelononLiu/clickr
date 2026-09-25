#!/usr/bin/env bash
# 交叉编译出 Windows 可执行文件，产物放 dist/。
#
# 全部代码只用 syscall + 标准库，没有 cgo，所以不需要 mingw-w64：
# CGO_ENABLED=0 就能从 Linux/WSL 直接产出原生 PE。
#
# 构建戳是刻意加的。之前吃过一次亏 —— 打完补丁只 build 到了别处，
# 却把旧的 exe 部署了出去，用户测的全程是旧版；而我当时用「源文件与目标
# 文件 md5 一致」去校验，那只证明复制没出错，**证明不了源文件是新的**。
# 现在每次构建都注入时间戳，并且机械校验它真的写进了二进制；
# 部署后可以直接问产物自己：dist/kb-sniffer-debug.exe -version
set -euo pipefail

cd "$(dirname "$0")/.."

# 名字集中在这里。改项目名只动这一行 ——
# 散在各处的话，改名一定会漏（这次改名实测漏了 88 处）。
APP=kb-sniffer

DIST=dist
OUT="$DIST/$APP.exe"
DEBUG_OUT="$DIST/$APP-debug.exe"
STAMP="$(date +%Y%m%d-%H%M%S)"

export GOOS=windows
export GOARCH=amd64
export CGO_ENABLED=0

mkdir -p "$DIST"
echo "构建戳: $STAMP"

# 发布版：GUI 子系统，双击不弹黑框
go build -trimpath -ldflags "-H=windowsgui -s -w -X main.buildStamp=$STAMP" -o "$OUT" .
# 调试版：保留控制台，-debug 能看到实时日志
go build -trimpath -ldflags "-s -w -X main.buildStamp=$STAMP" -o "$DEBUG_OUT" .

# 机械校验：戳必须真的在二进制里，否则说明构建或替换环节出了问题
for f in "$OUT" "$DEBUG_OUT"; do
	if ! grep -aq "$STAMP" "$f"; then
		echo "错误：构建戳未写入 $f，构建流程有问题" >&2
		exit 1
	fi
done

echo "$STAMP" > "$DIST/.buildstamp"
ls -lh "$OUT" "$DEBUG_OUT"
file "$OUT" | cut -d: -f2-
