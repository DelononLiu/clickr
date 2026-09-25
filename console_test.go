//go:build windows

// console_test.go —— 控制台判定 —— 唯一会「打断用户正在跑的命令」的分支。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o nkb.test.exe .
//	./nkb.test.exe -test.v

package main

import "testing"

// 控制台判定必须准确：这是唯一会「打断用户正在跑的命令」的分支，
// 判错了后果比取不到词严重得多。
//
// 以前这里是 `_ = foregroundIsConsole()` —— 什么都没断言，属于摆设测试。
func TestIsConsoleClass(t *testing.T) {
	console := []string{
		"ConsoleWindowClass",            // conhost：cmd.exe / powershell.exe / WSL
		"CASCADIA_HOSTING_WINDOW_CLASS", // Windows Terminal
		"mintty",                        // Git Bash / MSYS2
	}
	for _, c := range console {
		if !isConsoleClass(c) {
			t.Errorf("%q 应当被判定为控制台", c)
		}
	}
	// 这些**不能**被判成控制台，否则会白白放弃取词
	notConsole := []string{
		"",
		"Chrome_WidgetWin_1", // VS Code / 浏览器：集成终端在里面，拦不住也不能拦
		"Notepad",
		"Chrome_WidgetWin_0",
		"consolewindowclass", // 类名比较是大小写敏感的
		"ConsoleWindowClassX",
	}
	for _, c := range notConsole {
		if isConsoleClass(c) {
			t.Errorf("%q 不该被判定为控制台", c)
		}
	}
}
