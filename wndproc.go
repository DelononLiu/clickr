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

	case wmCtlColorEdit:
		// 原生输入框的底色与文字色跟我们的卡片对齐（它是子窗口，自己不会跟着重画）
		th := loadTheme()
		pSetTextColor.Call(wparam, uintptr(rgbOf(th.bodyText)))
		pSetBkColor.Call(wparam, uintptr(rgbOf(th.fieldBg)))
		return uintptr(fieldBrush(th.fieldBg))

	case wmMouseWheel:
		// 滚轮只对侧边栏有意义（菜单/设置页没有可滚内容）
		if hwnd == hwndSidebar {
			pt := getCursorPos()
			if sidebarHit(point{pt.X - sidebarPos.X, pt.Y - sidebarPos.Y}) != -1 ||
				sidebarHit(point{pt.X - sidebarPos.X, pt.Y - sidebarPos.Y}) == 0 {
				sidebarWheel(int(int16(wparam >> 16)))
			}
			return 0
		}

	case wmMouseLeave:
		if hwnd == hwndSidebar && sidebarHover != -1 {
			sidebarHover = -1
			sidebarRepaint()
		}
		if hwnd == hwndSettings && settingsHover != -1 {
			settingsHover = -1
			renderSettings()
			settingsSurf.present(hwndSettings, settingsPos.X, settingsPos.Y)
		}
		if hwnd == hwndBall && ballHovered {
			ballHovered = false
			refreshBall()
			scheduleHoverHide()
		}
		if hwnd == hwndMenu {
			scheduleHoverHide() // 离开菜单也延时判断（到点时鼠标还在球/菜单上就不收）
		}
		// 菜单那边只清高亮，**不收起面板**：指针从工具条挪到「…」面板要跨过中间那条
		// 6px 的缝，而透明区不吃鼠标消息，所以这条 leave 是必然会来的。
		// 跟着它收起的话，面板会在指针到达之前先消失（疯狂闪烁）。
		if hwnd == hwndMenu {
			// 指针是"过缝去面板"还是"走开了"？在整块里就留着面板。
			// 透明区收不到后续 mousemove，这一步是唯一能分辨的时机。
			cur := getCursorPos()
			local := point{cur.X - menuPos.X, cur.Y - menuPos.Y}
			collapse := menuExpanded && !menuBlockRect(menuModel_).contains(local)
			changed := menuHover != -1 || menuPanelHover != -1 || collapse
			menuHover = -1
			menuPanelHover = -1
			if collapse {
				menuExpanded = false
				updateMenuScreenRect()
			}
			if changed {
				renderMenu()
				menuSurf.present(hwndMenu, menuPos.X, menuPos.Y)
			}
		}

	case wmLButtonDown:
		onLButtonDown(hwnd, lparam)

	case wmLButtonUp:
		onLButtonUp(hwnd, lparam)

	// 悬浮球**没有右键菜单**了：退出/禁用/设置都从左键菜单进（见 onBallClick）。

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

	case wmHoverShowMenu:
		hoverShowMenu()

	case wmHoverHideMenu:
		// 到点了再看一眼鼠标在哪：还在球上或菜单上就留着
		if !menuKeepsOpenAt(getCursorPos()) {
			hideMenu()
		}

	case wmRepaintSettings:
		if settingsOpen {
			renderSettings()
			settingsSurf.present(hwndSettings, settingsPos.X, settingsPos.Y)
		}

	case wmAIRepaint:
		// 流式答案到了一段：合并成一次重画（见 requestAIRepaint）
		clearAIRepaint()
		if sidebarOpen {
			sidebarRepaint()
		}

	case wmOpenSidebar:
		openSidebar()

	case wmCloseSidebar:
		closeSidebar()

	case wmOpenSettings:
		openSettings()

	case wmCloseSettings:
		closeSettings()

	case wmRefreshBall:
		// 「划词弹出」被关掉了：球心标记要转成中性灰，不然看不出状态
		refreshBall()

	case wmCaptureChanged:
		// 鼠标捕获被别的窗口抢走（例如拖到一半弹出 UAC）：把拖动状态复位，
		// 否则球会一直跟着鼠标跑，而且再也回不到"单击出菜单"这条路。
		if hwnd == hwndBall && ballDragging {
			ballDragging = false
			saveBallPos(constrainBall(ballPos))
		}

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
		// 先登记离开通知：分层窗口只有登记过才会收到 WM_MOUSELEAVE
		tme := trackMouseEvent{CbSize: uint32(unsafe.Sizeof(trackMouseEvent{})),
			DwFlags: tmeLeave, HwndTrack: hwndBall}
		pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
		if !ballHovered {
			ballHovered = true
			refreshBall()
			scheduleHoverShow() // 停一会儿就自动把菜单弹出来（豆包那个手感）
		}
		if ballDragging {
			// 阈值之内当作手抖：不算拖动，单击才不会被吃掉
			if !ballMoved {
				dx := pt.X - ballDownAt.X
				dy := pt.Y - ballDownAt.Y
				if dx*dx+dy*dy < ballDragThreshold*ballDragThreshold {
					return
				}
				ballMoved = true
			}
			nx := pt.X - ballGrabOff.X
			ny := pt.Y - ballGrabOff.Y
			moveBallTo(point{nx, ny})
		}
		return
	}

	// 菜单 / 设置页：算出 hover 项，只有变化时才重绘
	tme := trackMouseEvent{CbSize: uint32(unsafe.Sizeof(trackMouseEvent{})),
		DwFlags: tmeLeave, HwndTrack: hwndBall}
	switch hwnd {
	case hwndMenu:
		tme.HwndTrack = hwndMenu
	case hwndSettings:
		tme.HwndTrack = hwndSettings
	case hwndSidebar:
		tme.HwndTrack = hwndSidebar
	}
	pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))

	if hwnd == hwndSidebar {
		local := point{pt.X - sidebarPos.X, pt.Y - sidebarPos.Y}
		if idx := sidebarHit(local); idx != sidebarHover {
			sidebarHover = idx
			sidebarRepaint()
		}
		return
	}
	if hwnd == hwndSettings {
		local := point{pt.X - settingsPos.X, pt.Y - settingsPos.Y}
		if h := settingsHit(local); h != settingsHover {
			settingsHover = h
			renderSettings()
			settingsSurf.present(hwndSettings, settingsPos.X, settingsPos.Y)
		}
		return
	}

	local := point{pt.X - menuPos.X, pt.Y - menuPos.Y}
	idx := rectIndexAt(barItemRects(menuModel_), local)
	panelIdx := -1
	if menuExpanded {
		panelIdx = rectIndexAt(panelItemRects(menuModel_), local)
	}

	expanded := menuExpanded
	switch {
	case idx == moreIndex(menuModel_) && idx >= 0:
		expanded = true // 悬停「…」展开
	case idx >= 0:
		expanded = false // 回到工具条上别的动作就收起
	case panelIdx >= 0:
		expanded = true // 站在面板里，保持展开
	}

	changed := idx != menuHover || panelIdx != menuPanelHover || expanded != menuExpanded
	menuHover, menuPanelHover, menuExpanded = idx, panelIdx, expanded
	if changed {
		updateMenuScreenRect() // 展开/收起改变了「菜单占哪块屏幕」
		renderMenu()
		menuSurf.present(hwndMenu, menuPos.X, menuPos.Y)
	}
}

// rectIndexAt 返回点落在第几个矩形里，都不在则 -1。
func rectIndexAt(rects []rect, pt point) int {
	for i, r := range rects {
		if r.contains(pt) {
			return i
		}
	}
	return -1
}

func onLButtonDown(hwnd uintptr, lparam uintptr) {
	pt := getCursorPos()
	if hwnd == hwndSettings {
		// 设置页没有拖动，按下即生效：点行 = 开关，点右上角 = 关
		settingsPressed = settingsHit(point{pt.X - settingsPos.X, pt.Y - settingsPos.Y})
		return
	}
	if hwnd == hwndSidebar {
		sidebarPressed = sidebarHit(point{pt.X - sidebarPos.X, pt.Y - sidebarPos.Y})
		return
	}
	if hwnd == hwndBall {
		ballDragging = true
		ballMoved = false
		ballDownAt = pt
		ballGrabOff = point{pt.X - ballPos.X, pt.Y - ballPos.Y}
		// 捕获鼠标：不然在窗口外松手就收不到 LButtonUp，ballDragging 会一直卡着
		pSetCapture.Call(hwnd)
		return
	}
	if hwnd == hwndMenu {
		local := point{pt.X - menuPos.X, pt.Y - menuPos.Y}
		menuPressed = rectIndexAt(barItemRects(menuModel_), local)
		menuPanelPressed = -1
		if menuExpanded {
			menuPanelPressed = rectIndexAt(panelItemRects(menuModel_), local)
		}
		// 按在「…」上就直接展开：不必等 hover 的那条路径
		if menuPressed == moreIndex(menuModel_) && menuPressed >= 0 {
			menuExpanded = true
			updateMenuScreenRect()
			renderMenu()
			menuSurf.present(hwndMenu, menuPos.X, menuPos.Y)
		}
	}
}

func onLButtonUp(hwnd uintptr, lparam uintptr) {
	pt := getCursorPos()

	if hwnd == hwndSidebar {
		local := point{pt.X - sidebarPos.X, pt.Y - sidebarPos.Y}
		pressed := sidebarPressed
		sidebarPressed = -1
		if sidebarHit(local) == pressed {
			sidebarClick(local)
		}
		return
	}

	if hwnd == hwndSettings {
		local := point{pt.X - settingsPos.X, pt.Y - settingsPos.Y}
		pressed := settingsPressed
		settingsPressed = -1
		// 按下和抬起要在同一个目标上，跟菜单一致（按下后拖走就不算点击）
		if hit := settingsHit(local); hit == pressed {
			settingsClick(local)
		}
		return
	}

	if hwnd == hwndBall {
		pReleaseCapture.Call()
		ballDragging = false
		if ballMoved {
			saveBallPos(constrainBall(ballPos))
			moveBallTo(constrainBall(ballPos))
			return
		}
		onBallClick(pt)
		return
	}

	if hwnd == hwndMenu {
		local := point{pt.X - menuPos.X, pt.Y - menuPos.Y}
		barPressed, panelPressed := menuPressed, menuPanelPressed
		menuPressed, menuPanelPressed = -1, -1

		var chosen *menuItem
		if r := rectIndexAt(barItemRects(menuModel_), local); r >= 0 && r == barPressed {
			it := barItems(menuModel_)[r]
			if !it.isMore { // 「…」只负责展开，本身不是动作
				chosen = &it
			}
		}
		if chosen == nil && menuExpanded && panelPressed >= 0 && panelPressed < len(menuModel_.more) {
			if r := rectIndexAt(panelItemRects(menuModel_), local); r == panelPressed {
				it := menuModel_.more[r]
				chosen = &it
			}
		}

		// 点的是「…」：只把二级菜单留着（它本来就在悬停时展开了），不收菜单。
		// 以前这里无条件 hideMenu()，于是"点一下更多"反而把整条菜单关掉了。
		if chosen == nil && barPressed >= 0 && barPressed == moreIndex(menuModel_) {
			menuExpanded = true
			updateMenuScreenRect()
			renderMenu()
			menuSurf.present(hwndMenu, menuPos.X, menuPos.Y)
			return
		}

		hideMenu()
		if chosen != nil && chosen.run != nil {
			go chosen.run()
		}
	}
}

// onBallClick 是单击悬浮球（不是拖动）的处理：**弹出菜单**。
//
// 悬浮球没有右键菜单：禁用/启用、自定义设置、退出都在这份左键菜单里，
// 所以右键按下不再有任何反应（豆包那边也是点一下球就展开工具栏）。
//
// 菜单内容分两种：
//
//	有上一次选区 → 划词技能条（对那段文字动手），二级菜单里追加应用项
//	没有选区     → 只出应用项（启用/禁用、设置、退出），免得点开是空的
func onBallClick(pt point) {
	// 左键单击 = 右侧边栏；**悬停**仍然是球旁边的菜单。
	// 两者不冲突：悬停是"顺手看一眼"，单击是"我要在这儿干活"。
	postToMain(wmOpenSidebar)
	_ = pt
}

// ballMenuModel 组装悬浮球菜单。
//
//	有选区 → 划词技能条 + 二级菜单（其余技能 + 禁用 + 自定义设置）+ 退出
//	没选区 / 功能关着 → 只出应用项（启用或禁用、设置、退出），免得点开是空的
//
// 注意**不要**再 append 一遍「禁用/设置」：selectionMenu 的二级菜单里已经有了，
// 重复 append 会让它们在面板里各出现两次（评审抓到过）。
func ballMenuModel() menuModel {
	text := getLastSelection()
	if text == "" || !selectionPopupEnabled() {
		return menuModel{items: appMenuItems()}
	}
	m := selectionMenu(text)
	m.more = append(m.more, exitItem())
	return m
}

// appMenuItems 是没有选区时的菜单：启用/禁用、设置、退出。
func appMenuItems() []menuItem {
	return []menuItem{togglePopupItem(), settingsItem(), exitItem()}
}

// togglePopupItem 按当前状态给出「禁用」或「启用」。
func togglePopupItem() menuItem {
	if selectionPopupEnabled() {
		return menuItem{title: disableTitle, icon: iconDisable, run: func() {
			setSelectionPopup(false)
			savePopupEnabled(false)
			postToMain(wmRefreshBall)
			log.Printf("[action] 已禁用「划词工具栏」：以后划词不再弹菜单，也不会发 Ctrl+C 取词。" +
				"想再开就单击悬浮球 → 启用")
		}}
	}
	return menuItem{title: enableTitle, icon: iconEnable, run: func() {
		setSelectionPopup(true)
		savePopupEnabled(true)
		postToMain(wmRefreshBall)
		log.Printf("[action] 已启用「划词工具栏」")
	}}
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
