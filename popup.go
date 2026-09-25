//go:build windows

// popup.go —— 弹出菜单：绘制 + 窗口行为（显示/隐藏/命中测试/选区锚点）+ 菜单内容。

package main

import (
	"log"
	"net/url"
	"unsafe"
)

func renderMenu() {
	th := loadTheme()
	n := len(menuModel_.items)
	w, h := menuWindowSize(n)

	if menuSurf != nil && (menuSurf.w != w || menuSurf.h != h) {
		menuSurf.free()
		menuSurf = nil
	}
	if menuSurf == nil {
		menuSurf = newSurface(w, h)
	}
	menuSurf.clear()

	pad := scaled(menuShadowPad())
	cardW := scaled(menuCardW)
	cardH := float64(n)*scaled(menuItemH) + scaled(menuCardPad)*2
	radius := scaled(menuRadius)

	// 投影 → 卡片 → 边框（顺序不能反，阴影必须垫在底下）
	if th.shadow.A > 0 {
		menuSurf.shadowRoundRect(pad, pad, cardW, cardH, radius,
			scaled(menuShadowBlur), scaled(menuShadowDY), th.shadow)
	}
	menuSurf.fillRoundRect(pad, pad, cardW, cardH, radius, th.cardBg)
	menuSurf.strokeRoundRect(pad+0.5, pad+0.5, cardW-1, cardH-1, radius-0.5, scaled(1), th.cardBorder)

	fTitle := font(scaledI(14), fwNormal)
	fShortcut := font(scaledI(13), fwNormal)

	for i, it := range menuModel_.items {
		ir := menuItemRect(i)
		rx := scaled(menuItemR)

		if i == menuHover {
			menuSurf.fillRoundRect(float64(ir.Left), float64(ir.Top),
				float64(ir.width()), float64(ir.height()), rx, th.hover)
		}

		// 图标
		iconX := float64(ir.Left) + scaled(menuItemPadH)
		iconY := float64(ir.Top) + (scaled(menuItemH)-scaled(menuIconSize))/2
		drawMenuIcon(menuSurf, it.icon, iconX, iconY, scaled(menuIconSize), th.text1)

		// 标题
		titleX := iconX + scaled(menuIconSize) + scaled(menuIconGap)
		titleW := float64(ir.Right) - scaled(menuItemPadH) - titleX
		shortcutW := float64(0)
		if i == menuHover && it.shortcut != "" {
			shortcutW = float64(textWidth(it.shortcut, fShortcut)) + scaled(12)
			titleW -= shortcutW
		}
		lineH := scaled(menuItemH)
		menuSurf.drawText(int32(titleX+0.5), ir.Top, it.title, fTitle,
			int32(titleW+0.5), int32(lineH), th.text1)

		// 快捷键只在 hover 时出现（和某道的 skill-item--hover 行为一致）
		if i == menuHover && it.shortcut != "" {
			sw := float64(textWidth(it.shortcut, fShortcut))
			sx := float64(ir.Right) - scaled(menuItemPadH) - sw
			menuSurf.drawText(int32(sx+0.5), ir.Top, it.shortcut, fShortcut,
				int32(sw+2), int32(lineH), th.text4)
		}
	}
}

// drawMenuIcon 画 16x16 菜单图标。
func drawMenuIcon(s *surface, icon int, x, y, box float64, col rgba) {
	th := 1.4 * scale
	switch icon {
	case iconCopy:
		// 两张错开的圆角方框
		s.strokeRoundRect(x+box*0.34, y+box*0.03, box*0.63, box*0.63, box*0.14, th, col)
		s.strokeRoundRect(x+box*0.03, y+box*0.34, box*0.63, box*0.63, box*0.14, th, col)
	case iconSearch:
		// 放大镜
		s.strokeCircle(x+box*0.42, y+box*0.42, box*0.32, th, col)
		s.fillLine(x+box*0.66, y+box*0.66, x+box*0.95, y+box*0.95, th*1.2, col)
	case iconClose:
		s.fillLine(x+box*0.22, y+box*0.22, x+box*0.78, y+box*0.78, th*1.2, col)
		s.fillLine(x+box*0.78, y+box*0.22, x+box*0.22, y+box*0.78, th*1.2, col)
	case iconTranslate:
		// 「文A」——直接用字体画，简单且辨识度高
		f := font(int32(box*0.72+0.5), fwMedium)
		s.drawText(int32(x), int32(y), "文", f, int32(box*0.58), int32(box), col)
		s.drawText(int32(x+box*0.56), int32(y), "A", f, int32(box*0.46), int32(box), col)
	}
}

// showMenu 在主线程上显示菜单（必须在窗口所属线程调用）。
func showMenu(anchor point, model menuModel) {
	menuModel_ = model
	menuHover = -1
	menuPressed = -1

	// 跟随锚点所在显示器的缩放
	if s := float64(dpiForPoint(anchor)) / 96.0; s != scale {
		scale = s
		recreateSurfaces()
	}

	renderMenu()
	w, h := menuWindowSize(len(model.items))
	menuPos = placeMenu(anchor, w, h)
	menuSurf.present(hwndMenu, menuPos.X, menuPos.Y)

	// 运行期证据：菜单显示前后，前台窗口分别是谁。
	//
	// 「不抢焦点」是本项目的硬要求（一旦抢了焦点，源程序的选区会被清掉，
	// 后续 Ctrl+C 也就复制不到东西）。静态上我们只用了 WS_EX_NOACTIVATE +
	// SWP_NOACTIVATE，从不调 SetForegroundWindow/ShowWindow —— 但那只证明
	// "没写激活代码"，证明不了运行期实际行为。这一行是量出来的。
	logForegroundAfterMenuShown()

	card := menuCardRect()
	menuRectMu.Lock()
	menuScreenRect = rect{
		card.Left + menuPos.X, card.Top + menuPos.Y,
		card.Right + menuPos.X, card.Bottom + menuPos.Y,
	}
	menuShown = true
	menuRectMu.Unlock()
}

func hideMenu() {
	// 先置位再动窗口：避免持锁期间调 Win32
	menuRectMu.Lock()
	shown := menuShown
	menuShown = false
	menuRectMu.Unlock()
	if !shown {
		return
	}
	menuHover = -1
	showWindow(hwndMenu, swHide)
}

// logForegroundAfterMenuShown 记录菜单出现后的前台窗口，用来验证「不抢焦点」。
//
// 判据：菜单显示之后，前台窗口**必须仍然是源程序**。
// 如果变成我们自己的窗口（NexusKBFloatBall / NexusKBSelectMenu），
// 那就是抢了焦点，属于严重回归。
func logForegroundAfterMenuShown() {
	fg, _, _ := pGetForegroundWindow.Call()
	class := windowClass(fg)
	ours := fg == hwndBall || fg == hwndMenu
	if ours {
		log.Printf("[focus] ⚠️ 菜单显示后前台窗口变成了我们自己（class=%q hwnd=%#x）—— "+
			"抢焦点了，源程序的选区会被清掉", class, fg)
		return
	}
	log.Printf("[focus] 菜单显示后前台窗口仍是 %q（未抢焦点）", foregroundWindowLabel())
}

// hideMenuIfOutside 由鼠标钩子线程在「左键按下」时调用。
//
// 两个刻意的选择：
//
//   - 判据是**卡片矩形**，不是窗口矩形，也不靠命中测试。
//     分层窗口里 alpha 不为 0 的阴影区同样会吃掉鼠标消息，
//     HM_NCHITTEST 的 HTTRANSPARENT 又只在同线程内转发，所以必须在钩子里判掉。
//
//   - 只 PostMessage 回主线程，不在这里直接 ShowWindow。
//     跨线程操作窗口要走 SendMessage 同步，容易和主线程的渲染互相卡住；
//     而主线程此刻正在 GetMessage 空转，收到消息后会立刻隐藏。
func hideMenuIfOutside(pt point) {
	menuRectMu.Lock()
	shown, r := menuShown, menuScreenRect
	menuRectMu.Unlock()
	if !shown || r.contains(pt) {
		return
	}
	postToMain(wmHideMenu)
}

func recreateSurfaces() {
	if ballSurf != nil {
		ballSurf.free()
		ballSurf = nil
	}
	if menuSurf != nil {
		menuSurf.free()
		menuSurf = nil
	}
	renderBall()
}

// hitTest 让阴影区域尽量把点击透出去。
//
// 注意：分层窗口的命中测试是按 alpha 通道做的，alpha 不为 0 的地方会吃掉消息；
// HTTRANSPARENT 只在同线程内转发。所以这只是「尽力而为」，
// 真正保证行为正确的是钩子里的 hideMenuIfOutside。
func hitTest(hwnd uintptr, screenPt point) uintptr {
	if hwnd == hwndBall {
		c := ballCenter()
		dx := float64(screenPt.X - c.X)
		dy := float64(screenPt.Y - c.Y)
		if dx*dx+dy*dy <= scaled(ballSize/2+1)*scaled(ballSize/2+1) {
			return htClient
		}
		return htTransparentResult()
	}
	if hwnd == hwndMenu {
		card := menuCardRect()
		screen := rect{
			card.Left + menuPos.X, card.Top + menuPos.Y,
			card.Right + menuPos.X, card.Bottom + menuPos.Y,
		}
		if screen.contains(screenPt) {
			return htClient
		}
		return htTransparentResult()
	}
	return htClient
}

// htTransparentResult 对应 HTTRANSPARENT(-1)。
// wndProc 的返回值是 LRESULT(uintptr)，而「负数常量转 uintptr」在 Go 里是编译错误，
// 所以这里直接返回全 1。
func htTransparentResult() uintptr { return ^uintptr(0) }

const (
	iconCopy = iota + 1
	iconSearch
	iconTranslate
	iconClose
)

type menuItem struct {
	title    string
	shortcut string
	icon     int
	run      func()
}

type menuModel struct {
	items []menuItem
}

func selectionMenu(text string) menuModel {
	q := url.QueryEscape(text)
	return menuModel{items: []menuItem{
		{title: "复制", shortcut: "Ctrl+C", icon: iconCopy, run: func() {
			if err := setClipboardText(text); err != nil {
				log.Printf("[action] 复制失败: %v", err)
			}
		}},
		{title: "搜索", shortcut: "Enter", icon: iconSearch, run: func() {
			shellOpen("https://www.bing.com/search?q=" + q)
		}},
		{title: "翻译", shortcut: "Ctrl+T", icon: iconTranslate, run: func() {
			shellOpen("https://dict.youdao.com/result?word=" + q + "&lang=en")
		}},
	}}
}

func shellOpen(url string) {
	op := utf16Ptr("open")
	pShellExecuteW.Call(0, uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(utf16Ptr(url))), 0, 0, swShownormal)
}
