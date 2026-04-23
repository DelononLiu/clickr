'use client';

import { useState, useEffect } from 'react';

const isElectron = () => {
  return typeof window !== 'undefined' &&
    (window as any).electronAPI &&
    typeof (window as any).electronAPI?.getConfig === 'function';
};

export function useConfig() {
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
      try {
        const cfg = await (window as any).electronAPI.getConfig();
        setConfig(cfg);
      } catch (e) {
        console.error('Failed to load config from Electron:', e);
      }
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
      try {
        await (window as any).electronAPI.saveConfig(newConfig);
      } catch (e) {
        console.error('Failed to save config to Electron:', e);
      }
    } else {
      localStorage.setItem('clickr_config', JSON.stringify(newConfig));
    }
    setConfig(newConfig);
  };

  return { config, saveConfig, reload: loadConfig };
}
