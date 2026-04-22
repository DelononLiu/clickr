const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('electronAPI', {
  getSelectedText: () => ipcRenderer.invoke('get-selected-text'),
  setSelectedText: (text) => ipcRenderer.invoke('set-selected-text', text),
  getConfig: () => ipcRenderer.invoke('get-config'),
  saveConfig: (config) => ipcRenderer.invoke('save-config', config),
  hideWindow: () => ipcRenderer.invoke('hide-window'),
  translate: (text) => ipcRenderer.invoke('translate', text),
  generate: (text) => ipcRenderer.invoke('generate', text),
  onShowQA: (callback) => ipcRenderer.on('show-qa', (_, text) => callback(text)),
  onShowSettings: (callback) => ipcRenderer.on('show-settings', () => callback())
});