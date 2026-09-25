//go:build windows

// abi.go —— Win32 结构体定义。
//
// **这个文件里的每一个结构体都必须和 Windows SDK 逐字节一致** ——
// 对齐错一位就是读写越界那种"平时没事、偶尔崩"的故障。
// 所以它跟别的文件的变化原因不同：审这个文件的方式是「拿头文件对」，
// 不是「读逻辑对不对」。因此单独放，别和业务封装混在一起。

package main

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

func (r rect) width() int32 { return r.Right - r.Left }

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
