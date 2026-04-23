import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "Clickr - 桌面AI助手",
  description: "跨平台桌面AI助手，通过快捷键快速调用AI功能",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="zh-CN">
      <body
        className={`${geistSans.variable} ${geistMono.variable} antialiased`}
      >
        {/* Electron 初始化脚本 - 仅在 Electron 环境中执行 */}
        <script
          dangerouslySetInnerHTML={{
            __html: `
              (function() {
                // 检测 Electron 环境并初始化事件监听
                if (typeof window !== 'undefined' && window.electronAPI) {
                  // 监听显示问答窗口事件
                  window.electronAPI.onShowQA((text) => {
                    const inputEl = document.getElementById('inputText');
                    if (inputEl && text) {
                      inputEl.value = text;
                      // 触发 input 事件更新 React state
                      inputEl.dispatchEvent(new Event('input', { bubbles: true }));
                    }
                  });

                  // 监听显示设置事件
                  window.electronAPI.onShowSettings(() => {
                    const settingsBtn = document.getElementById('settings-btn');
                    if (settingsBtn) {
                      settingsBtn.click();
                    }
                  });

                  // 监听剪贴板变化（可选）
                  // window.electronAPI.onClipboardChange((text) => {});
                }
              })();
            `,
          }}
        />
        {children}
      </body>
    </html>
  );
}
