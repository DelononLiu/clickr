//go:build windows

// 跑在 Windows 上的测试 —— 渲染依赖 GDI，只能在 Windows 上执行。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o kb-sniffer.test.exe .
//	./kb-sniffer.test.exe -test.v

package main

import (
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

// fakeSource 是注入用的假取文实现。
// 有了它才能断言「策略」本身 —— 不然只能拿开发者真实的剪贴板当 fixture。
type fakeSource struct {
	id        sourceID
	available bool
	sel       selection
	err       error
	calls     *int32
}

func (f fakeSource) Name() sourceID { return f.id }

func (f fakeSource) Available(captureContext) bool { return f.available }

func (f fakeSource) Read(captureContext, point) (selection, error) {
	if f.calls != nil {
		atomic.AddInt32(f.calls, 1)
	}
	return f.sel, f.err
}

// createEditTestWindow 建一个带 EDIT 控件的窗口，返回窗口句柄与控件句柄。
// UIA 的 TextPattern 是「选区」级的，所以必须有真控件才能验。
func createEditTestWindow(t *testing.T, text string) (hwnd, edit uintptr) {
	t.Helper()
	const cls = "KBSnifferUIATestWnd"
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
		uintptr(unsafe.Pointer(utf16Ptr("kb-sniffer UIA test"))),
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
