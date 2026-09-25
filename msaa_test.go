//go:build windows

// msaa_test.go —— MSAA 取词 + VARIANT 布局。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o nkb.test.exe .
//	./nkb.test.exe -test.v

package main

import (
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// 用一个自己创建的、内容已知的窗口来端到端验证 MSAA 通路。
//
// 这个测试的价值在于：vtable 槽位序号（accGetAccValue=11 / accGetAccName=10 /
// accGetAccRole=13 / accGetAccState=14 / accAccLocation=22）如果抄错一位，
// 这里要么直接崩、要么读到垃圾，不会静默通过。
func TestMSAAReadsTextFromRealControl(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := initMSAA(); err != nil {
		t.Fatalf("OleInitialize 失败: %v", err)
	}

	const cls = "NexusKBMSAATestWnd"
	const marker = "NexusKB-MSAA-TEST-8823"
	// 测试窗口自己的 WndProc：全部交给 DefWindowProc，
	// 避免和产品窗口的消息处理互相干扰
	testWndProc := syscall.NewCallback(func(hwnd, message, wparam, lparam uintptr) uintptr {
		r, _, _ := pDefWindowProcW.Call(hwnd, message, wparam, lparam)
		return r
	})

	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   testWndProc,
		HInstance:     getModuleHandle(),
		HbrBackground: 5, // COLOR_WINDOW+1，让窗口真的被画出来
		LpszClassName: utf16Ptr(cls),
	}
	if atom, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		t.Fatalf("RegisterClassExW 失败: %v", err)
	}

	// 放在工作区正中并置顶，保证 AccessibleObjectFromPoint 打得中
	wa := workArea(point{0, 0})
	x := (wa.Left + wa.Right) / 2
	y := (wa.Top + wa.Bottom) / 2
	w, h := int32(420), int32(140)

	hwnd, _, err := pCreateWindowExW.Call(
		uintptr(wsExTopmost),
		uintptr(unsafe.Pointer(utf16Ptr(cls))),
		uintptr(unsafe.Pointer(utf16Ptr("NexusKB MSAA test"))),
		uintptr(wsPopup),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		0, 0, getModuleHandle(), 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW 失败: %v", err)
	}
	defer pDestroyWindow.Call(hwnd)

	static, _, err := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr("Static"))),
		uintptr(unsafe.Pointer(utf16Ptr(marker))),
		// WS_VISIBLE 不能省：WindowFromPoint（MSAA 命中测试的底层）
		// 会跳过不可见窗口，少了它就只会打中父窗口
		uintptr(wsChild|wsVisible|ssLeft),
		24, 56, uintptr(w-48), 28,
		hwnd, 0, getModuleHandle(), 0)
	if static == 0 {
		t.Fatalf("创建 Static 控件失败: %v", err)
	}

	setWindowPos(hwnd, x, y, w, h, swpNoActivate|swpShowWindow)
	time.Sleep(600 * time.Millisecond) // 等它真的上屏

	// 打 Static 控件的中心
	target := point{x + 24 + (w-48)/2, y + 56 + 14}
	res, ok := msaaTextAt(target)
	if !ok {
		// 先确认 vtable 本身是好的：如果连可访问对象都取不到，
		// 那是环境问题（窗口被盖住）而不是我们抄错了槽位号；
		// 但只要能取到对象却读不出文本，那就必须失败，不能跳过。
		var probe uintptr
		var child variant
		hr, _, _ := pAccessibleObjectFromPoint.Call(packPoint(target),
			uintptr(unsafe.Pointer(&probe)), uintptr(unsafe.Pointer(&child)))
		if int32(hr) >= 0 && probe != 0 {
			comPtr(probe).release()
			child.clear()
			t.Fatalf("能取到可访问对象，却读不出文本 —— 这更像 vtable 槽位抄错了，不是环境问题")
		}
		t.Skipf("MSAA 在该环境下取不到可访问对象（前台窗口可能被盖住），跳过")
	}
	t.Logf("MSAA 取到: text=%q viaValue=%v bounds=%v hasBounds=%v",
		res.text, res.viaValue, res.bounds, res.hasBound)
	if !strings.Contains(res.text, marker) {
		t.Errorf("MSAA 取到 %q，期望包含 %q", res.text, marker)
	}
	if !res.hasBound {
		t.Error("应当能拿到 accLocation 的屏幕矩形")
	}
}

// VARIANT 在 x64 下必须是 24 字节：vt(2)+保留(6)+union(16)。
// 布局错了会把栈写坏 —— 这是那种「平时没事、偶尔崩」的 bug。
func TestVariantLayout(t *testing.T) {
	if got := unsafe.Sizeof(variant{}); got != 24 {
		t.Errorf("sizeof(variant) = %d，x64 下应为 24", got)
	}
	if off := unsafe.Offsetof(variant{}.Val); off != 8 {
		t.Errorf("variant.Val 偏移 = %d，应为 8", off)
	}
}

// 自检：MSAA 的 vtable 序号必须落在合理的函数地址上。
// 把每个槽位取出来，确认它们非零且互不相同 —— 顺序抄错时通常是
// 取到了相邻方法的地址，这条能提供一层廉价保护。
func TestMSAAVtableSlotsLookSane(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initMSAA(); err != nil {
		t.Skipf("OleInitialize 失败: %v", err)
	}
	pt := point{getSystemMetrics(0) / 2, getSystemMetrics(1) / 2}
	var acc uintptr
	var child variant
	hr, _, _ := pAccessibleObjectFromPoint.Call(packPoint(pt),
		uintptr(unsafe.Pointer(&acc)), uintptr(unsafe.Pointer(&child)))
	if int32(hr) < 0 || acc == 0 {
		t.Skipf("该环境取不到可访问对象")
	}
	a := comPtr(acc)
	defer a.release()
	defer child.clear()

	// 说明：这条只能证明「取到的地址是真实可执行代码」，**证明不了槽位顺序正确** ——
	// 顺序整体错一位时，六个地址依然互不相同、依然都可执行。
	// 真正守顺序的是端到端那条 TestMSAAReadsTextFromRealControl（它必须读出正确文本）。
	seen := map[uintptr]int{}
	slots := []int{vtblRelease, int(accGetAccName), int(accGetAccValue),
		int(accGetAccRole), int(accGetAccState), accAccLocation}
	for _, slot := range slots {
		addr := comVtblMethod(a, slot)
		if addr == 0 {
			t.Errorf("槽位 %d 的函数地址为 0", slot)
			continue
		}
		if !isExecutableAddress(addr) {
			t.Errorf("槽位 %d 的地址 %#x 不在可执行内存里，vtable 解引用可能越界", slot, addr)
		}
		if prev, dup := seen[addr]; dup {
			t.Errorf("槽位 %d 与槽位 %d 指向同一地址 %#x", slot, prev, addr)
		}
		seen[addr] = slot
	}
}
