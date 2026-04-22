#!/usr/bin/env bun

const PORT = process.env.PORT || 3000;
const path = require('path');

Bun.serve({
  port: PORT,
  async fetch(req) {
    const url = new URL(req.url);
    if (url.pathname === '/' || url.pathname === '/index.html') {
      const filePath = path.join(import.meta.dir, '../renderer/index.html');
      const file = Bun.file(filePath);
      return new Response(file, { headers: { 'content-type': 'text/html' } });
    }
    return new Response('Not found', { status: 404 });
  },
});

console.log(`🚀 Web dev server: http://localhost:${PORT}`);
console.log('💡 直接在浏览器中打开即可调试，无需 Electron');
