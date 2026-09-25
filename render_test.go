//go:build windows

// render_test.go —— 渲染、定位、主题（含缩放、阴影裁切、窗口可见性、暗色主题静默失效的守卫）。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o nkb.test.exe .
//	./nkb.test.exe -test.v

package main

import (
	"runtime"
	"testing"
)

func TestMenuAlwaysFitsInWorkArea(t *testing.T) {
	wa := workArea(point{0, 0})
	winW, winH := menuWindowSize(3)
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

func TestMenuPrefersBelowRightAndDoesNotCoverAnchor(t *testing.T) {
	wa := workArea(point{0, 0})
	winW, winH := menuWindowSize(3)
	pad := scaledI(menuShadowPad())

	// 工作区正中偏左上的位置，右下都有空间
	anchor := point{(wa.Left + wa.Right) / 2, (wa.Top + wa.Bottom) / 2}
	p := placeMenu(anchor, winW, winH)
	cardL, cardT := p.X+pad, p.Y+pad

	if cardL <= anchor.X {
		t.Errorf("右侧有空间时应向右弹，实际卡片左边 %d <= 锚点 %d", cardL, anchor.X)
	}
	if cardT <= anchor.Y {
		t.Errorf("下方有空间时应向下弹，实际卡片上边 %d <= 锚点 %d", cardT, anchor.Y)
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

	// 球心应该是品牌红 #F0142F（预乘后 B,G,R = 47,20,240）
	o := (mid*s.h + mid) * 4
	b, g, r := s.bits[o], s.bits[o+1], s.bits[o+2]
	if r != 0xF0 || g != 0x14 || b != 0x2F {
		t.Errorf("球心颜色预乘后为 (%d,%d,%d)，期望 #F0142F 预乘 (240,20,47)", b, g, r)
	}
}

func TestMenuRenderShapeAndHover(t *testing.T) {
	scale = 1.0
	menuModel_ = menuModel{items: []menuItem{
		{title: "复制", shortcut: "Ctrl+C", icon: iconCopy},
		{title: "搜索", shortcut: "Enter", icon: iconSearch},
		{title: "翻译", shortcut: "Ctrl+T", icon: iconTranslate},
	}}
	menuHover = 0

	n := len(menuModel_.items)
	w, h := menuWindowSize(n)
	menuSurf = newSurface(w, h)
	renderMenu()
	s := menuSurf

	pad := scaledI(menuShadowPad())

	// 卡片内部应该是不透明的
	if a := s.bits[((pad+20)*s.w+pad+20)*4+3]; a != 255 {
		t.Errorf("卡片内部 alpha=%d，应为 255", a)
	}
	// 窗口四角必须透明（圆角 + 投影区）
	if a := s.bits[0*4+3]; a != 0 {
		t.Errorf("左上角 alpha=%d，应为 0", a)
	}

	// 悬停项背景应比普通项暗一点（rgba(155,165,175,.12) 叠白 = 243,244,245）
	blankX := int32(pad) + int32(scaled(150))
	y0 := int32(pad) + int32(scaled(menuCardPad)) + int32(scaled(20))
	y1 := y0 + int32(scaled(menuItemH))
	hoverR := s.bits[(y0*s.w+blankX)*4+2]
	plainR := s.bits[(y1*s.w+blankX)*4+2]
	if !(hoverR < 250 && plainR >= 250) {
		t.Errorf("悬停项背景 R=%d（应 <250），普通项 R=%d（应 =255）", hoverR, plainR)
	}

	// 每一项里都应有文字墨迹（否则就是字体没加载出来）
	for i := range menuModel_.items {
		top := int32(pad) + int32(scaled(menuCardPad)) + int32(float64(i)*scaled(menuItemH))
		ink := 0
		for y := top; y < top+int32(scaled(menuItemH)); y++ {
			for x := int32(pad); x < int32(pad)+int32(scaled(menuCardW)); x++ {
				a := s.bits[(y*s.w+x)*4+3]
				r := s.bits[(y*s.w+x)*4+2]
				if a > 200 && r < 150 {
					ink++
				}
			}
		}
		if ink < 40 {
			t.Errorf("第 %d 项文字墨迹只有 %d 像素，疑似字体渲染失败", i, ink)
		}
	}
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

// 阴影不能被窗口边界切出直边。
//
// 这个测试以前是恒真的（`pad` 就是 `shadowReach()` 本身，断言 pad >= shadowReach 永远成立），
// 等于摆设。真正该验的是**渲染结果**：窗口最外一圈的 alpha 必须接近 0，
// 否则说明留白不够、阴影被裁，视觉上会看到一条硬边（这个 bug 真出现过）。
func TestShadowIsNotClippedAtWindowEdge(t *testing.T) {
	scale = 1.0
	menuModel_ = menuModel{items: []menuItem{
		{title: "复制", shortcut: "Ctrl+C", icon: iconCopy},
		{title: "搜索", icon: iconSearch},
		{title: "翻译", icon: iconTranslate},
	}}
	menuHover = -1
	w, h := menuWindowSize(3)
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

func TestThemeLoads(t *testing.T) {
	th := loadTheme()
	if th.cardBg.A != 1 {
		t.Errorf("卡片背景应当不透明，实际 alpha=%v", th.cardBg.A)
	}
	if th.accent.R == 0 {
		t.Error("品牌红未设置")
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
