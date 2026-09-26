//go:build windows

// ball.go —— 悬浮球：绘制、位置更新、位置持久化。

package main

// 位置的读写（posFile / loadBallPos / saveBallPos）在 config.go：
// 它和「划词弹出」开关同属「用户设置要持久化」这一件事。

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
	// 悬停时球体放大一点，读起来就是"活的"。
	// ballHoverGrow 是**半径**增量（和 ballContains 的判定区同口径）。
	r := scaled(ballSize) / 2
	if ballHovered {
		r = scaled(ballSize/2 + ballHoverGrow)
	}

	// 投影：豆包 --s-shadow-level1（两层：一圈细晕 + 一层柔和的）。
	// 留白由 ballShadowPad() 从这两层里最远的那层反推，见 D10。
	for _, l := range ballShadowLayers {
		ballSurf.shadowRoundRect(c-r, c-r, r*2, r*2, r,
			scaled(l.blur), scaled(l.dy), l.c)
	}
	ballSurf.fillCircle(c, c, r, th.ballBg)
	// 悬停底色：豆包所有可点元素的 :hover 都是这一层 rgba(0,0,0,.06)（暗色下是白的 6%）
	if ballHovered {
		ballSurf.fillCircle(c, c, r, th.hover)
	}
	ballSurf.strokeCircle(c, c, r-0.5, scaled(1), th.ballBorder)

	// 中心标记：鸢尾蓝四角星。
	//
	// 这是本项目自己的标记 —— 仿豆包仿的是观感（圆球 + 柔和投影 + 一个干净的中心图形），
	// 不搬它的品牌图形。暗色底上鸢尾蓝对比不够，换成同色系的浅端。
	//
	// 「划词弹出」关掉时标记转中性灰：球就是那个开关的入口，状态必须看得见，
	// 否则用户关了之后就再也想不起来怎么开。
	mark := th.ballMark
	if !selectionPopupEnabled() {
		mark = th.ballMarkOff
	}
	ballSurf.fillSparkle(c, c, scaled(ballMarkSize)/2, mark)
}

// refreshBall 重新渲染并推到屏幕上。开关状态变了之后要调它 ——
// 球的像素是缓存着的，不重画就还是旧状态。
func refreshBall() {
	renderBall()
	showBall(constrainBall(ballPos))
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
