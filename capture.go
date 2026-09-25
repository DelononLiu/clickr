//go:build windows

// capture.go —— 「取到用户选中的那段文字」。
//
// 这一版走的是最通用、纯 Go 就能实现的路子：模拟 Ctrl+C + 读剪贴板。
//
// 为什么不用 UIA：UIA 的 TextPattern 能直接读出选区，不碰剪贴板、不给源程序
// 发按键、还能拿到选区矩形，是更好的方案。但 Go 生态里没有可用的绑定，
// 得自己手写 3 个 COM 接口的 vtable（见 README 的「下一步」）。
// 两者是同一层抽象（都返回 text + 是否成功），后面可以无痛替换。
//
// # 剪贴板快照为什么不用 OLE
//
// 早先的版本用 OleGetClipboard 存 IDataObject、用完 OleSetClipboard 放回去，
// 实测约 6/7 次还原失败（CLIPBRD_E_CANT_OPEN / CANT_CLOSE），并且**会连累下一次取词**
// —— 还原失败时剪贴板可能被留在打开状态，下一次 OpenClipboard 就失败。
//
// 根因：OleGetClipboard 返回的是**代理对象，只在剪贴板未被修改期间有效**。
// 而我们紧接着就发 Ctrl+C 把剪贴板换掉了，代理当场失效，后面的 OleSetClipboard
// 必然失败。补一次 OleFlushClipboard 也没用 —— 那个函数只对「本进程自己放进
// 剪贴板的数据」有意义，对别人拥有的剪贴板是空操作。
//
// 现在改成自己逐格式快照：EnumClipboardFormats 枚举、HGLOBAL 类格式把字节拷出来、
// CF_BITMAP 用 CopyImage 复制一份；还原时 EmptyClipboard 再逐个写回。
// 全程不依赖任何代理，行为确定。
package main

import (
	"errors"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	// 左键抬起后先等一会儿，让源程序把选区真正落到剪贴板上
	selectionSettleMS = 160
	// 发完 Ctrl+C 后最多等这么久
	clipboardWaitMS = 320
	// 同一段文字在这个时间窗内不重复弹出
	dedupeWindowMS = 900
	// 单次采集的整体超时。超过就放弃这一次，避免采集线程被卡死。
	captureTimeoutMS = 3000
	// 快照剪贴板时单个格式、以及全部格式的体量上限，
	// 避免有人复制了一个巨大的东西把内存吃光。
	maxClipFormatBytes = 32 << 20
	maxClipTotalBytes  = 96 << 20
	// MSAA 兜底取到的文本上限。有些可访问对象（比如整个控制台缓冲区）
	// 会返回一大坨，对划词助手没意义，截断掉。
	maxMSAATextRunes = 2000
)

type captureRequest struct {
	anchor point
	how    string
}

var captureCh = make(chan captureRequest, 4)

// captureIdle 保证同一时刻只有一次采集在操作剪贴板。
// 超时被放弃的那次仍可能在后台跑完，所以用「不并发」而不是「加锁」来防交叉。
var captureIdle atomic.Bool

// ---------------------------------------------------------------- 结果

type captureResult struct {
	text    string
	anchor  point
	how     string
	ok      bool
	method  string
	elapsed time.Duration
}

var (
	resultMu     sync.Mutex
	lastText     string
	lastTextTime time.Time
)

// startCaptureWorker 起一个常驻的采集线程，串行处理请求。
//
// 串行是刻意的：剪贴板是全局独占资源，并发采集只会互相抢锁。
func startCaptureWorker(cb func(captureResult)) {
	go func() {
		for req := range captureCh {
			res, done := captureWithTimeout(req)
			if !done {
				// 注意这里不能报成「未取到文本」：那是「确实没有选区」，
				// 而这是「读取失败」，两者对上层和用户的含义完全不同。
				log.Printf("[capture] 本次采集整体超时或被跳过（>%dms 未收尾）。"+
					"常见原因是某个程序声明了延迟渲染却一直不交出数据", captureTimeoutMS)
				continue
			}
			if !res.ok {
				log.Printf("[capture] 未取到文本（%s）", req.how)
				continue
			}
			if isDuplicate(res.text) {
				log.Printf("[capture] 与上次相同，忽略（%s）", req.how)
				continue
			}
			cb(res)
		}
	}()
}

// captureWithTimeout 给单次采集套一个总超时。
//
// 剪贴板的读操作有可能**永久阻塞**：如果数据是延迟渲染的，而持有者迟迟不调用
// SetClipboardData，GetClipboardData 就会一直等下去。没有这层保护，
// 采集线程会被卡死，之后所有划词都毫无反应 —— 用户看到的就是「程序卡住了」。
// 超时后那次采集的 goroutine 会泄漏（Win32 没有干净的中断手段），
// 但主流程还能继续服务，比整体卡死好得多。
func captureWithTimeout(req captureRequest) (captureResult, bool) {
	// 上一次采集如果超时被放弃，它的 goroutine 并没有停下来 ——
	// Win32 没有干净的中断手段，它可能正卡在 GetClipboardData 里，
	// 也可能刚醒过来继续走「还原剪贴板」。
	// 这时候再起一次采集，两次就会并发操作同一个全局独占的剪贴板，
	// 互相把对方快照的内容写回去，甚至覆盖用户在这个窗口期内新复制的东西。
	//
	// 所以超时之后不再并发新采集，直接跳过本次请求。
	if !captureIdle.CompareAndSwap(true, false) {
		log.Printf("[capture] 上一次采集尚未收尾（可能已超时被放弃），跳过本次请求")
		return captureResult{}, false
	}
	defer captureIdle.Store(true)

	ch := make(chan captureResult, 1)
	go func() {
		// COM/MSAA 是按线程初始化的，所以把这次采集锁死在一个线程上，
		// 并在这里做 OleInitialize。重复初始化是幂等的（返回 S_FALSE）。
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := initMSAA(); err != nil {
			log.Printf("[capture] OleInitialize 失败，MSAA 兜底不可用: %v", err)
		}
		ch <- captureSelection(req)
	}()
	select {
	case r := <-ch:
		return r, true
	case <-time.After(captureTimeoutMS * time.Millisecond):
		return captureResult{}, false
	}
}

func isDuplicate(text string) bool {
	resultMu.Lock()
	defer resultMu.Unlock()
	now := time.Now()
	if text == lastText && now.Sub(lastTextTime) < dedupeWindowMS*time.Millisecond {
		return true
	}
	lastText, lastTextTime = text, now
	return false
}

// ---------------------------------------------------------------- 主流程

func captureSelection(req captureRequest) captureResult {
	start := time.Now()
	res := captureResult{anchor: req.anchor, how: req.how}

	// 0) 前台是真正的控制台窗口时，**只走 MSAA，绝不发 Ctrl+C**。
	//
	// 控制台里 Ctrl+C 的语义是「中断」而不是「复制」，我们合成的那次 Ctrl+C
	// 会被透传给 shell —— 用户正在跑的长任务会被我们打断。
	// 这个副作用比「取不到词」严重得多，所以这里连试都不试剪贴板。
	// 但 MSAA 是只读的、无副作用，试一下没坏处：控制台本身给不出什么，
	// 而 VS Code 那种 Electron 终端有时能给出整行文字。
	if foregroundIsConsole() {
		res.method = "skipped-console"
		// 这里刻意什么都不做。
		//
		// 试过用 MSAA 兜底，但实测**拿到的是错的文本**：可访问对象给出的是
		// 控制台窗口自身的名字/整块缓冲区，而不是用户拖选的那一段。
		// 弹出一个内容不对的菜单比不弹更糟，所以宁可什么都不弹。
		//
		// 也刻意不发 Ctrl+C：控制台里 Ctrl+C 是「中断」而非「复制」，
		// 会被透传给 shell 把用户正在跑的命令打断。
		//
		// 真控制台要拿到选区，正路是控制台自己的 API：
		//   AttachConsole(pid) → GetConsoleSelectionInfo() → ReadConsoleOutputCharacterW()
		// 这条还没实现（见 README 的「下一步」）。
		log.Printf("[capture] 前台是控制台窗口，已跳过（不发 Ctrl+C 以免打断你的命令；" +
			"读真控制台的选区需要控制台 API，尚未实现）")
		return res
	}

	// 1) 等源程序把选区落到剪贴板
	sleepMS(selectionSettleMS)

	// 2) 记录当前剪贴板状态
	beforeSeq := clipboardSequence()
	beforeText, _ := readClipboardText()

	// 3) 逐格式快照用户原来的剪贴板
	snap := snapshotClipboard()
	defer snap.freeNotOwned()

	// 4) 触发复制
	sendCtrlC()

	// 5) 等剪贴板更新
	deadline := time.Now().Add(clipboardWaitMS * time.Millisecond)
	for time.Now().Before(deadline) {
		sleepMS(15)
		if clipboardSequence() != beforeSeq {
			if text, ok := readClipboardText(); ok && text != "" {
				res.text, res.ok, res.method = text, true, "clipboard-seq"
				break
			}
		}
	}

	// 6) 序列号没变但内容变了的情况（有些程序复制相同内容不动序列号）
	if !res.ok {
		if text, ok := readClipboardText(); ok && text != "" && text != beforeText {
			res.text, res.ok, res.method = text, true, "clipboard-diff"
		}
	}

	// 7) 还原用户原本的剪贴板
	if !snap.restore() {
		log.Printf("[capture] 剪贴板还原失败：用户原来的剪贴板内容已丢失（快照含 %d 个格式）",
			len(snap.formats))
	}

	// 8) 剪贴板法失败时用 MSAA 兜底。
	//
	// 顺序是刻意的：**剪贴板优先**，因为只有它能拿到用户真正拖选的那一段；
	// MSAA 拿到的只是「鼠标点所在的那个元素/词/行」，精度更低。
	// 所以 MSAA 只在剪贴板拿不到东西时补位（典型场景：那个程序里
	// Ctrl+C 不是复制，比如 VS Code 的集成终端）。
	if !res.ok {
		if r, ok := msaaTextAt(req.anchor); ok {
			res.text, res.ok, res.method = r.text, true, "msaa-fallback"
			log.Printf("[capture] 剪贴板法未取到，改用 MSAA 兜底: %d 字", len([]rune(r.text)))
		}
	}

	res.elapsed = time.Since(start)
	return res
}

// ---------------------------------------------------------------- 剪贴板读写

func clipboardSequence() uint32 {
	v, _, _ := pGetClipboardSequenceNumber.Call()
	return uint32(v)
}

// openClipboardWithRetry 抢剪贴板，抢不到就反复等一小会儿。
// 剪贴板是全局独占资源，随时可能有别的进程正开着它。
func openClipboardWithRetry(attempts int, wait time.Duration) bool {
	for i := 0; i < attempts; i++ {
		if r, _, _ := pOpenClipboard.Call(0); r != 0 {
			return true
		}
		time.Sleep(wait)
	}
	return false
}

func readClipboardText() (string, bool) {
	if r, _, _ := pIsClipboardFormatAvailable.Call(cfUnicodeText); r == 0 {
		return "", false
	}
	if !openClipboardWithRetry(12, 15*time.Millisecond) {
		return "", false
	}
	defer pCloseClipboard.Call()

	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer pGlobalUnlock.Call(h)

	sz, _, _ := pGlobalSize.Call(h)
	if sz < 2 {
		return "", false
	}
	u16 := unsafe.Slice((*uint16)(unsafe.Pointer(p)), int(sz)/2)
	return syscall.UTF16ToString(u16), true
}

// setClipboardText 给「复制」动作写剪贴板。
func setClipboardText(text string) error {
	if text == "" {
		return errors.New("空文本")
	}
	utf16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	if !openClipboardWithRetry(12, 15*time.Millisecond) {
		return errors.New("OpenClipboard 失败")
	}
	defer pCloseClipboard.Call()

	if r, _, _ := pEmptyClipboard.Call(); r == 0 {
		return errors.New("EmptyClipboard 失败")
	}
	h, err := allocGlobalBytes(unsafe.Slice((*byte)(unsafe.Pointer(&utf16[0])), len(utf16)*2))
	if err != nil {
		return err
	}
	if r, _, _ := pSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		pGlobalFree.Call(h)
		return errors.New("SetClipboardData 失败")
	}
	// 成功之后内存归系统所有，不能再 free
	return nil
}

// ---------------------------------------------------------------- 剪贴板快照

// clipFormat 是一个剪贴板格式的副本。
// HGLOBAL 类格式的数据在 data 里；CF_BITMAP 这类 GDI 对象在 hObject 里。
type clipFormat struct {
	format  uint32
	data    []byte
	hObject uintptr
}

type clipSnapshot struct {
	formats []clipFormat
	bytes   int
}

// isGlobalHandleFormat 判断某个剪贴板格式的数据是不是 HGLOBAL。
//
// 剪贴板格式里只有少数几种传的是 GDI 对象或进程私有句柄，
// 其余（含所有注册格式 0xC000 以上）一律是 HGLOBAL。
func isGlobalHandleFormat(f uint32) bool {
	if f >= cfRegisteredFirst {
		return true
	}
	switch f {
	// CF_METAFILEPICT / CF_DSPMETAFILEPICT 传的是「装着 METAFILEPICT 结构的
	// 全局内存句柄」，是 HGLOBAL，能直接按字节拷；别和 HENHMETAFILE 搞混。
	case cfBitmap, cfPalette, cfEnhMetafile,
		cfOwnerDisplay, cfDspBitmap, cfDspEnhMetafile:
		return false
	}
	// 私有格式区 / GDI 对象区：传的都是句柄
	if f >= 0x0200 && f <= 0x03FF {
		return false
	}
	return true
}

// snapshotClipboard 把当前剪贴板整体拷一份出来，必须在发 Ctrl+C 之前调用。
func snapshotClipboard() clipSnapshot {
	var snap clipSnapshot
	if !openClipboardWithRetry(12, 15*time.Millisecond) {
		return snap
	}
	defer pCloseClipboard.Call()

	for f := uint32(0); ; {
		next, _, _ := pEnumClipboardFormats.Call(uintptr(f))
		f = uint32(next)
		if f == 0 {
			break
		}
		h, _, _ := pGetClipboardData.Call(uintptr(f))
		if h == 0 {
			continue
		}

		// CF_BITMAP 传的是 HBITMAP 而不是 HGLOBAL，得单独复制一份
		if f == cfBitmap {
			cp, _, _ := pCopyImage.Call(h, imageBitmap, 0, 0, 0)
			if cp != 0 {
				snap.formats = append(snap.formats, clipFormat{format: f, hObject: cp})
			}
			continue
		}

		if !isGlobalHandleFormat(f) {
			continue
		}
		sz, _, _ := pGlobalSize.Call(h)
		if sz == 0 || sz > maxClipFormatBytes || snap.bytes+int(sz) > maxClipTotalBytes {
			continue
		}
		p, _, _ := pGlobalLock.Call(h)
		if p == 0 {
			continue
		}
		buf := make([]byte, sz)
		copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(p)), int(sz)))
		pGlobalUnlock.Call(h)

		snap.formats = append(snap.formats, clipFormat{format: f, data: buf})
		snap.bytes += int(sz)
	}
	return snap
}

// restore 把快照写回剪贴板。
//
// 失败会重试几次：刚被我们 Ctrl+C 触发的那个程序可能还短暂占着剪贴板
// （延迟渲染是异步的），这时 OpenClipboard / EmptyClipboard 会失败。
func (s clipSnapshot) restore() bool {
	if len(s.formats) == 0 {
		// 原本剪贴板就是空的，没什么要还原的
		return true
	}
	const attempts = 6
	for i := 0; i < attempts; i++ {
		if s.restoreOnce() {
			return true
		}
		time.Sleep(time.Duration(30*(i+1)) * time.Millisecond)
	}
	return false
}

func (s clipSnapshot) restoreOnce() bool {
	if !openClipboardWithRetry(8, 15*time.Millisecond) {
		return false
	}
	defer pCloseClipboard.Call()

	if r, _, _ := pEmptyClipboard.Call(); r == 0 {
		return false
	}
	// 任何一个格式写失败都要如实返回 false，让上层重试并最终报错 ——
	// 否则「部分格式丢失」会被静默当成还原成功。
	allOK := true
	for i := range s.formats {
		c := &s.formats[i]
		if c.hObject != 0 {
			// CopyImage 出来的副本本来就是我们的，直接交给系统
			if r, _, _ := pSetClipboardData.Call(uintptr(c.format), c.hObject); r != 0 {
				c.hObject = 0
				continue
			}
			allOK = false
			continue
		}
		h, err := allocGlobalBytes(c.data)
		if err != nil {
			allOK = false
			continue
		}
		if r, _, _ := pSetClipboardData.Call(uintptr(c.format), h); r == 0 {
			pGlobalFree.Call(h) // 只有失败才需要自己释放
			allOK = false
		}
	}
	return allOK
}

// freeNotOwned 释放还原流程没有交出去、仍攥在手里的 GDI 对象副本，避免泄漏。
func (s clipSnapshot) freeNotOwned() {
	for i := range s.formats {
		if s.formats[i].hObject != 0 {
			pDeleteObject.Call(s.formats[i].hObject)
			s.formats[i].hObject = 0
		}
	}
}

// allocGlobalBytes 分配一块 GMEM_MOVEABLE 内存并拷入数据。
func allocGlobalBytes(b []byte) (uintptr, error) {
	if len(b) == 0 {
		return 0, errors.New("空数据")
	}
	h, _, _ := pGlobalAlloc.Call(gmemMoveable, uintptr(len(b)))
	if h == 0 {
		return 0, errors.New("GlobalAlloc 失败")
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		pGlobalFree.Call(h)
		return 0, errors.New("GlobalLock 失败")
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), len(b)), b)
	pGlobalUnlock.Call(h)
	return h, nil
}
