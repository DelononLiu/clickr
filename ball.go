//go:build windows

// ball.go —— 悬浮球：绘制、位置更新、位置持久化。

package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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

func posFile() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "NexusKB", "ball.pos")
}

func loadBallPos() (point, bool) {
	f := posFile()
	if f == "" {
		return point{}, false
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return point{}, false
	}
	parts := strings.Fields(strings.TrimSpace(string(b)))
	if len(parts) != 2 {
		return point{}, false
	}
	x, err1 := strconv.Atoi(parts[0])
	y, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return point{}, false
	}
	return point{int32(x), int32(y)}, true
}

func saveBallPos(pt point) {
	f := posFile()
	if f == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		log.Printf("[ui] 保存位置失败: %v", err)
		return
	}
	data := strconv.Itoa(int(pt.X)) + " " + strconv.Itoa(int(pt.Y))
	if err := os.WriteFile(f, []byte(data), 0o644); err != nil {
		log.Printf("[ui] 保存位置失败: %v", err)
	}
}
