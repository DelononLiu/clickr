//go:build windows

// nexus_test.go —— 跑在 Windows 上的测试（渲染依赖 GDI，只能在 Windows 执行）。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o nkb.test.exe .
//	./nkb.test.exe -test.v
package main

import (
	"errors"
	"flag"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// driveHookEvents 把一串事件喂给消费逻辑，返回识别到的动作序列。
func driveHookEvents(t *testing.T, events []hookEvent) []string {
	t.Helper()
	ch := make(chan hookEvent, len(events)+1)
	got := make(chan string, len(events)+1)

	go consumeHookEvents(ch,
		func(_ point, how string) { got <- how },
		nil,
	)

	for _, ev := range events {
		ch <- ev
	}

	var out []string
	deadline := time.After(800 * time.Millisecond)
	for {
		select {
		case how := <-got:
			out = append(out, how)
		case <-deadline:
			return out
		}
	}
}

func TestDragSelectIsRecognised(t *testing.T) {
	// 从左往右拖 120px，明显是一次框选
	got := driveHookEvents(t, []hookEvent{
		{kind: evDown, pt: point{500, 400}},
		{kind: evUp, pt: point{620, 402}},
	})
	if len(got) != 1 || got[0] != "drag" {
		t.Fatalf("期望识别出 1 次 drag，实际 %v", got)
	}
}

func TestTinyMoveIsNotASelection(t *testing.T) {
	// 移动 2px 属于手抖，不是划词（阈值 5px）
	got := driveHookEvents(t, []hookEvent{
		{kind: evDown, pt: point{500, 400}},
		{kind: evUp, pt: point{502, 401}},
	})
	if len(got) != 0 {
		t.Fatalf("2px 抖动不该算划词，实际 %v", got)
	}
}

func TestPlainClickIsNotASelection(t *testing.T) {
	got := driveHookEvents(t, []hookEvent{
		{kind: evDown, pt: point{500, 400}},
		{kind: evUp, pt: point{500, 400}},
	})
	if len(got) != 0 {
		t.Fatalf("单击不该算划词，实际 %v", got)
	}
}

func TestDoubleClickSelectsWord(t *testing.T) {
	// 同一个点快速点两下 = 双击选词
	got := driveHookEvents(t, []hookEvent{
		{kind: evDown, pt: point{300, 300}},
		{kind: evUp, pt: point{300, 300}},
		{kind: evDown, pt: point{301, 300}},
		{kind: evUp, pt: point{301, 300}},
	})
	if len(got) != 1 || got[0] != "double-click" {
		t.Fatalf("期望识别出 1 次 double-click，实际 %v", got)
	}
}

func TestTwoFarApartClicksAreNotDoubleClick(t *testing.T) {
	// 两次单击离得很远，不该被当成双击
	got := driveHookEvents(t, []hookEvent{
		{kind: evDown, pt: point{100, 100}},
		{kind: evUp, pt: point{100, 100}},
		{kind: evDown, pt: point{900, 700}},
		{kind: evUp, pt: point{900, 700}},
	})
	if len(got) != 0 {
		t.Fatalf("两处远距离单击不该合并成双击，实际 %v", got)
	}
}

func TestUpWithoutDownIsIgnored(t *testing.T) {
	// 程序启动前就按下的那次抬起，不该触发
	got := driveHookEvents(t, []hookEvent{
		{kind: evUp, pt: point{500, 400}},
	})
	if len(got) != 0 {
		t.Fatalf("孤立的上抬事件不该触发，实际 %v", got)
	}
}

// ---------------------------------------------------------------- 弹层定位

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

// ---------------------------------------------------------------- 渲染

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

func TestThemeLoads(t *testing.T) {
	th := loadTheme()
	if th.cardBg.A != 1 {
		t.Errorf("卡片背景应当不透明，实际 alpha=%v", th.cardBg.A)
	}
	if th.accent.R == 0 {
		t.Error("品牌红未设置")
	}
}

// ---------------------------------------------------------------- 位置与缩放

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

// ---------------------------------------------------------------- 剪贴板快照

// 这是针对「划词弄丢用户剪贴板」的回归测试，直接复现失效场景：
// 快照 → 被覆盖（相当于我们发的 Ctrl+C）→ 还原。
//
// 之前用 OleGetClipboard/OleSetClipboard 的版本在这里必然失败：
// 代理对象在剪贴板被改动后就失效了。
func TestClipboardSnapshotSurvivesOverwrite(t *testing.T) {
	// 先保住用户真实的剪贴板，测完放回去
	userSnap := snapshotClipboard()
	defer userSnap.restore()

	const marker = "NexusKB 快照往返测试 A1B2C3"
	if err := setClipboardText(marker); err != nil {
		t.Skipf("写剪贴板失败（可能有别的程序占着）: %v", err)
	}
	time.Sleep(80 * time.Millisecond)

	snap := snapshotClipboard()
	if len(snap.formats) == 0 {
		t.Fatal("快照为空，说明 EnumClipboardFormats/GetClipboardData 有问题")
	}
	var hasText bool
	for _, f := range snap.formats {
		if f.format == cfUnicodeText {
			hasText = true
		}
	}
	if !hasText {
		t.Error("快照里应当包含 CF_UNICODETEXT")
	}

	// 模拟被我们发出的 Ctrl+C 覆盖掉
	if err := setClipboardText("覆盖掉的内容-XXXX"); err != nil {
		t.Fatalf("覆盖剪贴板失败: %v", err)
	}
	// 剪贴板是全局资源，写入到可读之间偶发观察不到（实测过）。
	// 加一个短重试，并在彻底失败时把现场打出来，别只报一个空字符串。
	var got string
	for i := 0; i < 10; i++ {
		if got, _ = readClipboardText(); got == "覆盖掉的内容-XXXX" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got != "覆盖掉的内容-XXXX" {
		avail, _, _ := pIsClipboardFormatAvailable.Call(cfUnicodeText)
		opened, _, oerr := pOpenClipboard.Call(0)
		if opened != 0 {
			pCloseClipboard.Call()
		}
		t.Fatalf("预期剪贴板已被覆盖，实际 %q（seq=%d 文本格式可用=%d OpenClipboard=%d err=%v）",
			got, clipboardSequence(), avail, opened, oerr)
	}

	// 还原
	if !snap.restore() {
		t.Fatal("快照还原失败")
	}
	got, ok := readClipboardText()
	if !ok {
		t.Fatal("还原后读不到文本")
	}
	if got != marker {
		t.Errorf("还原后内容 = %q，期望 %q", got, marker)
	}
	// 快照里的每个格式都应当回到剪贴板上
	for _, f := range snap.formats {
		if r, _, _ := pIsClipboardFormatAvailable.Call(uintptr(f.format)); r == 0 {
			t.Errorf("格式 %d 还原后不存在", f.format)
		}
	}
}

// 逐个格式快照必须能正确区分 HGLOBAL 和 GDI 句柄：
// 把 HBITMAP 当 HGLOBAL 去 GlobalLock 会拿到垃圾数据甚至崩。
func TestGlobalHandleFormatClassification(t *testing.T) {
	cases := map[uint32]bool{
		cfUnicodeText:  true,  // HGLOBAL
		1:              true,  // CF_TEXT
		8:              true,  // CF_DIB
		15:             true,  // CF_HDROP（复制的文件）
		0xC000:         true,  // 注册格式，一律 HGLOBAL
		0xC123:         true,  // HTML Format / RTF 之类
		cfBitmap:       false, // HBITMAP，GDI 对象
		cfPalette:      false, // HPALETTE，GDI 对象
		cfEnhMetafile:  false, // HENHMETAFILE，GDI 对象
		cfOwnerDisplay: false, // 由所有者自绘，句柄为 NULL
		cfDspBitmap:    false, // HBITMAP

		// CF_METAFILEPICT / CF_DSPMETAFILEPICT 常被误当成 GDI 句柄，
		// 实际传的是「装着 METAFILEPICT 结构的全局内存句柄」，是 HGLOBAL，
		// 可以按字节直接拷。别和 HENHMETAFILE 混。
		cfMetafilePict:    true,
		cfDspMetafilePict: true,
		0x0200:            false, // 私有句柄区
		0x0300:            false, // GDI 对象区
	}
	for f, want := range cases {
		if got := isGlobalHandleFormat(f); got != want {
			t.Errorf("isGlobalHandleFormat(%#x) = %v，期望 %v", f, got, want)
		}
	}
}

// 控制台判定必须准确：这是唯一会「打断用户正在跑的命令」的分支，
// 判错了后果比取不到词严重得多。
//
// 以前这里是 `_ = foregroundIsConsole()` —— 什么都没断言，属于摆设测试。
func TestIsConsoleClass(t *testing.T) {
	console := []string{
		"ConsoleWindowClass",            // conhost：cmd.exe / powershell.exe / WSL
		"CASCADIA_HOSTING_WINDOW_CLASS", // Windows Terminal
		"mintty",                        // Git Bash / MSYS2
	}
	for _, c := range console {
		if !isConsoleClass(c) {
			t.Errorf("%q 应当被判定为控制台", c)
		}
	}
	// 这些**不能**被判成控制台，否则会白白放弃取词
	notConsole := []string{
		"",
		"Chrome_WidgetWin_1", // VS Code / 浏览器：集成终端在里面，拦不住也不能拦
		"Notepad",
		"Chrome_WidgetWin_0",
		"consolewindowclass", // 类名比较是大小写敏感的
		"ConsoleWindowClassX",
	}
	for _, c := range notConsole {
		if isConsoleClass(c) {
			t.Errorf("%q 不该被判定为控制台", c)
		}
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

func TestSetAndReadClipboardRoundTrip(t *testing.T) {
	userSnap := snapshotClipboard()
	defer userSnap.restore()

	const want = "往返测试-中文与符号 !@#$%"
	if err := setClipboardText(want); err != nil {
		t.Skipf("写剪贴板失败: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	got, ok := readClipboardText()
	if !ok || got != want {
		t.Errorf("读回 %q (ok=%v)，期望 %q", got, ok, want)
	}
	if setClipboardText("") == nil {
		t.Error("空文本应当报错")
	}
}

// ---------------------------------------------------------------- MSAA

// 用一个自己创建的、内容已知的窗口来端到端验证 MSAA 通路。
//
// 这个测试的价值在于：vtable 槽位序号（accGetAccValue=11 / accGetAccName=10 /
// accGetAccRole=13 / accGetAccState=14 / accAccLocation=22）如果抄错一位，
// 这里要么直接崩、要么读到垃圾，不会静默通过。
func TestMSAAReadsTextFromRealControl(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := initMSAA(); err != nil {
		t.Fatalf("OleInitialize 失败: %v", err)
	}

	const cls = "NexusKBMSAATestWnd"
	const marker = "NexusKB-MSAA-TEST-8823"
	// 测试窗口自己的 WndProc：全部交给 DefWindowProc，
	// 避免和产品窗口的消息处理互相干扰
	testWndProc := syscall.NewCallback(func(hwnd, message, wparam, lparam uintptr) uintptr {
		r, _, _ := pDefWindowProcW.Call(hwnd, message, wparam, lparam)
		return r
	})

	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   testWndProc,
		HInstance:     getModuleHandle(),
		HbrBackground: 5, // COLOR_WINDOW+1，让窗口真的被画出来
		LpszClassName: utf16Ptr(cls),
	}
	if atom, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		t.Fatalf("RegisterClassExW 失败: %v", err)
	}

	// 放在工作区正中并置顶，保证 AccessibleObjectFromPoint 打得中
	wa := workArea(point{0, 0})
	x := (wa.Left + wa.Right) / 2
	y := (wa.Top + wa.Bottom) / 2
	w, h := int32(420), int32(140)

	hwnd, _, err := pCreateWindowExW.Call(
		uintptr(wsExTopmost),
		uintptr(unsafe.Pointer(utf16Ptr(cls))),
		uintptr(unsafe.Pointer(utf16Ptr("NexusKB MSAA test"))),
		uintptr(wsPopup),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		0, 0, getModuleHandle(), 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW 失败: %v", err)
	}
	defer pDestroyWindow.Call(hwnd)

	static, _, err := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr("Static"))),
		uintptr(unsafe.Pointer(utf16Ptr(marker))),
		// WS_VISIBLE 不能省：WindowFromPoint（MSAA 命中测试的底层）
		// 会跳过不可见窗口，少了它就只会打中父窗口
		uintptr(wsChild|wsVisible|ssLeft),
		24, 56, uintptr(w-48), 28,
		hwnd, 0, getModuleHandle(), 0)
	if static == 0 {
		t.Fatalf("创建 Static 控件失败: %v", err)
	}

	setWindowPos(hwnd, x, y, w, h, swpNoActivate|swpShowWindow)
	time.Sleep(600 * time.Millisecond) // 等它真的上屏

	// 打 Static 控件的中心
	target := point{x + 24 + (w-48)/2, y + 56 + 14}
	res, ok := msaaTextAt(target)
	if !ok {
		// 先确认 vtable 本身是好的：如果连可访问对象都取不到，
		// 那是环境问题（窗口被盖住）而不是我们抄错了槽位号；
		// 但只要能取到对象却读不出文本，那就必须失败，不能跳过。
		var probe uintptr
		var child variant
		hr, _, _ := pAccessibleObjectFromPoint.Call(packPoint(target),
			uintptr(unsafe.Pointer(&probe)), uintptr(unsafe.Pointer(&child)))
		if int32(hr) >= 0 && probe != 0 {
			comPtr(probe).release()
			child.clear()
			t.Fatalf("能取到可访问对象，却读不出文本 —— 这更像 vtable 槽位抄错了，不是环境问题")
		}
		t.Skipf("MSAA 在该环境下取不到可访问对象（前台窗口可能被盖住），跳过")
	}
	t.Logf("MSAA 取到: text=%q viaValue=%v bounds=%v hasBounds=%v",
		res.text, res.viaValue, res.bounds, res.hasBound)
	if !strings.Contains(res.text, marker) {
		t.Errorf("MSAA 取到 %q，期望包含 %q", res.text, marker)
	}
	if !res.hasBound {
		t.Error("应当能拿到 accLocation 的屏幕矩形")
	}
}

// VARIANT 在 x64 下必须是 24 字节：vt(2)+保留(6)+union(16)。
// 布局错了会把栈写坏 —— 这是那种「平时没事、偶尔崩」的 bug。
func TestVariantLayout(t *testing.T) {
	if got := unsafe.Sizeof(variant{}); got != 24 {
		t.Errorf("sizeof(variant) = %d，x64 下应为 24", got)
	}
	if off := unsafe.Offsetof(variant{}.Val); off != 8 {
		t.Errorf("variant.Val 偏移 = %d，应为 8", off)
	}
}

// 自检：MSAA 的 vtable 序号必须落在合理的函数地址上。
// 把每个槽位取出来，确认它们非零且互不相同 —— 顺序抄错时通常是
// 取到了相邻方法的地址，这条能提供一层廉价保护。
func TestMSAAVtableSlotsLookSane(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initMSAA(); err != nil {
		t.Skipf("OleInitialize 失败: %v", err)
	}
	pt := point{getSystemMetrics(0) / 2, getSystemMetrics(1) / 2}
	var acc uintptr
	var child variant
	hr, _, _ := pAccessibleObjectFromPoint.Call(packPoint(pt),
		uintptr(unsafe.Pointer(&acc)), uintptr(unsafe.Pointer(&child)))
	if int32(hr) < 0 || acc == 0 {
		t.Skipf("该环境取不到可访问对象")
	}
	a := comPtr(acc)
	defer a.release()
	defer child.clear()

	// 说明：这条只能证明「取到的地址是真实可执行代码」，**证明不了槽位顺序正确** ——
	// 顺序整体错一位时，六个地址依然互不相同、依然都可执行。
	// 真正守顺序的是端到端那条 TestMSAAReadsTextFromRealControl（它必须读出正确文本）。
	seen := map[uintptr]int{}
	slots := []int{vtblRelease, int(accGetAccName), int(accGetAccValue),
		int(accGetAccRole), int(accGetAccState), accAccLocation}
	for _, slot := range slots {
		addr := comVtblMethod(a, slot)
		if addr == 0 {
			t.Errorf("槽位 %d 的函数地址为 0", slot)
			continue
		}
		if !isExecutableAddress(addr) {
			t.Errorf("槽位 %d 的地址 %#x 不在可执行内存里，vtable 解引用可能越界", slot, addr)
		}
		if prev, dup := seen[addr]; dup {
			t.Errorf("槽位 %d 与槽位 %d 指向同一地址 %#x", slot, prev, addr)
		}
		seen[addr] = slot
	}
}

// ---------------------------------------------------------------- 采集服务

// 回归测试：采集服务的「忙碌判据」曾经用 atomic.Bool 表达，
// 零值是 false，而判据写成 CompareAndSwap(true, false)（要求当前为 true），
// 于是**第一次采集就被跳过、之后每一次都被跳过** —— 整个程序的取词功能全废，
// 而界面上只会静默地不弹菜单。
//
// 这个测试断言「第一个请求必须被处理」，那条 bug 会被当场抓住。
func TestCaptureServiceHandlesFirstRequest(t *testing.T) {
	var calls atomic.Int32
	svc := newCaptureService(
		func(captureRequest) captureResult {
			calls.Add(1)
			return captureResult{ok: true, text: "第一个请求的文本", method: "fake"}
		},
		func(captureResult) {},
	)
	svc.start()
	time.Sleep(60 * time.Millisecond) // 等 worker 阻塞在 channel 接收上

	res, got := svc.submit(captureRequest{how: "test"}, 500*time.Millisecond)
	if !got {
		t.Fatal("第一个请求就被跳过了 —— 忙碌判据写错了（这正是 captureIdle 那个 bug 的症状）")
	}
	if !res.ok || res.text != "第一个请求的文本" {
		t.Errorf("结果不对: ok=%v text=%q", res.ok, res.text)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("worker 被调用 %d 次，期望 1 次", n)
	}
}

// 采集线程正忙（模拟卡在某个程序的延迟渲染上）时，新请求必须被**立刻丢弃**，
// 而不是排队 —— 排队没有意义（等它腾出手用户早选了别的），
// 更要紧的是绝不能出现两次采集并发操作同一个剪贴板。
func TestCaptureServiceDropsWhenBusy(t *testing.T) {
	var block atomic.Bool
	release := make(chan struct{})
	var calls atomic.Int32

	svc := newCaptureService(
		func(captureRequest) captureResult {
			calls.Add(1)
			if block.Load() {
				<-release
			}
			return captureResult{ok: true, text: "T", method: "fake"}
		},
		func(captureResult) {},
	)
	svc.start()
	time.Sleep(60 * time.Millisecond)

	block.Store(true)
	// 这条会卡在 work 里，所以放后台跑
	go func() { _, _ = svc.submit(captureRequest{how: "blocking"}, 200*time.Millisecond) }()

	// 等 worker 真的进入阻塞态
	deadline := time.Now().Add(1 * time.Second)
	for calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() < 1 {
		close(release)
		t.Fatal("worker 没有开始处理请求")
	}

	if _, ok := svc.submit(captureRequest{how: "while-busy"}, 150*time.Millisecond); ok {
		close(release)
		t.Error("采集线程忙时不应接受新请求（会导致两次采集并发操作剪贴板）")
	}

	close(release)
	// 放开之后应当恢复接活
	block.Store(false)
	time.Sleep(150 * time.Millisecond)
	if _, ok := svc.submit(captureRequest{how: "after"}, 500*time.Millisecond); !ok {
		t.Error("采集线程空闲后应当能继续接活")
	}
}

// ---------------------------------------------------------------- 取文策略

// fakeSource 是注入用的假取文实现。
// 有了它才能断言「策略」本身 —— 不然只能拿开发者真实的剪贴板当 fixture。
type fakeSource struct {
	id        sourceID
	available bool
	sel       selection
	err       error
	calls     *int32
}

func (f fakeSource) Name() sourceID                { return f.id }
func (f fakeSource) Available(captureContext) bool { return f.available }
func (f fakeSource) Read(captureContext, point) (selection, error) {
	if f.calls != nil {
		atomic.AddInt32(f.calls, 1)
	}
	return f.sel, f.err
}

func TestPipelinePrefersFirstAvailableSource(t *testing.T) {
	var secondCalls int32
	p := &capturePipeline{sources: []textSource{
		fakeSource{id: "first", available: true, sel: selection{Text: "第一"}},
		fakeSource{id: "second", available: true, sel: selection{Text: "第二"}, calls: &secondCalls},
	}}
	sel, err := p.ReadIn(captureContext{}, point{})
	if err != nil || sel.Text != "第一" {
		t.Fatalf("应取第一个可用的源，得到 %q err=%v", sel.Text, err)
	}
	if sel.Source != "first" {
		t.Errorf("Source 标记应为 first，实际 %q", sel.Source)
	}
	if secondCalls != 0 {
		t.Error("第一个源成功时不该去调第二个（那会白付一次 Ctrl+C 的副作用）")
	}
}

func TestPipelineSkipsUnavailableSource(t *testing.T) {
	var clipboardCalls int32
	p := &capturePipeline{sources: []textSource{
		fakeSource{id: "cb", available: false, sel: selection{Text: "不该被用到"}, calls: &clipboardCalls},
		fakeSource{id: "msaa", available: true, sel: selection{Text: "兜底"}},
	}}
	sel, err := p.ReadIn(captureContext{}, point{})
	if err != nil || sel.Text != "兜底" {
		t.Fatalf("应跳到可用的源，得到 %q err=%v", sel.Text, err)
	}
	if clipboardCalls != 0 {
		t.Error("不可用的源绝不能被调用")
	}
}

// 这是唯一会「打断用户正在运行的命令」的分支，必须准确。
func TestClipboardSourceRefusesInConsole(t *testing.T) {
	console := captureContext{ForegroundClass: "ConsoleWindowClass", IsConsole: true}
	if (clipboardSource{}).Available(console) {
		t.Error("真控制台里剪贴板源必须不可用：合成 Ctrl+C 会被透传给 shell，" +
			"把用户正在跑的命令打断")
	}
	if (msaaSource{}).Available(console) {
		t.Error("真控制台里 MSAA 源必须不可用：实测它返回的是控制台窗口自身信息，不是选区")
	}
	// 普通窗口里两者都应该可用
	normal := captureContext{ForegroundClass: "Chrome_WidgetWin_1"}
	if !(clipboardSource{}).Available(normal) || !(msaaSource{}).Available(normal) ||
		!(uiaSource{}).Available(normal) {
		t.Error("普通窗口里三个源都应可用")
	}
}

// 「确实没有选区」与「读取失败」必须能分开 —— 前者不该报错，后者需要让用户知道。
func TestPipelineDistinguishesNoSelectionFromFailure(t *testing.T) {
	noSel := &capturePipeline{sources: []textSource{
		fakeSource{id: "a", available: true, err: ErrNoSelection},
		fakeSource{id: "b", available: true, err: ErrUnsupported},
	}}
	if _, err := noSel.ReadIn(captureContext{}, point{}); !errors.Is(err, ErrNoSelection) {
		t.Errorf("期望 ErrNoSelection，得到 %v", err)
	}

	failed := &capturePipeline{sources: []textSource{
		fakeSource{id: "a", available: true, err: ErrReadFailed},
	}}
	if _, err := failed.ReadIn(captureContext{}, point{}); !errors.Is(err, ErrReadFailed) {
		t.Errorf("期望 ErrReadFailed，得到 %v", err)
	}
}

// 空白文本不算取到 —— 源返回 selection{Text:"  "}, nil 时必须继续往下试。
func TestPipelineRejectsBlankText(t *testing.T) {
	p := &capturePipeline{sources: []textSource{
		fakeSource{id: "blank", available: true, sel: selection{Text: "   \n\t "}},
		fakeSource{id: "real", available: true, sel: selection{Text: "真正的文本"}},
	}}
	sel, err := p.ReadIn(captureContext{}, point{})
	if err != nil || sel.Text != "真正的文本" {
		t.Fatalf("空白文本应当被跳过，得到 %q err=%v", sel.Text, err)
	}
}

// 选区矩形必须能从 source 一路带出来 —— 这是接 UIA 的主要收益
// （弹窗贴住选区末尾，而不是猜鼠标抬起点）。
func TestPipelineCarriesBoundsThrough(t *testing.T) {
	p := &capturePipeline{sources: []textSource{
		fakeSource{id: "uia", available: true, sel: selection{
			Text: "有矩形", Bounds: rect{10, 20, 110, 40}, HasBounds: true,
		}},
	}}
	sel, err := p.ReadIn(captureContext{}, point{})
	if err != nil {
		t.Fatal(err)
	}
	if !sel.HasBounds || sel.Bounds != (rect{10, 20, 110, 40}) {
		t.Errorf("矩形没带出来: hasBounds=%v bounds=%v", sel.HasBounds, sel.Bounds)
	}
}

// 生产用的默认 pipeline：顺序就是优先级，剪贴板必须排在 MSAA 前面。
func TestDefaultPipelineOrder(t *testing.T) {
	src := capturePipelineDefault.sources
	if len(src) < 3 {
		t.Fatalf("默认 pipeline 至少要有三个源，实际 %d 个", len(src))
	}
	if src[0].Name() != sourceClipboard {
		t.Errorf("第一个源必须是剪贴板（只有它能拿到用户真正拖选的那一段），实际 %q", src[0].Name())
	}
	if src[1].Name() != sourceUIA {
		t.Errorf("第二个源应为 UIA（精确选区 + 选区矩形，且不发 Ctrl+C），实际 %q", src[1].Name())
	}
	if src[2].Name() != sourceMSAA {
		t.Errorf("第三个源应为 MSAA 兜底，实际 %q", src[2].Name())
	}
}

// ---------------------------------------------------------------- UIA

// createEditTestWindow 建一个带 EDIT 控件的窗口，返回窗口句柄与控件句柄。
// UIA 的 TextPattern 是「选区」级的，所以必须有真控件才能验。
func createEditTestWindow(t *testing.T, text string) (hwnd, edit uintptr) {
	t.Helper()
	const cls = "NexusKBUIATestWnd"
	testWndProc := syscall.NewCallback(func(h, m, w, l uintptr) uintptr {
		r, _, _ := pDefWindowProcW.Call(h, m, w, l)
		return r
	})
	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   testWndProc,
		HInstance:     getModuleHandle(),
		HbrBackground: 5,
		LpszClassName: utf16Ptr(cls),
	}
	// 同一个测试进程里注册两次会返回 ERROR_CLASS_ALREADY_EXISTS(1410)，
	// 那是正常的（类本来就该只注册一次），不算失败。
	if atom, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		if err != syscall.Errno(1410) {
			t.Fatalf("RegisterClassExW 失败: %v", err)
		}
	}

	wa := workArea(point{0, 0})
	x := (wa.Left + wa.Right) / 2
	y := (wa.Top + wa.Bottom) / 2
	w, h := int32(460), int32(160)

	hwnd, _, err := pCreateWindowExW.Call(
		uintptr(wsExTopmost),
		uintptr(unsafe.Pointer(utf16Ptr(cls))),
		uintptr(unsafe.Pointer(utf16Ptr("NexusKB UIA test"))),
		uintptr(wsPopup),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		0, 0, getModuleHandle(), 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW 失败: %v", err)
	}

	// 用 RichEdit 而不是标准 Edit：标准 EDIT 只提供 ValuePattern，
	// **不提供 TextPattern**，而 TextPattern 才是「选区」级的接口。
	// RICHEDIT50W 由 Msftedit.dll 提供，要先把它加载起来。
	pLoadLibraryW.Call(uintptr(unsafe.Pointer(utf16Ptr("Msftedit.dll"))))

	// WS_VISIBLE 不能省（WindowFromPoint 会跳过不可见窗口）
	edit, _, err = pCreateWindowExW.Call(
		0x00000200, // WS_EX_CLIENTEDGE
		uintptr(unsafe.Pointer(utf16Ptr("RICHEDIT50W"))),
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(wsChild|wsVisible|0x0080|0x0004|0x1000), // VSCROLL|MULTILINE|ES_AUTOVSCROLL
		20, 20, uintptr(w-40), uintptr(h-60),
		hwnd, 0, getModuleHandle(), 0)
	if edit == 0 {
		pDestroyWindow.Call(hwnd)
		t.Fatalf("创建 EDIT 控件失败: %v", err)
	}

	setWindowPos(hwnd, x, y, w, h, swpNoActivate|swpShowWindow)
	time.Sleep(500 * time.Millisecond)
	return hwnd, edit
}

// 这一层最容易因为 vtable 槽位抄错而崩或返回垃圾。
// 用「控制类型必须是合理值」来体检 IUIAutomationElement 的槽位偏移。
func TestUIAElementVtableOffsets(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initMSAA(); err != nil {
		t.Skipf("OleInitialize 失败: %v", err)
	}
	if err := initUIA(); err != nil {
		t.Skipf("该环境拿不到 CUIAutomation: %v", err)
	}

	hwnd, edit := createEditTestWindow(t, "NexusKB-UIA-OFFSET-CHECK")
	defer pDestroyWindow.Call(hwnd)

	r, _ := windowRect(edit)
	target := point{(r.Left + r.Right) / 2, (r.Top + r.Bottom) / 2}

	elems := uiaCandidateElements(target)
	if len(elems) == 0 {
		t.Skipf("拿不到任何候选元素（可能被别的窗口盖住）")
	}
	defer func() {
		for _, e := range elems {
			e.release()
		}
	}()
	el := elems[len(elems)-1] // 命中测试的那个（Edit 控件本身）
	t.Logf("候选元素 %d 个", len(elems))

	// 槽位取错会读到别的属性，值就不可能是合理的控件类型
	ct, ok := el.controlType()
	if !ok {
		t.Fatal("读不到 ControlType —— IUIAutomationElement 的槽位可能错位")
	}
	t.Logf("命中元素控件类型 = %d", ct)
	if ct < 50000 || ct > 50060 {
		t.Errorf("控件类型 %d 不是合法的 UIA 控件类型，槽位大概率错位了", ct)
	}

	// 密码位必须可读且为 false（这不是密码框）
	if pwd, ok := el.isPassword(); ok && pwd {
		t.Error("普通 EDIT 被判成了密码框 —— IsPassword 槽位可能错位")
	}

	// 单验 GetCurrentPattern 的槽位：EDIT 一定有 ValuePattern。
	// 拿不到就说明槽位 16 错了（这一步不依赖 TextPattern 是否存在）。
	if _, ok := el.currentPattern(uiaValuePatternId); !ok {
		t.Error("连 ValuePattern 都拿不到 —— IUIAutomationElement 的 GetCurrentPattern 槽位错了")
	}
}

// 端到端：给 EDIT 控件设一个已知选区，用 UIA 读回来。
//
// 这条同时验证 IUIAutomationTextPattern / TextRangeArray / TextRange 三层的槽位，
// 以及 BSTR 解码与 GetBoundingRectangles 的 SAFEARRAY 读取。
func TestUIAReadsRealSelection(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initMSAA(); err != nil {
		t.Skipf("OleInitialize 失败: %v", err)
	}
	if err := initUIA(); err != nil {
		t.Skipf("该环境拿不到 CUIAutomation: %v", err)
	}

	const full = "AAA-BBB-CCC-DDD"
	hwnd, edit := createEditTestWindow(t, full)
	defer pDestroyWindow.Call(hwnd)

	// 让控件真的拿到焦点。UIA 的 GetSelection 取的是「当前选区」，
	// 而很多 provider 在控件没有焦点时会返回一个退化区间甚至整篇内容 ——
	// 这一点很重要：真实划词时源程序本来就是前台，所以这不是限制，
	// 但测试环境里必须显式模拟出来。
	pSetForegroundWindow.Call(hwnd)
	pSetFocus.Call(edit)
	time.Sleep(150 * time.Millisecond)

	// 选中 "BBB"（4..7）
	const (
		emSetSel = 0x00B1
		emGetSel = 0x00B0
	)
	pSendMessageW.Call(edit, emSetSel, 4, 7)
	time.Sleep(200 * time.Millisecond)

	// 先确认控件自己的选区确实是 4..7（排除「EM_SETSEL 没生效」这条岔路）
	sel, _, _ := pSendMessageW.Call(edit, emGetSel, 0, 0)
	start, end := int32(uint16(sel&0xFFFF)), int32(uint16((sel>>16)&0xFFFF))
	t.Logf("控件自报选区 = [%d,%d)", start, end)
	if start != 4 || end != 7 {
		t.Fatalf("EM_SETSEL 没生效（控件自报 [%d,%d)），后面的断言没有意义", start, end)
	}

	r, _ := windowRect(edit)
	target := point{(r.Left + r.Right) / 2, (r.Top + r.Bottom) / 2}

	res, err := uiaSelectionAt(target)
	if err != nil {
		t.Fatalf("UIA 读选区失败: %v", err)
	}
	t.Logf("UIA 读到: text=%q bounds=%v hasBounds=%v", res.text, res.bounds, res.hasBounds)
	if res.text != "BBB" {
		t.Errorf("期望选中 %q，实际 %q", "BBB", res.text)
	}
	if !res.hasBounds {
		t.Error("应当能拿到选区矩形 —— 这正是 UIA 相对剪贴板法的主要收益")
	}
}

// uiaSlotProbe 用来从命令行指定要探测的 vtable 槽位。
//
// 用 flag 而不是环境变量：经 WSL interop 启动时，Linux 侧的环境变量
// 默认不会传给 Windows 进程（只有 WSLENV 里列出的才会），实测踩过。
var uiaSlotProbe = flag.Int("uia-slot", 0, "把哪个 IUIAutomationTextPattern 槽位当 GetSelection 调用来探测")

// 机械探测：把某个槽位当作 GetSelection 调一次，把结果打出来。
// 槽位号从环境变量 NKB_SLOT 读 —— 每个槽位单独跑一个进程，
// 这样即使某个槽位是错的（会直接崩），也只影响那一次探测。
func TestUIASlotProbe(t *testing.T) {
	if *uiaSlotProbe == 0 {
		t.Skip("未指定 -uia-slot")
	}
	slot := *uiaSlotProbe

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initMSAA(); err != nil {
		t.Skipf("OleInitialize 失败: %v", err)
	}
	if err := initUIA(); err != nil {
		t.Skipf("拿不到 CUIAutomation: %v", err)
	}
	hwnd, edit := createEditTestWindow(t, "AAA-BBB-CCC-DDD")
	defer pDestroyWindow.Call(hwnd)
	pSetForegroundWindow.Call(hwnd)
	pSetFocus.Call(edit)
	time.Sleep(150 * time.Millisecond)
	pSendMessageW.Call(edit, 0x00B1, 4, 7)
	time.Sleep(200 * time.Millisecond)

	r, _ := windowRect(edit)
	target := point{(r.Left + r.Right) / 2, (r.Top + r.Bottom) / 2}

	elems := uiaCandidateElements(target)
	if len(elems) == 0 {
		t.Skip("拿不到元素")
	}
	el := elems[0]
	defer el.release()

	tp, ok := el.currentPattern(uiaTextPatternId)
	if !ok {
		t.Skip("无 TextPattern")
	}
	defer tp.release()

	t.Logf("把槽位 %d 当 GetSelection 调用…", slot)
	var arr uintptr
	hr, _, _ := syscall.SyscallN(comVtblMethod(tp, slot),
		uintptr(tp), uintptr(unsafe.Pointer(&arr)))
	t.Logf("  hr=0x%08X arr=%#x", uint32(hr), arr)
	if int32(hr) < 0 || arr == 0 {
		return
	}
	ra := comPtr(arr)
	defer ra.release()
	var n int32
	scriptHr, _, _ := syscall.SyscallN(comVtblMethod(ra, textRangeArrayGetLength),
		uintptr(ra), uintptr(unsafe.Pointer(&n)))
	t.Logf("  (把返回值当数组读长度) hr=0x%08X n=%d", uint32(scriptHr), n)
	for k := int32(0); k < n && k < 3; k++ {
		var rng uintptr
		syscall.SyscallN(comVtblMethod(ra, textRangeArrayGetElement),
			uintptr(ra), uintptr(k), uintptr(unsafe.Pointer(&rng)))
		if rng == 0 {
			continue
		}
		rg := comPtr(rng)
		var bstr uintptr
		syscall.SyscallN(comVtblMethod(rg, textRangeGetText),
			uintptr(rg), uintptr(uint32(0xFFFFFFFF)), uintptr(unsafe.Pointer(&bstr)))
		txt := ""
		if bstr != 0 {
			txt = bstrToString(bstr)
			pSysFreeString.Call(bstr)
		}
		raw := textRangeRects(rg)
		b, okb := selectionBounds(raw)
		t.Logf("    段 %d: text=%.30q bounds=%v ok=%v 原始矩形 %d 个: %v", k, txt, b, okb, len(raw), raw)
		rg.release()
	}
}
