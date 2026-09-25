//go:build windows

// state.go —— 共享可变状态。
//
// 这里是 debt 清单第 1 项的靶子：线程所有权契约目前只靠命名与注释表达。
// 拆成文件不解决那个问题（要用 owner 结构体），但至少让它显式、集中、好找。

package main

import "sync"

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
