//go:build windows

// ui.go —— UI 线程的骨架：共享可变状态 + 窗口类注册与创建。
//
// 状态集中在这里是刻意的 —— 它对应 docs/design-debt.md 第 1 项：
// 线程所有权契约目前只靠命名与注释表达。这个文件就是那项重构的靶子。

package main

import (
	"sync"
	"syscall"
	"unsafe"
)

var (
	scale = 1.0

	hwndBall uintptr
	hwndMenu uintptr
	ballSurf *surface
	menuSurf *surface

	// 以下状态**只允许 UI 线程（跑消息循环的那个）读写**。
	// 其它线程要改菜单内容一律走 pending* + PostMessage。
	ballPos     point
	menuPos     point
	menuModel_  menuModel
	menuHover   int
	menuPressed int

	ballDragging bool
	ballGrabOff  point
	ballMoved    bool

	// 跨线程投递区：采集线程在锁内写，UI 线程在 wmShowMenu 里取走。
	//
	// 早先是直接写 menuModel_/menuText，而 UI 线程渲染时无锁读它们 ——
	// 数据竞争。根因是「跨线程投递」和「UI 线程独占状态」这两件事
	// 共用了一组变量，现在按所有权拆开。
	stateMu     sync.Mutex
	pendingAt   point
	pendingMenu menuModel

	// lastSelection 由采集回调写、UI 线程读，单独一把锁。
	lastSelMu     sync.Mutex
	lastSelection string

	// 菜单卡片的屏幕矩形 + 「菜单在不在」这一个事实，供钩子线程查「点外面了没」。
	//
	// 唯一真值来源：以前 menuVisible 和 menuShown 各表示一次同一件事，
	// show/hide 各写两遍，两份表示可以互相矛盾。现在只留 menuShown。
	//
	// 单独一把锁：钩子线程读它、UI 线程写它。绝不能持锁去碰窗口
	// （showWindow 会走 SendMessage，持锁时调用会拖住钩子线程）。
	menuRectMu     sync.Mutex
	menuScreenRect rect
	menuShown      bool
)

func setLastSelection(text string) {
	lastSelMu.Lock()
	lastSelection = text
	lastSelMu.Unlock()
}

func getLastSelection() string {
	lastSelMu.Lock()
	defer lastSelMu.Unlock()
	return lastSelection
}

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
