//go:build windows

// layout.go —— 几何：尺寸、间距、定位、DPI 缩放。改这里的原因是布局规则变了。

package main

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
