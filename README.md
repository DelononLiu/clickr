# Clickr - 跨平台 AI 助手

🐦 **Web 版本**: [https://clickr.kiloapps.io](https://clickr.kiloapps.io) | 💻 **桌面版**: Electron (Windows/macOS/Linux)

一个现代化的 AI 助手应用，支持 **Web 浏览器** 和 **Electron 桌面端** 双端运行。通过快捷键快速调用 AI 进行翻译、生成、问答等操作。

---

## ✨ 功能特性

- 🌐 **双端统一** - 同一套代码运行在 Web 和桌面环境
- ⚡ **快捷键** - 全局热键快速唤起（桌面版）
- 📋 **剪贴板集成** - 自动读写剪贴板内容
- 🤖 **AI 对话** - 支持翻译、生成、总结、解释
- ⚙️ **灵活配置** - 自定义 API Key、Endpoint、模型
- 🎨 **现代 UI** - 深色主题，响应式设计
- 📦 **轻量级** - 基于 Next.js 16 + React 19 + Tailwind CSS 4

---

## 🚀 快速开始

### Web 版本（推荐）

直接在浏览器中使用，无需安装：

```bash
# 启动开发服务器
npm run dev:web
# 打开 http://localhost:3000
```

或者访问在线版本：https://clickr.kiloapps.io

### Electron 桌面版

提供系统托盘、全局快捷键等原生功能：

```bash
# 安装依赖
npm install

# 开发运行（桌面版）
npm start

# 构建 Windows Portable EXE
npm run build
```

---

## 📁 项目结构

```
clickr/
├── src/
│   ├── app/              # Next.js Web 版本（App Router）
│   │   ├── layout.tsx    # 根布局
│   │   ├── page.tsx      # 主页面
│   │   └── globals.css   # 全局样式
│   ├── main/             # Electron 主进程
│   │   └── index.js      # 桌面版入口
│   ├── renderer/         # Electron 渲染进程
│   │   └── index.html    # 桌面版 UI（备用）
│   ├── lib/env.ts        # 环境检测（Electron/Web）
│   └── hooks/useConfig.ts # 配置管理
├── package.json          # 依赖与脚本（兼容双端）
├── next.config.ts        # Next.js 配置
├── tsconfig.json         # TypeScript 配置
├── postcss.config.mjs    # Tailwind CSS 配置
├── eslint.config.mjs     # ESLint 配置
└── .kilo/               # Kilo Deploy 配置
```

---

## 🛠️ 技术栈

| 技术 | 用途 | 版本 |
|------|------|------|
| **Next.js** | Web 框架 (App Router) | 16.x |
| **React** | UI 库 | 19.x |
| **TypeScript** | 类型安全 | 5.9.x |
| **Tailwind CSS** | 样式系统 | 4.x |
| **Electron** | 桌面端框架 | 41.x |
| **Bun** | 包管理器（可选） | 1.x |

---

## 📖 配置说明

### 环境变量

创建 `.env.local` 文件（可选）：

```bash
# OpenAI 配置
OPENAI_API_KEY=sk-your-api-key
OPENAI_BASE_URL=https://api.openai.com/v1
OPENAI_MODEL=gpt-3.5-turbo

# 应用模式（可选：auto/web/electron）
CLICKR_MODE=auto
```

### 应用设置

首次运行需在设置界面配置：
- **API Key** - OpenAI 格式
- **API Endpoint** - 默认 `https://api.openai.com/v1`（支持第三方中转）
- **Model** - 默认 `gpt-3.5-turbo`

---

## 🎯 使用说明

### Web 版本

1. 打开浏览器访问应用
2. 在设置中配置 API Key
3. 输入文本，点击按钮执行 AI 操作
4. 结果自动复制到剪贴板

### Electron 桌面版

1. 运行 `npm start` 启动应用
2. 系统托盘会出现图标
3. 使用快捷键唤醒：
   - `Ctrl+Shift+Space` - 问答
   - `Ctrl+Shift+T` - 翻译
   - `Ctrl+Shift+G` - 生成
4. 右键托盘图标打开菜单

---

## 🧪 开发命令

```bash
# 安装依赖
npm install

# Web 开发服务器（Next.js）
npm run dev:web
# → http://localhost:3000

# Electron 桌面开发
npm start

# 类型检查
npm run typecheck

# 代码检查
npm run lint

# 构建 Web 版本（静态 export）
npm run build:web

# 构建 Electron EXE（Windows）
npm run build
```

---

## 🚢 部署

### Kilo Deploy（推荐）

本项目已配置 Kilo 自动部署：

1. 推送到 GitHub
2. Kilo 自动检测为 Next.js 项目
3. 构建并部署到 Kilo 托管平台
4. 获得 HTTPS URL（如 `https://clickr.kiloapps.io`）

**当前在线版本**: https://clickr-5058.d.kiloapps.io

### 其他平台

```yaml
# Cloudflare Pages
build: npm run build:web
output: .next

# Vercel
# 自动检测 Next.js，一键部署

# Netlify
build: npm run build:web
publish: .next
```

---

## 🔧 开发说明

### 双端适配原理

应用通过 `src/lib/env.ts` 检测运行环境：

```ts
const isElectron = () => {
  return typeof window !== 'undefined' &&
    (window as any).electronAPI &&
    typeof (window as any).electronAPI?.getConfig === 'function';
};
```

- **Electron 模式**：使用 `electronAPI` 读写配置、剪贴板
- **Web 模式**：使用 `localStorage` + Clipboard API

### 组件结构

- **`useConfig()`** - 配置状态管理（双端自动适配）
- **`useClipboard()`** - 剪贴板操作（双端自动适配）
- **layout.tsx** - 注入 Electron 事件监听脚本
- **page.tsx** - 主页面逻辑（纯 React）

---

## 📝 更新日志

### 2026-04-24
- ✅ 迁移到 Next.js 16 架构
- ✅ 支持 Web + Electron 双端
- ✅ 部署到 Kilo 平台
- ✅ 添加环境检测适配器
- ✅ 保持原有 Electron 代码不变

---

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

## 📄 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件

---

**Made with ❤️ by Clickr Team**
