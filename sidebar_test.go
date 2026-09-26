//go:build windows

// sidebar_test.go —— 右侧边栏的几何、命中与换行。
//
// 侧边栏是"只能靠看"的东西（内容随选中文字变），所以这里守的是能算的部分：
// 卡片贴在工作区右缘、行不越界不重叠、命中与绘制同源、换行不超宽。

package main

import "testing"

func TestSidebarDocksToRightEdgeInsideWorkArea(t *testing.T) {
	for _, sc := range []float64{1.0, 1.25, 2.0} {
		scale = sc
		wa := workArea(point{0, 0})
		card := sidebarCardRect()
		pad := int32(scaled(settingsShadowPad()))
		pos := placeSidebar()

		if card.Left < wa.Left || card.Top < wa.Top || card.Right > wa.Right || card.Bottom > wa.Bottom {
			t.Errorf("scale=%.2f：卡片 %v 超出工作区 %v", sc, card, wa)
		}
		// 贴右缘：右边留白正好是 sidebarMargin
		if gap := wa.Right - card.Right; gap != int32(scaled(sidebarMargin)) {
			t.Errorf("scale=%.2f：离右缘 %d，期望 %.0f", sc, gap, scaled(sidebarMargin))
		}
		if gap := card.Top - wa.Top; gap != int32(scaled(sidebarMargin)) {
			t.Errorf("scale=%.2f：离上缘 %d，期望 %.0f", sc, gap, scaled(sidebarMargin))
		}
		// 窗口 = 卡片 + 两侧留白，且窗口左上角就是 card - pad
		if w, h := sidebarWindowSize(); w != card.width()+2*pad || h != card.height()+2*pad {
			t.Errorf("scale=%.2f：窗口 %dx%d，期望 %dx%d", sc, w, h, card.width()+2*pad, card.height()+2*pad)
		}
		if pos.X != card.Left-pad || pos.Y != card.Top-pad {
			t.Errorf("scale=%.2f：窗口位置 %v，期望 (%d,%d)", sc, pos, card.Left-pad, card.Top-pad)
		}
	}
}

func TestSidebarRowsAndFooterFitInsideCard(t *testing.T) {
	scale = 1.0
	setLastSelection("一段示例文字")
	defer setLastSelection("")

	pad := int32(scaled(settingsShadowPad()))
	cardR := pad + int32(sidebarCardWidth())
	_, winH := sidebarWindowSize()
	cardB := pad + winH - 2*pad

	rows := sidebarActionRects()
	if len(rows) != len(sidebarActions()) {
		t.Fatalf("行数 %d 与矩形数 %d 不一致", len(sidebarActions()), len(rows))
	}
	var prev rect
	for i, r := range rows {
		if r.Left < pad || r.Right > cardR || r.Top < pad || r.Bottom > cardB {
			t.Errorf("第 %d 行动作行越出卡片: %v（卡片 x %d..%d y %d..%d）", i, r, pad, cardR, pad, cardB)
		}
		if i > 0 && r.Top < prev.Bottom {
			t.Errorf("第 %d 行与上一行重叠: %v / %v", i, prev, r)
		}
		prev = r
	}
	if len(rows) == 0 {
		t.Fatal("没有动作行")
	}
	// 底部那条分隔线 + 两行说明必须还在卡片里
	footerY := float64(prev.Bottom) + scaled(12)
	if need := footerY + scaled(10) + scaled(18) + scaled(30); need > float64(cardB) {
		t.Errorf("底部说明画到卡片外了：需要到 y=%.0f，卡片只到 %d", need, cardB)
	}
}

func TestSidebarHitMatchesDrawing(t *testing.T) {
	scale = 1.0
	setLastSelection("一段示例文字")
	defer setLastSelection("")

	cr := sidebarCloseRect()
	if got := sidebarHit(point{(cr.Left + cr.Right) / 2, (cr.Top + cr.Bottom) / 2}); got != hitSidebarClose {
		t.Errorf("关闭按钮中心命中 %d，期望 %d", got, hitSidebarClose)
	}
	for i, r := range sidebarActionRects() {
		c := point{(r.Left + r.Right) / 2, (r.Top + r.Bottom) / 2}
		if got := sidebarHit(c); got != i {
			t.Errorf("第 %d 行中心命中 %d", i, got)
		}
	}
	// 标题区（关闭按钮左边）：什么都不命中
	if got := sidebarHit(point{cr.Left - 80, (cr.Top + cr.Bottom) / 2}); got != -1 {
		t.Errorf("标题区命中 %d，期望 -1", got)
	}
}

func TestWrapTextFitsWidthAndLines(t *testing.T) {
	scale = 1.0
	f := font(scaledI(sidebarBodyPx), fwNormal)
	maxW := scaled(200)

	long := "这是一段很长的中文用于验证换行会不会超出给定的宽度限制并且最多只给四行剩下的用省略号收尾"
	lines := wrapText(long, maxW, 4, f)
	if len(lines) == 0 || len(lines) > 4 {
		t.Fatalf("行数 %d，期望 1..4", len(lines))
	}
	for i, ln := range lines {
		if w := float64(textWidth(ln, f)); w > maxW+1 {
			t.Errorf("第 %d 行宽 %.0f 超过上限 %.0f: %q", i, w, maxW, ln)
		}
	}
	lastRunes := []rune(lines[len(lines)-1])
	if len([]rune(join(lines))) < len([]rune(long)) && lastRunes[len(lastRunes)-1] != '…' {
		t.Errorf("内容没放完，最后一行应当以省略号收尾: %q", lines[len(lines)-1])
	}

	if got := wrapText("", maxW, 4, f); got != nil {
		t.Errorf("空字符串应当返回 nil，实际 %v", got)
	}
	// 短文本一行放得下就不该被截
	if got := wrapText("短", maxW, 4, f); len(got) != 1 || got[0] != "短" {
		t.Errorf("短文本被改动了: %v", got)
	}
}

func join(ss []string) string {
	out := ""
	for _, s := range ss {
		out += s
	}
	return out
}

// 侧边栏的动作行必须与菜单一致：设置页里关掉的技能，两边都不该出现。
func TestSidebarActionsFollowSkillToggles(t *testing.T) {
	scale = 1.0
	save := enabledSkillIDsCached()
	defer setSkillEnabledIDs(save)

	before := len(sidebarActions())
	setSkillEnabled("copy", false)
	after := len(sidebarActions())
	if after != before-1 {
		t.Errorf("关掉一个技能后侧边栏动作 %d 项（原 %d），应当少一项", after, before)
	}
	for _, it := range sidebarActions() {
		if it.title == "复制" {
			t.Error("关掉的技能仍然出现在侧边栏里")
		}
	}
	setSkillEnabled("copy", true)
}
