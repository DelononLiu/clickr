//go:build windows

// ballpos.go —— 悬浮球位置的持久化（文件 IO）。
//
// 从 UI 文件里拿出来：存储格式和布局/绘制没有共同的变化原因，
// 而且放在这里才谈得上"换个存储方式不动界面代码"。

package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func posFile() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "NexusKB", "ball.pos")
}

func loadBallPos() (point, bool) {
	f := posFile()
	if f == "" {
		return point{}, false
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return point{}, false
	}
	parts := strings.Fields(strings.TrimSpace(string(b)))
	if len(parts) != 2 {
		return point{}, false
	}
	x, err1 := strconv.Atoi(parts[0])
	y, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return point{}, false
	}
	return point{int32(x), int32(y)}, true
}

func saveBallPos(pt point) {
	f := posFile()
	if f == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		log.Printf("[ui] 保存位置失败: %v", err)
		return
	}
	data := strconv.Itoa(int(pt.X)) + " " + strconv.Itoa(int(pt.Y))
	if err := os.WriteFile(f, []byte(data), 0o644); err != nil {
		log.Printf("[ui] 保存位置失败: %v", err)
	}
}
