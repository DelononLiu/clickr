# NexusKB 架构设计

**读者**：要改动这个项目的人。
**不在这里的内容**：怎么用 → `README.md`；为什么这么选 → `docs/decisions.md`；哪里还欠着 → `docs/design-debt.md`。

---

## 1. 问题与目标

### 1.1 复杂度从哪里来

Windows **没有**一个统一 API 能回答「用户现在选中了哪段文字」。这件事得靠拼凑：

| 手段 | 能拿到什么 | 代价 |
|---|---|---|
| `Ctrl+C` + 剪贴板 | 用户真正拖选的那一段 | 有副作用、会动用户剪贴板、控制台里 Ctrl+C 是中断 |
| UIA `TextPattern` | 精确选区 + 选区矩形 | **只读无副作用**；依赖控件提供 TextPattern |
| MSAA `AccessibleObjectFromPoint` | 某个坐标下的可访问对象文本 | 只有"元素/词/行"，**没有选区概念** |
| UIA `TextPattern` | 精确选区 + 选区矩形 | 要手写 COM vtable；仍未实现 |
| UIA `TextPattern` | 精确选区 + 选区矩形 | 只读无副作用；依赖控件提供 TextPattern |
| 控制台 API | 真控制台的选区 | 需要 `AttachConsole`；UIA 已覆盖，暂不需要 |

**整个项目的复杂度几乎全部来自这一格**：模块划分、线程模型、失败处理，都是为了把"多种取文手段 + 各自的失败模式"收进一个可控的形状里。渲染和窗口那些反而是机械活。

### 1.2 目标

- 划词（拖选 / 双击选词）→ 取到选中文字 → 在选区旁弹出菜单
- 一个可拖动的悬浮球，位置持久化
- 观感对齐有道词典的 AI 助手（配色 / 圆角 / 阴影 / 暗色主题）
- **不许抢焦点**（抢了源程序的选区就没了）
- **不许破坏用户的剪贴板**
- 零第三方依赖、无 cgo，可从 Linux 交叉编译

### 1.3 非目标

- 真控制台里取文（cmd / PowerShell / Windows Terminal）——见 §6.4
- 键盘选区（Shift+方向键 / Ctrl+A）
- 富文本 / 图片取文、OCR
- 多实例（全程单例）
- 皮肤/主题系统（只跟随系统明暗）

---

## 2. 全景

```
┌──────────────────────────────────────────────────────────────────┐
│  UI 线程（main goroutine，LockOSThread + 消息循环）                │
│    · 创建并拥有全部窗口（悬浮球 / 菜单，均 WS_EX_LAYERED +         │
│      WS_EX_NOACTIVATE）                                           │
│    · 拥有全部 GDI surface、布局常量、菜单内容与交互状态            │
│    · WndProc：命中测试 / hover / 拖动 / 菜单项点击                 │
│    · 唯一有权调用 Win32 窗口 API 的线程                            │
└───────▲──────────────────────────────┬───────────────────────────┘
        │ PostMessage（单向，唯一入口）   │ 窗口消息
        │                              ▼
┌───────┴───────────────┐   ┌──────────────────────────────────────┐
│  钩子线程              │   │  采集线程                             │
│  LockOSThread +        │   │  LockOSThread + OleInitialize         │
│  SetWindowsHookEx      │   │  串行处理 captureCh                   │
│  (WH_MOUSE_LL)         │   │  · 剪贴板快照 / Ctrl+C / 读回 / 还原   │
│  回调只做非阻塞入队     │   │  · MSAA 兜底                          │
│        │               │   │        │                              │
│        ▼               │   │        ▼                              │
│   hookCh（缓冲 128）    │   │  queueMenu() ─ PostMessage ──────────┼──► 回 UI 线程
│        │               │   └──────────────────────────────────────┘
│        ▼               │
│  手势状态机            │
│  drag / double-click   │
│        │               │
│        ▼               │
│   captureCh（缓冲 4）───┼──► 采集线程
└────────────────────────┘
```

**关键性质**：跨线程通信**只有两个方向、两个原语**：

1. 别的线程 → UI 线程：`PostMessage(hwndBall, wmApp+N, ...)`，数据放锁保护的 `pending*`
2. UI 线程 → 别的线程：不需要（UI 从不主动调别的线程）

**没有任何线程直接操作另一个线程的窗口或状态**。这是这个设计唯一真正的不变量。

---

## 3. 模块划分

### 3.1 分层

```
编排层   main.go       初始化顺序、消息循环、自检/导出模式
          │
能力层   capture.go    取文策略（剪贴板快照 + MSAA 兜底）
         hook.go       全局手势识别
         msaa.go       MSAA 取词（COM 基础设施 + IAccessible vtable）
          │
界面层   ui.go         窗口/布局/渲染/交互/持久化/菜单业务内容  ← 唯一有 SRP 问题的地方
         render.go     分层窗口软件渲染（距离场 + GDI 文字遮罩）
          │
平台层   wintypes.go   Win32 结构体定义 + 带类型的薄封装
         win32.go      DLL / proc 绑定 + 全部常量
```

依赖方向**严格向下**，无环。唯一的坏味道是 `msaa.go` 读 `capture.go` 里的 `maxMSAATextRunes`，而 `capture.go` 调 `msaa.go` 的 `msaaTextAt` —— 文件级双向，说明能力层的边界没划干净（同包，编译器不报）。

### 3.2 各文件职责

| 文件 | 行数 | 职责 | 变更原因 |
|---|---|---|---|
| `win32.go` | 317 | DLL/proc 绑定、全部常量 | 用到新的 Win32 API 时 |
| `wintypes.go` | 430 | 结构体定义 + 薄封装 | 同上 |
| `render.go` | 507 | surface、距离场图元、文字遮罩、`present` | 视觉算法变化时 |
| `ui.go` | 811 | **窗口 / 布局 / 渲染 / 交互 / 持久化 / 菜单内容** | **六种互不相关的原因** ⚠ |
| `hook.go` | 187 | 钩子安装 + 手势状态机 | 触发规则变化时 |
| `capture.go` | 494 | 剪贴板快照 + 取文策略编排 | 取文手段增减时 |
| `msaa.go` | 259 | COM 基础设施 + IAccessible | MSAA 相关 |
| `main.go` | 257 | 组装与生命周期 | 初始化顺序变化时 |

### 3.3 `wintypes.go` 的隐藏问题

它同时装了两类**变更原因完全不同**的东西：

- **ABI 关键的结构体定义**（`wndClassExW` / `msg` / `monitorInfo` / `variant` / `logFontW` …）——必须逐字节对齐 Windows SDK，审阅方式是"和头文件比对"
- **业务性的 API 封装**（`workArea` / `systemUsesDarkMode` / `foregroundIsConsole` / `isOwnWindow` …）——审阅方式是"逻辑对不对"

混在一起让前者更难审。`systemUsesDarkMode`（读注册表）其实是**应用配置**，不是 Win32 类型，放这里尤其错位。

---

## 4. 线程模型与所有权契约

> 这一节是整份文档最重要的部分。历史上有 3 个 bug（离焦焦点、跨线程 ShowWindow、数据竞争）全部源于这条契约**只写在散文注释里**。

### 4.1 三条 Win32 硬约束

1. **窗口只能被创建它的线程操作**。`SetWindowPos` / `ShowWindow` / `DestroyWindow` 跨线程调用会走 `SendMessage`，而目标线程若不跑消息循环就会**永久阻塞**。
2. **COM 是按线程初始化的**。`OleInitialize` 必须在调用 `CoCreateInstance` / MSAA 的那个线程上做。
3. **剪贴板是全局独占资源**。同一时刻只能有一个进程开着它；两个线程并发操作会互相破坏。

### 4.2 三个角色

| 线程 | 生命周期 | 拥有什么 | 允许做什么 |
|---|---|---|---|
| **UI 线程** | 进程全程，`LockOSThread` + `GetMessageW` 循环 | 全部 `HWND`、全部 `*surface`、`scale`、`menuModel_`、`ballPos`、`menuHover`… | 任何 Win32 窗口/GDI 调用 |
| **钩子线程** | 进程全程，`LockOSThread` + 自己的消息循环 | `HHOOK` | **只能**非阻塞写 `hookCh` |
| **采集线程** | 进程全程，串行消费 `captureCh` | 剪贴板操作权 | GDI/窗口一律不许碰 |

外加：**每次采集起一个短命 goroutine**（带 3 秒看门狗），锁线程 + `OleInitialize` 后执行。

### 4.3 跨线程路径清单

| 从 | 到 | 机制 | 判定 |
|---|---|---|---|
| 钩子回调 | `hookCh` | 缓冲 128，`select/default` 非阻塞 | ✅ 安全，全项目最正确的一处 |
| `consumeHookEvents` | `hideMenuIfOutside` | 读 `menuRectMu` + `PostMessage` | ✅ 安全（非原子但无害） |
| `dispatchGesture` | `captureCh` | 非阻塞投递 | ✅ 安全 |
| 采集线程 | `queueMenu` | 锁内写 `pending*` + `PostMessage` | ✅ 安全 |
| UI 线程 | `pending*` 读 | `stateMu` | ✅ 安全 |
| 采集回调 | `lastSelection` | `lastSelMu` | ✅ 安全（**曾是竞争**） |
| 菜单项 `run()` | 任意 | `go chosen.run()` | ⚠️ **靠运气**：三个 action 恰好只调 `postToMain`/`setClipboardText`/`shellOpen`，将来任何一个碰窗口就复现"跨线程操作窗口" |
| `recreateSurfaces` | — | 无锁无文档 | ⚠️ **只能 UI 线程调**，但没有任何东西表达这一点 |

### 4.4 契约表达方式（现状）

**没有**。契约只存在于：

- 散文注释（`ui.go` 的"只允许主线程读写"、`main.go` 的线程模型说明）
- 命名前缀 `post*` / `queue*`
- 我的记忆

编译器看不见，reviewer 看不见，新加的代码看不见。**这是设计债 #1**（见 `docs/design-debt.md`）。

### 4.5 违反后的症状（真实记录）

| 症状 | 根因 |
|---|---|
| 菜单偶尔不消失 | 钩子线程直接跨线程调 `ShowWindow` → 改 `PostMessage` |
| 测试永久挂起 | 测试里跨线程 `SetWindowPos` → `SendMessage` 无人泵消息 |
| 菜单内容偶发错乱 | `menuModel_` 一个线程写、另一个线程渲染时无锁读 |
| 用户剪贴板被覆盖 | 超时放弃的采集线程仍在跑，与下一次采集并发还原 |

---

## 5. 关键数据流

```
用户在任意程序里拖选 → 松开左键
  │
  ├─[钩子线程] WH_MOUSE_LL 回调：记录 down 点，up 时算位移
  │    位移 > 5px → 判定为划词
  │    非阻塞入队 hookCh  →  (回调到此结束，绝不干重活)
  │
  ├─[消费 goroutine] 手势状态机
  │    拖选 / 双击选词 → onGesture
  │    左键按下 → onOutsideClick（点在菜单卡片外就收起）
  │    过滤：落在本进程窗口上的事件直接丢弃
  │
  ├─[采集 goroutine，串行]
  │    0. 前台是控制台？ → 记 method=skipped-console，直接返回（见 §6.4）
  │    1. 睡 160ms 等源程序把选区落到剪贴板
  │    2. 记 beforeSeq / beforeText
  │    3. snapshotClipboard()  ← 必须在发 Ctrl+C 之前
  │    4. sendCtrlC()
  │    5. 轮询 GetClipboardSequenceNumber 最多 320ms；变了就读
  │    6. 兜底：序列号没变但内容变了
  │    7. snap.restore()   ← 还用户剪贴板
  │    8. 仍失败？ → MSAA 兜底
  │
  └─[采集 goroutine → UI 线程] queueMenu(anchor, model, text) + PostMessage
       │
       └─[UI 线程] wmShowMenu
            取 pending* → showMenu()
              · 按锚点显示器 DPI 定 scale
              · renderMenu() → surface（距离场画卡片/阴影 + GDI 遮罩画字）
              · placeMenu() 定位（右下优先，翻转，夹进工作区）
              · present() = UpdateLayeredWindow + SetWindowPos(SWP_SHOWWINDOW|SWP_NOACTIVATE)
```

**为什么"取文"和"弹菜单"要分在两个线程**：取文会阻塞（轮询剪贴板最长 320ms、MSAA 跨进程调用可能很慢），而 UI 线程必须随时响应窗口消息。

---

## 6. 取文策略

### 6.1 顺序是刻意的

```
剪贴板优先 → MSAA 兜底
```

**不能反过来**：MSAA 拿到的只是"鼠标点所在的那个元素/词/行"，**MSAA 没有选区 API**。反过来会把浏览器/编辑器里已经很好的行为弄坏。

剪贴板法虽然副作用大，但它是唯一能拿到"用户真正拖选的那一段"的手段。

### 6.2 剪贴板快照为什么自己实现（不用 OLE）

早期版本用 `OleGetClipboard` 存 `IDataObject`、用完 `OleSetClipboard` 放回去。**实测约 6/7 次还原失败**（`CLIPBRD_E_CANT_OPEN` / `CANT_CLOSE`），而且会连累下一次取词。

根因：`OleGetClipboard` 返回的是**代理对象，只在剪贴板未被修改期间有效**——而我们紧接着就发 Ctrl+C 改了剪贴板，代理当场失效。补 `OleFlushClipboard` 也没用，那个函数只对"本进程自己放进剪贴板的数据"有意义。

现在：`EnumClipboardFormats` 枚举 → HGLOBAL 类格式拷字节 → `CF_BITMAP` 用 `CopyImage` → 还原时 `EmptyClipboard` 再逐个写回。**全程无代理，行为确定。**

### 6.3 剪贴板法的固有代价

| 代价 | 对策 |
|---|---|
| 清掉用户剪贴板 | 逐格式快照 + 还原；还原失败会**明确记日志** |
| 给源程序发一次真实复制事件 | 无法避免，接受 |
| 源程序不支持复制 | 序列号不变 → 正好当作"没有选区"的信号（白送的好处） |
| 延迟渲染的程序可能让 `GetClipboardData` **永久阻塞** | 单次采集 3 秒看门狗；超时后**不再并发新采集** |

### 6.4 控制台：刻意什么都不做

真控制台（`ConsoleWindowClass` / `CASCADIA_HOSTING_WINDOW_CLASS` / `mintty`）上前台时：

- **不发 Ctrl+C**：控制台里 Ctrl+C 是"中断"不是"复制"，会被透传给 shell，把用户正在跑的命令打断。这个副作用比"取不到词"严重得多。
- **不用 MSAA 冒充**：实测 MSAA 在控制台上返回的是**窗口自身的名字/整块缓冲区**，不是用户拖选的选区。弹一个内容不对的菜单比不弹更糟。

**要真正支持，正路是控制台自己的 API**：`AttachConsole(pid)` → `GetConsoleSelectionInfo()` → `ReadConsoleOutputCharacterW()`。尚未实现。

> 旁证：有道词典用的是 MSAA，它的 binaries 里**没有任何控制台 API**——所以有道的划词在真控制台里同样无效。这是技术路线的固有限制，不是实现遗漏。

### 6.5 为什么先 MSAA 而不是直接上 UIA

`IAccessible` 是 **IDispatch 派生**，vtable 前 7 槽是 IUnknown+IDispatch、后 21 槽是 `acc*`，顺序自 Win95 起固定。所以按序号取函数指针直接调即可，**不用实现 IDispatch::Invoke 的参数封送**。

UIA 的 `IUIAutomationTextPattern` / `TextRange` / `TextRangeArray` 三个接口都得手写 vtable，方法更多、顺序抄错直接崩。成本差一个量级。

代价是 MSAA 拿不到选区——所以 UIA 仍是终局（§8）。

---

## 7. 渲染管线

### 7.1 为什么是分层窗口 + 软件渲染

要"圆角 + 柔和阴影"，普通窗口做不到，必须逐像素 alpha。所以：

```
surface（32bpp 自上而下 DIB）
  → 距离场图元：fillRoundRect / strokeRoundRect / shadowRoundRect / fillCircle / fillSparkle
  → GDI 文字遮罩
  → present()：UpdateLayeredWindow（预乘 alpha）
```

选软件渲染而不是 D2D/GDI+：圆角+阴影本质是"有符号距离场 + 高斯衰减"，几十行数学就够，且完全可控、零依赖。

**三个非显然的坑**：

1. **GDI 往 32bpp DIB 上画东西不写 alpha 字节**（一直是 0），而 `UpdateLayeredWindow` 用预乘 alpha —— 直接 `DrawTextW` 画字会**一个字都看不见**。
2. 所以文字走**"黑字白底渲染 → 用亮度反相当覆盖率 → 手动合成"**。既绕开限制又保留抗锯齿。
3. 因此字体质量必须是 `ANTIALIASED_QUALITY`（灰度），**不能用 ClearType** —— ClearType 产生彩色子像素，会破坏"亮度当覆盖率"这个前提。

### 7.2 `UpdateLayeredWindow` 不负责显示窗口

它只设「位置 / 尺寸 / 内容 / 透明度」。**少了 `SetWindowPos(SWP_SHOWWINDOW)`，窗口矩形正确、像素也画好了，但屏幕上什么都看不到**——这个坑真踩过。

用 `SetWindowPos + SWP_NOACTIVATE` 而**不是** `ShowWindow(SW_SHOW)`：只有前者能带 `SWP_NOACTIVATE`，保证不抢焦点（§7.3）。

### 7.3 不抢焦点是硬要求

弹出菜单若抢焦点：源程序的**选区高亮会消失、选区也可能被清掉**，后面的 Ctrl+C 就复制不到东西了。

所以窗口是 `WS_EX_NOACTIVATE`，显示走 `SWP_NOACTIVATE`。**没有任何一条路径会激活窗口。**

### 7.4 窗口留白从阴影参数反推

窗口必须比可见卡片大出投影的扩散半径，否则阴影会被窗口边界**硬切出一条直边**（留白写死 18px 而实际要 21.4px 时真的看出来了）。

现在 `menuShadowPad()` / `ballShadowPad()` 从 `shadowReach(blur, dy)` 反推，有测试守着。

---

## 8. 演进：接入 UIA

### 8.1 现状的障碍

`captureSelection` **已经因为"换取文手段"被改过三次**（OLE→快照、加控制台防护、加 MSAA）。它现在是一个 if 链：

```go
if foregroundIsConsole() { ...特例... }
snapshotClipboard(); sendCtrlC(); ...轮询...
if !res.ok { msaaTextAt() }
res.method = "clipboard-seq" | "clipboard-diff" | "msaa-fallback"   // 魔法字符串
```

**这是修改式扩展，不是扩展式**。加 UIA 会变成第四次修改。

### 8.2 目标抽象

```go
type selection struct {
    Text      string
    Anchor    point
    Bounds    rect    // UIA 可得；剪贴板法留空
    HasBounds bool
}

var ErrNoSelection = errors.New("no selection")   // 与「读取失败」区分开

type SelectionReader interface {
    Name() string
    Read(ctx context.Context, at point) (selection, error)
}

type capturePipeline struct{ readers []SelectionReader }  // 顺序即优先级：是数据，不是控制流
```

收益：

- **UIA 只是一个新实现**，`Bounds` 自动可用 → 弹窗能贴住选区末尾，而不是猜鼠标点
- **`ErrNoSelection` vs 读取失败**分开 —— 这正是 UIA 的第一价值（剪贴板法只能靠"序列号没变"猜）
- 控制台从"函数中段的特例"变成"快速返回 `ErrNoSelection` 的 reader"，「不发 Ctrl+C」这个副作用约束与兜底顺序**解耦**
- 优先级从 if 链变成**切片顺序**，可以用 fake reader 单测，不必再拿开发者真实剪贴板当 fixture

### 8.3 迁移顺序（重要）

**必须先做 UI 线程所有权重构**（`docs/design-debt.md` 第 1 项）。

UIA 会引入两条新的跨线程路径（异步的选区矩形、可能失败的调用）。在所有权契约还只活在注释里的时候接 UIA，等于往一个已知会漏的地方再加两处接口。

```
① UI goroutine 独占窗口/GDI/布局状态        ← 不做这个，UIA 会再添两条竞争路径
② SelectionReader 抽象 + 错误分类
③ 用户可见的失败通道（当前失败只在默认关闭的日志里）
④ UIA TextPattern（此时只是"多一个 reader"）
```

---

## 9. 测试策略

22 个测试，**在真 Windows 上跑**（渲染依赖 GDI，无法在 Linux 执行）：

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o nkb.test.exe .
./nkb.test.exe -test.v          # 经 WSL interop 执行
```

| 区域 | 覆盖 | 有效性 |
|---|---|---|
| 手势识别 | 拖选 / 抖动 / 单击 / 双击 / 远距离单击 | ✅ 纯逻辑，喂合成事件 |
| 弹层定位 | 四角 + 正中都完整落进工作区；不遮挡锚点 | ✅ 纯几何 |
| 剪贴板 | **快照→覆盖→还原**往返；格式分类 | ✅ 直接复现过真实故障 |
| MSAA | 自建窗口 + Static 控件，端到端读回文本 | ✅ 同时验证 vtable 槽位 / BSTR / accLocation / 子控件命中 |
| 渲染 | 圆角透明、品牌红精确值、悬停底色、文字墨迹 | ⚠️ 断言像素，脆弱；视觉回归更适合看图 |
| **WndProc / 交互状态机** | **零覆盖** | ❌ 交互 bug 的所在地 |

两个专门的验证工具（这是本项目最有价值的可测试性投入）：

- **`-dump <前缀>`**：把渲染结果导成裸像素文件 + 一个小分析脚本 → GDI 渲染可以在没有桌面的环境下被**逐像素验证**
- **`-version` + build.sh 构建戳**：让"部署的到底是哪个版本"变成可机械回答的问题（这条是被坑出来的，见 `docs/decisions.md` D9）
