'use client';

import { useState, useEffect } from 'react';

// 环境检测：Electron vs Web
const isElectron = () => {
  return typeof window !== 'undefined' &&
    (window as any).electronAPI &&
    typeof (window as any).electronAPI?.getConfig === 'function';
};

// 配置管理适配器
const useConfig = () => {
  const [config, setConfig] = useState({
    openai_api_key: '',
    openai_base_url: 'https://api.openai.com/v1',
    openai_model: 'gpt-3.5-turbo',
  });

  useEffect(() => {
    loadConfig();
  }, []);

  const loadConfig = async () => {
    if (isElectron() && (window as any).electronAPI) {
      const cfg = await (window as any).electronAPI.getConfig();
      setConfig(cfg);
    } else {
      const saved = localStorage.getItem('clickr_config');
      if (saved) {
        try {
          setConfig(JSON.parse(saved));
        } catch (e) {
          console.error('Failed to parse config:', e);
        }
      }
    }
  };

  const saveConfig = async (newConfig: typeof config) => {
    if (isElectron() && (window as any).electronAPI) {
      await (window as any).electronAPI.saveConfig(newConfig);
    } else {
      localStorage.setItem('clickr_config', JSON.stringify(newConfig));
    }
    setConfig(newConfig);
  };

  return { config, saveConfig, reload: loadConfig };
};

// 剪贴板适配器
const useClipboard = () => {
  const readText = async () => {
    if (isElectron() && (window as any).electronAPI) {
      return await (window as any).electronAPI.getSelectedText();
    } else {
      try {
        return await navigator.clipboard.readText();
      } catch (e) {
        console.error('Clipboard read failed:', e);
        return '';
      }
    }
  };

  const writeText = async (text: string) => {
    if (isElectron() && (window as any).electronAPI) {
      await (window as any).electronAPI.setSelectedText(text);
    } else {
      try {
        await navigator.clipboard.writeText(text);
      } catch (e) {
        console.error('Clipboard write failed:', e);
      }
    }
  };

  return { readText, writeText };
};

export { isElectron, useConfig, useClipboard };
