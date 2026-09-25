# 设计决策记录

每条记录：**背景 → 决策 → 被否决的方案 → 后果**。
被否决的方案要留着——它们的失败原因正是后来者最需要知道的。

---

## D1 纯 Go + 零第三方依赖 + 无 cgo

**背景**：需要调大量 Win32 API，还要能从 Linux 交叉编译出 Windows exe。

**决策**：全部走 `syscall.NewLazyDLL` + `LazyProc.Call`，不引 `golang.org/x/sys/windows`，不引 `go-ole`。

**被否决**：
- `golang.org/x/sys/windows` —— 它确实更方便，但只覆盖一部分 API，混用两套风格比统一裸调更乱。
- **cgo + 一个 C 封装**（例如把 UIA 逻辑写成 C）—— 一旦引入 cgo 就要装 mingw-w64，`GOOS=windows CGO_ENABLED=0 go build` 这条从 WSL 一键出 exe 的链路就断了。宁可在 Go 里手写 COM vtable。

**后果**：
- ✅ `CGO_ENABLED=0` 交叉编译可用；无 `go.sum`；构建快
- ❌ **`-race` 不可用**（需要 cgo）。跨线程状态只能靠人读，不能靠工具验证——这是本项目最实在的代价，见 `design-debt.md`
- ❌ 需要自己维护结构体 ABI、HRESULT/BOOL 判定约定、COM vtable 槽位

---

## D2 原生 Win32 分层窗口，不上 Tauri / WebView2

**背景**：参考实现（有道）是 Tauri 2 + WebView2。Web 技术栈写 UI 明显更快。

**决策**：自己 `CreateWindowExW` + `UpdateLayeredWindow`。

**被否决**：
- **Tauri**：为了一个 4 项的菜单拖进 Rust 工具链 + WebView2 运行时依赖，且**窗口样式不可控**——Tauri 自己创建窗口，等你能 `SetWindowLongPtrW(GWL_EXSTYLE)` 补 `WS_EX_NOACTIVATE` 时，窗口已经显示过一次、可能已经抢过焦点了。而"不抢焦点"是本项目的硬要求。
- **WebView2 直接挂**：同上，库拥有窗口，拿不到创建时的样式控制权。

**后果**：
- ✅ `WS_EX_NOACTIVATE` / `SWP_NOACTIVATE` 全程可控；体积 2MB、无运行时依赖
- ❌ 视觉全靠手写：距离场圆角、阴影、文字遮罩（见 D3、D11）
- ❌ 要写窗口消息循环、命中测试、hover/press 状态机

---

## D3 软件渲染（距离场），不上 D2D / GDI+

**背景**：要圆角 + 柔和阴影，必须逐像素 alpha（分层窗口）。

**决策**：自己算圆角矩形的有符号距离场，阴影用高斯衰减。约 200 行纯数学。

**被否决**：
- **GDI+**：要绑 `GdipCreateFromHDC`/`GdipFillPath`/`GdipDrawString` 一大串扁平 API，比距离场代码还多。
- **Direct2D**：重，且对"画一个圆角矩形+阴影"是过度方案。
- **普通窗口 + `SetWindowRgn` 圆角**：没有阴影，且 16px 圆角的锯齿肉眼可见。

**后果**：
- ✅ 零依赖、完全可控、抗锯齿质量由自己决定
- ✅ 可以导出裸像素做逐像素验证（`-dump`）
- ❌ 字体渲染要绕过 GDI 不写 alpha 的限制（D11）

---

## D4 剪贴板自己逐格式快照，不用 OLE 代理

**背景**：剪贴板法必须先存下用户原本的剪贴板内容，用完还回去。第一版用 `OleGetClipboard` 存 `IDataObject`、`OleSetClipboard` 还原。

**症状**：实测约 **6/7 次还原失败**（`CLIPBRD_E_CANT_OPEN` / `CANT_CLOSE`），而且**会连累下一次取词**——还原失败时剪贴板可能被留在打开状态，下一次 `OpenClipboard` 就失败。用户侧表现是"划词弄丢了我的剪贴板"。

**根因**：`OleGetClipboard` 返回的是**代理对象，只在剪贴板未被修改期间有效**。而我们紧接着就发 Ctrl+C 把剪贴板换掉了，代理当场失效，`OleSetClipboard` 必然失败。

**被否决的补救**：先 `OleGetClipboard` 再 `OleFlushClipboard`。无效——`OleFlushClipboard` 只对"本进程自己放进剪贴板的数据"有意义，对别人拥有的剪贴板是空操作。**也否决了"加重试"**：这不是瞬时竞争，是语义错误，重试治不了。

**决策**：`EnumClipboardFormats` 逐格式快照。HGLOBAL 类格式拷字节；`CF_BITMAP` 用 `CopyImage` 复制一份（它不是 HGLOBAL）；还原时 `EmptyClipboard` + `SetClipboardData` 逐个写回。

**后果**：
- ✅ 无代理，行为确定；实测 6/7 失败 → 0
- ✅ 顺带修掉一个分类 bug：`CF_METAFILEPICT` / `CF_DSPMETAFILEPICT` 常被误当 GDI 句柄，其实是 HGLOBAL
- ❌ 要自己维护格式分类表与体量上限（单格式 32MB / 总量 96MB）
- ❌ 非 HGLOBAL 的格式（`CF_PALETTE` / `CF_ENHMETAFILE`）仍无法还原

---

## D5 剪贴板优先，MSAA 兜底（顺序不可反）

**背景**：两种手段都能"拿到文字"，但拿到的是不同的东西。

**决策**：先剪贴板，失败再 MSAA。

**为什么不能反过来**：**MSAA 没有选区 API**。`AccessibleObjectFromPoint` 给你的是"鼠标点所在的那个元素/词/行"。把它放在前面，会让浏览器/编辑器里本来很准的"精确选区"退化成"这一行"——把已经对的行为弄坏。

**后果**：
- ✅ 主流场景（浏览器 / 编辑器）保持精确选区
- ✅ Ctrl+C 不是复制的场景（VS Code 集成终端）由 MSAA 补位
- ❌ 即使在 MSAA 能work的场景，也要先付一次 Ctrl+C 的副作用代价

---

## D6 先 MSAA，后 UIA

**背景**：UIA `TextPattern` 才是"划词"的完全体（精确选区 + **选区矩形**）。但有道用的是 MSAA，且 Go 生态没有可用的 UIA 绑定。

**决策**：先 MSAA（本阶段已做），UIA 留作下一步。

**理由**：`IAccessible` 是 **IDispatch 派生**——vtable 前 7 槽是 IUnknown + IDispatch，后 21 槽是 `acc*` 方法，顺序自 Win95 起固定。按序号取函数指针直接调即可，**完全不用实现 `IDispatch::Invoke` 的参数封送**。

UIA 的 `IUIAutomationTextPattern` / `IUIAutomationTextRange` / `IUIAutomationTextRangeArray` 三个接口都要手写 vtable，方法更多，**顺序抄错一位就跳到错误的函数地址上，直接崩**。成本差一个量级。

**后果**：
- ✅ 低成本拿到"VS Code 终端"这类场景
- ❌ MSAA 拿不到选区矩形 → 弹窗只能猜鼠标点
- ⚠️ 已被评审确认：**当前结构下接 UIA 不是"加一个实现"，而是要第 4 次、紧接着第 5 次改 `captureSelection`**。所以 UIA 之前必须先做 `TextSource` 抽象（见 `design-debt.md` 第 1 项）

---

## D7 真控制台：剪贴板禁用，但 UIA 照试

**背景**：cmd / PowerShell / Windows Terminal 里取文。

**尝试过并否决**：
- **发 Ctrl+C**：控制台里 Ctrl+C 是**中断**不是复制，会被透传给 shell，把用户正在跑的命令打断。
  这个副作用比"取不到词"严重得多。→ 直接否决。
- **用 MSAA 兜底**：实测返回的是**控制台窗口自身的名字/整块缓冲区**，不是用户拖选的选区
  （用户实测反馈"能弹出但文本不对"）。**弹一个内容不对的菜单比不弹更糟**。→ 否决。

**第一版结论（已被推翻）**：前台是控制台窗口时什么都不做。
第一版把**所有**手段一起关掉了，这是过度收缩。

**修正后的决策**：按「有没有副作用」分而治之，而不是按「是不是控制台」一刀切。

| 手段 | 控制台里 | 理由 |
|---|---|---|
| 剪贴板 | ❌ 禁用 | 会发 Ctrl+C = 中断用户的命令 |
| MSAA | ❌ 禁用 | 返回窗口自身信息而非选区 |
| **UIA** | ✅ **照试** | **只读**，零副作用；试一下代价为零 |

能这么分，是因为三者的副作用性质不同 —— 之前的错误是拿"环境"当判据，
而正确的判据是"这个手段在这个环境里会不会造成伤害"。

**实测结果**（Windows Terminal 里跑 `ping -t` 然后划选输出）：

```
drag @(1494,794)  →  来源=uia  17 字  "字节=32 时间<1ms TTL="   耗时 64ms
```

控制台通了，而且因为走的是 UIA，那次划词**根本没发 Ctrl+C** —— ping 从原理上就不可能被打断。

**守门**：先问 provider `GetSupportedTextSelection`，返回 `None(0)` 就直接放弃，
避免重演 MSAA 那次"弹出内容不对的菜单"。

**实测补充（关于「不抢焦点」）**：在 VS Code 编辑区 / VS Code 终端 / 浏览器三处各划一次，
日志里的 `[focus] 菜单显示后前台窗口仍是 …（未抢焦点）` 三处全部成立；
更早一轮 13 次划词也是 13 次未抢焦点。**终端里选区消失不是抢焦点造成的**
—— 终端在 Ctrl+C 复制之后自己清掉了选区（编辑器复制后则保留高亮，
这就是"只有终端有问题"的原因）。

> 这也是为什么日志要带**窗口标题**而不只是类名：VS Code、Chrome、Edge、Slack 的窗口类名
> 全都是 `Chrome_WidgetWin_1`，光看类名根本分不出这次划词发生在哪个程序里。

**仍然没做的**：控制台 API（`AttachConsole` → `GetConsoleSelectionInfo` →
`ReadConsoleOutputCharacterW`）。UIA 已经把这条覆盖了，除非遇到 UIA 读不到的终端才需要。

## D8 绝不抢焦点

**背景**：弹出菜单时如果激活窗口，源程序的**选区高亮会消失、选区也可能被清掉**，之后的 Ctrl+C 就复制不到东西了——等于把取文的前提毁掉。

**决策**：
- 窗口样式 `WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW | WS_EX_TOPMOST | WS_EX_LAYERED`
- 显示一律 `SetWindowPos(..., SWP_NOACTIVATE | SWP_SHOWWINDOW)`，**绝不用 `ShowWindow(SW_SHOW)`**（`ShowWindow` 没法带 `SWP_NOACTIVATE`）

**后果**：
- ✅ 源程序选区保持
- ❌ 窗口拿不到键盘焦点 → 菜单上的快捷键（`Ctrl+C` / `Enter` / `Ctrl+T`）**目前只是展示，没有接线**。要接线只能上键盘钩子

---

## D9 构建戳 + 版本自证（否决 md5 比对）

**背景**：真踩过——打完补丁只 build 到临时文件，却把旧的 `NexusKB.exe` 部署了出去，用户测的全程是旧版。而我当时的校验是「源文件与目标文件 md5 一致」。

**为什么那个校验无效**：它只证明"复制没出错"，**证明不了"源文件是新的"**。校验了一个错的不变量。

**决策**：
1. `build.sh` 每次注入时间戳到 `main.buildStamp`，构建后 `grep` 二进制确认戳真的写进去了，不一致就 `exit 1`
2. 提供 `-version`，让**部署后的产物自报身份**
3. 启动日志第一行就打构建戳

**后果**：
- ✅ "部署的是哪个版本"变成可机械回答的问题
- ✅ 启动日志里能看到构建戳，排查时不用再猜
- ⚠️ 仍然依赖"部署时记得比对"——没有做成自动闸门

---

## D10 窗口留白从阴影参数反推

**背景**：窗口必须比可见卡片大出投影的扩散距离，否则阴影被窗口边界**硬切出一条直边**。

**症状**：留白写死 18px，而实际投影扩散需要 21.4px（`blur=12, dy=5`）。球体下方 alpha 到 48 就断崖——肉眼可见。

**决策**：`shadowReach(blur, dy) = blur*1.2 + |dy| + 2`，`menuShadowPad()` / `ballShadowPad()` 都从它反推。

**为什么是 1.2×blur**：σ = blur/2，`d = 1.2·blur` 时高斯权重已降到 `exp(-2.88) ≈ 5.6%`，再乘阴影自身 alpha 后不可见。1.5× 是浪费窗口面积。

**后果**：✅ 留白与阴影参数永远同步，有测试守着（`TestWindowPadCoversShadow`）

---

## D11 文字用灰度抗锯齿 + 亮度当覆盖率

**背景**：`UpdateLayeredWindow` 用**预乘 alpha**，而 **GDI 往 32bpp DIB 上画东西时不写 alpha 字节**（一直是 0）。直接 `DrawTextW` 画字 → alpha=0 → **一个字都看不见**。

**决策**：文字走"黑字白底渲染到 scratch → 用亮度反相当覆盖率 → 手动合成"。

**因此**：字体质量必须是 `ANTIALIASED_QUALITY`（灰度抗锯齿），**不能用 ClearType** —— ClearType 会产生彩色子像素，直接破坏"亮度当覆盖率"这个前提。

**被否决**：给 GDI 画过的像素做 alpha 后处理（无法区分背景与文字）；用 GDI+ 直接支持 alpha（见 D3）。

**后果**：✅ 抗锯齿文字 + 正确 alpha；遮罩按 `(文本, 尺寸, 字体)` 缓存 ✅
❌ 每个新字符串首次显示要建一个 DIB；缓存无上限（实际字符串集合有界，安全但不显式）
