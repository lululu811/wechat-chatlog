package bizhub

import (
	"bytes"
	"context"
	"embed"
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

//go:embed prompts
var promptsFS embed.FS

// Prompt 名称常量（与 prompts/ 目录下的 .md 文件一一对应）。
//
// 集中定义避免调用方拼错文件名（embed FS 的错误信息很啰嗦，集中定义后
// typo 在编译期就报）。新增 prompt 时只要在 prompts/ 加文件 + 这里加常量。
const (
	PromptSummarySystem       = "summary_system"
	PromptFeedSummarySystem   = "feed_summary_system"
	PromptDailyDigestSystem   = "daily_digest_system"
	PromptArticleSummarySystem = "article_summary_system"
)

// LoadPrompt 从嵌入的 prompts/ 目录加载 prompt 文本。
//
// 返回值是去掉尾部空行后的完整内容。文件不存在 / 读失败 → 返回 error，
// 调用方应 fail-fast（prompt 缺失意味着 prompt 配置被人误删，不能兜底）。
func LoadPrompt(name string) (string, error) {
	data, err := promptsFS.ReadFile("prompts/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("bizhub: load prompt %q: %w", name, err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

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

// MaxRetries CompleteWithRetry 最多重试次数（含首次调用）。
//
// 3 = 1 次首次 + 2 次重试。指数退避 1s → 2s → 4s，整个重试链最长 7s
// 加上每次调用的网络 / LLM 处理时间。PR2 暂不暴露为配置项，写死避免
// 重试参数被各调用方零散调成不一致。
const MaxRetries = 3

// retryableHTTPStatus 判断 HTTP 错误是否值得重试。
//
// 4xx（除 408/429）通常是请求参数问题，重试也是失败；5xx / 408 / 429
// 都是暂时性错误（服务端问题 / 限流），重试通常能过。
func retryableHTTPStatus(statusCode int) bool {
	if statusCode >= 500 && statusCode < 600 {
		return true
	}
	if statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests {
		return true
	}
	return false
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
		// 网络层错误（DNS / 连接拒绝 / EOF）一律重试
		return "", 0, 0, fmt.Errorf("bizhub: llm http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", 0, 0, fmt.Errorf("bizhub: llm read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		summary := string(respBody)
		if len(summary) > 512 {
			summary = summary[:512]
		}
		return "", 0, 0, &llmHTTPError{
			StatusCode: resp.StatusCode,
			Body:       summary,
		}
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

// llmHTTPError HTTP 非 2xx 响应。CompleteWithRetry 通过 type assertion 判断是否重试。
type llmHTTPError struct {
	StatusCode int
	Body       string
}

func (e *llmHTTPError) Error() string {
	return fmt.Sprintf("bizhub: llm request failed: status %d: %s", e.StatusCode, e.Body)
}

// Complete 调用 /v1/messages 生成文本；向后兼容（将整段 prompt 作为 user 消息）
func (c *LLMClient) Complete(ctx context.Context, prompt string) (string, int, int, error) {
	return c.CompleteWithSystem(ctx, "", prompt)
}

// CompleteWithSystem 调用 /v1/messages，分别传入 system 与 user 文本
//
// 不做重试、不做 JSON 解析。新代码请用 CompleteWithRetry / CompleteStructured。
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

// CompleteWithRetry 调 LLM 失败时按指数退避重试，最多 MaxRetries 次。
//
// 重试触发条件：
//   - 网络层错误（DNS / 连接拒绝 / EOF / 超时）—— 重试
//   - HTTP 5xx / 408 / 429 —— 重试
//   - 其他 4xx（参数错误、鉴权失败）—— 不重试，立即返回
//   - 业务错误（空 content / parse 失败）—— 不重试（重试也只会换一种坏输出）
//
// 退避：1s → 2s → 4s。ctx.Done() 立即返回，cancel 不会被 sleep 拖住。
//
// tokens 计数取最后一次成功调用的值；失败重试链全挂时返回 0。
func (c *LLMClient) CompleteWithRetry(ctx context.Context, system, user string) (string, int, int, error) {
	if c == nil || c.apiKey == "" {
		return "", 0, 0, ErrLLMNotConfigured
	}

	var lastErr error
	backoff := time.Second

	for attempt := 0; attempt < MaxRetries; attempt++ {
		text, inTok, outTok, err := c.completeOnce(ctx, system, user)
		if err == nil {
			return text, inTok, outTok, nil
		}
		lastErr = err

		if !shouldRetryLLM(err) {
			return "", 0, 0, err
		}

		// 最后一次失败后等不等都没意义（下次循环就是 break），所以最后一次不 sleep
		if attempt == MaxRetries-1 {
			break
		}

		select {
		case <-ctx.Done():
			return "", 0, 0, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}

	return "", 0, 0, fmt.Errorf("bizhub: llm retry exhausted (attempts=%d): %w", MaxRetries, lastErr)
}

// completeOnce 单次 LLM 调用。CompleteWithRetry 用，不导出。
func (c *LLMClient) completeOnce(ctx context.Context, system, user string) (string, int, int, error) {
	return c.CompleteWithSystem(ctx, system, user)
}

// shouldRetryLLM 判定错误是否值得重试。
func shouldRetryLLM(err error) bool {
	if err == nil {
		return false
	}
	// ctx 取消 / 超时不重试（按设计意图立刻退出）
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var httpErr *llmHTTPError
	if errors.As(err, &httpErr) {
		return retryableHTTPStatus(httpErr.StatusCode)
	}
	// 网络层 / EOF / DNS 错误（doRequest 包成 fmt.Errorf("... http: %w", err)）
	// 重试；parse 错误（doRequest 包成 "parse llm response failed"）不重试
	if strings.Contains(err.Error(), "llm http:") {
		return true
	}
	if strings.Contains(err.Error(), "llm read body:") {
		return true
	}
	return false
}

// CompleteStructured 调 LLM + 尝试解析 JSON。返回 (原始文本, 解析后的 JSON, inTok, outTok, err)。
//
// 重试由内部 CompleteWithRetry 处理。LLM 返回非 JSON 时不返回 error，
// raw 字段为 nil —— 调用方根据业务决定 raw 怎么兜底（GenerateDailyDigest
// 用 raw 当 headline、GenerateArticleSummary 用 raw 前 200 字当 summary 等）。
//
// 想要严格模式（解析失败直接报错）请自己调 CompleteWithRetry + ParseStructured。
func (c *LLMClient) CompleteStructured(ctx context.Context, system, user string) (string, json.RawMessage, int, int, error) {
	text, inTok, outTok, err := c.CompleteWithRetry(ctx, system, user)
	if err != nil {
		return text, nil, inTok, outTok, err
	}
	if raw, ok := ParseStructured(text); ok {
		return text, raw, inTok, outTok, nil
	}
	return text, nil, inTok, outTok, nil
}

// PromptLoader 抽象 LoadPrompt 入口，方便单测注入伪 prompt。
//
// bizhub 内部直接用 LoadPrompt；handler / 外部测试可以传入 mock 实现。
type PromptLoader interface {
	Load(name string) (string, error)
}

// embeddedPrompts 默认的 PromptLoader 实现，包一层 embed FS。
type embeddedPrompts struct{}

func (embeddedPrompts) Load(name string) (string, error) {
	return LoadPrompt(name)
}

// DefaultPrompts 业务默认的 PromptLoader 实例。
//
// 单测可以用 stubPromptLoader 替换；Runtime 不暴露 setter，意味着 PR2 之后
// 不能再加「运行时切 prompt 文件夹」这类需求——要支持必须改 API。
var DefaultPrompts PromptLoader = embeddedPrompts{}

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

var jsonFencePattern = regexp.MustCompile("(?s)`{3}json\\s*(.+?)\\s*`{3}")

// ParseStructured 尝试将 LLM 输出的文本解析为 json.RawMessage。
// 顺序：先整段尝试 json.Unmarshal；失败则抽取 ```json ... ``` 代码块。
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
