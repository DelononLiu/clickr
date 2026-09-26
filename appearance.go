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

// 品牌色：鸢尾蓝。
//
// 取值出处是 RAL 290 40 40 —— 这个色号的官方中英文名就是「鸢尾蓝 / Iris blue」，
// 色值 RGB(73,88,156) = #49589C。同类色还有 Pantone 18-3943 Blue Iris (#5B5EA6)，
// 比它亮一度、紫一点；要换就是改这一行。
// 用 var 而不是 const：rgba 里带 float64 的 alpha，复合字面量在 Go 里不是常量。
var (
	irisBlue = rgba{0x49, 0x58, 0x9C, 1}
	// 鸢尾蓝与白混 60%，做渐变描边的浅端。
	irisBlueLight = rgba{0xB6, 0xBC, 0xD7, 1}
)

// theme 是「设计稿」：配色。几何规格在同文件的常量区。
//
// 配色分两层：
//
//	中性层级（卡片底色 / 文字层级 / 悬停底色）与投影规格 —— 照豆包的 token
//	（豆包官网 CSS 里的 --s-color-* / --s-shadow-level*，见 docs/decisions.md D12）；
//	品牌色 —— **鸢尾蓝，不是豆包的品牌蓝**。仿的是它的观感，不搬它的品牌。
type theme struct {
	dark        bool
	cardBg      rgba
	label       rgba // 菜单文字与图标 —— 用主色，不用中性黑
	bodyText    rgba // 设置页正文（中性色：那里是"内容"不是"强调"）
	fieldBg     rgba // 设置页里输入框的底（原生 EDIT 子窗口也用它当背景）
	text4       rgba // 快捷键（次要信息，保持中性）
	hover       rgba
	divider     rgba // 工具条里「…」前那条竖线
	ballBg      rgba
	ballBorder  rgba
	navSelected rgba // 设置页左栏选中项底色
	blockBg     rgba // 设置页里那些 48px 行的浅底
	blockBorder rgba // 同上的细边框
	switchOff   rgba // 开关关掉时的底色
	switchKnob  rgba // 开关的圆点
	iris        rgba // 主色（鸢尾蓝）
	irisLight   rgba // 主色的浅端（暗色主题下做文字/标记色）
	ballMark    rgba // 球心标记（功能开着）：暗底上用浅端，否则鸢尾蓝压不住
	ballMarkOff rgba // 球心标记（「划词弹出」关着）：中性灰 —— 球就是那个开关，状态得看得见
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

	th := theme{
		dark:        dark,
		iris:        irisBlue,
		irisLight:   irisBlueLight,
		ballMark:    irisBlue,
		ballMarkOff: rgba{0x9A, 0xA0, 0xA6, 1},
		ballBorder:  rgba{0x00, 0x00, 0x00, 0.07},
	}
	if dark {
		th.cardBg = rgba{0x23, 0x25, 0x28, 1}      // --s-color-bg-float（暗色）
		th.label = irisBlueLight                   // 暗底上鸢尾蓝压不住（2.3:1），用同色系浅端
		th.bodyText = rgba{0xFF, 0xFF, 0xFF, 0.95} // 设置页正文
		th.fieldBg = rgba{0x2A, 0x2D, 0x30, 1}     // 输入框底（暗色）
		th.text4 = rgba{0xFF, 0xFF, 0xFF, 0.55}    // 次要文字（AA 对比度）
		th.hover = rgba{0xFF, 0xFF, 0xFF, 0.06}    // --s-color-bg-dm-fill（暗色）
		th.divider = rgba{0xFF, 0xFF, 0xFF, 0.08}  // --s-color-fill-secondary（暗色）
		th.ballBg = rgba{0x30, 0x31, 0x34, 1}      // 球体沿用原配色
		th.ballBorder = rgba{0xFF, 0xFF, 0xFF, 0.08}
		th.navSelected = rgba{0xB6, 0xBC, 0xD7, 0.18}
		th.blockBg = rgba{0x1E, 0x21, 0x23, 1}
		th.blockBorder = rgba{0xFF, 0xFF, 0xFF, 0.08}
		th.switchOff = rgba{0xFF, 0xFF, 0xFF, 0.16}
		th.switchKnob = rgba{0xFF, 0xFF, 0xFF, 1}
		th.ballMark = irisBlueLight
		th.ballMarkOff = rgba{0x7A, 0x80, 0x86, 1}
		return th
	}
	th.cardBg = rgba{0xFF, 0xFF, 0xFF, 1} // --s-color-bg-float
	th.label = irisBlue
	th.bodyText = rgba{0x1C, 0x1F, 0x23, 1} // 豆包那边的正文色 #1c1f23
	// 次要文字：豆包用 rgba(0,0,0,.3)（白底只有 2.1:1，不到 AA），这里提到 .55 ≈ 4.7:1
	th.text4 = rgba{0x00, 0x00, 0x00, 0.55}
	th.hover = rgba{0x00, 0x00, 0x00, 0.06}       // --color-bg-trans-primary
	th.divider = rgba{0xE5, 0xE6, 0xEB, 1}        // --s-color-fill-secondary
	th.navSelected = rgba{0x49, 0x58, 0x9C, 0.10} // 主色 10%（豆包用 --primary-transparent-2）
	th.blockBg = rgba{0xF9, 0xFA, 0xFB, 1}        // 豆包 .block-content / .menuSorterContentContainer
	th.blockBorder = rgba{0xE6, 0xEA, 0xED, 1}
	th.switchOff = rgba{0x00, 0x00, 0x00, 0.15}
	th.switchKnob = rgba{0xFF, 0xFF, 0xFF, 1}
	th.ballBg = rgba{0xFF, 0xFF, 0xFF, 1}
	return th
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
	// —— 横向工具条 ——
	//
	// 结构照豆包插件 v1.35.0 的 .float_btn_pad 家族（谁是卡片内边距、谁是图标、
	// 谁是文字、分隔线多粗、投影哪两层 —— 见 D15），但**绝对尺寸整体缩了一档**：
	// 豆包那套是给网页浮层用的（单项 40、图标 24、文字 18、整条 44），
	// 放在桌面划词工具条上字和图标都太大。
	//
	// 缩放比例大致是 0.7：图标 24→16、文字 18→13、单项 40→28、整条 44→32，
	// 圆角同步 14/12 → 10/8（仍然同心：8 = 10 − 内边距 2）。
	barPad      = 2.0  // 卡片内边距
	barItemH    = 28.0 // 6 + 图标 16 + 6
	barItemPadH = 6.0  // 单项四边内边距
	barIconSize = 16.0
	barIconGap  = 4.0
	barItemR    = 8.0 // 悬停底色圆角（= 卡片圆角 10 − 内边距 2，同心）
	barFontSize = 13.0

	// 「…」展开的二级菜单：与工具条同一套「单项」规格，宽度跟工具条一致。
	panelGap         = 6.0
	panelMinW        = 132.0 // 二级菜单的宽度下限（比这窄就不像菜单了）
	panelItemH       = 28.0
	panelItemPadH    = 8.0
	panelIconSize    = 16.0
	panelIconGap     = 6.0
	panelShortcutGap = 12.0
	panelFontSize    = 13.0
	panelShortcutPx  = 12.0

	// 卡片圆角：豆包是 14（展开态），跟着整体缩放取 10。
	menuRadius = 10.0

	// 工具条里「…」之前的那条竖分隔线。
	// 豆包是"左右各留 2px、中间 1px、高 32px"（相对单项 40 是 0.8），跟着缩到 22。
	dividerW   = 1.0
	dividerGap = 2.0
	// 注意：豆包那条规则写的是 height:32px + padding:8px 2px + background-clip:content-box，
	// 也就是"盒高 32、实际只画中间 16px"。按单项高度比（28/40）缩到 12。
	dividerH = 12.0

	// 二级菜单里的横分隔线：豆包是"上下各留 2px、中间 1px、左右各缩进 10px"。
	panelDividerH   = 1.0
	panelDividerPad = 10.0

	menuIconStroke = 1.5

	ballSize     = 40.0
	ballMarkSize = 18.0
	// 鼠标停在球上时**半径**放大这么多（半径，不是直径 —— ballContains 也是按半径算的：
	// 这两处口径必须一致，不然鼠标会在"放大出来的那一圈"上反复进出）
	ballHoverGrow = 3.0
	// 拖动阈值：按下后位移小于它就算单击（手抖不该被当成拖动）
	ballDragThreshold = 3.0

	// 鼠标停在球上多久自动弹菜单、离开多久收起。
	// 弹之前等一下，是为了"路过"不算：球常驻在屏幕边上，鼠标经常从它身上划过去。
	// 收之前等得久一点，是给"从球挪到菜单"留跨越那段空隙的时间
	//（空隙不属于任何窗口、收不到鼠标消息，所以只能靠时间 + 落点判断）。
	ballHoverOpenDelay  = 180.0
	ballHoverCloseDelay = 320.0
)

// 菜单用到的字体。**布局量宽度与渲染必须取同一个句柄** ——
// 工具条的宽度是按文字宽度算出来的，两处字体不一致，文字就会和悬停底色错位。
func barFont() uintptr      { return font(scaledI(barFontSize), fwNormal) }
func panelFont() uintptr    { return font(scaledI(panelFontSize), fwNormal) }
func shortcutFont() uintptr { return font(scaledI(panelShortcutPx), fwNormal) }

// shadowLayer 是一层 CSS box-shadow：0 dy blur 颜色。
type shadowLayer struct {
	blur, dy float64
	c        rgba
}

// 投影规格照抄豆包的 token。CSS 里 --s-shadow-level* 只定义了一套，明暗主题共用。
//
//	--s-shadow-level1: 0 0 4px rgba(0,0,0,.02), 0 6px 10px rgba(47,53,64,.1)   → 悬浮球
//	--s-shadow-level2: 0 0 4px rgba(0,0,0,.02), 0 10px 24px rgba(47,53,64,.2)  → 弹出菜单
var (
	ballShadowLayers = []shadowLayer{
		{blur: 4, dy: 0, c: rgba{0x00, 0x00, 0x00, 0.02}},
		{blur: 10, dy: 6, c: rgba{0x2F, 0x35, 0x40, 0.10}},
	}
	//	.float_btn_pad .floatBtnList { box-shadow: 0 0 1px 0 rgba(0,0,0,.3), 0 4px 14px 0 rgba(0,0,0,.1) }
	//
	// 第一层就是那圈 1px 的"边框" —— 豆包没有单独画描边，所以我们也把那圈细线去掉了。
	menuShadowLayers = []shadowLayer{
		{blur: 1, dy: 0, c: rgba{0x00, 0x00, 0x00, 0.30}},
		{blur: 14, dy: 4, c: rgba{0x00, 0x00, 0x00, 0.10}},
	}
)

func scaled(v float64) float64 { return v * scale }

func scaledI(v float64) int32 { return int32(v*scale + 0.5) }

// 窗口必须比可见内容大出投影扩散的距离，否则阴影会被窗口边界切出一条直边
// （实测肉眼可见，球体下方 alpha 到 48 就断崖）。
//
// 有多层阴影时取**扩散最远的那一层**，不是求和 —— 留白只需要覆盖最远的那个。
func shadowPadOf(layers []shadowLayer) float64 {
	reach := 0.0
	for _, l := range layers {
		if r := shadowReach(l.blur, l.dy); r > reach {
			reach = r
		}
	}
	return reach
}

func menuShadowPad() float64 { return shadowPadOf(menuShadowLayers) }

func ballShadowPad() float64 { return shadowPadOf(ballShadowLayers) }

func ballWindowSize() int32 { return scaledI(ballSize + 2*ballShadowPad()) }

// barItems 返回工具条上要画的项：菜单项 + 末尾的「…」。
//
// 二级面板为空时就**不画「…」** —— 点开是空的按钮比没有这个按钮更糟。
func barItems(m menuModel) []menuItem {
	items := make([]menuItem, 0, len(m.items)+1)
	items = append(items, m.items...)
	if len(m.more) > 0 {
		items = append(items, menuItem{icon: iconMore, isMore: true})
	}
	return items
}

// moreIndex 是「…」在工具条里的下标；没有「…」时返回 -1。
func moreIndex(m menuModel) int {
	if len(m.more) == 0 {
		return -1
	}
	return len(m.items)
}

// barItemWidth 返回单项宽度。
//
// 注意 textWidth 返回的已经是**物理像素**（字体句柄本身就是按 scale 建的），
// 所以文字宽度不能再乘一次 scale —— 乘了就会在高 DPI 下越算越宽。
func barItemWidth(it menuItem) float64 {
	w := 2*scaled(barItemPadH) + scaled(barIconSize)
	if it.isMore {
		return w + scaled(8) // 纯图标的「…」给宽一点（小一档之后仍然是 32x28 的可点区）
	}
	if it.title != "" {
		w += scaled(barIconGap) + float64(textWidth(it.title, barFont()))
	}
	return w
}

// barSize 返回工具条卡片的尺寸（不含阴影留白）。
func barSize(m menuModel) (float64, float64) {
	w := 2 * scaled(barPad)
	for i, it := range barItems(m) {
		if i > 0 && it.isMore {
			w += 2*scaled(dividerGap) + scaled(dividerW)
		}
		w += barItemWidth(it)
	}
	return w, scaled(barItemH) + 2*scaled(barPad)
}

// barItemRects 返回工具条各项在窗口坐标系里的矩形。
func barItemRects(m menuModel) []rect {
	pad := scaled(menuShadowPad())
	itemH := scaled(barItemH)
	x := pad + scaled(barPad)
	y := pad + scaled(barPad)
	out := make([]rect, 0, len(barItems(m)))
	for i, it := range barItems(m) {
		if i > 0 && it.isMore {
			x += 2*scaled(dividerGap) + scaled(dividerW)
		}
		w := barItemWidth(it)
		out = append(out, rect{int32(x), int32(y), int32(x + w), int32(y + itemH)})
		x += w
	}
	return out
}

// panelSize 返回「…」二级菜单的尺寸。
//
// 宽度**按它自己的内容算**，不再跟工具条一样宽：工具条上挤着四五个技能，
// 二级菜单里通常只有两项，拉成一样宽就是一大条空白。
// 太窄会像块标签，所以给一个下限 panelMinW。
func panelSize(m menuModel) (float64, float64) {
	w := 0.0
	for _, it := range m.more {
		iw := 2*scaled(panelItemPadH) + scaled(panelIconSize) + scaled(panelIconGap) +
			float64(textWidth(it.title, panelFont()))
		if it.shortcut != "" {
			iw += scaled(panelShortcutGap) + float64(textWidth(it.shortcut, shortcutFont()))
		}
		if iw > w {
			w = iw
		}
	}
	if min := scaled(panelMinW); w < min {
		w = min
	}
	return w, 2*scaled(barPad) + float64(len(m.more))*scaled(panelItemH)
}

// menuContentWidth 是窗口内容需要的宽度：工具条与二级菜单谁宽取谁。
// （二级菜单比工具条宽时不能裁掉它。）
func menuContentWidth(m menuModel) float64 {
	bw, _ := barSize(m)
	if len(m.more) == 0 {
		return bw
	}
	pw, _ := panelSize(m)
	if pw > bw {
		return pw
	}
	return bw
}

// panelItemRects 返回面板各项在窗口坐标系里的矩形。
func panelItemRects(m menuModel) []rect {
	card := panelCardRect(m)
	x := float64(card.Left) + scaled(barPad)
	w := float64(card.width()) - 2*scaled(barPad)
	y := float64(card.Top) + scaled(barPad)
	out := make([]rect, 0, len(m.more))
	for range m.more {
		out = append(out, rect{int32(x), int32(y), int32(x + w), int32(y + scaled(panelItemH))})
		y += scaled(panelItemH)
	}
	return out
}

// barCardRect / panelCardRect 是两块卡片本身（命中测试与「点外面了没」都用它）。
func barCardRect(m menuModel) rect {
	pad := scaled(menuShadowPad())
	bw, bh := barSize(m)
	return rect{int32(pad), int32(pad), int32(pad + bw), int32(pad + bh)}
}

// panelCardRect 是二级菜单那块卡片。
//
// **右缘与工具条右缘对齐** —— 它挂在工具条最右边那个「…」下面，
// 挂左边会让人以为它属于第一个技能。
func panelCardRect(m menuModel) rect {
	bar := barCardRect(m)
	pw, ph := panelSize(m)
	right := float64(bar.Right)
	if cw := menuContentWidth(m); cw > float64(bar.width()) {
		right = float64(bar.Left) + cw // 面板更宽时：右缘仍然是内容区的右缘
	}
	y := float64(bar.Bottom) + scaled(panelGap)
	return rect{int32(right - pw), int32(y), int32(right), int32(y + ph)}
}

// menuWindowSize 返回整个窗口的尺寸。
//
// 窗口高度**按展开态预留**（工具条 + 缝 + 面板），收起时下面那块是 alpha=0 的
// 透明区 —— 透明区不吃鼠标消息，所以不影响下面的程序。这样做的理由：
// 悬停展开时不必再去改窗口尺寸并重排位置（改尺寸要重建 surface + 重新 present，
// 在 hover 这种高频路径上不值得）。
func menuWindowSize(m menuModel) (int32, int32) {
	pad := scaled(menuShadowPad())
	_, bh := barSize(m)
	h := bh
	if len(m.more) > 0 {
		_, ph := panelSize(m)
		h += scaled(panelGap) + ph
	}
	return int32(menuContentWidth(m) + pad*2 + 0.5), int32(h + pad*2 + 0.5)
}

// menuVisibleRect 返回菜单**当前真的画了东西**的那块矩形。
//
// 收起态就是工具条；展开态是工具条 + 缝 + 二级菜单。命中测试、钩子判"点外面"、
// 离开时判"还没走开"三处都要它 —— 以前三处各写一遍 if，容易改漏一处。
func menuVisibleRect(m menuModel, expanded bool) rect {
	if expanded && len(m.more) > 0 {
		return menuBlockRect(m)
	}
	return barCardRect(m)
}

// menuBlockRect 是工具条 + 面板合起来的矩形（含中间那条缝）。
//
// 用途是判「指针还在菜单这块里吗」：缝是透明的、不吃鼠标消息，
// 指针过缝时窗口一定会收到 WM_MOUSELEAVE —— 用它把「过缝去面板」和
// 「走开了」区分开，前者不该把面板收掉。
func menuBlockRect(m menuModel) rect {
	r := barCardRect(m)
	if len(m.more) > 0 {
		r = unionRect(r, panelCardRect(m))
	}
	return r
}

// unionRect 把两块卡片并成一个矩形：窗口里可见的部分可能是两段
// （工具条 + 展开的面板），而「点在菜单外面了吗」只能用一个矩形来判。
func unionRect(a, b rect) rect {
	return rect{
		minI(a.Left, b.Left), minI(a.Top, b.Top),
		maxI(a.Right, b.Right), maxI(a.Bottom, b.Bottom),
	}
}

func minI(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func maxI(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// 锚点与菜单的相对位置：**鼠标那一行的正下方一行**，左边与鼠标对齐。
//
// 往下让开一行是为了不压住光标，也不盖住刚选中的那几个字；
// 左右不再留偏移，是因为"鼠标一停，条就在它底下"比"右下角 8px"更符合预期。
const (
	menuAnchorGapX = 0.0  // 左边与鼠标对齐
	menuAnchorGapY = 20.0 // 往下让开一行
)

// placeMenu 决定菜单往哪边弹：默认鼠标正下方一行；
// 右边放不下翻到鼠标左边、下面放不下翻到鼠标上面，最后把整块夹进显示器工作区
// （rcWork，不是整块屏幕）。
//
// 夹的是**整块**（工具条 + 可能展开的面板）：宁可整条离鼠标远一点，
// 也不要展开时面板跑到屏幕外面去。
func placeMenu(anchor point, winW, winH int32) point {
	wa := workArea(anchor)
	pad := scaledI(menuShadowPad())
	cardW := winW - pad*2
	cardH := winH - pad*2
	gapX := scaledI(menuAnchorGapX)
	gapY := scaledI(menuAnchorGapY)

	cardX := anchor.X + gapX
	cardY := anchor.Y + gapY
	if cardX+cardW > wa.Right {
		cardX = anchor.X - gapX - cardW
	}
	if cardY+cardH > wa.Bottom {
		cardY = anchor.Y - gapY - cardH
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
