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

	hwndBall     uintptr
	hwndMenu     uintptr
	hwndSettings uintptr
	hwndSidebar  uintptr
	ballSurf     *surface
	menuSurf     *surface
	settingsSurf *surface
	sidebarSurf  *surface

	// 以下状态**只允许 UI 线程（跑消息循环的那个）读写**。
	// 其它线程要改菜单内容一律走 pending* + PostMessage。
	//
	// 菜单的分区：menuHover/menuPressed 管横向工具条，menuPanel* 管「…」点开的
	// 二级面板。menuExpanded 一旦置位就不因为鼠标移开而收起 ——
	// 指针从工具条挪到面板要跨过中间那条 6px 的透明缝（透明区不吃鼠标消息，
	// 一定会产生 WM_MOUSELEAVE），跟着 leave 收起就会疯狂闪烁。
	ballPos          point
	menuPos          point
	menuModel_       menuModel
	menuHover        int
	menuPressed      int
	menuExpanded     bool
	menuPanelHover   int
	menuPanelPressed int

	ballDragging bool
	ballGrabOff  point
	ballMoved    bool
	// 按下时的坐标：单击与拖动靠位移阈值区分。以前任何 1px 移动就算拖动，
	// 手一抖就把单击吃掉了 —— 而球是功能禁用之后唯一的入口。
	ballDownAt      point
	ballHovered     bool // 鼠标停在球上：球会放大一点 + 加一层悬停底色
	settingsOpen    bool // 设置窗口是否开着
	settingsPos     point
	settingsHover   = hitNone // 悬停在第几行（hitCloseButton / hitNone 见 settings.go）
	settingsPressed int
	sidebarPos      point
	sidebarHover    = -1 // 侧边栏悬停：>=0 第几行动作，hitSidebarClose = 关闭按钮，-1 没有
	sidebarPressed  int
	sidebarOpen     bool // 同样在 menuRectMu 内发布（钩子线程读）
	sidebarScroll   int  // 答案区的翻页偏移（行）

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

	// 「划词自动弹菜单」开关：UI 线程写（菜单里点「关闭」/右键悬浮球切换），
	// **钩子线程每次划词都要读** —— 读写分属两个线程，所以单独一把锁。
	//
	// 关掉之后连取词都不做：取词要发 Ctrl+C、要动剪贴板，功能关了还去动用户的
	// 剪贴板说不过去。所以这个判断放在钩子回调里，不在渲染菜单那一步。
	popupMu          sync.Mutex
	selectionPopupOn bool

	// 开启的技能 id：菜单组装时读（可能在采集线程），设置页里写。同样单独一把锁。
	// 缓存住是因为设置页每帧都要问"这一项开着吗"，不能每次都去读磁盘。
	skillsMu     sync.Mutex
	skillsOn     []string
	skillsLoaded bool

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

	// 设置窗口同样要能被"点外面就收起"判到，所以也有自己的屏幕矩形。
	settingsScreenRect rect
	// 侧边栏同理：钩子线程判"这点在不在浮层上"。
	sidebarScreenRect rect
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

// selectionPopupEnabled 判断这次划词要不要处理（在钩子线程调用）。
func selectionPopupEnabled() bool {
	popupMu.Lock()
	defer popupMu.Unlock()
	return selectionPopupOn
}

func setSelectionPopup(on bool) {
	popupMu.Lock()
	selectionPopupOn = on
	popupMu.Unlock()
}

// enabledSkillIDsCached 返回开启的技能 id（首次调用时从磁盘读一次）。
func enabledSkillIDsCached() []string {
	skillsMu.Lock()
	defer skillsMu.Unlock()
	if !skillsLoaded {
		skillsOn = loadSkills()
		skillsLoaded = true
	}
	return append([]string(nil), skillsOn...)
}

// setSkillEnabledIDs 更新缓存并落盘。
func setSkillEnabledIDs(ids []string) {
	skillsMu.Lock()
	skillsOn = append([]string(nil), ids...)
	skillsLoaded = true
	skillsMu.Unlock()
	saveSkills(ids)
}

const (
	classBall     = "ClickrFloatBall"
	classMenu     = "ClickrSelectMenu"
	classSettings = "ClickrSettings"
	classSidebar  = "ClickrSidebar"
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
// createLayeredWindow 建一个"绝不抢焦点"的分层窗口 —— 菜单、悬浮球、侧边栏都用它。
//
// 设置页不走这里：它要能打字，而 WS_EX_NOACTIVATE 的窗口拿不到键盘焦点、
// 也就没有输入法。见 createLayeredWindowActivatable 与 D17。
func createLayeredWindow(class string) (uintptr, error) {
	return createLayeredWindowEx(class, true)
}

// createLayeredWindowActivatable 建一个**可以拿到焦点**的分层窗口。
//
// 只有设置页用它：那里要填 url / key / 模型。抢焦点在这里不是问题 ——
// 用户是主动点开设置页来配置的，不是在划词（D8 要保护的是后者）。
func createLayeredWindowActivatable(class string) (uintptr, error) {
	return createLayeredWindowEx(class, false)
}

func createLayeredWindowEx(class string, noActivate bool) (uintptr, error) {
	ex := uintptr(wsExLayered | wsExTopmost | wsExToolWindow)
	if noActivate {
		ex |= wsExNoActivate
	}
	h, _, err := pCreateWindowExW.Call(
		ex,
		uintptr(unsafe.Pointer(utf16Ptr(class))),
		uintptr(unsafe.Pointer(utf16Ptr("clickr"))),
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
	if err := registerWindowClass(classSettings, idcArrow); err != nil {
		return err
	}
	if hwndSettings, err = createLayeredWindowActivatable(classSettings); err != nil {
		return err
	}
	if err := registerWindowClass(classSidebar, idcArrow); err != nil {
		return err
	}
	if hwndSidebar, err = createLayeredWindow(classSidebar); err != nil {
		return err
	}
	return nil
}
