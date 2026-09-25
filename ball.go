//go:build windows

// ball.go —— 悬浮球：绘制与位置更新。

package main

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
	r := scaled(ballSize) / 2

	if th.dark {
		ballSurf.shadowRoundRect(c-r, c-r, r*2, r*2, r,
			scaled(ballShadowBlur), scaled(ballShadowDY), rgba{0, 0, 0, 0.35})
	} else {
		ballSurf.shadowRoundRect(c-r, c-r, r*2, r*2, r,
			scaled(ballShadowBlur), scaled(ballShadowDY), rgba{0x8B, 0x92, 0xA0, 0.30})
	}
	ballSurf.fillCircle(c, c, r, th.ballBg)
	ballSurf.strokeCircle(c, c, r-0.5, scaled(1), th.ballBorder)

	// 中心品牌标记（参考有道那个红点：实测主色约 #F0142F）
	ballSurf.fillSparkle(c, c, scaled(ballMarkSize)/2, th.accent)
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
