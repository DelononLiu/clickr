#!/usr/bin/env bun
/**
 * Web 版本构建脚本
 * 将应用打包为静态文件，可部署到任何静态托管平台
 */

import { write, mkdir } from "bun";
import { resolve, dirname } from "path";
import { fileURLToPath } from "url";
import { existsSync, unlinkSync, rmdirSync, mkdirSync, readdirSync, copyFileSync } from "fs";

const __dirname = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(__dirname, "..");
const distDir = resolve(rootDir, "public");

// 清理旧构建
try {
  if (existsSync(distDir)) {
    // 删除目录内所有文件
    const files = readdirSync(distDir);
    for (const file of files) {
      unlinkSync(resolve(distDir, file));
    }
    rmdirSync(distDir);
    console.log("✅ 清理旧构建目录");
  }
} catch (e) {
  // 目录不存在，忽略
}

// 创建输出目录
mkdirSync(distDir, { recursive: true });
console.log("✅ 创建输出目录:", distDir);

// 复制主页面
const srcHtml = resolve(rootDir, "src/renderer/index.html");
const destHtml = resolve(distDir, "index.html");
copyFileSync(srcHtml, destHtml);
console.log("✅ 复制 index.html");

// 可选：复制其他静态资源（如图标）
const srcPublic = resolve(rootDir, "public");
try {
  const entries = readdirSync(srcPublic);
  for (const entry of entries) {
    copyFileSync(
      resolve(srcPublic, entry),
      resolve(distDir, entry)
    );
    console.log(`✅ 复制 ${entry}`);
  }
} catch (e) {
  // public 目录不存在，忽略
}

console.log("\n🎉 Web 版本构建完成！");
console.log("📁 输出目录:", distDir);
console.log("📤 可将 public/ 目录部署到:");
console.log("   - Cloudflare Pages");
console.log("   - Vercel");
console.log("   - Netlify");
console.log("   - GitHub Pages");
console.log("   - 任何静态文件托管服务");
