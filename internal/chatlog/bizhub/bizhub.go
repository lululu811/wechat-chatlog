// Package bizhub 提供公众号文章汇总功能。
// 从微信本地 biz_message_0.db 解析公众号文章数据，按 ghid 分类持久化到独立 SQLite，
// 并通过 HTTP API 和 Web UI 提供查询和展示。
package bizhub

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/sjzar/chatlog/internal/chatlog/bizhub/mdexport"
	"github.com/sjzar/chatlog/internal/wechatdb"
)

// Config bizhub 可选的配置源（用于 summary 抓取并发等覆写）。可注入 nil。
type Config interface {
	GetSummaryFetchContent() bool
	GetSummaryFetchConcurrency() int
	GetLLMMaxTokens() int
	GetFeedSummaryCacheHours() int
	GetMDExportDir() string
	GetMDExportScript() string
}

// Service 公众号文章汇总服务
type Service struct {
	store    *Store
	syncer   *Syncer
	workDir  string
	config   Config
	exporter *mdexport.Exporter // lazy init

	mu         sync.RWMutex
	syncing    bool
	lastResult *SyncResult

	// 轻量缓存：只缓存公众号列表和标签（数据量小）
	cacheMu  sync.RWMutex
	accounts []Account
	tags     []Tag
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

// Start 启动服务（后台执行全量同步）
func (s *Service) Start() {
	go func() {
		log.Info().Msg("bizhub: starting full sync")
		result, err := s.SyncAll()
		if err != nil {
			log.Error().Err(err).Msg("bizhub: full sync failed")
			return
		}
		log.Info().
			Int("accounts", len(result.Accounts)).
			Int("totalArticles", result.TotalArticles).
			Int("newArticles", result.NewArticles).
			Msg("bizhub: full sync completed")

		s.refreshLightCache()
		s.InitTags()
	}()
}

// Stop 停止服务
func (s *Service) Stop() error {
	return s.store.Close()
}

// SetConfig 注入可选配置源（用于 summary 抓取/LLM 调用的覆写）。可传 nil。
func (s *Service) SetConfig(cfg Config) {
	s.config = cfg
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

// CountListArticles 与 ListArticles 同条件的总数，查询失败时返回 0
func (s *Service) CountListArticles(ghid string, days int) int {
	n, _ := s.store.CountListArticles(ArticleFilter{GHID: ghid, Days: days})
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

const summarySystemPrompt = "你是公众号内容分析师。根据用户提供的文章列表与可选指令，生成结构化分析报告。" +
	"你必须严格输出合法 JSON，遵守以下 schema 且不得出现 JSON 之外的字符。" +
	"如果输出代码块请用 \x60\x60\x60json\x60\x60\x60。" +
	"schema: {highlights:[string], themes:[{title,summary,articles:[{ghName,title,url}]}], mustReads:[{ghName,title,url,score(1-10),reason}], byAccount:[{ghName,digest}], summary:string}。"

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
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

	text, inTok, outTok, err := llm.CompleteWithSystem(ctx, summarySystemPrompt, sb.String())
	if err != nil {
		return nil, err
	}

	var structured json.RawMessage
	if raw, ok := ParseStructured(text); ok {
		structured = raw
	} else {
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

const feedSummarySystemPrompt = "你是公众号内容分析师。用户会给你一份我关注的微信公众号在指定时间窗口内发布的文章列表（仅含标题、公众号名、发布时间、摘要、链接，无正文）。" +
	"你必须严格输出合法 JSON，不得出现 JSON 之外的任何文字；如需包裹请使用 \x60\x60\x60json\x60\x60\x60。" +
	"schema 必须严格遵守：{\"headline\":\"<一句话整体概述>\",\"themes\":[{\"title\":\"<主题名>\",\"summary\":\"<一段总结>\",\"count\":<相关文章数>}],\"keywords\":[<8-12 个高频实体词或短语>]," +
	"\"hotTakes\":[<3-5 条跨文章的观点，每条一句话>]}" +
	"themes 至少 3 个，按文章数倒序；keywords 优先人名/机构/产品/技术名词；hotTakes 必须是跨文章综合提炼而非单篇摘要。" +
	"headline 不要包含时间词如 \"/最近/今日\"，聚焦主题判断。"

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

	text, _, _, err := llm.CompleteWithSystem(ctx, feedSummarySystemPrompt, sb.String())
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
		// 记录失败
		_ = s.store.UpsertExportRecord(articleID, article.URL, "", "", "failed", err.Error())
		return nil, err
	}

	// 记录成功
	if err := s.store.UpsertExportRecord(articleID, article.URL, result.MDPath, "", "exported", ""); err != nil {
		log.Warn().Err(err).Int64("articleID", articleID).Msg("bizhub: upsert export record failed")
	}

	return &ExportArticleResult{
		ArticleID: articleID,
		Title:     article.Title,
		Account:   result.Account,
		MDPath:    result.MDPath,
		Status:    "exported",
		Duration:  result.Duration.String(),
	}, nil
}

// ExportBatchRequest 批量导出请求
type ExportBatchRequest struct {
	Days  int      `json:"days"`
	Limit int      `json:"limit"`
	GHIDs []string `json:"ghIDs,omitempty"` // 为空则导出所有可见账号
}

// ExportBatchResult 批量导出结果
type ExportBatchResult struct {
	Total    int                    `json:"total"`
	Success  int                    `json:"success"`
	Failed   int                    `json:"failed"`
	Skipped  int                    `json:"skipped"`
	Details  []*ExportArticleResult `json:"details,omitempty"`
	Errors   []string               `json:"errors,omitempty"`
	Duration string                 `json:"duration"`
}

// ExportBatch 批量导出未导出的文章
func (s *Service) ExportBatch(ctx context.Context, req ExportBatchRequest) (*ExportBatchResult, error) {
	if req.Days <= 0 {
		req.Days = 30
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}

	articles, err := s.store.GetUnexportedArticles(req.Days, req.Limit)
	if err != nil {
		return nil, err
	}

	// 按 GHIDs 过滤
	if len(req.GHIDs) > 0 {
		ghidSet := make(map[string]struct{})
		for _, ghid := range req.GHIDs {
			ghidSet[ghid] = struct{}{}
		}
		filtered := make([]Article, 0, len(articles))
		for _, a := range articles {
			if _, ok := ghidSet[a.GHID]; ok {
				filtered = append(filtered, a)
			}
		}
		articles = filtered
	}

	exp, err := s.getExporter()
	if err != nil {
		return nil, err
	}

	start := time.Now()
	result := &ExportBatchResult{
		Total: len(articles),
	}

	for _, a := range articles {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		// 检查是否已导出
		existing, err := s.store.GetExportRecordByURL(a.URL)
		if err == nil && existing != nil && existing.Status == "exported" {
			result.Skipped++
			continue
		}

		exportResult, err := exp.Export(ctx, a.URL)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", a.Title, err))
			_ = s.store.UpsertExportRecord(a.ID, a.URL, "", "", "failed", err.Error())
			continue
		}

		_ = s.store.UpsertExportRecord(a.ID, a.URL, exportResult.MDPath, "", "exported", "")
		result.Success++
		result.Details = append(result.Details, &ExportArticleResult{
			ArticleID: a.ID,
			Title:     a.Title,
			Account:   exportResult.Account,
			MDPath:    exportResult.MDPath,
			Status:    "exported",
			Duration:  exportResult.Duration.String(),
		})
	}

	result.Duration = time.Since(start).String()
	return result, nil
}

// GetExportStatus 获取导出状态
func (s *Service) GetExportStatus() (exported, pending int, err error) {
	return s.store.GetExportStats()
}

// IsExportConfigured 返回 MD 导出是否已配置
func (s *Service) IsExportConfigured() bool {
	if s.config == nil {
		return false
	}
	return s.config.GetMDExportScript() != "" && s.config.GetMDExportDir() != ""
}
