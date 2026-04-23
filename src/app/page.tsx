'use client';

import { useState, useEffect } from 'react';
import { isElectron, useConfig, useClipboard } from '@/lib/env';

export default function Home() {
  const [inputText, setInputText] = useState('');
  const [output, setOutput] = useState('');
  const [showSettings, setShowSettings] = useState(false);
  const [loading, setLoading] = useState(false);
  const { config, saveConfig } = useConfig();
  const { readText, writeText } = useClipboard();

  // 初始化时读取剪贴板
  useEffect(() => {
    const init = async () => {
      const text = await readText();
      if (text) setInputText(text);
    };
    init();
  }, []);

  const callAI = async (prompt: string) => {
    if (!inputText.trim()) {
      setOutput('请输入内容');
      return;
    }

    setLoading(true);
    setOutput('处理中...');

    try {
      const response = await fetch(`${config.openai_base_url}/chat/completions`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${config.openai_api_key}`,
        },
        body: JSON.stringify({
          model: config.openai_model,
          messages: [{ role: 'user', content: `${prompt}\n\n${inputText}` }],
          temperature: 0.7,
        }),
      });

      const data = await response.json();

      if (data.choices && data.choices[0]) {
        const result = data.choices[0].message.content;
        setOutput(result);
        await writeText(result);
      } else {
        setOutput(`错误: ${JSON.stringify(data)}`);
      }
    } catch (error: any) {
      setOutput(`请求失败: ${error.message}`);
    } finally {
      setLoading(false);
    }
  };

  const handleTranslate = () => callAI('请将以下内容翻译成中文:');
  const handleGenerate = () => callAI('请根据以下内容生成回复:');
  const handleSummarize = () => callAI('请简洁总结以下内容:');
  const handleExplain = () => callAI('请详细解释以下内容:');

  const handleSaveSettings = async () => {
    await saveConfig(config);
    setShowSettings(false);
  };

  if (showSettings) {
    return (
      <div className="settings active">
        <div className="input-group">
          <label>OpenAI API Key:</label>
          <input
            type="password"
            value={config.openai_api_key}
            onChange={(e) => useConfig().saveConfig({ ...config, openai_api_key: e.target.value })}
            placeholder="sk-..."
          />
        </div>

        <div className="input-group">
          <label>API Endpoint:</label>
          <input
            type="text"
            value={config.openai_base_url}
            onChange={(e) => useConfig().saveConfig({ ...config, openai_base_url: e.target.value })}
            placeholder="https://api.openai.com/v1"
          />
        </div>

        <div className="input-group">
          <label>Model:</label>
          <input
            type="text"
            value={config.openai_model}
            onChange={(e) => useConfig().saveConfig({ ...config, openai_model: e.target.value })}
            placeholder="gpt-3.5-turbo"
          />
        </div>

        <div className="actions">
          <button className="action-btn primary" onClick={handleSaveSettings}>
            保存设置
          </button>
          <button
            className="action-btn"
            onClick={() => setShowSettings(false)}
          >
            取消
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="main-view active">
      <div className="input-section">
        <label>输入内容:</label>
        <textarea
          id="inputText"
          value={inputText}
          onChange={(e) => setInputText(e.target.value)}
          placeholder="选中文本或直接输入..."
        />
      </div>

      <div className="actions">
        <button
          className="action-btn translate"
          onClick={handleTranslate}
          disabled={loading}
        >
          🌐 翻译
        </button>
        <button
          className="action-btn generate"
          onClick={handleGenerate}
          disabled={loading}
        >
          ✨ 生成
        </button>
        <button
          className="action-btn"
          onClick={handleSummarize}
          disabled={loading}
        >
          📝 总结
        </button>
        <button
          className="action-btn"
          onClick={handleExplain}
          disabled={loading}
        >
          💡 解释
        </button>
      </div>

      <div className="output-section">
        <label>AI 回复:</label>
        <div className={`output ${loading ? 'loading' : ''}`} id="output">
          {output}
        </div>
      </div>

      <div className="hint">
        提示：结果会自动复制到剪贴板
        {!isElectron() && '（Web 版本）'}
      </div>

      {/* Electron 设置按钮（Web 版本隐藏） */}
      {isElectron() && (
        <button
          id="settings-btn"
          onClick={() => setShowSettings(true)}
          style={{
            position: 'fixed',
            top: 12,
            right: 12,
            background: 'none',
            border: 'none',
            color: '#888',
            cursor: 'pointer',
            fontSize: '18px',
            zIndex: 1000,
          }}
        >
          ⚙
        </button>
      )}
    </div>
  );
}
