//go:build windows

// sidebar.go —— 右侧边栏：左键单击悬浮球弹出。
//
// 框架照豆包插件侧边栏抄（static/css/side_panel.css + side_panel.html）：
//
//	.skeleton-header { margin:14px 15px 6px 0 }       ← 顶部一行，右边放图标按钮（18x18）
//	.skeleton-content { padding:0 20px }              ← 内容左右各留 20
//	.skeleton-list-item { height:40px; border-radius:12px }
//	.skeleton-input  { border:1px solid rgba(0,0,0,.08); margin:0 12px 20px }
//
// 豆包网页版聊天输入框的规格（从它的 CSS token 里量的）本来也在这儿：
// `--input-guidance-input-container-{radius:20px, padding:12px, border:1px solid rgba(0,0,0,.12)}`。
//
// **但那个输入框我们没画**：本项目是"绝不抢焦点"的分层窗口（D8），拿不到键盘焦点，
// 也就没有输入法。画一个点不动的输入框是假可供性 —— 评审刚抓过这类问题。
// 侧边栏因此做成**只读**的：看当前选中了什么、对它做动作、看开关状态。
// 见 D16：AI 问答那条路要接的话，得先决定"输入从哪来"。

package main

import (
	"log"
)

// 侧边栏几何（CSS px，最终乘 scale）
const (
	sidebarW      = 360.0
	sidebarMargin = 12.0 // 离屏幕右缘与上下的留白
	sidebarR      = 12.0

	sidebarHeadH   = 44.0 // 顶部标题行
	sidebarCardR   = 10.0
	sidebarRowH    = 44.0 // 动作行
	sidebarPad     = 20.0 // 内容左右留白（豆包 .skeleton-content padding:0 20px）
	sidebarRowR    = 12.0 // 行圆角（豆包 .skeleton-list-item border-radius:12px）
	sidebarIcon    = 20.0
	sidebarTitlePx = 15.0
	sidebarBodyPx  = 13.0
	sidebarHintPx  = 12.0
)

// sidebarHit 的返回值：-1 什么都没命中，>= 0 是第几个动作行，hitSidebarClose 是关闭按钮。
const (
	hitSidebarClose = -2
)

// sidebarActions 是侧边栏里那几行动作 —— 直接来自技能表，
// 所以"设置页里关掉的技能"在这里也不会出现，两边永远一致。
func sidebarActions() []menuItem {
	text := getLastSelection()
	st := aiNow()
	items := make([]menuItem, 0, len(skills())+2)

	// 第一条：临时问答。生成中时它就是「停止」—— 同一行换个身份，
	// 不另加一行，也不留一个"点了没反应"的按钮。
	switch {
	case st.Busy:
		items = append(items, menuItem{title: "停止", icon: iconStop, run: func() {
			aiStop()
			requestAIRepaint()
		}})
	case text != "":
		items = append(items, menuItem{title: "问一下", icon: iconAsk, run: func() {
			go aiAsk(text)
		}})
	}

	for _, sk := range enabledSkills() {
		it := menuItem{title: sk.title, icon: sk.icon, run: sk.action(text)}
		if text == "" {
			it.run = func() { log.Printf("[sidebar] 还没选中内容，动作不执行") }
		}
		items = append(items, it)
	}
	items = append(items, settingsItem())
	return items
}

// ================================================================ 几何

// sidebarCardRect 是侧边栏**卡片**在屏幕上的矩形。
//
// 位置（placeSidebar）、窗口尺寸、钩子判"点在不在浮层上"三处都从它推导 ——
// 以前这里浮点留白与取整留白混着用（scaledI(pad) 与 scaled(pad)），
// 在 125% 缩放下会差 1px（测试直接抓到了）。
func sidebarCardRect() rect {
	wa := workArea(point{0, 0})
	margin := int32(scaled(sidebarMargin))
	return rect{
		Left:   wa.Right - int32(scaled(sidebarW)) - margin,
		Top:    wa.Top + margin,
		Right:  wa.Right - margin,
		Bottom: wa.Bottom - margin,
	}
}

func sidebarWindowSize() (int32, int32) {
	card := sidebarCardRect()
	pad := int32(scaled(settingsShadowPad()))
	return card.width() + 2*pad, card.height() + 2*pad
}

// placeSidebar 返回窗口左上角（卡片左上角再往左上退一个阴影留白）。
func placeSidebar() point {
	card := sidebarCardRect()
	pad := int32(scaled(settingsShadowPad()))
	return point{card.Left - pad, card.Top - pad}
}

// sidebarCardWidth 是卡片宽度（CSS/设备像素口径与绘制一致）。
func sidebarCardWidth() float64 { return float64(sidebarCardRect().width()) }

// sidebarContentX 是内容区左缘（窗口坐标系）。
func sidebarContentX() float64 { return scaled(settingsShadowPad()) + scaled(sidebarPad) }

func sidebarCloseRect() rect {
	pad := scaled(settingsShadowPad())
	cardW := sidebarCardWidth()
	b := scaled(closeBtnBox)
	x := pad + cardW - scaled(15) - b
	y := pad + scaled(14) // 豆包 .skeleton-header margin:14px 15px 6px 0
	return rect{int32(x), int32(y), int32(x + b), int32(y + b)}
}

// sidebarAnswerRect 是答案区（在"当前选中"卡片和动作行之间，占满剩余高度）。
//
// 有答案/在生成/有错时就画在这一块里，并且**贴着底部对齐**（像聊天窗那样，
// 新内容在下面），超出部分用滚轮翻。
func sidebarAnswerRect() rect {
	pad := scaled(settingsShadowPad())
	x := pad + scaled(sidebarPad)
	w := sidebarCardWidth() - 2*scaled(sidebarPad)
	top := pad + scaled(sidebarHeadH) + sidebarSummaryHeight()
	// 动作行与底部一共要占多少，倒推出答案区的下边界
	acts := float64(len(sidebarActions())) * (scaled(sidebarRowH) + scaled(8))
	bottom := pad + sidebarCardHeight() - scaled(sidebarFooterH) - acts - scaled(8)
	if bottom < top+scaled(60) {
		bottom = top + scaled(60) // 挤到不行也要留一块（宁可盖住动作行一点）
	}
	return rect{int32(x), int32(top), int32(x + w), int32(bottom)}
}

// sidebarFooterH 是底部那块（分隔线 + 两行说明）的高度。
const sidebarFooterH = 76.0

// sidebarCardHeight 是侧边栏卡片的高度（设备像素口径）。
func sidebarCardHeight() float64 { return float64(sidebarCardRect().height()) }

// sidebarActionRects 返回动作行在窗口坐标系里的矩形（从"选中内容"卡片下面开始）。
func sidebarActionRects() []rect {
	pad := scaled(settingsShadowPad())
	x := pad + scaled(sidebarPad)
	w := sidebarCardWidth() - 2*scaled(sidebarPad)
	y := float64(sidebarAnswerRect().Bottom) + scaled(8)
	out := make([]rect, 0, len(sidebarActions()))
	for range sidebarActions() {
		out = append(out, rect{int32(x), int32(y), int32(x + w), int32(y + scaled(sidebarRowH))})
		y += scaled(sidebarRowH) + scaled(8)
	}
	return out
}

// sidebarSummaryHeight 是"当前选中"那块卡片的高度（按文字行数算）。
func sidebarSummaryHeight() float64 {
	lines := float64(len(sidebarSummaryLines()))
	if lines > 4 {
		lines = 4
	}
	if lines == 0 {
		lines = 1
	}
	return scaled(sidebarBodyPx)*1.6*lines + 2*scaled(12) + scaled(24) // 内边距 + 小标题
}

// sidebarHit 判断鼠标在侧边栏里的位置。
func sidebarHit(pt point) int {
	if sidebarCloseRect().contains(pt) {
		return hitSidebarClose
	}
	for i, r := range sidebarActionRects() {
		if r.contains(pt) {
			return i
		}
	}
	return -1
}

// ================================================================ 文字换行

// wrapText 按像素宽度贪心折行，最多 maxLines 行（最后一行放不下就省略）。
//
// 我们的绘制层没有排版能力（drawText 是单行 + 省略号），所以换行得自己算。
// 中文按 rune 切就行；这样也顺带避免了"在字中间断"的问题。
func wrapText(text string, maxW float64, maxLines int, hf uintptr) []string {
	if text == "" || maxW <= 0 {
		return nil
	}
	runes := []rune(text)
	var lines []string
	var cur []rune
	for i := 0; i < len(runes); i++ {
		cur = append(cur, runes[i])
		if float64(textWidth(string(cur), hf)) > maxW {
			// 放不下了：把最后一个字退回去，另起一行
			cur = cur[:len(cur)-1]
			if len(cur) == 0 { // 单字就超宽（窗口太窄），只能硬放
				cur = append(cur, runes[i])
			} else {
				i--
			}
			lines = append(lines, string(cur))
			cur = nil
			if len(lines) == maxLines {
				return lines
			}
		}
	}
	if len(cur) > 0 && len(lines) < maxLines {
		lines = append(lines, string(cur))
	}
	// 内容还没放完：最后一行加省略号
	if len(lines) == maxLines {
		used := 0
		for _, l := range lines {
			used += len([]rune(l))
		}
		if used < len(runes) {
			last := []rune(lines[maxLines-1])
			for len(last) > 1 && float64(textWidth(string(last)+"…", hf)) > maxW {
				last = last[:len(last)-1]
			}
			lines[maxLines-1] = string(last) + "…"
		}
	}
	return lines
}

func sidebarSummaryLines() []string {
	f := font(scaledI(sidebarBodyPx), fwNormal)
	return wrapText(getLastSelection(), sidebarCardWidth()-2*scaled(sidebarPad)-2*scaled(12), 4, f)
}

// ================================================================ 绘制

func renderSidebar() {
	th := loadTheme()
	w, h := sidebarWindowSize()
	if sidebarSurf != nil && (sidebarSurf.w != w || sidebarSurf.h != h) {
		sidebarSurf.free()
		sidebarSurf = nil
	}
	if sidebarSurf == nil {
		sidebarSurf = newSurface(w, h)
	}
	sidebarSurf.clear()

	pad := scaled(settingsShadowPad())
	cardW, cardH := sidebarCardWidth(), float64(h)-2*pad
	radius := scaled(sidebarR)

	for _, l := range menuShadowLayers {
		sidebarSurf.shadowRoundRect(pad, pad, cardW, cardH, radius, scaled(l.blur), scaled(l.dy), l.c)
	}
	sidebarSurf.fillRoundRect(pad, pad, cardW, cardH, radius, th.cardBg)

	// 顶部标题行：左边标题、右边关闭（豆包 .skeleton-header 就是"右边放图标"）
	head := rect{int32(pad), int32(pad), int32(pad + cardW), int32(pad + scaled(sidebarHeadH))}
	sidebarSurf.drawText(head.Left+int32(scaled(sidebarPad)), head.Top, "划词助手",
		font(scaledI(sidebarTitlePx), fwSemiBold),
		head.width()-int32(scaled(sidebarPad+40)), head.height(), th.bodyText)

	cr := sidebarCloseRect()
	if sidebarHover == hitSidebarClose {
		sidebarSurf.fillRoundRect(float64(cr.Left), float64(cr.Top),
			float64(cr.width()), float64(cr.height()), float64(cr.width())/2, th.hover)
	}
	drawDisableIcon(sidebarSurf, float64(cr.Left)+scaled(4), float64(cr.Top)+scaled(4),
		scaled(16), th, false)

	drawSidebarSummary(th, pad)
	drawSidebarAnswer(th)
	drawSidebarActions(th)
	drawSidebarFooter(th, pad, cardW, cardH)
}

// drawSidebarSummary 画"当前选中"那张卡片。没有选中内容时给一句怎么用。
func drawSidebarSummary(th theme, pad float64) {
	x := pad + scaled(sidebarPad)
	w := sidebarCardWidth() - 2*scaled(sidebarPad)
	y := pad + scaled(sidebarHeadH)
	h := sidebarSummaryHeight() - scaled(8)
	r := rect{int32(x), int32(y), int32(x + w), int32(y + h)}

	sidebarSurf.fillRoundRect(x, y, w, h, scaled(sidebarCardR), th.blockBg)
	sidebarSurf.strokeRoundRect(x+0.5, y+0.5, w-1, h-1, scaled(sidebarCardR), scaled(1), th.blockBorder)

	fHint := font(scaledI(sidebarHintPx), fwNormal)
	sidebarSurf.drawText(r.Left+int32(scaled(12)), r.Top+int32(scaled(6)), "当前选中", fHint,
		r.width()-int32(scaled(24)), int32(scaled(18)), th.text4)

	fBody := font(scaledI(sidebarBodyPx), fwNormal)
	lines := sidebarSummaryLines()
	lineH := int32(scaled(sidebarBodyPx) * 1.6)
	if len(lines) == 0 {
		sidebarSurf.drawText(r.Left+int32(scaled(12)), r.Top+int32(scaled(26)),
			"还没有选中内容 —— 在任意程序里划一段文字，这里就能对它动手",
			fBody, r.width()-int32(scaled(24)), int32(scaled(18))*2, th.text4)
		return
	}
	for i, ln := range lines {
		sidebarSurf.drawText(r.Left+int32(scaled(12)), r.Top+int32(scaled(26))+int32(i)*lineH,
			ln, fBody, r.width()-int32(scaled(24)), lineH, th.bodyText)
	}
}

// drawSidebarAnswer 画问答区。
//
// 三种状态各画各的：没问过（只有提示）、生成中（已到的部分 + 省略号）、有错（把错误原文摆出来）。
// 文本**贴着底部对齐**（像聊天窗那样新的在下面），内容比框高就用滚轮翻。
func drawSidebarAnswer(th theme) {
	r := sidebarAnswerRect()
	st := aiNow()
	fBody := font(scaledI(sidebarBodyPx), fwNormal)
	fHint := font(scaledI(sidebarHintPx), fwNormal)

	// 没有内容时不画卡片，省得空一块灰底
	if st.Question == "" && st.Answer == "" && st.Err == "" {
		sidebarSurf.drawText(r.Left, r.Top, "临时问答：选中一段文字，点下面的「问一下」",
			fHint, r.width(), r.height(), th.text4)
		return
	}

	sidebarSurf.fillRoundRect(float64(r.Left), float64(r.Top),
		float64(r.width()), float64(r.height()), scaled(sidebarCardR), th.blockBg)
	sidebarSurf.strokeRoundRect(float64(r.Left)+0.5, float64(r.Top)+0.5,
		float64(r.width())-1, float64(r.height())-1, scaled(sidebarCardR), scaled(1), th.blockBorder)

	innerX := float64(r.Left) + scaled(12)
	innerW := float64(r.width()) - 2*scaled(12)
	lineH := int32(scaled(sidebarBodyPx) * 1.7)

	// 问：最多两行，免得答案没地方
	qLines := wrapText("问："+st.Question, innerW, 2, fBody)
	y := float64(r.Top) + scaled(10)
	for _, ln := range qLines {
		sidebarSurf.drawText(int32(innerX), int32(y), ln, fBody, int32(innerW), lineH, th.text4)
		y += float64(lineH)
	}

	// 答：正文，或错误原文（错误用次要色，但整段都画出来，不吞）
	body := st.Answer
	if st.Err != "" {
		body = "⚠ " + st.Err
	} else if st.Busy {
		body += "▌"
	}
	avail := float64(r.Bottom) - y - scaled(10)
	maxLines := int(avail / float64(lineH))
	if maxLines < 1 {
		maxLines = 1
	}
	aLines := wrapText(body, innerW, maxLines+sidebarScroll, fBody)
	// 贴底显示：只画最后 maxLines 行（scroll 往上翻）
	if len(aLines) > maxLines {
		aLines = aLines[len(aLines)-maxLines:]
	}
	for _, ln := range aLines {
		sidebarSurf.drawText(int32(innerX), int32(y), ln, fBody, int32(innerW), lineH, th.bodyText)
		y += float64(lineH)
	}
}

// drawSidebarActions 画动作行：图标 + 名称 + 右侧一句说明。
func drawSidebarActions(th theme) {
	f := font(scaledI(sidebarTitlePx), fwNormal)
	fHint := font(scaledI(sidebarHintPx), fwNormal)
	for i, it := range sidebarActions() {
		r := sidebarActionRects()[i]
		if i == sidebarHover {
			sidebarSurf.fillRoundRect(float64(r.Left), float64(r.Top),
				float64(r.width()), float64(r.height()), scaled(sidebarRowR), th.hover)
		}
		iconX := float64(r.Left) + scaled(12)
		iconY := float64(r.Top) + (float64(r.height())-scaled(sidebarIcon))/2
		drawMenuIcon(sidebarSurf, it.icon, iconX, iconY, scaled(sidebarIcon), th.label)
		tx := int32(iconX + scaled(sidebarIcon) + scaled(10))
		sidebarSurf.drawText(tx, r.Top, it.title, f,
			r.width()-int32(scaled(120)), r.height(), th.bodyText)
		if d := skillHint(it.icon); d != "" {
			dw := textWidth(d, fHint)
			sidebarSurf.drawText(r.Right-int32(scaled(12))-dw, r.Top, d, fHint,
				dw+2, r.height(), th.text4)
		}
	}
}

// skillHint 是动作行右边那句说明。
func skillHint(icon int) string {
	switch icon {
	case iconCopy:
		return "剪贴板"
	case iconTranslate:
		return "浏览器"
	case iconSearch:
		return "浏览器"
	case iconKnowledge:
		return "百科"
	case iconSettings:
		return "开关与技能"
	}
	return ""
}

// drawSidebarFooter 画底部：一条分隔线 + 状态 + 一句说明。
func drawSidebarFooter(th theme, pad, cardW, cardH float64) {
	rects := sidebarActionRects()
	y := cardH - scaled(64)
	if len(rects) > 0 && float64(rects[len(rects)-1].Bottom) > y-scaled(12) {
		y = float64(rects[len(rects)-1].Bottom) + scaled(12)
	}
	x := pad + scaled(sidebarPad)
	w := cardW - 2*scaled(sidebarPad)
	sidebarSurf.fillRoundRect(x, y, w, scaled(1), 0, th.divider)

	state := "划词工具栏：已启用"
	if !selectionPopupEnabled() {
		state = "划词工具栏：已禁用（鼠标停到球上可重新启用）"
	}
	fHint := font(scaledI(sidebarHintPx), fwNormal)
	sidebarSurf.drawText(int32(x), int32(y+scaled(10)), state, fHint,
		int32(w), int32(scaled(18)), th.text4)
	sidebarSurf.drawText(int32(x), int32(y+scaled(30)),
		truncate(describeAIStatus(), 46), fHint,
		int32(w), int32(scaled(18)), th.text4)
}

// ================================================================ 显示 / 隐藏 / 交互

func openSidebar() {
	hideMenu() // 一次只留一个浮层
	if settingsOpen {
		closeSettings()
	}

	sidebarPos = placeSidebar()
	sidebarHover = -1 // 先复位再画（同上）
	sidebarScroll = 0
	renderSidebar()
	sidebarSurf.present(hwndSidebar, sidebarPos.X, sidebarPos.Y)

	menuRectMu.Lock()
	sidebarScreenRect = sidebarCardRect()
	sidebarOpen = true
	menuRectMu.Unlock()
	w, h := sidebarWindowSize()
	log.Printf("[sidebar] 打开侧边栏 @(%d,%d) %dx%d", sidebarPos.X, sidebarPos.Y, w, h)
}

func closeSidebar() {
	menuRectMu.Lock()
	was := sidebarOpen
	sidebarOpen = false
	menuRectMu.Unlock()
	if !was {
		return
	}
	// 关掉侧边栏就把在跑的问答停掉：它是"临时问答"，没有"后台继续生成"这回事
	aiStop()
	sidebarHover = -1
	showWindow(hwndSidebar, swHide)
}

// sidebarClick 处理侧边栏里的一次点击。
func sidebarClick(pt point) {
	idx := sidebarHit(pt)
	if idx == hitSidebarClose {
		closeSidebar()
		return
	}
	acts := sidebarActions()
	if idx < 0 || idx >= len(acts) {
		return
	}
	it := acts[idx]
	if it.run != nil {
		go it.run()
	}
	// 技能动作跑完把侧边栏收掉（它挡着右侧一大块屏幕）；设置是切换，不关
	if it.icon != iconSettings {
		closeSidebar()
	}
}

// sidebarSizedTo 判断一个屏幕点是否落在侧边栏上（钩子线程用）。
func sidebarSizedTo(pt point) bool {
	menuRectMu.Lock()
	open, r := sidebarOpen, sidebarScreenRect
	menuRectMu.Unlock()
	return open && r.contains(pt)
}

// sidebarWheel 处理滚轮：上下翻答案。
//
// 返回 true 表示"这一下滚轮我用了"，调用方不必再管。
func sidebarWheel(delta int) bool {
	if delta == 0 {
		return false
	}
	step := 2
	if delta > 0 {
		sidebarScroll += step
	} else {
		sidebarScroll -= step
		if sidebarScroll < 0 {
			sidebarScroll = 0
		}
	}
	if sidebarScroll > 500 {
		sidebarScroll = 500 // 上限：别让它无限涨
	}
	sidebarRepaint()
	return true
}

// 侧边栏重画用的小工具：鼠标悬停变化时调用。
func sidebarRepaint() {
	renderSidebar()
	sidebarSurf.present(hwndSidebar, sidebarPos.X, sidebarPos.Y)
}
