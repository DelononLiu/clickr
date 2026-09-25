# NexusKB

Windows 划词助手的最小实现：**划词 → 取到选中文字 → 在选区旁弹出菜单**，外加一个可拖动的悬浮球。

参考的是有道词典那套「AI 助手」的观感（配色、圆角、阴影数值都是从它的前端 CSS 里对着抄的）。

> **文档分工**
> 本文件 = 怎么用、怎么编、整体思路。
> [`docs/architecture.md`](docs/architecture.md) = 架构设计（模块划分、**线程模型与所有权契约**、数据流）。
> [`docs/decisions.md`](docs/decisions.md) = 每个设计决策的理由与被否决的方案。
> [`docs/design-debt.md`](docs/design-debt.md) = 已知设计债与排序。

```
win32.go        Win32 API 裸绑定 + 常量
wintypes.go     Win32 结构体 + 带类型的薄封装
render.go       分层窗口软件渲染（距离场圆角/阴影、GDI 文字遮罩）
hook.go         WH_MOUSE_LL 全局鼠标钩子 + 手势状态机
selection.go    取文抽象（selection / textSource / pipeline / 错误分类）
capture.go      剪贴板取文（逐格式快照）+ 采集线程
uia.go          UIA TextPattern（精确选区 + 选区矩形）
msaa.go         MSAA 取词（COM 基础设施 + IAccessible vtable）
ui.go           窗口/布局/渲染/交互/持久化/菜单内容
main.go         初始化、消息循环、自检与导出
nexus_test.go   跑在真 Windows 上的测试（22 项）
build.sh        交叉编译脚本（含构建戳校验）
docs/           设计文档
```

---

## 跑起来

编译好的可执行文件：**`C:\Users\long2015\NexusKB\NexusKB.exe`**

1. 双击 `NexusKB.exe` → 屏幕右侧出现一个白色圆球（带品牌红图标）
2. 在任意程序里**拖选一段文字**（或双击选词）→ 松开鼠标后菜单自动弹在选区旁边
3. 菜单里三项：`复制` / `搜索` / `翻译`
4. 点别处 → 菜单自动收起
5. **右键悬浮球 → 退出**；按住悬浮球可以拖动，位置会记住

命令行参数：

| 参数 | 作用 |
|---|---|
| `-debug` | 打印取词/动作日志（同时写 `%LocalAppData%\NexusKB\nexuskb.log`） |
| `-version` | 打印构建戳后退出 —— 用来确认"跑着的到底是哪个版本" |
| `-selftest` | 显示悬浮球和菜单示例 2.5 秒后退出，并打印各窗口的 `visible` / `rect` |
| `-dump <前缀>` | 把渲染结果导出成裸像素文件，供离线逐像素检查 |

## 编译

```bash
./build.sh            # 在 Linux/WSL 上直接出 Windows exe
```

全部代码只用 `syscall` + 标准库，**没有第三方依赖、没有 cgo**，所以 `CGO_ENABLED=0` 就能交叉编译，不需要 mingw-w64。

`build.sh` 每次注入一个构建戳，并在构建后 `grep` 二进制确认戳真的写进去了；不一致就非零退出。**部署后可以直接问产物自己**：

```bash
./NexusKB-debug.exe -version     # -> NexusKB build=20260926-035149
```

也建议在 Windows 上直接编：`go build -ldflags "-H=windowsgui -s -w" -o NexusKB.exe .`

## 测试

渲染依赖 GDI，只能在 Windows 上跑；交叉编译测试二进制再通过 WSL interop 执行：

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o nkb.test.exe .
./nkb.test.exe -test.v
```

覆盖：手势识别（拖选 / 抖动 / 单击 / 双击 / 远距离单击不误判）、弹层定位（四角与正中都完整落进工作区、不遮挡锚点）、剪贴板快照往返（**直接复现过真实故障**）、格式分类、MSAA 端到端（自建窗口 + Static 控件读回文本，同时验证 vtable 槽位 / BSTR / `accLocation` / 子控件命中）、窗口可见性回归、逐像素渲染断言。

> ⚠️ **`-race` 在本项目不可用** —— 它需要 cgo，而本项目刻意保持 `CGO_ENABLED=0` 以便从 Linux 交叉编译（见 `docs/decisions.md` D1）。跨线程状态因此**无法靠工具验证**，只能靠结构设计（见 `docs/design-debt.md` 第 1 项）。

---

## 三个功能各自怎么实现的

### 1. 识别「划词」

`WH_MOUSE_LL` 全局低层鼠标钩子，装在**独立且 `LockOSThread` 锁死**的线程上，那个线程自己跑消息循环。

判定规则（`hook.go`）：左键按下记坐标 → 左键抬起时位移超过 5px 算拖选；否则按单击处理，两次快速单击且位置接近算双击选词。落在本进程窗口上的事件直接丢掉（否则点自己的菜单会又被当成一次划词）。

**最容易踩的坑**：钩子回调里不能干任何重活。Windows 有 `LowLevelHooksTimeout`（默认 300ms），回调超时会被**静默摘钩** —— 不报错、不回调，表现就是「用一会儿就不灵了」。所以回调只做一件事：非阻塞地往 channel 里塞事件。

### 2. 取到选中文字

两层，**顺序是刻意的**：

三种手段按顺序试，**顺序就是优先级**（`selection.go` 里 `capturePipelineDefault.sources` 是一个切片，加一种就是加一行）：

| 顺序 | 手段 | 能拿到什么 | 代价 |
|---|---|---|---|
| 1 | `Ctrl+C` + 读剪贴板 | 用户**真正拖选的那一段** | 有副作用、动剪贴板 |
| 2 | **UIA `TextPattern`** | **精确选区 + 选区矩形** | 依赖控件提供 TextPattern |
| 3 | MSAA `AccessibleObjectFromPoint` | 鼠标点所在的**元素/词/行** | 精度低（MSAA 没有选区 API） |

**顺序不能乱**：MSAA 拿不到选区，放前面会把浏览器/编辑器里本来很准的"精确选区"退化成"这一行"。

**为什么 UIA 不直接排第一**（它其实"更对"——不发 Ctrl+C、不碰剪贴板、还给得出选区矩形）：先保守放在剪贴板之后，避免回归。等实测确认它在各处都可靠，把它提到第一位就是**切片里换一行**的事，那一步能连 Ctrl+C 的副作用一起去掉。

跑 `NexusKB-debug.exe -probe` 可以对当前鼠标位置跑一遍**只读**的 UIA / MSAA 并打印结果，用来收集这个决策所需的实测数据。

**剪贴板通路**（`capture.go`）：

- 先睡 160ms 让源程序把选区落到剪贴板
- **在发 Ctrl+C 之前**用 `EnumClipboardFormats` 逐格式快照用户原本的剪贴板（HGLOBAL 类格式拷字节；`CF_BITMAP` 用 `CopyImage` 复制，它不是 HGLOBAL）
- `SendInput` 发 Ctrl+C，轮询 `GetClipboardSequenceNumber()` 等它变化（最多 320ms）
- **序列号没变 = 源程序不支持复制 = 根本没有选区**，这是这个方法白送的一个判断
- 把快照写回剪贴板（失败会重试并**明确记日志**：那意味着用户剪贴板内容丢了）
- 同一段文字 900ms 内不重复弹
- 单次采集有 3 秒看门狗；超时后**不再并发新采集**（放弃的那次还在后台跑，并发操作同一个剪贴板会互相破坏）

> **为什么不用 OLE 存剪贴板**：第一版用 `OleGetClipboard` 存 `IDataObject` + `OleSetClipboard` 还原，实测约 **6/7 次失败**，还会连累下一次取词。根因是 `OleGetClipboard` 返回的**代理只在剪贴板未被修改期间有效**，而我们紧接着就发 Ctrl+C 改了它。详见 `docs/decisions.md` D4。

**UIA 通路**（`uia.go`）：`GetFocusedElement`（拿不到再按坐标命中）→ `GetCurrentPattern(UIA_TextPatternId)` → `GetSelection` → `GetText(-1)` + `GetBoundingRectangles`。

拿到选区矩形后会**用它当弹窗锚点**（取选区末尾那个矩形），比鼠标抬起点准 —— 反向拖选时差别明显。

> vtable 槽位是**机械探测出来的**，不是照头文件推的。我最初按 IDL 声明顺序推断 `GetSelection` 在槽位 6，结果它返回的是整行文字（其实 `GetVisibleRanges` 在槽位 6，`GetSelection` 在 5）。用 `-uia-slot=N` 逐槽位探测（每个槽位单跑一个进程，崩了只影响那一次）才定下来。教训写在代码注释里：**vtable 顺序这种东西，能测就别推。**

### 3. 弹出菜单

`ui.go`：`WS_EX_LAYERED | WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE` 的弹出窗口，用 `UpdateLayeredWindow` 逐像素呈现。

**`NOACTIVATE` 是命门**：一旦弹窗抢了焦点，源程序的选区高亮会消失、选区也可能被清掉，后面的 Ctrl+C 就什么都复制不到。

显示走 `SetWindowPos(..., SWP_NOACTIVATE | SWP_SHOWWINDOW)`，**绝不用 `ShowWindow(SW_SHOW)`** —— 只有前者能带 `SWP_NOACTIVATE`。

> 另一个坑：`UpdateLayeredWindow` 只设「位置/尺寸/内容/透明度」，**不负责显示窗口**。少了那一步 `SetWindowPos`，坐标正确、像素也画好了，但屏幕上什么都看不到。

定位（`placeMenu`）：优先弹在锚点右下，右边放不下翻左边、下面放不下翻上面，最后把卡片整体夹进显示器工作区（`rcWork`，不是整块屏幕）。

视觉：`render.go` 里用「圆角矩形的有符号距离场 + 高斯衰减」还原 CSS 的 `border-radius` 和 `box-shadow`。窗口留白从 `shadowReach(blur, dy)` 反推，不写死 —— 写死过，结果阴影被窗口边界切出一条直边。

> **第三个坑**：GDI 往 32bpp DIB 上画东西时**不写 alpha 字节**，而 `UpdateLayeredWindow` 用的是预乘 alpha，alpha=0 就等于全透明 —— 直接 `DrawTextW` 画字会一个字都看不见。
> 做法是「黑字白底渲染到 scratch → 用亮度反相当作覆盖率 → 手动合成」，既绕开限制又保留抗锯齿。
> 因此字体质量必须用 `ANTIALIASED_QUALITY`（灰度），**不能用 ClearType** —— ClearType 产生彩色子像素，会破坏「亮度当覆盖率」这个前提。

### 悬浮球

同样是一个分层窗口，处理拖动（`SetCapture` + 移动窗口）、`WM_DPICHANGED`（拖到另一块不同缩放的显示器上时重新渲染）、右键「退出」。位置存到 `%AppData%\NexusKB\ball.pos`。

球体配色是从有道那个 PNG 里量出来的：浅色主题白球、深色主题 `#303134`，中心品牌红 **`#F0142F`**（精确值，测试里有断言）。

---

## 已知限制

1. **真控制台（cmd / PowerShell / Windows Terminal）里什么都不做** —— 不发 Ctrl+C（控制台里 Ctrl+C 是"中断"不是"复制"，会被透传给 shell 把用户正在跑的命令打断），也不用 MSAA 冒充（实测 MSAA 在那返回的是控制台窗口自身的名字/整块缓冲区，**不是选区**，弹一个内容不对的菜单比不弹更糟）。
   **这不是实现遗漏**：有道的 binaries 里没有任何控制台 API，它的划词在真控制台里同样无效。要支持得走控制台自己的 `AttachConsole` + `GetConsoleSelectionInfo` + `ReadConsoleOutputCharacterW`，尚未实现。
2. **只认鼠标划词**，不认键盘选区（Shift+方向键、Ctrl+A）。
3. **剪贴板法有副作用**：会给源程序发一次真实的复制事件，可能进入它的复制历史。参数是编译期常量（`capture.go` 顶部 `selectionSettleMS` / `clipboardWaitMS` / `dedupeWindowMS`），**目前没有暴露成命令行参数**。
4. **剪贴板还原不是全格式覆盖**：非 HGLOBAL 的格式（`CF_PALETTE` / `CF_ENHMETAFILE` / `CF_OWNERDISPLAY` 等）和单格式超过 32MB 的内容不会被快照，还原时**会丢**。`CF_BITMAP` / `CF_DIB` / `CF_HDROP`（复制的文件）/ 注册格式（HTML / RTF）在内。
5. **菜单上显示的快键键（`Ctrl+C` / `Enter` / `Ctrl+T`）目前只是展示，没有接线。** 窗口是 `WS_EX_NOACTIVATE` 拿不到键盘焦点，要接线只能再上键盘钩子。
6. **分层窗口的阴影区会吃掉鼠标消息**。Windows 对分层窗口的命中测试按 alpha 通道做，alpha 不为 0 的地方就捕获点击（阴影 alpha 虽小但不为 0），而 `HTTRANSPARENT` 只在同线程内转发。所以悬浮球外面约 20px 的阴影环是一小块"死区"。
   菜单不受影响 —— 钩子在左键按下时会判「点是不是在卡片外」，是就立刻收起，点击会正常落到下面的程序上。
7. **不支持高亮跟随**：菜单不会跟着选区跑，只在弹出那一刻定位。
8. **失败没有用户可见的反馈**：采集失败、剪贴板还原失败都只写日志，而日志默认关闭（`-debug` 才开）。用户侧的表现是"什么都没发生"。见 `docs/design-debt.md` 第 3 项。

## 下一步

**接 UIA `TextPattern`**（精确选区 + 选区矩形）。

```
IUIAutomation::ElementFromPoint(pt)
  → GetCurrentPatternAs(UIA_TextPatternId)   → IUIAutomationTextPattern
  → GetSelection()                            → IUIAutomationTextRangeArray
  → GetElement(0)                             → IUIAutomationTextRange
  → GetText(-1) + GetBoundingRectangles()
```

好处：不再发 Ctrl+C（无副作用）、拿到**选区矩形**（弹窗贴住选区末尾而不是猜鼠标点）、能区分「没有选区」和「取文失败」。

**但顺序很重要 —— 必须先做两件前置**，否则 UIA 会变成对 `captureSelection` 的第 4 次、紧接着第 5 次修改：

```
① UI goroutine 独占窗口/GDI/布局状态   ← 否则 UIA 的两条异步路径又添两处竞争
② TextSource 抽象 + 错误分类           ← 做完之后 UIA 只是"新增一个文件 + sources 加一行"
③ UIA TextPattern
```

详见 [`docs/design-debt.md`](docs/design-debt.md)。

**Go 侧的现状**：唯一的 UIA 库 `hnakamur/w32uiautomation`（及其 fork `BelodedAleksey/w32uiautomation`）**没有实现 TextPattern / TextRange / TextRangeArray** —— 恰好是取选区所需的那三个接口，得自己补，大约 150 行。

**vtable 的字段顺序必须和 `UIAutomationClient.h` 里接口方法的声明顺序逐字一致**，漏一个或顺序错位就会跳到错误的函数地址上，症状是崩溃或莫名 HRESULT。照抄时务必对着 SDK 头文件核对：

```
C:\Program Files (x86)\Windows Kits\10\Include\<版本>\um\UIAutomationClient.h
```
