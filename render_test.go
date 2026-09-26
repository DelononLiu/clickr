//go:build windows

// render_test.go —— 渲染、定位、主题（含缩放、阴影裁切、窗口可见性、暗色主题静默失效的守卫）。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o clickr.test.exe .
//	./clickr.test.exe -test.v

package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// testMenu 造一个和真实菜单同构的模型：横向三项 + 「…」展开的面板。
func testMenu() menuModel {
	return menuModel{
		items: []menuItem{
			{title: "复制", icon: iconCopy},
			{title: "搜索", icon: iconSearch},
			{title: "翻译", icon: iconTranslate},
		},
		more: []menuItem{
			{title: "关闭划词弹出", icon: iconClose},
			{title: "退出", icon: iconClose},
		},
	}
}

func TestMenuAlwaysFitsInWorkArea(t *testing.T) {
	wa := workArea(point{0, 0})
	winW, winH := menuWindowSize(testMenu())
	pad := scaledI(menuShadowPad())

	corners := map[string]point{
		"右下角": {wa.Right - 2, wa.Bottom - 2},
		"左上角": {wa.Left + 2, wa.Top + 2},
		"右上角": {wa.Right - 2, wa.Top + 2},
		"左下角": {wa.Left + 2, wa.Bottom - 2},
		"正中间": {(wa.Left + wa.Right) / 2, (wa.Top + wa.Bottom) / 2},
	}
	for name, anchor := range corners {
		p := placeMenu(anchor, winW, winH)
		l, top := p.X+pad, p.Y+pad
		r, b := l+winW-2*pad, top+winH-2*pad
		if l < wa.Left || top < wa.Top || r > wa.Right || b > wa.Bottom {
			t.Errorf("%s: 卡片 (%d,%d)-(%d,%d) 超出工作区 (%d,%d)-(%d,%d)",
				name, l, top, r, b, wa.Left, wa.Top, wa.Right, wa.Bottom)
		}
	}
}

// 菜单贴在**鼠标正下方一行**，左边与鼠标对齐，且不压住光标。
//
// 这条规格变过：原来是"锚点右下 8px + 锚点取选区矩形右下角"，
// 现在是"鼠标正下方一行 + 锚点就是鼠标点"。
func TestMenuSitsOneLineBelowMouseAnchor(t *testing.T) {
	wa := workArea(point{0, 0})
	winW, winH := menuWindowSize(testMenu())
	pad := scaledI(menuShadowPad())

	// 工作区正中：右下都有空间，不会触发翻转
	anchor := point{(wa.Left + wa.Right) / 2, (wa.Top + wa.Bottom) / 2}
	p := placeMenu(anchor, winW, winH)
	cardL, cardT := p.X+pad, p.Y+pad

	if cardL != anchor.X {
		t.Errorf("菜单左边 %d 应与鼠标 x %d 对齐", cardL, anchor.X)
	}
	if want := anchor.Y + scaledI(menuAnchorGapY); cardT != want {
		t.Errorf("菜单上边 %d，期望 %d（鼠标下方一行）", cardT, want)
	}
	if cardT <= anchor.Y {
		t.Errorf("菜单压住了光标：上边 %d <= 鼠标 y %d", cardT, anchor.Y)
	}
}

func TestBallRenderShape(t *testing.T) {
	scale = 1.0
	renderBall()
	s := ballSurf
	if s == nil {
		t.Fatal("悬浮球表面为空")
	}

	// 四角必须完全透明，否则窗口会是个方块
	for _, c := range []struct {
		name string
		x, y int32
	}{{"左上", 0, 0}, {"右上", s.w - 1, 0}, {"左下", 0, s.h - 1}, {"右下", s.w - 1, s.h - 1}} {
		if a := s.bits[(c.y*s.w+c.x)*4+3]; a != 0 {
			t.Errorf("%s 角 alpha=%d，应为 0", c.name, a)
		}
	}

	// 球心必须不透明
	mid := s.w / 2
	if a := s.bits[(mid*s.h+mid)*4+3]; a != 255 {
		t.Errorf("球心 alpha=%d，应为 255", a)
	}

	// 球心必须是品牌标记本身的颜色：四角星在正中心覆盖率是 1，
	// 所以这里取到的应当是**原色**，不掺球底色。
	//
	// 期望值从 loadTheme() 里取，不写死 —— 暗色主题下标记会用浅端
	// （#49589C 压在 #303134 上对比不够），写死就只在浅色机器上过了。
	th := loadTheme()
	setSelectionPopup(true)
	renderBall()
	o := (mid*s.h + mid) * 4
	b, g, r := s.bits[o], s.bits[o+1], s.bits[o+2]
	if r != th.ballMark.R || g != th.ballMark.G || b != th.ballMark.B {
		t.Errorf("球心颜色为 (%d,%d,%d)，期望品牌标记 #%02X%02X%02X（dark=%v）",
			r, g, b, th.ballMark.R, th.ballMark.G, th.ballMark.B, th.dark)
	}

	// 「划词弹出」关掉之后，球心标记必须变成另一个颜色。
	//
	// 这不是装饰：功能关掉后菜单不再出现，**球是唯一还在屏幕上的东西**，
	// 它同时也是重新开启的入口 —— 状态看不见的话，用户就找不回来了。
	setSelectionPopup(false)
	renderBall()
	b2, g2, r2 := s.bits[o], s.bits[o+1], s.bits[o+2]
	if r2 == r && g2 == g && b2 == b {
		t.Errorf("关掉「划词弹出」后球心标记没变（还是 #%02X%02X%02X），状态看不出来", r2, g2, b2)
	}
	if r2 != th.ballMarkOff.R || g2 != th.ballMarkOff.G || b2 != th.ballMarkOff.B {
		t.Errorf("关掉后球心是 (%d,%d,%d)，期望中性灰 #%02X%02X%02X",
			r2, g2, b2, th.ballMarkOff.R, th.ballMarkOff.G, th.ballMarkOff.B)
	}
	setSelectionPopup(true)
}

// 菜单渲染：横向工具条 + 「…」展开的面板。
//
// 全部断言都**不假设主题**：浅色/暗色只是同一套几何配两组颜色，
// 写死"悬停一定更暗""文字一定更深"就只在浅色机器上过了。
func TestMenuRenderShapeAndHover(t *testing.T) {
	scale = 1.0
	m := testMenu()
	menuModel_ = m
	menuHover = 0
	menuExpanded = true
	menuPanelHover = 0

	w, h := menuWindowSize(m)
	menuSurf = newSurface(w, h)
	renderMenu()
	s := menuSurf

	bar := barCardRect(m)
	panel := panelCardRect(m)

	// 两块卡片内部都必须不透明
	for name, r := range map[string]rect{"工具条": bar, "面板": panel} {
		o := (int(r.Top+6)*int(s.w) + int(r.Left+6)) * 4
		if a := s.bits[o+3]; a != 255 {
			t.Errorf("%s内部 alpha=%d，应为 255", name, a)
		}
	}
	// 窗口四角必须透明（圆角 + 投影区）
	if a := s.bits[0*4+3]; a != 0 {
		t.Errorf("左上角 alpha=%d，应为 0", a)
	}

	// 悬停项与普通项背景必须不同（浅色 #F5F5F5、暗色 rgba(255,255,255,.06)）
	br := barItemRects(m)
	hoverX, plainX := br[0].Left+2, br[1].Left+2
	cy := (br[0].Top + br[0].Bottom) / 2
	hoverR := s.bits[(cy*s.w+hoverX)*4+2]
	plainR := s.bits[(cy*s.w+plainX)*4+2]
	if hoverR == plainR {
		t.Errorf("工具条悬停项与普通项背景相同（R=%d），悬停态没画出来", hoverR)
	}

	// 面板里也一样
	pr := panelItemRects(m)
	ph := s.bits[((pr[0].Top+pr[0].Bottom)/2*s.w+pr[0].Left+2)*4+2]
	pp := s.bits[((pr[1].Top+pr[1].Bottom)/2*s.w+pr[1].Left+2)*4+2]
	if ph == pp {
		t.Errorf("面板悬停项与普通项背景相同（R=%d），悬停态没画出来", ph)
	}

	// 卡片**没有自己的描边**：豆包那圈 1px 的边是投影的第一层
	// （0 0 1px 0 rgba(0,0,0,.3)）。所以这里断言两件事：
	// 卡片内缘就是底色本身，而贴边外面确实有那圈淡淡的投影。
	edgeX := (bar.Left + bar.Right) / 2
	inside := s.bits[((bar.Top+3)*s.w+edgeX)*4 : ((bar.Top+3)*s.w+edgeX)*4+4]
	outside := s.bits[((bar.Top-2)*s.w+edgeX)*4 : ((bar.Top-2)*s.w+edgeX)*4+4]
	if abs(int(inside[2])-255) > 2 || abs(int(inside[0])-255) > 2 {
		t.Errorf("卡片内缘应当是底色本身（无描边），实际 RGB=(%d,%d,%d)", inside[2], inside[1], inside[0])
	}
	if a := outside[3]; a == 0 || a > 90 {
		t.Errorf("卡片外缘那圈 1px 投影 alpha=%d，期望 1..90（豆包 0 0 1px rgba(0,0,0,.3)）", a)
	}

	// 每一项里都要有文字墨迹（否则就是字体没加载出来）。
	// 判据是"和底色不一样"，不是"比底色暗" —— 暗色主题下文字是白的。
	checkInk := func(name string, r rect) {
		t.Helper()
		o := (int(r.Top+2)*int(s.w) + int(r.Right-2)) * 4
		bgR := int(s.bits[o+2])
		ink := 0
		for y := r.Top; y < r.Bottom; y++ {
			for x := r.Left; x < r.Right; x++ {
				o := (y*s.w + x) * 4
				if a, rr := s.bits[o+3], int(s.bits[o+2]); a > 200 && abs(rr-bgR) > 60 {
					ink++
				}
			}
		}
		if ink < 25 {
			t.Errorf("%s 里文字墨迹只有 %d 像素，疑似字体渲染失败", name, ink)
		}
	}
	for i, it := range barItems(m) {
		if it.title != "" {
			checkInk("工具条第"+itoa(i)+"项 "+it.title, br[i])
		}
	}
	for i, it := range m.more {
		checkInk("面板第"+itoa(i)+"项 "+it.title, pr[i])
	}
}

// 尺寸：这是贴在选区旁边的浮层，必须"扁"。
//
// 之前是竖列表（185x128），改成横条之后高度应该只有 30 多个像素，
// 而且**宽度是由内容算出来的**，不是写死的常量。
func TestMenuBarIsShortAndContentSized(t *testing.T) {
	scale = 1.0
	m := testMenu()
	bw, bh := barSize(m)

	// 结构是豆包的（内边距 + 图标 + 内边距），尺寸整体缩了一档：
	// 单项 6+16+6 = 28，加卡片内边距 2*2 = 32。见 D15 修订。
	if bh != scaled(32) {
		t.Errorf("工具条高 %.0f，应当是 %.0f（单项 %.0f + 内边距 %.0f）",
			bh, scaled(32), scaled(barItemH), 2*scaled(barPad))
	}
	if barIconSize > 20 {
		t.Errorf("图标 %.0f 太大（缩过一档后应当是 16）", barIconSize)
	}
	if barFontSize > 15 {
		t.Errorf("文字 %.0f 太大（缩过一档后应当是 13）", barFontSize)
	}
	if bw <= bh {
		t.Errorf("工具条 %.0fx%.0f 不是横条", bw, bh)
	}

	// 内容变宽，条就跟着变宽（宽度必须是算出来的）
	longer := m
	longer.items = append([]menuItem{{title: "这是一条很长的动作名", icon: iconCopy}}, m.items...)
	bw2, _ := barSize(longer)
	if bw2 <= bw {
		t.Errorf("加了更长的动作之后工具条宽度没变（%.0f → %.0f）", bw, bw2)
	}

	// 窗口必须为**展开态**预留高度：收起时下面那块是透明的，展开时不能重排
	_, winH := menuWindowSize(m)
	_, ph := panelSize(m)
	want := scaled(menuShadowPad())*2 + bh + scaled(panelGap) + ph
	if diff := float64(winH) - want; diff > 1 || diff < -1 {
		t.Errorf("窗口高 %d，期望 %.0f（工具条 %.0f + 缝 %.0f + 面板 %.0f + 投影留白）",
			winH, want, bh, scaled(panelGap), ph)
	}

	// 二级菜单的宽度**按它自己的内容算**，不再跟工具条一样宽：
	// 工具条挤着四五个技能，菜单里通常只有两项，拉一样宽就是一大条空白。
	pw, _ := panelSize(m)
	if pw >= bw {
		t.Errorf("二级菜单宽 %.0f 不该 ≥ 工具条 %.0f（应当按内容收窄）", pw, bw)
	}
	if pw < scaled(panelMinW) {
		t.Errorf("二级菜单宽 %.0f 小于下限 %.0f", pw, scaled(panelMinW))
	}
	// 收窄之后仍然要落在窗口里，且右缘与工具条右缘对齐（它是从「…」掉下来的）
	bar, panel := barCardRect(m), panelCardRect(m)
	winW, _ := menuWindowSize(m)
	if panel.Right > winW-int32(scaled(menuShadowPad())) {
		t.Errorf("二级菜单右缘 %d 超出窗口内容区", panel.Right)
	}
	if panel.Right != bar.Right {
		t.Errorf("二级菜单右缘 %d 应与工具条右缘 %d 对齐", panel.Right, bar.Right)
	}

	// 菜单项比工具条还长时：窗口要跟着变宽，不能把它裁掉
	long := m
	long.more = []menuItem{{title: "这是一条很长的二级菜单项，长到超过工具条宽度", icon: iconClose}}
	lw := menuContentWidth(long)
	lbw, _ := barSize(long)
	if lw <= lbw {
		t.Errorf("菜单项更长时内容宽度应当跟着变宽：%.0f vs 工具条 %.0f", lw, lbw)
	}
	if winW2, _ := menuWindowSize(long); float64(winW2) < lw {
		t.Errorf("窗口宽 %d 装不下内容 %.0f", winW2, lw)
	}

	// 面板为空时工具条上不该出现「…」
	if n := len(barItems(menuModel{items: m.items})); n != len(m.items) {
		t.Errorf("面板为空时工具条上不该有「…」，实际有 %d 项（动作 %d 项）", n, len(m.items))
	}
	if moreIndex(menuModel{items: m.items}) != -1 {
		t.Error("面板为空时 moreIndex 应为 -1")
	}
	if got := moreIndex(m); got != len(m.items) {
		t.Errorf("「…」的下标是 %d，期望 %d", got, len(m.items))
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// 回归测试：窗口尺寸是乘在 scale 上的，用错的 scale 去夹紧位置
// 会在高 DPI 上把悬浮球挤出屏幕（125% 下实测溢出 5px）。
func TestConstrainBallKeepsWindowInsideWorkArea(t *testing.T) {
	for _, s := range []float64{1.0, 1.25, 1.5, 2.0} {
		scale = s
		wa := workArea(point{0, 0})
		sz := ballWindowSize()
		inputs := []point{
			{wa.Right + 500, wa.Bottom + 500},
			{wa.Right - 10, wa.Bottom - 10},
			{wa.Left - 500, wa.Top - 500},
			{wa.Right - sz + 1, wa.Top},
		}
		for _, in := range inputs {
			got := constrainBall(in)
			if got.X < wa.Left || got.Y < wa.Top ||
				got.X+sz > wa.Right || got.Y+sz > wa.Bottom {
				t.Errorf("scale=%.2f 输入 %v → 输出 %v（窗口 %d）超出工作区 %v",
					s, in, got, sz, wa)
			}
		}
	}
}

func TestDefaultBallPosIsInsideWorkArea(t *testing.T) {
	for _, s := range []float64{1.0, 1.25, 2.0} {
		scale = s
		got := constrainBall(defaultBallPos())
		wa := workArea(got)
		sz := ballWindowSize()
		if got.X < wa.Left || got.Y < wa.Top ||
			got.X+sz > wa.Right || got.Y+sz > wa.Bottom {
			t.Errorf("scale=%.2f 默认位置 %v（窗口 %d）超出工作区 %v", s, got, sz, wa)
		}
	}
}

// 面板不能跟着 WM_MOUSELEAVE 一起收掉。
//
// 工具条和面板之间那条 6px 的缝是透明的（alpha=0），指针过缝时窗口**必然**
// 收到 WM_MOUSELEAVE —— 处理办法是看指针还在不在这一整块里。这条测试守的是
// 那个"整块"确实把缝包进去了，否则"去过面板"会被判成"走开了"，面板一路闪。
func TestMenuBlockCoversGapBetweenBarAndPanel(t *testing.T) {
	scale = 1.0
	m := testMenu()
	bar := barCardRect(m)
	panel := panelCardRect(m)
	block := menuBlockRect(m)

	gapMid := point{(bar.Left + bar.Right) / 2, (bar.Bottom + panel.Top) / 2}
	if !block.contains(gapMid) {
		t.Errorf("工具条与面板之间的缝 %v 不在整块 %v 里", gapMid, block)
	}
	if !block.contains(point{(bar.Left + bar.Right) / 2, panel.Top + 2}) {
		t.Error("面板内部不在整块里")
	}
	if block.contains(point{block.Left - 30, gapMid.Y}) {
		t.Error("整块左边 30px 处不该算在菜单里（那说明指针已经走开了）")
	}
	// 面板为空时，整块就是工具条本身
	bare := menuBlockRect(menuModel{items: m.items})
	if bare != barCardRect(menuModel{items: m.items}) {
		t.Error("没有「…」时整块应当就是工具条")
	}
}

// 阴影不能被窗口边界切出直边。
//
// 这个测试以前是恒真的（`pad` 就是 `shadowReach()` 本身，断言 pad >= shadowReach 永远成立），
// 等于摆设。真正该验的是**渲染结果**：窗口最外一圈的 alpha 必须接近 0，
// 否则说明留白不够、阴影被裁，视觉上会看到一条硬边（这个 bug 真出现过）。
func TestShadowIsNotClippedAtWindowEdge(t *testing.T) {
	scale = 1.0
	menuModel_ = testMenu()
	menuHover = -1
	menuPanelHover = -1
	menuExpanded = true
	w, h := menuWindowSize(menuModel_)
	menuSurf = newSurface(w, h)
	renderMenu()
	s := menuSurf

	maxEdgeAlpha := uint8(0)
	scan := func(x, y int32) {
		if a := s.bits[(y*s.w+x)*4+3]; a > maxEdgeAlpha {
			maxEdgeAlpha = a
		}
	}
	for x := int32(0); x < w; x++ {
		scan(x, 0)
		scan(x, h-1)
	}
	for y := int32(0); y < h; y++ {
		scan(0, y)
		scan(w-1, y)
	}
	// 最外圈允许有极淡的残余，但绝不该出现明显可见的 alpha
	if maxEdgeAlpha > 16 {
		t.Errorf("窗口最外圈 alpha 最高 %d（>16），说明投影被窗口边界裁切、"+
			"会看到一条硬边。检查 shadowReach 与 scaled(menuShadowPad()) 的取值",
			maxEdgeAlpha)
	}
}

// 回归测试：创建窗口时没带 WS_VISIBLE，而 UpdateLayeredWindow 只负责
// 「位置/尺寸/内容/透明度」、不会显示窗口 —— 少一步 SWP_SHOWWINDOW
// 就会「坐标对、像素对，但屏幕上什么都看不到」。
func TestLayeredWindowIsVisibleAfterPresent(t *testing.T) {
	// 必须锁线程：窗口归创建它的线程所有，跨线程 SetWindowPos 会 SendMessage，
	// 而 Go 的测试线程不跑消息循环，那条消息会永远等下去（死锁）。
	// 产品的 main() 里本来就是 LockOSThread + 消息循环，所以不存在这个问题。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := createWindows(); err != nil {
		t.Fatalf("创建窗口失败: %v", err)
	}
	scale = 1.0

	renderBall()
	showBall(point{80, 80})
	if !isWindowVisible(hwndBall) {
		t.Error("present 之后悬浮球窗口仍不可见")
	}
	if r, ok := windowRect(hwndBall); !ok {
		t.Error("拿不到悬浮球窗口矩形")
	} else if r.width() != ballWindowSize() {
		t.Errorf("悬浮球窗口宽 %d，期望 %d", r.width(), ballWindowSize())
	}

	menuModel_ = menuModel{items: []menuItem{{title: "复制", icon: iconCopy}}}
	menuHover = -1
	renderMenu()
	menuSurf.present(hwndMenu, 200, 200)
	if !isWindowVisible(hwndMenu) {
		t.Error("present 之后菜单窗口仍不可见")
	}
}

// dump 文件的魔数必须和注释、文档里写的一致。
//
// 这条守的是一个真踩过的坑：项目从 NexusKB 改名到 clickr 时，writeDump 上面的注释
// 和 docs/architecture.md 的「改名完整性」清单都改成了 CLKR，唯独**实际的字节**
// 还是旧项目名的 'N','K','B','1' —— 而那份清单是照着注释核的，于是两边一起错。
// 直到这次实测导出 dump 文件才发现。魔数只在二进制里，注释与文档都管不住它。
func TestDumpMagicIsDocumentedOne(t *testing.T) {
	f := filepath.Join(t.TempDir(), "magic.clkr")
	scale = 1.0
	renderBall()
	writeDump(f, ballSurf)

	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("读 dump 文件失败: %v", err)
	}
	if len(b) < 12 {
		t.Fatalf("dump 文件只有 %d 字节", len(b))
	}
	if got := string(b[:4]); got != "CLKR" {
		t.Errorf("dump 魔数是 %q，注释与 docs/architecture.md 里写的是 \"CLKR\"", got)
	}
	w, h := int(int32(binary.LittleEndian.Uint32(b[4:8]))), int(int32(binary.LittleEndian.Uint32(b[8:12])))
	if w != int(ballWindowSize()) || h != int(ballWindowSize()) {
		t.Errorf("dump 头里的尺寸 %dx%d，期望 %dx%d", w, h, ballWindowSize(), ballWindowSize())
	}
	if len(b) != 12+w*h*4 {
		t.Errorf("dump 长度 %d，期望 12+%d", len(b), w*h*4)
	}
}

func TestThemeLoads(t *testing.T) {
	th := loadTheme()
	if th.cardBg.A != 1 {
		t.Errorf("卡片背景应当不透明，实际 alpha=%v", th.cardBg.A)
	}
	// 主色是鸢尾蓝（RAL 290 40 40 的官方色名就是「鸢尾蓝 / Iris blue」= #49589C），
	// 不是豆包的品牌蓝 #0057FF —— 仿的是观感，不是把品牌色搬过来。
	if th.iris != irisBlue {
		t.Errorf("主色是 #%02X%02X%02X，期望鸢尾蓝 #%02X%02X%02X",
			th.iris.R, th.iris.G, th.iris.B, irisBlue.R, irisBlue.G, irisBlue.B)
	}
	if th.irisLight.A != 1 {
		t.Error("主色浅端没设（暗色主题的文字/标记要用它）")
	}
	// 菜单文字与图标走主色。**规格是有明确取值的**：浅色用鸢尾蓝本体，
	// 暗色必须用浅端 —— 鸢尾蓝压在 #232528 上只有 2.3:1，读不了。
	// （以前这里只断言"在色系里"，于是暗色写错也照样过，评审抓到了。）
	wantLabel := irisBlue
	if th.dark {
		wantLabel = irisBlueLight
	}
	if th.label != wantLabel {
		t.Errorf("dark=%v 时菜单文字色是 #%02X%02X%02X，期望 #%02X%02X%02X",
			th.dark, th.label.R, th.label.G, th.label.B, wantLabel.R, wantLabel.G, wantLabel.B)
	}
	// 设置页正文两套主题都必须设了（浅色漏设过一次 → 整页正文 alpha=0，看不见）
	if th.bodyText.A == 0 {
		t.Errorf("dark=%v 时设置页正文色 alpha=0（会整页看不见）", th.dark)
	}
	// 次要文字要够看：白底/暗底都得过 AA（4.5:1）
	if !th.dark && th.text4.A < 0.5 {
		t.Errorf("浅色下次要文字 alpha=%.2f，白底对比度不到 AA（要 ≥0.5）", th.text4.A)
	}
	if th.dark && th.text4.A < 0.5 {
		t.Errorf("暗色下次要文字 alpha=%.2f，对比度不到 AA（要 ≥0.5）", th.text4.A)
	}
	// 投影必须就是豆包那两层（第一层当边框用），数值照抄，不许随手改
	if len(menuShadowLayers) != 2 ||
		menuShadowLayers[0].blur != 1 || menuShadowLayers[0].c.A != 0.30 ||
		menuShadowLayers[1].blur != 14 || menuShadowLayers[1].dy != 4 || menuShadowLayers[1].c.A != 0.10 {
		t.Errorf("菜单投影不是豆包那两层: %+v", menuShadowLayers)
	}
	// 设置页那几种底色必须都设了（否则设置页会画成一片白）
	for name, c := range map[string]rgba{
		"navSelected": th.navSelected, "blockBg": th.blockBg, "switchOff": th.switchOff,
	} {
		if c.A == 0 {
			t.Errorf("主题里 %s 没设", name)
		}
	}
	// 圆角同心：悬停块圆角 = 卡片圆角 - 内边距
	if barItemR != menuRadius-barPad {
		t.Errorf("悬停块圆角 %.0f 与卡片圆角 %.0f（内边距 %.0f）不同心", barItemR, menuRadius, barPad)
	}
	// 球心标记必须在鸢尾蓝这一色系里（暗色用浅端）
	if th.ballMark != th.iris && th.ballMark != th.irisLight {
		t.Errorf("球心标记色 #%02X%02X%02X 不在鸢尾蓝色系里",
			th.ballMark.R, th.ballMark.G, th.ballMark.B)
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

// 鼠标停在悬浮球上会自动弹菜单（豆包那个手感）。这条交互靠两个延时消息实现，
// 这里守的是它的两个判据：延迟值合理，以及"该不该留着"的判定覆盖球与菜单两块。
func TestHoverMenuDelaysAreReasonable(t *testing.T) {
	if ballHoverOpenDelay <= 0 || ballHoverCloseDelay <= 0 {
		t.Fatal("悬停延时不能是 0 或负数")
	}
	if ballHoverOpenDelay > 400 {
		t.Errorf("弹出延时 %.0fms 太久，会让人以为没反应", ballHoverOpenDelay)
	}
	if ballHoverCloseDelay < ballHoverOpenDelay {
		t.Errorf("收起延时 %.0fms 比弹出延时 %.0fms 还短：从球挪到菜单会被误收",
			ballHoverCloseDelay, ballHoverOpenDelay)
	}
}

func TestMenuKeepsOpenOnBallOrMenu(t *testing.T) {
	scale = 1.0
	ballPos = point{1000, 500}
	center := ballCenter()

	menuRectMu.Lock()
	oldShown, oldRect := menuShown, menuScreenRect
	menuRectMu.Unlock()
	defer func() {
		menuRectMu.Lock()
		menuShown, menuScreenRect = oldShown, oldRect
		menuRectMu.Unlock()
	}()

	// 鼠标在球上 → 留着
	if !menuKeepsOpenAt(center) {
		t.Error("鼠标在球上时不该收菜单")
	}
	// 球外、菜单也没显示 → 该收
	if menuKeepsOpenAt(point{center.X - 200, center.Y}) {
		t.Error("鼠标两边都不在时应当收起")
	}

	// 菜单显示着、鼠标在菜单上 → 留着（这是"从球挪到菜单"那一半）
	menuRectMu.Lock()
	menuShown = true
	menuScreenRect = rect{center.X - 60, center.Y + 40, center.X + 200, center.Y + 160}
	menuRectMu.Unlock()
	if !menuKeepsOpenAt(point{center.X + 20, center.Y + 80}) {
		t.Error("鼠标在菜单上时不该收菜单")
	}
	// 菜单显示着但鼠标跑远了 → 该收
	if menuKeepsOpenAt(point{center.X + 600, center.Y + 600}) {
		t.Error("鼠标跑远之后应当收起")
	}
}

func TestBallGrowsOnHover(t *testing.T) {
	scale = 1.0
	measure := func() int {
		renderBall()
		s := ballSurf
		mid := int32(s.h / 2)
		n := 0
		for x := int32(0); x < s.w; x++ {
			if s.bits[(mid*s.w+x)*4+3] > 200 {
				n++
			}
		}
		return n
	}
	ballHovered = false
	plain := measure()
	ballHovered = true
	hovered := measure()
	ballHovered = false

	if hovered <= plain {
		t.Errorf("悬停时球没有变大：%d → %d（各像素宽度）", plain, hovered)
	}
	// ballHoverGrow 是半径增量 → 直径应当增大它的两倍（抗锯齿边界允许 ±2px）
	if want := int(scaled(ballHoverGrow)) * 2; hovered-plain < want-2 || hovered-plain > want+2 {
		t.Errorf("悬停直径增大 %d px，期望约 %d（ballHoverGrow 的两倍）", hovered-plain, want)
	}
}
