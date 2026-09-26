//go:build windows

// ai.go —— 临时问答：把选中的文字当问题，流式拿回答案。
//
// 设计上的几个定论（见 docs/decisions.md D16 / D17）：
//
//  1. **"划词即问题"**，不做输入框。本项目所有窗口都是"绝不抢焦点"的分层窗口（D8），
//     拿不到键盘焦点也就没有输入法 —— 打字这条路要另立门户（普通窗口 + 原生 EDIT）。
//  2. **OpenAI 兼容接口**（`POST {base}/chat/completions`，SSE 流式）。
//     这样 OpenAI / DeepSeek / 火山方舟 / Ollama / LM Studio 用的是同一段代码。
//  3. **配置读纯文本文件**（`%AppData%\clickr\ai.txt`）。密钥不进代码、不进 UI、不进日志。
//  4. **一次性**：问一次就是一次，不留历史（这就是"临时问答"与"长会话"的区别）。
//     再问一次会替换上一次，连问题带答案一起。
//
// 线程约定：`aiAsk` 在**后台 goroutine** 里跑（网络请求绝不能占 UI 线程），
// 它只往下面这个带锁的状态里写，然后 PostMessage 让主线程重画侧边栏。

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	aiTimeout     = 60 * time.Second
	aiMaxAnswerKB = 64 // 答案缓冲上限：模型跑飞了也不至于把内存吃光
)

// aiConfig 是接口配置。缺哪项都会在侧边栏里明说，而不是"点了没反应"。
type aiConfig struct {
	BaseURL   string // 例如 https://api.deepseek.com/v1
	APIKey    string
	Model     string
	System    string // 可选的系统提示词
	MaxTokens int    // 0 = 不传
}

func aiConfigPath() string {
	if d := configDir(); d != "" {
		return filepath.Join(d, "ai.txt")
	}
	return ""
}

// loadAIConfig 读配置。格式是 `键: 值` 一行一条（`#` 开头是注释）。
//
// 用这种最笨的格式是刻意的：用户要能拿记事本改，而且**改坏了要能给出明确的话**，
// 而不是一个 JSON 解析错误。
func loadAIConfig() (aiConfig, error) {
	f := aiConfigPath()
	if f == "" {
		return aiConfig{}, errors.New("拿不到配置目录")
	}
	b, err := os.ReadFile(f)
	if err != nil {
		if os.IsNotExist(err) {
			return aiConfig{}, fmt.Errorf("还没配置：请新建 %s", f)
		}
		return aiConfig{}, fmt.Errorf("读配置失败: %v", err)
	}
	return parseAIConfig(string(b), f)
}

// parseAIConfig 解析配置文本。格式是 `键: 值` 一行一条（`#` 开头是注释）。
//
// 用这种最笨的格式是刻意的：用户要能拿记事本改，而且**改坏了要能给出明确的话**，
// 而不是一个 JSON 解析错误。path 只是用来把错误信息写清楚。
func parseAIConfig(text, path string) (aiConfig, error) {
	var cfg aiConfig
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return cfg, fmt.Errorf("这一行看不懂（要 `键: 值`）：%q", line)
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "base_url", "baseurl", "url":
			cfg.BaseURL = v
		case "api_key", "apikey", "key":
			cfg.APIKey = v
		case "model":
			cfg.Model = v
		case "system":
			cfg.System = v
		case "max_tokens", "maxtokens":
			if n, err := strconv.Atoi(v); err == nil {
				cfg.MaxTokens = n
			}
		default:
			return cfg, fmt.Errorf("不认识的配置项 %q（可用的：base_url / api_key / model / system / max_tokens）", k)
		}
	}
	var missing []string
	if cfg.BaseURL == "" {
		missing = append(missing, "base_url")
	}
	if cfg.Model == "" {
		missing = append(missing, "model")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("配置缺了：%s（文件 %s）", strings.Join(missing, "、"), path)
	}
	if cfg.APIKey == "" {
		// 最常见的一种状态，单独说清楚：配置在、模型在，就差一个 key
		return cfg, fmt.Errorf("还没填 api_key（文件：%s）", path)
	}
	if !strings.HasPrefix(cfg.BaseURL, "http") {
		return cfg, fmt.Errorf("base_url 要以 http(s):// 开头：%q", cfg.BaseURL)
	}
	return cfg, nil
}

// aiConfigTemplate 是"预留好的配置"：第一次跑就把文件写好，用户只差填一个 key。
//
// 默认按 **DeepSeek** 填（用户指定的模型）。写的是注释 + 可改的值，不是写死的常量：
// 换成任何 OpenAI 兼容的服务（OpenAI / 火山方舟 / Ollama / LM Studio）都只改这三行。
const aiConfigTemplate = `# 侧边栏「问一下」（临时问答）的接口配置 —— OpenAI 兼容接口。
#
# 只差把 api_key 填上（在 https://platform.deepseek.com 申请）。
# 换别的服务：改 base_url 与 model 即可（Ollama 是 http://localhost:11434/v1）。
base_url: https://api.deepseek.com/v1
model: deepseek-flash
api_key:

# 下面两项可选
# system: 你是临时问答助手，直接给答案，不要寒暄。
# max_tokens: 1024
`

// ensureAIConfigTemplate 在文件不存在时写一份预置配置。
// 写失败只记日志，不拦启动 —— 用户可能是只读的配置目录。
func ensureAIConfigTemplate() {
	f := aiConfigPath()
	if f == "" {
		return
	}
	if _, err := os.Stat(f); err == nil {
		return // 已经有了，绝不覆盖（里面可能有用户的 key）
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		log.Printf("[ai] 建配置目录失败: %v", err)
		return
	}
	if err := os.WriteFile(f, []byte(aiConfigTemplate), 0o600); err != nil {
		log.Printf("[ai] 写预置配置失败: %v", err)
		return
	}
	log.Printf("[ai] 已生成预置配置（DeepSeek，待填 api_key）：%s", f)
}

const aiDefaultSystem = "你是临时问答助手。用户会给你一段文字，直接给出准确、简短、有用的回答；" +
	"不要寒暄，不要复述原文，不要反问。"

// ================================================================ 问答状态

// aiState 是侧边栏要显示的那点东西。跨线程：后台请求写、UI 线程读。
type aiState struct {
	mu       sync.Mutex
	question string
	answer   string
	err      string
	busy     bool
	gen      int // 每次提问 +1：过期的流只丢弃，不往界面上写
	cancel   context.CancelFunc
}

var ai aiState

type aiSnapshot struct {
	Question string
	Answer   string
	Err      string
	Busy     bool
}

func aiNow() aiSnapshot {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	return aiSnapshot{Question: ai.question, Answer: ai.answer, Err: ai.err, Busy: ai.busy}
}

// aiBegin 开一次新问答（旧的一次性问答就此作废）。
func aiBegin(question string) int {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	if ai.cancel != nil {
		ai.cancel()
		ai.cancel = nil
	}
	ai.gen++
	ai.question = question
	ai.answer = ""
	ai.err = ""
	ai.busy = true
	return ai.gen
}

func aiSetCancel(gen int, cancel context.CancelFunc) {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	if ai.gen == gen {
		ai.cancel = cancel
	}
}

func aiAppend(gen int, delta string) bool {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	if gen != ai.gen {
		return false // 过期的一次，丢弃
	}
	if len(ai.answer) < aiMaxAnswerKB*1024 {
		ai.answer += delta
	}
	return true
}

func aiFinish(gen int, err error) bool {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	if gen != ai.gen {
		return false
	}
	ai.busy = false
	ai.cancel = nil
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		ai.err = "已停止"
	case errors.Is(err, context.DeadlineExceeded):
		ai.err = fmt.Sprintf("超时（%s）", aiTimeout)
	default:
		ai.err = err.Error()
	}
	return true
}

// aiStop 用户点了「停止」，或者关了侧边栏。
func aiStop() {
	ai.mu.Lock()
	cancel := ai.cancel
	ai.cancel = nil
	if ai.busy {
		ai.busy = false
		ai.err = "已停止"
	}
	ai.gen++ // 让还在跑的流作废
	ai.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ================================================================ 请求

// chatRequest 是 OpenAI 兼容的请求体。
type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// buildChatRequest 拼请求体（纯函数，便于测试）。
func buildChatRequest(cfg aiConfig, question string) ([]byte, error) {
	system := cfg.System
	if system == "" {
		system = aiDefaultSystem
	}
	return json.Marshal(chatRequest{
		Model: cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: question},
		},
		Stream:    true,
		MaxTokens: cfg.MaxTokens,
	})
}

// parseSSELine 从一行 SSE 里取出增量文本。
//
// 形如 `data: {"choices":[{"delta":{"content":"你"}}]}`；`data: [DONE]` 表示结束。
// 取不到增量就返回 ok=false，调用方照常往下读（注释行、空行、厂商自己的字段都这样跳过）。
func parseSSELine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, ":") {
		return "", false
	}
	payload, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return "", false
	}
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return "", false
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
			Message struct { // 少数服务在流式里也给 message
				Content string `json:"content"`
			} `json:"message"`
			Text string `json:"text"` // 兼容老式 completions 风格
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return "", false
	}
	if chunk.Error != nil && chunk.Error.Message != "" {
		return "", false
	}
	for _, c := range chunk.Choices {
		if c.Delta.Content != "" {
			return c.Delta.Content, true
		}
		if c.Message.Content != "" {
			return c.Message.Content, true
		}
		if c.Text != "" {
			return c.Text, true
		}
	}
	return "", false
}

// aiAsk 发起一次临时问答。**必须在后台 goroutine 里调用**（网络请求不能占 UI 线程）。
//
// 它自己负责把结果写进 aiState 并请求重画，调用方不用管返回值。
func aiAsk(question string) {
	cfg, err := loadAIConfig()
	if err != nil {
		gen := aiBegin(question)
		aiFinish(gen, err)
		requestAIRepaint()
		return
	}
	gen := aiBegin(question)
	requestAIRepaint()

	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	aiSetCancel(gen, cancel)
	defer cancel()

	body, err := buildChatRequest(cfg, question)
	if err != nil {
		aiFinish(gen, err)
		requestAIRepaint()
		return
	}

	url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		aiFinish(gen, err)
		requestAIRepaint()
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: aiTimeout}
	resp, err := client.Do(req)
	if err != nil {
		aiFinish(gen, fmt.Errorf("连不上 %s: %v", cfg.BaseURL, err))
		requestAIRepaint()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 把服务端的话原样带出来 —— 401/404/429 的原文比我们的转述有用得多
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		detail := strings.TrimSpace(string(msg))
		aiFinish(gen, fmt.Errorf("HTTP %d：%s", resp.StatusCode, truncate(detail, 300)))
		requestAIRepaint()
		return
	}

	// 流式读：SSE 那些行可能很长，默认 64KB 的 scanner 不够
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	got := false
	for sc.Scan() {
		if delta, ok := parseSSELine(sc.Text()); ok {
			got = true
			if aiAppend(gen, delta) {
				requestAIRepaint()
			}
		}
	}
	if err := sc.Err(); err != nil {
		aiFinish(gen, err)
		requestAIRepaint()
		return
	}
	if !got {
		aiFinish(gen, errors.New("接口没返回任何内容（有些服务不支持 stream，或模型名/权限不对）"))
		requestAIRepaint()
		return
	}
	aiFinish(gen, nil)
	requestAIRepaint()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ================================================================ 重画（合并）

var (
	aiRepaintMu      sync.Mutex
	aiRepaintPending bool
)

// requestAIRepaint 请求主线程重画侧边栏。
//
// 流式输出是"每个字一次"的，直接每次都 PostMessage 会把消息队列灌满；
// 这里合并成"最多一个在途"，主线程收到后照当前状态重画一次。
func requestAIRepaint() {
	aiRepaintMu.Lock()
	if aiRepaintPending {
		aiRepaintMu.Unlock()
		return
	}
	aiRepaintPending = true
	aiRepaintMu.Unlock()
	postToMain(wmAIRepaint)
}

func clearAIRepaint() {
	aiRepaintMu.Lock()
	aiRepaintPending = false
	aiRepaintMu.Unlock()
}

// describeAIStatus 给侧边栏底部用一句话说清现在是什么状态。
//
// 这里刻意把"缺什么"说出来（缺 key / 文件读不到），而不是一句"未配置" ——
// 用户看到"还没填 api_key（文件：…）"才知道下一步干什么。
func describeAIStatus() string {
	cfg, err := loadAIConfig()
	if err != nil {
		return "临时问答：" + err.Error()
	}
	return fmt.Sprintf("临时问答：%s @ %s", cfg.Model, strings.TrimPrefix(strings.TrimPrefix(cfg.BaseURL, "https://"), "http://"))
}

// openAIConfig 用系统默认程序打开配置文件（记事本），方便填 key。
func openAIConfig() {
	f := aiConfigPath()
	if f == "" {
		return
	}
	ensureAIConfigTemplate()
	shellOpen(f)
}

// saveAIConfig 把配置写回 ai.txt。
//
// 写的是**规范化的文本**（键的顺序固定，注释保留一段说明），而不是"改一行"：
// 用户手改过的格式我们不该假装能原样保住，但至少要说清楚写进去的是什么。
// api_key 每次都以明文写入 —— 这一点在文件和 README 里都写明了，不藏着。
func saveAIConfig(cfg aiConfig) error {
	f := aiConfigPath()
	if f == "" {
		return errors.New("拿不到配置目录")
	}
	text, err := formatAIConfig(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	return os.WriteFile(f, []byte(text), 0o600)
}

// formatAIConfig 生成配置文本（纯函数，便于测试）。
//
// 写的是**规范化的文本**，而不是"改一行"：用户手改过的格式我们不该假装能原样保住，
// 但至少要说清楚写进去的是什么。api_key 明文写入 —— 这一点文件里和 README 里都写明了。
func formatAIConfig(cfg aiConfig) (string, error) {
	if cfg.BaseURL == "" || cfg.Model == "" {
		return "", errors.New("接口地址和模型都不能为空")
	}
	if !strings.HasPrefix(cfg.BaseURL, "http") {
		return "", fmt.Errorf("接口地址要以 http(s):// 开头：%q", cfg.BaseURL)
	}
	var b strings.Builder
	b.WriteString("# 侧边栏「问一下」（临时问答）的接口配置 —— OpenAI 兼容接口。\n")
	b.WriteString("# 这个文件由设置页写入；api_key 是明文，别把它分享出去。\n")
	b.WriteString("base_url: " + strings.TrimSpace(cfg.BaseURL) + "\n")
	b.WriteString("model: " + strings.TrimSpace(cfg.Model) + "\n")
	b.WriteString("api_key: " + strings.TrimSpace(cfg.APIKey) + "\n")
	if cfg.System != "" {
		b.WriteString("system: " + strings.TrimSpace(cfg.System) + "\n")
	}
	if cfg.MaxTokens > 0 {
		b.WriteString("max_tokens: " + strconv.Itoa(cfg.MaxTokens) + "\n")
	}
	return b.String(), nil
}

// aiPing 发一次最小请求，用来在设置页里"测试连接"。
//
// 走的是同一段请求代码（buildChatRequest），只是不开流式、超时更短 ——
// 免得"测试用的那条路"和"真正用的那条路"不一致。
func aiPing(cfg aiConfig) (string, error) {
	body, err := buildChatRequest(cfg, "你好")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("连不上 %s：%v", cfg.BaseURL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d：%s", resp.StatusCode, truncate(strings.TrimSpace(string(raw)), 200))
	}
	// 服务端可能回流式也可能回整包，两种都认
	if s := firstContentFromSSE(string(raw)); s != "" {
		return s, nil
	}
	var whole struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &whole); err == nil && len(whole.Choices) > 0 {
		return whole.Choices[0].Message.Content, nil
	}
	return "", errors.New("回包里没有可识别的内容")
}

// firstContentFromSSE 把整段 SSE 文本里的增量拼起来（给"测试连接"用）。
func firstContentFromSSE(raw string) string {
	var out strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		if d, ok := parseSSELine(line); ok {
			out.WriteString(d)
		}
	}
	return out.String()
}
