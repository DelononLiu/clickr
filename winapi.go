//go:build windows

// winapi.go —— Win32 的无策略薄封装：纯查询与纯转发。
//
// 判据是「这个函数里有没有本项目的决策」。有决策的一律归到它的消费者那边去
// （控制台判定 → selection.go，手势阈值 → hook.go，主题 → theme.go，
// 打开链接 → menucontent.go），这里只留机械转发。

package main

import (
	"syscall"
	"unsafe"
)

func getModuleHandle() uintptr {
	h, _, _ := pGetModuleHandleW.Call(0)
	return h
}

func currentProcessID() uint32 {
	v, _, _ := pGetCurrentProcessId.Call()
	return uint32(v)
}

func tickCount64() uint64 {
	v, _, _ := pGetTickCount64.Call()
	return uint64(v)
}

func getCursorPos() point {
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return pt
}

// monitorFromPoint / windowFromPoint 的 POINT 参数是「按值传递」的 8 字节结构体。
//
// Windows x64 调用约定规定：1/2/4/8 字节的结构体按整数寄存器传递。
// 所以这里把 X、Y 打包成一个 uint64 塞进寄存器即可，不需要分配内存。
func packPoint(pt point) uintptr {
	return uintptr(uint64(uint32(pt.X)) | uint64(uint32(pt.Y))<<32)
}

func monitorFromPoint(pt point) uintptr {
	h, _, _ := pMonitorFromPoint.Call(packPoint(pt), monitorDefaultToNearest)
	return h
}

func windowFromPoint(pt point) uintptr {
	h, _, _ := pWindowFromPoint.Call(packPoint(pt))
	return h
}

// workArea 返回该点所在显示器的工作区（不含任务栏），用的是物理像素。
func workArea(pt point) rect {
	mon := monitorFromPoint(pt)
	mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	if mon == 0 {
		return rect{0, 0, int32(getSystemMetrics(0)), int32(getSystemMetrics(1))}
	}
	ok, _, _ := pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	if ok == 0 {
		return rect{0, 0, int32(getSystemMetrics(0)), int32(getSystemMetrics(1))}
	}
	return mi.RcWork
}

func getSystemMetrics(idx int32) int32 {
	v, _, _ := pGetSystemMetrics.Call(uintptr(idx))
	return int32(v)
}

// isExecutableAddress 判断某个地址是否落在已提交且可执行的内存页里。
//
// 用来给「从 COM vtable 里取出来的函数指针」做一层廉价体检：
// 如果槽位索引越界、指到了数据区，这里会判 false。
func isExecutableAddress(addr uintptr) bool {
	var mbi memoryBasicInformation
	n, _, _ := pVirtualQuery.Call(addr, uintptr(unsafe.Pointer(&mbi)),
		unsafe.Sizeof(mbi))
	if n == 0 || mbi.State != memCommit {
		return false
	}
	switch mbi.Protect {
	case pageExecute, pageExecuteRead, pageExecuteReadWrite, pageExecuteWriteCopy:
		return true
	}
	return false
}

// hasConsole 判断进程有没有可写的控制台。
//
// 用 GetConsoleWindow 而不是 GetStdHandle：被 detached 启动时
// 标准句柄可能是无效值而非 NULL，只看句柄会误判。
func hasConsole() bool {
	r, _, _ := pGetConsoleWindow.Call()
	return r != 0
}

// foregroundWindowClass 返回前台窗口的类名（拿不到就是空串）。
func foregroundWindowClass() string {
	hwnd, _, _ := pGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	return windowClass(hwnd)
}

// windowTitle 取窗口标题。
//
// 存在的理由：窗口**类名不足以区分应用** —— VS Code、Chrome、Edge、Slack
// 都是 Electron/Chromium，类名统统是 "Chrome_WidgetWin_1"。
// 结果看日志时完全分不出「这次划词发生在哪个程序」，只能靠人去记。
// 标题能把它们分开（"xxx - Visual Studio Code" / 页面标题）。
func windowTitle(hwnd uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, int(n)+1)
	got, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if got == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:got])
}

// foregroundWindowLabel 给日志用：类名 + 标题。
func foregroundWindowLabel() string {
	hwnd, _, _ := pGetForegroundWindow.Call()
	if hwnd == 0 {
		return "(无前台窗口)"
	}
	title := windowTitle(hwnd)
	if title == "" {
		return windowClass(hwnd)
	}
	if len([]rune(title)) > 48 {
		title = string([]rune(title)[:48]) + "…"
	}
	return windowClass(hwnd) + " | " + title
}

func windowClass(hwnd uintptr) string {
	var buf [128]uint16
	n, _, _ := pGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func isWindowVisible(hwnd uintptr) bool {
	r, _, _ := pIsWindowVisible.Call(hwnd)
	return r != 0
}

func windowRect(hwnd uintptr) (rect, bool) {
	var r rect
	ok, _, _ := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r, ok != 0
}

func setWindowPos(hwnd uintptr, x, y, w, h int32, flags uint32) {
	pSetWindowPos.Call(hwnd, ^uintptr(0) /*HWND_TOPMOST*/, uintptr(x), uintptr(y),
		uintptr(w), uintptr(h), uintptr(flags))
}

func showWindow(hwnd uintptr, cmd int32) {
	pShowWindow.Call(hwnd, uintptr(cmd))
}

func systemMetric(idx int32) int32 {
	v, _, _ := pGetSystemMetrics.Call(uintptr(idx))
	if v == 0 {
		return 4 // 拿不到就给个保守默认，别变成 0（否则任何位移都算拖选）
	}
	return int32(v)
}

// utf16Ptr 把 Go 字符串转成以 NUL 结尾的 UTF-16 指针。
func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

// messageBox 弹一个原生对话框。
//
// GUI 子系统（-H=windowsgui）编译出来的程序没有控制台，
// 初始化失败时如果只写 log 就等于静默退出，用户完全不知道发生了什么。
func messageBox(title, text string, flags uintptr) {
	messageBoxOwned(0, title, text, flags)
}

// messageBoxOwned 指定属主窗口。传 0 的话，这个框可能被我们自己
// 那个 WS_EX_TOPMOST 的悬浮球盖住 —— 用户就永远看不到错误提示。
func messageBoxOwned(owner uintptr, title, text string, flags uintptr) {
	pMessageBoxW.Call(owner,
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(unsafe.Pointer(utf16Ptr(title))),
		flags)
}

func loadCursor(id uintptr) uintptr {
	h, _, _ := pLoadCursorW.Call(0, id)
	return h
}
