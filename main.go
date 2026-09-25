//go:build windows

// NexusKB —— Windows 划词助手最小实现（Go）。
//
// 只做四件事：
//  1. 全局鼠标钩子识别「划词」动作（拖选 / 双击选词）
//  2. 取到用户选中的那段文字
//  3. 在选区旁边弹出菜单（复制 / 搜索 / 翻译）
//  4. 一个可拖动的悬浮球
//
// 线程模型（三块，各自独立）：
//
//	主线程     LockOSThread + OLE 初始化 + 建窗口 + 跑 Win32 消息循环。
//	           所有窗口的创建与渲染都只发生在这里。
//	钩子线程   LockOSThread + SetWindowsHookEx(WH_MOUSE_LL) + 自己的消息循环。
//	          回调只往 channel 里投事件，绝不干重活。
//	采集线程   LockOSThread + OleInitialize，串行处理 Ctrl+C / 剪贴板。
//
// 跨线程一律靠 PostMessage 回主线程，不做跨线程窗口操作。
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"
)

// buildStamp 由 build.sh 通过 -ldflags "-X main.buildStamp=..." 注入。
//
// 存在的意义是防「发错版本」：之前吃过一次亏 —— 打完补丁只 build 到临时文件，
// 却把旧的 NexusKB.exe 部署了出去，用户测的全程是旧版。
// 有了构建戳就能直接从日志/`-version` 确认跑的是哪个版本。
var buildStamp = "dev"

func main() {
	// 主线程必须锁死：Win32 窗口的消息只会派发到创建它的那个线程，
	// 消息循环一旦跑到别的 OS 线程上，窗口就收不到消息了。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	verbose := flag.Bool("debug", false, "打印取词/动作日志")
	selfTest := flag.Bool("selftest", false, "显示悬浮球与菜单 2.5 秒后退出（自检）")
	dump := flag.String("dump", "", "把渲染结果导出成 <前缀>_ball.nkb / <前缀>_menu.nkb 后退出")
	showVersion := flag.Bool("version", false, "打印构建戳后退出（用来确认部署的到底是哪个版本）")
	flag.Parse()

	if *showVersion {
		fmt.Printf("NexusKB build=%s\n", buildStamp)
		return
	}

	setupLogging(*verbose)
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	// ---- 1) DPI 感知：必须在创建任何窗口之前设置 ----
	aware := setDPIAwareness()
	log.Printf("[init] NexusKB build=%s DPI 感知=%s", buildStamp, aware)

	// ---- 3) 建窗口 ----
	if err := createWindows(); err != nil {
		fatal("创建窗口失败: %v", err)
	}

	// ---- 4) 悬浮球就位 ----
	saved, hasSaved := loadBallPos()

	// 顺序很关键：必须先定 scale，再算位置。
	// 窗口尺寸（含投影留白）都是乘在 scale 上的，拿未缩放的尺寸去夹紧位置
	// 会在高 DPI 上把悬浮球挤出屏幕边缘（125% 下实测溢出 5px）。
	probe := point{getSystemMetrics(0) / 2, getSystemMetrics(1) / 2}
	if hasSaved {
		probe = saved
	}
	scale = float64(dpiForPoint(probe)) / 96.0

	initial := defaultBallPos()
	if hasSaved {
		initial = saved
	}
	initial = constrainBall(initial)
	renderBall()
	showBall(initial)
	log.Printf("[init] 悬浮球窗口 %dx%d @(%d,%d) scale=%.2f 工作区=(%d,%d)-(%d,%d)",
		ballWindowSize(), ballWindowSize(), initial.X, initial.Y, scale,
		workArea(initial).Left, workArea(initial).Top,
		workArea(initial).Right, workArea(initial).Bottom)

	if *dump != "" {
		runDump(*dump)
		return
	}

	if *selfTest {
		runSelfTest()
		return
	}

	// ---- 5) 采集线程 ----
	startCaptureWorker(func(res captureResult) {
		setLastSelection(res.text)
		log.Printf("[capture] 取到 %d 字（%s，耗时 %v）: %.40q",
			len([]rune(res.text)), res.method, res.elapsed.Round(1e6), res.text)
		queueMenu(res.anchor, selectionMenu(res.text))
	})

	// ---- 6) 装全局鼠标钩子 ----
	err := startMouseHook(
		func(anchor point, how string) {
			log.Printf("[hook] 识别到划词动作: %s @(%d,%d)", how, anchor.X, anchor.Y)
			select {
			case captureCh <- captureRequest{anchor: anchor, how: how}:
			default:
				log.Printf("[capture] 队列已满，丢弃本次")
			}
		},
		hideMenuIfOutside,
	)
	if err != nil {
		fatal("安装全局鼠标钩子失败: %v\n\n可能是被安全软件拦截。", err)
	}
	log.Printf("[init] 就绪：划词后会自动弹出菜单；右键悬浮球退出")

	// ---- 7) 消息循环 ----
	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // 0 = WM_QUIT，-1 = 出错
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	log.Printf("[exit] 消息循环结束")
}

// runDump 把渲染出来的两张图导出成裸文件，供离线逐像素检查。
//
// 渲染代码大量依赖 GDI（CreateDIBSection / DrawTextW），只能在 Windows 上跑；
// 有了这个导出，就能在别的机器上把图当数据来验，而不是只能「编译通过」。
//
// 文件格式（小端）：
//
//	"NKB1" + int32 w + int32 h + w*h*4 字节 BGRA（预乘 alpha，和 UpdateLayeredWindow 一致）
func runDump(prefix string) {
	renderBall()
	writeDump(prefix+"_ball.nkb", ballSurf)

	// 用真实的三项菜单来导出，包含 hover 态（第 0 项）。
	// 这里不需要锁：runDump 在启动期跑，此时钩子/采集线程都还没起。
	menuModel_ = selectionMenu("这是一段被选中的示例文字，用来验证菜单渲染是否正确。")
	menuHover = 0
	n := len(menuModel_.items)

	w, h := menuWindowSize(n)
	menuSurf = newSurface(w, h)
	renderMenu()
	writeDump(prefix+"_menu.nkb", menuSurf)

	log.Printf("[dump] 已导出 %s_ball.nkb (%dx%d) 与 %s_menu.nkb (%dx%d)，scale=%.2f",
		prefix, ballWindowSize(), ballWindowSize(), prefix, w, h, scale)
}

func writeDump(path string, s *surface) {
	if s == nil {
		log.Printf("[dump] 表面为空: %s", path)
		return
	}
	buf := make([]byte, 0, 16+len(s.bits))
	buf = append(buf, 'N', 'K', 'B', '1')
	buf = appendLE32(buf, uint32(s.w))
	buf = appendLE32(buf, uint32(s.h))
	buf = append(buf, s.bits...)
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		log.Printf("[dump] 写文件失败 %s: %v", path, err)
	}
}

func appendLE32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// runSelfTest 把两个窗口都显示出来，让人肉看一眼渲染对不对。
// 不做任何钩子/剪贴板动作，纯 UI 冒烟。
func runSelfTest() {
	log.Printf("[selftest] 显示悬浮球 + 菜单示例，2.5 秒后退出")

	sample := selectionMenu("这是一段被选中的示例文字，用来验证菜单渲染。")
	// 从悬浮球旁边弹出来，方便一起看
	anchor := point{ballPos.X + ballWindowSize(), ballPos.Y + ballWindowSize()/2}
	showMenu(anchor, sample)

	// 诊断：窗口到底有没有真的显示出来
	for _, w := range []struct {
		name string
		h    uintptr
	}{{"悬浮球", hwndBall}, {"菜单", hwndMenu}} {
		r, ok := windowRect(w.h)
		log.Printf("[selftest] %s hwnd=%#x visible=%v rectOK=%v rect=%v",
			w.name, w.h, isWindowVisible(w.h), ok, r)
	}

	sleepMS(2500)

	hideMenu()
	pDestroyWindow.Call(hwndBall)
	pDestroyWindow.Call(hwndMenu)
	log.Printf("[selftest] 完成")
}

// setupLogging 安排日志去向。
//
// 开发用控制台版能直接在终端看；发布用 -H=windowsgui 没有控制台，
// 所以 -debug 时同时写一份到文件，方便排查「双击没反应」。
func setupLogging(verbose bool) {
	if !verbose {
		log.SetOutput(io.Discard)
		return
	}
	// 顺序有讲究：文件放前面。
	//
	// io.MultiWriter 遇到第一个报错的 writer 就会立刻返回，**后面的不再写**。
	// GUI 子系统（-H=windowsgui）被 detached 启动时没有控制台，
	// os.Stderr 是个无效句柄、写入必失败 —— 如果 stderr 排在前面，
	// 日志文件就永远拿不到任何内容（实测踩过）。
	writers := []io.Writer{}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		dir = filepath.Join(dir, "NexusKB")
		if os.MkdirAll(dir, 0o755) == nil {
			if f, err := os.OpenFile(filepath.Join(dir, "nexuskb.log"),
				os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				writers = append(writers, f)
			}
		}
	}
	if hasConsole() {
		writers = append(writers, os.Stderr)
	}
	if len(writers) == 0 {
		writers = append(writers, os.Stderr)
	}
	log.SetOutput(io.MultiWriter(writers...))
}

// fatal 记录并弹框后退出。GUI 子系统下这是唯一能让用户看到错误的方式。
func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[fatal] %s", msg)
	messageBox("NexusKB 启动失败", msg, mbOK|mbIconError)
	os.Exit(1)
}
