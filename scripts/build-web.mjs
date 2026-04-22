#!/usr/bin/env node
/**
 * Web 版本构建脚本 - 纯 Node.js 实现
 * 无需 Bun，兼容 Cloudflare、Vercel、Netlify 等所有平台
 */

import { resolve, dirname } from "path";
import { fileURLToPath } from "url";
import { existsSync, unlinkSync, rmdirSync, mkdirSync, readdirSync, copyFileSync } from "fs";

const __dirname = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(__dirname, "..");
const distDir = resolve(rootDir, "public");

// 清理旧构建
if (existsSync(distDir)) {
  const files = readdirSync(distDir);
  for (const file of files) {
    unlinkSync(resolve(distDir, file));
  }
  rmdirSync(distDir);
  console.log("✅ 清理旧构建目录");
}

// 创建输出目录
mkdirSync(distDir, { recursive: true });
console.log("✅ 创建输出目录:", distDir);

// 复制主页面
const srcHtml = resolve(rootDir, "src/renderer/index.html");
const destHtml = resolve(distDir, "index.html");
copyFileSync(srcHtml, destHtml);
console.log("✅ 复制 index.html");

// 复制静态资源（图标等）
const srcPublic = resolve(rootDir, "public");
try {
  const entries = readdirSync(srcPublic);
  for (const entry of entries) {
    copyFileSync(resolve(srcPublic, entry), resolve(distDir, entry));
    console.log(`✅ 复制 ${entry}`);
  }
} catch (e) {
  // public 目录不存在，忽略
}

console.log("\n🎉 Web 版本构建完成！");
