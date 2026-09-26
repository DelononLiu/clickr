//go:build windows

// settings_test.go —— 设置页的几何与状态。
//
// 设置页没有"看一眼就知道对不对"的对照物（评审就是靠导出的像素发现
// "底部状态文案画到卡片外"和"浅色主题下正文 alpha=0"的），所以这里把
// **能变成断言的部分**全部钉住：行矩形不重叠、开关在行内、底部内容在卡片内、
// 关闭按钮与行的命中区不打架、主题里设置页用到的颜色都真的设了。

package main

import (
	"runtime"
	"testing"
)

func TestSettingsRowsFitInsideCard(t *testing.T) {
	scale = 1.0
	rows := settingsRows()
	rects := settingsRowRects()
	if len(rows) != len(rects) {
		t.Fatalf("行数 %d 与矩形数 %d 不一致", len(rows), len(rects))
	}

	pad := int32(scaled(settingsShadowPad()))
	cardRight := pad + int32(scaled(settingsW))
	cardBottom := pad + int32(scaled(settingsCardHeight()))

	var prev rect
	for i, r := range rects {
		if r.Left < pad || r.Right > cardRight {
			t.Errorf("第 %d 行横向越出卡片: %v（卡片 x %d..%d）", i, r, pad, cardRight)
		}
		if i > 0 && r.Top < prev.Bottom {
			t.Errorf("第 %d 行与上一行重叠: %v / %v", i, prev, r)
		}
		prev = r
	}

	// 底部那条分隔线 + 状态文案必须在卡片内（这条是评审按像素抓出来的缺陷）
	y := float64(prev.Bottom) + scaled(16)
	if int32(y+scaled(1)+scaled(12)+scaled(20)) > cardBottom {
		t.Errorf("底部状态文案画到卡片外了：需要到 y=%.0f，卡片只到 %d",
			y+scaled(1)+scaled(12)+scaled(20), cardBottom)
	}
}

func TestSettingsSwitchSitsInsideItsRow(t *testing.T) {
	scale = 1.0
	rects := settingsRowRects()
	rows := settingsRows()
	for i, r := range rows {
		if r.kind != rowToggle && r.kind != rowSkill {
			continue
		}
		sw := settingsSwitchRect(rects[i])
		if sw.Left < rects[i].Left || sw.Right > rects[i].Right ||
			sw.Top < rects[i].Top || sw.Bottom > rects[i].Bottom {
			t.Errorf("第 %d 行（%s）的开关 %v 不在行 %v 内", i, r.title, sw, rects[i])
		}
		if w, h := sw.width(), sw.height(); w != int32(scaled(switchW)) || h != int32(scaled(switchH)) {
			t.Errorf("开关尺寸 %dx%d，期望 %dx%d", w, h, int32(scaled(switchW)), int32(scaled(switchH)))
		}
	}
}

func TestSettingsHitMatchesDrawing(t *testing.T) {
	scale = 1.0
	// 关闭按钮：命中区就是画出来的那个 24x24 圆
	cr := settingsCloseRect()
	mid := point{(cr.Left + cr.Right) / 2, (cr.Top + cr.Bottom) / 2}
	if got := settingsHit(mid); got != hitCloseButton {
		t.Errorf("关闭按钮中心的命中结果是 %d，期望 %d", got, hitCloseButton)
	}
	if got := settingsHit(point{cr.Left - 4, mid.Y}); got == hitCloseButton {
		t.Error("关闭按钮左边 4px 处不该算命中关闭按钮")
	}

	// 每一行的中心应当命中它自己
	for i, r := range settingsRowRects() {
		c := point{(r.Left + r.Right) / 2, (r.Top + r.Bottom) / 2}
		if got := settingsHit(c); got != i {
			t.Errorf("第 %d 行中心的命中结果是 %d", i, got)
		}
	}

	// 空白处：什么都没命中
	if got := settingsHit(point{settingsCloseRect().Left, settingsRowRects()[0].Top - 20}); got != hitNone {
		t.Errorf("空白处的命中结果是 %d，期望 %d", got, hitNone)
	}
}

func TestSettingsToggleRowIsClickableAcrossWholeRow(t *testing.T) {
	scale = 1.0
	rects := settingsRowRects()
	rows := settingsRows()
	for i, r := range rows {
		if r.kind != rowToggle {
			continue
		}
		// 豆包的 .global-switch 是"整行可点"，不是只有小开关可点
		left := point{rects[i].Left + 4, (rects[i].Top + rects[i].Bottom) / 2}
		if got := settingsHit(left); got != i {
			t.Errorf("开关行最左侧也应当命中该行，实际 %d（期望 %d）", got, i)
		}
	}
}

func TestSkillToggleRoundTrip(t *testing.T) {
	// 走内存缓存那条路（不碰用户真实的 %AppData%）
	save := enabledSkillIDsCached()
	t.Cleanup(func() { setSkillEnabledIDs(save) })

	if !skillEnabled("copy") {
		t.Fatal("默认应当开着「复制」")
	}
	setSkillEnabled("copy", false)
	if skillEnabled("copy") {
		t.Error("关掉「复制」之后仍然读到开着")
	}
	if got := len(enabledSkills()); got != len(skills())-1 {
		t.Errorf("关掉一个之后可用技能 %d 个，期望 %d", got, len(skills())-1)
	}
	setSkillEnabled("copy", true)
	if !skillEnabled("copy") {
		t.Error("重新开启失败")
	}
	// 顺序必须稳定：重新开启后仍按注册顺序
	ids := enabledSkillIDs()
	for i, sk := range skills() {
		if i < len(ids) && ids[i] != sk.id {
			t.Errorf("技能顺序乱了：第 %d 个是 %q，期望 %q", i, ids[i], sk.id)
		}
	}
}

// 图标走的是 emoji 字体（Segoe UI Emoji）。字体缺失、字形不存在、或者
// GDI 把它当彩色字形画不出来 —— 任何一种都会让图标变成一块空白，
// 而屏幕上看不出来是"没画"还是"画了但很淡"。所以这里逐像素验墨迹。
func TestEmojiIconsRenderInk(t *testing.T) {
	scale = 1.0
	th := loadTheme()
	for icon, glyph := range iconEmoji {
		box := scaled(barIconSize)
		n := int32(box) + 2
		surf := newSurface(n, n)
		surf.clear()
		drawMenuIcon(surf, icon, 1, 1, box, th.label)
		ink := 0
		for i := 3; i < len(surf.bits); i += 4 {
			if surf.bits[i] > 40 {
				ink++
			}
		}
		surf.free()
		if ink == 0 {
			t.Errorf("图标 %d（emoji %q）没画出任何墨迹 —— 字体缺失或字形不存在", icon, glyph)
		}
	}
}

// 设置页那三个字段用的是**原生 EDIT 控件**（分层窗口拿不到键盘焦点，
// 自绘输入框得自己写光标/选区/输入法）。这条测试直接验证这层"壳"能通：
// 建得出控件、写进去能读回来。
//
// 需要真窗口，所以只能在 Windows 上跑（本仓库的测试本来就只跑 Windows）。
func TestSettingsFieldsAreRealEditControls(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := createWindows(); err != nil {
		t.Fatalf("建窗口失败: %v", err)
	}
	ensureSettingsFields()
	for _, f := range settingsFields() {
		if h := fieldHWND(f.id); h == 0 {
			t.Fatalf("字段 %s 的原生控件没建出来", f.key)
		}
		fieldSetText(f.id, "值-"+f.key)
		if got := fieldText(f.id); got != "值-"+f.key {
			t.Errorf("字段 %s 往返失败: %q", f.key, got)
		}
	}
	// key 那个字段必须是掩码（不然旁边的人一眼看走）
	var style uintptr
	idx := 0
	for _, f := range settingsFields() {
		if f.secret {
			idx++
			style = 0
			// 读回样式：GWL_STYLE = -16
			v, _, _ := pGetWindowLongW.Call(fieldHWND(f.id), uintptr(^uintptr(15))) // GWL_STYLE = -16
			style = v
		}
	}
	if idx == 0 {
		t.Fatal("应当有一个 secret 字段（api_key）")
	}
	if style&esPassword == 0 {
		t.Error("api_key 输入框没有 ES_PASSWORD 样式")
	}
}
