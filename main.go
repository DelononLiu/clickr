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
	"time"
	"unsafe"
)

// buildStamp 由 build.sh 通过 -ldflags "-X main.buildStamp=..." 注入。
//
// 存在的意义是防「发错版本」：之前吃过一次亏 —— 打完补丁只 build 到临时文件，
// 却把旧的 NexusKB.exe 部署了出去，用户测的全程是旧版。
// 有了构建戳就能直接从日志/`-version` 确认跑的是哪个版本。
var buildStamp = "dev"

// captureSvc 由 main 装配；测试各自 new 一个实例，互不干扰
var captureSvc *captureService

func main() {
	// 主线程必须锁死：Win32 窗口的消息只会派发到创建它的那个线程，
	// 消息循环一旦跑到别的 OS 线程上，窗口就收不到消息了。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	verbose := flag.Bool("debug", false, "打印取词/动作日志")
	selfTest := flag.Bool("selftest", false, "显示悬浮球与菜单 2.5 秒后退出（自检）")
	dump := flag.String("dump", "", "把渲染结果导出成 <前缀>_ball.nkb / <前缀>_menu.nkb 后退出")
	showVersion := flag.Bool("version", false, "打印构建戳后退出（用来确认部署的到底是哪个版本）")
	probe := flag.Bool("probe", false, "对当前鼠标位置跑一遍**只读**取文手段（UIA / MSAA）并打印结果；不发 Ctrl+C")
	flag.Parse()

	if *showVersion {
		// 发布版（-H=windowsgui）没有 stdout，只 Printf 等于什么都不会出现 ——
		// 而 `-version` 恰恰是确认「跑的是哪个版本」的手段。
		// 所以再写一份到日志文件。
		//
		// 这里**不用** MessageBox：它会阻塞等用户点确定，
		// 在被脚本/管道调用时会把调用方一起挂死（真踩过）。
		msg := "NexusKB build=" + buildStamp
		fmt.Println(msg)
		writeCrashLog(msg)
		return
	}

	setupLogging(*verbose)
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	// ---- 0) 先把 DLL 搜索路径收紧到 System32 ----
	//
	// syscall.NewLazyDLL 只对 kernel32/advapi32/shell32 做 System32 钉死，
	// 其余（user32/gdi32/ole32/oleaut32/**oleacc**/shcore）走标准搜索顺序，
	// 也就是**应用目录优先** —— 只要有人往 NexusKB.exe 同目录放一个 oleacc.dll，
	// 就会被我们加载并调用。
	//
	// 这个 exe 就放在用户可写的目录里，所以这是真实的本地提权面。
	// 必须在加载任何 DLL 之前调用（NewLazyDLL 本身不加载，首次 Call 才加载）。
	if r, _, err := pSetDefaultDllDirectories.Call(loadLibrarySearchSystem32); r == 0 {
		log.Printf("[init] SetDefaultDllDirectories 失败（DLL 搜索路径未能收紧）: %v", err)
	} else {
		log.Printf("[init] DLL 搜索路径已收紧为仅 System32")
	}

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
	dpiProbe := point{getSystemMetrics(0) / 2, getSystemMetrics(1) / 2}
	if hasSaved {
		dpiProbe = saved
	}
	scale = float64(dpiForPoint(dpiProbe)) / 96.0

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

	if *probe {
		runProbe()
		return
	}

	if *dump != "" {
		runDump(*dump)
		return
	}

	if *selfTest {
		runSelfTest()
		return
	}

	// ---- 5) 采集线程 ----
	captureSvc = newCaptureService(captureSelection, func(res captureResult) {
		setLastSelection(res.text)
		log.Printf("[capture] 取到 %d 字（来源=%s，耗时 %v）: %.40q",
			len([]rune(res.text)), res.method, res.elapsed.Round(1e6), res.text)
		// UIA 能给出选区矩形时就用选区末尾当锚点，比鼠标抬起点准。
		// 剪贴板法没有矩形，退回鼠标点。
		anchor := res.anchor
		if res.hasBounds {
			anchor = point{res.bounds.Right, res.bounds.Bottom}
			log.Printf("[capture] 使用选区矩形 %v 作为锚点", res.bounds)
		}
		queueMenu(anchor, selectionMenu(res.text))
	})
	// 必须在装有消费者之后才装钩子：captureCh 是无缓冲的，
	// 采集 worker 若还没阻塞在接收上，第一个请求会被当成「忙」而丢弃。
	// 也就是说这两步的**顺序是有承载作用的**，别调换。
	captureSvc.start()

	// ---- 6) 装全局鼠标钩子 ----
	err := startMouseHook(
		func(anchor point, how string) {
			log.Printf("[hook] 识别到划词动作: %s @(%d,%d)", how, anchor.X, anchor.Y)
			req := captureRequest{anchor: anchor, how: how}
			go func() {
				if _, ok := captureSvc.submit(req, captureTimeoutMS*time.Millisecond); !ok {
					log.Printf("[capture] 采集线程忙或超时，本次请求未完成（%s）", how)
				}
			}()
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

// runProbe 对当前鼠标位置跑一遍**只读**的取文手段并打印结果。
//
// 存在的理由：pipeline 里 UIA 目前排在剪贴板之后（保守，避免回归），
// 但 UIA 才是"更对"的那个 —— 它不发 Ctrl+C、不碰剪贴板、还能给出选区矩形。
// 要不要把它提到第一位，应该由**真实场景下的实测数据**决定，而不是靠推断。
//
// 这里刻意只跑只读的源：剪贴板法会发 Ctrl+C，不适合当诊断工具。
//
// 用法：把鼠标移到（已经在某个程序里选好的）文字上，然后
//
//	NexusKB-debug.exe -probe
func runProbe() {
	pt := getCursorPos()
	c := probeCaptureContext()
	fmt.Printf("位置 (%d,%d)  前台类名=%q  是否控制台=%v\n", pt.X, pt.Y, c.ForegroundClass, c.IsConsole)

	if err := initMSAA(); err != nil {
		fmt.Printf("OleInitialize 失败: %v\n", err)
	}

	// UIA 用**按坐标命中**而不是焦点元素：探测进程一启动就成了前台窗口，
	// 走焦点会探到我们自己（产品路径不会 —— 我们从不激活窗口）。
	if c.IsConsole {
		fmt.Printf("%-6s 不可用（控制台）\n", sourceUIA)
	} else if res, err := uiaSelectionAtHitTest(pt); err != nil {
		fmt.Printf("%-6s 失败: %v\n", sourceUIA, err)
	} else {
		fmt.Printf("%-6s 取到 %d 字  bounds=%v  hasBounds=%v\n", sourceUIA,
			len([]rune(res.text)), res.bounds, res.hasBounds)
		fmt.Printf("       文本: %.160q\n", res.text)
	}

	ms := msaaSource{maxRunes: maxMSAATextRunes}
	if !ms.Available(c) {
		fmt.Printf("%-6s 不可用（控制台）\n", sourceMSAA)
	} else if sel, err := ms.Read(c, pt); err != nil {
		fmt.Printf("%-6s 失败: %v\n", sourceMSAA, err)
	} else {
		fmt.Printf("%-6s 取到 %d 字  bounds=%v  hasBounds=%v\n", sourceMSAA,
			len([]rune(sel.Text)), sel.Bounds, sel.HasBounds)
		fmt.Printf("       文本: %.160q\n", sel.Text)
	}
	fmt.Println("提示：剪贴板法不在探测范围内（它会发 Ctrl+C，有副作用）。")
	fmt.Println("     要对比剪贴板法的结果，直接在程序里划词看菜单/日志即可。")
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
// **默认就写文件**，`-debug` 只是额外加一份到 stderr。
//
// 早先默认是 io.Discard（什么都不写），理由是"正常用户不需要日志"。但结果是：
// 「没取到文本」「剪贴板还原失败、你的剪贴板内容已丢」这些故障对用户
// **完全不可见** —— 而 GUI 用户根本不会带 -debug 重启。
func setupLogging(verbose bool) {
	// 顺序有讲究：文件放前面。
	//
	// io.MultiWriter 遇到第一个报错的 writer 就立刻返回、**后面的不再写**。
	// GUI 子系统被 detached 启动时没有控制台，os.Stderr 是无效句柄、写入必失败；
	// 如果 stderr 排在前面，日志文件就永远拿不到任何内容（实测踩过）。
	writers := []io.Writer{}
	if f := openLogFile(); f != nil {
		writers = append(writers, f)
	}
	if verbose && hasConsole() {
		writers = append(writers, os.Stderr)
	}
	if len(writers) == 0 {
		writers = append(writers, os.Stderr)
	}
	log.SetOutput(io.MultiWriter(writers...))
}

// maxLogBytes 超过就把旧日志轮转一次，避免无上限增长。
const maxLogBytes = 1 << 20

func logFilePath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "NexusKB", "nexuskb.log")
}

func openLogFile() *os.File {
	path := logFilePath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogBytes {
		_ = os.Rename(path, path+".1") // 轮转失败也无所谓，继续追加
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	return f
}

// writeCrashLog 无条件往日志文件追加一行，不依赖当前 log 的输出目标。
func writeCrashLog(line string) {
	f := openLogFile()
	if f == nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", line)
}

// fatal 记录并弹框后退出。GUI 子系统下这是唯一能让用户看到错误的方式。
func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	// 无条件写一份到日志文件：此时日志可能被重定向到任何地方，
	// 而「为什么没起来」恰恰是事后最需要留下的信息。
	if f := openLogFile(); f != nil {
		fmt.Fprintf(f, "[fatal] %s\n", msg)
		f.Close()
	}
	log.Printf("[fatal] %s", msg)
	// owner 传悬浮球窗口：传 0 的话这个框会被我们自己的 topmost 悬浮球盖住
	messageBoxOwned(hwndBall, "NexusKB 启动失败", msg, mbOK|mbIconError|mbTopmost|mbSetForeground)
	os.Exit(1)
}
