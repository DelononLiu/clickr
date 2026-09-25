//go:build windows

// selection_test.go —— 取文策略（pipeline）：优先级、跳过、错误区分、控制台判定。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o nkb.test.exe .
//	./nkb.test.exe -test.v

package main

import (
	"errors"
	"testing"
)

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
