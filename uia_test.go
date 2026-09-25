//go:build windows

// uia_test.go —— UIA TextPattern + vtable 槽位探测工具。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o nkb.test.exe .
//	./nkb.test.exe -test.v

package main

import (
	"flag"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// 这一层最容易因为 vtable 槽位抄错而崩或返回垃圾。
// 用「控制类型必须是合理值」来体检 IUIAutomationElement 的槽位偏移。
func TestUIAElementVtableOffsets(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initMSAA(); err != nil {
		t.Skipf("OleInitialize 失败: %v", err)
	}
	if err := initUIA(); err != nil {
		t.Skipf("该环境拿不到 CUIAutomation: %v", err)
	}

	hwnd, edit := createEditTestWindow(t, "NexusKB-UIA-OFFSET-CHECK")
	defer pDestroyWindow.Call(hwnd)

	r, _ := windowRect(edit)
	target := point{(r.Left + r.Right) / 2, (r.Top + r.Bottom) / 2}

	elems := uiaCandidateElements(target)
	if len(elems) == 0 {
		t.Skipf("拿不到任何候选元素（可能被别的窗口盖住）")
	}
	defer func() {
		for _, e := range elems {
			e.release()
		}
	}()
	el := elems[len(elems)-1] // 命中测试的那个（Edit 控件本身）
	t.Logf("候选元素 %d 个", len(elems))

	// 槽位取错会读到别的属性，值就不可能是合理的控件类型
	ct, ok := el.controlType()
	if !ok {
		t.Fatal("读不到 ControlType —— IUIAutomationElement 的槽位可能错位")
	}
	t.Logf("命中元素控件类型 = %d", ct)
	if ct < 50000 || ct > 50060 {
		t.Errorf("控件类型 %d 不是合法的 UIA 控件类型，槽位大概率错位了", ct)
	}

	// 密码位必须可读且为 false（这不是密码框）
	if pwd, ok := el.isPassword(); ok && pwd {
		t.Error("普通 EDIT 被判成了密码框 —— IsPassword 槽位可能错位")
	}

	// 单验 GetCurrentPattern 的槽位：EDIT 一定有 ValuePattern。
	// 拿不到就说明槽位 16 错了（这一步不依赖 TextPattern 是否存在）。
	if _, ok := el.currentPattern(uiaValuePatternId); !ok {
		t.Error("连 ValuePattern 都拿不到 —— IUIAutomationElement 的 GetCurrentPattern 槽位错了")
	}
}

// 端到端：给 EDIT 控件设一个已知选区，用 UIA 读回来。
//
// 这条同时验证 IUIAutomationTextPattern / TextRangeArray / TextRange 三层的槽位，
// 以及 BSTR 解码与 GetBoundingRectangles 的 SAFEARRAY 读取。
func TestUIAReadsRealSelection(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initMSAA(); err != nil {
		t.Skipf("OleInitialize 失败: %v", err)
	}
	if err := initUIA(); err != nil {
		t.Skipf("该环境拿不到 CUIAutomation: %v", err)
	}

	const full = "AAA-BBB-CCC-DDD"
	hwnd, edit := createEditTestWindow(t, full)
	defer pDestroyWindow.Call(hwnd)

	// 让控件真的拿到焦点。UIA 的 GetSelection 取的是「当前选区」，
	// 而很多 provider 在控件没有焦点时会返回一个退化区间甚至整篇内容 ——
	// 这一点很重要：真实划词时源程序本来就是前台，所以这不是限制，
	// 但测试环境里必须显式模拟出来。
	pSetForegroundWindow.Call(hwnd)
	pSetFocus.Call(edit)
	time.Sleep(150 * time.Millisecond)

	// 选中 "BBB"（4..7）
	const (
		emSetSel = 0x00B1
		emGetSel = 0x00B0
	)
	pSendMessageW.Call(edit, emSetSel, 4, 7)
	time.Sleep(200 * time.Millisecond)

	// 先确认控件自己的选区确实是 4..7（排除「EM_SETSEL 没生效」这条岔路）
	sel, _, _ := pSendMessageW.Call(edit, emGetSel, 0, 0)
	start, end := int32(uint16(sel&0xFFFF)), int32(uint16((sel>>16)&0xFFFF))
	t.Logf("控件自报选区 = [%d,%d)", start, end)
	if start != 4 || end != 7 {
		t.Fatalf("EM_SETSEL 没生效（控件自报 [%d,%d)），后面的断言没有意义", start, end)
	}

	r, _ := windowRect(edit)
	target := point{(r.Left + r.Right) / 2, (r.Top + r.Bottom) / 2}

	res, err := uiaSelectionAt(target)
	if err != nil {
		t.Fatalf("UIA 读选区失败: %v", err)
	}
	t.Logf("UIA 读到: text=%q bounds=%v hasBounds=%v", res.text, res.bounds, res.hasBounds)
	if res.text != "BBB" {
		t.Errorf("期望选中 %q，实际 %q", "BBB", res.text)
	}
	if !res.hasBounds {
		t.Error("应当能拿到选区矩形 —— 这正是 UIA 相对剪贴板法的主要收益")
	}
}

// uiaSlotProbe 用来从命令行指定要探测的 vtable 槽位。
//
// 用 flag 而不是环境变量：经 WSL interop 启动时，Linux 侧的环境变量
// 默认不会传给 Windows 进程（只有 WSLENV 里列出的才会），实测踩过。
var uiaSlotProbe = flag.Int("uia-slot", 0, "把哪个 IUIAutomationTextPattern 槽位当 GetSelection 调用来探测")

// 机械探测：把某个槽位当作 GetSelection 调一次，把结果打出来。
// 槽位号从环境变量 NKB_SLOT 读 —— 每个槽位单独跑一个进程，
// 这样即使某个槽位是错的（会直接崩），也只影响那一次探测。
func TestUIASlotProbe(t *testing.T) {
	if *uiaSlotProbe == 0 {
		t.Skip("未指定 -uia-slot")
	}
	slot := *uiaSlotProbe

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initMSAA(); err != nil {
		t.Skipf("OleInitialize 失败: %v", err)
	}
	if err := initUIA(); err != nil {
		t.Skipf("拿不到 CUIAutomation: %v", err)
	}
	hwnd, edit := createEditTestWindow(t, "AAA-BBB-CCC-DDD")
	defer pDestroyWindow.Call(hwnd)
	pSetForegroundWindow.Call(hwnd)
	pSetFocus.Call(edit)
	time.Sleep(150 * time.Millisecond)
	pSendMessageW.Call(edit, 0x00B1, 4, 7)
	time.Sleep(200 * time.Millisecond)

	r, _ := windowRect(edit)
	target := point{(r.Left + r.Right) / 2, (r.Top + r.Bottom) / 2}

	elems := uiaCandidateElements(target)
	if len(elems) == 0 {
		t.Skip("拿不到元素")
	}
	el := elems[0]
	defer el.release()

	tp, ok := el.currentPattern(uiaTextPatternId)
	if !ok {
		t.Skip("无 TextPattern")
	}
	defer tp.release()

	t.Logf("把槽位 %d 当 GetSelection 调用…", slot)
	var arr uintptr
	hr, _, _ := syscall.SyscallN(comVtblMethod(tp, slot),
		uintptr(tp), uintptr(unsafe.Pointer(&arr)))
	t.Logf("  hr=0x%08X arr=%#x", uint32(hr), arr)
	if int32(hr) < 0 || arr == 0 {
		return
	}
	ra := comPtr(arr)
	defer ra.release()
	var n int32
	scriptHr, _, _ := syscall.SyscallN(comVtblMethod(ra, textRangeArrayGetLength),
		uintptr(ra), uintptr(unsafe.Pointer(&n)))
	t.Logf("  (把返回值当数组读长度) hr=0x%08X n=%d", uint32(scriptHr), n)
	for k := int32(0); k < n && k < 3; k++ {
		var rng uintptr
		syscall.SyscallN(comVtblMethod(ra, textRangeArrayGetElement),
			uintptr(ra), uintptr(k), uintptr(unsafe.Pointer(&rng)))
		if rng == 0 {
			continue
		}
		rg := comPtr(rng)
		var bstr uintptr
		syscall.SyscallN(comVtblMethod(rg, textRangeGetText),
			uintptr(rg), uintptr(uint32(0xFFFFFFFF)), uintptr(unsafe.Pointer(&bstr)))
		txt := ""
		if bstr != 0 {
			txt = bstrToString(bstr)
			pSysFreeString.Call(bstr)
		}
		raw := textRangeRects(rg)
		b, okb := selectionBounds(raw)
		t.Logf("    段 %d: text=%.30q bounds=%v ok=%v 原始矩形 %d 个: %v", k, txt, b, okb, len(raw), raw)
		rg.release()
	}
}
