//go:build windows

// uia.go —— UI Automation 的 TextPattern：拿**精确选区** + **选区矩形**。
//
// # 为什么这个文件值得写
//
// 剪贴板法能拿到"用户真正拖选的那一段"，但要付出发送 Ctrl+C 的代价
// （副作用、必须快照并还原用户剪贴板、控制台里根本不能用），而且它对
// 选区在哪一无所知 —— 弹窗只能猜鼠标抬起点。
//
// UIA 的 TextPattern 直接回答"当前选区是什么、在屏幕的哪个矩形里"，
// 不发任何按键、不碰剪贴板。这是"划词"这件事的完全体。
//
// # 为什么之前没先做它
//
// Go 生态里唯一的 UIA 库（hnakamur/w32uiautomation 及其 fork）**没有实现
// TextPattern / TextRange / TextRangeArray** —— 恰好是取选区所需的那三个接口。
// 而 MSAA 的 IAccessible 是 IDispatch 派生、vtable 固定，按序号就能调，
// 成本差一个量级。所以先用 MSAA 把主链路跑通，再补这里。
//
// # vtable 槽位从哪来
//
// 下面的常量是 UIAutomationClient.h 里各接口方法的**声明顺序**（IUnknown 占 0-2）。
// 抄错一位就会跳到相邻方法的地址上 —— 症状是崩溃或莫名的 HRESULT。
// 核对方式：对着 SDK 头文件数，或跑 TestUIAVtableSlotsAreExecutable。
//
//	IUIAutomation:          03 CompareElements ... 07 ElementFromPoint, 08 GetFocusedElement
//	IUIAutomationElement:   10 GetCurrentPropertyValue, 16 GetCurrentPattern,
//	                        21 Get_CurrentControlType, 23 Get_CurrentName,
//	                        35 Get_CurrentIsPassword
//	IUIAutomationTextPattern:        06 GetSelection
//	IUIAutomationTextRangeArray:     03 Get_Length, 04 GetElement
//	IUIAutomationTextRange:          10 GetBoundingRectangles, 12 GetText
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// ---------------------------------------------------------------- 槽位

const (
	uiaElementFromPoint  = 7
	uiaGetFocusedElement = 8

	elemGetCurrentPropertyValue = 10
	elemGetCurrentPattern       = 16
	elemGetCurrentControlType   = 21
	elemGetCurrentName          = 23
	elemGetCurrentIsPassword    = 35

	// ⚠️ 下面这组槽位是**机械探测确定的**，不是照着头文件推的。
	//
	// 最初按「IDL 声明顺序」推断成 RangeFromPoint=4 / GetSelection=6，
	// 结果 GetSelection 返回的是整行文字 —— 那其实是 GetVisibleRanges。
	// 用一个逐槽位探测的测试（TestUIASlotProbe，每个槽位单跑一个进程，
	// 崩了只影响那一次）实测出真实布局：
	//
	//	槽位 5 → "BBB"（正确的选区！）            → GetSelection
	//	槽位 6 → 整行文字                        → GetVisibleRanges
	//	槽位 3 → 崩（参数对不上）                → RangeFromPoint
	//	槽位 4 → 崩（参数对不上）                → RangeFromChild
	//	槽位 7 → 只写 4 字节枚举，当指针读是垃圾   → get_SupportedTextSelection
	//
	// 也就是说 get_SupportedTextSelection 在实际 vtable 里排在**最后**，
	// 而不是 IDL 里的最前。教训：vtable 顺序这种东西，能测就别推。
	textPatternRangeFromPoint        = 3
	textPatternRangeFromChild        = 4
	textPatternGetSelection          = 5
	textPatternGetVisibleRanges      = 6
	textPatternGetSupportedSelection = 7

	textRangeArrayGetLength  = 3
	textRangeArrayGetElement = 4

	textRangeGetBoundingRectangles = 10
	textRangeGetText               = 12
)

// 模式 / 属性 ID。值取自 UIAutomationClient.h 的枚举
// （与 w32uiautomation 的 patternid.go / propertyidentifiers.go 一致）。
const (
	uiaTextPatternId         = 10014
	uiaTextPattern2Id        = 10024
	uiaValuePatternId        = 10002
	uiaIsPasswordPropertyId  = 30019
	uiaControlTypePropertyId = 30003
	uiaNamePropertyId        = 30004
)

const clsctxInprocServer = 0x1

// ---------------------------------------------------------------- COM 入口

// CLSID_CUIAutomation / IID_IUIAutomation
//
// 用 GetCurrentPattern 而不是 GetCurrentPatternAs 取模式，就是为了少依赖一个
// 我无法在此处离线核对的 IID：GetCurrentPattern 只按 patternId 取，返回 IUnknown，
// 拿到之后直接按该模式自己的 vtable 解释即可。
var (
	clsidCUIAutomation = syscall.GUID{
		Data1: 0xff48dba4, Data2: 0x60ef, Data3: 0x4201,
		Data4: [8]byte{0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e},
	}
	iidIUIAutomation = syscall.GUID{
		Data1: 0x30cbe57d, Data2: 0xd9d0, Data3: 0x452a,
		Data4: [8]byte{0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee},
	}
)

var (
	uiaOnce sync.Once
	uiaRoot comPtr
	uiaErr  error
)

// initUIA 创建 IUIAutomation 实例。必须在**已 COM 初始化的线程**上调用 ——
// 本项目的采集线程是常驻且 OleInitialize 过的，所以只创建一次并缓存即可。
func initUIA() error {
	uiaOnce.Do(func() {
		var p uintptr
		hr, _, _ := pCoCreateInstance.Call(
			uintptr(unsafe.Pointer(&clsidCUIAutomation)),
			0,
			clsctxInprocServer,
			uintptr(unsafe.Pointer(&iidIUIAutomation)),
			uintptr(unsafe.Pointer(&p)))
		if int32(hr) < 0 || p == 0 {
			uiaErr = fmt.Errorf("CoCreateInstance(CUIAutomation) 失败 hr=0x%08X", uint32(hr))
			return
		}
		uiaRoot = comPtr(p)
	})
	return uiaErr
}

// ---------------------------------------------------------------- 元素

// uiaCandidateElements 按「最可能持有选区」的顺序给出候选元素。
//
// 顺序很关键，是被实测纠正过的：
//
// **焦点元素优先，而不是命中测试的元素。**
// 选区属于**有焦点的那个元素**；而 ElementFromPoint 给的是「光标底下的元素」，
// 那往往是文档包装元素 —— 实测 RichEdit 里命中到的是 Document(50030)，
// 它的 TextPattern 返回的是**整篇内容**，而真正的选区挂在 Edit(50004) 上
// （控件自报 EM_GETSEL = [4,7)，一致）。
//
// 真实划词时源程序本来就是前台，所以焦点元素才是对的入口；
// 命中测试留作兜底（某些场景下焦点元素可能不在被划的那个控件上）。
func uiaCandidateElements(pt point) []comPtr {
	if err := initUIA(); err != nil {
		return nil
	}
	var out []comPtr

	var focused uintptr
	if hr, _, _ := syscall.SyscallN(comVtblMethod(uiaRoot, uiaGetFocusedElement),
		uintptr(uiaRoot), uintptr(unsafe.Pointer(&focused))); int32(hr) >= 0 && focused != 0 {
		out = append(out, comPtr(focused))
	}

	var hit uintptr
	if hr, _, _ := syscall.SyscallN(comVtblMethod(uiaRoot, uiaElementFromPoint),
		uintptr(uiaRoot), packPoint(pt), uintptr(unsafe.Pointer(&hit))); int32(hr) >= 0 && hit != 0 {
		out = append(out, comPtr(hit))
	}
	return out
}

func (e comPtr) isPassword() (bool, bool) {
	var v variant
	hr, _, _ := syscall.SyscallN(comVtblMethod(e, elemGetCurrentPropertyValue),
		uintptr(e), uintptr(uiaIsPasswordPropertyId), uintptr(unsafe.Pointer(&v)))
	defer v.clear()
	if int32(hr) < 0 {
		return false, false
	}
	// VT_BOOL 是 VARIANT_BOOL：0 = false，0xFFFF(-1) = true
	if v.VT != vtBool {
		return false, false
	}
	return int16(uint16(v.Val)) != 0, true
}

func (e comPtr) controlType() (int32, bool) {
	var v variant
	hr, _, _ := syscall.SyscallN(comVtblMethod(e, elemGetCurrentPropertyValue),
		uintptr(e), uintptr(uiaControlTypePropertyId), uintptr(unsafe.Pointer(&v)))
	defer v.clear()
	if int32(hr) < 0 || v.VT != vtI4 {
		return 0, false
	}
	return int32(v.Val), true
}

// currentPattern 取控件支持的模式（拿不到说明该控件不支持）。
func (e comPtr) currentPattern(patternID uintptr) (comPtr, bool) {
	var p uintptr
	hr, _, _ := syscall.SyscallN(comVtblMethod(e, elemGetCurrentPattern),
		uintptr(e), patternID, uintptr(unsafe.Pointer(&p)))
	if int32(hr) < 0 || p == 0 {
		return 0, false
	}
	return comPtr(p), true
}

// ---------------------------------------------------------------- 选区

type uiaResult struct {
	text      string
	bounds    rect
	hasBounds bool
}

// uiaSelectionAtHitTest 只按坐标命中，不看焦点元素。
//
// 给 `-probe` 用：诊断进程一启动就会变成前台窗口，
// 于是 GetFocusedElement 拿到的是**我们自己**，探不到用户正在看的那个程序。
// 产品路径不会遇到这个问题 —— 我们从不激活自己的窗口（SWP_NOACTIVATE），
// 用户划词时源程序一直是前台。
func uiaSelectionAtHitTest(pt point) (uiaResult, error) {
	if err := initUIA(); err != nil {
		return uiaResult{}, err
	}
	var hit uintptr
	hr, _, _ := syscall.SyscallN(comVtblMethod(uiaRoot, uiaElementFromPoint),
		uintptr(uiaRoot), packPoint(pt), uintptr(unsafe.Pointer(&hit)))
	if int32(hr) < 0 || hit == 0 {
		return uiaResult{}, ErrUnsupported
	}
	el := comPtr(hit)
	defer el.release()
	return uiaSelectionFromElement(el)
}

// uiaSelectionAt 取屏幕坐标处的**文本选区**。
//
// 与 MSAA 的本质差别：MSAA 给的是"这个元素的值"，UIA 给的是"用户的选区"。
// 候选元素按「焦点优先」的顺序试，返回第一个真有非空选区的。
func uiaSelectionAt(pt point) (uiaResult, error) {
	elems := uiaCandidateElements(pt)
	if len(elems) == 0 {
		return uiaResult{}, ErrUnsupported
	}
	defer func() {
		for _, e := range elems {
			e.release()
		}
	}()

	var lastErr error = ErrNoSelection
	for _, el := range elems {
		res, err := uiaSelectionFromElement(el)
		if err == nil {
			return res, nil
		}
		// ErrUnsupported 保留下来（说明这个元素不支持 TextPattern），
		// 但只要有元素给出「没有选区」，那才是更准确的答案。
		if !errors.Is(err, ErrNoSelection) {
			lastErr = err
		}
	}
	return uiaResult{}, lastErr
}

// uiaSelectionFromElement 从单个元素上取选区。
func uiaSelectionFromElement(el comPtr) (uiaResult, error) {
	// 密码框绝不取
	if pwd, ok := el.isPassword(); ok && pwd {
		return uiaResult{}, ErrNoSelection
	}

	tp, ok := el.currentPattern(uiaTextPatternId)
	if !ok {
		if tp, ok = el.currentPattern(uiaTextPattern2Id); !ok {
			return uiaResult{}, ErrUnsupported
		}
	}
	defer tp.release()

	var arr uintptr
	hr, _, _ := syscall.SyscallN(comVtblMethod(tp, textPatternGetSelection),
		uintptr(tp), uintptr(unsafe.Pointer(&arr)))
	if int32(hr) < 0 || arr == 0 {
		return uiaResult{}, ErrNoSelection
	}
	ra := comPtr(arr)
	defer ra.release()

	var n int32
	hr, _, _ = syscall.SyscallN(comVtblMethod(ra, textRangeArrayGetLength),
		uintptr(ra), uintptr(unsafe.Pointer(&n)))
	if int32(hr) < 0 || n <= 0 {
		return uiaResult{}, ErrNoSelection
	}

	var rng uintptr
	hr, _, _ = syscall.SyscallN(comVtblMethod(ra, textRangeArrayGetElement),
		uintptr(ra), 0, uintptr(unsafe.Pointer(&rng)))
	if int32(hr) < 0 || rng == 0 {
		return uiaResult{}, ErrNoSelection
	}
	rg := comPtr(rng)
	defer rg.release()

	// GetText(-1)：maxLength = -1 表示全取。
	// 参数是 32 位 int，用 uint32(-1) 避免符号扩展成 64 位的大数。
	var bstr uintptr
	hr, _, _ = syscall.SyscallN(comVtblMethod(rg, textRangeGetText),
		uintptr(rg), uintptr(uint32(0xFFFFFFFF)), uintptr(unsafe.Pointer(&bstr)))
	if int32(hr) < 0 || bstr == 0 {
		return uiaResult{}, ErrNoSelection
	}
	defer pSysFreeString.Call(bstr)

	text := bstrToString(bstr)
	if strings.TrimSpace(text) == "" {
		return uiaResult{}, ErrNoSelection
	}

	res := uiaResult{text: text}
	if b, ok := textRangeLastRect(rg); ok {
		res.bounds, res.hasBounds = b, true
	}
	return res, nil
}

// textRangeLastRect 取选区矩形列表里的**最后一个**。
//
// 一段跨行的选区会有多个矩形，最后一个就是选区的末尾 ——
// 把菜单弹在那里最贴近用户刚划完的位置。
func textRangeLastRect(rg comPtr) (rect, bool) {
	var psa uintptr
	hr, _, _ := syscall.SyscallN(comVtblMethod(rg, textRangeGetBoundingRectangles),
		uintptr(rg), uintptr(unsafe.Pointer(&psa)))
	if int32(hr) < 0 || psa == 0 {
		return rect{}, false
	}
	defer pSafeArrayDestroy.Call(psa)

	var lb, ub int32
	if r, _, _ := pSafeArrayGetLBound.Call(psa, 1, uintptr(unsafe.Pointer(&lb))); int32(r) < 0 {
		return rect{}, false
	}
	if r, _, _ := pSafeArrayGetUBound.Call(psa, 1, uintptr(unsafe.Pointer(&ub))); int32(r) < 0 {
		return rect{}, false
	}
	n := int(ub-lb) + 1
	if n < 4 {
		return rect{}, false
	}

	var data unsafe.Pointer
	if r, _, _ := pSafeArrayAccessData.Call(psa, uintptr(unsafe.Pointer(&data))); int32(r) < 0 || data == nil {
		return rect{}, false
	}
	defer pSafeArrayUnaccessData.Call(psa)

	// 每 4 个 double 一组：left, top, width, height
	vals := unsafe.Slice((*float64)(data), n)
	last := vals[n-4:]
	if last[2] <= 0 || last[3] <= 0 {
		return rect{}, false
	}
	return rect{
		Left:   int32(last[0]),
		Top:    int32(last[1]),
		Right:  int32(last[0] + last[2]),
		Bottom: int32(last[1] + last[3]),
	}, true
}

// ---------------------------------------------------------------- source

// uiaSource 把 UIA 接进取文 pipeline。
//
// 放在**剪贴板之后、MSAA 之前**：
//   - 剪贴板能拿到精确选区时不必多此一举，也不会引入回归；
//   - 剪贴板拿不到时（典型：VS Code 集成终端里 Ctrl+C 不是复制），
//     UIA 给的是**精确选区 + 选区矩形**，比 MSAA 的"这一行"准得多。
//
// 等实测确认 UIA 在各场景都可靠之后，把它提到第一位就能连 Ctrl+C 的副作用
// 一起去掉 —— 那是切片顺序的一行之改。
type uiaSource struct{}

func (uiaSource) Name() sourceID { return sourceUIA }

// Available：真控制台上 UIA 也读不到选区（那里的文本在控制台缓冲区里，
// 要用控制台自己的 API），所以和别的源一样判为不可用。
func (uiaSource) Available(c captureContext) bool { return !c.IsConsole }

func (uiaSource) Read(_ captureContext, at point) (selection, error) {
	r, err := uiaSelectionAt(at)
	if err != nil {
		return selection{}, err
	}
	return selection{
		Text:      r.text,
		Bounds:    r.bounds,
		HasBounds: r.hasBounds,
	}, nil
}
