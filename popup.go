//go:build windows

// popup.go —— 弹出菜单：绘制 + 窗口行为（显示/隐藏/命中测试/选区锚点）+ 菜单内容。

package main

import (
	"log"
	"math"
	"net/url"
	"time"
	"unsafe"
)

// renderMenu 画整个菜单：横向工具条（+ 需要时的「…」二级面板）。
//
// 画卡片的顺序是**三趟**而不是一张一张画完：先所有投影、再所有底色、最后所有描边。
// 两张卡片的阴影会互相盖到对方身上（工具条的投影往下扩散 30 多 px，正好落在面板上），
// 一张张画完的话，面板的投影会把工具条的下缘也压暗一块。
func renderMenu() {
	th := loadTheme()
	m := menuModel_
	w, h := menuWindowSize(m)

	if menuSurf != nil && (menuSurf.w != w || menuSurf.h != h) {
		menuSurf.free()
		menuSurf = nil
	}
	if menuSurf == nil {
		menuSurf = newSurface(w, h)
	}
	menuSurf.clear()

	radius := scaled(menuRadius)
	bar := barCardRect(m)
	showPanel := menuExpanded && len(m.more) > 0
	var panel rect
	if showPanel {
		panel = panelCardRect(m)
	}

	for _, r := range cardsOf(bar, panel, showPanel) {
		for _, l := range menuShadowLayers {
			menuSurf.shadowRoundRect(float64(r.Left), float64(r.Top),
				float64(r.width()), float64(r.height()), radius,
				scaled(l.blur), scaled(l.dy), l.c)
		}
	}
	for _, r := range cardsOf(bar, panel, showPanel) {
		menuSurf.fillRoundRect(float64(r.Left), float64(r.Top),
			float64(r.width()), float64(r.height()), radius, th.cardBg)
	}
	// 卡片没有单独的描边：豆包那圈 1px 的"边框"就是投影的第一层
	// （0 0 1px 0 rgba(0,0,0,.3)），见 menuShadowLayers。多画一圈反而更重。

	drawMenuBar(th, m)
	if showPanel {
		drawMenuPanel(th, m)
	}
}

func cardsOf(bar, panel rect, withPanel bool) []rect {
	if withPanel {
		return []rect{bar, panel}
	}
	return []rect{bar}
}

// drawMenuBar 画横向工具条：图标在前、文字在后，最后一项是「…」。
func drawMenuBar(th theme, m menuModel) {
	f := barFont()
	items := barItems(m)
	rects := barItemRects(m)
	rx := scaled(barItemR)

	for i, it := range items {
		ir := rects[i]
		if i == menuHover {
			menuSurf.fillRoundRect(float64(ir.Left), float64(ir.Top),
				float64(ir.width()), float64(ir.height()), rx, th.hover)
		}

		iconX := float64(ir.Left) + scaled(barItemPadH)
		iconY := float64(ir.Top) + (float64(ir.height())-scaled(barIconSize))/2
		drawMenuIcon(menuSurf, it.icon, iconX, iconY, scaled(barIconSize), th.label)

		if it.title != "" {
			tx := iconX + scaled(barIconSize) + scaled(barIconGap)
			tw := float64(ir.Right) - scaled(barItemPadH) - tx
			menuSurf.drawText(int32(tx+0.5), ir.Top, it.title, f,
				int32(tw+0.5), ir.height(), th.label)
		}

		// 「…」（豆包那边叫 float_btn_item_setting，40x40）之前来一条竖分隔线：
		// height:32px、width:5px 里画 1px（左右各让开 2px），颜色 rgba(0,0,0,.06)。
		if it.isMore {
			dl := scaled(dividerW)
			dh := scaled(dividerH)
			dx := float64(ir.Left) - scaled(dividerGap) - dl
			dy := float64(ir.Top) + (float64(ir.height())-dh)/2
			menuSurf.fillRoundRect(dx, dy, dl, dh, 0, th.divider)
		}
	}
}

// drawMenuPanel 画「…」展开的二级面板：竖列表，图标 + 文字 + 快捷键。
func drawMenuPanel(th theme, m menuModel) {
	f := panelFont()
	fShort := shortcutFont()
	rects := panelItemRects(m)
	rx := scaled(barItemR)

	// 第一项如果是「禁用」，在它上面拉一条横分隔线：
	// 豆包的下拉菜单就是这么把"其余技能"和底部的"禁用/自定义设置"分开的
	//（.float_btn_pad .floatBtnPanelLine：height:1px + 左右各缩进 10px）。
	if len(m.more) > 0 && m.more[0].icon == iconDisable && len(rects) > 0 {
		pad := scaled(panelDividerPad)
		y := float64(rects[0].Top) - scaled(panelGap)/2
		menuSurf.fillRoundRect(float64(rects[0].Left)+pad, y,
			float64(rects[0].width())-2*pad, scaled(panelDividerH), 0, th.divider)
	}

	for i, it := range m.more {
		ir := rects[i]
		if i == menuPanelHover {
			menuSurf.fillRoundRect(float64(ir.Left), float64(ir.Top),
				float64(ir.width()), float64(ir.height()), rx, th.hover)
		}

		iconX := float64(ir.Left) + scaled(panelItemPadH)
		iconY := float64(ir.Top) + (float64(ir.height())-scaled(panelIconSize))/2
		if it.icon == iconDisable {
			// 豆包的「禁用」就是那个小叉：.floatBtnCloseIcon 是 24x24、圆角 12、
			// 里面一个 12px 的 ×，悬停时底色 rgba(0,0,0,.06)。
			drawDisableIcon(menuSurf, iconX, iconY, scaled(panelIconSize), th, i == menuPanelHover)
		} else {
			drawMenuIcon(menuSurf, it.icon, iconX, iconY, scaled(panelIconSize), th.label)
		}

		tx := iconX + scaled(panelIconSize) + scaled(panelIconGap)
		tw := float64(ir.Right) - scaled(panelItemPadH) - tx
		if it.shortcut != "" {
			sw := float64(textWidth(it.shortcut, fShort))
			sx := float64(ir.Right) - scaled(panelItemPadH) - sw
			tw -= sw + scaled(panelShortcutGap)
			menuSurf.drawText(int32(sx+0.5), ir.Top, it.shortcut, fShort,
				int32(sw+2), ir.height(), th.text4)
		}
		menuSurf.drawText(int32(tx+0.5), ir.Top, it.title, f,
			int32(tw+0.5), ir.height(), th.label)
	}
}

// drawMenuIcon 画线性菜单图标（box 是图标外框边长，15/16px 都用同一套画法）。
//
// 画法仿豆包/字节那套线性图标：统一 1.5px 线宽、圆头端点、方形网格、
// 不靠填充靠描边。但图形是照语义自己画的 —— 仿的是画法，不是把它的图标抄过来。
func drawMenuIcon(s *surface, icon int, x, y, box float64, col rgba) {
	// 优先用 emoji（用户要求）：GDI 把 emoji 画成单色轮廓，正好走我们"亮度当覆盖率"那套。
	if g, ok := iconEmoji[icon]; ok {
		drawEmojiGlyph(s, g, x, y, box, col)
		return
	}
	th := menuIconStroke * scale
	switch icon {
	case iconCopy:
		// 两张错开的圆角方框（后面的那张被前面的压掉一角，靠留白读出来）
		s.strokeRoundRect(x+box*0.34, y+box*0.04, box*0.62, box*0.62, box*0.16, th, col)
		s.strokeRoundRect(x+box*0.04, y+box*0.34, box*0.62, box*0.62, box*0.16, th, col)
	case iconSearch:
		// 放大镜
		s.strokeCircle(x+box*0.42, y+box*0.42, box*0.32, th, col)
		s.fillLine(x+box*0.68, y+box*0.68, x+box*0.96, y+box*0.96, th, col)
	case iconClose:
		s.fillLine(x+box*0.22, y+box*0.22, x+box*0.78, y+box*0.78, th, col)
		s.fillLine(x+box*0.78, y+box*0.22, x+box*0.22, y+box*0.78, th, col)
	case iconMore:
		// 三个点（横向省略号）
		r := box * 0.085
		cy := y + box*0.5
		for _, dx := range []float64{0.16, 0.5, 0.84} {
			s.fillCircle(x+box*dx, cy, r, col)
		}
	case iconEnable:
		// 对勾
		s.fillLine(x+box*0.18, y+box*0.52, x+box*0.42, y+box*0.76, th*1.1, col)
		s.fillLine(x+box*0.42, y+box*0.76, x+box*0.84, y+box*0.26, th*1.1, col)
	case iconSettings:
		// 齿轮：一个圆 + 八颗齿
		drawGearIcon(s, x, y, box, th, col)
	case iconChat:
		// 对话气泡：圆角方框 + 左下角的小尾巴
		s.strokeRoundRect(x+box*0.06, y+box*0.1, box*0.88, box*0.66, box*0.2, th, col)
		s.fillLine(x+box*0.28, y+box*0.76, x+box*0.28, y+box*0.94, th, col)
		s.fillLine(x+box*0.28, y+box*0.94, x+box*0.46, y+box*0.76, th, col)
	case iconExit:
		drawExitIcon(s, x, y, box, th, col)
	case iconTranslate:
		drawTranslateIcon(s, x, y, box, th, col)
	}
}

// iconEmoji 是"图标 → emoji"的对照表。改图标只要改这里。
//
// 只有**图标**用它；「禁用」那个小叉不走这里 —— 它是照豆包 CSS 画的
// （24x24 圆角按钮底 + 12px 的 ×），是那个插件里最有辨识度的一处。
var iconEmoji = map[int]string{
	iconCopy:      "📋", // 剪贴板
	iconTranslate: "🌐", // 地球 —— 翻译的通用符号
	iconSearch:    "🔍",
	iconChat:      "💬",
	iconKnowledge: "📚",
	iconAsk:       "✨",
	iconStop:      "🛑",
	iconSettings:  "⚙",
	iconEnable:    "✅",
	iconExit:      "🚪",
}

// drawEmojiGlyph 把一个 emoji 画进 box 大小的方框里（居中）。
//
// 字号取 box 的 1.15 倍：emoji 字形本身四周有留白，按 box 取会显得小一圈。
func drawEmojiGlyph(s *surface, glyph string, x, y, box float64, col rgba) {
	f := emojiFont(int32(box*1.15 + 0.5))
	w := textWidth(glyph, f)
	if w <= 0 {
		return
	}
	tx := x + (box-float64(w))/2
	// maxW 给实测宽度 + 2：drawText 内部用的是 dtEndEllipsis，
	// 给小了会把图标画成"…"，那就成了另一种图标了。
	s.drawText(int32(tx+0.5), int32(y), glyph, f, w+2, int32(box+0.5), col)
}

// drawDisableIcon 画「禁用」那个小叉。
//
// 数值照 .float_btn_pad .floatBtnBrand .floatBtnCloseIcon 抄：
// 24x24 的圆角按钮（radius 12），里面一个 12px 的 ×，悬停底色 rgba(0,0,0,.06)。
func drawDisableIcon(s *surface, x, y, box float64, th theme, hovered bool) {
	if hovered {
		s.fillRoundRect(x, y, box, box, scaled(12), th.hover)
	}
	// 叉臂长度按 font-size:12px 的 × 量：大约占按钮的 42%
	c := box * 0.5
	a := box * 0.25
	lw := 1.5 * scale
	col := th.label
	s.fillLine(x+c-a, y+c-a, x+c+a, y+c+a, lw, col)
	s.fillLine(x+c+a, y+c-a, x+c-a, y+c+a, lw, col)
}

// drawGearIcon 画齿轮：一个圆环 + 八颗齿。
func drawGearIcon(s *surface, x, y, box, th float64, col rgba) {
	cx, cy := x+box*0.5, y+box*0.5
	s.strokeCircle(cx, cy, box*0.3, th, col)
	s.strokeCircle(cx, cy, box*0.11, th, col)
	for i := 0; i < 8; i++ {
		a := float64(i) * math.Pi / 4
		dx, dy := math.Cos(a), math.Sin(a)
		s.fillLine(cx+dx*box*0.32, cy+dy*box*0.32,
			cx+dx*box*0.46, cy+dy*box*0.46, th, col)
	}
}

// drawExitIcon 画「退出」：一扇门 + 一支朝外的箭头。
func drawExitIcon(s *surface, x, y, box, th float64, col rgba) {
	s.strokeRoundRect(x+box*0.04, y+box*0.14, box*0.5, box*0.72, box*0.12, th, col)
	s.fillLine(x+box*0.44, y+box*0.5, x+box*0.94, y+box*0.5, th, col)
	s.fillLine(x+box*0.72, y+box*0.3, x+box*0.94, y+box*0.5, th, col)
	s.fillLine(x+box*0.94, y+box*0.5, x+box*0.72, y+box*0.7, th, col)
}

// drawTranslateIcon 用笔画画「文A」。
//
// 以前这两个字是直接拿字体画的 —— 省事，但笔画粗细、端点、基线都跟着字体走，
// 和旁边两个描边图标不是一套语言。改成手画：文 = 一横 + 一点 + 乂，A = 两条斜腿 + 一横。
func drawTranslateIcon(s *surface, x, y, box, th float64, col rgba) {
	// 左半：文
	w := box * 0.44
	cx := x + box*0.22
	s.fillLine(cx-w*0.5, y+box*0.16, cx+w*0.5, y+box*0.16, th, col)   // 亠 的横
	s.fillLine(cx, y+box*0.03, cx, y+box*0.12, th, col)               // 亠 的点
	s.fillLine(cx+w*0.38, y+box*0.34, cx-w*0.40, y+box*0.96, th, col) // 撇
	s.fillLine(cx-w*0.38, y+box*0.34, cx+w*0.40, y+box*0.96, th, col) // 捺

	// 右半：A
	ax := x + box*0.76
	aw := box * 0.42
	s.fillLine(ax-aw*0.5, y+box*0.96, ax, y+box*0.06, th, col)
	s.fillLine(ax, y+box*0.06, ax+aw*0.5, y+box*0.96, th, col)
	s.fillLine(ax-aw*0.30, y+box*0.66, ax+aw*0.30, y+box*0.66, th, col)
}

// showMenu 在主线程上显示菜单（必须在窗口所属线程调用）。
func showMenu(anchor point, model menuModel) {
	menuModel_ = model
	menuHover = -1
	menuPressed = -1
	menuExpanded = false
	menuPanelHover = -1
	menuPanelPressed = -1

	// 跟随锚点所在显示器的缩放
	if s := float64(dpiForPoint(anchor)) / 96.0; s != scale {
		scale = s
		recreateSurfaces()
	}

	renderMenu()
	w, h := menuWindowSize(model)
	menuPos = placeMenu(anchor, w, h)
	menuSurf.present(hwndMenu, menuPos.X, menuPos.Y)

	// 运行期证据：菜单显示前后，前台窗口分别是谁。
	//
	// 「不抢焦点」是本项目的硬要求（一旦抢了焦点，源程序的选区会被清掉，
	// 后续 Ctrl+C 也就复制不到东西）。静态上我们只用了 WS_EX_NOACTIVATE +
	// SWP_NOACTIVATE，从不调 SetForegroundWindow/ShowWindow —— 但那只证明
	// "没写激活代码"，证明不了运行期实际行为。这一行是量出来的。
	logForegroundAfterMenuShown()

	menuRectMu.Lock()
	menuShown = true
	menuRectMu.Unlock()
	updateMenuScreenRect()
}

// updateMenuScreenRect 刷新「菜单占了屏幕上哪块」—— 钩子线程靠它判「点在菜单外面了吗」。
//
// 展开面板之后必须重算：否则面板会落在矩形之外，用户一点面板就被当成「点在外面」，
// 菜单在点击落到面板上之前就收了。
func updateMenuScreenRect() {
	card := menuVisibleRect(menuModel_, menuExpanded)
	menuRectMu.Lock()
	menuScreenRect = rect{
		card.Left + menuPos.X, card.Top + menuPos.Y,
		card.Right + menuPos.X, card.Bottom + menuPos.Y,
	}
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
	menuPanelHover = -1
	menuPressed = -1
	menuPanelPressed = -1
	menuExpanded = false
	showWindow(hwndMenu, swHide)
}

// logForegroundAfterMenuShown 记录菜单出现后的前台窗口，用来验证「不抢焦点」。
//
// 判据：菜单显示之后，前台窗口**必须仍然是源程序**。
// 如果变成我们自己的窗口（ClickrFloatBall / ClickrSelectMenu），
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
	set := settingsScreenRect
	settingsShown := settingsOpen
	menuRectMu.Unlock()

	// 菜单 / 设置页 / 侧边栏是三块独立浮层，各自判各自的矩形
	if settingsShown && !set.contains(pt) {
		postToMain(wmCloseSettings)
	}
	if !sidebarSizedTo(pt) {
		menuRectMu.Lock()
		sbOpen := sidebarOpen
		menuRectMu.Unlock()
		if sbOpen {
			postToMain(wmCloseSidebar)
		}
	}
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
		if ballContains(screenPt) {
			return htClient
		}
		return htTransparentResult()
	}
	if hwnd == hwndMenu {
		card := menuVisibleRect(menuModel_, menuExpanded)
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

// ballContains 判断屏幕坐标是否落在悬浮球上。
//
// 判定区按**悬停放大后**的尺寸算：按原尺寸算的话，鼠标一进放大出来的那一圈
// 就会被判成出界，球和外圈之间来回抖。
func ballContains(screenPt point) bool {
	c := ballCenter()
	dx := float64(screenPt.X - c.X)
	dy := float64(screenPt.Y - c.Y)
	hr := scaled(ballSize/2 + ballHoverGrow + 1)
	return dx*dx+dy*dy <= hr*hr
}

// menuKeepsOpenAt 判断"鼠标在这个位置时，浮层该不该留着"。
//
// 球上、或菜单上 → 留着；两边都不在 → 该收。
// 这个判断放在**延时到点的那一刻**做，而不是"离开时就决定"：从球挪到菜单要跨过
// 一段不属于任何窗口的空隙（那里收不到鼠标消息），只有到点时再看一眼鼠标在哪，
// 才能把"正在往菜单挪"和"走开了"分开。
func menuKeepsOpenAt(screenPt point) bool {
	if ballContains(screenPt) {
		return true
	}
	menuRectMu.Lock()
	shown, r := menuShown, menuScreenRect
	menuRectMu.Unlock()
	return shown && r.contains(screenPt)
}

// scheduleHoverShow / scheduleHoverHide 是"悬停自动出菜单"用的两个延时。
// 定时器只把消息投回主线程，绝不在别的线程里碰窗口。
func scheduleHoverShow() {
	time.AfterFunc(time.Duration(ballHoverOpenDelay)*time.Millisecond,
		func() { postToMain(wmHoverShowMenu) })
}

func scheduleHoverHide() {
	time.AfterFunc(time.Duration(ballHoverCloseDelay)*time.Millisecond,
		func() { postToMain(wmHoverHideMenu) })
}

// hoverShowMenu 是"鼠标停在球上"到点后的处理：到点了还要再确认一次 ——
// 鼠标是否还停在球上、设置页是不是开着（开着就别再叠一层菜单）。
func hoverShowMenu() {
	menuRectMu.Lock()
	sbOpen := sidebarOpen
	menuRectMu.Unlock()
	if !ballHovered || settingsOpen || sbOpen {
		return // 设置页 / 侧边栏开着时不再叠一层菜单
	}
	menuRectMu.Lock()
	shown := menuShown
	menuRectMu.Unlock()
	if shown {
		return
	}
	showMenu(ballCenter(), ballMenuModel())
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
	iconMore
	iconDisable   // 「禁用」：那个小叉
	iconEnable    // 「启用」：对勾
	iconExit      // 退出：门 + 朝外的箭头
	iconSettings  // 自定义设置：齿轮
	iconChat      // 对话气泡（备用：目前没有项用它）
	iconKnowledge // 知识检索：书
	iconAsk       // 临时问答：问一下
	iconStop      // 停止生成
)

type menuItem struct {
	title    string
	shortcut string
	icon     int
	// isMore 标记工具条末尾那个「…」：它不是动作，是展开二级面板的开关。
	isMore bool
	run    func()
}

// menuModel 是一次弹出的内容：
//
//	items 横向工具条上的动作（图标 + 文字）
//	more  「…」展开的二级面板（竖向列表）；为空时工具条上不出现「…」
type menuModel struct {
	items []menuItem
	more  []menuItem
}

func selectionMenu(text string) menuModel {
	enabled := enabledSkills()
	var bar, rest []menuItem
	for i, sk := range enabled {
		it := menuItem{title: sk.title, icon: sk.icon, run: sk.action(text)}
		if i < maxBarSkills {
			bar = append(bar, it)
		} else {
			rest = append(rest, it)
		}
	}
	// 二级菜单的底部：豆包那边是「其余技能 + 禁用 + 自定义设置」
	rest = append(rest, moreMenuItems()...)
	return menuModel{items: bar, more: rest}
}

// skill 是一种划词技能。菜单上它就是「图标 + 文字」的一项。
type skill struct {
	id    string
	title string
	icon  int
	// action 拿到选中的文字，返回这一项被点时要干的事。
	action func(text string) func()
}

// skills 是全部技能。**加一项技能 = 往这个切片里加一行**：
// 顺序就是工具条上的顺序，超过 maxBarSkills 的自动落进二级菜单；
// 用户在设置页里关掉的那几个不进菜单（见 settings.go）。
func skills() []skill {
	return []skill{
		{id: "copy", title: "复制", icon: iconCopy, action: func(text string) func() {
			return func() {
				if err := setClipboardText(text); err != nil {
					log.Printf("[action] 复制失败: %v", err)
				}
			}
		}},
		{id: "translate", title: "翻译", icon: iconTranslate, action: func(text string) func() {
			u := "https://dict.youdao.com/result?word=" + url.QueryEscape(text) + "&lang=en"
			return func() { shellOpen(u) }
		}},
		{id: "search", title: "AI 搜索", icon: iconSearch, action: func(text string) func() {
			u := "https://www.bing.com/search?q=" + url.QueryEscape(text)
			return func() { shellOpen(u) }
		}},
		// id 保持 "ask" 不改：它写在用户的 skills.txt 里，改 id 会让那份配置失配
		//（认不出来的 id 会被当成"这个技能是关的"）。
		{id: "ask", title: "知识检索", icon: iconKnowledge, action: func(text string) func() {
			// 一次点击就到位：直接拿选中的文字去百科检索。
			// 不再"复制 + 打开对话页让用户自己粘贴"—— 那不叫检索，叫搬运。
			u := "https://baike.baidu.com/search?word=" + url.QueryEscape(text)
			return func() { shellOpen(u) }
		}},
	}
}

// enabledSkills 按注册顺序返回**开启的**技能。
//
// 没有任何配置（或配置里一个 id 都认不出来）时全部开启 —— 默认值必须是"全都能用"。
func enabledSkills() []skill {
	on := map[string]bool{}
	for _, id := range enabledSkillIDsCached() {
		on[id] = true
	}
	all := skills()
	if len(on) == 0 {
		return all
	}
	var out []skill
	for _, sk := range all {
		if on[sk.id] {
			out = append(out, sk)
		}
	}
	if len(out) == 0 {
		return all // 全关掉等于没有工具栏，退回全开
	}
	return out
}

// enabledSkillIDs 返回开启的技能 id（设置页与持久化用）。
func enabledSkillIDs() []string {
	var ids []string
	for _, sk := range enabledSkills() {
		ids = append(ids, sk.id)
	}
	return ids
}

// moreMenuItems 是工具条末尾「…」那一层的内容。
//
// 想加动作就往这个切片里加一行 —— 工具条上的「…」会跟着出现/消失
// （more 为空时「…」不画：点开是空的按钮比没有更糟）。
//
// 二级面板是竖向列表，宽度跟工具条对齐，因此放得下图标 + 文字 + 快捷键。
// moreMenuItems 是工具条末尾「…」那一层的内容：技能之外的固定项。
//
// 豆包的下拉菜单底部也是这两项（禁用 / 自定义设置）。
// 悬浮球的菜单在这之上再加一个「退出」—— 因为球没有右键菜单了，退出得有个去处。
func moreMenuItems() []menuItem {
	return []menuItem{
		togglePopupItem(),
		settingsItem(),
	}
}

// settingsItem 打开设置页。**它必须常驻**：禁用之后划词条不再出现，
// 球菜单是唯一入口，把设置项也一起藏掉就成了死胡同（评审抓到过）。
func settingsItem() menuItem {
	return menuItem{title: settingsTitle, icon: iconSettings, run: func() {
		postToMain(wmOpenSettings)
	}}
}

// exitItem 退出程序。
func exitItem() menuItem {
	return menuItem{title: "退出", icon: iconExit, run: func() {
		postToMain(wmQuitApp)
	}}
}

// 菜单文案。照豆包插件的下拉菜单用词：禁用 / 自定义设置。
const (
	disableTitle  = "禁用"
	enableTitle   = "启用"
	settingsTitle = "自定义设置"

	// 豆包：「前 4 个技能将显示在工具栏上，其他的将隐藏在下拉菜单中」
	maxBarSkills = 4
)

// shellOpen 用系统默认程序打开一个 URL 或路径。
func shellOpen(target string) {
	op := utf16Ptr("open")
	pShellExecuteW.Call(0, uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(utf16Ptr(target))), 0, 0, swShownormal)
}
