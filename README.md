# Clickr - 桌面AI助手

跨平台桌面AI助手，通过快捷键快速调用AI功能。

## 功能

| 功能 | 快捷键 | 说明 |
|------|--------|------|
| 问答 | `Ctrl+Shift+Space` | 呼出问答窗口 |
| 翻译 | `Ctrl+Shift+T` | 翻译剪贴板内容，自动复制 |
| 生成 | `Ctrl+Shift+G` | AI生成内容，自动复制 |

## 托盘操作

- 左键点击：问答窗口
- 右键菜单：功能选择
- 双击：翻译

## 配置

首次使用需配置API Key，在设置中填写：
- OpenAI API Key
- API Endpoint (可选，默认 OpenAI)
- Model (可选，默认 gpt-3.5-turbo)

## 开发

```bash
# 安装依赖
npm install

# 开发运行
npm start

# 构建EXE
npm run build
```

## 自动构建

推送到GitHub后自动构建EXE：
- Release标签触发正式版本发布
- 自动生成Portable EXE

## 许可证

MIT