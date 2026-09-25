//go:build windows

// win32.go —— 所有 Win32 API 的裸绑定。
//
// 这里刻意不引入第三方库（不用 golang.org/x/sys/windows，也不用 go-ole），
// 全部走 syscall.NewLazyDLL + LazyProc.Call。好处是 CGO_ENABLED=0 即可交叉编译，
// 在 Linux/WSL 上 `GOOS=windows go build` 直接出 exe。
package main

import "syscall"

// ---------------------------------------------------------------- DLL

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	oleacc   = syscall.NewLazyDLL("oleacc.dll")
	oleaut32 = syscall.NewLazyDLL("oleaut32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")
)

// ---------------------------------------------------------------- 进程 / DPI

var (
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDpiAwareness        = shcore.NewProc("SetProcessDpiAwareness")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	pGetDpiForMonitor              = shcore.NewProc("GetDpiForMonitor")
	pGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	pGetCurrentProcessId           = kernel32.NewProc("GetCurrentProcessId")
	pGetConsoleWindow              = kernel32.NewProc("GetConsoleWindow")
	pGetTickCount64                = kernel32.NewProc("GetTickCount64")
	pSleep                         = kernel32.NewProc("Sleep")
)

// ---------------------------------------------------------------- 窗口 / 消息

var (
	pRegisterClassExW = user32.NewProc("RegisterClassExW")
	pCreateWindowExW  = user32.NewProc("CreateWindowExW")
	pDefWindowProcW   = user32.NewProc("DefWindowProcW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pGetMessageW      = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessageW = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pPostMessageW     = user32.NewProc("PostMessageW")
	pSetWindowPos     = user32.NewProc("SetWindowPos")
	pShowWindow       = user32.NewProc("ShowWindow")
	pGetWindowRect    = user32.NewProc("GetWindowRect")
	pIsWindow         = user32.NewProc("IsWindow")
	pIsWindowVisible  = user32.NewProc("IsWindowVisible")
	pLoadCursorW      = user32.NewProc("LoadCursorW")
	pSetTimer         = user32.NewProc("SetTimer")
	pKillTimer        = user32.NewProc("KillTimer")
	pTrackMouseEvent  = user32.NewProc("TrackMouseEvent")
	pLoadImageW       = user32.NewProc("LoadImageW")
	pMessageBoxW      = user32.NewProc("MessageBoxW")
	pDestroyIcon      = user32.NewProc("DestroyIcon")
)

// 分层窗口
var (
	pUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	pGetDC               = user32.NewProc("GetDC")
	pReleaseDC           = user32.NewProc("ReleaseDC")
)

// ---------------------------------------------------------------- 鼠标 / 光标

var (
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	pGetClassNameW            = user32.NewProc("GetClassNameW")
	pSetCursorPos             = user32.NewProc("SetCursorPos")
	pWindowFromPoint          = user32.NewProc("WindowFromPoint")
	pGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	pMonitorFromPoint         = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
	pSendInput                = user32.NewProc("SendInput")
	pGetAsyncKeyState         = user32.NewProc("GetAsyncKeyState")
	pGetDoubleClickTime       = user32.NewProc("GetDoubleClickTime")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
)

// ---------------------------------------------------------------- 钩子

var (
	pSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx      = user32.NewProc("CallNextHookEx")
	pUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
)

// ---------------------------------------------------------------- 剪贴板

var (
	pOpenClipboard              = user32.NewProc("OpenClipboard")
	pCloseClipboard             = user32.NewProc("CloseClipboard")
	pEmptyClipboard             = user32.NewProc("EmptyClipboard")
	pGetClipboardData           = user32.NewProc("GetClipboardData")
	pSetClipboardData           = user32.NewProc("SetClipboardData")
	pIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	pGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	pEnumClipboardFormats       = user32.NewProc("EnumClipboardFormats")
	pCopyImage                  = user32.NewProc("CopyImage")
)

var (
	pGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	pGlobalFree   = kernel32.NewProc("GlobalFree")
	pGlobalLock   = kernel32.NewProc("GlobalLock")
	pGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	pGlobalSize   = kernel32.NewProc("GlobalSize")
)

// 控制台读取（cmd / PowerShell 这类真控制台）
var (
	pAttachConsole                = kernel32.NewProc("AttachConsole")
	pAllocConsole                 = kernel32.NewProc("AllocConsole")
	pFreeConsole                  = kernel32.NewProc("FreeConsole")
	pGetStdHandle                 = kernel32.NewProc("GetStdHandle")
	pSetConsoleSelectionInfo      = kernel32.NewProc("SetConsoleSelectionInfo")
	pGetConsoleSelectionInfo      = kernel32.NewProc("GetConsoleSelectionInfo")
	pReadConsoleOutputCharacterW  = kernel32.NewProc("ReadConsoleOutputCharacterW")
	pWriteConsoleOutputCharacterW = kernel32.NewProc("WriteConsoleOutputCharacterW")
	pGetConsoleScreenBufferInfo   = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

// MSAA（有道走的那条路）：从 oleacc.dll 拿「某个屏幕坐标下的可访问对象」
var (
	pAccessibleObjectFromPoint  = oleacc.NewProc("AccessibleObjectFromPoint")
	pAccessibleObjectFromWindow = oleacc.NewProc("AccessibleObjectFromWindow")
)

// COM / BSTR
var (
	pOleInitialize = ole32.NewProc("OleInitialize")
	pVariantClear  = oleaut32.NewProc("VariantClear")
	pSysStringLen  = oleaut32.NewProc("SysStringLen")
	pSysFreeString = oleaut32.NewProc("SysFreeString")
)

// 注意：剪贴板快照刻意**不用** OLE。
//
// OleGetClipboard 返回的是代理对象，只在剪贴板未被修改期间有效；
// 而我们自己紧接着就发 Ctrl+C 改了剪贴板，代理当场失效，
// 后续 OleSetClipboard 必然失败（实测约 6/7 次）。
// 详见 capture.go 顶部注释。

// ---------------------------------------------------------------- GDI

var (
	pCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC              = gdi32.NewProc("DeleteDC")
	pCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	pSelectObject          = gdi32.NewProc("SelectObject")
	pDeleteObject          = gdi32.NewProc("DeleteObject")
	pCreateFontIndirectW   = gdi32.NewProc("CreateFontIndirectW")
	pGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
	pSetBkMode             = gdi32.NewProc("SetBkMode")
	pSetTextColor          = gdi32.NewProc("SetTextColor")
	pCreateSolidBrush      = gdi32.NewProc("CreateSolidBrush")
	pFillRect              = user32.NewProc("FillRect")
	pDrawTextW             = user32.NewProc("DrawTextW")
)

// ---------------------------------------------------------------- Shell / 注册表

var (
	pShellExecuteW    = shell32.NewProc("ShellExecuteW")
	pRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	pRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	pRegCloseKey      = advapi32.NewProc("RegCloseKey")
)

// ================================================================ 常量

const (
	// 窗口样式
	wsPopup        = 0x80000000
	wsVisible      = 0x10000000
	wsChild        = 0x40000000
	ssLeft         = 0x00000000 // Static 控件左对齐（就是默认值 0）
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
	wsExNoActivate = 0x08000000
	wsExLayered    = 0x00080000

	// SetWindowPos
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010
	swpShowWindow = 0x0040
	swpHideWindow = 0x0080
	swpNoZOrder   = 0x0004

	// 消息
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmPaint         = 0x000F
	wmNCHitTest     = 0x0084
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmMouseLeave    = 0x02A3
	wmDpiChanged    = 0x02E0
	wmApp           = 0x8000
	wmTimer         = 0x0113

	// 自定义消息（hook/capture 线程 → 主线程）
	wmShowMenu = wmApp + 1
	wmHideMenu = wmApp + 2
	wmQuitApp  = wmApp + 3

	// 命中测试
	htTransparent = -1
	htClient      = 1

	// 钩子
	whMouseLL = 14
	hcAction  = 0

	// 鼠标按键
	vkControl = 0x11
	vkC       = 0x43
	vkShift   = 0x10
	vkMenu    = 0x12

	keyeventfKeyUp = 0x0002

	// 剪贴板
	cfBitmap          = 2
	cfMetafilePict    = 3
	cfPalette         = 9
	cfUnicodeText     = 13
	cfEnhMetafile     = 14
	cfOwnerDisplay    = 0x0080
	cfDspBitmap       = 0x0082
	cfDspMetafilePict = 0x0083
	cfDspEnhMetafile  = 0x008E
	cfRegisteredFirst = 0xC000
	gmemMoveable      = 0x0002
	imageBitmap       = 0

	// DIB
	dibRGBColors = 0
	biRGB        = 0

	// DrawText
	dtLeft        = 0x0000
	dtCenter      = 0x0001
	dtRight       = 0x0002
	dtVCenter     = 0x0004
	dtSingleLine  = 0x0020
	dtNoPrefix    = 0x0800
	dtEndEllipsis = 0x00008000

	// GDI
	transparentBkMode  = 1
	antialiasedQuality = 4
	defaultCharset     = 1
	fwNormal           = 400
	fwMedium           = 500

	// 图层混合
	ulwAlpha   = 0x02
	acSrcOver  = 0x00
	acSrcAlpha = 0x01

	// 显示器
	monitorDefaultToNearest = 2
	mdtEffectiveDPI         = 0

	// 光标
	idcArrow   = 32512
	idcHand    = 32649
	idcSizeAll = 32646

	// TrackMouseEvent
	tmeLeave = 0x00000002

	// GetConsoleSelectionInfo 的标志位
	consoSelectionInUse = 0x0001       // 有选区
	stdOutputHandle     = ^uintptr(11) // GetStdHandle(STD_OUTPUT_HANDLE) = -11
	consoNoSelection    = 0x0000

	// MessageBox
	mbOK        = 0x00000000
	mbIconError = 0x00000010

	// ShellExecute
	swHide       = 0
	swShownormal = 1

	// HKEY_CURRENT_USER
	//
	// x64 上必须是 0xFFFFFFFF80000001，不能写成 0x80000001。
	// winreg.h 里的定义是 (HKEY)(ULONG_PTR)((LONG)0x80000000)：
	// LONG 先符号扩展成 64 位再转成指针，高 32 位全是 1。
	// 写短了 RegOpenKeyExW 会以 ERROR_INVALID_HANDLE 失败，
	// 结果是「暗色主题永远不生效」这种不报错的静默故障。
	hkeyCurrentUser = 0xFFFFFFFF80000001

	// SetProcessDpiAwarenessContext 用的伪句柄
	dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3) // -4
)

// DPI 相关
const (
	processPerMonitorDpiAware = 2
)
