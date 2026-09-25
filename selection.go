//go:build windows

// selection.go —— 取文抽象。
//
// # 为什么需要这一层
//
// 这个项目真正的复杂度全部集中在「怎么拿到用户选中的那段文字」，而手段有三四种，
// 每种各有自己的失败模式。之前这些手段是直接写在一个 `captureSelection` 函数里的
// if 链，于是每加一种就要改一次那个函数 —— 实测已经被改过三次
// （OLE→快照、加控制台防护、加 MSAA）。
//
// 这里把「顺序」从控制流变成数据结构：pipeline 里 sources 切片的顺序就是优先级，
// `Available` 就是「这次环境下该不该用它」。加一种手段 = 新增一个实现 + 往切片里加一行。
//
// # 错误分类是关键
//
// `(text, bool)` 这种返回形状把两件必须区分的事压成了一个值：
// **「用户确实没有选区」** 与 **「读取失败了」**。
// 前者不该弹菜单、也不该报错；后者需要让用户知道出问题了。
// UIA 的第一价值恰恰是能明确回答这个问题，所以类型上必须先把位置留出来。
package main

import (
	"errors"
	"log"
	"strings"
	"time"
)

// ---------------------------------------------------------------- 结果

type sourceID string

const (
	sourceClipboard sourceID = "clipboard"
	sourceMSAA      sourceID = "msaa"
	sourceUIA       sourceID = "uia" // 尚未实现，见 README「下一步」
)

// selection 是一次成功的取文结果。
type selection struct {
	Text   string
	Source sourceID

	// Bounds 是选区（或元素）的屏幕矩形。UIA 能给出真正的选区矩形；
	// 剪贴板法给不出（它对选区在哪一无所知），MSAA 给的是元素的矩形。
	Bounds    rect
	HasBounds bool
}

var (
	// ErrNoSelection = 确实没有选区。这不是故障，不该报错。
	ErrNoSelection = errors.New("没有选区")
	// ErrUnsupported = 该控件不提供文本（比如自绘 UI、真控制台）。
	ErrUnsupported = errors.New("该控件不提供文本")
	// ErrReadFailed = 读取过程中出错了（剪贴板打不开、超时等）。
	ErrReadFailed = errors.New("读取失败")
)

// ---------------------------------------------------------------- 环境

// captureContext 是「这次采集所处的环境」。
//
// 单独抽出来、采集一次后传给所有 source，而不是让每个 source 各自去问
// 「前台窗口是什么」—— 那样既重复又容易出现答案不一致。
type captureContext struct {
	ForegroundClass string
	IsConsole       bool
}

func probeCaptureContext() captureContext {
	class := foregroundWindowClass()
	return captureContext{
		ForegroundClass: class,
		IsConsole:       isConsoleClass(class),
	}
}

// maxMSAATextRunes 是 MSAA 兜底接受的最大字符数。
// 有些可访问对象（比如整个控制台缓冲区）会返回一大坨，对划词助手没意义。
const maxMSAATextRunes = 2000

// ---------------------------------------------------------------- source

type textSource interface {
	Name() sourceID
	// Available 回答「这次环境下该不该用它」。优先级靠切片顺序，可用性靠这里。
	Available(captureContext) bool
	Read(captureContext, point) (selection, error)
}

type capturePipeline struct {
	sources []textSource
}

// Read 按顺序尝试，返回第一个成功的结果。
//
// 顺序即优先级：**剪贴板优先，MSAA 兜底**，不能反过来 ——
// MSAA 没有选区 API，它给的是「鼠标点所在的那个元素/词/行」，
// 放在前面会把浏览器/编辑器里本来很准的「精确选区」退化成「这一行」。
func (p *capturePipeline) Read(at point) (selection, error) {
	return p.ReadIn(probeCaptureContext(), at)
}

// ReadIn 用给定的环境跑一遍。把它和探针分开是为了可测：
// 测试可以注入「前台是控制台」这种环境，不必真的去开一个 cmd 窗口。
func (p *capturePipeline) ReadIn(c captureContext, at point) (selection, error) {
	var sawNoSel, sawUnsupported, sawFailed bool

	for _, s := range p.sources {
		if !s.Available(c) {
			continue
		}
		sel, err := s.Read(c, at)
		if err == nil {
			if strings.TrimSpace(sel.Text) != "" {
				sel.Source = s.Name()
				return sel, nil
			}
			// 拿到了但内容全是空白 —— 等同于没有选区，继续往下试
			sawNoSel = true
			continue
		}
		switch {
		case errors.Is(err, ErrReadFailed):
			sawFailed = true
		case errors.Is(err, ErrUnsupported):
			sawUnsupported = true
		default:
			sawNoSel = true
		}
	}

	// 错误优先级：真实故障 > 明确没有选区 > 该控件不支持。
	//
	// 不能简单地「返回最后一个」：多个 source 给出的答案不一样时，
	// 上层需要的是**最有信息量的那个**。ErrReadFailed 要让用户知道出问题了，
	// 而另外两个都表示「这里本来就没东西可翻」，不该报错。
	switch {
	case sawFailed:
		return selection{}, ErrReadFailed
	case sawNoSel:
		return selection{}, ErrNoSelection
	case sawUnsupported:
		return selection{}, ErrUnsupported
	}

	// 所有 source 都因为 Available=false 被跳过（当前只可能发生在真控制台）
	if c.IsConsole {
		log.Printf("[capture] 前台是控制台窗口（class=%q），所有取文手段都不可用："+
			"不发 Ctrl+C 以免打断你的命令；MSAA 在那返回的是窗口自身信息而非选区。"+
			"读真控制台的选区需要控制台 API，尚未实现", c.ForegroundClass)
	}
	return selection{}, ErrUnsupported
}

// capturePipelineDefault 是生产用的实例。
//
// 顺序就是优先级，改这里等于改策略。
var capturePipelineDefault = &capturePipeline{sources: []textSource{
	clipboardSource{},
	msaaSource{maxRunes: maxMSAATextRunes},
}}

// ---------------------------------------------------------------- 剪贴板 source

type clipboardSource struct{}

func (clipboardSource) Name() sourceID { return sourceClipboard }

// Available：真控制台里**绝不可用**。
//
// 控制台里 Ctrl+C 的语义是「中断」而不是「复制」，我们合成的那次 Ctrl+C
// 会被透传给 shell，把用户正在跑的长任务打断。这个副作用比「取不到词」
// 严重得多，所以那里连试都不试。
func (clipboardSource) Available(c captureContext) bool { return !c.IsConsole }

func (clipboardSource) Read(_ captureContext, at point) (selection, error) {
	// 1) 等源程序把选区落到剪贴板
	sleepMS(selectionSettleMS)

	// 2) 记录当前剪贴板状态
	beforeSeq := clipboardSequence()
	beforeText, beforeOK := readClipboardText()

	// 3) 逐格式快照用户原来的剪贴板（必须在发 Ctrl+C 之前）
	snap := snapshotClipboard()
	defer snap.freeNotOwned()

	// 4) 触发复制
	sendCtrlC()

	// 5) 等剪贴板更新
	text := ""
	deadline := time.Now().Add(clipboardWaitMS * time.Millisecond)
	for time.Now().Before(deadline) {
		sleepMS(15)
		if clipboardSequence() != beforeSeq {
			if t, ok := readClipboardText(); ok && t != "" {
				text = t
				break
			}
		}
	}

	// 6) 序列号没变但内容变了的情况（有些程序复制相同内容不动序列号）
	if text == "" {
		if t, ok := readClipboardText(); ok && t != "" && t != beforeText {
			text = t
		}
	}

	// 7) 还原用户原本的剪贴板
	if len(snap.dropped) > 0 {
		log.Printf("[capture] 警告：有 %d 个剪贴板格式无法快照（非 HGLOBAL 或超限），"+
			"还原后这些格式会丢失: %v", len(snap.dropped), snap.dropped)
	}
	if !snap.restore() {
		log.Printf("[capture] 剪贴板还原失败：用户原来的剪贴板内容已丢失"+
			"（快照 ok=%v，含 %d 个格式）", snap.ok, len(snap.formats))
	}

	switch {
	case text != "":
		return selection{Text: text}, nil
	case !snap.ok && !beforeOK:
		// 连剪贴板都打不开 —— 这是故障，不是「没有选区」
		return selection{}, ErrReadFailed
	default:
		// 序列号没变 = 源程序没执行复制 = 它不认为有选区。
		// 这是剪贴板法白送的一个判断（UIA 能明确回答，这里只能这样推断）。
		return selection{}, ErrNoSelection
	}
}

// ---------------------------------------------------------------- MSAA source

type msaaSource struct {
	// maxRunes 是接受的最大字符数。做成字段而不是读包级常量 ——
	// 之前 msaa.go 反过来读 capture.go 里的常量，等于「提供者知道消费者的预算」，
	// 依赖方向是反的。
	maxRunes int
}

func (msaaSource) Name() sourceID { return sourceMSAA }

// Available：真控制台里不可用。
//
// 实测：MSAA 在控制台上返回的是**控制台窗口自身的名字/整块缓冲区**，
// 不是用户拖选的选区（用户反馈「能弹出但文本不对」）。
// 弹一个内容不对的菜单比不弹更糟，所以那里直接判为不可用。
func (msaaSource) Available(c captureContext) bool { return !c.IsConsole }

func (s msaaSource) Read(_ captureContext, at point) (selection, error) {
	r, ok := msaaTextAt(at)
	if !ok {
		return selection{}, ErrUnsupported
	}
	text := normalizeMSAAText(r.text, s.maxRunes)
	if text == "" {
		return selection{}, ErrNoSelection
	}
	return selection{
		Text:      text,
		Bounds:    r.bounds,
		HasBounds: r.hasBound,
	}, nil
}
