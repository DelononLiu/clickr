//go:build windows

// hook.go —— 全局低层鼠标钩子（WH_MOUSE_LL）。
//
// 两个必须遵守的规则：
//
//  1. 钩子必须装在「有自己的消息循环、且被 LockOSThread 锁死」的线程上。
//     Go 的 goroutine 会被调度到不同 OS 线程，不锁线程钩子就会失效。
//
//  2. 钩子回调里绝对不做任何耗时的事。Windows 有个 LowLevelHooksTimeout
//     （默认 300ms），回调超时会被**静默摘钩** —— 不报错、不回调，
//     表现就是「用一会儿就不灵了，重启才好」。所以回调只做一件事：
//     把事件塞进带缓冲的 channel（非阻塞），其余逻辑全在别的 goroutine 里跑。
package main

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const (
	evDown = iota + 1
	evUp
	evRightUp
)

type hookEvent struct {
	kind int
	pt   point
}

var (
	hookCh = make(chan hookEvent, 128)

	// 拖动/双击的判定阈值都取系统度量（已按 DPI 缩放），见 dragThreshold()。
	// 曾经写死 5px / 4px：200% 缩放下那只等效 2.5 / 2 逻辑像素，手一抖就算划词。
	// 每次手势现取，这样拖动窗口到别的缩放显示器上也会跟着变。
)

// 回调只创建一次并常驻；syscall.NewCallback 的返回值必须被持有，
// 否则可能被 GC 回收掉。
var mouseHookProc = syscall.NewCallback(func(nCode uintptr, wParam, lParam uintptr) uintptr {
	if int32(nCode) == hcAction {
		info := (*msllHookStruct)(unsafe.Pointer(lParam))
		var ev hookEvent
		switch uint32(wParam) {
		case wmLButtonDown:
			ev = hookEvent{kind: evDown, pt: info.Pt}
		case wmLButtonUp:
			ev = hookEvent{kind: evUp, pt: info.Pt}
		case wmRButtonUp:
			ev = hookEvent{kind: evRightUp, pt: info.Pt}
		}
		if ev.kind != 0 {
			// 非阻塞投递。丢掉一两个事件无所谓，卡住回调才是致命的。
			select {
			case hookCh <- ev:
			default:
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
})

// startMouseHook 在独立线程上装钩子并跑消息循环。返回后钩子即已就绪。
func startMouseHook(onGesture func(anchor point, how string), onOutsideClick func(pt point)) error {
	if err := installHook(); err != nil {
		return err
	}
	go consumeHookEvents(hookCh, onGesture, onOutsideClick)
	return nil
}

func installHook() error {
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // 锁死这个 OS 线程，消息循环和回调都在它上面
		defer runtime.UnlockOSThread()

		h, _, err := pSetWindowsHookExW.Call(whMouseLL, mouseHookProc, 0, 0)
		if h == 0 {
			ready <- err
			return
		}
		ready <- nil

		var m msg
		for {
			r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 { // 0 = WM_QUIT, -1 = 错误
				break
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		pUnhookWindowsHookEx.Call(h)
	}()
	return <-ready
}

func consumeHookEvents(events <-chan hookEvent, onGesture func(point, string), onOutsideClick func(point)) {
	var (
		downPt     point
		haveDown   bool
		lastUpTime uint64
		lastUpPt   point
	)
	dblMs := uint64(getDoubleClickTime())

	for ev := range events {
		switch ev.kind {
		case evDown:
			haveDown = true
			downPt = ev.pt
			// 点到菜单/悬浮球外面就把菜单收起来。
			// 这里用「命中测试」而不是看坐标是否在窗口矩形内，
			// 因为分层窗口的透明区域是自动穿透的。
			if onOutsideClick != nil {
				onOutsideClick(ev.pt)
			}

		case evUp:
			if !haveDown {
				continue
			}
			haveDown = false

			dxTh, dyTh := dragThreshold()
			dx := abs32(ev.pt.X - downPt.X)
			dy := abs32(ev.pt.Y - downPt.Y)
			if dx > dxTh || dy > dyTh {
				// 拖动 → 这是一次框选
				lastUpTime = 0
				dispatchGesture(onGesture, ev.pt, "drag")
				continue
			}

			// 单击：可能是双击选词的第一下，也可能是第二下
			now := tickCount64()
			proxX, proxY := doubleClickProximity()
			if lastUpTime != 0 &&
				now-lastUpTime <= dblMs &&
				abs32(ev.pt.X-lastUpPt.X) <= proxX &&
				abs32(ev.pt.Y-lastUpPt.Y) <= proxY {
				lastUpTime = 0
				dispatchGesture(onGesture, ev.pt, "double-click")
				continue
			}
			lastUpTime = now
			lastUpPt = ev.pt

		case evRightUp:
			haveDown = false
		}
	}
}

// dispatchGesture 过滤掉落在本进程窗口上的手势，再交给上层。
// 否则点自己的菜单会又被当成一次划词，形成死循环。
func dispatchGesture(onGesture func(point, string), pt point, how string) {
	if onGesture == nil || isOwnWindow(pt) {
		return
	}
	go onGesture(pt, how)
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func getDoubleClickTime() uint32 {
	v, _, _ := pGetDoubleClickTime.Call()
	if v == 0 {
		return 500
	}
	return uint32(v)
}

// sleepMS 只是为了让 capture 那边的意图更清楚。
func sleepMS(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

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
