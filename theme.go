//go:build windows

// theme.go —— 配色。改这里的原因是设计稿变了。

package main

import (
	"fmt"
	"log"
	"sync"
	"unsafe"
)

type theme struct {
	dark       bool
	cardBg     rgba
	cardBorder rgba
	text1      rgba
	text4      rgba
	hover      rgba
	shadow     rgba
	sep        rgba
	ballBg     rgba
	ballBorder rgba
	accent     rgba
}

func loadTheme() theme {
	dark, err := systemUsesDarkMode()
	if err != nil {
		// 以前这里静默当作「浅色」——「读注册表失败」和「系统就是浅色」
		// 无法区分，于是暗色主题失效过很久都没人发现。
		// 现在只记一次，不刷屏。
		logThemeOnce.Do(func() {
			log.Printf("[theme] 读系统主题失败，按浅色处理: %v", err)
		})
	}
	if dark {
		return theme{
			dark:       true,
			cardBg:     rgba{0x20, 0x21, 0x24, 1},
			cardBorder: rgba{0x3B, 0x3C, 0x40, 1},
			text1:      rgba{0xD2, 0xD3, 0xD6, 1},
			text4:      rgba{0x8A, 0x8C, 0x90, 1},
			hover:      rgba{0x9B, 0xA5, 0xAF, 0.16},
			shadow:     rgba{0x00, 0x00, 0x00, 0}, // 有道暗色下 box-shadow: none
			sep:        rgba{0x3B, 0x3C, 0x40, 1},
			ballBg:     rgba{0x30, 0x31, 0x34, 1},
			ballBorder: rgba{0xFF, 0xFF, 0xFF, 0.08},
			accent:     rgba{0xF0, 0x14, 0x2F, 1},
		}
	}
	return theme{
		dark:       false,
		cardBg:     rgba{0xFF, 0xFF, 0xFF, 1},
		cardBorder: rgba{0xE4, 0xE7, 0xF3, 1},
		text1:      rgba{0x2A, 0x2B, 0x2E, 1},
		text4:      rgba{0xA8, 0xAA, 0xAD, 1},
		hover:      rgba{0x9B, 0xA5, 0xAF, 0.12},
		shadow:     rgba{0x8B, 0x92, 0xA0, 0.14},
		sep:        rgba{0xE4, 0xE7, 0xF3, 1},
		ballBg:     rgba{0xFF, 0xFF, 0xFF, 1},
		ballBorder: rgba{0x00, 0x00, 0x00, 0.07},
		accent:     rgba{0xF0, 0x14, 0x2F, 1},
	}
}

// 主题读取失败只提示一次，避免每次渲染都刷日志
var logThemeOnce sync.Once

// systemUsesDarkMode 读 HKCU\...\Themes\Personalize\AppsUseLightTheme。
// 每次显示菜单时现查，这样用户在系统里切换主题后无需重启。
func systemUsesDarkMode() (bool, error) {
	sub := utf16Ptr(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	val := utf16Ptr("AppsUseLightTheme")
	var hkey uintptr
	if r, _, _ := pRegOpenKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(sub)),
		0, 0x20019 /*KEY_READ*/, uintptr(unsafe.Pointer(&hkey))); r != 0 {
		return false, fmt.Errorf("RegOpenKeyExW 失败 code=%d", r)
	}
	defer pRegCloseKey.Call(hkey)

	var data uint32
	cb := uint32(4)
	r, _, _ := pRegQueryValueExW.Call(hkey, uintptr(unsafe.Pointer(val)), 0, 0,
		uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&cb)))
	if r != 0 {
		return false, fmt.Errorf("RegQueryValueExW 失败 code=%d", r)
	}
	// AppsUseLightTheme: 0 = 暗色，非 0 = 浅色
	return data == 0, nil
}
