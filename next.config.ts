import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Electron 兼容：不使用 server components 的静态导出
  output: 'standalone',
  // 静态站点生成（SSG）
  trailingSlash: false,
};

export default nextConfig;
