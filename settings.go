//go:build windows

// settings.go —— 设置页：绘制 + 交互。
//
// 框架是照豆包插件的设置页抄的（static/css/options.css）：
//
//	.optionsPage          { margin:auto }
//	.sider                { padding:40px 40px 40px 0 }
//	.sider .logo          { height:32px; margin-bottom:12px; padding-left:20px }
//	.sider .nav-item      { border-radius:12px; font-size:16px; font-weight:600; height:48px }
//	.sider .nav-item-selected { background:var(--primary-transparent-2) }
//	.content              { border-left:1px solid rgba(0,0,0,.12) }
//	.content .section     { padding:40px 0; width:100% }
//	.block-container      { padding:16px 0 }
//	.block-content        { background:#f9fafb; border:1px solid rgba(0,0,0,.08); border-radius:16px }
//	.global-switch        { height:56px; padding:0 16px; justify-content:space-between }
//	.menuSorterContentContainer { height:48px; border-radius:12px; border:1px solid #e6eaed; background:#f9fafb }
//	.menuSorterItem .name { font-size:16px; font-weight:500; letter-spacing:-.02em; line-height:20px; margin-left:9px }
//	.close（弹窗那套）      { border-radius:50%; height:24px; width:24px; right:14px; top:14px }
//
// 只有**具体放哪些项**是我定的，而且全在 settingsRows() 一个切片里 ——
// 要加/删/改项就动那一处，绘制与命中测试都是按它跑的。
//
// 颜色不用豆包的品牌蓝，用本项目的鸢尾蓝（色调不复制）。

package main

import (
	"log"
	"path/filepath"
	"sync"
)

// 设置窗口的几何（单位是 CSS px，最终都乘 scale）。
const (
	settingsW = 720.0
	settingsH = 500.0
	settingsR = 12.0 // 卡片圆角

	siderW      = 200.0
	siderPadX   = 20.0
	siderPadY   = 40.0
	navItemH    = 48.0
	navItemR    = 12.0
	contentPadX = 40.0
	contentPadY = 32.0

	pageTitlePx = 20.0
	navFontPx   = 16.0
	rowTitlePx  = 16.0
	rowSubPx    = 12.0
	sectionPx   = 13.0
	hintPx      = 12.0

	switchW = 40.0
	switchH = 24.0
	switchR = 12.0

	closeBtnBox = 24.0 // 右上角关闭按钮（豆包弹窗那套：24x24 圆形）
)

// settingsRowKind 是设置页里的一行是什么。
type settingsRowKind int

const (
	rowSection settingsRowKind = iota // 分组标题
	rowHint                           // 灰色说明
	rowToggle                         // 一行 + 右侧开关
	rowSkill                          // 一个技能 + 右侧开关（豆包的技能管理行）
	rowAction                         // 一行 + 右侧箭头（点了干一件事）
	rowField                          // 一行 + 右侧一个**原生输入框**（见 settings_fields.go）
)

// settingsRow 是设置页的一行。
//
// 加一项设置 = 往 settingsRows() 里加一个结构体，别的都不用动。
type settingsRow struct {
	kind  settingsRowKind
	title string
	sub   string
	icon  int
	// on/off 只在 rowToggle / rowSkill 上有效；set 负责真正改状态（含持久化）。
	on  func() bool
	set func(bool)
	// run 只在 rowAction 上有效。
	run func()
	// fieldKey 只在 rowField 上有效：对应 ai.txt 里的键名（base_url / api_key / model）。
	fieldKey string
}

// settingsRows 是设置页的全部内容。
func settingsRows() []settingsRow {
	rows := []settingsRow{
		{kind: rowSection, title: "显示"},
		{kind: rowToggle, title: "当选中文本时显示工具栏",
			on: selectionPopupEnabled, set: func(v bool) {
				setSelectionPopup(v)
				savePopupEnabled(v)
				postToMain(wmRefreshBall)
				log.Printf("[settings] 「划词工具栏」→ %s", onOff(v))
			}},
		{kind: rowSection, title: "AI 划词技能"},
		{kind: rowHint, title: "前 4 个技能将显示在工具栏上，其他的将隐藏在下拉菜单中"},
	}
	for _, sk := range skills() {
		sk := sk
		rows = append(rows, settingsRow{
			kind: rowSkill, title: sk.title, icon: sk.icon,
			on:  func() bool { return skillEnabled(sk.id) },
			set: func(v bool) { setSkillEnabled(sk.id, v) },
		})
	}
	rows = append(rows,
		settingsRow{kind: rowSection, title: "临时问答（AI）"},
		settingsRow{kind: rowField, title: "接口地址", fieldKey: "base_url",
			sub: "OpenAI 兼容；DeepSeek 用 https://api.deepseek.com/v1"},
		settingsRow{kind: rowField, title: "API Key", fieldKey: "api_key",
			sub: "只存在本机 ai.txt，不会写进日志"},
		settingsRow{kind: rowField, title: "模型", fieldKey: "model",
			sub: "DeepSeek：deepseek-flash / deepseek-v4-pro"},
		settingsRow{kind: rowAction, title: "保存", sub: "写入 ai.txt",
			run: func() { saveAIConfigFromForm() }},
		settingsRow{kind: rowAction, title: "测试连接", sub: "",
			run: func() { testAIConnection() }},

		settingsRow{kind: rowSection, title: "其他"},
		settingsRow{kind: rowAction, title: "打开 AI 配置文件", sub: "ai.txt（填 api_key）",
			run: func() { openAIConfig() }},
		settingsRow{kind: rowAction, title: "打开日志目录", sub: logDirHint(),
			// 打开的是**目录**不是文件：文案写的就是目录（评审指出过文案与行为不符）
			run: func() { shellOpen(filepath.Dir(logFilePath())) }},
	)
	return rows
}

func logDirHint() string {
	if p := logFilePath(); p != "" {
		return p
	}
	return "%LocalAppData%\\clickr\\clickr.log"
}

// skillEnabled 判断某个技能是否开着。
func skillEnabled(id string) bool {
	for _, on := range enabledSkillIDs() {
		if on == id {
			return true
		}
	}
	return false
}

// setSkillEnabled 开关一个技能并落盘。
func setSkillEnabled(id string, on bool) {
	cur := enabledSkillIDs()
	next := make([]string, 0, len(cur)+1)
	for _, c := range cur {
		if c != id {
			next = append(next, c)
		}
	}
	if on {
		// 保持注册顺序：重新按 skills() 的顺序过滤一遍
		want := map[string]bool{id: true}
		for _, c := range next {
			want[c] = true
		}
		next = next[:0]
		for _, sk := range skills() {
			if want[sk.id] {
				next = append(next, sk.id)
			}
		}
	}
	setSkillEnabledIDs(next)
	log.Printf("[settings] 技能 %q → %s（工具栏现在：%v）", id, onOff(on), enabledSkillIDs())
}

// ================================================================ 几何

// settingsRowRects 把 settingsRows() 变成窗口坐标系里的矩形列表。
// 绘制与命中测试都走它，两边不会不一致。
func settingsRowRects() []rect {
	pad := scaled(settingsShadowPad())
	x := pad + scaled(siderW) + scaled(contentPadX)
	w := scaled(settingsW) - scaled(siderW) - 2*scaled(contentPadX)
	y := pad + scaled(contentPadY)
	out := make([]rect, 0, len(settingsRows()))
	for _, r := range settingsRows() {
		var h float64
		switch r.kind {
		case rowSection:
			h = 40
		case rowHint:
			h = 26
		case rowToggle:
			h = 56 // 豆包 .global-switch { height:56px }
		case rowField:
			h = 44 // 输入框那一行
		case rowSkill, rowAction:
			h = 48 // 豆包 .menuSorterContentContainer { height:48px }
		}
		out = append(out, rect{int32(x), int32(y), int32(x + w), int32(y + scaled(h))})
		y += scaled(h)
	}
	return out
}

func settingsRowIndexAt(pt point) int {
	for i, r := range settingsRowRects() {
		if r.contains(pt) {
			return i
		}
	}
	return -1
}

// settingsSwitchRect 返回某一行右侧开关的矩形。
func settingsSwitchRect(r rect) rect {
	sw, sh := scaled(switchW), scaled(switchH)
	x := float64(r.Right) - scaled(16) - sw
	y := float64(r.Top) + (float64(r.height())-sh)/2
	return rect{int32(x), int32(y), int32(x + sw), int32(y + sh)}
}

// settingsCloseRect 是右上角那个关闭按钮。
func settingsCloseRect() rect {
	pad := scaled(settingsShadowPad())
	b := scaled(closeBtnBox)
	x := pad + scaled(settingsW) - scaled(14) - b
	y := pad + scaled(14)
	return rect{int32(x), int32(y), int32(x + b), int32(y + b)}
}

// settingsContentHeight 是"标题 + 所有行 + 底部状态"实际需要的高度。
//
// 卡片高度取它和 settingsH 的较大者 —— 以前写死 500，行一多底部那句
// 「已启用/已关闭」就画到卡片外面、贴在阴影上了（评审按像素抓到过）。
func settingsContentHeight() float64 {
	h := contentPadY + 36 // 页标题那一块
	rects := settingsRowRects()
	if len(rects) > 0 {
		h = float64(rects[len(rects)-1].Bottom-int32(settingsShadowPad())) / scale
	}
	return h + 16 + 1 + 12 + 20 + contentPadY // 分隔线 + 状态文案 + 下边距
}

func settingsCardHeight() float64 {
	if need := settingsContentHeight(); need > settingsH {
		return need
	}
	return settingsH
}

func settingsWindowSize() (int32, int32) {
	pad := scaled(settingsShadowPad())
	return int32(scaled(settingsW) + 2*pad + 0.5),
		int32(scaled(settingsCardHeight()) + 2*pad + 0.5)
}

// settingsShadowPad 用菜单那套投影，留白走同一份计算（见 D10）。
func settingsShadowPad() float64 { return shadowPadOf(menuShadowLayers) }

// ================================================================ 绘制

func renderSettings() {
	th := loadTheme()
	w, h := settingsWindowSize()
	if settingsSurf != nil && (settingsSurf.w != w || settingsSurf.h != h) {
		settingsSurf.free()
		settingsSurf = nil
	}
	if settingsSurf == nil {
		settingsSurf = newSurface(w, h)
	}
	settingsSurf.clear()

	pad := scaled(settingsShadowPad())
	cardW, cardH := scaled(settingsW), scaled(settingsCardHeight())
	radius := scaled(settingsR)

	for _, l := range menuShadowLayers {
		settingsSurf.shadowRoundRect(pad, pad, cardW, cardH, radius,
			scaled(l.blur), scaled(l.dy), l.c)
	}
	settingsSurf.fillRoundRect(pad, pad, cardW, cardH, radius, th.cardBg)

	// 左栏 / 内容区之间的那条竖线：豆包是 border-left:1px solid rgba(0,0,0,.12)
	lineX := pad + scaled(siderW)
	settingsSurf.fillRoundRect(lineX, pad, scaled(1), cardH, 0, th.divider)

	drawSettingsSider(th, pad)
	drawSettingsContent(th, pad)

	// 右上角关闭按钮（豆包弹窗那套：24x24 圆、悬停 rgba(0,0,0,.06)）
	cr := settingsCloseRect()
	// settingsHover == -2 就是"指针在关闭按钮上"（settingsHit 的返回值），
	// 以前这个字段只被置 false，叉永远不亮 —— 全页唯一没有悬停反馈的可点元素。
	if settingsHover == hitCloseButton {
		settingsSurf.fillRoundRect(float64(cr.Left), float64(cr.Top),
			float64(cr.width()), float64(cr.height()), float64(cr.width())/2, th.hover)
	}
	drawDisableIcon(settingsSurf, float64(cr.Left)+scaled(4), float64(cr.Top)+scaled(4),
		scaled(16), th, false)
}

// drawSettingsSider 画左栏：logo + 一个导航项（豆包那边的 .sider/.nav）。
func drawSettingsSider(th theme, pad float64) {
	// logo：32x32 的圆里放本项目的标记
	lx := pad + scaled(siderPadX)
	ly := pad + scaled(siderPadY)
	box := scaled(32)
	settingsSurf.fillCircle(lx+box/2, ly+box/2, box/2, th.ballBg)
	settingsSurf.strokeCircle(lx+box/2, ly+box/2, box/2-0.5, scaled(1), th.ballBorder)
	settingsSurf.fillSparkle(lx+box/2, ly+box/2, box*0.28, th.ballMark)

	// 导航项：唯一一页，处于选中态
	ny := ly + box + scaled(12)
	nr := rect{int32(lx), int32(ny), int32(lx + scaled(siderW-siderPadX*2)), int32(ny + scaled(navItemH))}
	settingsSurf.fillRoundRect(float64(nr.Left), float64(nr.Top),
		float64(nr.width()), float64(nr.height()), scaled(navItemR), th.navSelected)
	f := font(scaledI(navFontPx), fwMedium)
	settingsSurf.drawText(nr.Left+int32(scaled(20)), nr.Top, "AI 划词工具栏", f,
		nr.width()-int32(scaled(28)), nr.height(), th.label)
}

// drawSettingsContent 画右栏：标题 + 分组 + 各行。
func drawSettingsContent(th theme, pad float64) {
	x := pad + scaled(siderW) + scaled(contentPadX)
	w := scaled(settingsW) - scaled(siderW) - 2*scaled(contentPadX)

	// 页面标题
	title := rect{int32(x), int32(pad + scaled(20)), int32(x + w), int32(pad + scaled(56))}
	settingsSurf.drawText(title.Left, title.Top, "设置", font(scaledI(pageTitlePx), fwSemiBold),
		int32(w), title.height(), th.bodyText)

	rects := settingsRowRects()
	rows := settingsRows()
	fTitle := font(scaledI(rowTitlePx), fwMedium)
	fSub := font(scaledI(rowSubPx), fwNormal)
	fSection := font(scaledI(sectionPx), fwMedium)

	for i, r := range rows {
		ir := rects[i]
		switch r.kind {
		case rowSection:
			settingsSurf.drawText(ir.Left, ir.Top+int32(scaled(14)), r.title, fSection,
				ir.width(), ir.height()-int32(scaled(14)), th.label)
		case rowHint:
			settingsSurf.drawText(ir.Left, ir.Top, r.title, fSub, ir.width(), ir.height(), th.text4)
		case rowToggle:
			// 豆包 .global-switch：整行可点，左边标题、右边开关
			if i == settingsHover {
				settingsSurf.fillRoundRect(float64(ir.Left), float64(ir.Top),
					float64(ir.width()), float64(ir.height()), scaled(12), th.hover)
			}
			drawRowLabel(th, ir, r.title, fTitle)
			drawSwitch(th, settingsSwitchRect(ir), r.on())
		case rowField:
			// 标签在左；右边那块"框"由我们画底 + 边框，原生 EDIT 子窗口贴在上面
			// （子窗口不会跟着我们重画，位置在 layoutSettingsFields 里摆）
			settingsSurf.drawText(ir.Left, ir.Top, r.title, fTitle,
				ir.width()-int32(scaled(200)), ir.height(), th.bodyText)
			box := fieldRect(ir)
			settingsSurf.fillRoundRect(float64(box.Left), float64(box.Top),
				float64(box.width()), float64(box.height()), scaled(8), th.fieldBg)
			settingsSurf.strokeRoundRect(float64(box.Left)+0.5, float64(box.Top)+0.5,
				float64(box.width())-1, float64(box.height())-1, scaled(8), scaled(1), th.blockBorder)
			if r.sub != "" {
				settingsSurf.drawText(ir.Left, ir.Top+int32(scaled(22)), r.sub, fSub,
					ir.width()-int32(scaled(200)), int32(scaled(18)), th.text4)
			}
		case rowSkill, rowAction:
			// 豆包 .menuSorterContentContainer：浅底 + 细边框的 48px 行
			settingsSurf.fillRoundRect(float64(ir.Left), float64(ir.Top),
				float64(ir.width()), float64(ir.height()), scaled(12), th.blockBg)
			settingsSurf.strokeRoundRect(float64(ir.Left)+0.5, float64(ir.Top)+0.5,
				float64(ir.width())-1, float64(ir.height())-1, scaled(12), scaled(1), th.blockBorder)
			if i == settingsHover {
				settingsSurf.fillRoundRect(float64(ir.Left), float64(ir.Top),
					float64(ir.width()), float64(ir.height()), scaled(12), th.hover)
			}
			// 图标 20px（豆包 .menuSorterContentContainer img { height:20px }）
			iconX := float64(ir.Left) + scaled(14)
			iconY := float64(ir.Top) + (float64(ir.height())-scaled(20))/2
			drawMenuIcon(settingsSurf, r.icon, iconX, iconY, scaled(20), th.label)
			tx := int32(iconX + scaled(20) + scaled(9))
			tw := int32(float64(ir.Right) - scaled(16) - float64(tx))
			if r.kind == rowSkill {
				tw -= int32(scaled(switchW + 12))
			}
			if r.kind == rowSkill {
				settingsSurf.drawText(tx, ir.Top, r.title, fTitle, tw, ir.height(), th.bodyText)
				drawSwitch(th, settingsSwitchRect(ir), r.on())
			} else {
				// 动作行：标题在左、说明（路径之类）在右，**不要**两次 drawText 叠在同一处
				subW := int32(0)
				if r.sub != "" {
					subW = textWidth(r.sub, fSub)
				}
				titleW := tw - subW - int32(scaled(12))
				if titleW < int32(scaled(40)) {
					titleW = int32(scaled(40))
				}
				settingsSurf.drawText(tx, ir.Top, r.title, fTitle, titleW, ir.height(), th.bodyText)
				if subW > 0 {
					sx := ir.Right - int32(scaled(16)) - subW
					settingsSurf.drawText(sx, ir.Top, r.sub, fSub, subW+2, ir.height(), th.text4)
				}
			}
		}
	}

	// 底部：一条分隔线 + 状态文案（豆包 .split / .line）
	last := rects[len(rects)-1]
	y := float64(last.Bottom) + scaled(16)
	settingsSurf.fillRoundRect(x, y, w, scaled(1), 0, th.divider)
	state := "划词工具栏：已启用"
	if !selectionPopupEnabled() {
		state = "划词工具栏：已关闭"
	}
	if st := settingsStatus(); st != "" {
		state = st
	}
	settingsSurf.drawText(int32(x), int32(y+scaled(12)), state, fSub,
		int32(w), int32(scaled(20)), th.text4)
}

// drawRowLabel 画开关行那一行的标题（右侧留给开关）。
func drawRowLabel(th theme, ir rect, title string, fTitle uintptr) {
	x := ir.Left + int32(scaled(16))
	settingsSurf.drawText(x, ir.Top, title, fTitle,
		ir.width()-int32(scaled(switchW+32)), ir.height(), th.bodyText)
}

// drawSwitch 画一个开关。规格照 Semi（豆包用的就是 Semi Design）：
// 高 24、圆角 12、打开时用主色，关掉时是浅灰底。
func drawSwitch(th theme, r rect, on bool) {
	w, h := float64(r.width()), float64(r.height())
	bg := th.switchOff
	if on {
		bg = th.iris
	}
	settingsSurf.fillRoundRect(float64(r.Left), float64(r.Top), w, h, scaled(switchR), bg)
	knob := h - scaled(4)
	kx := float64(r.Left) + scaled(2)
	if on {
		kx = float64(r.Right) - scaled(2) - knob
	}
	settingsSurf.fillCircle(kx+knob/2, float64(r.Top)+h/2, knob/2, th.switchKnob)
}

// settingsHit 的返回值：hitCloseButton 是右上角关闭按钮，hitNone 是哪里都不在，
// 其余 >= 0 表示第几行。
const (
	hitNone        = -1
	hitCloseButton = -2
)

func settingsHit(pt point) int {
	if settingsCloseRect().contains(pt) {
		return hitCloseButton
	}
	return settingsRowIndexAt(pt)
}

// settingsClick 处理一次点击。
func settingsClick(pt point) {
	idx := settingsHit(pt)
	switch idx {
	case hitCloseButton:
		closeSettings()
		return
	case hitNone:
		return
	}
	rows := settingsRows()
	if idx >= len(rows) {
		return
	}
	r := rows[idx]
	switch r.kind {
	case rowField:
		// 点这一行的任何地方都把焦点交给那个框（原生 EDIT 自己也会收到点击，
		// 这里只是让"点标签也能输入"）
		if f := fieldByKey(r.fieldKey); f.id != 0 {
			focusSettingsField(f.id)
		}
	case rowToggle:
		// 整行可点，不要求点在小开关上
		if r.set != nil && r.on != nil {
			r.set(!r.on())
		}
	case rowSkill:
		if r.set != nil && r.on != nil {
			r.set(!r.on())
		}
	case rowAction:
		if r.run != nil {
			go r.run()
		}
	}
	renderSettings()
	settingsSurf.present(hwndSettings, settingsPos.X, settingsPos.Y)
}

// ================================================================ 显示 / 隐藏

func openSettings() {
	// 先把球旁边那块清掉，避免两层浮层叠在一起
	hideMenu()

	w, h := settingsWindowSize()
	settingsPos = placeSettings(ballCenter(), w, h)
	settingsHover = hitNone // 先复位再画：否则首帧会带着上一轮的悬停高亮
	renderSettings()
	// 设置页是**可激活**的窗口，并且带原生输入框：呈现时允许它拿焦点（见 D17）
	settingsSurf.presentActivating(hwndSettings, settingsPos.X, settingsPos.Y)
	layoutSettingsFields(true)
	// 焦点给第一个空着的框，用户打开就能直接打字
	best := editModel
	if fieldText(editBaseURL) == "" {
		best = editBaseURL
	} else if fieldText(editAPIKey) == "" {
		best = editAPIKey
	}
	focusSettingsField(best)

	// settingsOpen 和 settingsScreenRect 一起在锁内发布：
	// 钩子线程在 menuRectMu 里读这两个字段判"点了设置页外面吗"（见 hideMenuIfOutside）
	menuRectMu.Lock()
	settingsScreenRect = rect{
		settingsPos.X, settingsPos.Y, settingsPos.X + w, settingsPos.Y + h,
	}
	settingsOpen = true
	menuRectMu.Unlock()
	log.Printf("[settings] 打开设置页 @(%d,%d) %dx%d", settingsPos.X, settingsPos.Y, w, h)
}

func closeSettings() {
	menuRectMu.Lock()
	was := settingsOpen
	settingsOpen = false
	menuRectMu.Unlock()
	if !was {
		return
	}
	settingsHover = hitNone
	hideSettingsFields()
	showWindow(hwndSettings, swHide)
}

// placeSettings 把设置窗口摆在悬浮球左上角，然后夹进工作区。
func placeSettings(anchor point, w, h int32) point {
	wa := workArea(anchor)
	pad := scaledI(settingsShadowPad())
	x := anchor.X - w - scaledI(24) + pad
	y := anchor.Y - h/2
	x = clampI(x, wa.Left, wa.Right-w)
	y = clampI(y, wa.Top, wa.Bottom-h)
	return point{x, y}
}

// focusSettingsField 把键盘焦点给到某个输入框。
func focusSettingsField(id int) {
	if h := fieldHWND(id); h != 0 {
		pSetFocus.Call(h)
	}
}

// hideSettingsFields 收起设置页时把输入框一起藏起来（它们是子窗口，不会自己消失）。
func hideSettingsFields() {
	for _, f := range settingsFields() {
		if h := fieldHWND(f.id); h != 0 {
			showWindow(h, swHide)
		}
	}
}

// saveAIConfigFromForm 把三个输入框的值写进 ai.txt，并把结果回报在设置页底部。
func saveAIConfigFromForm() {
	cfg, err := settingsFormValues()
	if err == nil {
		err = saveAIConfig(cfg)
	}
	if err != nil {
		setSettingsStatus("保存失败：" + err.Error())
		log.Printf("[settings] 保存 AI 配置失败: %v", err)
	} else {
		setSettingsStatus("已保存到 " + aiConfigPath())
		log.Printf("[settings] 已保存 AI 配置：%s @ %s", cfg.Model, cfg.BaseURL)
	}
	repaintSettings()
}

// testAIConnection 先保存再发一次最小请求，把结果回报在设置页底部。
//
// 有它才知道"填的东西到底通不通" —— 否则用户只能回到侧边栏试，
// 而那里只会显示一句错误。
func testAIConnection() {
	cfg, _ := settingsFormValues()
	if err := saveAIConfig(cfg); err != nil {
		setSettingsStatus("保存失败：" + err.Error())
		repaintSettings()
		return
	}
	setSettingsStatus("正在测试…")
	repaintSettings()
	go func() {
		answer, err := aiPing(cfg)
		if err != nil {
			setSettingsStatus("❌ " + err.Error())
		} else {
			setSettingsStatus("✅ 通了：" + truncate(answer, 60))
		}
		repaintSettings()
	}()
}

// settingsStatus 是设置页底部那一行状态（保存结果 / 测试结果）。
//
// 单独一份状态是因为它跟"开关"无关：保存成功、测试失败都得有地方说。
var (
	settingsStatusMu sync.Mutex
	settingsStatus_  string
)

func setSettingsStatus(s string) {
	settingsStatusMu.Lock()
	settingsStatus_ = s
	settingsStatusMu.Unlock()
}

func settingsStatus() string {
	settingsStatusMu.Lock()
	defer settingsStatusMu.Unlock()
	return settingsStatus_
}

// repaintSettings 重画设置页（保存/测试之后从后台线程请求）。
func repaintSettings() {
	postToMain(wmRepaintSettings)
}
