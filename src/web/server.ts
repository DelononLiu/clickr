#!/usr/bin/env bun

const PORT = process.env.PORT || 3000;
const HOST = process.env.HOST || '0.0.0.0';
const path = require('path');

Bun.serve({
  port: Number(PORT),
  hostname: HOST,
  async fetch(req) {
    const url = new URL(req.url);
    if (url.pathname === '/' || url.pathname === '/index.html') {
      const filePath = path.join(import.meta.dir, '../renderer/index.html');
      const file = Bun.file(filePath);
      return new Response(file, { headers: { 'content-type': 'text/html' } });
    }
    // 静态资源
    if (url.pathname.startsWith('/_next/') || url.pathname.match(/\.(js|css|png|jpg|jpeg|gif|ico|svg)$/)) {
      const filePath = path.join(import.meta.dir, '..', url.pathname);
      try {
        const file = Bun.file(filePath);
        return new Response(file);
      } catch (e) {
        return new Response('Not found', { status: 404 });
      }
    }
    return new Response('Not found', { status: 404 });
  },
});

console.log(`🚀 Web server running at:`);
console.log(`   Local:  http://localhost:${PORT}`);
console.log(`   Public: http://104.28.162.94:${PORT}`);
console.log('💡 按 Ctrl+C 停止');
