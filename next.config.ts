import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // 静态导出模式 - 生成 .next/static 和 HTML 文件
  // 适用于 GitHub Pages、Cloudflare Pages 等静态托管
  output: 'export',
  // GitHub Pages 需要 trailing slash
  trailingSlash: true,
  // 图片优化：使用 Next.js Image 组件
  images: {
    unoptimized: true, // 静态导出时不进行图片优化
  },
};

export default nextConfig;
