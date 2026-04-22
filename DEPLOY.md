# Clickr Web 版本

本项目为 Clickr 桌面的 Web 调试版本，可直接在浏览器中运行。

## 快速开始

```bash
# 安装依赖
bun install

# 启动开发服务器
bun web
# 打开 http://localhost:3000
```

## 部署到 Cloudflare Pages

### 方式一：通过 Git 自动部署（推荐）

1. 将项目推送到 GitHub/GitLab
2. 登录 Cloudflare Dashboard → Pages
3. 连接仓库，配置：
   - **构建命令**: `bun install && bun run build:web`
   - **输出目录**: `public`
   - **构建环境变量**: `NODE_VERSION=20`

### 方式二：手动上传

```bash
# 构建静态文件
bun run build:web

# 整个 public/ 目录上传到 Cloudflare Pages
```

## 项目结构

```
clickr/
├── src/
│   ├── renderer/
│   │   └── index.html      # 主应用（Electron + Web 兼容）
│   └── web/
│       └── server.ts       # Web 开发服务器
├── package.json
├── bun.lock
└── public/                 # 静态资源（部署后）
```

## 功能特性

- ✅ 跨平台：Web 浏览器 + Electron 桌面端
- ✅ 自适应：移动端友好
- ✅ 离线可用：配置存储在 localStorage
- ✅ 剪贴板集成：使用 Clipboard API
- ✅ AI 对话：翻译、生成、总结、解释

## 环境变量（可选）

在 Cloudflare Pages 设置环境变量：

| 变量名 | 说明 | 默认值 |
|--------|------|--------|
| `OPENAI_API_KEY` | OpenAI API Key | - |
| `OPENAI_BASE_URL` | API 端点 | `https://api.openai.com/v1` |
| `OPENAI_MODEL` | 模型名称 | `gpt-3.5-turbo` |

## 技术栈

- **运行时**: Bun (超快 JavaScript 运行时)
- **前端**: 原生 HTML + CSS + JavaScript（无框架）
- **后端**: Electron（桌面端）+ 静态托管（Web）
- **AI**: OpenAI API 兼容接口

## 开发说明

Web 版本使用适配器模式，自动检测运行环境：

```javascript
// 配置适配
const config = await (isElectron()
  ? window.electronAPI.getConfig()
  : localStorage.getItem('config'));
```

无需修改代码，同一套代码即可在桌面和浏览器中运行。

## License

MIT
