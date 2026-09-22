package bizhub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	ErrLLMNotConfigured  = errors.New("bizhub: llm not configured")
	ErrNoWatchedArticles = errors.New("bizhub: no articles from watched accounts")
)

const defaultLLMMaxTokens = 8192

// LLMClient Anthropic 兼容的 LLM 客户端
type LLMClient struct {
	baseURL   string
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
}

// NewLLMClient 创建 LLM 客户端，apiKey 为空时调用会返回 ErrLLMNotConfigured
func NewLLMClient(baseURL, apiKey, model string) *LLMClient {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	if model == "" {
		model = "claude-3-5-sonnet-latest"
	}
	return &LLMClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// SetMaxTokens 设置 LLM 输出最大 token 数；<=0 时回退到默认 4096
func (c *LLMClient) SetMaxTokens(n int) {
	if c == nil {
		return
	}
	c.maxTokens = n
}

// MaxTokens 返回当前生效的最大 token 数（0 表示使用默认）
func (c *LLMClient) MaxTokens() int {
	if c == nil {
		return 0
	}
	return c.maxTokens
}

// Model 返回当前使用的模型名
func (c *LLMClient) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

type llmMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type llmRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []llmMessage `json:"messages"`
}

type llmResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (c *LLMClient) doRequest(ctx context.Context, messages []llmMessage) (string, int, int, error) {
	if c == nil || c.apiKey == "" {
		return "", 0, 0, ErrLLMNotConfigured
	}
	maxTokens := c.maxTokens
	if maxTokens <= 0 {
		maxTokens = defaultLLMMaxTokens
	}
	body, err := json.Marshal(llmRequest{
		Model:     c.model,
		MaxTokens: maxTokens,
		Messages:  messages,
	})
	if err != nil {
		return "", 0, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", 0, 0, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", 0, 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		summary := string(respBody)
		if len(summary) > 512 {
			summary = summary[:512]
		}
		return "", 0, 0, fmt.Errorf("bizhub: llm request failed: status %d: %s", resp.StatusCode, summary)
	}

	var lr llmResponse
	if err := json.Unmarshal(respBody, &lr); err != nil {
		return "", 0, 0, fmt.Errorf("bizhub: parse llm response failed: %w", err)
	}

	var sb strings.Builder
	for _, part := range lr.Content {
		if part.Type == "text" {
			sb.WriteString(part.Text)
		}
	}
	if sb.Len() == 0 {
		return "", 0, 0, errors.New("bizhub: llm returned empty content")
	}
	return sb.String(), lr.Usage.InputTokens, lr.Usage.OutputTokens, nil
}

// Complete 调用 /v1/messages 生成文本；向后兼容（将整段 prompt 作为 user 消息）
func (c *LLMClient) Complete(ctx context.Context, prompt string) (string, int, int, error) {
	return c.CompleteWithSystem(ctx, "", prompt)
}

// CompleteWithSystem 调用 /v1/messages，分别传入 system 与 user 文本
func (c *LLMClient) CompleteWithSystem(ctx context.Context, system, user string) (string, int, int, error) {
	if c == nil || c.apiKey == "" {
		return "", 0, 0, ErrLLMNotConfigured
	}
	var messages []llmMessage
	if system != "" {
		messages = append(messages, llmMessage{Role: "system", Content: system})
	}
	messages = append(messages, llmMessage{Role: "user", Content: user})
	return c.doRequest(ctx, messages)
}

var jsonFencePattern = regexp.MustCompile("(?s)`{3}json\\s*(.+?)\\s*`{3}")

// ParseStructured 尝试将 LLM 输出的文本解析为 json.RawMessage。
// 顺序：先整段尝试 json.Unmarshal；失败则抽取 \x60\x60\x60json ... \x60\x60\x60 代码块。
// 成功返回 RawMessage 与 true；失败返回空 RawMessage 与 false。
func ParseStructured(content string) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, false
	}
	var any json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &any); err == nil && len(any) > 0 {
		return any, true
	}
	if m := jsonFencePattern.FindStringSubmatch(content); len(m) >= 2 {
		candidate := strings.TrimSpace(m[1])
		if err := json.Unmarshal([]byte(candidate), &any); err == nil && len(any) > 0 {
			return any, true
		}
	}
	return nil, false
}
