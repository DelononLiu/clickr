//go:build windows

// wndproc.go —— 窗口过程与鼠标交互；跨线程投递的唯一出口。

package main

import (
	"log"
	"unsafe"
)

// wndProc 是所有窗口消息的入口。
//
// 外面的 recover 是必须的：这个函数由 Go 以 C 回调的形式被 Windows 调用，
// 一旦 panic 冒泡出去，Go 运行时**无法把 panic 交回 C 调用者**，
// 进程会直接静默死掉（没有日志、没有报错框），排查时毫无线索。
func wndProc(hwnd uintptr, message uint32, wparam, lparam uintptr) (ret uintptr) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[wndproc] 处理消息 %#x 时 panic: %v", message, r)
			ret = 0
		}
	}()
	return wndProcImpl(hwnd, message, wparam, lparam)
}

func wndProcImpl(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	switch message {
	case wmNCHitTest:
		x := int32(int16(lparam & 0xFFFF))
		y := int32(int16((lparam >> 16) & 0xFFFF))
		return hitTest(hwnd, point{x, y})

	case wmMouseMove:
		onMouseMove(hwnd, lparam)

	case wmMouseLeave:
		if hwnd == hwndMenu {
			changed := menuHover != -1
			menuHover = -1
			if changed {
				renderMenu()
				menuSurf.present(hwndMenu, menuPos.X, menuPos.Y)
			}
		}

	case wmLButtonDown:
		onLButtonDown(hwnd, lparam)

	case wmLButtonUp:
		onLButtonUp(hwnd, lparam)

	case wmRButtonUp:
		if hwnd == hwndBall {
			onBallRightClick()
		}

	case wmDpiChanged:
		// 悬浮球被拖到另一块不同缩放的显示器上
		if hwnd == hwndBall {
			scale = float64(dpiForPoint(getCursorPos())) / 96.0
			recreateSurfaces()
			moveBallTo(constrainBall(ballPos))
		}

	case wmShowMenu:
		stateMu.Lock()
		model, anchor := pendingMenu, pendingAt
		stateMu.Unlock()
		if len(model.items) > 0 {
			showMenu(anchor, model)
		}

	case wmHideMenu:
		hideMenu()

	case wmQuitApp:
		hideMenu()
		pDestroyWindow.Call(hwndBall)
		pDestroyWindow.Call(hwndMenu)
		pPostQuitMessage.Call(0)

	case wmDestroy:
		pPostQuitMessage.Call(0)
	}

	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(message), wparam, lparam)
	return r
}

func onMouseMove(hwnd uintptr, lparam uintptr) {
	pt := getCursorPos()

	if hwnd == hwndBall {
		if ballDragging {
			nx := pt.X - ballGrabOff.X
			ny := pt.Y - ballGrabOff.Y
			ballMoved = true
			moveBallTo(point{nx, ny})
		}
		return
	}

	// 菜单：算出 hover 项，只有变化时才重绘
	tme := trackMouseEvent{CbSize: uint32(unsafe.Sizeof(trackMouseEvent{})),
		DwFlags: tmeLeave, HwndTrack: hwndBall}
	if hwnd == hwndMenu {
		tme.HwndTrack = hwndMenu
	}
	pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))

	local := point{pt.X - menuPos.X, pt.Y - menuPos.Y}
	idx := -1
	for i := range menuModel_.items {
		if menuItemRect(i).contains(local) {
			idx = i
			break
		}
	}
	changed := idx != menuHover
	menuHover = idx
	if changed {
		renderMenu()
		menuSurf.present(hwndMenu, menuPos.X, menuPos.Y)
	}
}

func onLButtonDown(hwnd uintptr, lparam uintptr) {
	pt := getCursorPos()
	if hwnd == hwndBall {
		ballDragging = true
		ballMoved = false
		ballGrabOff = point{pt.X - ballPos.X, pt.Y - ballPos.Y}
		return
	}
	if hwnd == hwndMenu {
		local := point{pt.X - menuPos.X, pt.Y - menuPos.Y}
		menuPressed = -1
		for i := range menuModel_.items {
			if menuItemRect(i).contains(local) {
				menuPressed = i
				break
			}
		}
	}
}

func onLButtonUp(hwnd uintptr, lparam uintptr) {
	pt := getCursorPos()

	if hwnd == hwndBall {
		ballDragging = false
		if ballMoved {
			saveBallPos(constrainBall(ballPos))
			moveBallTo(constrainBall(ballPos))
			return
		}
		// 单击悬浮球：把上一次选中的文字再弹一次（相当于「重开菜单」）
		if sel := getLastSelection(); sel != "" {
			queueMenu(pt, selectionMenu(sel))
		}
		return
	}

	if hwnd == hwndMenu {
		local := point{pt.X - menuPos.X, pt.Y - menuPos.Y}
		pressed := menuPressed
		menuPressed = -1
		var chosen *menuItem
		if pressed >= 0 && pressed < len(menuModel_.items) &&
			menuItemRect(pressed).contains(local) {
			it := menuModel_.items[pressed]
			chosen = &it
		}

		hideMenu()
		if chosen != nil && chosen.run != nil {
			go chosen.run()
		}
	}
}

func onBallRightClick() {
	model := menuModel{items: []menuItem{
		{title: "退出", shortcut: "", icon: iconClose, run: func() {
			postToMain(wmQuitApp)
		}},
	}}
	queueMenu(ballCenter(), model)
}

// queueMenu 从任意线程请求主线程弹菜单。
func queueMenu(anchor point, model menuModel) {
	stateMu.Lock()
	pendingAt, pendingMenu = anchor, model
	stateMu.Unlock()
	postToMain(wmShowMenu)
}

func postToMain(message uint32) {
	pPostMessageW.Call(hwndBall, uintptr(message), 0, 0)
}
