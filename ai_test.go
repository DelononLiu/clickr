//go:build windows

// ai_test.go —— 临时问答里"能算的部分"：配置解析、请求体、SSE 解析、状态机。
//
// 这里全是**纯函数/纯状态**，不发真请求 —— 网络那头是别人的服务，
// 测试不该依赖它。真请求靠手工验证（侧边栏点「问一下」）。

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAIConfigParsesDeepSeekTemplate(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "ai.txt")
	// 预置模板必须**除了 key 之外**都是可直接用的
	tmpl := strings.Replace(aiConfigTemplate, "api_key:", "api_key: sk-test", 1)
	if err := os.WriteFile(f, []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseAIConfig(string(tmpl), f)
	if err != nil {
		t.Fatalf("预置模板应该能直接解析通过: %v", err)
	}
	if cfg.BaseURL != "https://api.deepseek.com/v1" || cfg.Model != "deepseek-flash" {
		t.Errorf("预置值不对: base_url=%q model=%q", cfg.BaseURL, cfg.Model)
	}
	if cfg.APIKey != "sk-test" {
		t.Errorf("api_key 没读出来: %q", cfg.APIKey)
	}
}

func TestAIConfigSaysWhatIsMissing(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "ai.txt")

	// 只有 key 没填：要说清"还没填 api_key"，而不是笼统的"未配置"
	noKey := strings.Replace(aiConfigTemplate, "api_key:", "api_key:", 1)
	_, err := parseAIConfig(noKey, f)
	if err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Errorf("缺 key 时的错误应当点名 api_key，实际: %v", err)
	}

	// 写错的键名要报出来，而不是静默忽略
	_, err = parseAIConfig("base_url: https://x/v1\nmodel: m\napi_key: k\nnonsense: 1\n", f)
	if err == nil || !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("不认识的配置项应当被指名，实际: %v", err)
	}

	// 少字段
	_, err = parseAIConfig("api_key: k\n", f)
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("缺 base_url 应当被点名，实际: %v", err)
	}

	// base_url 不是 http
	_, err = parseAIConfig("base_url: api.deepseek.com\nmodel: m\napi_key: k\n", f)
	if err == nil {
		t.Error("base_url 缺协议头应当报错")
	}

	// 注释与空行不算配置
	cfg, err := parseAIConfig("# 注释\n\nbase_url: https://a/v1\nmodel: m\napi_key: k\n", f)
	if err != nil || cfg.Model != "m" {
		t.Errorf("注释/空行不该影响解析: %v %v", cfg, err)
	}
}

func TestBuildChatRequestIsOpenAICompatible(t *testing.T) {
	body, err := buildChatRequest(aiConfig{Model: "deepseek-flash"}, "天空为什么是蓝的")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{`"model":"deepseek-flash"`, `"stream":true`, `"role":"system"`, `"role":"user"`, "天空为什么是蓝的"} {
		if !strings.Contains(s, want) {
			t.Errorf("请求体里少了 %s：%s", want, s)
		}
	}
	// 不填 system 时用内置提示词；填了就用用户的
	body2, _ := buildChatRequest(aiConfig{Model: "m", System: "自定义"}, "q")
	if !strings.Contains(string(body2), "自定义") || strings.Contains(string(body2), aiDefaultSystem[:10]) {
		t.Errorf("自定义 system 没生效: %s", body2)
	}
}

func TestParseSSELine(t *testing.T) {
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{`data: {"choices":[{"delta":{"content":"你"}}]}`, "你", true},
		{`data:{"choices":[{"delta":{"content":"好"}}]}`, "好", true}, // 冒号后没空格
		{`data: [DONE]`, "", false},
		{"", "", false},
		{": keep-alive", "", false},
		{`event: ping`, "", false},
		{`data: {"choices":[{"delta":{}}]}`, "", false},   // 空增量
		{`data: {"error":{"message":"boom"}}`, "", false}, // 错误包不当内容
		{`data: not json`, "", false},
		{`data: {"choices":[{"message":{"content":"非流式回包"}}]}`, "非流式回包", true},
	}
	for _, c := range cases {
		got, ok := parseSSELine(c.line)
		if ok != c.ok || got != c.want {
			t.Errorf("parseSSELine(%q) = (%q,%v)，期望 (%q,%v)", c.line, got, ok, c.want, c.ok)
		}
	}
}

func TestAIStateDiscardsStaleStream(t *testing.T) {
	// 第二次提问之后，第一次那还在跑的流不许再往界面上写字
	gen1 := aiBegin("第一个问题")
	gen2 := aiBegin("第二个问题")
	if aiAppend(gen1, "旧答案") {
		t.Error("过期的流不该写进去")
	}
	if aiAppend(gen2, "新答案") {
		// 写进去了
	}
	st := aiNow()
	if st.Answer != "新答案" || st.Question != "第二个问题" {
		t.Errorf("状态不对: %+v", st)
	}
	aiFinish(gen1, errors.New("旧流报错")) // 过期错误也不该冒出来
	if st := aiNow(); st.Err != "" {
		t.Errorf("过期的错误冒到了界面上: %q", st.Err)
	}
	aiFinish(gen2, nil)
	if st := aiNow(); st.Busy {
		t.Error("结束后还在「生成中」")
	}
}

func TestAIStopMarksCancelled(t *testing.T) {
	gen := aiBegin("问题")
	aiStop()
	st := aiNow()
	if st.Busy {
		t.Error("停止之后仍然在生成中")
	}
	if st.Err == "" {
		t.Error("停止之后应当给一句可见的说明（本项目「失败要看得见」）")
	}
	if aiAppend(gen, "x") {
		t.Error("停止之后旧流还在写")
	}
}

func TestTruncateKeepsRunesIntact(t *testing.T) {
	if got := truncate("中文很长的一段话", 3); got != "中文很…" {
		t.Errorf("truncate 按字符切: %q", got)
	}
	if got := truncate("短", 10); got != "短" {
		t.Errorf("不该改动短字符串: %q", got)
	}
}

func TestFormatAIConfigRoundTrip(t *testing.T) {
	// 设置页保存 → 再读回来，必须一模一样（含没在界面上露出的高级项）
	in := aiConfig{
		BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-flash",
		APIKey: "sk-abc", System: "自定义提示词", MaxTokens: 1024,
	}
	text, err := formatAIConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := parseAIConfig(text, "ai.txt")
	if err != nil {
		t.Fatalf("自己写出来的配置读不回来: %v\n%s", err, text)
	}
	if out != in {
		t.Errorf("往返不一致:\n 写 %+v\n 读 %+v", in, out)
	}

	// 空地址/空模型要拦住
	if _, err := formatAIConfig(aiConfig{BaseURL: "", Model: "m"}); err == nil {
		t.Error("空 base_url 应当报错")
	}
	if _, err := formatAIConfig(aiConfig{BaseURL: "api.x.com", Model: "m"}); err == nil {
		t.Error("缺协议头应当报错")
	}
}

func TestFirstContentFromSSE(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"甲\"}}]}\n\n" +
		": ping\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"乙\"}}]}\n\n" +
		"data: [DONE]\n"
	if got := firstContentFromSSE(sse); got != "甲乙" {
		t.Errorf("拼接结果 %q，期望 \"甲乙\"", got)
	}
	if got := firstContentFromSSE("{}"); got != "" {
		t.Errorf("非 SSE 应当返回空，实际 %q", got)
	}
}
