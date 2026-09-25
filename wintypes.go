//go:build windows

// wintypes.go —— Win32 结构体定义 + 带类型的薄封装。
//
// 结构体布局必须和 Windows SDK 一致（x64 下的对齐很重要），
// 每个结构体都标了关键字段的偏移，方便日后核对。
package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// ================================================================ 基础结构体

type point struct {
	X int32 // offset 0
	Y int32 // offset 4
}

type size struct {
	CX int32
	CY int32
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

func (r rect) width() int32  { return r.Right - r.Left }
func (r rect) height() int32 { return r.Bottom - r.Top }

func (r rect) contains(p point) bool {
	return p.X >= r.Left && p.X < r.Right && p.Y >= r.Top && p.Y < r.Bottom
}

// WNDCLASSEXW：CbSize@0, Style@4, LpfnWndProc@8, CbClsExtra@16, CbWndExtra@20,
// HInstance@24, HIcon@32, HCursor@40, HbrBackground@48, LpszMenuName@56,
// LpszClassName@64, HIconSm@72 —— 共 80 字节
type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

// MSG：x64 下共 48 字节
type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

// MONITORINFO：CbSize@0, RcMonitor@4, RcWork@20, DwFlags@36 —— 共 40 字节
type monitorInfo struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	DwFlags   uint32
}

// MSLLHOOKSTRUCT：Pt@0, MouseData@8, Flags@12, Time@16, DwExtraInfo@24 —— 共 32 字节
type msllHookStruct struct {
	Pt          point
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

// TRACKMOUSEEVENT
type trackMouseEvent struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   uintptr
	DwHoverTime uint32
}

// BITMAPINFOHEADER：共 40 字节
type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// LOGFONTW：LF_FACESIZE = 32，共 92 字节
type logFontW struct {
	LfHeight         int32
	LfWidth          int32
	LfEscapement     int32
	LfOrientation    int32
	LfWeight         int32
	LfItalic         byte
	LfUnderline      byte
	LfStrikeOut      byte
	LfCharSet        byte
	LfOutPrecision   byte
	LfClipPrecision  byte
	LfQuality        byte
	LfPitchAndFamily byte
	LfFaceName       [32]uint16
}

// BLENDFUNCTION：4 个字节
type blendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

// ---------------------------------------------------------------- SendInput

// KEYBDINPUT：WVk@0, WScan@2, DwFlags@4, Time@8, DwExtraInfo@16 —— 共 24 字节
type keybdInput struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

// INPUT：Type@0(4)+pad(4), union@8。x64 下 union 最大成员 32 字节，故总长 40。
type input struct {
	Type uint32
	_    uint32
	Ki   keybdInput
	_    [8]byte // 把 union 补到 32 字节
}

// ================================================================ 薄封装

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

// SetProcessDpiAwarenessContext 等三个 API 逐级降级。
//
// 这一步必须在创建任何窗口之前做，否则进程是 DPI-unaware 的：
// 系统会把我们拿到的坐标「虚拟化」缩放，多显示器不同缩放时弹窗就会飘。
// 注意本程序没有嵌 manifest，所以只能靠运行时调用来设置。
func setDPIAwareness() string {
	if r, _, _ := pSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2); r != 0 {
		return "PerMonitorV2"
	}
	// HRESULT 要按**有符号**判成功：S_OK=0、S_FALSE=1 都属于成功，
	// 只有负值才是失败。写成 == 0 会把 S_FALSE 当成失败。
	if r, _, _ := pSetProcessDpiAwareness.Call(processPerMonitorDpiAware); int32(r) >= 0 {
		return "PerMonitor (shcore)"
	}
	if r, _, _ := pSetProcessDPIAware.Call(); r != 0 {
		return "System (fallback)"
	}
	return "unaware"
}

// dpiForPoint 返回该点所在显示器的有效 DPI（96 = 100%）。
func dpiForPoint(pt point) int32 {
	mon := monitorFromPoint(pt)
	if mon == 0 {
		return 96
	}
	var x, y uint32
	hr, _, _ := pGetDpiForMonitor.Call(mon, mdtEffectiveDPI,
		uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y)))
	if hr != 0 || x == 0 {
		return 96
	}
	return int32(x)
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

// CONSOLE_SELECTION_INFO：控制台当前的选区状态。
//
// 这是读真控制台选区的关键 —— 控制台自己知道用户选了什么，
// 不需要我们模拟 Ctrl+C，也不需要 MSAA。
//
//	CbSize@0(4) DwFlags@4(4) DwSelectionAnchor@8(COORD 4) SrSelection@12(SMALL_RECT 8) => 20 字节
type consoleSelectionInfo struct {
	CbSize            uint32
	DwFlags           uint32
	DwSelectionAnchor coord
	SrSelection       smallRect
}

type coord struct {
	X int16
	Y int16
}

type smallRect struct {
	Left   int16
	Top    int16
	Right  int16
	Bottom int16
}

// MEMORY_BASIC_INFORMATION（x64，共 48 字节）。
//
//	BaseAddress@0 AllocationBase@8 AllocationProtect@16
//	(PartitionId@20) RegionSize@24 State@32 Protect@36 Type@40
type memoryBasicInformation struct {
	BaseAddress       uintptr
	AllocationBase    uintptr
	AllocationProtect uint32
	_                 uint32
	RegionSize        uintptr
	State             uint32
	Protect           uint32
	Type              uint32
	_                 uint32
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

// foregroundIsConsole 判断当前前台窗口是不是一个真正的控制台窗口。
//
// 为什么要判：控制台里 Ctrl+C 是「中断」而不是「复制」。我们合成的那次 Ctrl+C
// 会被透传给 shell，把用户正在跑的命令打断 —— 这个副作用比取不到词严重得多。
//
// 只能按窗口类名判。VS Code 的集成终端是 Electron 窗口（Chrome_WidgetWin_1），
// 和它的编辑器区无法区分，所以那种情况拦不住。
func foregroundIsConsole() bool {
	hwnd, _, _ := pGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}
	return isConsoleClass(windowClass(hwnd))
}

// consoleWindowClasses 是「真控制台」的窗口类名表。
//
// 提成表是为了可测：以前类名内联在 switch 里，于是唯一能写的测试只有
// `_ = foregroundIsConsole()` —— 什么都没断言。而这个判定是**唯一会打断
// 用户正在运行的命令**的分支，判错了代价比取不到词大得多。
//
// 这里刻意**不含** VS Code 的集成终端：它是 Electron 窗口（Chrome_WidgetWin_1），
// 和编辑器区无法区分，拦不住；那种情况交给 MSAA 兜底。
var consoleWindowClasses = map[string]bool{
	"ConsoleWindowClass":            true, // conhost：cmd.exe / powershell.exe / WSL
	"CASCADIA_HOSTING_WINDOW_CLASS": true, // Windows Terminal
	"mintty":                        true, // Git Bash / MSYS2
}

func isConsoleClass(class string) bool { return consoleWindowClasses[class] }

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

// isOwnWindow 判断某个屏幕坐标下的窗口是不是本进程的窗口。
// 用来避免「点自己的菜单/悬浮球」又被当成一次划词。
func isOwnWindow(pt point) bool {
	hwnd := windowFromPoint(pt)
	if hwnd == 0 {
		return false
	}
	var pid uint32
	pGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid == currentProcessID()
}

// isKeyDown 查询按键当前是否按下。
func isKeyDown(vk uintptr) bool {
	v, _, _ := pGetAsyncKeyState.Call(vk)
	return int16(uint16(v)) < 0
}

func sendKey(vk uint16, up bool) {
	flags := uint32(0)
	if up {
		flags = keyeventfKeyUp
	}
	in := input{Type: 1 /*INPUT_KEYBOARD*/, Ki: keybdInput{WVk: vk, DwFlags: flags}}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

// sendCtrlC 模拟一次 Ctrl+C。
//
// 关键点：用户在划词时很可能正按着 Shift（或者别的方式），
// 这里额外补发一次 Ctrl 抬起，避免修饰键状态被我们搞乱。
func sendCtrlC() {
	// 关键：只有**我们自己按下**的 Ctrl 才由我们抬起。
	//
	// 用户很可能正按着 Ctrl 在别处操作；无条件补一次「抬起」会把他的 Ctrl
	// 松开（修饰键状态错乱）。之前的条件只挡住了「按下」，没挡住「抬起」。
	wePressedCtrl := false
	if !isKeyDown(vkControl) {
		sendKey(vkControl, false)
		wePressedCtrl = true
	}
	sendKey(vkC, false)
	sendKey(vkC, true)
	if wePressedCtrl {
		sendKey(vkControl, true)
	}
}

// dragThreshold 返回「算拖选而不算单击」的位移阈值。
//
// 用系统的 SM_CXDRAG / SM_CYDRAG，而不是写死 5px：
// 这两个值的语义就是「拖动判定矩形」，而且**系统已经按 DPI 缩放过**。
// 写死的话，200% 缩放下的 5 物理像素只等效 2.5 逻辑像素，手一抖就被当成划词。
func dragThreshold() (int32, int32) { return systemMetric(smCXDRAG), systemMetric(smCYDRAG) }

// doubleClickProximity 返回双击允许的位置偏差，同样取系统值（已按 DPI 缩放）。
func doubleClickProximity() (int32, int32) {
	return systemMetric(smCXDOUBLECLK), systemMetric(smCYDOUBLECLK)
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

func shellOpen(url string) {
	op := utf16Ptr("open")
	pShellExecuteW.Call(0, uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(utf16Ptr(url))), 0, 0, swShownormal)
}

// ================================================================ 注册表（读主题）

// systemUsesDarkMode 读 HKCU\...\Themes\Personalize\AppsUseLightTheme。
// 每次显示菜单时现查，这样用户在系统里切换主题后无需重启。
func systemUsesDarkMode() (bool, error) {
	sub := utf16Ptr(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	val := utf16Ptr("AppsUseLightTheme")
	var hkey uintptr
	if r, _, _ := pRegOpenKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(sub)),
		0, 0x20019 /*KEY_READ*/, uintptr(unsafe.Pointer(&hkey))); r != 0 {
		return false, fmt.Errorf("RegOpenKeyExW 失败 code=%d", r)
	}
	defer pRegCloseKey.Call(hkey)

	var data uint32
	cb := uint32(4)
	r, _, _ := pRegQueryValueExW.Call(hkey, uintptr(unsafe.Pointer(val)), 0, 0,
		uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&cb)))
	if r != 0 {
		return false, fmt.Errorf("RegQueryValueExW 失败 code=%d", r)
	}
	// AppsUseLightTheme: 0 = 暗色，非 0 = 浅色
	return data == 0, nil
}
