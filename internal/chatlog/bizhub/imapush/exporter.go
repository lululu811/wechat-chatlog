// Package imapush 提供微信公众号文章 → IMA 知识库 推送能力。
// 通过调用 imaskai skill（`~/.claude/skills/ima/ima_api.cjs`）的
// `openapi/wiki/v1/import_urls` 端点实现与 IMA OpenAPI 的解耦。
//
// 与 mdexport 的关系：形状对齐（FailureKind / 错误分类 / 并发安全），
// 但没有"先暂存再合并"的 staging 概念（IMA 自己抓原文，没有"产出文件"）；
// 也没有"脚本能力探测"（node 脚本行为固定：成功 → stdout JSON / 失败 → stderr JSON）。
package imapush

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// importBatchLimit import_urls 单批最大 URL 数（硬约束）。
//
// 这是 IMA OpenAPI 的接口硬约束，不是我们设的；超过会直接被 IMA 拒掉。
// 分批逻辑必须按这个上限切，调用方不可绕过。
const importBatchLimit = 10

// waitGrace 超时后留给管道关闭的宽限期。
//
// 超过这个时间就不等了：cmd.Wait() 在孙进程持有管道时会一直阻塞，
// WaitDelay 到点后会直接关掉 I/O 管道强制它返回。
const waitGrace = 5 * time.Second

// FailureKind 失败分类。分类的意义是决定「怎么办」，而不只是「出了什么事」：
// 凭证缺失与 imaskai 升级要人去处理（改配置 / 升级 skill），
// 限流靠降并发退避，业务错误（KB 不存在、URL 不合法）靠人改配置。
//
// 命名与 mdexport.FailureKind 对仗，但不共用 —— IMA 错误图谱与微信抓取器不同
// （没有验证码，没有"输出目录不可写"），强行复用会让 store / bizhub 的聚合 SQL
// 拿到一坨无意义的「抓取器」分类。
type FailureKind string

const (
	// KindCredential 凭证缺失 / 解析失败（imaskai -100 + msg 含 credential/凭证）
	KindCredential FailureKind = "credential"
	// KindSkillUpdate imaskai 需要升级（-200）
	KindSkillUpdate FailureKind = "skill_update"
	// KindRateLimited 频率限制（业务层 110021 / 限流 / 频率）
	KindRateLimited FailureKind = "rate_limited"
	// KindUpstreamFailed IMA 业务错误（KB 不存在 / 资源错 / 其他 code != 0）
	KindUpstreamFailed FailureKind = "upstream_failed"
	// KindInvalidURL URL 不合法（业务层 code != 0 + msg 含 url/链接/invalid）
	KindInvalidURL FailureKind = "invalid_url"
	// KindTimeout 单批推送超时 —— 可直接重试
	KindTimeout FailureKind = "timeout"
	// KindDependency 依赖缺失（node 不在 PATH / imaskai 不在 skill_dir）
	KindDependency FailureKind = "dependency"
	// KindScriptFailed 其他脚本进程错误（兜底）
	KindScriptFailed FailureKind = "script_failed"
	// KindUnknown 兜底
	KindUnknown FailureKind = "unknown"
)

// permanentKinds 需要人工介入、重试无意义的分类。
//
// 与 mdexport 形状一致：单独抽出来是为了让 store 层拼 SQL 过滤时不复制分类逻辑。
// KindDependency 进这里：node 未装 / imaskai 不在 skill_dir 都得人改环境，
// 不是 chatlog 自己重试就能过的。
var permanentKinds = []FailureKind{
	KindCredential, KindSkillUpdate, KindUpstreamFailed, KindInvalidURL, KindDependency,
}

// Retryable 该失败是否值得直接重试（不需要人改配置 / 升级 skill）。
func (k FailureKind) Retryable() bool {
	switch k {
	case KindTimeout, KindRateLimited, KindScriptFailed, KindUnknown:
		return true
	}
	return false
}

// NeedsHuman 该失败是否需要人工介入（改配置、升级 imaskai）。
func (k FailureKind) NeedsHuman() bool {
	for _, p := range permanentKinds {
		if k == p {
			return true
		}
	}
	return false
}

// PermanentKinds 返回「需人工处理」的分类字符串名单，供 store 层拼 SQL。
func PermanentKinds() []string {
	out := make([]string, len(permanentKinds))
	for i, k := range permanentKinds {
		out[i] = string(k)
	}
	return out
}

// ParseKind 把落库的分类字符串还原成 FailureKind。
//
// 空串（老记录迁移或成功记录）一律按 KindUnknown 处理 —— KindUnknown 是可重试的，
// 所以历史遗留的失败记录会在下一轮批量推送时被重新捡起来。
func ParseKind(s string) FailureKind {
	if s == "" {
		return KindUnknown
	}
	return FailureKind(s)
}

// Label 面向用户的中文短标签。
func (k FailureKind) Label() string {
	switch k {
	case KindCredential:
		return "IMA 凭证缺失"
	case KindSkillUpdate:
		return "imaskai 需升级"
	case KindRateLimited:
		return "被限流"
	case KindUpstreamFailed:
		return "IMA 业务错误"
	case KindInvalidURL:
		return "URL 不合法"
	case KindTimeout:
		return "超时"
	case KindDependency:
		return "依赖缺失"
	case KindScriptFailed:
		return "推送失败"
	}
	return "未知错误"
}

// PushError 带分类的推送错误。
//
// Kind / Msg / ExitCode / Stderr 含义与 mdexport.ExportError 对仗。
type PushError struct {
	Kind     FailureKind
	Msg      string
	ExitCode int
	Stderr   string
}

func (e *PushError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("imapush: %s", e.Kind.Label())
}

// KindOf 从任意 error 中取出失败分类；非 PushError 视为 unknown。
func KindOf(err error) FailureKind {
	var pe *PushError
	if errors.As(err, &pe) {
		return pe.Kind
	}
	if err == nil {
		return ""
	}
	return KindUnknown
}

// classifyProcessErr 依据进程退出码与 stderr 判定失败分类（处理 imaskai 的两层错误协议）。
//
// imaskai 的两层协议：
//  1. 进程退出码 != 0 时，stderr 是结构化 JSON {"code": -100|-200, "msg": "..."}
//     -100 = 程序错误（凭证缺、网络错、参数错）
//     -200 = skill 需升级（原请求未发出）
//  2. 进程正常退出时，stdout 是业务响应 {"code": 0, "data": {...}}
//     code != 0 表示 IMA 业务错误，按 msg 内容细分。
//
// 这个函数只处理第 1 层（进程层）。第 2 层（业务层）由 classifyBusiness 处理。
func classifyProcessErr(exitCode int, stderr string, timedOut bool) FailureKind {
	if timedOut {
		return KindTimeout
	}
	if exitCode == 0 {
		return KindUnknown
	}

	// 解析 stderr 的结构化错误
	var eerr struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal([]byte(stderr), &eerr)

	switch eerr.Code {
	case -200:
		return KindSkillUpdate
	case -100:
		low := strings.ToLower(eerr.Msg)
		switch {
		case containsAny(low, "credential", "凭证", "未找到 ima 凭证", "missing credentials"):
			return KindCredential
		case containsAny(low, "command not found", "imaskai not found", "no such file", "not installed"):
			return KindDependency
		}
		return KindScriptFailed
	}

	// stderr 不是结构化 JSON —— 兜底按 stderr 文本关键词
	low := strings.ToLower(stderr)
	switch {
	case containsAny(low, "credential", "凭证"):
		return KindCredential
	case containsAny(low, "command not found", "no such file", "imaskai not found"):
		return KindDependency
	case containsAny(low, "timeout", "timed out", "deadline exceeded", "超时"):
		return KindTimeout
	}
	return KindScriptFailed
}

// classifyBusinessErr 把 IMA 业务响应里的 (code, msg) 映射到 FailureKind。
//
// IMA 的错误码目前不公开分类细则，所以只能按 msg 关键词启发式分类。
// 抓取到的关键词会随 imaskai 升级变化 —— 改前先看 ~/.claude/skills/ima/.history
// 或 ima-smoke.sh 跑的 transcript。
//
// 匹配顺序很关键 —— 必须先匹配更具体的"知识库 / 资源"类，再匹配更宽泛的
// "url 不合法"类。否则 "invalid knowledge_base_id" 会被错分成 InvalidURL
// （msg 同时含 "invalid" 和 "knowledge_base"，先撞到 invalid 这一支）。
func classifyBusinessErr(code int, msg string) FailureKind {
	if code == 0 {
		return KindUnknown
	}
	low := strings.ToLower(msg)
	switch {
	case containsAny(low, "110021", "频率", "频繁", "限流", "too many", "frequent", "rate limit", "freq control"):
		return KindRateLimited
	case containsAny(low, "知识库不存在", "knowledge_base", "knowledge base", "kb_id", "folder_id"):
		return KindUpstreamFailed
	case containsAny(low, "url 不合法", "链接无效", "invalid url"):
		return KindInvalidURL
	}
	return KindUpstreamFailed
}

// containsAny 检查 s 是否包含任一关键词（已小写化）。
func containsAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// Config 推送器配置
type Config struct {
	// SkillDir imaskai skill 路径（必填），ima_api.cjs 应当在此目录下
	SkillDir string
	// KnowledgeBaseID IMA 知识库 ID（必填）
	KnowledgeBaseID string
	// FolderID 文件夹 ID（可选）。根目录时与 KB ID 相同。
	FolderID string
	// Timeout 单批推送超时（默认 60s）
	Timeout time.Duration
}

// PushResult 单 URL 推送结果
type PushResult struct {
	URL      string
	MediaID  string        // IMA 返回的 media_id；失败时为空
	RetCode  int           // IMA 业务层 ret_code；0 = 成功
	RetMsg   string        // IMA 业务层 msg（失败时才有内容）
	Kind     FailureKind   // 失败分类；RetCode == 0 时为空
	Duration time.Duration // 本批耗时（不是单 URL）
}

// Exporter 调用 imaskai skill 执行 IMA 推送。
//
// 与 mdexport.Exporter 的关键差异：
//   - 没有 runMu（脚本行为固定：成功走 stdout，失败走 stderr，不存在"老脚本 vs 新脚本"的探测）
//   - 没有 capsMu（无能力探测；脚本契约就是固定的）
type Exporter struct {
	cfg Config
}

// New 创建推送器
func New(cfg Config) (*Exporter, error) {
	if cfg.SkillDir == "" {
		return nil, fmt.Errorf("imapush: skill dir is required")
	}
	if cfg.KnowledgeBaseID == "" {
		return nil, fmt.Errorf("imapush: knowledge base id is required")
	}
	if cfg.FolderID == "" {
		cfg.FolderID = cfg.KnowledgeBaseID
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &Exporter{cfg: cfg}, nil
}

// Check 检查推送器配置是否可用（skill 存在 ima_api.cjs、KB ID 非空）
func (e *Exporter) Check() error {
	apiPath := e.apiPath()
	if _, err := os.Stat(apiPath); err != nil {
		return &PushError{
			Kind: KindDependency,
			Msg:  fmt.Sprintf("imapush: imaskai not found at %s (%v)", apiPath, err),
		}
	}
	if e.cfg.KnowledgeBaseID == "" {
		return &PushError{
			Kind: KindDependency,
			Msg:  "imapush: knowledge base id is empty",
		}
	}
	return nil
}

// apiPath 拼接 imaskai 客户端脚本路径
func (e *Exporter) apiPath() string {
	return e.cfg.SkillDir + "/ima_api.cjs"
}

// PushOne 单 URL 推送（语法糖：等同于 Push(ctx, [url])）。
//
// 返回单条结果。失败可能是 process-level（cert/timeout 等整批失败）或
// business-level（IMA ret_code != 0）。
func (e *Exporter) PushOne(ctx context.Context, url string) (*PushResult, error) {
	res, err := e.Push(ctx, []string{url})
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, &PushError{
			Kind: KindUnknown,
			Msg:  "imapush: empty result from push",
		}
	}
	r := res[0]
	return &r, nil
}

// Push 推送一组 URL 到 IMA 知识库（按 importBatchLimit 自动分批）。
//
// 返回值：
//   - 进程层错误（node 缺失、凭证缺、超时）：返回非 nil error，结果无意义，调用方应把全部 URL 标失败。
//   - 业务层错误（IMA ret_code != 0）：返回 nil error，每条 URL 的失败详情在对应 PushResult.Kind / RetMsg。
//
// urls 顺序与返回结果顺序不保证一致 —— 调用方应按 PushResult.URL 反查。
// 顺序不重要是因为导出层本来就要按 article_id 反查业务记录。
func (e *Exporter) Push(ctx context.Context, urls []string) ([]PushResult, error) {
	if len(urls) == 0 {
		return nil, nil
	}
	if len(urls) > importBatchLimit*100 {
		// 单次调用太多 batch：提醒调用方分批，避免长时间占用连接。
		// 不做硬阻断 —— 单纯是给一个 log 提示。
		log.Warn().Int("urls", len(urls)).
			Msg("imapush: 单次推送超过 1000 URL，建议调用方自行分批")
	}

	var allResults []PushResult
	for start := 0; start < len(urls); start += importBatchLimit {
		end := start + importBatchLimit
		if end > len(urls) {
			end = len(urls)
		}
		batch := urls[start:end]
		batchResults, err := e.pushBatch(ctx, batch)
		if err != nil {
			// 进程层失败：本批所有 URL 都按该失败分类；已成功的批次结果保留。
			failedKind := KindOf(err)
			for _, u := range batch {
				allResults = append(allResults, PushResult{
					URL:  u,
					Kind: failedKind,
				})
			}
			return allResults, err
		}
		allResults = append(allResults, batchResults...)
	}
	return allResults, nil
}

// pushBatch 单批推送（len(urls) <= importBatchLimit）
//
// 进程层错误返回 *PushError；业务层错误通过 PushResult.Kind 表达。
func (e *Exporter) pushBatch(ctx context.Context, urls []string) ([]PushResult, error) {
	if len(urls) == 0 {
		return nil, nil
	}
	if len(urls) > importBatchLimit {
		return nil, fmt.Errorf("imapush: batch size %d exceeds limit %d", len(urls), importBatchLimit)
	}

	// 构造 body
	bodyJSON, err := buildImportBody(e.cfg.KnowledgeBaseID, e.cfg.FolderID, urls)
	if err != nil {
		return nil, &PushError{Kind: KindScriptFailed, Msg: fmt.Sprintf("imapush: build body: %v", err)}
	}

	// opts 留空：让 imaskai 自己读 env / ~/.config/ima/ 凭证。
	// chatlog 不持有 IMA 凭证，凭证轮换时不必重启 chatlog。
	optsJSON := "{}"

	runCtx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "node", e.apiPath(), "openapi/wiki/v1/import_urls", bodyJSON, optsJSON)
	var stderr, stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	cmd.WaitDelay = waitGrace
	setKillProcessGroup(cmd)

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	if runErr != nil {
		timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
		exitCode := -1
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		}
		kind := classifyProcessErr(exitCode, stderr.String(), timedOut)
		msg := fmt.Sprintf("imapush: %s", kind.Label())
		if s := strings.TrimSpace(stderr.String()); s != "" {
			msg = fmt.Sprintf("%s: %s", msg, lastLine(s))
		}
		return nil, &PushError{Kind: kind, Msg: msg, ExitCode: exitCode, Stderr: stderr.String()}
	}

	// 进程正常退出 —— 解析 stdout 业务层响应
	return parseImportResponse(stdout.String(), urls, elapsed)
}

// buildImportBody 构造 import_urls 的请求 body JSON 字符串
func buildImportBody(kbID, folderID string, urls []string) (string, error) {
	type body struct {
		KnowledgeBaseID string   `json:"knowledge_base_id"`
		FolderID        string   `json:"folder_id"`
		URLs            []string `json:"urls"`
	}
	b := body{
		KnowledgeBaseID: kbID,
		FolderID:        folderID,
		URLs:            urls,
	}
	out, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// parseImportResponse 解析 import_urls 业务层响应
//
// IMA 返回结构（成功）：
//
//	{"code":0,"msg":"success","data":{"results":{"<url>":{"url":"...","ret_code":0,"media_id":"..."}}}}
//
// IMA 返回结构（失败）：
//
//	{"code":110021,"msg":"频率限制..."}           ← 整批失败
//	{"code":0,"data":{"results":{"<url>":{"ret_code":1,"msg":"..."}}}} ← 单 URL 失败
//
// 这两种情况 stdout 都能解析；code != 0 是整批级业务错误，code == 0 但
// results 内 ret_code != 0 是单 URL 业务错误。
func parseImportResponse(stdout string, inputURLs []string, elapsed time.Duration) ([]PushResult, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Results map[string]struct {
				URL     string `json:"url"`
				RetCode int    `json:"ret_code"`
				MediaID string `json:"media_id"`
				Msg     string `json:"msg"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return nil, &PushError{
			Kind: KindScriptFailed,
			Msg:  fmt.Sprintf("imapush: parse stdout: %v", err),
		}
	}

	// 整批级业务错误（如 110021 频控、KB 不存在）
	if resp.Code != 0 {
		kind := classifyBusinessErr(resp.Code, resp.Msg)
		results := make([]PushResult, 0, len(inputURLs))
		for _, u := range inputURLs {
			results = append(results, PushResult{
				URL:      u,
				RetCode:  resp.Code,
				RetMsg:   resp.Msg,
				Kind:     kind,
				Duration: elapsed,
			})
		}
		return results, nil
	}

	// 单 URL 业务错误（results 内 ret_code != 0）
	inputSet := make(map[string]struct{}, len(inputURLs))
	for _, u := range inputURLs {
		inputSet[u] = struct{}{}
	}
	results := make([]PushResult, 0, len(inputURLs))
	for u, r := range resp.Data.Results {
		if _, ok := inputSet[u]; !ok {
			// IMA 返回了不在我们输入里的 URL —— 防御：忽略
			continue
		}
		pr := PushResult{
			URL:      u,
			RetCode:  r.RetCode,
			MediaID:  r.MediaID,
			RetMsg:   r.Msg,
			Duration: elapsed,
		}
		if r.RetCode != 0 {
			pr.Kind = classifyBusinessErr(r.RetCode, r.Msg)
		}
		results = append(results, pr)
	}

	// 防御：如果 IMA 没回某个 URL，按 unknown 失败补齐
	if len(results) < len(inputURLs) {
		have := make(map[string]struct{}, len(results))
		for _, r := range results {
			have[r.URL] = struct{}{}
		}
		for _, u := range inputURLs {
			if _, ok := have[u]; !ok {
				results = append(results, PushResult{
					URL:      u,
					Kind:     KindUnknown,
					Duration: elapsed,
				})
			}
		}
	}
	return results, nil
}

// lastLine 取 stderr 的最后一行非空内容，用于错误信息（脚本会把整个日志打到 stderr）
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 200 {
				l = l[:200] + "…"
			}
			return l
		}
	}
	return ""
}
