package bizhub

// Pipeline 状态机（PR1）。
//
// 设计动机：fetch / mdexport / summarize / imapush 四步原本各自有独立的 endpoint
// 和独立的状态字段（biz_article_contents / biz_exported_articles / biz_summaries /
// biz_push_job_items）。一篇 article 要走完全部 4 步，前端要按 4 个字段的组合状态
// 决定按钮——心智负担重，"现在到底是哪一步"经常说不清楚。
//
// PR1 把"当前在哪一步"收敛到 biz_articles.pipeline_status 单列字符串。
// 数据 cache 表（contents / exported_articles / summaries / push_jobs）保留不动，
// Worker 仍然是它们的写入方，但**不从中读状态**——状态来源只有 pipeline_status。
//
// 5 阶段正态：pending → fetched → md_exported → summarized → pushed
// 4 阶段失败态：failed:fetch / failed:mdexport / failed:summarize / failed:imapush
//
// 失败态由 ticker **不**自动重试（反爬保护），只接受 /admin/pipeline/retry/:id 触发。
// 同一篇文章被一个 Worker 串行吃到底，避免并发改中间状态。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Pipeline 状态常量。与 store.go 的 pipelinePickableStatuses 协同。
const (
	PipelinePending     = "pending"
	PipelineFetched     = "fetched"
	PipelineMDExported  = "md_exported"
	PipelineSummarized  = "summarized"
	PipelinePushed      = "pushed"

	// 失败态。Worker 只把文章推到这些状态，**不**从这里恢复——
	// 恢复必须由 /admin/pipeline/retry/:id 显式触发。
	PipelineFailedFetch    = "failed:fetch"
	PipelineFailedMdexport = "failed:mdexport"
	PipelineFailedSummarize = "failed:summarize"
	PipelineFailedImapush  = "failed:imapush"
)

// Pipeline 阶段名常量。和 shouldRunStage / MarkPipelineFailed 配套。
//
// 注意：stage 名是状态机的"另一面"——状态用 failed:<stage> 编码，
// stage 名就是编码后缀。这里集中定义，避免 stage 字符串散落各处的拼写错误。
const (
	StageFetch     = "fetch"
	StageMdexport  = "mdexport"
	StageSummarize = "summarize"
	StageImapush   = "imapush"
)

// pipelineOrder 阶段推进顺序。advanceOne 遍历这个顺序，按 shouldRunStage 决定跑哪一段。
//
// 注意：顺序是写死的，不做配置化——四阶段是这条 pipeline 的本质。
var pipelineOrder = []string{StageFetch, StageMdexport, StageSummarize, StageImapush}

// nextStatusAfter stage 成功推进后，文章应该进入的状态。
//
// 失败时不会调到这里；advanceOne 在 stage.Run 返回 Err 时直接调 MarkPipelineFailed。
func nextStatusAfter(stage string) string {
	switch stage {
	case StageFetch:
		return PipelineFetched
	case StageMdexport:
		return PipelineMDExported
	case StageSummarize:
		return PipelineSummarized
	case StageImapush:
		return PipelinePushed
	}
	return ""
}

// shouldRunStage 给定当前 pipeline_status，决定 stage 是否应该被本轮 advance 触发。
//
// 规则：
//   pending → fetch
//   fetched → mdexport
//   md_exported → summarize
//   summarized → imapush
//   failed:fetch → fetch（重试）
//   failed:mdexport → mdexport（重试）
//   failed:summarize → summarize（重试）
//   failed:imapush → imapush（重试）
//   pushed / 其他 → 不跑任何 stage
func shouldRunStage(current, stage string) bool {
	switch stage {
	case StageFetch:
		return current == PipelinePending || current == PipelineFailedFetch
	case StageMdexport:
		return current == PipelineFetched || current == PipelineFailedMdexport
	case StageSummarize:
		return current == PipelineMDExported || current == PipelineFailedSummarize
	case StageImapush:
		return current == PipelineSummarized || current == PipelineFailedImapush
	}
	return false
}

// StageResult 单阶段执行的返回值。
//
// Err 不为空表示本阶段失败，Worker 写 failed:<stage> + pipeline_error 然后停步。
// Next 不为空表示本阶段成功，Worker 写新 status。
// （如果两者都为空——不该发生——视为 no-op，Worker 仅刷新 updated_at）
type StageResult struct {
	Err error
}

// pipelineStage 是 4 个 stage 之一的方法签名，由 *Service 实现。
//
// 拿到 *Article（已含 pipeline_status）后，判断是不是自己的活，是就跑。
// 这里 stage 自己也要做 shouldRunStage 二次校验（防止 Worker 状态计算和 stage
// 实际行为不一致造成越权写入）。
type pipelineStage interface {
	Name() string
	Run(ctx context.Context, a *Article) StageResult
}

// namedStage 是 pipelineStage 的函数式适配。
//
// stage 实现是 *Service 的方法，方法名不带 Name()；用 namedStage 包一层
// 让 stage 注册表能按 name 索引。如果将来 stage 多到需要切换为接口实现，
// 把 namedStage 删掉、把 Service 方法改成 struct 即可。
type namedStage struct {
	name string
	fn   func(context.Context, *Article) StageResult
}

func (s namedStage) Name() string { return s.name }
func (s namedStage) Run(ctx context.Context, a *Article) StageResult {
	return s.fn(ctx, a)
}

// ----- Worker -----

// WorkerConfig Worker 启动参数。Service.Start 时一次性传入。
type WorkerConfig struct {
	Enabled  bool          // 默认 false；不开 Worker = 不自动推进
	Interval time.Duration // 默认 5min，ticker 间隔
	Batch    int           // 默认 20，每 tick 最多推多少篇
}

// Worker 推进 pipeline 的后台 goroutine。
//
// 单 goroutine 设计：worker 串行处理 batch 内每篇文章，保证一篇 article 在一个
// tick 内被吃到底，不会有两个 worker 抢同一篇的中间状态。batch=20 / interval=5min
// 意味着稳态下每 5min 推进 20 篇（受各 stage 实际耗时影响）。
type Worker struct {
	svc      *Service
	cfg      WorkerConfig
	trigger  chan struct{}
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// NewWorker 构造 Worker。构造不会启动 goroutine——必须显式 Start。
func NewWorker(svc *Service, cfg WorkerConfig) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 20
	}
	return &Worker{
		svc:     svc,
		cfg:     cfg,
		trigger: make(chan struct{}, 1),
	}
}

// Start 启动后台循环。
//
// ctx 是父 context，cancel 时 worker 退出。建议用 Service.bgCtx。
// 首次 tick 在 Interval 后立即触发，避免和刚刚的 sync 撞车。
func (w *Worker) Start(ctx context.Context) {
	if !w.cfg.Enabled {
		log.Info().Msg("bizhub: pipeline worker disabled (BizWorkerEnabled=false)")
		return
	}
	if w.cancel != nil {
		log.Warn().Msg("bizhub: pipeline worker already started")
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()

		log.Info().
			Dur("interval", w.cfg.Interval).
			Int("batch", w.cfg.Batch).
			Msg("bizhub: pipeline worker started")

		// 首次延迟跑，避免和刚启动的 sync / resumeInterruptedExportJobs 撞。
		// 同时给 HTTP server 一点时间 ready，/admin/pipeline/status 在这个窗口
		// 里返回的 in-flight=0 不会让用户误以为"worker 没起"。
		select {
		case <-runCtx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		ticker := time.NewTicker(w.cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-runCtx.Done():
				log.Info().Msg("bizhub: pipeline worker stopped")
				return
			case <-ticker.C:
				w.RunOnce(runCtx)
			case <-w.trigger:
				w.RunOnce(runCtx)
			}
		}
	}()
}

// Stop 停止 worker。等当前正在跑的 stage 跑完（或 ctx 到期）才返回。
//
// 幂等：多次调用安全。
func (w *Worker) Stop() {
	w.stopOnce.Do(func() {
		if w.cancel != nil {
			w.cancel()
		}
		w.wg.Wait()
	})
}

// Trigger 主动触发一次 RunOnce。
//
// 用于 /admin/pipeline/trigger HTTP endpoint 和「手动 retry 一篇」的立即响应。
// 用 buffered chan(1) 做 non-blocking trigger，多次 Trigger 在 chan 满时被合并为一次。
func (w *Worker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

// RunOnce 推一批文章走完 pipeline。
//
// 流程：拉 batch → 逐篇 advanceOne（fetch → mdexport → summarize → imapush 失败即停）。
// 每篇独立 SQL 提交（Store.AdvancePipeline / MarkPipelineFailed 各自是单条 UPDATE），
// 因此 batch 内某一篇失败不会影响其他篇。
func (w *Worker) RunOnce(ctx context.Context) {
	batch, err := w.svc.store.PickPipelineBatch(ctx, w.cfg.Batch)
	if err != nil {
		log.Warn().Err(err).Msg("bizhub: pipeline pick batch failed")
		return
	}
	if len(batch) == 0 {
		return
	}

	log.Info().Int("count", len(batch)).Msg("bizhub: pipeline tick start")

	for i := range batch {
		select {
		case <-ctx.Done():
			log.Info().Msg("bizhub: pipeline tick aborted by ctx")
			return
		default:
		}
		w.advanceOne(ctx, &batch[i])
	}

	log.Info().Int("count", len(batch)).Msg("bizhub: pipeline tick done")
}

// advanceOne 把单篇文章按状态推进到下一个 stage（失败即停）。
//
// 设计：单 article 在一个 goroutine 内串行吃到底。fallthrough 控制流让代码极简——
// switch 的 case 没有 break，每个 case 跑完直接进下一个 case。如果某个 stage 失败，
// 写 failed:<stage> 然后 return，下一次 case 不再跑。
func (w *Worker) advanceOne(ctx context.Context, a *Article) {
	log.Debug().Int64("article", a.ID).Str("from", a.PipelineStatus).Msg("bizhub: advance start")

	for _, stageName := range pipelineOrder {
		if !shouldRunStage(a.PipelineStatus, stageName) {
			continue
		}

		stage := w.stageByName(stageName)
		if stage == nil {
			log.Warn().Str("stage", stageName).Msg("bizhub: stage not registered, skipping")
			continue
		}

		log.Info().Int64("article", a.ID).Str("stage", stageName).Str("from", a.PipelineStatus).Msg("bizhub: stage start")

		result := stage.Run(ctx, a)
		if result.Err != nil {
			log.Warn().Int64("article", a.ID).Str("stage", stageName).Err(result.Err).Msg("bizhub: stage failed")
			if mfErr := w.svc.store.MarkPipelineFailed(ctx, a.ID, stageName, result.Err.Error()); mfErr != nil {
				log.Error().Err(mfErr).Int64("article", a.ID).Msg("bizhub: mark pipeline failed (write error)")
			}
			return
		}

		next := nextStatusAfter(stageName)
		if next == "" {
			log.Warn().Int64("article", a.ID).Str("stage", stageName).Msg("bizhub: stage has no next status, treat as success but no advance")
			return
		}

		if err := w.svc.store.AdvancePipeline(ctx, a.ID, next); err != nil {
			log.Error().Err(err).Int64("article", a.ID).Str("to", next).Msg("bizhub: advance pipeline (write error)")
			return
		}

		a.PipelineStatus = next
		log.Info().Int64("article", a.ID).Str("stage", stageName).Str("to", next).Msg("bizhub: stage ok")
	}

	log.Debug().Int64("article", a.ID).Str("final", a.PipelineStatus).Msg("bizhub: advance done")
}

// stageByName 拿 stage 实例。目前 4 个 stage 都是 *Service 的方法，集中 switch 一份。
func (w *Worker) stageByName(name string) pipelineStage {
	switch name {
	case StageFetch:
		return namedStage{name: StageFetch, fn: w.svc.fetchStage}
	case StageMdexport:
		return namedStage{name: StageMdexport, fn: w.svc.mdexportStage}
	case StageSummarize:
		return namedStage{name: StageSummarize, fn: w.svc.summarizeStage}
	case StageImapush:
		return namedStage{name: StageImapush, fn: w.svc.imapushStage}
	}
	return nil
}

// ----- 4 个 stage 实现 -----
//
// 全部以 *Service 方法形式存在，方便共享 Service 上的 store / llm / config / 懒初始化的 exporter。
// 每个 stage 只做一件事：调底层 API + 把结果写到对应 cache 表。pipeline_status 由 Worker 统一管。

// fetchStage 通过 fetcher 抓 URL 解析 markdown，写入 biz_article_contents。
func (s *Service) fetchStage(ctx context.Context, a *Article) StageResult {
	if a.URL == "" {
		return StageResult{Err: errors.New("article has no URL")}
	}

	opts := NewDefaultFetchOptions()
	opts.Timeout = 15 * time.Second

	title, content, bytes, err := FetchAndExtract(ctx, a.URL, opts)
	if err != nil {
		return StageResult{Err: fmt.Errorf("fetch: %w", err)}
	}

	urlHash := HashURL(a.URL)
	status := 1
	errStr := ""
	if err != nil {
		status = 0
		errStr = err.Error()
	}
	if writeErr := s.store.UpsertContent(urlHash, a.URL, title, content, status, errStr, bytes); writeErr != nil {
		return StageResult{Err: fmt.Errorf("fetch: upsert content: %w", writeErr)}
	}
	return StageResult{}
}

// mdexportStage 调用外部 bash 脚本导 MD 文件，写入 biz_exported_articles。
//
// 复用 Service.getExporter() 的懒初始化：第一次调用时才检查配置并 New。
// 如果未配置（脚本路径 / 输出目录缺失），stage 报"mdexport not configured"并失败。
// Worker 看到错误会写 failed:mdexport，用户在管理页能看到原因再去配。
func (s *Service) mdexportStage(ctx context.Context, a *Article) StageResult {
	if a.URL == "" {
		return StageResult{Err: errors.New("article has no URL")}
	}

	exporter, err := s.getExporter()
	if err != nil {
		return StageResult{Err: fmt.Errorf("mdexport: init: %w", err)}
	}
	if exporter == nil {
		return StageResult{Err: errors.New("mdexport not configured (md_export_script / md_export_dir missing)")}
	}

	result, err := exporter.Export(ctx, a.URL)
	if err != nil {
		return StageResult{Err: fmt.Errorf("mdexport: %w", err)}
	}

	// 写归档记录（status='exported'，kind 空表示成功）。
	if err := s.store.UpsertExportRecord(a.ID, a.URL, result.MDPath, "", ExportStatusExported, "", ""); err != nil {
		return StageResult{Err: fmt.Errorf("mdexport: upsert record: %w", err)}
	}
	return StageResult{}
}

// summarizeStage 调 LLM 生成单篇文章摘要，写 biz_exported_articles.summary_path。
//
// 复用 Service.GenerateArticleSummary（已存在）。注意 llm 为 nil 时 stage 立刻失败——
// 与 SummarizePipeline 期望一致（LLM 没配就别推）。
func (s *Service) summarizeStage(ctx context.Context, a *Article) StageResult {
	if s.llm == nil {
		return StageResult{Err: errors.New("summarize: llm not configured (llm_api_key missing)")}
	}
	if err := s.GenerateArticleSummary(ctx, a.ID, s.llm); err != nil {
		return StageResult{Err: fmt.Errorf("summarize: %w", err)}
	}
	return StageResult{}
}

// imapushStage 把文章 URL 推到 IMA 知识库，写 biz_exported_articles.pushed_* 字段。
//
// 复用 Service.PushArticle（已存在，链式校验 IsArticleExported）。pipeline 走到这里
// 时上一阶段 mdexport 已经成功，所以 IsArticleExported 一定 true，不会触发 409 链式失败。
func (s *Service) imapushStage(ctx context.Context, a *Article) StageResult {
	if a.URL == "" {
		return StageResult{Err: errors.New("article has no URL")}
	}

	// PushArticle 内部已经会检查 imapush 是否配置；为了 stage 失败信息更清楚，
	// 这里先做一次显式检查。
	if !s.IsPushConfigured() {
		return StageResult{Err: errors.New("imapush not configured (ima_push_skill_dir / ima_push_kb_id missing)")}
	}

	if _, err := s.PushArticle(ctx, a.ID); err != nil {
		return StageResult{Err: fmt.Errorf("imapush: %w", err)}
	}
	return StageResult{}
}
