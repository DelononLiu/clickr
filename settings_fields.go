//go:build windows

// settings_fields.go —— 设置页里的**原生输入框**（AI 接口的 url / key / 模型）。
//
// 为什么这里出现了原生控件，而别处全是自绘：
//
//	我们的窗口是"绝不抢焦点"的分层窗口（D8），拿不到键盘焦点也就没有输入法 ——
//	自绘输入框等于自己写光标、选区、IME 组合串，那是另一个量级的工程。
//	所以设置页这一块**破例**：窗口可激活（createLayeredWindowActivatable），
//	输入框用 Win32 原生 EDIT，光标/选区/输入法/剪贴板全是白送的。
//
// 视觉上仍然是自绘的：圆角、边框、底色由我们的渲染器画（drawSettingsFields），
// EDIT 只作为一个**无边框**的子窗口贴在框里，WM_CTLCOLOREDIT 里把它的
// 文字色与底色设成和卡片一致，看起来就还是一块自绘的输入框。

package main

import (
	"log"
	"sync"
	"syscall"
	"unsafe"
)

const (
	editBaseURL = 1001
	editAPIKey  = 1002
	editModel   = 1003
)

// settingsField 是一个输入字段。加字段 = 往 settingsFields() 加一行。
type settingsField struct {
	id     int
	key    string // 对应 ai.txt 里的键名
	label  string
	hint   string
	secret bool // 掩码显示（key）
	hwnd   uintptr
}

func settingsFields() []settingsField {
	return []settingsField{
		{id: editBaseURL, key: "base_url", label: "接口地址", hint: "OpenAI 兼容，例如 https://api.deepseek.com/v1"},
		{id: editAPIKey, key: "api_key", label: "API Key", hint: "写在这里，不会进日志", secret: true},
		{id: editModel, key: "model", label: "模型", hint: "例如 deepseek-flash"},
	}
}

// fieldRect 是输入框在该行里的位置（右侧那一块）。
func fieldRect(row rect) rect {
	w := float64(row.width()) * 0.56
	h := scaled(30)
	x := float64(row.Right) - scaled(12) - w
	y := float64(row.Top) + (float64(row.height())-h)/2
	return rect{int32(x), int32(y), int32(x + w), int32(y + h)}
}

// ensureSettingsFields 建好输入框（懒建：第一次打开设置页才建）。
func ensureSettingsFields() {
	if hwndSettings == 0 {
		return
	}
	for i := range settingsFields() {
		f := &settingsFields()[i] // 注意：这里拿到的是副本，下面用 id 找真身
		if f.id == 0 {
			continue
		}
	}
	// 逐个建：已经建过就跳过
	for _, want := range settingsFields() {
		if fieldHWND(want.id) != 0 {
			continue
		}
		style := uintptr(wsChild | wsVisible | esAutoHScroll | esLeft)
		if want.secret {
			style |= esPassword
		}
		h, _, err := pCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(utf16Ptr("EDIT"))),
			uintptr(unsafe.Pointer(utf16Ptr(""))),
			style,
			0, 0, 10, 10,
			hwndSettings, uintptr(want.id), getModuleHandle(), 0)
		if h == 0 {
			log.Printf("[settings] 建输入框失败 id=%d: %v", want.id, err)
			continue
		}
		// 字体：和设置页正文同族同号（否则原生控件会用系统默认的宋体）
		pSendMessageW.Call(h, wmSetFont, font(scaledI(rowTitlePx), fwNormal), 1)
		fieldSetHWND(want.id, h)
	}
}

// layoutSettingsFields 把输入框摆到该在的位置，并把当前配置填进去。
func layoutSettingsFields(loadFromConfig bool) {
	ensureSettingsFields()

	cfg, _ := loadAIConfig()
	vals := map[string]string{
		"base_url": cfg.BaseURL,
		"api_key":  cfg.APIKey,
		"model":    cfg.Model,
	}
	rows := settingsRowRects()
	kinds := settingsRows()
	for i, r := range kinds {
		if r.kind != rowField {
			continue
		}
		f := fieldByKey(r.fieldKey)
		h := fieldHWND(f.id)
		if h == 0 || i >= len(rows) {
			continue
		}
		pad := int32(scaled(settingsShadowPad()))
		box := fieldRect(rows[i])
		// 坐标是相对设置窗口客户区的（输入框是子窗口）
		pSetWindowPos.Call(h, 0,
			uintptr(box.Left-pad), uintptr(box.Top-pad),
			uintptr(box.width()), uintptr(box.height()),
			uintptr(swpNoZOrder|swpShowWindow))
		if loadFromConfig {
			fieldSetText(f.id, vals[f.key])
		}
	}
}

func fieldByKey(key string) settingsField {
	for _, f := range settingsFields() {
		if f.key == key {
			return f
		}
	}
	return settingsField{}
}

// ================================================================ 句柄表

var fieldHWNDs = map[int]uintptr{}

func fieldHWND(id int) uintptr       { return fieldHWNDs[id] }
func fieldSetHWND(id int, h uintptr) { fieldHWNDs[id] = h }

// fieldText 读输入框里的内容。
func fieldText(id int) string {
	h := fieldHWND(id)
	if h == 0 {
		return ""
	}
	n, _, _ := pGetWindowTextLengthW.Call(h)
	buf := make([]uint16, int(n)+1)
	if len(buf) == 0 {
		return ""
	}
	pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func fieldSetText(id int, s string) {
	h := fieldHWND(id)
	if h == 0 {
		return
	}
	pSetWindowTextW.Call(h, uintptr(unsafe.Pointer(utf16Ptr(s))))
}

// settingsFormValues 把三个框里的值读出来（保存/测试都用它）。
func settingsFormValues() (aiConfig, error) {
	c := aiConfig{
		BaseURL: fieldText(editBaseURL),
		APIKey:  fieldText(editAPIKey),
		Model:   fieldText(editModel),
	}
	// 高级项（system / max_tokens）不在界面上，保存时保留文件里原有的
	if old, err := loadAIConfig(); err == nil {
		c.System = old.System
		c.MaxTokens = old.MaxTokens
	}
	return c, nil
}

// ================================================================ 原生控件的配色

// rgbOf 把主题色转成 GDI 的 COLORREF（0x00BBGGRR）。
func rgbOf(c rgba) uint32 {
	return uint32(c.R) | uint32(c.G)<<8 | uint32(c.B)<<16
}

var (
	fieldBrushMu  sync.Mutex
	fieldBrushH   uintptr
	fieldBrushKey uint32
)

// fieldBrush 返回输入框背景用的画刷（按颜色缓存一个）。
func fieldBrush(c rgba) uintptr {
	col := rgbOf(c)
	fieldBrushMu.Lock()
	defer fieldBrushMu.Unlock()
	if fieldBrushH != 0 && fieldBrushKey == col {
		return fieldBrushH
	}
	if fieldBrushH != 0 {
		pDeleteObject.Call(fieldBrushH)
	}
	h, _, _ := pCreateSolidBrush.Call(uintptr(col))
	fieldBrushH, fieldBrushKey = h, col
	return h
}
