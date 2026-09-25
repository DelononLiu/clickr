//go:build windows

// ui.go —— 悬浮球 + 弹出菜单两个分层窗口。
//
// 视觉参数直接对着有道那套 CSS 抄的（数值来自反解出来的 index-B-wLol35.css）：
//
//	卡片      width:185  border-radius:16  border:1px #e4e7f3
//	          background:#fff   box-shadow: 0 5px 10px rgba(139,146,160,.14)
//	菜单项    height:40   border-radius:12   padding:0 8px
//	hover     background: rgba(155,165,175,.12)
//	标题      14px / #2a2b2e        快捷键 13px / #a8aaad（仅 hover 时显示）
//	暗色      卡片 #202124  边框 #3B3C40  文字 #D2D3D6  无阴影
//
// 两个窗口都是 WS_EX_LAYERED|WS_EX_TOPMOST|WS_EX_TOOLWINDOW|WS_EX_NOACTIVATE。
// NOACTIVATE 是命门：一旦弹窗抢了焦点，源程序的选区高亮会消失、选区也可能被清掉，
// 后面的 Ctrl+C 就复制不到东西了。显示一律走 UpdateLayeredWindow
// （它等价于 SetWindowPos + SWP_NOACTIVATE），绝不用 ShowWindow(SW_SHOW)。
package main

import (
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// ================================================================ 布局常量（96dpi 下的逻辑值）

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
func scaledI(v float64) int32  { return int32(v*scale + 0.5) }

// 窗口必须比可见内容大出投影扩散的距离，否则阴影会被窗口边界切出一条直边
// （实测肉眼可见，球体下方 alpha 到 48 就断崖）。
func menuShadowPad() float64 { return shadowReach(menuShadowBlur, menuShadowDY) }
func ballShadowPad() float64 { return shadowReach(ballShadowBlur, ballShadowDY) }

// ================================================================ 主题

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

// ================================================================ 菜单模型

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

// ================================================================ 全局状态

// 主题读取失败只提示一次，避免每次渲染都刷日志
var logThemeOnce sync.Once

var (
	scale = 1.0

	hwndBall uintptr
	hwndMenu uintptr
	ballSurf *surface
	menuSurf *surface

	// 以下状态**只允许 UI 线程（跑消息循环的那个）读写**。
	// 其它线程要改菜单内容一律走 pending* + PostMessage。
	ballPos     point
	menuPos     point
	menuModel_  menuModel
	menuHover   int
	menuPressed int

	ballDragging bool
	ballGrabOff  point
	ballMoved    bool

	// 跨线程投递区：采集线程在锁内写，UI 线程在 wmShowMenu 里取走。
	//
	// 早先是直接写 menuModel_/menuText，而 UI 线程渲染时无锁读它们 ——
	// 数据竞争。根因是「跨线程投递」和「UI 线程独占状态」这两件事
	// 共用了一组变量，现在按所有权拆开。
	stateMu     sync.Mutex
	pendingAt   point
	pendingMenu menuModel

	// lastSelection 由采集回调写、UI 线程读，单独一把锁。
	lastSelMu     sync.Mutex
	lastSelection string

	// 菜单卡片的屏幕矩形 + 「菜单在不在」这一个事实，供钩子线程查「点外面了没」。
	//
	// 唯一真值来源：以前 menuVisible 和 menuShown 各表示一次同一件事，
	// show/hide 各写两遍，两份表示可以互相矛盾。现在只留 menuShown。
	//
	// 单独一把锁：钩子线程读它、UI 线程写它。绝不能持锁去碰窗口
	// （showWindow 会走 SendMessage，持锁时调用会拖住钩子线程）。
	menuRectMu     sync.Mutex
	menuScreenRect rect
	menuShown      bool
)

const (
	classBall = "NexusKBFloatBall"
	classMenu = "NexusKBSelectMenu"
)

// ================================================================ 窗口类与创建

var wndProcAddr = syscall.NewCallback(wndProc)

func registerWindowClass(name string, cursorID uintptr) error {
	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   wndProcAddr,
		HInstance:     getModuleHandle(),
		HCursor:       loadCursor(cursorID),
		LpszClassName: utf16Ptr(name),
	}
	atom, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return err
	}
	return nil
}

// createLayeredWindow 建一个初始不可见的弹出窗口。
// 注意 WS_POPUP + 四个扩展样式，以及**没有** WS_VISIBLE —— 显示交给 present()。
func createLayeredWindow(class string) (uintptr, error) {
	ex := uintptr(wsExLayered | wsExTopmost | wsExToolWindow | wsExNoActivate)
	h, _, err := pCreateWindowExW.Call(
		ex,
		uintptr(unsafe.Pointer(utf16Ptr(class))),
		uintptr(unsafe.Pointer(utf16Ptr("NexusKB"))),
		uintptr(wsPopup),
		0, 0, 10, 10,
		0, 0, getModuleHandle(), 0)
	if h == 0 {
		return 0, err
	}
	return h, nil
}

func createWindows() error {
	if err := registerWindowClass(classBall, idcHand); err != nil {
		return err
	}
	if err := registerWindowClass(classMenu, idcArrow); err != nil {
		return err
	}
	var err error
	if hwndBall, err = createLayeredWindow(classBall); err != nil {
		return err
	}
	if hwndMenu, err = createLayeredWindow(classMenu); err != nil {
		return err
	}
	return nil
}

// ================================================================ 悬浮球

func ballWindowSize() int32 { return scaledI(ballSize + 2*ballShadowPad()) }

func renderBall() {
	th := loadTheme()
	sz := ballWindowSize()
	if ballSurf != nil && ballSurf.w != sz {
		ballSurf.free()
		ballSurf = nil
	}
	if ballSurf == nil {
		ballSurf = newSurface(sz, sz)
	}
	ballSurf.clear()

	c := float64(ballWindowSize()) / 2
	r := scaled(ballSize) / 2

	if th.dark {
		ballSurf.shadowRoundRect(c-r, c-r, r*2, r*2, r,
			scaled(ballShadowBlur), scaled(ballShadowDY), rgba{0, 0, 0, 0.35})
	} else {
		ballSurf.shadowRoundRect(c-r, c-r, r*2, r*2, r,
			scaled(ballShadowBlur), scaled(ballShadowDY), rgba{0x8B, 0x92, 0xA0, 0.30})
	}
	ballSurf.fillCircle(c, c, r, th.ballBg)
	ballSurf.strokeCircle(c, c, r-0.5, scaled(1), th.ballBorder)

	// 中心品牌标记（参考有道那个红点：实测主色约 #F0142F）
	ballSurf.fillSparkle(c, c, scaled(ballMarkSize)/2, th.accent)
}

func showBall(pt point) {
	ballPos = pt
	if ballSurf == nil {
		renderBall()
	}
	ballSurf.present(hwndBall, ballPos.X, ballPos.Y)
}

func moveBallTo(pt point) {
	ballPos = pt
	if ballSurf != nil {
		ballSurf.present(hwndBall, ballPos.X, ballPos.Y)
	}
}

func ballCenter() point {
	half := ballWindowSize() / 2
	return point{ballPos.X + half, ballPos.Y + half}
}

// ================================================================ 菜单

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

// ================================================================ 显示 / 隐藏

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

// ================================================================ 窗口过程

// wndProc 是所有窗口消息的入口。
//
// 外面的 recover 是必须的：这个函数由 Go 以 C 回调的形式被 Windows 调用，
// 一旦 panic 冒泡出去，Go 运行时**无法把 panic 交回 C 调用者**，
// 进程会直接静默死掉（没有日志、没有报错框），排查时毫无线索。
func wndProc(hwnd uintptr, message uint32, wparam, lparam uintptr) (ret uintptr) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[wndproc] 处理消息 %#x 时 panic: %v", message, r)
			ret = 0
		}
	}()
	return wndProcImpl(hwnd, message, wparam, lparam)
}

func wndProcImpl(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	switch message {
	case wmNCHitTest:
		x := int32(int16(lparam & 0xFFFF))
		y := int32(int16((lparam >> 16) & 0xFFFF))
		return hitTest(hwnd, point{x, y})

	case wmMouseMove:
		onMouseMove(hwnd, lparam)

	case wmMouseLeave:
		if hwnd == hwndMenu {
			changed := menuHover != -1
			menuHover = -1
			if changed {
				renderMenu()
				menuSurf.present(hwndMenu, menuPos.X, menuPos.Y)
			}
		}

	case wmLButtonDown:
		onLButtonDown(hwnd, lparam)

	case wmLButtonUp:
		onLButtonUp(hwnd, lparam)

	case wmRButtonUp:
		if hwnd == hwndBall {
			onBallRightClick()
		}

	case wmDpiChanged:
		// 悬浮球被拖到另一块不同缩放的显示器上
		if hwnd == hwndBall {
			scale = float64(dpiForPoint(getCursorPos())) / 96.0
			recreateSurfaces()
			moveBallTo(constrainBall(ballPos))
		}

	case wmShowMenu:
		stateMu.Lock()
		model, anchor := pendingMenu, pendingAt
		stateMu.Unlock()
		if len(model.items) > 0 {
			showMenu(anchor, model)
		}

	case wmHideMenu:
		hideMenu()

	case wmQuitApp:
		hideMenu()
		pDestroyWindow.Call(hwndBall)
		pDestroyWindow.Call(hwndMenu)
		pPostQuitMessage.Call(0)

	case wmDestroy:
		pPostQuitMessage.Call(0)
	}

	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(message), wparam, lparam)
	return r
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

func onMouseMove(hwnd uintptr, lparam uintptr) {
	pt := getCursorPos()

	if hwnd == hwndBall {
		if ballDragging {
			nx := pt.X - ballGrabOff.X
			ny := pt.Y - ballGrabOff.Y
			ballMoved = true
			moveBallTo(point{nx, ny})
		}
		return
	}

	// 菜单：算出 hover 项，只有变化时才重绘
	tme := trackMouseEvent{CbSize: uint32(unsafe.Sizeof(trackMouseEvent{})),
		DwFlags: tmeLeave, HwndTrack: hwndBall}
	if hwnd == hwndMenu {
		tme.HwndTrack = hwndMenu
	}
	pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))

	local := point{pt.X - menuPos.X, pt.Y - menuPos.Y}
	idx := -1
	for i := range menuModel_.items {
		if menuItemRect(i).contains(local) {
			idx = i
			break
		}
	}
	changed := idx != menuHover
	menuHover = idx
	if changed {
		renderMenu()
		menuSurf.present(hwndMenu, menuPos.X, menuPos.Y)
	}
}

func onLButtonDown(hwnd uintptr, lparam uintptr) {
	pt := getCursorPos()
	if hwnd == hwndBall {
		ballDragging = true
		ballMoved = false
		ballGrabOff = point{pt.X - ballPos.X, pt.Y - ballPos.Y}
		return
	}
	if hwnd == hwndMenu {
		local := point{pt.X - menuPos.X, pt.Y - menuPos.Y}
		menuPressed = -1
		for i := range menuModel_.items {
			if menuItemRect(i).contains(local) {
				menuPressed = i
				break
			}
		}
	}
}

func onLButtonUp(hwnd uintptr, lparam uintptr) {
	pt := getCursorPos()

	if hwnd == hwndBall {
		ballDragging = false
		if ballMoved {
			saveBallPos(constrainBall(ballPos))
			moveBallTo(constrainBall(ballPos))
			return
		}
		// 单击悬浮球：把上一次选中的文字再弹一次（相当于「重开菜单」）
		if sel := getLastSelection(); sel != "" {
			queueMenu(pt, selectionMenu(sel))
		}
		return
	}

	if hwnd == hwndMenu {
		local := point{pt.X - menuPos.X, pt.Y - menuPos.Y}
		pressed := menuPressed
		menuPressed = -1
		var chosen *menuItem
		if pressed >= 0 && pressed < len(menuModel_.items) &&
			menuItemRect(pressed).contains(local) {
			it := menuModel_.items[pressed]
			chosen = &it
		}

		hideMenu()
		if chosen != nil && chosen.run != nil {
			go chosen.run()
		}
	}
}

func onBallRightClick() {
	model := menuModel{items: []menuItem{
		{title: "退出", shortcut: "", icon: iconClose, run: func() {
			postToMain(wmQuitApp)
		}},
	}}
	queueMenu(ballCenter(), model)
}

// ================================================================ 主线程投递

// queueMenu 从任意线程请求主线程弹菜单。
func queueMenu(anchor point, model menuModel) {
	stateMu.Lock()
	pendingAt, pendingMenu = anchor, model
	stateMu.Unlock()
	postToMain(wmShowMenu)
}

func postToMain(message uint32) {
	pPostMessageW.Call(hwndBall, uintptr(message), 0, 0)
}

// ================================================================ 位置持久化

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

// ================================================================ 菜单内容

func setLastSelection(text string) {
	lastSelMu.Lock()
	lastSelection = text
	lastSelMu.Unlock()
}

func getLastSelection() string {
	lastSelMu.Lock()
	defer lastSelMu.Unlock()
	return lastSelection
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
