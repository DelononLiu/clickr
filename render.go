//go:build windows

// render.go —— 分层窗口（WS_EX_LAYERED + UpdateLayeredWindow）的软件渲染器。
//
// 为什么要自己画：
//   - 想要「圆角 + 柔和阴影」，普通窗口做不到，得用逐像素 alpha 的分层窗口；
//   - 有道的菜单是 border-radius:16px + box-shadow: 0 5px 10px rgba(139,146,160,.14)，
//     这里用「圆角矩形的有符号距离场 + 高斯衰减」把同样的观感还原出来。
//
// 一个关键坑：GDI 往 32bpp DIB 上画东西时，alpha 字节不会被写入（一直是 0）。
// 而 UpdateLayeredWindow 用的是预乘 alpha，alpha=0 就等于完全透明 ——
// 直接用 DrawTextW 画字会一个字都看不见。
// 所以文字走「黑字白底 mask → 用亮度当覆盖率 → 手动合成」这条路。
package main

import (
	"log"
	"math"
	"sync"
	"syscall"
	"unsafe"
)

type rgba struct {
	R, G, B uint8
	A       float64
}

func (c rgba) withAlpha(a float64) rgba { c.A = a; return c }

// ================================================================ surface

type surface struct {
	w, h int32
	hdc  uintptr
	hbmp uintptr
	old  uintptr
	bits []byte
}

func newSurface(w, h int32) *surface {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)

	hdc, _, _ := pCreateCompatibleDC.Call(screen)

	bmi := bitmapInfo{}
	bmi.Header.BiSize = uint32(unsafe.Sizeof(bitmapInfoHeader{}))
	bmi.Header.BiWidth = w
	bmi.Header.BiHeight = -h // 负高度 = 自上而下的行序，免得每次手动翻行
	bmi.Header.BiPlanes = 1
	bmi.Header.BiBitCount = 32
	bmi.Header.BiCompression = biRGB

	var bits unsafe.Pointer
	hbmp, _, _ := pCreateDIBSection.Call(hdc, uintptr(unsafe.Pointer(&bmi)),
		dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbmp == 0 {
		panic("CreateDIBSection failed")
	}
	old, _, _ := pSelectObject.Call(hdc, hbmp)

	return &surface{
		w: w, h: h,
		hdc:  hdc,
		hbmp: hbmp,
		old:  old,
		bits: unsafe.Slice((*byte)(bits), int(w)*int(h)*4),
	}
}

func (s *surface) free() {
	if s.hdc == 0 {
		return
	}
	pSelectObject.Call(s.hdc, s.old)
	pDeleteObject.Call(s.hbmp)
	pDeleteDC.Call(s.hdc)
	s.hdc, s.hbmp, s.bits = 0, 0, nil
}

// blendPixel 做一次 source-over 合成（源和目标都是预乘 alpha）。
//
//	dst = src*a + dst*(1-a)
func (s *surface) blendPixel(i int, c rgba, a float64) {
	if a <= 0.001 {
		return
	}
	if a > 1 {
		a = 1
	}
	ia := 1 - a
	d := s.bits[i : i+4 : i+4]
	d[0] = clamp8(float64(c.B)*a + float64(d[0])*ia)
	d[1] = clamp8(float64(c.G)*a + float64(d[1])*ia)
	d[2] = clamp8(float64(c.R)*a + float64(d[2])*ia)
	d[3] = clamp8(a*255 + float64(d[3])*ia)
}

func clamp8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampI(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ================================================================ 距离场图元

// sdRoundRect 返回点到圆角矩形边界的有符号距离（内部为负）。
func sdRoundRect(px, py, cx, cy, hw, hh, r float64) float64 {
	if r > hw {
		r = hw
	}
	if r > hh {
		r = hh
	}
	qx := math.Abs(px-cx) - (hw - r)
	qy := math.Abs(py-cy) - (hh - r)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) +
		math.Min(math.Max(qx, qy), 0) - r
}

// fillRoundRect 填充一个抗锯齿圆角矩形。(x,y) 是左上角。
func (s *surface) fillRoundRect(x, y, w, h, r float64, c rgba) {
	if w <= 0 || h <= 0 || c.A <= 0 {
		return
	}
	cx, cy := x+w/2, y+h/2
	hw, hh := w/2, h/2

	x0 := clampI(int32(math.Floor(x-1)), 0, s.w-1)
	x1 := clampI(int32(math.Ceil(x+w+1)), 0, s.w-1)
	y0 := clampI(int32(math.Floor(y-1)), 0, s.h-1)
	y1 := clampI(int32(math.Ceil(y+h+1)), 0, s.h-1)

	for py := y0; py <= y1; py++ {
		fy := float64(py) + 0.5
		row := int(py) * int(s.w) * 4
		for px := x0; px <= x1; px++ {
			fx := float64(px) + 0.5
			d := sdRoundRect(fx, fy, cx, cy, hw, hh, r)
			cov := clamp01(0.5 - d)
			if cov > 0 {
				s.blendPixel(row+int(px)*4, c, c.A*cov)
			}
		}
	}
}

// strokeRoundRect 描一个抗锯齿圆角矩形边框，线宽约 thickness 像素。
func (s *surface) strokeRoundRect(x, y, w, h, r, thickness float64, c rgba) {
	if w <= 0 || h <= 0 || c.A <= 0 {
		return
	}
	cx, cy := x+w/2, y+h/2
	hw, hh := w/2, h/2
	half := thickness / 2

	x0 := clampI(int32(math.Floor(x-2)), 0, s.w-1)
	x1 := clampI(int32(math.Ceil(x+w+2)), 0, s.w-1)
	y0 := clampI(int32(math.Floor(y-2)), 0, s.h-1)
	y1 := clampI(int32(math.Ceil(y+h+2)), 0, s.h-1)

	for py := y0; py <= y1; py++ {
		fy := float64(py) + 0.5
		row := int(py) * int(s.w) * 4
		for px := x0; px <= x1; px++ {
			fx := float64(px) + 0.5
			d := math.Abs(sdRoundRect(fx, fy, cx, cy, hw, hh, r))
			cov := clamp01(half + 0.5 - d)
			if cov > 0 {
				s.blendPixel(row+int(px)*4, c, c.A*cov)
			}
		}
	}
}

// shadowReach 返回一个投影实际会扩散到的距离。
//
// 窗口尺寸必须比内容大出这么多，否则阴影会被窗口边界硬切，
// 在 alpha 还有明显数值的地方出现一条直边（实测肉眼可见）。
// 取 1.2*blur：此时高斯权重已降到 exp(-2.88) ≈ 5.6%，再乘阴影 alpha 后不可见。
func shadowReach(blur, dy float64) float64 {
	return blur*1.2 + math.Abs(dy) + 2
}

// shadowRoundRect 画投影：把圆角矩形下移 dy，再按高斯衰减往外扩散。
// 对应 CSS 的 box-shadow: 0 dy blur rgba(...)，其中 sigma = blur/2。
func (s *surface) shadowRoundRect(x, y, w, h, r, blur, dy float64, c rgba) {
	sigma := blur / 2
	if sigma <= 0 {
		sigma = 1
	}
	reach := shadowReach(blur, dy)
	cx, cy := x+w/2, y+h/2+dy
	hw, hh := w/2, h/2
	twoSigmaSq := 2 * sigma * sigma

	x0 := clampI(int32(math.Floor(x-reach)), 0, s.w-1)
	x1 := clampI(int32(math.Ceil(x+w+reach)), 0, s.w-1)
	y0 := clampI(int32(math.Floor(y-reach)), 0, s.h-1)
	y1 := clampI(int32(math.Ceil(y+h+reach)), 0, s.h-1)

	for py := y0; py <= y1; py++ {
		fy := float64(py) + 0.5
		row := int(py) * int(s.w) * 4
		for px := x0; px <= x1; px++ {
			fx := float64(px) + 0.5
			d := sdRoundRect(fx, fy, cx, cy, hw, hh, r)
			if d < 0 {
				d = 0
			}
			a := c.A * math.Exp(-(d*d)/twoSigmaSq)
			if a > 0.002 {
				s.blendPixel(row+int(px)*4, c, a)
			}
		}
	}
}

// clear 把整个表面清成完全透明（预乘 alpha 下的全 0）。
func (s *surface) clear() {
	clear(s.bits)
}

func sdCircle(px, py, cx, cy, r float64) float64 {
	return math.Hypot(px-cx, py-cy) - r
}

// fillSparkle 画一个四角星（✦），用作悬浮球的品牌标记。
//
// 形状取「两个透镜的并集」：
//   - 竖向的尖角 = 两个**水平**错开的圆的交集（窄而高）
//   - 横向的尖角 = 两个**垂直**错开的圆的交集（扁而宽）
//
// 两个透镜一叠，就得到边缘内凹的四角星。
// R 和 a 的取值由「横向半宽 = 0.18*rad、纵向半长 = rad」反解得到。
func (s *surface) fillSparkle(cx, cy, rad float64, c rgba) {
	if rad <= 0 || c.A <= 0 {
		return
	}
	R := rad * 2.868
	a := rad * 2.688

	x0 := clampI(int32(cx-rad-2), 0, s.w-1)
	x1 := clampI(int32(cx+rad+2), 0, s.w-1)
	y0 := clampI(int32(cy-rad-2), 0, s.h-1)
	y1 := clampI(int32(cy+rad+2), 0, s.h-1)

	for py := y0; py <= y1; py++ {
		fy := float64(py) + 0.5
		row := int(py) * int(s.w) * 4
		for px := x0; px <= x1; px++ {
			fx := float64(px) + 0.5
			dV := math.Max(sdCircle(fx, fy, cx-a, cy, R), sdCircle(fx, fy, cx+a, cy, R))
			dH := math.Max(sdCircle(fx, fy, cx, cy-a, R), sdCircle(fx, fy, cx, cy+a, R))
			d := math.Min(dV, dH)
			if cov := clamp01(0.5 - d); cov > 0 {
				s.blendPixel(row+int(px)*4, c, c.A*cov)
			}
		}
	}
}

// fillCircle / strokeCircle 是圆角矩形的特例，单独包一层方便读。
func (s *surface) fillCircle(cx, cy, rad float64, c rgba) {
	s.fillRoundRect(cx-rad, cy-rad, rad*2, rad*2, rad, c)
}

func (s *surface) strokeCircle(cx, cy, rad, thickness float64, c rgba) {
	s.strokeRoundRect(cx-rad, cy-rad, rad*2, rad*2, rad, thickness, c)
}

// fillLine 画一条带圆头的粗线（用于放大镜手柄等）。
func (s *surface) fillLine(x1, y1, x2, y2, thickness float64, c rgba) {
	dx, dy := x2-x1, y2-y1
	length := math.Hypot(dx, dy)
	if length == 0 {
		s.fillCircle(x1, y1, thickness/2, c)
		return
	}
	steps := int(length*2) + 1
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		s.fillCircle(x1+dx*t, y1+dy*t, thickness/2, c)
	}
}

// ================================================================ 字体与文字

var (
	fontMu    sync.Mutex
	fontCache = map[[2]int32]uintptr{}
)

// font 取一个按像素高度指定的字体句柄（带缓存）。
// 用 ANTIALIASED_QUALITY（灰度抗锯齿）而不是 ClearType：
// ClearType 会产生彩色子像素，破坏「用亮度当覆盖率」的取字方案。
func font(sizePx, weight int32) uintptr {
	key := [2]int32{sizePx, weight}
	fontMu.Lock()
	defer fontMu.Unlock()
	if h, ok := fontCache[key]; ok {
		return h
	}
	lf := logFontW{
		LfHeight:  -sizePx, // 负值 = 字符高度（和 CSS 的 px 概念一致）
		LfWeight:  weight,
		LfCharSet: defaultCharset,
		LfQuality: antialiasedQuality,
	}
	name, _ := syscall.UTF16FromString("Microsoft YaHei UI")
	copy(lf.LfFaceName[:], name)
	h, _, _ := pCreateFontIndirectW.Call(uintptr(unsafe.Pointer(&lf)))
	fontCache[key] = h
	return h
}

type glyphMask struct {
	w, h int32
	cov  []byte // 每个像素的覆盖率 0..255
}

var (
	maskMu    sync.Mutex
	maskCache = map[string]*glyphMask{}
)

// textMask 用 GDI 把文字渲染成「黑字白底」，再把亮度反相当作 alpha 覆盖率。
//
// 这样是为了绕开「GDI 不写 alpha 字节」的限制，同时保留抗锯齿。
func textMask(text string, maxW, lineH int32, hf uintptr) *glyphMask {
	cacheKey := text + "\x00" + itoa(int(maxW)) + "x" + itoa(int(lineH)) + "\x00" + itoa(int(hf))
	maskMu.Lock()
	if m, ok := maskCache[cacheKey]; ok {
		maskMu.Unlock()
		return m
	}
	maskMu.Unlock()

	s := newSurface(maxW, lineH)
	defer s.free()

	// 白底
	white, _, _ := pCreateSolidBrush.Call(0x00FFFFFF)
	r := rect{0, 0, maxW, lineH}
	pFillRect.Call(s.hdc, uintptr(unsafe.Pointer(&r)), white)
	pDeleteObject.Call(white)

	// 黑字
	utf16, _ := syscall.UTF16FromString(text)
	pSetBkMode.Call(s.hdc, transparentBkMode)
	pSetTextColor.Call(s.hdc, 0x00000000)
	pSelectObject.Call(s.hdc, hf)
	pDrawTextW.Call(s.hdc,
		uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)-1),
		uintptr(unsafe.Pointer(&r)),
		dtLeft|dtVCenter|dtSingleLine|dtNoPrefix|dtEndEllipsis)

	m := &glyphMask{w: maxW, h: lineH, cov: make([]byte, int(maxW)*int(lineH))}
	for i := 0; i < len(m.cov); i++ {
		m.cov[i] = 255 - s.bits[i*4+2] // R 通道：白 255 → 覆盖 0
	}

	maskMu.Lock()
	maskCache[cacheKey] = m
	maskMu.Unlock()
	return m
}

// drawText 把一段文字画到目标表面。(x,y) 是左上角。
func (s *surface) drawText(x, y int32, text string, hf uintptr, maxW, lineH int32, c rgba) {
	if text == "" || maxW <= 0 || lineH <= 0 {
		return
	}
	m := textMask(text, maxW, lineH, hf)
	for row := int32(0); row < m.h; row++ {
		dy := y + row
		if dy < 0 || dy >= s.h {
			continue
		}
		for col := int32(0); col < m.w; col++ {
			dx := x + col
			if dx < 0 || dx >= s.w {
				continue
			}
			cov := m.cov[row*m.w+col]
			if cov == 0 {
				continue
			}
			s.blendPixel(int(dy)*int(s.w)*4+int(dx)*4, c, c.A*float64(cov)/255)
		}
	}
}

// textWidth 量一段文字在当前字体下的宽度（用于让浮层跟着文字自适应）。
func textWidth(text string, hf uintptr) int32 {
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)
	hdc, _, _ := pCreateCompatibleDC.Call(screen)
	defer pDeleteDC.Call(hdc)
	pSelectObject.Call(hdc, hf)

	utf16, _ := syscall.UTF16FromString(text)
	var sz size
	pGetTextExtentPoint32W.Call(hdc,
		uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)-1),
		uintptr(unsafe.Pointer(&sz)))
	return sz.CX
}

// ================================================================ 呈现

// present 把表面的内容推到分层窗口上（同时决定窗口的位置和大小），并确保窗口可见。
func (s *surface) present(hwnd uintptr, x, y int32) {
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)

	dst := point{x, y}
	src := point{0, 0}
	sz := size{s.w, s.h}
	bf := blendFunction{
		BlendOp:             acSrcOver,
		BlendFlags:          0,
		SourceConstantAlpha: 255,
		AlphaFormat:         acSrcAlpha, // 预乘 alpha
	}
	ok, _, uerr := pUpdateLayeredWindow.Call(hwnd,
		screen,
		uintptr(unsafe.Pointer(&dst)),
		uintptr(unsafe.Pointer(&sz)),
		s.hdc,
		uintptr(unsafe.Pointer(&src)),
		0,
		uintptr(unsafe.Pointer(&bf)),
		ulwAlpha)
	if ok == 0 {
		// 这里以前是完全静默的，而正是这条路径出过
		// 「坐标正确、像素也画好了、屏幕上却什么都看不到」的故障。
		log.Printf("[render] UpdateLayeredWindow 失败 hwnd=%#x pos=(%d,%d) size=%dx%d: %v",
			hwnd, x, y, s.w, s.h, uerr)
	}

	// 关键一步：UpdateLayeredWindow 只负责「位置 / 尺寸 / 内容 / 透明度」，
	// **不会**把隐藏的窗口显示出来。
	// 少了下面这几行，窗口矩形正确、像素也画好了，但屏幕上什么都看不到。
	//
	// 用 SetWindowPos + SWP_SHOWWINDOW 而不是 ShowWindow(SW_SHOW)：
	// 只有前者能同时带上 SWP_NOACTIVATE，保证不抢焦点 ——
	// 一旦抢了焦点，源程序的选区高亮会消失，弹窗就白弹了。
	pSetWindowPos.Call(hwnd,
		^uintptr(0), // HWND_TOPMOST
		0, 0, 0, 0,
		uintptr(swpNoMove|swpNoSize|swpNoActivate|swpShowWindow))
}

// itoa 避免为了拼缓存 key 引入 strconv。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
