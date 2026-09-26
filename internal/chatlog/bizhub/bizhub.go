// Package bizhub 提供公众号文章汇总功能。
// 从微信本地 biz_message_0.db 解析公众号文章数据，按 ghid 分类持久化到独立 SQLite，
// 并通过 HTTP API 和 Web UI 提供查询和展示。
package bizhub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/chenliitaz/chatlog/internal/chatlog/bizhub/imapush"
	"github.com/chenliitaz/chatlog/internal/chatlog/bizhub/mdexport"
	"github.com/chenliitaz/chatlog/internal/wechatdb"
)

// Config bizhub 可选的配置源（用于 summary 抓取并发等覆写）。可注入 nil。
type Config interface {
	GetSummaryFetchContent() bool
	GetSummaryFetchConcurrency() int
	GetLLMMaxTokens() int
	GetFeedSummaryCacheHours() int
	GetMDExportDir() string
	GetMDExportScript() string
	// GetMDExportConcurrency 批量归档并发数；<=0 时调用方回退到默认 3，上限 8
	GetMDExportConcurrency() int
	// IMA 推送（v1）
	GetIMAPushSkillDir() string
	GetIMAPushKBID() string
	GetIMAPushFolderID() string
	GetIMAPushConcurrency() int
	// LLM 客户端构造参数（worker.summarizeStage 需要）。
	// 由 Config 实现：ctx.Context 返回 TUI 内存配置；conf.ServerConfig 返回
	// 持久化的 server 配置。两边都返回空字符串时 worker 会跑、summarize stage
	// 立刻报 "llm not configured" 并写 failed:summarize（不致命，可以重试）。
	GetLLMBaseURL() string
	GetLLMAPIKey() string
	GetLLMModel() string
	// Pipeline Worker（PR1）
	GetBizWorkerEnabled() bool
	// GetBizWorkerInterval 返回 ticker 间隔（秒）。<=0 时 Worker 回退到默认 300s。
	GetBizWorkerInterval() int
	// GetBizWorkerBatchSize 返回每 tick 处理的最多文章数。<=0 时 Worker 回退到默认 20。
	GetBizWorkerBatchSize() int
}

// Service 公众号文章汇总服务
type Service struct {
	store    *Store
	syncer   *Syncer
	workDir  string
	config   Config
	exporter *mdexport.Exporter // lazy init
	pusher   *imapush.Exporter  // lazy init

	// llm 是 LLM 客户端（summarize stage 需要）。由外部在 SetConfig 之后
	// 调 SetLLM 注入；为空时 summarize stage 立刻报 "llm not configured"，
	// 其他 stage 不受影响（fetch / mdexport / imapush 不需要 LLM）。
	llm *LLMClient

	// worker 是 PR1 引入的 pipeline 后台推进器。在 Start() 末尾启动，
	// Stop() 开头停。Config nil 或 BizWorkerEnabled=false 时不启动。
	worker *Worker

	mu         sync.RWMutex
	syncing    bool
	lastResult *SyncResult

	// 轻量缓存：只缓存公众号列表和标签（数据量小）
	cacheMu  sync.RWMutex
	accounts []Account
	tags     []Tag

	// 归档任务编排。同一时刻只允许一个批量归档任务在跑 —— 导出是重磁盘 / 重网络
	// 操作，并发跑多个任务只会互相抢抓取器和限流额度，反而更容易触发风控。
	jobMu     sync.Mutex
	activeJob string
	jobCancel context.CancelFunc
	jobCtx    context.Context

	// 推送任务编排。与归档任务独立的并发槽位 —— push 候选集是「已 export」，
	// 不会跟 export 抢资源；同时跑一个 export + 一个 push 是允许的。
	pushJobMu     sync.Mutex
	activePushJob string
	pushJobCancel context.CancelFunc
	pushJobCtx    context.Context

	// bgCtx 是任务级上下文：批量归档在 HTTP 请求返回之后继续跑，所以不能挂
	// 在请求的 ctx 上（请求结束就会取消）。页面关掉、用户走人都不该打断归档。
	bgCtx    context.Context
	bgCancel context.CancelFunc
}

// NewService 创建公众号汇总服务
func NewService(wechatDB *wechatdb.DB, workDir string) (*Service, error) {
	store, err := Open(workDir)
	if err != nil {
		return nil, err
	}

	syncer := NewSyncer(wechatDB, store, workDir)

	svc := &Service{
		store:   store,
		syncer:  syncer,
		workDir: workDir,
	}
	svc.bgCtx, svc.bgCancel = context.WithCancel(context.Background())

	// 只加载轻量数据（公众号列表 + 标签）
	svc.refreshLightCache()

	return svc, nil
}

// refreshLightCache 只刷新公众号列表和标签（不加载文章）
func (s *Service) refreshLightCache() {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	accounts, err := s.store.GetAccounts()
	if err != nil {
		return
	}
	s.accounts = accounts

	tags, err := s.store.GetTags()
	if err != nil {
		return
	}
	s.tags = tags
}

// Start 启动服务（先修复上次残留的归档/推送任务，再后台执行全量同步，再启动 pipeline worker）
//
// Worker 启动放在 sync 之后：第一次 sync 会写入大量新行（pipeline_status='pending'），
// worker 立刻开始推会与 sync 撞上同一批文章的 status。sync 完成后 worker 起来，
// 顺序更可预测。
func (s *Service) Start() {
	s.resumeInterruptedExportJobs()
	s.resumeInterruptedPushJobs()

	go func() {
		log.Info().Msg("bizhub: starting full sync")
		result, err := s.SyncAll()
		if err != nil {
			log.Error().Err(err).Msg("bizhub: full sync failed")
		} else {
			log.Info().
				Int("accounts", len(result.Accounts)).
				Int("totalArticles", result.TotalArticles).
				Int("newArticles", result.NewArticles).
				Msg("bizhub: full sync completed")

			s.refreshLightCache()
			s.InitTags()
		}

		// sync 完成（不论成败）再起 worker —— sync 失败时 worker 仍然有用，
		// 至少能把失败前已经写入的少数行推下去。
		s.startPipelineWorker()
	}()
}

// Stop 停止服务（先停 pipeline worker → 取消在跑的归档任务 → 取消推送任务 → 关库）
//
// 顺序很重要：worker 是周期性抓批、可能正在写 stage，必须先停避免新推进；
// 然后再取消归档/推送长任务；最后关库。
func (s *Service) Stop() error {
	if s.worker != nil {
		s.worker.Stop()
		s.worker = nil
	}
	s.CancelExportJob()
	if s.bgCancel != nil {
		s.bgCancel()
	}
	return s.store.Close()
}

// startPipelineWorker 根据 Config 构造并启动 Worker。
//
// 从 Config 读 Enabled / Interval / BatchSize，三者都缺省有合理回退（见 NewWorker）。
// 想要"迁移期不开 Worker"的运维场景：把 BizWorkerEnabled 设为 false 即可。
//
// LLM 在这里按需构造：worker.summarizeStage 需要 LLM；外部（http.Service / manager）
// 已经构造过 LLM 并 SetLLM 进来的话就复用；否则从 Config 读 api_key 自造。
// 重复构造的成本只是一个 LLMClient 结构体（stateless HTTP client），接受这个微小
// 的重复以换取 wiring 的简单。
func (s *Service) startPipelineWorker() {
	if s.config == nil {
		log.Info().Msg("bizhub: pipeline worker skipped (no config)")
		return
	}

	if s.llm == nil && s.config.GetLLMAPIKey() != "" {
		s.llm = NewLLMClient(
			s.config.GetLLMBaseURL(),
			s.config.GetLLMAPIKey(),
			s.config.GetLLMModel(),
		)
	}

	cfg := WorkerConfig{
		Enabled: s.config.GetBizWorkerEnabled(),
	}
	if d := s.config.GetBizWorkerInterval(); d > 0 {
		cfg.Interval = time.Duration(d) * time.Second
	}
	if b := s.config.GetBizWorkerBatchSize(); b > 0 {
		cfg.Batch = b
	}

	s.worker = NewWorker(s, cfg)
	s.worker.Start(s.bgCtx)
}

// SetConfig 注入可选配置源（用于 summary 抓取/LLM 调用的覆写）。可传 nil。
func (s *Service) SetConfig(cfg Config) {
	s.config = cfg
}

// SetLLM 注入 LLM 客户端（summarize stage 用）。
//
// 必须在 Start() 之前调用；调用后 summarize stage 才会有 LLM 可用。
// 多次调用以最后一次为准。
func (s *Service) SetLLM(llm *LLMClient) {
	s.llm = llm
}

// LLM 返回注入的 LLM 客户端（可能被 worker / handler 调用）。
func (s *Service) LLM() *LLMClient {
	return s.llm
}

// Worker 返回 pipeline worker。给 handler 用来 Trigger / 拿配置。
// worker 为 nil 表示 worker 未启动（比如 TUI 模式），handler 应自行判断。
func (s *Service) Worker() *Worker {
	return s.worker
}

// Store 返回底层 store
func (s *Service) Store() *Store {
	return s.store
}

// SyncAll 全量同步（线程安全）
func (s *Service) SyncAll() (*SyncResult, error) {
	s.mu.Lock()
	if s.syncing {
		s.mu.Unlock()
		return s.lastResult, nil
	}
	s.syncing = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.syncing = false
		s.mu.Unlock()
	}()

	result, err := s.syncer.SyncAll()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.lastResult = result
	s.mu.Unlock()

	return result, nil
}

// SyncOne 同步单个公众号
func (s *Service) SyncOne(ghid string) (*AccountSyncResult, error) {
	return s.syncer.SyncOne(ghid)
}

// IsSyncing 返回是否正在同步
func (s *Service) IsSyncing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.syncing
}

// LastResult 返回最后一次同步结果
func (s *Service) LastResult() *SyncResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastResult
}

// GetAccounts 获取公众号列表（缓存）
func (s *Service) GetAccounts() []Account {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.accounts == nil {
		return []Account{}
	}
	return s.accounts
}

// GetAllAccounts 获取所有公众号（含隐藏，不缓存）
func (s *Service) GetAllAccounts() ([]Account, error) {
	return s.store.GetAllAccounts()
}

// SetAccountsHidden 批量设置公众号隐藏状态
func (s *Service) SetAccountsHidden(ghIDs []string, hidden bool) error {
	if err := s.store.SetAccountsHidden(ghIDs, hidden); err != nil {
		return err
	}
	s.refreshLightCache()
	return nil
}

// SetAccountsWatched 批量设置公众号关注状态
func (s *Service) SetAccountsWatched(ghIDs []string, watched bool) error {
	if err := s.store.SetAccountsWatched(ghIDs, watched); err != nil {
		return err
	}
	s.refreshLightCache()
	return nil
}

// GetFeedArticles 获取关注公众号近 days 天的文章流（days<=0 时默认 7 天）
func (s *Service) GetFeedArticles(days, limit, offset int) ([]Article, error) {
	if days <= 0 {
		days = 7
	}
	return s.store.GetFeedArticles(days, limit, offset)
}

// GetBookmarkedArticles 获取已收藏文章列表
func (s *Service) GetBookmarkedArticles(limit, offset int) ([]Article, error) {
	return s.store.GetBookmarkedArticles(limit, offset)
}

// ToggleArticleBookmark 设置单篇文章收藏状态
func (s *Service) ToggleArticleBookmark(id int64, bookmarked bool) error {
	return s.store.SetArticlesBookmarked([]int64{id}, bookmarked)
}

// MarkArticleRead 标记单篇文章已读/未读
func (s *Service) MarkArticleRead(id int64, read bool) error {
	return s.store.SetArticlesRead([]int64{id}, read)
}

// BatchMarkRead 批量标记文章已读。ids 非空时按 ID 列表；否则按 ghid+days 范围。
func (s *Service) BatchMarkRead(ids []int64, ghid string, days int) (int64, error) {
	if len(ids) > 0 {
		if err := s.store.SetArticlesRead(ids, true); err != nil {
			return 0, err
		}
		return int64(len(ids)), nil
	}
	return s.store.MarkAllRead(ghid, days)
}

// ListSummaries 获取汇总列表（不含正文）
func (s *Service) ListSummaries() ([]Summary, error) {
	return s.store.ListSummaries()
}

// GetSummary 获取单条汇总（含正文）
func (s *Service) GetSummary(id int64) (*Summary, error) {
	return s.store.GetSummary(id)
}

// GetArticles 按需从 DB 查询文章（懒加载）：单个公众号的全部文章
func (s *Service) GetArticles(ghid string, limit, offset int) ([]Article, error) {
	return s.store.GetArticles(ghid, limit, offset)
}

// ListArticles 按「账号 + 时间窗」取文章，两个条件都可以为空。
// 供「动态 / 全部」双模式共用：动态模式带关注+时间窗走 GetFeedArticles，
// 全部模式（ghid 空、days 空）走这里当资料库翻旧文。
func (s *Service) ListArticles(ghid string, days, limit, offset int) ([]Article, error) {
	return s.store.ListArticles(ArticleFilter{GHID: ghid, Days: days, Limit: limit, Offset: offset})
}

// ListArticlesWithFilter 按完整过滤条件取文章（含未读筛选）
func (s *Service) ListArticlesWithFilter(f ArticleFilter) ([]Article, error) {
	return s.store.ListArticles(f)
}

// CountListArticles 与 ListArticles 同条件的总数，查询失败时返回 0
func (s *Service) CountListArticles(ghid string, days int) int {
	n, _ := s.store.CountListArticles(ArticleFilter{GHID: ghid, Days: days})
	return n
}

// CountListArticlesWithFilter 按完整过滤条件统计总数
func (s *Service) CountListArticlesWithFilter(f ArticleFilter) int {
	n, _ := s.store.CountListArticles(f)
	return n
}

// GetArticle 获取单篇文章
func (s *Service) GetArticle(id int64) (*Article, error) {
	return s.store.GetArticle(id)
}

// GetArticleCount 按需从 DB 查询总数
func (s *Service) GetArticleCount() int {
	n, _ := s.store.GetArticleCount()
	return n
}

// CountArticles 某公众号的文章总数（分页用），查询失败时返回 0
func (s *Service) CountArticles(ghid string) int {
	n, _ := s.store.CountArticles(ghid)
	return n
}

// CountSearchArticles 搜索结果总数（分页用），查询失败时返回 0
func (s *Service) CountSearchArticles(keyword string) int {
	n, _ := s.store.CountSearchArticles(keyword)
	return n
}

// CountFeedArticles 关注动态总数（分页用），查询失败时返回 0
func (s *Service) CountFeedArticles(days int) int {
	n, _ := s.store.CountFeedArticles(days)
	return n
}

// CountBookmarkedArticles 已收藏文章总数（分页用），查询失败时返回 0
func (s *Service) CountBookmarkedArticles() int {
	n, _ := s.store.CountBookmarkedArticles()
	return n
}

// SearchArticles 按需从 DB 搜索
func (s *Service) SearchArticles(keyword string, limit, offset int) ([]Article, error) {
	return s.store.SearchArticles(keyword, limit, offset)
}

// GetTags 获取所有标签（缓存）
func (s *Service) GetTags() []Tag {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.tags == nil {
		return []Tag{}
	}
	return s.tags
}

// CreateTag 创建标签
func (s *Service) CreateTag(name, color string) (*Tag, error) {
	tag, err := s.store.CreateTag(name, color)
	if err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	s.tags = append(s.tags, *tag)
	s.cacheMu.Unlock()
	return tag, nil
}

// UpdateTag 更新标签
func (s *Service) UpdateTag(id int64, name, color string) error {
	if err := s.store.UpdateTag(id, name, color); err != nil {
		return err
	}
	s.cacheMu.Lock()
	tags, _ := s.store.GetTags()
	if tags != nil {
		s.tags = tags
	}
	s.cacheMu.Unlock()
	return nil
}

// DeleteTag 删除标签
func (s *Service) DeleteTag(id int64) error {
	if err := s.store.DeleteTag(id); err != nil {
		return err
	}
	s.cacheMu.Lock()
	var newTags []Tag
	for _, t := range s.tags {
		if t.ID != id {
			newTags = append(newTags, t)
		}
	}
	s.tags = newTags
	s.cacheMu.Unlock()
	return nil
}

// SetAccountTags 覆盖式设置公众号标签（保留旧语义，等价于 replace）
func (s *Service) SetAccountTags(ghid string, tagIDs []int64) error {
	return s.store.SetAccountTags(ghid, tagIDs)
}

// SetAccountTagsMode 按指定模式设置单个公众号的标签
func (s *Service) SetAccountTagsMode(ghid string, tagIDs []int64, mode TagAssignMode) error {
	return s.store.SetAccountTagsMode(ghid, tagIDs, mode)
}

// SetAccountsTagsMode 按指定模式批量设置多个公众号的标签（单事务）
func (s *Service) SetAccountsTagsMode(ghids []string, tagIDs []int64, mode TagAssignMode) error {
	return s.store.SetAccountsTagsMode(ghids, tagIDs, mode)
}

// GetAccountTags 按需从 DB 查询公众号标签
func (s *Service) GetAccountTags(ghid string) ([]Tag, error) {
	return s.store.GetAccountTags(ghid)
}

// GetAccountsByTag 按需从 DB 查询标签下的公众号
func (s *Service) GetAccountsByTag(tagID int64) ([]Account, error) {
	return s.store.GetAccountsByTag(tagID)
}

// GetAllAccountTags 批量获取所有公众号的标签（SSR 用，一次查询）
func (s *Service) GetAllAccountTags() map[string][]Tag {
	result, err := s.store.GetAllAccountTagsMap()
	if err != nil {
		return make(map[string][]Tag)
	}
	return result
}

// InitTags 初始化默认标签并自动分类
func (s *Service) InitTags() {
	log.Info().Msg("bizhub: initializing default tags")
	if err := s.store.InitDefaultTags(); err != nil {
		log.Error().Err(err).Msg("bizhub: failed to init default tags")
		return
	}

	log.Info().Msg("bizhub: auto-tagging accounts")
	if err := s.store.AutoTagAccounts(); err != nil {
		log.Error().Err(err).Msg("bizhub: failed to auto-tag accounts")
		return
	}

	// 刷新标签缓存
	s.cacheMu.Lock()
	tags, _ := s.store.GetTags()
	if tags != nil {
		s.tags = tags
	}
	s.cacheMu.Unlock()

	log.Info().Msg("bizhub: tags initialized and accounts tagged")
}

// SummaryRequest GenerateSummary 的入参
type SummaryRequest struct {
	Days        int    `json:"days"`
	Instruction string `json:"instruction,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

const summarySystemPrompt = "summary_system" // see prompts/summary_system.md

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// firstLine 截取 s 的第一行（按 \n 切），截到 max runes。
//
// daily digest 在 LLM 没返回 JSON 时用这个当 headline 兜底。LLM 偶尔会先吐一句
// "以下是..." 的引导句再吐 JSON，那种情况下用 firstLine 也比整段塞进 headline
// 干净。
func firstLine(s string, max int) string {
	if s == "" {
		return ""
	}
	for i, r := range s {
		if r == '\n' {
			s = s[:i]
			break
		}
	}
	return truncateRunes(s, max)
}

// GenerateSummary 抓取关注公众号近期文章、调用 LLM 生成结构化汇总并落库。
func (s *Service) GenerateSummary(ctx context.Context, req SummaryRequest, llm *LLMClient) (*Summary, error) {
	if llm == nil {
		return nil, ErrLLMNotConfigured
	}
	days := req.Days
	if days <= 0 {
		days = 1
	}
	scope := req.Scope
	if scope == "" {
		scope = "watched"
	}
	if scope != "watched" {
		return nil, fmt.Errorf("bizhub: scope %q not implemented yet", scope)
	}

	since := time.Now().AddDate(0, 0, -days)
	articles, err := s.store.GetWatchedArticlesSince(since, 30)
	if err != nil {
		return nil, err
	}
	if len(articles) == 0 {
		return nil, fmt.Errorf("%w: 最近 %d 天没有关注公众号的文章", ErrNoWatchedArticles, days)
	}

	// 计算 urlHash，复用已抓取的正文
	hashes := make([]string, 0, len(articles))
	hashToArticle := make(map[string]*Article, len(articles))
	for i := range articles {
		h := HashURL(articles[i].URL)
		hashes = append(hashes, h)
		hashToArticle[h] = &articles[i]
	}

	cached, err := s.store.GetContentByHashes(hashes)
	if err != nil {
		return nil, err
	}

	// 已缓存且 30 天内有效的 status=1 复用；其余入 NeedFetch
	thirtyDaysAgo := time.Now().Add(-30 * 24 * time.Hour).Unix()
	needFetchURLs := make([]string, 0)
	needFetchHashes := make(map[string]string)
	for _, h := range hashes {
		entry, ok := cached[h]
		skip := ok && entry.Status == 1 && entry.FetchedAt > thirtyDaysAgo && entry.Content != ""
		if !skip {
			art := hashToArticle[h]
			if art == nil || art.URL == "" {
				continue
			}
			needFetchURLs = append(needFetchURLs, art.URL)
			needFetchHashes[art.URL] = h
		}
	}

	fetchOpts := NewDefaultFetchOptions()
	fetchOpts.Timeout = 8 * time.Second
	if s.config != nil {
		if c := s.config.GetSummaryFetchConcurrency(); c > 0 {
			fetchOpts.Concurrency = c
		}
	}

	fetched := map[string]FetchResult{}
	if len(needFetchURLs) > 0 {
		results, ferr := FetchBatch(ctx, needFetchURLs, fetchOpts)
		if ferr != nil {
			return nil, ferr
		}
		fetched = results
	}

	for url, h := range needFetchHashes {
		fr, ok := fetched[h]
		if !ok {
			continue
		}
		status := 1
		errStr := ""
		if fr.Err != nil {
			status = 0
			errStr = fr.Err.Error()
		}
		if err := s.store.UpsertContent(h, url, fr.Title, fr.Content, status, errStr, fr.Bytes); err != nil {
			log.Warn().Err(err).Str("url", url).Msg("bizhub: upsert content failed")
		}
	}

	// 重新加载最新的内容（含刚抓取的）
	cached, err = s.store.GetContentByHashes(hashes)
	if err != nil {
		return nil, err
	}

	// 拼装 user prompt
	var sb strings.Builder
	fmt.Fprintf(&sb, "以下是最近 %d 天我关注的微信公众号发布的 %d 篇文章（含公众号名、标题、时间、摘要、链接、以及部分抓取的正文）：\n\n", days, len(articles))
	for i, a := range articles {
		h := HashURL(a.URL)
		entry := cached[h]
		body := ""
		if entry.Content != "" {
			body = truncateRunes(entry.Content, 1500)
		} else if entry.Error != "" {
			body = fmt.Sprintf("(未抓取：%s)", entry.Error)
		} else {
			body = "(未抓取)"
		}
		fmt.Fprintf(&sb, "### [%d] %s · %s\n", i+1, a.GHName, a.Title)
		fmt.Fprintf(&sb, "- 发布：%s\n", time.Unix(a.PublishedAt, 0).Format("2006-01-02 15:04"))
		fmt.Fprintf(&sb, "- 摘要：%s\n", truncateRunes(a.Desc, 100))
		fmt.Fprintf(&sb, "- 链接：%s\n", a.URL)
		fmt.Fprintf(&sb, "- 正文：%s\n\n", body)
	}
	if req.Instruction != "" {
		fmt.Fprintf(&sb, "用户指令：%s\n\n", req.Instruction)
	}
	sb.WriteString("请按上面的 schema 输出 JSON。")

	systemPrompt, err := LoadPrompt(summarySystemPrompt)
	if err != nil {
		return nil, fmt.Errorf("bizhub: load summary prompt: %w", err)
	}
	text, structured, inTok, outTok, err := llm.CompleteStructured(ctx, systemPrompt, sb.String())
	if err != nil {
		return nil, err
	}
	if len(structured) == 0 {
		log.Warn().Msg("bizhub: llm output not parseable as json, fallback to markdown content")
	}

	content := text
	if len(structured) == 0 {
		// 保留 markdown 作为兑底
		structured = nil
	}

	// 统计成功抓取的条数：cached 已经包含本次新抓取的结果
	fetchedCount := 0
	for _, e := range cached {
		if e.Status == 1 && e.Content != "" {
			fetchedCount++
		}
	}
	log.Info().Int("cachedMapLen", len(cached)).Int("needFetchURLs", len(needFetchURLs)).Int("finalFetchedCount", fetchedCount).Msg("bizhub: fetch stats")

	model := ""
	if llm != nil {
		model = llm.Model()
	}

	summary, err := s.store.CreateSummary(days, len(articles), content, structured, req.Instruction, scope, model, inTok, outTok, fetchedCount)
	if err != nil {
		return nil, err
	}
	return summary, nil
}

// FeedSummary 关注动态页面的结构化 LLM 汇总（服务端缓存 + 旁路刷新）
type FeedSummary struct {
	WindowDays int             `json:"windowDays"`
	Headline   string          `json:"headline"`
	Structured json.RawMessage `json:"structured"`
	ComputedAt int64           `json:"computedAt"`
	FromCache  bool            `json:"fromCache"`
}

const defaultFeedSummaryCacheHours = 4

const feedSummarySystemPrompt = "feed_summary_system" // see prompts/feed_summary_system.md

func (s *Service) feedSummaryCacheHours() int {
	if s.config != nil {
		if h := s.config.GetFeedSummaryCacheHours(); h > 0 {
			return h
		}
	}
	return defaultFeedSummaryCacheHours
}

func extractFeedHeadline(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj struct {
		Headline string `json:"headline"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return obj.Headline
}

func rawMessageOrNil(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

// GenerateFeedSummary 拉取关注公众号近 windowDays 天的文章（最多 30 篇），按缓存策略返回或生成结构化汇总。
// bypassCache=true 跳过缓存检查，强制重新调用 LLM；llm 为 nil 时若无缓存返回 ErrLLMNotConfigured。
func (s *Service) GenerateFeedSummary(ctx context.Context, windowDays int, llm *LLMClient, bypassCache bool) (*FeedSummary, error) {
	if windowDays <= 0 {
		windowDays = 7
	}
	if windowDays > 30 {
		windowDays = 30
	}

	cacheHours := s.feedSummaryCacheHours()
	since := time.Now().AddDate(0, 0, -windowDays)
	articles, err := s.store.GetWatchedArticlesSince(since, 30)
	if err != nil {
		return nil, err
	}
	if len(articles) == 0 {
		return nil, fmt.Errorf("%w: 最近 %d 天没有关注公众号的文章", ErrNoWatchedArticles, windowDays)
	}

	var latestArticleAt int64
	for _, a := range articles {
		if a.PublishedAt > latestArticleAt {
			latestArticleAt = a.PublishedAt
		}
	}

	now := time.Now().Unix()
	cachedHeadline, cachedStructured, computedAt, cacheExists, cacheErr := s.store.GetLatestFeedSummary(windowDays)
	if cacheErr != nil {
		log.Warn().Err(cacheErr).Int("windowDays", windowDays).Msg("bizhub: feed summary cache lookup failed")
	}

	cacheFresh := cacheExists &&
		(now-computedAt) < int64(cacheHours)*3600 &&
		computedAt >= latestArticleAt

	if cacheFresh && !bypassCache {
		log.Info().
			Int("windowDays", windowDays).
			Int64("computedAt", computedAt).
			Int64("latestArticleAt", latestArticleAt).
			Msg("bizhub: feed summary cache hit")
		return &FeedSummary{
			WindowDays: windowDays,
			Headline:   cachedHeadline,
			Structured: rawMessageOrNil(cachedStructured),
			ComputedAt: computedAt,
			FromCache:  true,
		}, nil
	}

	if llm == nil {
		if cacheExists {
			log.Info().
				Int("windowDays", windowDays).
				Int64("computedAt", computedAt).
				Bool("bypassCache", bypassCache).
				Msg("bizhub: feed summary cache returned (llm not configured)")
			return &FeedSummary{
				WindowDays: windowDays,
				Headline:   cachedHeadline,
				Structured: rawMessageOrNil(cachedStructured),
				ComputedAt: computedAt,
				FromCache:  true,
			}, nil
		}
		return nil, ErrLLMNotConfigured
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "以下是最近 %d 天我关注的微信公众号发布的 %d 篇文章（按发布时间倒序）。请基于标题、公众号名、摘要判断主题。\n\n", windowDays, len(articles))
	for i, a := range articles {
		fmt.Fprintf(&sb, "### [%d] %s · %s\n", i+1, a.GHName, a.Title)
		fmt.Fprintf(&sb, "- 发布：%s\n", time.Unix(a.PublishedAt, 0).Format("2006-01-02 15:04"))
		fmt.Fprintf(&sb, "- 摘要：%s\n", truncateRunes(a.Desc, 200))
		fmt.Fprintf(&sb, "- 链接：%s\n\n", a.URL)
	}
	sb.WriteString("请按 schema 输出严格 JSON。themes 的 count 字段填该主题下的相关文章数。")

	systemPrompt, err := LoadPrompt(feedSummarySystemPrompt)
	if err != nil {
		return nil, fmt.Errorf("bizhub: load feed summary prompt: %w", err)
	}
	// feed summary 严格要求 JSON —— 失败直接 ErrNotJSON，调用方据此给前端 502。
	text, _, _, err := llm.CompleteWithRetry(ctx, systemPrompt, sb.String())
	if err != nil {
		return nil, err
	}
	structured, ok := ParseStructured(text)
	if !ok {
		return nil, fmt.Errorf("%w: feed summary not parseable as json", ErrNotJSON)
	}

	headline := extractFeedHeadline(structured)

	if err := s.store.UpsertFeedSummary(windowDays, headline, string(structured)); err != nil {
		log.Warn().Err(err).Int("windowDays", windowDays).Msg("bizhub: upsert feed summary failed")
	}

	return &FeedSummary{
		WindowDays: windowDays,
		Headline:   headline,
		Structured: structured,
		ComputedAt: now,
		FromCache:  false,
	}, nil
}

// --- 每日精选 ---

const dailyDigestSystemPrompt = "daily_digest_system" // see prompts/daily_digest_system.md

// GenerateDailyDigest 生成今日精选。
// fresh=true 跳过缓存强制重新生成；否则如果今天已有精选且文章没大变，直接返回。
func (s *Service) GenerateDailyDigest(ctx context.Context, llm *LLMClient, fresh bool) (*DailyDigest, error) {
	if llm == nil {
		return nil, ErrLLMNotConfigured
	}

	today := time.Now().Format("2006-01-02")

	// 缓存：今天已有精选且不超过 4 小时 → 直接返回
	if !fresh {
		existing, err := s.store.GetDailyDigest(today)
		if err == nil && existing != nil && time.Now().Unix()-existing.ComputedAt < 4*3600 {
			return existing, nil
		}
	}

	// 取最近 24 小时的 watched 文章，最多 50 篇
	since := time.Now().Add(-24 * time.Hour)
	articles, err := s.store.GetWatchedArticlesSince(since, 50)
	if err != nil {
		return nil, err
	}
	if len(articles) == 0 {
		return nil, ErrNoWatchedArticles
	}

	// 构建 prompt
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("以下是我关注的公众号在最近 24 小时内发布的 %d 篇文章：\n\n", len(articles)))
	for i, a := range articles {
		desc := a.Desc
		if runes := []rune(desc); len(runes) > 150 {
			desc = string(runes[:150])
		}
		sb.WriteString(fmt.Sprintf("%d. [id=%d] 【%s】%s\n   发布: %s | 摘要: %s\n   链接: %s\n\n",
			i+1, a.ID, a.GHName, a.Title,
			time.Unix(a.PublishedAt, 0).Format("15:04"),
			desc, a.URL))
	}

	systemPrompt, err := LoadPrompt(dailyDigestSystemPrompt)
	if err != nil {
		return nil, fmt.Errorf("bizhub: load daily digest prompt: %w", err)
	}
	// daily digest 走 lenient：JSON 解析失败时 raw text 当 headline、picks 留空。
	// 原因：daily digest 是日常消费品，宁可看到 raw markdown 也不能 502 报错
	// 让用户没法用。和 feed summary 的「严格 JSON」是两种产品选择 —— digest
	// 重"先有内容"，feed summary 重"质量"。
	text, structured, _, _, err := llm.CompleteStructured(ctx, systemPrompt, sb.String())
	if err != nil {
		return nil, err
	}

	// 提取 headline 和 picks
	var result struct {
		Headline string `json:"headline"`
		Picks    []struct {
			ID     int64  `json:"id"`
			Title  string `json:"title"`
			Source string `json:"source"`
			Reason string `json:"reason"`
			Score  int    `json:"score"`
		} `json:"picks"`
	}
	switch {
	case len(structured) > 0:
		if err := json.Unmarshal(structured, &result); err != nil {
			log.Warn().Err(err).Msg("bizhub: daily digest structured parse failed, fallback to raw text headline")
			result.Headline = firstLine(text, 80)
		}
	default:
		// LLM 没返回 JSON —— raw text 当 headline，picks 空着。digest 至少能给用户
		// 一个能看的页面（headline + 文章列表）。
		log.Warn().Int("textLen", len(text)).Msg("bizhub: daily digest LLM output not JSON, fallback to raw text")
		result.Headline = firstLine(text, 80)
	}

	// 建立 article ID → article 的映射（用于补全 URL 等信息）
	articleMap := make(map[int64]Article)
	for _, a := range articles {
		articleMap[a.ID] = a
	}

	picks := make([]DigestPick, 0, len(result.Picks))
	articleIDs := make([]int64, 0, len(result.Picks))
	for _, p := range result.Picks {
		a := articleMap[p.ID]
		pick := DigestPick{
			ArticleID: p.ID,
			Title:     p.Title,
			GHName:    p.Source,
			Reason:    p.Reason,
			Score:     p.Score,
		}
		if a.URL != "" {
			pick.URL = a.URL
		}
		if pick.Title == "" {
			pick.Title = a.Title
		}
		if pick.GHName == "" {
			pick.GHName = a.GHName
		}
		picks = append(picks, pick)
		articleIDs = append(articleIDs, p.ID)
	}

	// 落库
	if err := s.store.UpsertDailyDigest(today, result.Headline, picks, articleIDs); err != nil {
		log.Warn().Err(err).Msg("bizhub: save daily digest failed")
	}

	digest, _ := s.store.GetDailyDigest(today)
	if digest == nil {
		digest = &DailyDigest{
			DigestDate: today,
			Headline:   result.Headline,
			Picks:      picks,
			ArticleIDs: articleIDs,
			ComputedAt: time.Now().Unix(),
		}
	}
	return digest, nil
}

// --- 板块看板 ---

// SectorSummary 板块聚合数据
type SectorSummary struct {
	TagID        int64     `json:"tagId"`
	TagName      string    `json:"tagName"`
	Color        string    `json:"color"`
	ArticleCount int       `json:"articleCount"`
	TopArticles  []Article `json:"topArticles"`
}

// GetSectorDashboard 按标签聚合 watched 账号的近期文章热度
func (s *Service) GetSectorDashboard(days int) ([]SectorSummary, error) {
	return s.store.GetSectorDashboard(days)
}

// --- 文章时间线 ---

// SearchTimeline 按关键词搜索 watched 账号文章，时间排序
func (s *Service) SearchTimeline(keyword string, days, limit int) ([]Article, int, error) {
	return s.store.SearchTimeline(keyword, days, limit)
}

// --- MD 导出相关 ---

// getExporter 懒初始化导出器（依赖 config）
func (s *Service) getExporter() (*mdexport.Exporter, error) {
	if s.exporter != nil {
		return s.exporter, nil
	}
	if s.config == nil {
		return nil, fmt.Errorf("mdexport: config not set")
	}
	script := s.config.GetMDExportScript()
	dir := s.config.GetMDExportDir()
	if script == "" || dir == "" {
		return nil, fmt.Errorf("mdexport: md_export_script and md_export_dir must be configured")
	}
	exp, err := mdexport.New(mdexport.Config{
		Script:    script,
		OutputDir: dir,
	})
	if err != nil {
		return nil, err
	}
	s.exporter = exp
	return exp, nil
}

// ExportArticleResult 单篇导出结果
type ExportArticleResult struct {
	ArticleID int64  `json:"articleID"`
	Title     string `json:"title"`
	Account   string `json:"account"`
	MDPath    string `json:"mdPath"`
	Status    string `json:"status"`
	Duration  string `json:"duration"`
}

// ExportArticle 导出单篇文章为 MD
func (s *Service) ExportArticle(ctx context.Context, articleID int64) (*ExportArticleResult, error) {
	article, err := s.store.GetArticle(articleID)
	if err != nil {
		return nil, err
	}
	if article == nil {
		return nil, fmt.Errorf("article not found: %d", articleID)
	}

	exp, err := s.getExporter()
	if err != nil {
		return nil, err
	}

	result, err := exp.Export(ctx, article.URL)
	if err != nil {
		// 记录失败（含分类，供下次批量归档判断该不该重试）
		kind := mdexport.KindOf(err)
		logExportFailure(articleID, "", err)
		_ = s.store.UpsertExportRecord(articleID, article.URL, "", "", ExportStatusFailed, string(kind), err.Error())
		return nil, err
	}

	// 记录成功
	if err := s.store.UpsertExportRecord(articleID, article.URL, result.MDPath, "", ExportStatusExported, "", ""); err != nil {
		log.Warn().Err(err).Int64("articleID", articleID).Msg("bizhub: upsert export record failed")
	}

	return &ExportArticleResult{
		ArticleID: articleID,
		Title:     article.Title,
		Account:   result.Account,
		MDPath:    result.MDPath,
		Status:    ExportStatusExported,
		Duration:  result.Duration.String(),
	}, nil
}

// ExportBatchRequest 批量归档请求
type ExportBatchRequest struct {
	Days  int      `json:"days"`
	Limit int      `json:"limit"`
	GHIDs []string `json:"ghIDs,omitempty"` // 为空则处理所有可见账号
}

// 时间窗上限。批量归档会把每篇文章交给抓取器跑一遍真实网络请求，
// 窗口开得过大等于一次性对微信发起上千次抓取，是触发风控最直接的方式。
// 超过上限直接报错而不是静默截断 —— 静默截断会让用户以为「近 3 年都归档好了」。
const maxExportWindowDays = 365

// 归档并发与熔断参数
const (
	defaultExportConcurrency = 3
	maxExportConcurrency     = 8

	// exportItemTimeout 单篇归档超时。抓取器要下载正文 + 图片，正常 10–40 秒；
	// 3 分钟足够区分「慢」和「卡死」。
	exportItemTimeout = 3 * time.Minute

	// 失败率降级：至少处理过这么多篇之后才开始判断，避免「跑 2 篇失败 1 篇」
	// 就把并发降到 1，那样小批量任务永远跑不起来。
	exportDegradeMinSamples = 6
	// 失败率达到该比例即降级为串行
	exportDegradeFailRate = 0.5
)

// ErrExportNotConfigured 归档功能未配置（缺 md_export_script / md_export_dir）
var ErrExportNotConfigured = errors.New("MD 归档未配置：请先设置 md_export_script 与 md_export_dir")

// ErrExportJobRunning 已有归档任务在跑
var ErrExportJobRunning = errors.New("已有归档任务正在运行，请等它结束或先取消")

// ErrInvalidExportWindow 时间范围不合法（超出上限或为负数）
var ErrInvalidExportWindow = errors.New("时间范围不合法")

// ErrExportJobNotFound 归档任务不存在
var ErrExportJobNotFound = errors.New("归档任务不存在")

// 归档任务配置：把并发与时间窗的口径集中在这里，避免 handler 和 service
// 各自校验一遍再慢慢漂移。
func (s *Service) exportConcurrency() int {
	n := 0
	if s.config != nil {
		n = s.config.GetMDExportConcurrency()
	}
	if n <= 0 {
		return defaultExportConcurrency
	}
	if n > maxExportConcurrency {
		return maxExportConcurrency
	}
	return n
}

// ValidateExportWindow 校验时间窗，返回规范化后的值。
//
// 这里显式报错而不是截断，是这次迭代修掉的一个真实缺陷：旧实现里
// days > 30 会被静默改成 30，用户选「近 90 天」得到的结果和「近 30 天」一模一样，
// 数字看着正常，实际漏了两个月 —— 这种 bug 没有任何线索能被用户发现。
func ValidateExportWindow(days int) (int, error) {
	if days <= 0 {
		return 30, nil
	}
	if days > maxExportWindowDays {
		return 0, fmt.Errorf("%w：最多 %d 天，收到 %d 天", ErrInvalidExportWindow, maxExportWindowDays, days)
	}
	return days, nil
}

// StartExportJob 创建并启动批量归档任务，立即返回任务快照。
//
// 与旧 ExportBatch 的关键差别：旧接口是一次同步 HTTP 请求，整批跑完才返回，
// 中途关页面 / 服务重启进度全丢，前端只能用假进度条。现在返回的是任务 ID，
// 逐篇结果落库，前端轮询、服务重启后可续跑。
func (s *Service) StartExportJob(req ExportBatchRequest) (*ExportJob, error) {
	if !s.IsExportConfigured() {
		return nil, ErrExportNotConfigured
	}
	// 先占位再建任务。
	//
	// 这个检查必须放在建库之前：否则会建出一个永远不会被执行的僵尸任务 ——
	// launchExportJob 因为名额被占而拒绝启动，但任务行已经落库、状态写着 running，
	// 前端会一直轮询一个不动的进度条。之前只在 launch 里拦，就是这个问题。
	if s.ActiveExportJob() != "" {
		return nil, ErrExportJobRunning
	}

	days, err := ValidateExportWindow(req.Days)
	if err != nil {
		return nil, err
	}
	req.Days = days
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.Limit > 1000 {
		req.Limit = 1000
	}

	items, err := s.buildExportPlan(req)
	if err != nil {
		return nil, err
	}

	job := &ExportJob{
		ID:          newExportJobID(),
		Status:      ExportJobRunning,
		Days:        req.Days,
		MaxItems:    req.Limit,
		Concurrency: s.exportConcurrency(),
		Total:       len(items),
	}
	if len(items) == 0 {
		job.Status = ExportJobDone
		job.FinishedAt = time.Now().Unix()
	}

	if err := s.store.CreateExportJob(job, items); err != nil {
		return nil, err
	}
	if len(items) > 0 {
		s.launchExportJob(job.ID)
	}
	return s.store.GetExportJob(job.ID)
}

// buildExportPlan 把候选文章转成任务条目，并在这里做「该不该跑」的判断。
//
// 判断放在 Go 层而不是 SQL 里，因为它依赖 mdexport 的失败分类与重试计数；
// 塞进 SQL 等于把「哪些失败算永久失败」的逻辑复制第二遍，两份迟早会不一致。
//
// 被判定为不必再跑的条目会以 skipped 状态建出来而不是直接丢弃 ——
// 用户需要看见「这 3 篇卡在验证码上等你去处理」，而不是让它们从数字里消失。
func (s *Service) buildExportPlan(req ExportBatchRequest) ([]ExportJobItem, error) {
	articles, err := s.store.GetUnexportedArticles(req.Days, req.Limit, req.GHIDs...)
	if err != nil {
		return nil, err
	}
	if len(articles) == 0 {
		return nil, nil
	}

	ids := make([]int64, len(articles))
	for i, a := range articles {
		ids[i] = a.ID
	}
	records, err := s.store.GetExportRecordsByArticleIDs(ids)
	if err != nil {
		return nil, err
	}

	items := make([]ExportJobItem, 0, len(articles))
	for _, a := range articles {
		it := ExportJobItem{
			ArticleID: a.ID,
			Title:     a.Title,
			Account:   a.GHName,
			Status:    ExportItemPending,
		}
		rec := records[a.ID]
		if rec != nil && rec.Status == ExportStatusFailed {
			kind := mdexport.ParseKind(rec.Kind)
			switch {
			case kind.NeedsHuman():
				it.Status = ExportItemSkipped
				it.Kind = string(kind)
				it.Error = "需人工处理：" + kind.Label()
			case rec.Attempts >= maxExportAttempts:
				it.Status = ExportItemSkipped
				it.Kind = string(kind)
				it.Error = fmt.Sprintf("已自动重试 %d 次仍失败，已停止自动重试", rec.Attempts)
			}
		}
		items = append(items, it)
	}
	return items, nil
}

// GetExportJob 读任务详情。items=true 时附带逐篇条目（管理页展开失败清单用）。
func (s *Service) GetExportJob(id string, withItems bool) (*ExportJob, []ExportJobItem, error) {
	job, err := s.store.GetExportJob(id)
	if err != nil || job == nil {
		return job, nil, err
	}
	if !withItems {
		return job, nil, nil
	}
	items, err := s.store.ListExportJobItems(id)
	if err != nil {
		return job, nil, err
	}
	return job, items, nil
}

// ListExportJobs 读最近任务列表（前端进页面时用它恢复进度显示）
func (s *Service) ListExportJobs(limit int) ([]*ExportJob, error) {
	return s.store.ListExportJobs(limit)
}

// ActiveExportJob 返回正在运行的任务 ID（空串表示空闲）
func (s *Service) ActiveExportJob() string {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	return s.activeJob
}

// CancelExportJob 取消正在运行的任务，返回被取消的任务 ID。
//
// 已经跑完的条目保持原状（不回退，文件已经落盘了），还在 pending 的条目
// 留在 pending —— 用户过一会点「重试」就能接着跑。
//
// 已经请求过取消的任务再点会返回 false：取消是幂等的，重复返回 true
// 会让前端误以为「又取消了一个任务」。
func (s *Service) CancelExportJob() (string, bool) {
	s.jobMu.Lock()
	id := s.activeJob
	cancel := s.jobCancel
	ctx := s.jobCtx
	s.jobMu.Unlock()

	if id == "" || cancel == nil {
		return "", false
	}
	if ctx != nil && ctx.Err() != nil {
		return "", false
	}
	cancel()
	return id, true
}

// RetryExportJob 把任务里失败 / 中断的条目退回 pending 并重新启动。
//
// 「重试」是整任务级别的：一次运行里失败的原因往往相同（限流、验证码），
// 让人一篇篇点不现实；想看单篇状态就在管理页的失败清单里看。
func (s *Service) RetryExportJob(id string) (*ExportJob, error) {
	if !s.IsExportConfigured() {
		return nil, ErrExportNotConfigured
	}
	job, err := s.store.GetExportJob(id)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf("%w: %s", ErrExportJobNotFound, id)
	}
	if s.ActiveExportJob() != "" {
		return nil, ErrExportJobRunning
	}

	// 「重试」= 把这一任务里没成功过的全部重新入队。手动重试的前提是用户
	// 已经处理了失败原因，所以之前被判「需人工处理」而跳过的条目也一起重来。
	if _, err := s.store.RequeueExportJobUnfinished(id); err != nil {
		return nil, err
	}
	// 状态由 launchExportJob 统一置为 running，这里不重复写 ——
	// 两个地方都能改状态时，早晚会有一个忘了改。
	s.launchExportJob(id)
	return s.store.GetExportJob(id)
}

// GetExportStatus 归档统计（按时间窗）
func (s *Service) GetExportStatus(days int) (*ExportStats, error) {
	return s.store.GetExportStats(days)
}

// resumeInterruptedExportJobs 启动时修复并续跑上次残留的归档任务。
func (s *Service) resumeInterruptedExportJobs() {
	ids, err := s.store.MarkInterruptedExportJobs()
	if err != nil {
		log.Warn().Err(err).Msg("bizhub: 归档任务状态修复失败")
		return
	}
	if len(ids) == 0 {
		return
	}
	if !s.IsExportConfigured() {
		log.Warn().Int("jobs", len(ids)).
			Msg("bizhub: 有归档任务因重启中断，但 MD 归档未配置，暂不续跑")
		return
	}
	// 只续跑最近一个：更早的任务是历史遗留的，把它一起拉起来只会同时抢抓取额度。
	id := ids[0]
	log.Info().Str("jobID", id).Int("interrupted", len(ids)).
		Msg("bizhub: 检测到中断的归档任务，正在续跑")
	s.launchExportJob(id)
}

// launchExportJob 在后台跑任务。同一时刻只允许一个任务在跑。
func (s *Service) launchExportJob(jobID string) {
	s.jobMu.Lock()
	if s.activeJob != "" {
		s.jobMu.Unlock()
		log.Warn().Str("jobID", jobID).Str("active", s.activeJob).
			Msg("bizhub: 已有归档任务在运行，跳过本次启动")
		return
	}
	ctx, cancel := context.WithCancel(s.bgCtx)
	s.activeJob = jobID
	s.jobCancel = cancel
	s.jobCtx = ctx
	s.jobMu.Unlock()

	// 拿到名额后立刻把任务标回 running。
	//
	// 续跑路径必须这一步：任务是从 interrupted 状态被重新拉起来的，
	// 如果状态留在 interrupted，ExportJob.Done() 就一直是 true ——
	// 前端不会再轮询，界面上会永远显示「被中断」，而后台其实正在跑。
	if err := s.store.SetExportJobStatus(jobID, ExportJobRunning, "", false); err != nil {
		log.Warn().Err(err).Str("jobID", jobID).Msg("bizhub: 重置归档任务状态失败")
	}

	go func() {
		defer func() {
			s.releaseExportJob(jobID)
			cancel()
		}()
		s.runExportJob(ctx, jobID)
	}()
}

// runExportJob 任务主循环：分批并发执行，按失败率降级，必要时熔断。
//
// 用「分块等待」而不是常驻 worker pool：并发数需要在运行中调小，
// 固定大小的 worker pool 只能一开始就把并发定死。分块会牺牲一点流水线效率
// （一块里最慢的那篇会拖住下一块），换来的是并发可动态收窄 —— 面对风控时
// 这个能力比吞吐重要。
func (s *Service) runExportJob(ctx context.Context, jobID string) {
	exp, err := s.getExporter()
	if err != nil {
		_ = s.store.SetExportJobStatus(jobID, ExportJobFailed, err.Error(), true)
		return
	}

	job, err := s.store.GetExportJob(jobID)
	if err != nil || job == nil {
		log.Error().Err(err).Str("jobID", jobID).Msg("bizhub: 加载归档任务失败")
		return
	}

	all, err := s.store.ListExportJobItems(jobID)
	if err != nil {
		_ = s.store.SetExportJobStatus(jobID, ExportJobFailed, err.Error(), true)
		return
	}
	var work []ExportJobItem
	for _, it := range all {
		if it.Status == ExportItemPending {
			work = append(work, it)
		}
	}

	concurrency := job.Concurrency
	if concurrency <= 0 {
		concurrency = defaultExportConcurrency
	}
	degraded := job.Degraded

	succeeded, failed := 0, 0
	aborted := false
	var failedKinds []mdexport.FailureKind

	for i := 0; i < len(work); {
		if ctx.Err() != nil {
			break
		}
		end := i + concurrency
		if end > len(work) {
			end = len(work)
		}
		chunk := work[i:end]
		i = end

		var wg sync.WaitGroup
		var mu sync.Mutex
		var chunkKinds []mdexport.FailureKind

		for _, it := range chunk {
			wg.Add(1)
			go func(it ExportJobItem) {
				defer wg.Done()
				res := s.runExportItem(ctx, exp, it)
				mu.Lock()
				switch res.Status {
				case ExportItemDone:
					succeeded++
				case ExportItemFailed:
					failed++
					chunkKinds = append(chunkKinds, mdexport.ParseKind(res.Kind))
				}
				mu.Unlock()
			}(it)
		}
		wg.Wait()

		failedKinds = append(failedKinds, chunkKinds...)

		processed := succeeded + failed
		if !degraded && processed >= exportDegradeMinSamples &&
			float64(failed)/float64(processed) >= exportDegradeFailRate {
			concurrency = 1
			degraded = true
			_ = s.store.SetExportJobConcurrency(jobID, concurrency, true)
			log.Warn().Str("jobID", jobID).
				Int("processed", processed).Int("failed", failed).
				Msg("bizhub: 归档失败率偏高，并发已降级为 1")
		}

		// 熔断：整块全失败且都是「需人工处理」的失败（典型是验证码）。
		// 继续跑下去只会把账号彻底打进风控名单，剩下的留给人处理。
		if shouldAbortChunk(chunkKinds) {
			aborted = true
			remaining := work[i:]
			for _, it := range remaining {
				it.Status = ExportItemSkipped
				it.Kind = string(mdexport.KindCaptcha)
				it.Error = "连续触发验证码，本轮已暂停；处理后点「重试」继续"
				_ = s.store.UpdateExportJobItem(it)
			}
			log.Error().Str("jobID", jobID).Int("skipped", len(remaining)).
				Msg("bizhub: 归档触发熔断，剩余条目已暂停")
			break
		}
	}

	// 汇总整个任务所有条目的真实最终状态，避免因局部统计误判全局
	allFinished, err := s.store.ListExportJobItems(jobID)
	totalDone, totalFailed := 0, 0
	if err == nil {
		for _, item := range allFinished {
			if item.Status == ExportItemDone {
				totalDone++
			} else if item.Status == ExportItemFailed {
				totalFailed++
			}
		}
	} else {
		totalDone, totalFailed = succeeded, failed
	}

	status, msg := ExportJobDone, ""
	switch {
	case ctx.Err() != nil:
		status, msg = ExportJobCanceled, "任务已被取消"
	case aborted:
		status = ExportJobFailed
		msg = "连续触发验证码，本轮已暂停；处理后点「重试」继续"
	case totalFailed > 0 && totalDone == 0:
		status = ExportJobFailed
		// 全部失败且原因是同一类时把分类说出来。
		// 「全部条目归档失败」等于没说 —— 用户需要知道的是去装抓取器、还是去过验证码。
		if label := dominantKindLabel(failedKinds); label != "" {
			msg = "全部条目归档失败：" + label + "，处理后点「重试」继续"
		} else {
			msg = "全部条目归档失败"
		}
	case totalFailed > 0 && totalDone > 0:
		status = ExportJobDone
		msg = fmt.Sprintf("部分条目归档完成（成功 %d 篇，失败 %d 篇）", totalDone, totalFailed)
	case totalDone > 0:
		status = ExportJobDone
		msg = fmt.Sprintf("归档完成（成功 %d 篇）", totalDone)
	}

	// 先释放并发名额，再写终态。
	//
	// 顺序反过来会有个很难查的窗口：任务行已经是 done/failed，但 activeJob 还没清，
	// 用户这时点「重试」会被 ErrExportJobRunning 挡回去，提示「已有任务在运行」——
	// 而界面上明明显示任务已经结束了。保持「任务行是终态 ⟹ 名额已释放」这条不变量，
	// 就不会出现这种自相矛盾的状态组合。
	s.releaseExportJob(jobID)

	if err := s.store.SetExportJobStatus(jobID, status, msg, true); err != nil {
		log.Warn().Err(err).Str("jobID", jobID).Msg("bizhub: 更新归档任务状态失败")
	}
	log.Info().Str("jobID", jobID).Str("status", status).
		Int("succeeded", totalDone).Int("failed", totalFailed).Msg("bizhub: 归档任务结束")
}

// exportLogStderrLines 单条归档失败日志里保留的 stderr 行数。
//
// 抓取器报错时最后几行才是根因（Python traceback 的异常行在最末），
// 但全量 stderr 可能有几百行浏览器日志，所以只取尾部。
const exportLogStderrLines = 15

// logExportFailure 把归档失败「可定位」的那一半信息写进服务日志。
//
// 界面和接口上只留一行分类（「脚本执行失败」），这是对的 —— 用户不需要看 traceback。
// 但脚本 stderr 里的原文（抓取器为什么退出、浏览器为什么起不来）原本被直接丢掉，
// 于是排查只剩「换个环境再试一次」这种办法，代价极高且不一定能复现。
//
// 只写日志、不进库：stderr 可能很长且含本机绝对路径，塞进任务行既撑爆字段
// 又把路径带给了前端。jobID 为空表示单篇导出（非任务内）。
func logExportFailure(articleID int64, jobID string, err error) {
	ev := log.Warn().Int64("articleID", articleID).
		Str("kind", string(mdexport.KindOf(err)))
	if jobID != "" {
		ev = ev.Str("jobID", jobID)
	}
	var ee *mdexport.ExportError
	if errors.As(err, &ee) {
		ev = ev.Int("exitCode", ee.ExitCode).
			Str("stderr", tailLines(ee.Stderr, exportLogStderrLines))
	}
	ev.Msg("bizhub: 归档失败详情")
}

// tailLines 取最后 n 行；空输入返回空串。
func tailLines(s string, n int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// releaseExportJob 释放并发名额（幂等）。实际释放由 launchExportJob 的 defer 兜底。
func (s *Service) releaseExportJob(jobID string) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.activeJob == jobID {
		s.activeJob = ""
		s.jobCancel = nil
		s.jobCtx = nil
	}
}

// findExistingMD 在输出目录检查指定账号和标题的文章是否已存在 Markdown 文件。
// 微信抓取脚本常将特殊字符（|、:、/、* 等）替换为下划线，此处做归一化容错匹配。
func findExistingMD(baseDir, account, title string) string {
	if baseDir == "" || title == "" {
		return ""
	}
	cleanAccount := strings.TrimSpace(account)
	if cleanAccount == "" {
		cleanAccount = "default"
	}
	accountDir := filepath.Join(baseDir, cleanAccount)
	if fi, err := os.Stat(accountDir); err != nil || !fi.IsDir() {
		return ""
	}

	cleanTitle := strings.TrimSpace(title)
	directMD := filepath.Join(accountDir, cleanTitle, cleanTitle+".md")
	if fi, err := os.Stat(directMD); err == nil && !fi.IsDir() {
		return directMD
	}

	entries, err := os.ReadDir(accountDir)
	if err != nil {
		return ""
	}

	norm := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if r == '|' || r == ':' || r == '：' || r == '%' || r == '/' || r == '\\' ||
				r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '_' ||
				r == ' ' || r == '\u00a0' {
				continue
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	normTarget := norm(cleanTitle)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == cleanTitle || (normTarget != "" && norm(e.Name()) == normTarget) {
			subDir := filepath.Join(accountDir, e.Name())
			subEntries, _ := os.ReadDir(subDir)
			for _, se := range subEntries {
				if !se.IsDir() && strings.HasSuffix(se.Name(), ".md") {
					return filepath.Join(subDir, se.Name())
				}
			}
		}
	}
	return ""
}

// runExportItem 归档单篇，并把结果写回条目与归档记录。
func (s *Service) runExportItem(ctx context.Context, exp *mdexport.Exporter, it ExportJobItem) ExportJobItem {
	it.Status = ExportItemRunning
	it.Kind = ""
	it.Error = ""
	_ = s.store.UpdateExportJobItem(it)

	article, err := s.store.GetArticle(it.ArticleID)
	if err != nil || article == nil {
		it.Status = ExportItemSkipped
		it.Error = "文章已不存在（可能已被重新同步移除）"
		_ = s.store.UpdateExportJobItem(it)
		return it
	}

	// 1. 检查数据库是否已标记导出（支持快速续传/幂等保护）
	if isExp, _ := s.store.IsArticleExported(article.ID); isExp {
		rec, _ := s.store.GetExportRecord(article.ID)
		mdPath := ""
		if rec != nil {
			mdPath = rec.MDPath
		}
		it.Status = ExportItemDone
		it.MDPath = mdPath
		_ = s.store.UpdateExportJobItem(it)
		return it
	}

	// 2. 检查输出目录下是否已有 Markdown 产出（避免重复调用外部脚本重新抓取）
	if exp != nil && exp.OutputDir() != "" {
		if existingMD := findExistingMD(exp.OutputDir(), article.GHName, article.Title); existingMD != "" {
			it.Status = ExportItemDone
			it.MDPath = existingMD
			_ = s.store.UpdateExportJobItem(it)
			_ = s.store.UpsertExportRecord(article.ID, article.URL, existingMD, "",
				ExportStatusExported, "", "")
			return it
		}
	}

	itemCtx, cancel := context.WithTimeout(ctx, exportItemTimeout)
	defer cancel()

	res, err := exp.Export(itemCtx, article.URL)
	if err != nil {
		kind := mdexport.KindOf(err)
		it.Status = ExportItemFailed
		it.Kind = string(kind)
		it.Error = err.Error()
		logExportFailure(it.ArticleID, it.JobID, err)
		_ = s.store.UpdateExportJobItem(it)
		_ = s.store.UpsertExportRecord(article.ID, article.URL, "", "",
			ExportStatusFailed, string(kind), err.Error())
		return it
	}

	it.Status = ExportItemDone
	it.MDPath = res.MDPath
	_ = s.store.UpdateExportJobItem(it)
	_ = s.store.UpsertExportRecord(article.ID, article.URL, res.MDPath, "",
		ExportStatusExported, "", "")
	return it
}

// shouldAbortChunk 判断这一块结果是否触发熔断。
//
// 条件：至少 2 篇、全部失败、且失败分类全部属于「需人工处理」。
// 用「全部」而不是「过半」是因为要避免误熔断：一块里混着超时和验证码时，
// 降并发就够了，没必要停手。
func shouldAbortChunk(kinds []mdexport.FailureKind) bool {
	if len(kinds) < 2 {
		return false
	}
	for _, k := range kinds {
		if !k.NeedsHuman() {
			return false
		}
	}
	return true
}

// dominantKindLabel 所有失败都是同一类时给出该类的中文标签，否则空串。
func dominantKindLabel(kinds []mdexport.FailureKind) string {
	if len(kinds) == 0 {
		return ""
	}
	first := kinds[0]
	for _, k := range kinds {
		if k != first {
			return ""
		}
	}
	return first.Label()
}

// newExportJobID 生成任务 ID：时间戳（可读）+ 随机后缀（同秒创建也不冲突）
func newExportJobID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "job_" + time.Now().Format("20060102T150405") + "_" + hex.EncodeToString(b[:])
}

// --- IMA 推送 任务化 ---

// PushBatchRequest 建任务请求（与 ExportBatchRequest 镜像）
type PushBatchRequest struct {
	Days  int      `json:"days"`
	Limit int      `json:"limit"`
	GHIDs []string `json:"ghIDs"`
}

// pushItemTimeout 单批推送超时。import_urls 单批 1-10 URL，IMA 端处理
// 通常 5-15 秒；超时设 60 秒与 imapush.Exporter 默认 Timeout 一致。
const pushItemTimeout = 60 * time.Second

// defaultPushConcurrency 默认并发数（与 export 同 3）
const defaultPushConcurrency = 3

// ErrPushNotConfigured 推送功能未配置
var ErrPushNotConfigured = errors.New("IMA 推送未配置：请先设置 ima_push_skill_dir 与 ima_push_kb_id")

// ErrPushJobRunning 已有推送任务在跑
var ErrPushJobRunning = errors.New("已有推送任务正在运行，请等它结束或先取消")

// ErrPushJobNotFound 推送任务不存在
var ErrPushJobNotFound = errors.New("推送任务不存在")

// pushConcurrency 取配置的推送并发数；<=0 时回退默认 3，上限 8。
func (s *Service) pushConcurrency() int {
	if s.config == nil {
		return defaultPushConcurrency
	}
	c := s.config.GetIMAPushConcurrency()
	if c <= 0 {
		c = defaultPushConcurrency
	}
	if c > 8 {
		c = 8
	}
	return c
}

// ActivePushJob 当前在跑的推送任务 ID（无任务时返回空字符串）
func (s *Service) ActivePushJob() string {
	s.pushJobMu.Lock()
	defer s.pushJobMu.Unlock()
	return s.activePushJob
}

// ValidatePushWindow 校验推送窗口天数，与 ValidateExportWindow 同款（共用上限 365 天）。
func ValidatePushWindow(days int) (int, error) {
	if days <= 0 {
		return 30, nil
	}
	if days > maxExportWindowDays {
		return 0, fmt.Errorf("%w：最多 %d 天，收到 %d 天", ErrInvalidExportWindow, maxExportWindowDays, days)
	}
	return days, nil
}

// StartPushJob 创建并启动批量推送任务，立即返回任务快照。
func (s *Service) StartPushJob(req PushBatchRequest) (*PushJob, error) {
	if !s.IsPushConfigured() {
		return nil, ErrPushNotConfigured
	}
	// 先占位再建任务（与 StartExportJob 同款逻辑）
	if s.ActivePushJob() != "" {
		return nil, ErrPushJobRunning
	}

	days, err := ValidatePushWindow(req.Days)
	if err != nil {
		return nil, err
	}
	req.Days = days
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.Limit > 1000 {
		req.Limit = 1000
	}

	items, err := s.buildPushPlan(req)
	if err != nil {
		return nil, err
	}

	job := &PushJob{
		ID:          newExportJobID(), // 复用同一 ID 格式：前缀 job_ 即表明是哪类任务
		Status:      ExportJobRunning,
		Days:        req.Days,
		MaxItems:    req.Limit,
		Concurrency: s.pushConcurrency(),
		Total:       len(items),
	}
	if len(items) == 0 {
		job.Status = ExportJobDone
		job.FinishedAt = time.Now().Unix()
	}

	if err := s.store.CreatePushJob(job, items); err != nil {
		return nil, err
	}
	if len(items) > 0 {
		s.launchPushJob(job.ID)
	}
	return s.store.GetPushJob(job.ID)
}

// buildPushPlan 把候选文章转成推送任务条目。
//
// 链式约束在 PushCandidateWhere 已表达：未 export 成功的不会出现在 articles 里。
// 这里只决定「该不该跳」（永久失败 / 重试用尽）。
func (s *Service) buildPushPlan(req PushBatchRequest) ([]PushJobItem, error) {
	articles, err := s.store.GetPushableArticles(req.Days, req.Limit)
	if err != nil {
		return nil, err
	}

	if len(req.GHIDs) > 0 {
		ghidSet := make(map[string]struct{}, len(req.GHIDs))
		for _, ghid := range req.GHIDs {
			ghidSet[ghid] = struct{}{}
		}
		filtered := articles[:0]
		for _, a := range articles {
			if _, ok := ghidSet[a.GHID]; ok {
				filtered = append(filtered, a)
			}
		}
		articles = filtered
	}
	if len(articles) == 0 {
		return nil, nil
	}

	ids := make([]int64, len(articles))
	for i, a := range articles {
		ids[i] = a.ID
	}
	records, err := s.store.GetExportRecordsByArticleIDs(ids)
	if err != nil {
		return nil, err
	}

	items := make([]PushJobItem, 0, len(articles))
	for _, a := range articles {
		it := PushJobItem{
			JobID:     "", // 会在 CreatePushJob 阶段填上
			ArticleID: a.ID,
			Title:     a.Title,
			Account:   a.GHName,
			Status:    ExportItemPending,
		}
		rec := records[a.ID]
		if rec != nil && rec.PushedStatus == PushStatusFailed {
			kind := imapush.ParseKind(rec.PushKind)
			switch {
			case kind.NeedsHuman():
				it.Status = ExportItemSkipped
				it.Kind = string(kind)
				it.Error = "需人工处理：" + kind.Label()
			case rec.PushAttempts >= maxPushAttempts:
				it.Status = ExportItemSkipped
				it.Kind = string(kind)
				it.Error = fmt.Sprintf("已自动重试 %d 次仍失败，已停止自动重试", rec.PushAttempts)
			}
		}
		items = append(items, it)
	}
	return items, nil
}

// GetPushJob 读任务详情
func (s *Service) GetPushJob(id string, withItems bool) (*PushJob, []PushJobItem, error) {
	job, err := s.store.GetPushJob(id)
	if err != nil {
		return nil, nil, err
	}
	if job == nil {
		return nil, nil, ErrPushJobNotFound
	}
	if !withItems {
		return job, nil, nil
	}
	items, err := s.store.ListPushJobItems(id)
	if err != nil {
		return nil, nil, err
	}
	if items == nil {
		items = []PushJobItem{}
	}
	return job, items, nil
}

// ListPushJobs 读最近的任务列表
func (s *Service) ListPushJobs(limit int) ([]*PushJob, error) {
	jobs, err := s.store.ListPushJobs(limit)
	if err != nil {
		return nil, err
	}
	if jobs == nil {
		jobs = []*PushJob{}
	}
	return jobs, nil
}

// RetryPushJob 重试任务里未成功的条目
func (s *Service) RetryPushJob(id string) (*PushJob, error) {
	job, err := s.store.GetPushJob(id)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrPushJobNotFound
	}
	if job.Status == ExportJobRunning {
		return nil, ErrExportJobRunning
	}

	if _, err := s.store.RequeuePushJobUnfinished(id); err != nil {
		return nil, err
	}
	s.launchPushJob(id)
	return s.store.GetPushJob(id)
}

// CancelPushJob 取消任务
func (s *Service) CancelPushJob(id string) error {
	s.pushJobMu.Lock()
	if s.activePushJob == id {
		if s.pushJobCancel != nil {
			s.pushJobCancel()
		}
		s.pushJobMu.Unlock()
		return nil
	}
	s.pushJobMu.Unlock()

	// 不在跑：直接标 canceled
	return s.store.SetPushJobStatus(id, ExportJobCanceled, "用户取消", true)
}

// GetPushStatus 推送统计
func (s *Service) GetPushStatus(days int) (*PushStats, error) {
	return s.store.GetPushStats(days)
}

// resumeInterruptedPushJobs 启动时修复并续跑上次残留的推送任务
func (s *Service) resumeInterruptedPushJobs() {
	ids, err := s.store.MarkInterruptedPushJobs()
	if err != nil {
		log.Warn().Err(err).Msg("bizhub: 推送任务状态修复失败")
		return
	}
	if len(ids) == 0 {
		return
	}
	if !s.IsPushConfigured() {
		log.Warn().Int("jobs", len(ids)).
			Msg("bizhub: 有推送任务因重启中断，但 IMA 推送未配置，暂不续跑")
		return
	}
	id := ids[0]
	log.Info().Str("jobID", id).Int("interrupted", len(ids)).
		Msg("bizhub: 检测到中断的推送任务，正在续跑")
	s.launchPushJob(id)
}

// launchPushJob 在后台跑任务。push 与 export 是独立的并发槽位 —— 用户可以
// 同时跑一个 export + 一个 push（push 候选是"已 export"，不会跟 export 撞资源）。
func (s *Service) launchPushJob(jobID string) {
	s.pushJobMu.Lock()
	if s.activePushJob != "" {
		s.pushJobMu.Unlock()
		log.Warn().Str("jobID", jobID).Str("active", s.activePushJob).
			Msg("bizhub: 已有推送任务在运行，跳过本次启动")
		return
	}
	ctx, cancel := context.WithCancel(s.bgCtx)
	s.activePushJob = jobID
	s.pushJobCancel = cancel
	s.pushJobCtx = ctx
	s.pushJobMu.Unlock()

	if err := s.store.SetPushJobStatus(jobID, ExportJobRunning, "", false); err != nil {
		log.Warn().Err(err).Str("jobID", jobID).Msg("bizhub: 重置推送任务状态失败")
	}

	go func() {
		defer func() {
			s.releasePushJob(jobID)
			cancel()
		}()
		s.runPushJob(ctx, jobID)
	}()
}

// runPushJob 推送任务主循环。
//
// 与 runExportJob 形状一致：分块并发执行，按失败率降级，按"全是永久失败"熔断。
// 唯一差异是失败分类用 imapush.FailureKind 而不是 mdexport.FailureKind。
func (s *Service) runPushJob(ctx context.Context, jobID string) {
	exp, err := s.getPushExporter()
	if err != nil {
		_ = s.store.SetPushJobStatus(jobID, ExportJobFailed, err.Error(), true)
		return
	}

	job, err := s.store.GetPushJob(jobID)
	if err != nil || job == nil {
		log.Error().Err(err).Str("jobID", jobID).Msg("bizhub: 加载推送任务失败")
		return
	}

	all, err := s.store.ListPushJobItems(jobID)
	if err != nil {
		_ = s.store.SetPushJobStatus(jobID, ExportJobFailed, err.Error(), true)
		return
	}
	var work []PushJobItem
	for _, it := range all {
		if it.Status == ExportItemPending {
			it.JobID = jobID // 注入任务 ID，runPushItem 的 logPushFailure 会用
			work = append(work, it)
		}
	}

	concurrency := job.Concurrency
	if concurrency <= 0 {
		concurrency = defaultPushConcurrency
	}
	degraded := job.Degraded

	succeeded, failed := 0, 0
	aborted := false
	var failedKinds []imapush.FailureKind

	for i := 0; i < len(work); {
		if ctx.Err() != nil {
			break
		}
		end := i + concurrency
		if end > len(work) {
			end = len(work)
		}
		chunk := work[i:end]
		i = end

		var wg sync.WaitGroup
		var mu sync.Mutex
		var chunkKinds []imapush.FailureKind

		for _, it := range chunk {
			wg.Add(1)
			go func(it PushJobItem) {
				defer wg.Done()
				res := s.runPushItem(ctx, exp, it)
				mu.Lock()
				switch res.Status {
				case ExportItemDone:
					succeeded++
				case ExportItemFailed:
					failed++
					chunkKinds = append(chunkKinds, imapush.ParseKind(res.Kind))
				}
				mu.Unlock()
			}(it)
		}
		wg.Wait()

		failedKinds = append(failedKinds, chunkKinds...)

		processed := succeeded + failed
		if !degraded && processed >= exportDegradeMinSamples &&
			float64(failed)/float64(processed) >= exportDegradeFailRate {
			concurrency = 1
			degraded = true
			_ = s.store.SetPushJobConcurrency(jobID, concurrency, true)
			log.Warn().Str("jobID", jobID).
				Int("processed", processed).Int("failed", failed).
				Msg("bizhub: 推送失败率偏高，并发已降级为 1")
		}

		// 熔断：整块全是永久失败（凭证 / skill 升级 / 上游 KB 错） → 停手
		if shouldAbortPushChunk(chunkKinds) {
			aborted = true
			remaining := work[i:]
			for _, it := range remaining {
				it.Status = ExportItemSkipped
				it.Kind = string(imapush.KindCredential)
				it.Error = "连续触发永久失败，本轮已暂停；处理后点「重试」继续"
				_ = s.store.UpdatePushJobItem(it)
			}
			log.Error().Str("jobID", jobID).Int("skipped", len(remaining)).
				Msg("bizhub: 推送触发熔断，剩余条目已暂停")
			break
		}
	}

	status, msg := ExportJobDone, ""
	switch {
	case ctx.Err() != nil:
		status, msg = ExportJobCanceled, "任务已被取消"
	case aborted:
		status = ExportJobFailed
		msg = "连续触发永久失败，本轮已暂停；处理后点「重试」继续"
	case failed > 0 && succeeded == 0:
		status = ExportJobFailed
		if label := dominantPushKindLabel(failedKinds); label != "" {
			msg = "全部条目推送失败：" + label + "，处理后点「重试」继续"
		} else {
			msg = "全部条目推送失败"
		}
	}

	s.releasePushJob(jobID)

	if err := s.store.SetPushJobStatus(jobID, status, msg, true); err != nil {
		log.Warn().Err(err).Str("jobID", jobID).Msg("bizhub: 更新推送任务状态失败")
	}
	log.Info().Str("jobID", jobID).Str("status", status).
		Int("succeeded", succeeded).Int("failed", failed).Msg("bizhub: 推送任务结束")
}

// releasePushJob 释放并发名额（幂等）
func (s *Service) releasePushJob(jobID string) {
	s.pushJobMu.Lock()
	defer s.pushJobMu.Unlock()
	if s.activePushJob == jobID {
		s.activePushJob = ""
		s.pushJobCancel = nil
		s.pushJobCtx = nil
	}
}

// runPushItem 推送单篇，并把结果写回条目与归档记录
//
// it.JobID 由 runPushJob 在切片前注入 —— 这里不能再覆盖，否则 UpdatePushJobItem
// 的 WHERE job_id=? 条件会匹配 0 行（job_id 为空串），导致所有 runPushItem
// 的状态更新（包括 "running → failed"）都 silently no-op。
// 这是个一踩就碎的细节，但属于数据正确性而非性能优化，所以放在醒目位置。
func (s *Service) runPushItem(ctx context.Context, exp *imapush.Exporter, it PushJobItem) PushJobItem {
	it.Status = ExportItemRunning
	it.Kind = ""
	it.Error = ""
	_ = s.store.UpdatePushJobItem(it)

	article, err := s.store.GetArticle(it.ArticleID)
	if err != nil || article == nil {
		it.Status = ExportItemSkipped
		it.Error = "文章已不存在（可能已被重新同步移除）"
		_ = s.store.UpdatePushJobItem(it)
		return it
	}

	itemCtx, cancel := context.WithTimeout(ctx, pushItemTimeout)
	defer cancel()

	results, err := exp.Push(itemCtx, []string{article.URL})
	if err != nil {
		kind := imapush.KindOf(err)
		it.Status = ExportItemFailed
		it.Kind = string(kind)
		it.Error = err.Error()
		logPushFailure(it.ArticleID, it.JobID, err)
		_ = s.store.UpdatePushJobItem(it)
		// 进程层失败：所有 URL 都按同一 kind 失败；本批只有 1 个 URL，按其处理。
		_ = s.store.UpsertPushRecord(article.ID, "", PushStatusFailed, string(kind), err.Error())
		return it
	}

	if len(results) == 0 {
		it.Status = ExportItemFailed
		it.Kind = string(imapush.KindUnknown)
		it.Error = "推送器未返回结果"
		_ = s.store.UpdatePushJobItem(it)
		return it
	}

	r := results[0]
	if r.Kind != "" {
		// 业务层失败
		it.Status = ExportItemFailed
		it.Kind = string(r.Kind)
		it.Error = r.RetMsg
		_ = s.store.UpdatePushJobItem(it)
		_ = s.store.UpsertPushRecord(article.ID, "", PushStatusFailed, string(r.Kind), r.RetMsg)
		return it
	}

	it.Status = ExportItemDone
	it.MediaID = r.MediaID
	_ = s.store.UpdatePushJobItem(it)
	_ = s.store.UpsertPushRecord(article.ID, r.MediaID, PushStatusPushed, "", "")
	return it
}

// shouldAbortPushChunk 判断这一块结果是否触发熔断（推送版）
func shouldAbortPushChunk(kinds []imapush.FailureKind) bool {
	if len(kinds) < 2 {
		return false
	}
	for _, k := range kinds {
		if !k.NeedsHuman() {
			return false
		}
	}
	return true
}

// dominantPushKindLabel 所有失败都是同一类时给出该类的中文标签
func dominantPushKindLabel(kinds []imapush.FailureKind) string {
	if len(kinds) == 0 {
		return ""
	}
	first := kinds[0]
	for _, k := range kinds {
		if k != first {
			return ""
		}
	}
	return first.Label()
}

// IsExportConfigured 返回 MD 导出是否已配置
func (s *Service) IsExportConfigured() bool {
	if s.config == nil {
		return false
	}
	return s.config.GetMDExportScript() != "" && s.config.GetMDExportDir() != ""
}

// IsPushConfigured 返回 IMA 推送是否已配置。
//
// 比 IsExportConfigured 更严：不仅要 KB ID 非空，还要 skill_dir 真实存在
// ima_api.cjs（空路径或路径错都应该被视作未启用，避免 push handler 启动后
// 才报 dependency 错）。
func (s *Service) IsPushConfigured() bool {
	if s.config == nil {
		return false
	}
	skillDir := s.config.GetIMAPushSkillDir()
	kbID := s.config.GetIMAPushKBID()
	if skillDir == "" || kbID == "" {
		return false
	}
	// 检查 imaskai 是否真实可用 —— 缺这个会让 push 启动时直接报 dependency 错，
	// 在 IsPushConfigured 这一层就该拦下。
	apiPath := skillDir + "/ima_api.cjs"
	if _, err := os.Stat(apiPath); err != nil {
		return false
	}
	return true
}

// IsArticleExported 链式校验：文章是否已经 MD 归档成功。
//
// status IN ('exported', 'summary_generated') 视为已归档；
// 状态为 'failed' 或没有记录都视为未归档。
//
// 这是 push 候选集的唯一判定点 —— 单篇 handler 与 buildPushPlan 共用，
// 避免「页面说能 push、点下去报错」的静默偏差。
func (s *Service) IsArticleExported(articleID int64) (bool, error) {
	return s.store.IsArticleExported(articleID)
}

// --- IMA 推送相关 ---

// getPushExporter 懒初始化推送器（依赖 config）。
//
// 与 getExporter 同思路：lazy init 让"未启用"和"启用但配置错"两种状态都
// 在第一次 PushArticle 调用时才暴露，避免启动期硬依赖配置。
func (s *Service) getPushExporter() (*imapush.Exporter, error) {
	if s.pusher != nil {
		return s.pusher, nil
	}
	if s.config == nil {
		return nil, fmt.Errorf("imapush: config not set")
	}
	skillDir := s.config.GetIMAPushSkillDir()
	kbID := s.config.GetIMAPushKBID()
	if skillDir == "" || kbID == "" {
		return nil, fmt.Errorf("imapush: ima_push_skill_dir and ima_push_kb_id must be configured")
	}
	folderID := s.config.GetIMAPushFolderID()
	exp, err := imapush.New(imapush.Config{
		SkillDir:        skillDir,
		KnowledgeBaseID: kbID,
		FolderID:        folderID,
	})
	if err != nil {
		return nil, err
	}
	s.pusher = exp
	return exp, nil
}

// PushArticleResult 单篇推送结果
type PushArticleResult struct {
	ArticleID int64  `json:"articleID"`
	Title     string `json:"title"`
	Account   string `json:"account"`
	URL       string `json:"url"`
	MediaID   string `json:"mediaID"`
	Status    string `json:"status"`
	Duration  string `json:"duration"`
}

// ErrArticleNotExported 推送的链式前置校验失败：文章未 export 成功。
//
// 单篇 handler 与 buildPushPlan 都用 errors.Is(err, ErrArticleNotExported) 判断，
// 403/409 状态码由此 error 触发。把它做成 exported error 是为了不让字符串错误
// 在调用方传话时丢语义。
var ErrArticleNotExported = errors.New("imapush: article has not been exported yet")

// PushArticle 推送单篇文章到 IMA。
//
// 链式前置：调用方（handler / 批量任务）已校验 IsArticleExported，
// 本方法不再重复校验 —— 复用这条 contract 是为了不让同一语义在校验链上
// 散在多处。
func (s *Service) PushArticle(ctx context.Context, articleID int64) (*PushArticleResult, error) {
	article, err := s.store.GetArticle(articleID)
	if err != nil {
		return nil, err
	}
	if article == nil {
		return nil, fmt.Errorf("article not found: %d", articleID)
	}

	exp, err := s.getPushExporter()
	if err != nil {
		return nil, err
	}

	result, err := exp.PushOne(ctx, article.URL)
	if err != nil {
		kind := imapush.KindOf(err)
		// 进程层失败：不知道 article 是否真的进了 IMA，全部按失败记
		_ = s.store.UpsertPushRecord(articleID, "", PushStatusFailed, string(kind), err.Error())
		logPushFailure(articleID, "", err)
		return nil, err
	}

	if result.Kind != "" {
		// 业务层失败（ret_code != 0 或整批级业务错误）
		_ = s.store.UpsertPushRecord(articleID, "", PushStatusFailed, string(result.Kind), result.RetMsg)
		return nil, &imapush.PushError{Kind: result.Kind, Msg: result.RetMsg}
	}

	// 成功
	if err := s.store.UpsertPushRecord(articleID, result.MediaID, PushStatusPushed, "", ""); err != nil {
		log.Warn().Err(err).Int64("articleID", articleID).Msg("bizhub: upsert push record failed")
	}

	return &PushArticleResult{
		ArticleID: articleID,
		Title:     article.Title,
		Account:   article.GHName,
		URL:       article.URL,
		MediaID:   result.MediaID,
		Status:    PushStatusPushed,
		Duration:  result.Duration.String(),
	}, nil
}

// logPushFailure 把推送失败的 stderr 摘要写进服务日志（不进库）。
//
// 与 logExportFailure 同款：界面只显示一行分类，但服务日志要保留 stderr 原文
// 方便排查 imaskai / 凭证 / 网络错。
func logPushFailure(articleID int64, jobID string, err error) {
	ev := log.Warn().Int64("articleID", articleID).
		Str("kind", string(imapush.KindOf(err)))
	if jobID != "" {
		ev = ev.Str("jobID", jobID)
	}
	var pe *imapush.PushError
	if errors.As(err, &pe) {
		ev = ev.Int("exitCode", pe.ExitCode).
			Str("stderr", tailLines(pe.Stderr, exportLogStderrLines))
	}
	ev.Msg("bizhub: 推送失败详情")
}
