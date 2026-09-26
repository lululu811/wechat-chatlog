// Package mdexport 提供微信公众号文章 → Markdown 导出能力。
// 通过调用外部 shell 脚本（可配置）实现与具体转换工具的解耦。
package mdexport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// FailureKind 失败分类。分类的意义是决定「怎么办」，而不只是「出了什么事」：
// 验证码与限流要降并发/等一会，依赖缺失与目录不可写要人去改配置，超时可以直接重试。
type FailureKind string

const (
	// KindCaptcha 微信返回验证码 / 环境异常 —— 需要人工介入，且应立即降低并发
	KindCaptcha FailureKind = "captcha"
	// KindRateLimited 频率限制 —— 降并发后重试通常能过
	KindRateLimited FailureKind = "rate_limited"
	// KindTimeout 单篇导出超时 —— 可直接重试
	KindTimeout FailureKind = "timeout"
	// KindDependency 抓取器未安装（脚本退出码 2）—— 必须人工处理，重试无意义
	KindDependency FailureKind = "dependency"
	// KindOutputDir 输出目录不可写（脚本退出码 4）—— 必须人工处理，重试无意义
	KindOutputDir FailureKind = "output_dir"
	// KindNoOutput 脚本成功退出但没有产出新 .md —— 多半是页面结构变了或脚本行为异常
	KindNoOutput FailureKind = "no_output"
	// KindScriptFailed 脚本非零退出且无法归入上面几类
	KindScriptFailed FailureKind = "script_failed"
	// KindInvalidURL 非 mp.weixin.qq.com 的文章链接 —— 数据问题，重试无意义
	KindInvalidURL FailureKind = "invalid_url"
	// KindUnknown 兜底
	KindUnknown FailureKind = "unknown"
)

// permanentKinds 需要人工介入、重试无意义的分类。
//
// 单独抽成一个切片而不是散在 NeedsHuman 的 switch 里，是因为存储层还要用
// 这份名单做 SQL 过滤（统计「被卡住等人工处理」的篇数）。名单只在这里定义一次，
// NeedsHuman 与 PermanentKinds 都从它派生，避免哪天加了一类永久失败却忘了同步 SQL。
var permanentKinds = []FailureKind{KindCaptcha, KindDependency, KindOutputDir, KindInvalidURL}

// Retryable 该失败是否值得直接重试（不需要人改配置/处理验证码）。
func (k FailureKind) Retryable() bool {
	switch k {
	case KindTimeout, KindRateLimited, KindScriptFailed, KindUnknown, KindNoOutput:
		return true
	}
	return false
}

// NeedsHuman 该失败是否需要人工介入（改配置、过验证码）。
func (k FailureKind) NeedsHuman() bool {
	for _, p := range permanentKinds {
		if k == p {
			return true
		}
	}
	return false
}

// PermanentKinds 返回「需人工处理」的分类字符串名单，供存储层拼 SQL。
func PermanentKinds() []string {
	out := make([]string, len(permanentKinds))
	for i, k := range permanentKinds {
		out[i] = string(k)
	}
	return out
}

// ParseKind 把落库的分类字符串还原成 FailureKind。
//
// 空串（老记录迁移而来、或成功记录）一律按 KindUnknown 处理 —— KindUnknown
// 是可重试的，所以历史遗留的 failed 记录会在下一轮批量归档时被重新捡起来。
func ParseKind(s string) FailureKind {
	if s == "" {
		return KindUnknown
	}
	return FailureKind(s)
}

// Label 面向用户的中文短标签。
func (k FailureKind) Label() string {
	switch k {
	case KindCaptcha:
		return "触发验证码"
	case KindRateLimited:
		return "被限流"
	case KindTimeout:
		return "超时"
	case KindDependency:
		return "抓取器未安装"
	case KindOutputDir:
		return "输出目录不可写"
	case KindNoOutput:
		return "脚本无产出"
	case KindScriptFailed:
		return "脚本执行失败"
	case KindInvalidURL:
		return "链接不合法"
	}
	return "未知错误"
}

// ExportError 带分类的导出错误。
type ExportError struct {
	Kind     FailureKind
	Msg      string
	ExitCode int
	Stderr   string
}

func (e *ExportError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("mdexport: %s", e.Kind.Label())
}

// KindOf 从任意 error 中取出失败分类；非 ExportError 视为 unknown。
func KindOf(err error) FailureKind {
	var ee *ExportError
	if errors.As(err, &ee) {
		return ee.Kind
	}
	if err == nil {
		return ""
	}
	return KindUnknown
}

// classify 依据退出码与 stderr 内容判定失败分类。
//
// 退出码约定来自 script/export-md.sh 的契约（1 参数 / 2 依赖 / 3 抓取失败 / 4 目录不可写），
// 但 stderr 才是用来区分「抓取失败」里到底是验证码还是限流的关键信息 ——
// 这两者对外表现都是退出码 3，处置方式却不同（一个要等人过验证码，一个降并发就能过）。
func classify(exitCode int, stderr string, timedOut bool) FailureKind {
	if timedOut {
		return KindTimeout
	}
	switch exitCode {
	case 1:
		return KindInvalidURL
	case 2:
		return KindDependency
	case 4:
		return KindOutputDir
	}

	low := strings.ToLower(stderr)
	containsAny := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(low, w) {
				return true
			}
		}
		return false
	}

	switch {
	case containsAny("验证码", "环境异常", "verify", "captcha", "security check"):
		return KindCaptcha
	case containsAny("频率", "限流", "too many", "frequent", "rate limit", "freq control"):
		return KindRateLimited
	case containsAny("timeout", "timed out", "超时", "deadline exceeded"):
		return KindTimeout
	case containsAny("not found in path", "command not found", "no such file", "not installed"):
		return KindDependency
	case containsAny("not writable", "permission denied", "cannot create output dir"):
		return KindOutputDir
	}

	if exitCode > 0 {
		return KindScriptFailed
	}
	return KindUnknown
}

// Config 导出器配置
type Config struct {
	// Script shell 脚本路径（必填）
	Script string
	// OutputDir 输出根目录（必填），输出结构: <OutputDir>/<account>/<title>/<title>.md
	OutputDir string
	// Timeout 单次导出超时（默认 120s）
	Timeout time.Duration
}

// Result 单次导出结果
type Result struct {
	// MDPath 生成的 .md 文件绝对路径
	MDPath string
	// Account 公众号名（从输出目录结构推断）
	Account string
	// Title 文章标题（从输出目录结构推断）
	Title string
	// Duration 执行耗时
	Duration time.Duration
}

// Exporter 调用外部脚本执行 MD 导出
//
// 并发相关的两个锁，职责不同，不要合并：
//
//	runMu  —— 在「还不知道脚本会不会报告输出路径」或「确认它不报告」时，
//	          把整次导出（快照 → 跑脚本 → 扫描）串起来。因为回退的判定方式是
//	          目录前后差集，只有一次导出在飞的时候才成立 —— 两个并发导出会让
//	          先结束的那个看到后结束的那个刚写好的文件，从而认错产出路径
//	          （表现为两篇文章的记录指向同一个 md，另一篇的文件成孤儿，
//	          而且状态都写着「已导出」）。宁慢不错。
//	capsMu —— 保护 capsKnown / capsReport 这一对状态。
//
// 脚本上报 MD_PATH（见 script/export-md.sh）时完全不碰 runMu，并发不受影响。
type Exporter struct {
	cfg Config

	runMu sync.Mutex

	capsMu     sync.Mutex
	capsKnown  bool
	capsReport bool

	warnOnce sync.Once
}

// OutputDir 返回输出根目录
func (e *Exporter) OutputDir() string {
	return e.cfg.OutputDir
}

// needsExclusiveRun 报告本次导出是否必须独占执行。
func (e *Exporter) needsExclusiveRun() bool {
	e.capsMu.Lock()
	defer e.capsMu.Unlock()
	return !e.capsKnown || !e.capsReport
}

// observePathReport 记下「脚本是否会报告 MD_PATH」。首次结论即定论 ——
// 脚本能力不会中途变化，反复试探只会让本来能并发的批次白白串行。
func (e *Exporter) observePathReport(reported bool) {
	e.capsMu.Lock()
	first := !e.capsKnown
	e.capsKnown = true
	e.capsReport = reported
	e.capsMu.Unlock()

	if first && !reported {
		e.warnOnce.Do(func() {
			log.Warn().Str("script", e.cfg.Script).
				Msg("mdexport: 脚本未输出 MD_PATH，归档并发将被限制为串行（建议按 script/export-md.sh 的契约上报产出路径）")
		})
	}
}

// New 创建导出器
func New(cfg Config) (*Exporter, error) {
	if cfg.Script == "" {
		return nil, fmt.Errorf("mdexport: script path is required")
	}
	if cfg.OutputDir == "" {
		return nil, fmt.Errorf("mdexport: output dir is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}

	// 确保输出目录存在
	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("mdexport: create output dir: %w", err)
	}

	return &Exporter{cfg: cfg}, nil
}

// exportWaitGrace 超时后留给管道关闭的宽限期。
//
// 超过这个时间就不等了：cmd.Wait() 在孙进程持有管道时会一直阻塞，
// WaitDelay 到点后会直接关掉 I/O 管道强制它返回。
const exportWaitGrace = 5 * time.Second

// Export 执行单篇文章导出
// url: 微信文章 URL
func (e *Exporter) Export(ctx context.Context, url string) (*Result, error) {
	// 规范化：http → https
	if strings.HasPrefix(url, "http://mp.weixin.qq.com/") {
		url = "https://" + strings.TrimPrefix(url, "http://")
	}
	if !strings.HasPrefix(url, "https://mp.weixin.qq.com/") {
		return nil, &ExportError{
			Kind: KindInvalidURL,
			Msg:  fmt.Sprintf("mdexport: invalid wechat article url: %s", url),
		}
	}

	// 检查脚本是否存在
	if _, err := os.Stat(e.cfg.Script); err != nil {
		return nil, &ExportError{
			Kind: KindDependency,
			Msg:  fmt.Sprintf("mdexport: script not found: %s (%v)", e.cfg.Script, err),
		}
	}

	// 能力未知 / 脚本不上报路径时独占执行，保证回退的目录 diff 判定成立
	if e.needsExclusiveRun() {
		e.runMu.Lock()
		defer e.runMu.Unlock()
	}

	// 快照导出前的目录状态，仅在需要回退判定时才用得上（记录两次的代价可忽略）
	before, err := scanOutputDir(e.cfg.OutputDir)
	if err != nil {
		before = make(map[string]struct{})
	}

	// 执行脚本
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", e.cfg.Script, url, "-o", e.cfg.OutputDir)
	var stderr, stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	// 超时必须真的能结束这次调用。
	//
	// CommandContext 只 kill 直接子进程（这里是 bash），而脚本会再拉起抓取器，
	// 抓取器又拉起浏览器 —— 这些孙进程会继续持有 stdout/stderr 的写端。
	// cmd.Wait() 要等管道关闭才返回，于是「超时」形同虚设：HTTP 请求一直挂着，
	// 后台还攒下一堆抓到一半的浏览器进程。
	//
	// 两道保险：
	//   WaitDelay  —— 上下文到期后再等一小会儿就强行关掉管道，保证 Wait 一定返回；
	//   进程组 kill —— 把整棵树一起收掉，不留孤儿（仅 unix，见 procgroup_unix.go）。
	cmd.WaitDelay = exportWaitGrace
	setKillProcessGroup(cmd)

	runErr := cmd.Run()
	if runErr != nil {
		// 区分「脚本自己失败」与「被我们超时杀掉」：后者是 context 到期
		timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
		exitCode := -1
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		}
		kind := classify(exitCode, stderr.String(), timedOut)
		msg := fmt.Sprintf("mdexport: %s", kind.Label())
		if s := strings.TrimSpace(stderr.String()); s != "" {
			msg = fmt.Sprintf("%s: %s", msg, lastLine(s))
		}
		return nil, &ExportError{Kind: kind, Msg: msg, ExitCode: exitCode, Stderr: stderr.String()}
	}

	elapsed := time.Since(start)

	// 首选：脚本自己报告的产出路径。它是唯一在并发下也正确的来源。
	if p, ok := parseReportedMDPath(stdout.String(), e.cfg.OutputDir); ok {
		e.observePathReport(true)
		res := resultFromPath(p, elapsed)
		e.fillAccountTitle(res, p)
		return res, nil
	}
	e.observePathReport(false)

	// 回退：对输出目录做前后差集（老脚本 / 第三方脚本），此时已独占执行
	after, err := scanOutputDir(e.cfg.OutputDir)
	if err != nil {
		return nil, &ExportError{Kind: KindOutputDir, Msg: fmt.Sprintf("mdexport: scan output dir: %v", err)}
	}

	var newMD string
	for p := range after {
		if _, ok := before[p]; !ok {
			if strings.HasSuffix(p, ".md") {
				newMD = p
				break
			}
		}
	}

	if newMD == "" {
		return nil, &ExportError{
			Kind:   KindNoOutput,
			Msg:    "mdexport: no new .md file found after export",
			Stderr: stderr.String(),
		}
	}

	res := resultFromPath(newMD, elapsed)
	e.fillAccountTitle(res, newMD)
	return res, nil
}

// parseReportedMDPath 从脚本 stdout 里取 MD_PATH=<path>（取最后一条）。
//
// 校验必须做严：路径要在输出目录内、要以 .md 结尾、要真实存在。
// 脚本报了一个不存在的路径时宁可用回退逻辑，也不能把脏路径写进归档记录 ——
// 那会让管理页显示「已归档」而磁盘上根本没有文件。
func parseReportedMDPath(stdout, outputDir string) (string, bool) {
	const marker = "MD_PATH="
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, marker) {
			continue
		}
		p := strings.TrimSpace(strings.TrimPrefix(line, marker))
		if p == "" || !strings.HasSuffix(p, ".md") || !filepath.IsAbs(p) {
			return "", false
		}
		// 防止脚本把产出写到输出目录之外（配置写错或脚本被换掉）
		rel, err := filepath.Rel(outputDir, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", false
		}
		if _, err := os.Stat(p); err != nil {
			return "", false
		}
		return p, true
	}
	return "", false
}

// resultFromPath 组装 Result（除 account/title 外）
func resultFromPath(p string, elapsed time.Duration) *Result {
	return &Result{MDPath: p, Duration: elapsed}
}

// fillAccountTitle 从 <OutputDir>/<account>/<title>/<title>.md 推断账号与标题
func (e *Exporter) fillAccountTitle(res *Result, mdPath string) {
	rel, err := filepath.Rel(e.cfg.OutputDir, mdPath)
	if err != nil {
		return
	}
	parts := strings.Split(rel, string(filepath.Separator))
	switch {
	case len(parts) >= 3:
		res.Account = parts[0]
		res.Title = parts[1]
	case len(parts) == 2:
		res.Account = parts[0]
		res.Title = strings.TrimSuffix(parts[1], ".md")
	}
}

// Check 检查导出器配置是否可用（脚本存在、输出目录可写）
func (e *Exporter) Check() error {
	if _, err := os.Stat(e.cfg.Script); err != nil {
		return &ExportError{Kind: KindDependency, Msg: fmt.Sprintf("mdexport: script not found: %s", e.cfg.Script)}
	}
	info, err := os.Stat(e.cfg.OutputDir)
	if err != nil {
		return &ExportError{Kind: KindOutputDir, Msg: fmt.Sprintf("mdexport: output dir not accessible: %s", e.cfg.OutputDir)}
	}
	if !info.IsDir() {
		return &ExportError{Kind: KindOutputDir, Msg: fmt.Sprintf("mdexport: output path is not a directory: %s", e.cfg.OutputDir)}
	}
	return nil
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

// scanOutputDir 扫描目录下所有 .md 文件的绝对路径。
//
// 跳过以 . 开头的目录：script/export-md.sh 的暂存目录是 <OutputDir>/.staging.XXXXXX，
// 里面的文件是「还没搬到位」的中间产物 —— 把它们算进差集会让回退判定误报。
func scanOutputDir(dir string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			if path != dir && strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			result[path] = struct{}{}
		}
		return nil
	})
	return result, err
}
