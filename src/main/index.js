const { app, BrowserWindow, globalShortcut, Tray, Menu, ipcMain, nativeImage, clipboard } = require('electron');
const path = require('path');
const fs = require('fs');

let mainWindow = null;
let tray = null;
let isQuitting = false;

const CONFIG_PATH = path.join(app.getPath('userData'), 'config.json');

function getConfig() {
  const defaultConfig = {
    openai_api_key: process.env.OPENAI_API_KEY || '',
    openai_base_url: process.env.OPENAI_BASE_URL || 'https://api.openai.com/v1',
    openai_model: process.env.OPENAI_MODEL || 'gpt-3.5-turbo',
    hotkey: 'CommandOrControl+Shift+Space',
    translate_hotkey: 'CommandOrControl+Shift+T',
    generate_hotkey: 'CommandOrControl+Shift+G'
  };
  
  try {
    if (fs.existsSync(CONFIG_PATH)) {
      const userConfig = JSON.parse(fs.readFileSync(CONFIG_PATH, 'utf-8'));
      return { ...defaultConfig, ...userConfig };
    }
  } catch (e) {
    console.error('Failed to load config:', e);
  }
  return defaultConfig;
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 400,
    height: 500,
    show: false,
    frame: true,
    resizable: true,
    alwaysOnTop: true,
    skipTaskbar: false,
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true,
      preload: path.join(__dirname, 'preload.js')
    }
  });

  mainWindow.loadFile(path.join(__dirname, '../renderer/index.html'));

  mainWindow.on('close', (event) => {
    if (!isQuitting) {
      event.preventDefault();
      mainWindow.hide();
    }
  });

  mainWindow.on('blur', () => {
    if (!mainWindow.isFocused()) {
      mainWindow.hide();
    }
  });
}

function createTray() {
  const iconPath = path.join(__dirname, '../build/icon.png');
  let icon;
  
  if (fs.existsSync(iconPath)) {
    icon = nativeImage.createFromPath(iconPath);
  } else {
    icon = nativeImage.createFromDataURL('data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAABHNCSVQICAgIfAhkiAAAAAlwSFlzAAAA7AAAAOwBeShxvQAAABl0RVh0U29mdHdhcmUAd3d3Lmlua3NjYXBlLm9yZ5vuPBoAAAGlSURBVFiF7ZY9TsNAEIW/WZuQgoICiQpyHFyCG3ALcgNyC+5AAYkFvoASL0BOQJFIQYGChITYeBMTezZr16+dxC4WSTb2afbPm9mdnYWFhYX/G2U5Y/3/8IuA4wDJJGIvIjYCb0Xk/8I/FxE3gMv4r0PvBaRWwF3Yj4WkV0R2QnYqSLyJiKXIvIiIq8BLiOXIvJdRN6KyJuIuIqsiMh3EXkfYNdFZK+I7AW8EpG9ALsSsCsici0iH0TkfYCLInIY8FRE9gNsiMh7EXkfYNdFZK+I7AW8EpG9ALsSsCsici0iH0TkfYCLInIY8FRE9gNsiMh7EXkfYNdFZK/I7AW8EpG9ALsSsCsici0iH0TkfYCLInIY8FRE9gNsiMh7EXkfYNdFZK/I7AW8EpG9ALsSsCsici0iH0TkfYCLInIY8FRE9gNsiMh7EXkfYNdFZK/I7AW8EpG9ALsSsCsici0iH0TkfYD1EXkjYH+AzFsCkrgKjP8CqD8a/AtY/gUIIpJY/gUIIpJHJLEED/h/E0CJJVBAAVJYAgUU8LcooIB/gAIKaKGA/+2igAIWV1xxySX/AhT4C1BcMRJZ/gUIIpJYHv8Fh/8rWCx+AYoklhD+C/gXwD+5+A8A8z/GvwBBSCLJvmX5F/A/hMRq7/kXoMj/A/z7Y/4F/AuQxLIv+J+QWA39/xdA/A1ILHv+C/gXsCxBZYvkXIIiIZJky/AhSLS/4FLBaX/AuQ+AtQXA2JLCH8F6BIYvkXsCxIEpLE8i9AET8B+AMxGZ3J9dZ5kwAAAAASUVORK5CYII=');
  }
  
  try {
    tray = new Tray(icon.resize({ width: 16, height: 16 }));
  } catch (e) {
    console.error('Failed to create tray:', e);
    return;
  }
  
  const contextMenu = Menu.buildFromTemplate([
    { label: '💬 问答 (Ctrl+Shift+Space)', click: () => showQA() },
    { label: '🌐 翻译 (Ctrl+Shift+T)', click: () => translateSelected() },
    { label: '✨ 生成内容 (Ctrl+Shift+G)', click: () => generateContent() },
    { type: 'separator' },
    { label: '⚙ 设置', click: () => showSettings() },
    { type: 'separator' },
    { label: '❌ 退出', click: () => { isQuitting = true; app.quit(); } }
  ]);
  
  tray.setToolTip('Clickr - 桌面AI助手');
  tray.setContextMenu(contextMenu);
  
  tray.on('click', () => {
    showQA();
  });
  
  tray.on('double-click', () => {
    translateSelected();
  });
}

function showQA() {
  const text = clipboard.readText();
  mainWindow.webContents.send('show-qa', text);
  mainWindow.show();
  mainWindow.focus();
}

function showSettings() {
  mainWindow.show();
  mainWindow.webContents.send('show-settings');
}

async function translateSelected() {
  const text = clipboard.readText();
  if (!text) return;
  
  const config = getConfig();
  if (!config.openai_api_key) {
    clipboard.writeText('请先配置API Key');
    return;
  }
  
  try {
    const response = await fetch(`${config.openai_base_url}/chat/completions`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${config.openai_api_key}`
      },
      body: JSON.stringify({
        model: config.openai_model,
        messages: [{ role: 'user', content: `请将以下内容翻译成中文:\n\n${text}` }],
        temperature: 0.7
      })
    });
    
    const data = await response.json();
    if (data.choices && data.choices[0]) {
      clipboard.writeText(data.choices[0].message.content);
      showNotification('翻译完成', '结果已复制到剪贴板');
    }
  } catch (e) {
    console.error('Translate error:', e);
    clipboard.writeText('翻译失败: ' + e.message);
  }
}

async function generateContent() {
  const text = clipboard.readText();
  if (!text) return;
  
  const config = getConfig();
  if (!config.openai_api_key) {
    clipboard.writeText('请先配置API Key');
    return;
  }
  
  try {
    const response = await fetch(`${config.openai_base_url}/chat/completions`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${config.openai_api_key}`
      },
      body: JSON.stringify({
        model: config.openai_model,
        messages: [{ role: 'user', content: `请根据以下内容生成回复:\n\n${text}` }],
        temperature: 0.7
      })
    });
    
    const data = await response.json();
    if (data.choices && data.choices[0]) {
      clipboard.writeText(data.choices[0].message.content);
      showNotification('生成完成', '结果已复制到剪贴板');
    }
  } catch (e) {
    console.error('Generate error:', e);
    clipboard.writeText('生成失败: ' + e.message);
  }
}

function showNotification(title, body) {
  const { Notification } = require('electron');
  if (Notification.isSupported()) {
    new Notification({ title, body }).show();
  }
}

function registerHotkeys() {
  const config = getConfig();
  
  globalShortcut.unregisterAll();
  
  globalShortcut.register(config.hotkey, () => showQA());
  globalShortcut.register(config.translate_hotkey, () => translateSelected());
  globalShortcut.register(config.generate_hotkey, () => generateContent());
  
  console.log('Hotkeys registered:', config.hotkey, config.translate_hotkey, config.generate_hotkey);
}

const { ipcMain } = require('electron');

ipcMain.handle('get-selected-text', () => clipboard.readText());
ipcMain.handle('set-selected-text', (_, text) => clipboard.writeText(text));
ipcMain.handle('get-config', () => getConfig());
ipcMain.handle('save-config', (_, config) => {
  try {
    fs.writeFileSync(CONFIG_PATH, JSON.stringify(config, null, 2));
    registerHotkeys();
    return true;
  } catch (e) {
    console.error('Failed to save config:', e);
    return false;
  }
});
ipcMain.handle('hide-window', () => mainWindow.hide());
ipcMain.handle('translate', async (_, text) => {
  const config = getConfig();
  const response = await fetch(`${config.openai_base_url}/chat/completions`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${config.openai_api_key}`
    },
    body: JSON.stringify({
      model: config.openai_model,
      messages: [{ role: 'user', content: `请将以下内容翻译成中文:\n\n${text}` }],
      temperature: 0.7
    })
  });
  const data = await response.json();
  return data.choices?.[0]?.message?.content || '';
});
ipcMain.handle('generate', async (_, text) => {
  const config = getConfig();
  const response = await fetch(`${config.openai_base_url}/chat/completions`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${config.openai_api_key}`
    },
    body: JSON.stringify({
      model: config.openai_model,
      messages: [{ role: 'user', content: text }],
      temperature: 0.7
    })
  });
  const data = await response.json();
  return data.choices?.[0]?.message?.content || '';
});

app.whenReady().then(() => {
  createWindow();
  createTray();
  registerHotkeys();
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});

app.on('will-quit', () => globalShortcut.unregisterAll());
app.on('before-quit', () => isQuitting = true);