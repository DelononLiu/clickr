//go:build windows

// windows.go —— 窗口类注册与窗口创建。

package main

import (
	"syscall"
	"unsafe"
)

const (
	classBall = "NexusKBFloatBall"
	classMenu = "NexusKBSelectMenu"
)

var wndProcAddr = syscall.NewCallback(wndProc)

func registerWindowClass(name string, cursorID uintptr) error {
	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   wndProcAddr,
		HInstance:     getModuleHandle(),
		HCursor:       loadCursor(cursorID),
		LpszClassName: utf16Ptr(name),
	}
	atom, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return err
	}
	return nil
}

// createLayeredWindow 建一个初始不可见的弹出窗口。
// 注意 WS_POPUP + 四个扩展样式，以及**没有** WS_VISIBLE —— 显示交给 present()。
func createLayeredWindow(class string) (uintptr, error) {
	ex := uintptr(wsExLayered | wsExTopmost | wsExToolWindow | wsExNoActivate)
	h, _, err := pCreateWindowExW.Call(
		ex,
		uintptr(unsafe.Pointer(utf16Ptr(class))),
		uintptr(unsafe.Pointer(utf16Ptr("NexusKB"))),
		uintptr(wsPopup),
		0, 0, 10, 10,
		0, 0, getModuleHandle(), 0)
	if h == 0 {
		return 0, err
	}
	return h, nil
}

func createWindows() error {
	if err := registerWindowClass(classBall, idcHand); err != nil {
		return err
	}
	if err := registerWindowClass(classMenu, idcArrow); err != nil {
		return err
	}
	var err error
	if hwndBall, err = createLayeredWindow(classBall); err != nil {
		return err
	}
	if hwndMenu, err = createLayeredWindow(classMenu); err != nil {
		return err
	}
	return nil
}
