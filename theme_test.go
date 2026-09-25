//go:build windows

// theme_test.go —— 主题读取（含「暗色主题静默失效」那条 bug 的守卫）。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o nkb.test.exe .
//	./nkb.test.exe -test.v

package main

import "testing"

func TestThemeLoads(t *testing.T) {
	th := loadTheme()
	if th.cardBg.A != 1 {
		t.Errorf("卡片背景应当不透明，实际 alpha=%v", th.cardBg.A)
	}
	if th.accent.R == 0 {
		t.Error("品牌红未设置")
	}
}

// 主题读取必须能报错。
//
// 这条是冲着坑 #9 去的：HKEY_CURRENT_USER 常量曾在 x64 上被截断成 0x80000001，
// RegOpenKeyExW 一直以 ERROR_INVALID_HANDLE 失败，而函数吞掉错误返回 false,
// 于是「暗色主题永远不生效」既不报错也无法被测试观测到。
// 只要断言 err == nil，这个 bug 当场就会被抓住。
func TestSystemThemeReadsWithoutError(t *testing.T) {
	dark, err := systemUsesDarkMode()
	if err != nil {
		t.Fatalf("读系统主题失败（HKEY/注册表路径可能有问题）: %v", err)
	}
	t.Logf("系统主题: dark=%v", dark)

	th := loadTheme()
	if th.dark != dark {
		t.Errorf("loadTheme 的 dark=%v 与系统主题 %v 不一致", th.dark, dark)
	}
	// 两套调色板必须真的不同，否则「支持暗色」是假的
	if th.cardBg == (rgba{0xFF, 0xFF, 0xFF, 1}) && dark {
		t.Error("系统是暗色，卡片背景却还是白色")
	}
}
