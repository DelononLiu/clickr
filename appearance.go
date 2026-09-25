//go:build windows

// appearance.go —— 视觉规格：配色 + 几何（尺寸/间距/定位/DPI 缩放）。
//
// 合在一起是因为它们同属一类东西 —— 设计侧给的数值。
// 具体的视觉算法（距离场、文字遮罩）在 render.go。

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

const (
	menuCardW    = 185.0
	menuCardPad  = 4.0
	menuItemH    = 40.0
	menuItemPadH = 8.0
	menuIconSize = 16.0
	menuIconGap  = 4.0
	menuRadius   = 16.0
	menuItemR    = 12.0

	// 投影参数，对应有道的 box-shadow: 0 5px 10px rgba(139,146,160,.14)
	menuShadowBlur = 10.0
	menuShadowDY   = 5.0

	ballSize       = 40.0
	ballShadowBlur = 12.0
	ballShadowDY   = 5.0
	ballMarkSize   = 18.0
)

func scaled(v float64) float64 { return v * scale }

func scaledI(v float64) int32 { return int32(v*scale + 0.5) }

// 窗口必须比可见内容大出投影扩散的距离，否则阴影会被窗口边界切出一条直边
// （实测肉眼可见，球体下方 alpha 到 48 就断崖）。
func menuShadowPad() float64 { return shadowReach(menuShadowBlur, menuShadowDY) }

func ballShadowPad() float64 { return shadowReach(ballShadowBlur, ballShadowDY) }

func ballWindowSize() int32 { return scaledI(ballSize + 2*ballShadowPad()) }

func menuWindowSize(n int) (int32, int32) {
	pad := scaled(menuShadowPad())
	w := scaled(menuCardW) + pad*2
	h := float64(n)*scaled(menuItemH) + scaled(menuCardPad)*2 + pad*2
	return int32(w + 0.5), int32(h + 0.5)
}

// menuItemRect 返回第 i 项在窗口坐标系里的矩形。
func menuItemRect(i int) rect {
	pad := scaled(menuShadowPad())
	x := pad + scaled(menuCardPad)
	y := pad + scaled(menuCardPad) + float64(i)*scaled(menuItemH)
	w := scaled(menuCardW) - scaled(menuCardPad)*2
	h := scaled(menuItemH)
	return rect{int32(x), int32(y), int32(x + w), int32(y + h)}
}

func menuCardRect() rect {
	pad := scaled(menuShadowPad())
	return rect{
		int32(pad), int32(pad),
		int32(pad + scaled(menuCardW)),
		int32(pad + float64(len(menuModel_.items))*scaled(menuItemH) + scaled(menuCardPad)*2),
	}
}

// placeMenu 决定菜单往哪边弹。
// 和某道的 panelPlacement 一个思路：优先右下，放不下就翻到左边/上面，最后夹进工作区。
func placeMenu(anchor point, winW, winH int32) point {
	wa := workArea(anchor)
	pad := scaledI(menuShadowPad())
	cardW := winW - pad*2
	cardH := winH - pad*2
	gap := scaledI(8)

	cardX := anchor.X + gap
	cardY := anchor.Y + gap
	if cardX+cardW > wa.Right {
		cardX = anchor.X - gap - cardW
	}
	if cardY+cardH > wa.Bottom {
		cardY = anchor.Y - gap - cardH
	}
	cardX = clampI(cardX, wa.Left, wa.Right-cardW)
	cardY = clampI(cardY, wa.Top, wa.Bottom-cardH)
	return point{cardX - pad, cardY - pad}
}

// defaultBallPos 参考某道的默认位置：贴右边、离底部约 200px。
func defaultBallPos() point {
	probe := point{getSystemMetrics(0) - 1, getSystemMetrics(1) - 1}
	wa := workArea(probe)
	sz := ballWindowSize()
	return point{
		X: wa.Right - sz - scaledI(16),
		Y: wa.Bottom - scaledI(200),
	}
}

func constrainBall(pt point) point {
	wa := workArea(point{pt.X + ballWindowSize()/2, pt.Y + ballWindowSize()/2})
	sz := ballWindowSize()
	return point{
		X: clampI(pt.X, wa.Left, wa.Right-sz),
		Y: clampI(pt.Y, wa.Top, wa.Bottom-sz),
	}
}
