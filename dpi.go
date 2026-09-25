//go:build windows

// dpi.go —— DPI 感知与查询。
//
// 顺序敏感：setDPIAwareness 必须在创建任何窗口之前调用，
// 否则进程是 DPI-unaware 的，系统会把坐标虚拟化，多显示器下弹窗会飘。

package main

import "unsafe"

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
