# 设计债清单

三轮独立评审（架构 / 设计原则 / 实现原则）的结论汇总，按 **影响 × 修复成本** 排序。
每条都带代码证据；不写"建议优化"这种无法验证的话。

> 排序原则：先堵**会复发的机制**，再修**这一次的实例**。三轮评审独立指向同一个结论——
> 这个项目最大的问题不是某处写错了，而是**线程所有权契约只活在散文注释里**。

---

## 🔴 第 1 项：UI goroutine 独占窗口 / GDI / 布局状态

**影响：最高。成本：中等偏高（改动机械但量大）。必须走在 UIA 前面。**

### 问题

「窗口只能被创建它的线程操作」是 Win32 硬约束，但代码里**没有任何类型、签名或 API 形状表达它**——只有注释和 `post*`/`queue*` 命名前缀。编译器、reviewer、新代码都看不见。

约 30 个包级可变变量没有归属者，只有"谁先调谁赢"的约定。

### 证据（跨线程路径逐条）

| 路径 | 判定 |
|---|---|
| 钩子回调 → `hookCh`（缓冲 128、非阻塞） | ✅ 安全，全项目最正确的一处 |
| 消费 goroutine → `hideMenuIfOutside` | ✅ 安全（读受锁 + PostMessage） |
| 采集线程 → `queueMenu` → `pending*` + PostMessage | ✅ 安全 |
| 采集回调 → `lastSelection`（`lastSelMu`） | ✅ 安全（**曾是竞争**） |
| **菜单项 `go chosen.run()`** | ⚠️ **靠运气**：三个 action 恰好只调 `postToMain`/`setClipboardText`/`shellOpen`。将来任何一个碰窗口就复现坑 #6 |
| **`recreateSurfaces`** | ⚠️ 只能 UI 线程调，**无任何表达**；它 free / 重建 GDI surface |
| **`runDump` 用 `stateMu` 写 `menuModel_`** | ⚠️ 而渲染端已经不锁了 → **那把锁现在不保护任何东西，却让读者以为受保护** ← 我修竞争时引入的不一致 |
| **`postToMain` 不看返回值** | ⚠️ 固定投 `hwndBall`，而 `wmQuitApp` 先销毁球窗口 → 之后的投递**静默丢失** |

### 为什么这是第 1 项

历史上 3 个 bug 全部源于这条缺失的不变量：

| 症状 | 根因 |
|---|---|
| 菜单偶尔不消失 | 钩子线程跨线程调 `ShowWindow` |
| 测试永久挂起 | 跨线程 `SetWindowPos` → `SendMessage` 无人泵消息 |
| 菜单内容偶发错乱 | 一个线程写、另一个线程渲染时无锁读 |

**更关键的是：UIA 会引入两条新的跨线程路径**（异步的选区矩形、可能失败的调用）。在契约还只活在注释里的时候接 UIA，等于往一个已知会漏的地方再加两处接口。

### 改法：让违反变得不可能

```go
type uiThread struct {
    cmds   chan uiCmd              // 外部只写，UI 线程只读
    wake   uintptr                 // message-only 窗口，只当门铃
    hwndBall, hwndMenu uintptr
    ballSurf, menuSurf *surface    // 只在本 goroutine 内 new/free/present
    scale  float64
    ball   ballState
    menu   menuState               // model,text,pos,hover,pressed,visible
}

type uiCmd interface{ isUICmd() }  // sealed
type cmdShowMenu struct{ anchor point; model menuModel }
type cmdHideMenu struct{}
type cmdHideIfOutside struct{ pt point }   // 判定移回 UI 线程
```

**实现细节（不做会死锁）**：消息循环阻塞在 `GetMessageW`，**不能** `select` channel。做法：外部先入 channel 再 `PostMessage(u.wake, wmWake, 0, 0)`，在 `wmWake` 处理里 drain。

**收益**：`menuRectMu` / `menuScreenRect` / `menuShown` 三个全局**直接消失**——它们存在的唯一理由就是让外线程读 UI 状态。

---

## ✅ 已完成：`TextSource` 抽象 + 错误分类（原第 2 项）

**结论：已落地（`selection.go`）。UIA 现在是「新增一个文件 + sources 加一行」。**

实现要点：
- `selection{Text, Source, Bounds, HasBounds}` —— 把 UIA 的主要收益（**选区矩形**）在类型上留出位置；
  剪贴板法给不出矩形，MSAA 给的是元素矩形，UIA 能给真正的选区矩形
- `ErrNoSelection` / `ErrUnsupported` / `ErrReadFailed` 三者分开 —— 「用户确实没选东西」
  与「读取失败」对上层和用户含义完全不同，以前被压成一个 `bool`
- `captureContext{ForegroundClass, IsConsole}` 由 pipeline 探一次传给所有 source，
  不再让每个 source 各自去问「前台窗口是什么」
- **「顺序是刻意的」从控制流变成了数据结构**：`sources` 切片顺序即优先级；
  真控制台里剪贴板源 `Available()==false`（不发 Ctrl+C 防打断），
  MSAA 源也不可用（实测返回窗口自身信息）
- 多 source 错误优先级：真实故障 > 明确没选区 > 控件不支持
- `maxMSAATextRunes` 从构造参数传入，`msaa.go` 读 `capture.go` 常量那个反向依赖消失了

**可测性**：`fakeSource` + `ReadIn(注入的 context)` 让策略本身可断言，
不必再拿开发者真实剪贴板当 fixture。新增 7 个测试覆盖优先级、跳过、
控制台拒发 Ctrl+C、错误区分、空白文本、矩形透传、默认顺序。

<details><summary>原始评审结论（保留）</summary>

**影响：高。成本：低。是第 1 项之后、UIA 之前的前置。**

### 问题

`capture.go` 顶部注释声称"两者是同一层抽象（都返回 text + 是否成功），后面可以无痛替换"。

**这个抽象不存在。** 实证：

- 来源身份是**字符串字面量**：`"clipboard-seq"` / `"clipboard-diff"` / `"msaa-fallback"` / `"skipped-console"`（只进日志，无类型检查）
- `msaaResult` 带 `bounds` / `hasBound`，而 `captureResult` **没有矩形字段** → MSAA 拿到的 `accLocation` 在 `capture.go` 里**被直接丢弃**
- `anchor` 只是入参鼠标点的原样回显

**即：UIA 最大的收益（选区矩形）在现结果类型里根本无法表达。**

### 证据：`captureSelection` 已被改过三次

OLE → 快照、加控制台防护、加 MSAA。每次都是**修改式扩展**。加 UIA 会是第四次，紧接着第五次（控制台早退写成函数体内提前 `return`，UIA 在 Windows Terminal 里永远得不到机会，要放开又得改一次）。

### 正确形状

```go
type selection struct {
    Text      string
    Anchor    point
    Bounds    rect    // UIA 可得；剪贴板法留空
    HasBounds bool
}

// 关键：把「确实没有选区」和「读取失败」分开。
// 剪贴板法只能靠"序列号没变"猜，而 UIA 能明确回答。
var (
    ErrNoSelection = errors.New("没有选区")
    ErrUnsupported = errors.New("控件不支持取词")
)

type captureContext struct {
    ForegroundClass     string
    AllowSyntheticInput bool   // 控制台里为 false：不许发 Ctrl+C
}

type selectionReader interface {
    Name() string
    Available(captureContext) bool
    Read(ctx context.Context, at point) (selection, error)
}
```

</details>

**收益**：

- UIA 从"改 5 个文件"降为"**新增一个文件 + sources 加一行**"
- `Bounds` 自动可用 → 弹窗贴住选区末尾，而不是猜鼠标点
- 控制台从"函数中段的特例"变成"`Available()==false` 的 reader"，「不发 Ctrl+C 防打断」的副作用约束与兜底顺序**解耦**
- 优先级从 if 链变成**切片顺序**（是数据，不是控制流）
- 可以用 `fakeSource` 单测，**不必再拿开发者真实剪贴板当 fixture**
- `msaa.go` 读 `capture.go` 的 `maxMSAATextRunes` 这个反向依赖 → 变成构造参数，消失

---

## 🟠 第 3 项：用户可见的失败通道

**影响：高（这是"影响"最大的单点）。成本：低。**

### 问题

采集失败的唯一信号在**默认关闭的日志**里（`setupLogging(false)` 直接 `log.SetOutput(io.Discard)`）。

于是对用户来说：

- 「没取到文本」= 什么都没发生
- 「剪贴板还原失败，你原来的剪贴板内容已丢」= **同样是什么都没发生**

程序自己有菜单窗口、有 `messageBox`，却完全没有反馈通道。

### 附带问题

- **6 种错误处理风格并存**：`log.Printf` 后继续 / `fatal()` 弹框 / `(T, bool)` / `error` / `panic`（`CreateDIBSection` 失败）/ 几乎每个 `p*.Call` 都 `_` 掉 `GetLastError`
- 同一类失败有两种用户可见性（panic 无声退出 vs fatal 弹框），**取决于从哪一行抛出**
- `captureWithTimeout` 的 `bool` 表示"没超时"——**第三种语义**混进了同一种返回形状

### 改法

```go
type reporter interface {
    Info(msg string, kv ...any)
    Fail(err error, kv ...any)   // 必达用户：菜单内提示条 / 悬浮球红点 / 系统通知
}
```

剪贴板还原失败 = 数据丢失，属 `Fail` 级。

---

## 🟡 廉价必修（按性价比）

| # | 问题 | 改法 |
|---|---|---|
| 1 | `runDump` 用 `stateMu` 写 `menuModel_`，渲染端不锁 → **锁不保护任何东西却让人误以为受保护** | 删掉 `runDump` 里的锁（它是启动期单线程调用） |
| 2 | `menuText` 只写不读（死载荷），`showMenu`/`queueMenu` 的 text 参数是死的 | 删除 |
| 3 | `menuVisible` 与 `menuShown` 两处表示同一事实，show/hide 各写两次 | 合并成一个 |
| 4 | `systemUsesDarkMode()` 出错即 `return false` → **D9 那种静默失败类型上不可观测** | 返回 `(bool, error)`，测试断言 `err == nil` |
| 5 | `TestForegroundConsoleDetectionDoesNotCrash` 断言体是 `_ = f()` —— **什么都没断言** | 改成 `isConsoleClass(class string) bool` + 表驱动 |
| 6 | `TestMSAAReadsTextFromRealControl` 打不中就 `Skipf` → **"vtable 抄错"与"环境不配合"不可区分，能静默通过** | Skip 前先断言 vtable 槽位（已有 `TestMSAAVtableSlotsLookSane`），或把 skip 条件收紧到"拿不到任何可访问对象" |
| 7 | `isDuplicate` 是纯函数却只能真等 900ms | 时间源入参化 `now func() time.Time` |
| 8 | `accString(slot int, …)` 槽位号无法类型检查（能把 `accGetAccRole` 传给取名字的方法） | 换成具名类型 `type accStringSlot int` |
| 9 | `surface.present` / `setWindowPos` 丢弃 Win32 返回值 → free 后再 present 静默出错 | 已修 `present`；`setWindowPos` 待办 |
| 10 | 菜单快捷键是**死承诺**（`WS_EX_NOACTIVATE` 拿不到键盘焦点） | 要么上键盘钩子接线，要么不显示 |
| 11 | `TestGlobalHandleFormatClassification` 曾固化**错误的规格**（把 `CF_METAFILEPICT` 当非 HGLOBAL） | 已修代码与测试；教训：测试固化的是规格，规格错则测试是负资产 |

---

## ⛔ 明确不值得做（负向清单）

评审明确点了名，记录下来避免以后有人再提：

| 提案 | 为什么不做 |
|---|---|
| 把 `render.go` 的距离场拆成 `internal/gdi` | 单消费者、零复用 |
| `win32.go` 的常量按模块分散 | 价值恰恰在**集中对照 SDK** |
| 做 source 注册 / 泛化插件机制 | 3 个源、一个字面量 slice 更好读 |
| `posFile`/`saveBallPos` 独立成包 | 40 行、一个调用者，只需让路径可注入 |
| 替换 `render.go` 里手写的 `itoa` | 为零依赖有意为之 |
| 引入 `golang.org/x/sys/windows` | 见 D1 |
| 上 Tauri / WebView2 做 UI | 见 D2：**拿不到窗口样式控制权，与"不抢焦点"冲突** |

---

## ✅ 已修的（记录，避免重复踩）

| 问题 | 修法 |
|---|---|
| 剪贴板还原 6/7 失败 | 弃 OLE 代理，改逐格式快照（D4） |
| 阴影被窗口边界硬切 | 留白从 `shadowReach(blur,dy)` 反推（D10） |
| 悬浮球被挤出屏幕 | 先定 `scale` 再算位置；加 4 档缩放回归测试 |
| 窗口从未显示 | `present()` 补 `SetWindowPos(SWP_SHOWWINDOW)`；加 `IsWindowVisible` 断言 |
| 部署了旧版本 | 构建戳 + `-version` 自证（D9） |
| 跨线程 `ShowWindow` | 改 `PostMessage` |
| `menuModel_` / `lastSelection` 数据竞争 | 信箱 `pending*` 与 UI 独占状态分离 + `lastSelMu` |
| 超时放弃的采集线程与下一次并发操作剪贴板 | `captureIdle` 禁止并发采集 |
| `HKEY_CURRENT_USER` 常量在 x64 被截断 → 暗色主题静默失效 | `0xFFFFFFFF80000001` |
| MSAA 在控制台返回窗口自身信息（用户实测"文本不对"） | 控制台里什么都不做（D7） |
| `setDPIAwareness` 用 `== 0` 判 HRESULT（S_FALSE 也算成功） | 改 `int32(r) >= 0` |
| 还原时部分格式失败却报成功 | `restoreOnce` 如实返回 `false` |
| `msaaReady` 只写不读 | 删除（线程局部状态不该用包级变量表达） |
| **采集被自己永久关停**：用 `atomic.Bool` 表达「忙不忙」，零值是 false 而判据是 `CompareAndSwap(true,false)` → 首次采集就被跳过、此后永远跳过 | 改用**常驻采集实例**；「空闲吗」由无缓冲 channel 自身的语义回答，没有初值陷阱。`TestCaptureServiceHandlesFirstRequest` 守着 |
| 还原剪贴板每轮重试都 `EmptyClipboard` → 抹掉上一轮已还原的格式且不重写，却返回成功 | 只在首轮清空；每格式记 `done`；失败如实返回 |
| 快照为空 = 成功（分不清「本来就没东西」和「快照失败」）；主动丢弃的格式不上报 | 加 `ok` 与 `dropped`，调用方如实记日志 |
| `sendCtrlC` 无条件补发 Ctrl「抬起」→ **会把用户正按着的 Ctrl 松开** | 只抬起我们自己按下的 |
| `accInt` 失败分支提前 return → 漏 `VariantClear`，泄漏 BSTR/IDispatch | `defer out.clear()` 提到 hr 判断之前 |
| 拖动/双击阈值写死 5px / 4px（物理像素）→ 200% 缩放下等效 2.5 逻辑像素，手抖即误判 | 改取系统度量 `SM_CXDRAG`/`SM_CXDOUBLECLK`（系统已按 DPI 缩放） |
| `syscall.NewLazyDLL` 对 user32/gdi32/ole32/oleaut32/**oleacc** 走标准搜索顺序 = **应用目录优先** → 同目录放个 `oleacc.dll` 就会被加载 | 启动时 `SetDefaultDllDirectories(LOAD_LIBRARY_SEARCH_SYSTEM32)` |
| `CreateDIBSection` 失败直接 panic，而它在 WndProc 调用链上 → panic 无法抛回 C，**进程无声退出** | panic 前记日志；WndProc 加 `recover` |
| 日志默认 `io.Discard` → 采集失败/剪贴板丢失对用户完全不可见 | 默认写文件（>1MB 轮转），`-debug` 只额外加 stderr |
| `fatal` 的原因进不了日志文件；`-version` 在 GUI 版无 stdout 打不出来 | `fatal` 无条件落盘；`-version` 同时写日志文件（**不用 MessageBox**：它会阻塞，被脚本调用时把调用方一起挂死） |
| 读 DIB 像素前没有 `GdiFlush`（GDI 对 DIB 的绘制按线程批处理） | 加 `pGdiFlush` |
| `TestWindowPadCoversShadow` 恒真（`pad` 就是 `shadowReach` 本身）；`TestMSAAVtableSlotsLookSane` 只验「六地址互不相同」，**顺序整体错一位照样通过** | 换成真的断言：窗口最外圈 alpha 必须接近 0（阴影没被裁）；vtable 地址必须落在**可执行内存**里（`VirtualQuery`）；并注明「守顺序的是端到端那条测试」 |
| `TestForegroundConsoleDetectionDoesNotCrash` 断言体是 `_ = f()` | 类名表化 + `TestIsConsoleClass` 真断言 |

---

## 建议顺序

```
① UI goroutine 独占状态          ← 不做这个，UIA 会再添两条竞争路径
② TextSource + 错误分类          ← 让 UIA 从"改 5 个文件"变成"加 1 个文件"
③ 用户可见的失败通道
④ 廉价必修那一表
⑤ UIA TextPattern               ← 此时只是"多一个 reader"
```

**②③ 可以和第 ① 并行做**：② 只碰 `capture.go`/`msaa.go`，③ 是新增文件。

---

## 一条方法上的教训（被验证了两次）

第一轮修完竞争后我引入了一个**致命 bug**：用 `atomic.Bool` 表达采集线程「忙不忙」，零值是 `false` 而判据写成 `CompareAndSwap(true, false)`（要求当前值为 `true`）——
首次采集即被跳过，此后每一次都被跳过。**整个取词功能全废**，而界面上只是"静默地不弹菜单"。

它能上线的原因正是评审早就点明的那条：**`captureSelection` 主链路零覆盖**。
修完之后补了 `TestCaptureServiceHandlesFirstRequest`——断言"第一个请求必须被处理"，这条 bug 会被当场抓住。

**没有测试守着的关键路径，等于没有路径。**

## 一条方法上的教训

`-race` 在本项目**不可用**（D1：`CGO_ENABLED=0` 交叉编译需要 cgo）。所以跨线程状态**无法靠工具验证**——这正是数据竞争只能靠人读代码发现的原因。

既然工具这条路断了，就只能靠**让违反在类型层面不可表达**（第 1 项）来替代。
