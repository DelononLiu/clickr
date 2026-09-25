//go:build windows

// msaa.go —— 用 MSAA（Microsoft Active Accessibility）读「鼠标底下那段文字」。
//
// 这是有道词典走的那条路：它的 TextExtractorImpl64.dll 只导入了
// oleacc.dll 的 AccessibleObjectFromPoint / AccessibleObjectFromWindow，
// 整个安装目录 48 个模块里没有任何一个用 UIAutomationCore。
//
// 为什么在 Go 里做 MSAA 比做 UIA 划算得多：
// IAccessible 是 **IDispatch 派生**，vtable 里前 7 个槽位固定是
// IUnknown + IDispatch，后面 21 个是 acc* 方法，顺序自 Win95 起就没变过。
// 所以只要按序号取函数指针直接调即可，不用去实现 IDispatch::Invoke 的参数封送。
// 相比之下 UIA 的 IUIAutomationTextPattern/TextRange/TextRangeArray 都得自己
// 手写 vtable，且各接口方法多、顺序抄错就直接崩（那是下一步 B 的事）。
//
// # 它能给你什么、不能给你什么
//
// 能：某个屏幕坐标下的可访问对象的文本（accValue / accName）、角色、状态、
// 屏幕矩形（accLocation）。对 VS Code 这类 Electron 应用，集成终端和编辑器
// 的文本都在可访问性树里，所以终端里的文字也读得到。
//
// 不能：**选区**。MSAA 没有文本范围（range）API，拿不到「用户拖选的那一段」，
// 只能拿到「鼠标点所在的那个元素/词/行」。所以在本项目里它是剪贴板法
// （能拿到精确选区）的**兜底**，而不是替代品。
package main

import (
	"strings"
	"syscall"
	"unsafe"
)

// ================================================================ COM 基础设施

// comPtr 是一个 COM 接口指针（对象地址）。
//
// 对象内存的头 8 字节就是 vtable 地址，vtable[n] 是第 n 个方法的函数地址。
//
// 底层用 uintptr 而不是 unsafe.Pointer：Go 不允许给「底层类型是指针」的
// 类型定义方法（invalid receiver type），uintptr 才可以。
type comPtr uintptr

// IUnknown / IDispatch 的固定槽位
const (
	vtblQueryInterface = 0
	vtblAddRef         = 1
	vtblRelease        = 2
)

// IAccessible 的槽位序号。前 7 个继承自 IUnknown + IDispatch，
// 之后按 oleacc.h 里的声明顺序排列（这个顺序自 Windows 95 起固定不变）。
// 槽位分成两种具名类型：返回 BSTR 的和返回 VARIANT(int) 的。
// 这样「把取名字的方法传给取角色的调用」在编译期就不成立 ——
// 之前两者都是裸 int，抄错槽位号编译器不会说话。
type accStringSlot int
type accIntSlot int

const (
	accGetAccName  accStringSlot = 10
	accGetAccValue accStringSlot = 11
)

const (
	accGetAccRole  accIntSlot = 13
	accGetAccState accIntSlot = 14
)

// accAccLocation 按序号直接调，不属于上面两类
const accAccLocation = 22

func comVtblMethod(p comPtr, index int) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(p))
	return *(*uintptr)(unsafe.Pointer(vtbl + uintptr(index)*unsafe.Sizeof(uintptr(0))))
}

// release 调 IUnknown::Release。调用方必须保证不会重复释放。
func (p comPtr) release() {
	if p == 0 {
		return
	}
	syscall.SyscallN(comVtblMethod(p, vtblRelease), uintptr(p))
}

// ================================================================ VARIANT

// variant 对应 Win32 的 VARIANT。
//
// x64 下共 24 字节：vt(2) + 保留(6) + union(16)。
// union 里最大的成员是 DECIMAL（16 字节），所以整体 24 字节、8 字节对齐。
//
// 我们只用到两种形态：
//   - VT_I4      → Val 的低 4 字节是子元素 id（0 表示对象自身，CHILDID_SELF）
//   - VT_DISPATCH→ Val 是子对象的 IDispatch*
type variant struct {
	VT        uint16
	Reserved1 uint16
	Reserved2 uint16
	Reserved3 uint16
	Val       uint64
	_         uint64 // 把 union 补到 16 字节
}

const (
	vtEmpty    = 0
	vtI4       = 3
	vtBstr     = 8
	vtDispatch = 9
	vtBool     = 11 // VARIANT_BOOL：0 = false，0xFFFF = true
)

// childID 返回 VT_I4 形态的取值。
func (v *variant) childID() int32 {
	if v.VT == vtI4 {
		return int32(v.Val)
	}
	return 0
}

func (v *variant) clear() {
	if v == nil {
		return
	}
	pVariantClear.Call(uintptr(unsafe.Pointer(v)))
}

// selfChild 表示「就是对象自身」而不是某个子元素。
func selfChild() variant { return variant{VT: vtI4, Val: 0} }

// ================================================================ IAccessible 调用

// accString 调 get_accName / get_accValue 这类返回 BSTR 的方法。
func (p comPtr) accString(slot accStringSlot, child *variant) (string, bool) {
	var bstr uintptr
	hr, _, _ := syscall.SyscallN(comVtblMethod(p, int(slot)),
		uintptr(p),
		uintptr(unsafe.Pointer(child)),
		uintptr(unsafe.Pointer(&bstr)))
	if int32(hr) < 0 || bstr == 0 {
		return "", false
	}
	defer pSysFreeString.Call(bstr)
	return bstrToString(bstr), true
}

// accInt 调 get_accRole / get_accState 这类返回 VARIANT(int) 的方法。
func (p comPtr) accInt(slot accIntSlot, child *variant) (int32, bool) {
	var out variant
	// defer 必须放在 hr 判断**之前**：失败时 provider 仍可能往 out 里写了
	// 东西（BSTR / IDispatch），提前 return 会漏掉 VariantClear，泄漏。
	// defer 是栈式的，放在这里对成功路径同样成立。
	hr, _, _ := syscall.SyscallN(comVtblMethod(p, int(slot)),
		uintptr(p),
		uintptr(unsafe.Pointer(child)),
		uintptr(unsafe.Pointer(&out)))
	defer out.clear()
	if int32(hr) < 0 {
		return 0, false
	}
	if out.VT != vtI4 {
		return 0, false
	}
	return int32(out.Val), true
}

// accLocation 取该可访问对象在屏幕上的矩形（物理像素）。
func (p comPtr) accLocation(child *variant) (rect, bool) {
	var l, t, w, h int32
	hr, _, _ := syscall.SyscallN(comVtblMethod(p, accAccLocation),
		uintptr(p),
		uintptr(unsafe.Pointer(&l)),
		uintptr(unsafe.Pointer(&t)),
		uintptr(unsafe.Pointer(&w)),
		uintptr(unsafe.Pointer(&h)),
		uintptr(unsafe.Pointer(child)))
	if int32(hr) < 0 || w <= 0 || h <= 0 {
		return rect{}, false
	}
	return rect{l, t, l + w, t + h}, true
}

// bstrToString 把 BSTR 转成 Go 字符串。
//
// BSTR 是「前面 4 字节存长度、紧跟 UTF-16 字符」的指针，
// 所以从指针位置往后就是字符数据。
func bstrToString(b uintptr) string {
	n, _, _ := pSysStringLen.Call(b)
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(b)), int(n)))
}

// ================================================================ 对外接口

// STATE_SYSTEM_PROTECTED：密码框。MSAA 里就靠这个位判断。
const stateSystemProtected = 0x40000000

// msaaResult 是 MSAA 一次取词的结果。
type msaaResult struct {
	text     string
	bounds   rect
	hasBound bool
	viaValue bool // true=来自 accValue，false=来自 accName
}

// msaaTextAt 取屏幕坐标 pt 处可访问对象的文本。
//
// 调用前必须已经在**当前线程**上 OleInitialize 过。
func msaaTextAt(pt point) (msaaResult, bool) {
	var acc uintptr
	var child variant
	hr, _, _ := pAccessibleObjectFromPoint.Call(
		packPoint(pt),
		uintptr(unsafe.Pointer(&acc)),
		uintptr(unsafe.Pointer(&child)))
	if int32(hr) < 0 || acc == 0 {
		return msaaResult{}, false
	}
	a := comPtr(acc)
	defer a.release()
	defer child.clear()

	// 密码框直接放弃。MSAA 用 accState 的 STATE_SYSTEM_PROTECTED 位表示。
	if state, ok := a.accInt(accGetAccState, &child); ok && state&stateSystemProtected != 0 {
		return msaaResult{}, false
	}

	res := msaaResult{}
	if b, ok := a.accLocation(&child); ok {
		res.bounds, res.hasBound = b, true
	}

	// 值优先（可编辑控件、文本视图的文字在 accValue），
	// 拿不到再退回名字（静态文本节点的文字通常挂在 accName 上）。
	//
	// 这里返回**原始**文本、不做截断：截断预算是调用方（msaaSource）的事，
	// 提供者去读消费者的常量会把依赖方向弄反。
	if v, ok := a.accString(accGetAccValue, &child); ok && v != "" {
		res.text, res.viaValue = v, true
		return res, true
	}
	if n, ok := a.accString(accGetAccName, &child); ok && n != "" {
		res.text = n
		return res, true
	}
	return msaaResult{}, false
}

// normalizeMSAAText 清理可访问对象返回的文本。
//
// 它可能带大量首尾空白（尤其是控制台/文本视图），也可能是一整屏内容
// （比如整个控制台缓冲区），这里统一去空白并按预算截断。
//
// 预算由调用方传入，而不是读包级常量 —— 之前 msaa.go 反过来读 capture.go 的常量，
// 等于「提供者知道消费者的预算」，依赖方向是反的。
func normalizeMSAAText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
}

// initMSAA 在当前线程初始化 OLE。必须在调用 msaaTextAt 之前做，
// 而且必须由**同一个线程**来做 —— COM 是按线程初始化的。
//
// 不记录「是否初始化过」：那是线程局部的状态，用包级变量表达反而是错的
// （一个线程初始化了，不代表另一个线程也能用）。
func initMSAA() error {
	hr, _, err := pOleInitialize.Call(0)
	if int32(hr) < 0 {
		return err
	}
	return nil
}
