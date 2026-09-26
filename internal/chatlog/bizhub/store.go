package bizhub

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"

	"github.com/chenliitaz/chatlog/internal/chatlog/bizhub/imapush"
	"github.com/chenliitaz/chatlog/internal/chatlog/bizhub/mdexport"
	"github.com/chenliitaz/chatlog/internal/model"
)

// HashURL 返回 URL 的 md5 hex 哈希（用于 biz_article_contents 主键）
func HashURL(u string) string {
	sum := md5.Sum([]byte(u))
	return hex.EncodeToString(sum[:])
}

// Store 管理公众号文章的本地持久化存储
type Store struct {
	db   *sql.DB
	path string
	mu   sync.RWMutex
}

// Account 公众号信息（含文章统计）
type Account struct {
	GHID         string `json:"ghID"`
	GHName       string `json:"ghName"`
	ArticleCount int    `json:"articleCount"`
	UpdatedAt    int64  `json:"updatedAt"`
	Hidden       bool   `json:"hidden"`
	Watched      bool   `json:"watched"`
}

// Article 公众号文章
type Article struct {
	ID          int64  `json:"id"`
	GHID        string `json:"ghID"`
	GHName      string `json:"ghName"`
	Title       string `json:"title"`
	Desc        string `json:"description"`
	URL         string `json:"url"`
	AppID       string `json:"appID"`
	LocalType   int64  `json:"localType"`
	LocalID     int64  `json:"localID"`
	SortSeq     int64  `json:"sortSeq"`
	PublishedAt int64  `json:"publishedAt"`
	SyncedAt    int64  `json:"syncedAt"`
	Bookmarked  bool   `json:"bookmarked"`
	IsRead      bool   `json:"isRead"`
	// 归档 / 推送 状态 —— 由 articleRows 的 LEFT JOIN biz_exported_articles 填充。
	// 前端用这两个字段决定按钮 disabled / 高亮。空字符串 = 没有归档记录。
	ExportStatus string `json:"exportStatus,omitempty"` // exported / summary_generated / failed / ""
	PushStatus   string `json:"pushStatus,omitempty"`   // pushed / push_failed / ""

	// Pipeline 状态机字段。Worker 推进时改这些，前端展示用。
	// 5 阶段正态：pending → fetched → md_exported → summarized → pushed
	// 失败态：failed:fetch / failed:mdexport / failed:summarize / failed:imapush
	// 详见 pipeline.go 的状态常量定义。
	PipelineStatus    string `json:"pipelineStatus,omitempty"`
	PipelineError     string `json:"pipelineError,omitempty"`
	PipelineAttempt   int    `json:"pipelineAttempt,omitempty"`
	PipelineUpdatedAt int64  `json:"pipelineUpdatedAt,omitempty"`

	// 正文与 AI 摘要（单篇详情查看时动态装载）
	Content    string   `json:"content,omitempty"`
	Digest     string   `json:"digest,omitempty"`
	Highlights []string `json:"highlights,omitempty"`
	Themes     []string `json:"themes,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
	MustRead   int      `json:"mustRead,omitempty"`
}

// Summary 关注公众号文章的 LLM 汇总
type Summary struct {
	ID           int64           `json:"id"`
	Days         int             `json:"days"`
	ArticleCount int             `json:"articleCount"`
	Content      string          `json:"content"`
	Instruction  string          `json:"instruction,omitempty"`
	Scope        string          `json:"scope,omitempty"`
	Structured   json.RawMessage `json:"structured,omitempty"`
	FetchedCount int             `json:"fetchedCount"`
	TokensIn     int             `json:"tokensIn"`
	TokensOut    int             `json:"tokensOut"`
	Model        string          `json:"model,omitempty"`
	CreatedAt    int64           `json:"createdAt"`
}

// FeedSummaryRow biz_feed_summaries 表的原始记录（headline + 完整 JSON）
type FeedSummaryRow struct {
	WindowDays int    `json:"windowDays"`
	Headline   string `json:"headline"`
	Structured string `json:"structured"`
	ComputedAt int64  `json:"computedAt"`
}

// Tag 标签
type Tag struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	CreatedAt int64  `json:"createdAt"`
}

// AccountWithTag 公众号（含标签）
type AccountWithTag struct {
	Account
	Tags []Tag `json:"tags"`
}

// 归档记录状态（biz_exported_articles.status）。
//
// 状态机：
//
//	(无记录) --归档成功--> exported --生成摘要--> summary_generated
//	(无记录) --归档失败--> failed  --再次批量归档--> exported
//
// 关键语义：failed 不是终态。下次批量归档会把 failed 重新纳入候选，
// 直到重试次数用尽（attempts >= maxExportAttempts）或失败类型被判定为
// 「需人工处理」（验证码 / 脚本缺失 / 输出目录不可写）为止。
const (
	ExportStatusExported   = "exported"
	ExportStatusSummarized = "summary_generated"
	ExportStatusFailed     = "failed"
)

// maxExportAttempts 单篇文章自动重试次数上限。
//
// 为什么要设上限：可重试失败（超时 / 限流）如果一直失败，说明不是偶发问题，
// 无限重试只会每轮都在同一篇上浪费时间且加重风控。到顶后落库为需人工处理，
// 由人在管理页看失败原因、手动重试。
const maxExportAttempts = 3

// ExportedArticle MD 导出记录（biz_exported_articles 表）
type ExportedArticle struct {
	ID          int64  `json:"id"`
	ArticleID   int64  `json:"articleID"`
	SourceURL   string `json:"sourceURL"`
	MDPath      string `json:"mdPath"`
	SummaryPath string `json:"summaryPath"`
	Status      string `json:"status"` // exported / summary_generated / failed
	Error       string `json:"error"`
	Kind        string `json:"kind"`     // 失败分类（mdexport.FailureKind），成功时为空
	Attempts    int    `json:"attempts"` // 累计尝试次数，用于重试上限判定
	ExportedAt  int64  `json:"exportedAt"`
	SummaryAt   int64  `json:"summaryAt"`

	// 推送记录（IMA）—— 与 MD 字段独立。
	// 一篇文章可以 MD 成功 / IMA 未推、MD 失败 / IMA 已推，状态由这两组字段组合。
	PushedStatus  string `json:"pushedStatus,omitempty"`  // pushed / push_failed / ""
	PushedMediaID string `json:"pushedMediaID,omitempty"` // IMA 返回的 media_id
	PushedAt      int64  `json:"pushedAt"`
	PushAttempts  int    `json:"pushAttempts"`
	PushKind      string `json:"pushKind,omitempty"`
}

// ExportJob 批量归档任务（biz_export_jobs 表）。
//
// 任务化之前，批量导出是一次同步 HTTP 请求：关掉页面 / 服务重启都会让进度
// 彻底丢失，前端只能用假进度条（30% / 60% / 100%）糊弄。任务化之后
// 「任务 + 逐篇结果」都落库，进度可轮询、可断点续跑。
type ExportJob struct {
	ID          string `json:"id"`
	Status      string `json:"status"` // running / done / failed / canceled / interrupted
	Days        int    `json:"days"`
	MaxItems    int    `json:"maxItems"`
	Concurrency int    `json:"concurrency"`
	Degraded    bool   `json:"degraded"` // 是否因失败率过高自动降级过并发
	Total       int    `json:"total"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	FinishedAt  int64  `json:"finishedAt"`
	Error       string `json:"error"`

	// 以下为按 item 聚合的实时计数（读取时填充，不落库）
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	Running   int `json:"running"`
	Pending   int `json:"pending"`
}

// Done 已完成（含成功与中断，中断也意味着本轮不再推进）
func (j *ExportJob) Done() bool {
	return j.Status != ExportJobRunning
}

// Processed 已产生终态的条目数（成功 + 失败 + 跳过），用于进度百分比
func (j *ExportJob) Processed() int {
	return j.Succeeded + j.Failed + j.Skipped
}

// Percent 进度百分比（0–100）。total 为 0 时返回 100，避免前端算出 NaN。
func (j *ExportJob) Percent() int {
	if j.Total <= 0 {
		return 100
	}
	p := j.Processed() * 100 / j.Total
	if p > 100 {
		p = 100
	}
	return p
}

// 任务状态
const (
	ExportJobRunning     = "running"
	ExportJobDone        = "done"
	ExportJobFailed      = "failed"
	ExportJobCanceled    = "canceled"
	ExportJobInterrupted = "interrupted" // 服务重启导致中断，可续跑
)

// 任务条目状态
const (
	ExportItemPending = "pending"
	ExportItemRunning = "running"
	ExportItemDone    = "done"
	ExportItemFailed  = "failed"
	ExportItemSkipped = "skipped"
)

// ExportJobItem 批量归档任务中的单篇条目
type ExportJobItem struct {
	JobID     string `json:"jobID"`
	ArticleID int64  `json:"articleID"`
	Title     string `json:"title"`
	Account   string `json:"account"`
	Status    string `json:"status"` // pending / running / done / failed / skipped
	Kind      string `json:"kind"`   // 失败分类，仅 failed/skipped 有值
	Error     string `json:"error"`
	MDPath    string `json:"mdPath"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Open 打开或创建 biz_articles.db
func Open(workDir string) (*Store, error) {
	if workDir == "" {
		return nil, ErrWorkDirEmpty
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(workDir, "biz_articles.db")

	// 迁移前备份：在打开数据库之前检查并备份（避免 sql.Open 创建空文件）
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		backupPath := path + ".bak"
		// 仅在备份文件不存在时创建（避免每次启动都覆盖备份）
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			if err := copyFile(path, backupPath); err != nil {
				log.Warn().Err(err).Str("path", path).Msg("bizhub: failed to backup database before migration")
			} else {
				log.Info().Str("backup", backupPath).Msg("bizhub: database backed up before migration")
			}
		}
	}

	// Use WAL mode, busy timeout, and connection pooling for concurrent access
	db, err := sql.Open("sqlite3", path+"?_journal=WAL&_busy_timeout=10000&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	// Set connection pool for better concurrent access
	db.SetMaxOpenConns(1) // SQLite works best with single writer
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Path 返回数据库文件路径
func (s *Store) Path() string { return s.path }

// Close 关闭数据库连接
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS biz_accounts (
    gh_id     TEXT PRIMARY KEY,
    gh_name   TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS biz_articles (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    gh_id      TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    url        TEXT NOT NULL DEFAULT '',
    app_id     TEXT NOT NULL DEFAULT '',
    local_type INTEGER NOT NULL DEFAULT 0,
    local_id   INTEGER NOT NULL DEFAULT 0,
    sort_seq   INTEGER NOT NULL DEFAULT 0,
    published_at INTEGER NOT NULL DEFAULT 0,
    synced_at  INTEGER NOT NULL DEFAULT 0,
    UNIQUE(gh_id, url)
);

CREATE TABLE IF NOT EXISTS biz_tags (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    color      TEXT NOT NULL DEFAULT '#3b82f6',
    created_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS biz_account_tags (
    gh_id      TEXT NOT NULL,
    tag_id     INTEGER NOT NULL,
    PRIMARY KEY (gh_id, tag_id),
    FOREIGN KEY (gh_id) REFERENCES biz_accounts(gh_id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id) REFERENCES biz_tags(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS biz_summaries (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    days          INTEGER NOT NULL,
    article_count INTEGER NOT NULL DEFAULT 0,
    content       TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS biz_article_contents (
    url_hash    TEXT PRIMARY KEY,
    url         TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL DEFAULT '',
    status      INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT '',
    fetched_at  INTEGER NOT NULL DEFAULT 0,
    bytes       INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS biz_feed_summaries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    window_days INTEGER NOT NULL,
    headline    TEXT NOT NULL DEFAULT '',
    structured  TEXT NOT NULL DEFAULT '',
    computed_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS biz_daily_digests (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    digest_date TEXT NOT NULL,
    headline    TEXT NOT NULL DEFAULT '',
    picks       TEXT NOT NULL DEFAULT '[]',
    article_ids TEXT NOT NULL DEFAULT '[]',
    computed_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(digest_date)
);

CREATE INDEX IF NOT EXISTS idx_articles_ghid ON biz_articles(gh_id);
CREATE INDEX IF NOT EXISTS idx_articles_time ON biz_articles(published_at);
CREATE INDEX IF NOT EXISTS idx_account_tags_ghid ON biz_account_tags(gh_id);
CREATE INDEX IF NOT EXISTS idx_feed_summaries_window ON biz_feed_summaries(window_days, computed_at DESC);

CREATE TABLE IF NOT EXISTS biz_exported_articles (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    article_id   INTEGER NOT NULL,
    source_url   TEXT NOT NULL,
    md_path      TEXT NOT NULL DEFAULT '',
    summary_path TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'exported',
    error        TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL DEFAULT '',
    attempts     INTEGER NOT NULL DEFAULT 0,
    exported_at  INTEGER NOT NULL DEFAULT 0,
    summary_at   INTEGER NOT NULL DEFAULT 0,
    UNIQUE(article_id)
);

CREATE INDEX IF NOT EXISTS idx_exported_articles_source_url ON biz_exported_articles(source_url);

-- 批量归档任务（Sprint 1 任务化）。任务与逐篇结果落库，服务重启后可断点续跑。
CREATE TABLE IF NOT EXISTS biz_export_jobs (
    id          TEXT PRIMARY KEY,
    status      TEXT NOT NULL DEFAULT 'running',
    days        INTEGER NOT NULL DEFAULT 30,
    max_items   INTEGER NOT NULL DEFAULT 100,
    concurrency INTEGER NOT NULL DEFAULT 3,
    degraded    INTEGER NOT NULL DEFAULT 0,
    total       INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0,
    finished_at INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_export_jobs_status ON biz_export_jobs(status, created_at DESC);

CREATE TABLE IF NOT EXISTS biz_export_job_items (
    job_id     TEXT NOT NULL,
    article_id INTEGER NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    account    TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'pending',
    kind       TEXT NOT NULL DEFAULT '',
    error      TEXT NOT NULL DEFAULT '',
    md_path    TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (job_id, article_id)
);

CREATE INDEX IF NOT EXISTS idx_export_job_items_status ON biz_export_job_items(job_id, status);
`
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}

	columns := make(map[string]bool)
	rows, err := s.db.Query(`PRAGMA table_info(biz_accounts)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !columns["hidden"] {
		if _, err := s.db.Exec(`ALTER TABLE biz_accounts ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !columns["watched"] {
		if _, err := s.db.Exec(`ALTER TABLE biz_accounts ADD COLUMN watched INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}

	// biz_articles 渐进式列扩展（PRAGMA 检测，不存在则 ALTER）
	artCols := make(map[string]bool)
	artRows, err := s.db.Query(`PRAGMA table_info(biz_articles)`)
	if err != nil {
		return err
	}
	for artRows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := artRows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			artRows.Close()
			return err
		}
		artCols[name] = true
	}
	if err := artRows.Close(); err != nil {
		return err
	}
	if !artCols["bookmarked"] {
		if _, err := s.db.Exec(`ALTER TABLE biz_articles ADD COLUMN bookmarked INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !artCols["is_read"] {
		if _, err := s.db.Exec(`ALTER TABLE biz_articles ADD COLUMN is_read INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}

	// Pipeline 状态机字段（PR1 引入）。
	//
	// pipeline_status 默认 'pending'：所有未处理的文章（包括迁移前已存在的
	// 23822 条）都会被 Worker 第一次 tick 捡起来跑一遍 —— 这是显式选择，
	// 让 Worker 重新建立可信的产物基础，不依赖历史不完整的 biz_exported_articles 记录。
	// 想「迁移不重抓」的用户可以把 BizWorkerEnabled 关掉、或者把 status 手动
	// 改成 'pushed' 后再开 Worker。
	if !artCols["pipeline_status"] {
		if _, err := s.db.Exec(`ALTER TABLE biz_articles ADD COLUMN pipeline_status TEXT NOT NULL DEFAULT 'pending'`); err != nil {
			return err
		}
	}
	if !artCols["pipeline_error"] {
		if _, err := s.db.Exec(`ALTER TABLE biz_articles ADD COLUMN pipeline_error TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !artCols["pipeline_attempt"] {
		if _, err := s.db.Exec(`ALTER TABLE biz_articles ADD COLUMN pipeline_attempt INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !artCols["pipeline_updated_at"] {
		if _, err := s.db.Exec(`ALTER TABLE biz_articles ADD COLUMN pipeline_updated_at INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}

	// Pipeline 状态机索引：Worker 按 (status, updated_at ASC) 拉候选，
	// 走这个复合索引能避免全表扫描 23000+ 行。
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_pipeline ON biz_articles(pipeline_status, pipeline_updated_at)`); err != nil {
		return err
	}

	// biz_summaries 渐进式列扩展（PRAGMA 检测，不存在则 ALTER）
	sumCols := make(map[string]bool)
	sumRows, err := s.db.Query(`PRAGMA table_info(biz_summaries)`)
	if err != nil {
		return err
	}
	for sumRows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := sumRows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			sumRows.Close()
			return err
		}
		sumCols[name] = true
	}
	if err := sumRows.Close(); err != nil {
		return err
	}
	type summaryCol struct {
		name string
		def  string
	}
	sumAlters := []summaryCol{
		{"instruction", "TEXT NOT NULL DEFAULT ''"},
		{"scope", "TEXT NOT NULL DEFAULT 'watched'"},
		{"structured", "TEXT NOT NULL DEFAULT ''"},
		{"fetched_count", "INTEGER NOT NULL DEFAULT 0"},
		{"tokens_in", "INTEGER NOT NULL DEFAULT 0"},
		{"tokens_out", "INTEGER NOT NULL DEFAULT 0"},
		{"model", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range sumAlters {
		if sumCols[c.name] {
			continue
		}
		if _, err := s.db.Exec(`ALTER TABLE biz_summaries ADD COLUMN ` + c.name + ` ` + c.def); err != nil {
			return err
		}
	}

	// biz_exported_articles 渐进式列扩展。
	//
	// kind / attempts 是「失败可重试」语义的落地字段：老库里已有 status='failed'
	// 的记录（它们此前被 e.id IS NULL 谓词永久排除在候选之外），ALTER 后
	// attempts 默认 0、kind 默认空 —— 空 kind 落到 KindUnknown，按可重试处理，
	// 于是这些历史失败文章会在下一次批量归档时被重新捡起来。
	expCols := make(map[string]bool)
	expRows, err := s.db.Query(`PRAGMA table_info(biz_exported_articles)`)
	if err != nil {
		return err
	}
	for expRows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := expRows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			expRows.Close()
			return err
		}
		expCols[name] = true
	}
	if err := expRows.Close(); err != nil {
		return err
	}
	type exportCol struct {
		name string
		def  string
	}
	expAlters := []exportCol{
		{"kind", "TEXT NOT NULL DEFAULT ''"},
		{"attempts", "INTEGER NOT NULL DEFAULT 0"},
		// 推送状态字段（与 MD 归档的 status / kind / attempts 完全独立）：
		// 一篇可以 MD 失败 / IMA 已推，或 MD 成功 / IMA 未推。
		{"pushed_at", "INTEGER NOT NULL DEFAULT 0"},
		{"pushed_media_id", "TEXT NOT NULL DEFAULT ''"},
		{"pushed_status", "TEXT NOT NULL DEFAULT ''"},
		{"push_attempts", "INTEGER NOT NULL DEFAULT 0"},
		{"push_kind", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range expAlters {
		if expCols[c.name] {
			continue
		}
		if _, err := s.db.Exec(`ALTER TABLE biz_exported_articles ADD COLUMN ` + c.name + ` ` + c.def); err != nil {
			return err
		}
	}

	// biz_push_jobs / biz_push_job_items —— 批量推送任务镜像 biz_export_jobs。
	//
	// 为什么单建一张表、不复用 export 的 job+item 表：聚合 SQL / 索引 / 状态机
	// 都与 export 任务独立；用 job_type 字段共享一份表会让所有聚合查询都得带
	// 类型过滤，复杂度上移到 store 层。push 是新功能、独立的并发槽位、独立
	// 的失败分类，独立表更干净。
	pushJobSchema := `
CREATE TABLE IF NOT EXISTS biz_push_jobs (
    id          TEXT PRIMARY KEY,
    status      TEXT NOT NULL DEFAULT 'running',
    days        INTEGER NOT NULL DEFAULT 30,
    max_items   INTEGER NOT NULL DEFAULT 100,
    concurrency INTEGER NOT NULL DEFAULT 3,
    degraded    INTEGER NOT NULL DEFAULT 0,
    total       INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0,
    finished_at INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_push_jobs_status ON biz_push_jobs(status, created_at DESC);

CREATE TABLE IF NOT EXISTS biz_push_job_items (
    job_id     TEXT NOT NULL,
    article_id INTEGER NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    account    TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'pending',
    kind       TEXT NOT NULL DEFAULT '',
    error      TEXT NOT NULL DEFAULT '',
    media_id   TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (job_id, article_id)
);

CREATE INDEX IF NOT EXISTS idx_push_job_items_status ON biz_push_job_items(job_id, status);
`
	if _, err := s.db.Exec(pushJobSchema); err != nil {
		return err
	}

	return nil
}

// UpsertAccounts 批量更新公众号信息
func (s *Store) UpsertAccounts(accounts []Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO biz_accounts (gh_id, gh_name, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(gh_id) DO UPDATE SET gh_name=excluded.gh_name, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, acc := range accounts {
		if _, err := stmt.Exec(acc.GHID, acc.GHName, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertArticles 批量插入文章（忽略已存在的）
func (s *Store) UpsertArticles(articles []*model.BizMessage) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO biz_articles 
		(gh_id, title, description, url, app_id, local_type, local_id, sort_seq, published_at, synced_at) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	count := 0
	for _, m := range articles {
		if m == nil || m.URL == "" {
			continue
		}
		result, err := stmt.Exec(
			m.GHID, m.Title, m.Desc, m.URL, m.AppID,
			m.LocalType, m.LocalID, m.SortSeq,
			m.Time.Unix(), now,
		)
		if err != nil {
			continue
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			count++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// GetAccounts 获取公众号列表（含文章数统计，排除隐藏账号）
func (s *Store) GetAccounts() ([]Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT a.gh_id, a.gh_name, a.updated_at, a.watched, COUNT(b.id) as article_count
		FROM biz_accounts a
		LEFT JOIN biz_articles b ON a.gh_id = b.gh_id
		WHERE a.hidden = 0
		GROUP BY a.gh_id
		ORDER BY article_count DESC, a.gh_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		var acc Account
		var watched int
		if err := rows.Scan(&acc.GHID, &acc.GHName, &acc.UpdatedAt, &watched, &acc.ArticleCount); err != nil {
			return nil, err
		}
		acc.Watched = watched != 0
		accounts = append(accounts, acc)
	}
	return accounts, rows.Err()
}

// GetAllAccounts 获取所有公众号列表（含隐藏账号）
func (s *Store) GetAllAccounts() ([]Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT a.gh_id, a.gh_name, a.updated_at, a.hidden, a.watched, COUNT(b.id) as article_count
		FROM biz_accounts a
		LEFT JOIN biz_articles b ON a.gh_id = b.gh_id
		GROUP BY a.gh_id
		ORDER BY article_count DESC, a.gh_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		var acc Account
		var hidden, watched int
		if err := rows.Scan(&acc.GHID, &acc.GHName, &acc.UpdatedAt, &hidden, &watched, &acc.ArticleCount); err != nil {
			return nil, err
		}
		acc.Hidden = hidden != 0
		acc.Watched = watched != 0
		accounts = append(accounts, acc)
	}
	return accounts, rows.Err()
}

// SetAccountsHidden 批量设置公众号隐藏状态
func (s *Store) SetAccountsHidden(ghIDs []string, hidden bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE biz_accounts SET hidden = ? WHERE gh_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	v := 0
	if hidden {
		v = 1
	}
	for _, ghid := range ghIDs {
		if _, err := stmt.Exec(v, ghid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetAccountsWatched 批量设置公众号关注状态
func (s *Store) SetAccountsWatched(ghIDs []string, watched bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE biz_accounts SET watched = ? WHERE gh_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	v := 0
	if watched {
		v = 1
	}
	for _, ghid := range ghIDs {
		if _, err := stmt.Exec(v, ghid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ArticleFilter 是文章列表查询的筛选条件。
//
// 与 GetFeedArticles（关注动态流）的区别：这里不隐含任何关注/可见性过滤 ——
// 「全部」模式要当资料库用，得能翻到未关注、甚至已隐藏公众号的旧文。
// 两个条件都可选：GHID 为空即全部公众号，Days <= 0 即不限时间。
type ArticleFilter struct {
	GHID       string
	TagID      int64
	Days       int
	Limit      int
	Offset     int
	UnreadOnly bool
}

const articleRows = `
	SELECT a.id, a.gh_id, COALESCE(acc.gh_name, ''), a.title, a.description, a.url,
	       a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked, a.is_read,
	       COALESCE(e.status, ''), COALESCE(e.pushed_status, ''),
	       COALESCE(a.pipeline_status, ''), COALESCE(a.pipeline_error, ''),
	       COALESCE(a.pipeline_attempt, 0), COALESCE(a.pipeline_updated_at, 0)
	FROM biz_articles a
	LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id
	LEFT JOIN biz_exported_articles e ON a.id = e.article_id`

// articleWhere 生成 WHERE 子句与参数。列表查询与计数必须共用它，
// 否则 total 与实际返回条数会不一致（这类不一致只在翻页到头时才暴露）。
func articleWhere(f ArticleFilter) (string, []any) {
	var conds []string
	var args []any
	if f.GHID != "" {
		conds = append(conds, "a.gh_id = ?")
		args = append(args, f.GHID)
	}
	if f.TagID > 0 {
		conds = append(conds, "a.gh_id IN (SELECT gh_id FROM biz_account_tags WHERE tag_id = ?)")
		args = append(args, f.TagID)
	}
	if f.Days > 0 {
		conds = append(conds, "a.published_at >= ?")
		args = append(args, time.Now().Add(-time.Duration(f.Days)*24*time.Hour).Unix())
	}
	if f.UnreadOnly {
		conds = append(conds, "a.is_read = 0")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// scanArticles 收敛文章行扫描。列顺序必须与 articleRows 一致。
func scanArticles(rows *sql.Rows) ([]Article, error) {
	var articles []Article
	for rows.Next() {
		var a Article
		var bookmarked, isRead int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked, &isRead, &a.ExportStatus, &a.PushStatus, &a.PipelineStatus, &a.PipelineError, &a.PipelineAttempt, &a.PipelineUpdatedAt); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
		a.IsRead = isRead != 0
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// ListArticles 按筛选条件取文章（按发布时间倒序分页）。
func (s *Store) ListArticles(f ArticleFilter) ([]Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	where, args := articleWhere(f)
	args = append(args, limit, offset)
	rows, err := s.db.Query(articleRows+where+`
	ORDER BY a.published_at DESC
	LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArticles(rows)
}

// CountListArticles 统计 ListArticles 的命中总数（忽略 Limit/Offset）。
func (s *Store) CountListArticles(f ArticleFilter) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	where, args := articleWhere(f)
	var n int
	// where 只引用 biz_articles 的列，不需要 JOIN biz_accounts。
	err := s.db.QueryRow(`SELECT COUNT(1) FROM biz_articles a`+where, args...).Scan(&n)
	return n, err
}

// GetArticles 按 ghid 获取文章列表（分页）：单个公众号的全部文章，不限时间。
func (s *Store) GetArticles(ghid string, limit, offset int) ([]Article, error) {
	return s.ListArticles(ArticleFilter{GHID: ghid, Limit: limit, Offset: offset})
}

// GetArticle 获取单篇文章详情
func (s *Store) GetArticle(id int64) (*Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var a Article
	var bookmarked, isRead int
	err := s.db.QueryRow(`
		SELECT a.id, a.gh_id, COALESCE(acc.gh_name, ''), a.title, a.description, a.url,
		       a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked, a.is_read,
		       COALESCE(e.status, ''), COALESCE(e.pushed_status, ''),
		       COALESCE(a.pipeline_status, ''), COALESCE(a.pipeline_error, ''),
		       COALESCE(a.pipeline_attempt, 0), COALESCE(a.pipeline_updated_at, 0)
		FROM biz_articles a
		LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE a.id = ?
	`, id).Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked, &isRead, &a.ExportStatus, &a.PushStatus, &a.PipelineStatus, &a.PipelineError, &a.PipelineAttempt, &a.PipelineUpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	a.Bookmarked = bookmarked != 0
	a.IsRead = isRead != 0
	return &a, nil
}

// SearchArticles 搜索文章（按标题）
func (s *Store) SearchArticles(keyword string, limit, offset int) ([]Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.Query(`
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked, a.is_read,
		       COALESCE(e.status, ''), COALESCE(e.pushed_status, ''),
		       COALESCE(a.pipeline_status, ''), COALESCE(a.pipeline_error, ''),
		       COALESCE(a.pipeline_attempt, 0), COALESCE(a.pipeline_updated_at, 0)
		FROM biz_articles a
		LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE a.title LIKE ?
		AND a.gh_id NOT IN (SELECT gh_id FROM biz_accounts WHERE hidden = 1)
		ORDER BY a.published_at DESC
		LIMIT ? OFFSET ?
	`, "%"+keyword+"%", limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var a Article
		var bookmarked, isRead int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked, &isRead, &a.ExportStatus, &a.PushStatus, &a.PipelineStatus, &a.PipelineError, &a.PipelineAttempt, &a.PipelineUpdatedAt); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
		a.IsRead = isRead != 0
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// GetFeedArticles 获取可见且被关注公众号在 days 天内的文章流（按发布时间倒序分页）
func (s *Store) GetFeedArticles(days, limit, offset int) ([]Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if days <= 0 {
		days = 7
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	rows, err := s.db.Query(`
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked, a.is_read,
		       COALESCE(e.status, ''), COALESCE(e.pushed_status, ''),
		       COALESCE(a.pipeline_status, ''), COALESCE(a.pipeline_error, ''),
		       COALESCE(a.pipeline_attempt, 0), COALESCE(a.pipeline_updated_at, 0)
		FROM biz_articles a
		JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE acc.hidden = 0 AND acc.watched = 1 AND a.published_at >= ?
		ORDER BY a.published_at DESC
		LIMIT ? OFFSET ?
	`, since, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var a Article
		var bookmarked, isRead int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked, &isRead, &a.ExportStatus, &a.PushStatus, &a.PipelineStatus, &a.PipelineError, &a.PipelineAttempt, &a.PipelineUpdatedAt); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
		a.IsRead = isRead != 0
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// GetWatchedArticlesSince 获取关注公众号自 since 以来的文章（按发布时间倒序，desc 截断到 100 字符）
func (s *Store) GetWatchedArticlesSince(since time.Time, limit int) ([]Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 30
	}

	rows, err := s.db.Query(`
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked, a.is_read,
		       COALESCE(e.status, ''), COALESCE(e.pushed_status, ''),
		       COALESCE(a.pipeline_status, ''), COALESCE(a.pipeline_error, ''),
		       COALESCE(a.pipeline_attempt, 0), COALESCE(a.pipeline_updated_at, 0)
		FROM biz_articles a
		JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE acc.hidden = 0 AND acc.watched = 1 AND a.published_at >= ?
		ORDER BY a.published_at DESC
		LIMIT ?
	`, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var a Article
		var bookmarked, isRead int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked, &isRead, &a.ExportStatus, &a.PushStatus, &a.PipelineStatus, &a.PipelineError, &a.PipelineAttempt, &a.PipelineUpdatedAt); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
		a.IsRead = isRead != 0
		if desc := []rune(a.Desc); len(desc) > 100 {
			a.Desc = string(desc[:100])
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// SetArticlesBookmarked 批量设置文章收藏状态（事务）
func (s *Store) SetArticlesBookmarked(ids []int64, bookmarked bool) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	v := 0
	if bookmarked {
		v = 1
	}
	stmt, err := tx.Prepare(`UPDATE biz_articles SET bookmarked = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.Exec(v, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetArticlesRead 批量标记文章已读/未读
func (s *Store) SetArticlesRead(ids []int64, read bool) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	v := 0
	if read {
		v = 1
	}
	stmt, err := tx.Prepare(`UPDATE biz_articles SET is_read = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.Exec(v, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MarkAllRead 把当前可见的文章全部标记为已读。
// ghid 非空时只标记该账号；days > 0 时只标记最近 N 天。
func (s *Store) MarkAllRead(ghid string, days int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE biz_articles SET is_read = 1 WHERE is_read = 0`
	var args []any

	if ghid != "" {
		query += ` AND gh_id = ?`
		args = append(args, ghid)
	}
	if days > 0 {
		query += ` AND published_at >= ?`
		args = append(args, time.Now().AddDate(0, 0, -days).Unix())
	}

	res, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetBookmarkedArticles 获取已收藏文章列表（按发布时间倒序）
func (s *Store) GetBookmarkedArticles(limit, offset int) ([]Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.Query(`
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked, a.is_read,
		       COALESCE(e.status, ''), COALESCE(e.pushed_status, ''),
		       COALESCE(a.pipeline_status, ''), COALESCE(a.pipeline_error, ''),
		       COALESCE(a.pipeline_attempt, 0), COALESCE(a.pipeline_updated_at, 0)
		FROM biz_articles a
		LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE a.bookmarked = 1
		ORDER BY a.published_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var a Article
		var bookmarked, isRead int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked, &isRead, &a.ExportStatus, &a.PushStatus, &a.PipelineStatus, &a.PipelineError, &a.PipelineAttempt, &a.PipelineUpdatedAt); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
		a.IsRead = isRead != 0
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// GetLastSyncTime 获取某个公众号的最后同步时间
func (s *Store) GetLastSyncTime(ghid string) (int64, error) {
	var t int64
	err := s.db.QueryRow(`SELECT updated_at FROM biz_accounts WHERE gh_id = ?`, ghid).Scan(&t)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return t, nil
}

// GetArticleCount 获取文章总数
func (s *Store) GetArticleCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM biz_articles`).Scan(&n)
	return n, err
}

// CountArticles 统计某个公众号的文章总数（供分页返回真实 total）
func (s *Store) CountArticles(ghid string) (int, error) {
	return s.CountListArticles(ArticleFilter{GHID: ghid})
}

// CountSearchArticles 统计搜索结果总数。
// WHERE 条件必须与 SearchArticles 保持一致，否则 total 与列表不匹配。
func (s *Store) CountSearchArticles(keyword string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var n int
	err := s.db.QueryRow(`
		SELECT COUNT(1)
		FROM biz_articles a
		WHERE a.title LIKE ?
		AND a.gh_id NOT IN (SELECT gh_id FROM biz_accounts WHERE hidden = 1)
	`, "%"+keyword+"%").Scan(&n)
	return n, err
}

// CountFeedArticles 统计关注动态的文章总数。
// WHERE 条件必须与 GetFeedArticles 保持一致，否则 total 与列表不匹配。
func (s *Store) CountFeedArticles(days int) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if days <= 0 {
		days = 7
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()

	var n int
	err := s.db.QueryRow(`
		SELECT COUNT(1)
		FROM biz_articles a
		JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE acc.hidden = 0 AND acc.watched = 1 AND a.published_at >= ?
	`, since).Scan(&n)
	return n, err
}

// CountBookmarkedArticles 统计已收藏文章总数（供分页返回真实 total）
func (s *Store) CountBookmarkedArticles() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM biz_articles WHERE bookmarked = 1`).Scan(&n)
	return n, err
}

// CreateSummary 保存一条汇总
func (s *Store) CreateSummary(days, articleCount int, content string, structured json.RawMessage, instruction, scope, model string, tokensIn, tokensOut, fetchedCount int) (*Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	structuredStr := ""
	if len(structured) > 0 {
		structuredStr = string(structured)
	}
	result, err := s.db.Exec(`INSERT INTO biz_summaries
		(days, article_count, content, instruction, scope, structured, fetched_count, tokens_in, tokens_out, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		days, articleCount, content, instruction, scope, structuredStr, fetchedCount, tokensIn, tokensOut, model, now)
	if err != nil {
		return nil, err
	}
	id, _ := result.LastInsertId()
	sum := &Summary{
		ID:           id,
		Days:         days,
		ArticleCount: articleCount,
		Content:      content,
		Instruction:  instruction,
		Scope:        scope,
		Structured:   structured,
		FetchedCount: fetchedCount,
		TokensIn:     tokensIn,
		TokensOut:    tokensOut,
		Model:        model,
		CreatedAt:    now,
	}
	return sum, nil
}

// ListSummaries 获取汇总列表（不含 content/structured）
func (s *Store) ListSummaries() ([]Summary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, days, article_count, instruction, scope, fetched_count, tokens_in, tokens_out, model, created_at FROM biz_summaries ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []Summary
	for rows.Next() {
		var sum Summary
		if err := rows.Scan(&sum.ID, &sum.Days, &sum.ArticleCount, &sum.Instruction, &sum.Scope, &sum.FetchedCount, &sum.TokensIn, &sum.TokensOut, &sum.Model, &sum.CreatedAt); err != nil {
			return nil, err
		}
		summaries = append(summaries, sum)
	}
	return summaries, rows.Err()
}

// GetSummary 获取单条汇总（含 content 与 structured）
func (s *Store) GetSummary(id int64) (*Summary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sum Summary
	var structured string
	err := s.db.QueryRow(`SELECT id, days, article_count, content, instruction, scope, structured, fetched_count, tokens_in, tokens_out, model, created_at FROM biz_summaries WHERE id = ?`, id).
		Scan(&sum.ID, &sum.Days, &sum.ArticleCount, &sum.Content, &sum.Instruction, &sum.Scope, &structured, &sum.FetchedCount, &sum.TokensIn, &sum.TokensOut, &sum.Model, &sum.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if structured != "" {
		sum.Structured = json.RawMessage(structured)
	}
	return &sum, nil
}

// UpsertFeedSummary 写入或刷新指定 window_days 的 feed summary（先删除旧行再插入）
func (s *Store) UpsertFeedSummary(windowDays int, headline, structured string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM biz_feed_summaries WHERE window_days = ?`, windowDays); err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT INTO biz_feed_summaries (window_days, headline, structured, computed_at) VALUES (?, ?, ?, ?)`,
		windowDays, headline, structured, time.Now().Unix()); err != nil {
		return err
	}

	return tx.Commit()
}

// GetLatestFeedSummary 取该 window_days 最新一行；不存在时 exists=false
func (s *Store) GetLatestFeedSummary(windowDays int) (headline, structured string, computedAt int64, exists bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT headline, structured, computed_at FROM biz_feed_summaries WHERE window_days = ? ORDER BY computed_at DESC, id DESC LIMIT 1`, windowDays)
	if err := row.Scan(&headline, &structured, &computedAt); err != nil {
		if err == sql.ErrNoRows {
			return "", "", 0, false, nil
		}
		return "", "", 0, false, err
	}
	return headline, structured, computedAt, true, nil
}

// --- 每日精选 ---

// DailyDigest 每日精选结果
type DailyDigest struct {
	ID         int64        `json:"id"`
	DigestDate string       `json:"digestDate"`
	Headline   string       `json:"headline"`
	Picks      []DigestPick `json:"picks"`
	ArticleIDs []int64      `json:"articleIds"`
	ComputedAt int64        `json:"computedAt"`
}

// DigestPick 单篇精选
type DigestPick struct {
	ArticleID int64  `json:"articleId"`
	Title     string `json:"title"`
	GHName    string `json:"ghName"`
	URL       string `json:"url"`
	Reason    string `json:"reason"`
	Score     int    `json:"score"`
}

// UpsertDailyDigest 写入或更新某天的每日精选
func (s *Store) UpsertDailyDigest(digestDate, headline string, picks []DigestPick, articleIDs []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	picksJSON, _ := json.Marshal(picks)
	idsJSON, _ := json.Marshal(articleIDs)

	_, err := s.db.Exec(`INSERT INTO biz_daily_digests (digest_date, headline, picks, article_ids, computed_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(digest_date) DO UPDATE SET headline=excluded.headline, picks=excluded.picks, article_ids=excluded.article_ids, computed_at=excluded.computed_at`,
		digestDate, headline, string(picksJSON), string(idsJSON), time.Now().Unix())
	return err
}

// GetDailyDigest 取某天的精选；不存在时返回 nil, nil
func (s *Store) GetDailyDigest(digestDate string) (*DailyDigest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT id, digest_date, headline, picks, article_ids, computed_at FROM biz_daily_digests WHERE digest_date = ?`, digestDate)
	var d DailyDigest
	var picksStr, idsStr string
	if err := row.Scan(&d.ID, &d.DigestDate, &d.Headline, &picksStr, &idsStr, &d.ComputedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	json.Unmarshal([]byte(picksStr), &d.Picks)
	json.Unmarshal([]byte(idsStr), &d.ArticleIDs)
	if d.Picks == nil {
		d.Picks = []DigestPick{}
	}
	if d.ArticleIDs == nil {
		d.ArticleIDs = []int64{}
	}
	return &d, nil
}

// GetLatestDailyDigest 取最近一天的精选
func (s *Store) GetLatestDailyDigest() (*DailyDigest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT id, digest_date, headline, picks, article_ids, computed_at FROM biz_daily_digests ORDER BY digest_date DESC LIMIT 1`)
	var d DailyDigest
	var picksStr, idsStr string
	if err := row.Scan(&d.ID, &d.DigestDate, &d.Headline, &picksStr, &idsStr, &d.ComputedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	json.Unmarshal([]byte(picksStr), &d.Picks)
	json.Unmarshal([]byte(idsStr), &d.ArticleIDs)
	if d.Picks == nil {
		d.Picks = []DigestPick{}
	}
	if d.ArticleIDs == nil {
		d.ArticleIDs = []int64{}
	}
	return &d, nil
}

// --- 板块看板 ---

// GetSectorDashboard 按标签聚合 watched 账号近期文章热度。
// 只返回有文章的标签，按文章数降序。每个标签附带最多 5 篇最新文章。
func (s *Store) GetSectorDashboard(days int) ([]SectorSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	since := time.Now().AddDate(0, 0, -days).Unix()

	// 1. 取每个标签下的文章数和 tag 信息
	rows, err := s.db.Query(`
		SELECT t.id, t.name, t.color, COUNT(a.id) as cnt
		FROM biz_tags t
		JOIN biz_account_tags at ON t.id = at.tag_id
		JOIN biz_accounts acc ON at.gh_id = acc.gh_id
		JOIN biz_articles a ON a.gh_id = acc.gh_id
		WHERE acc.watched = 1 AND acc.hidden = 0 AND a.published_at >= ?
		GROUP BY t.id
		ORDER BY cnt DESC
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sectors []SectorSummary
	for rows.Next() {
		var sec SectorSummary
		if err := rows.Scan(&sec.TagID, &sec.TagName, &sec.Color, &sec.ArticleCount); err != nil {
			return nil, err
		}
		sectors = append(sectors, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 2. 每个标签取最新 5 篇文章
	for i := range sectors {
		artRows, err := s.db.Query(`
			SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked, a.is_read,
		       COALESCE(e.status, ''), COALESCE(e.pushed_status, '')
			FROM biz_articles a
			JOIN biz_accounts acc ON a.gh_id = acc.gh_id
			LEFT JOIN biz_exported_articles e ON a.id = e.article_id
			JOIN biz_account_tags at ON acc.gh_id = at.gh_id
			WHERE at.tag_id = ? AND acc.watched = 1 AND acc.hidden = 0 AND a.published_at >= ?
			ORDER BY a.published_at DESC
			LIMIT 5
		`, sectors[i].TagID, since)
		if err != nil {
			continue
		}
		articles, err := scanArticles(artRows)
		artRows.Close()
		if err != nil {
			continue
		}
		sectors[i].TopArticles = articles
	}

	if sectors == nil {
		sectors = []SectorSummary{}
	}
	return sectors, nil
}

// --- 文章时间线 ---

// SearchTimeline 按关键词搜索 watched 账号文章（标题 + 描述匹配），按时间排序
func (s *Store) SearchTimeline(keyword string, days, limit int) ([]Article, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	since := time.Now().AddDate(0, 0, -days).Unix()
	pattern := "%" + keyword + "%"

	// 总数
	var total int
	err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM biz_articles a
		JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE acc.watched = 1 AND acc.hidden = 0
		  AND a.published_at >= ?
		  AND (a.title LIKE ? OR a.description LIKE ?)
	`, since, pattern, pattern).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// 文章列表
	rows, err := s.db.Query(`
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked, a.is_read,
		       COALESCE(e.status, ''), COALESCE(e.pushed_status, '')
		FROM biz_articles a
		JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE acc.watched = 1 AND acc.hidden = 0
		  AND a.published_at >= ?
		  AND (a.title LIKE ? OR a.description LIKE ?)
		ORDER BY a.published_at DESC
		LIMIT ?
	`, since, pattern, pattern, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	articles, err := scanArticles(rows)
	if err != nil {
		return nil, 0, err
	}
	return articles, total, nil
}

// --- MD 导出 ---
func (s *Store) UpsertContent(urlHash, url, title, content string, status int, errStr string, bytes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`INSERT INTO biz_article_contents (url_hash, url, title, content, status, error, fetched_at, bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(url_hash) DO UPDATE SET
			url=excluded.url,
			title=excluded.title,
			content=excluded.content,
			status=excluded.status,
			error=excluded.error,
			fetched_at=excluded.fetched_at,
			bytes=excluded.bytes`,
		urlHash, url, title, content, status, errStr, time.Now().Unix(), bytes)
	return err
}

// ContentEntry 单个 hash 对应的抓取结果
type ContentEntry struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	Status    int    `json:"status"`
	FetchedAt int64  `json:"fetchedAt"`
	Error     string `json:"error"`
}

// GetContent 按 urlHash 获取单条抓取结果
func (s *Store) GetContent(urlHash string) (title, content string, status int, fetchedAt int64, errStr string, exists bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT title, content, status, fetched_at, error FROM biz_article_contents WHERE url_hash = ?`, urlHash)
	err = row.Scan(&title, &content, &status, &fetchedAt, &errStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", 0, 0, "", false, nil
		}
		return "", "", 0, 0, "", false, err
	}
	return title, content, status, fetchedAt, errStr, true, nil
}

// GetContentByHashes 批量按 urlHash 拉取抓取结果（一次查询）
func (s *Store) GetContentByHashes(hashes []string) (map[string]ContentEntry, error) {
	result := make(map[string]ContentEntry)
	if len(hashes) == 0 {
		return result, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	placeholders := make([]string, len(hashes))
	args := make([]any, len(hashes))
	for i, h := range hashes {
		placeholders[i] = "?"
		args[i] = h
	}
	query := `SELECT url_hash, title, content, status, fetched_at, error FROM biz_article_contents WHERE url_hash IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		var e ContentEntry
		if err := rows.Scan(&hash, &e.Title, &e.Content, &e.Status, &e.FetchedAt, &e.Error); err != nil {
			return nil, err
		}
		result[hash] = e
	}
	return result, rows.Err()
}

// CreateTag 创建标签
func (s *Store) CreateTag(name, color string) (*Tag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if color == "" {
		color = "#3b82f6"
	}
	now := time.Now().Unix()
	result, err := s.db.Exec(`INSERT INTO biz_tags (name, color, created_at) VALUES (?, ?, ?)`, name, color, now)
	if err != nil {
		return nil, err
	}
	id, _ := result.LastInsertId()
	return &Tag{ID: id, Name: name, Color: color, CreatedAt: now}, nil
}

// GetTags 获取所有标签
func (s *Store) GetTags() ([]Tag, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, name, color, created_at FROM biz_tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.CreatedAt); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// UpdateTag 更新标签名称和颜色
func (s *Store) UpdateTag(id int64, name, color string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE biz_tags SET name = ?, color = ? WHERE id = ?`, name, color, id)
	return err
}

// DeleteTag 删除标签
func (s *Store) DeleteTag(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM biz_tags WHERE id = ?`, id)
	return err
}

// TagAssignMode 决定打标签时如何处理公众号已有标签。
type TagAssignMode string

const (
	// TagModeAdd 追加：保留已有标签，只增加指定的标签。
	TagModeAdd TagAssignMode = "add"
	// TagModeRemove 移除：只删除指定的标签，保留其余标签。
	TagModeRemove TagAssignMode = "remove"
	// TagModeReplace 替换：清空已有标签后写入指定的标签。
	TagModeReplace TagAssignMode = "replace"
)

// ParseTagAssignMode 解析外部传入的模式字符串，空值按 add 处理。
func ParseTagAssignMode(s string) (TagAssignMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(TagModeAdd):
		return TagModeAdd, nil
	case string(TagModeRemove):
		return TagModeRemove, nil
	case string(TagModeReplace):
		return TagModeReplace, nil
	default:
		return "", fmt.Errorf("bizhub: invalid tag assign mode %q (want add/remove/replace)", s)
	}
}

// SetAccountTags 覆盖式设置单个公众号的标签（等价于 replace 模式）。
// 保留该签名的原因是已有调用方期望替换语义；新增调用请使用 SetAccountTagsMode。
func (s *Store) SetAccountTags(ghid string, tagIDs []int64) error {
	return s.SetAccountsTagsMode([]string{ghid}, tagIDs, TagModeReplace)
}

// SetAccountTagsMode 按指定模式设置单个公众号的标签。
func (s *Store) SetAccountTagsMode(ghid string, tagIDs []int64, mode TagAssignMode) error {
	return s.SetAccountsTagsMode([]string{ghid}, tagIDs, mode)
}

// SetAccountsTagsMode 按指定模式批量设置多个公众号的标签。
// 所有公众号在同一个事务内完成，避免部分成功导致的状态不一致。
func (s *Store) SetAccountsTagsMode(ghids []string, tagIDs []int64, mode TagAssignMode) error {
	if len(ghids) == 0 {
		return nil
	}
	switch mode {
	case TagModeAdd, TagModeRemove, TagModeReplace:
	default:
		return fmt.Errorf("bizhub: invalid tag assign mode %q", mode)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	delOne, err := tx.Prepare(`DELETE FROM biz_account_tags WHERE gh_id = ? AND tag_id = ?`)
	if err != nil {
		return err
	}
	defer delOne.Close()

	delAll, err := tx.Prepare(`DELETE FROM biz_account_tags WHERE gh_id = ?`)
	if err != nil {
		return err
	}
	defer delAll.Close()

	// biz_account_tags 的主键是 (gh_id, tag_id)，add 模式下重复写入需忽略而非报错
	ins, err := tx.Prepare(`INSERT OR IGNORE INTO biz_account_tags (gh_id, tag_id) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()

	for _, ghid := range ghids {
		switch mode {
		case TagModeReplace:
			if _, err := delAll.Exec(ghid); err != nil {
				return err
			}
			for _, tagID := range tagIDs {
				if _, err := ins.Exec(ghid, tagID); err != nil {
					return err
				}
			}
		case TagModeRemove:
			for _, tagID := range tagIDs {
				if _, err := delOne.Exec(ghid, tagID); err != nil {
					return err
				}
			}
		case TagModeAdd:
			for _, tagID := range tagIDs {
				if _, err := ins.Exec(ghid, tagID); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

// GetAccountTags 获取公众号的标签
func (s *Store) GetAccountTags(ghid string) ([]Tag, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT t.id, t.name, t.color, t.created_at
		FROM biz_tags t
		INNER JOIN biz_account_tags at ON t.id = at.tag_id
		WHERE at.gh_id = ?
		ORDER BY t.name
	`, ghid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.CreatedAt); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// GetAllAccountTagsMap 批量获取所有公众号的标签（一次查询，按 ghid 分组）
func (s *Store) GetAllAccountTagsMap() (map[string][]Tag, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT at.gh_id, t.id, t.name, t.color, t.created_at
		FROM biz_account_tags at
		INNER JOIN biz_tags t ON t.id = at.tag_id
		ORDER BY t.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]Tag)
	for rows.Next() {
		var ghid string
		var t Tag
		if err := rows.Scan(&ghid, &t.ID, &t.Name, &t.Color, &t.CreatedAt); err != nil {
			return nil, err
		}
		result[ghid] = append(result[ghid], t)
	}
	return result, rows.Err()
}

// GetAccountsByTag 根据标签获取公众号
func (s *Store) GetAccountsByTag(tagID int64) ([]Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT a.gh_id, a.gh_name, a.updated_at, a.watched, COUNT(b.id) as article_count
		FROM biz_accounts a
		INNER JOIN biz_account_tags at ON a.gh_id = at.gh_id
		LEFT JOIN biz_articles b ON a.gh_id = b.gh_id
		WHERE at.tag_id = ? AND a.hidden = 0
		GROUP BY a.gh_id
		ORDER BY article_count DESC
	`, tagID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		var acc Account
		var watched int
		if err := rows.Scan(&acc.GHID, &acc.GHName, &acc.UpdatedAt, &watched, &acc.ArticleCount); err != nil {
			return nil, err
		}
		acc.Watched = watched != 0
		accounts = append(accounts, acc)
	}
	return accounts, rows.Err()
}

// InitDefaultTags 初始化默认标签
func (s *Store) InitDefaultTags() error {
	defaultTags := []struct {
		Name  string
		Color string
	}{
		{"股市投资", "#ef4444"},
		{"产业行业", "#3b82f6"},
		{"交通出行", "#0ea5e9"},
		{"消费品牌", "#f59e0b"},
		{"新疆本地", "#84cc16"},
		{"工具SaaS", "#a855f7"},
		{"自媒体", "#ec4899"},
		{"影视内容", "#d946ef"},
		{"公用事业", "#14b8a6"},
		{"旅行票务", "#f97316"},
		{"文化阅读", "#6366f1"},
		{"城市服务", "#0284c7"},
		{"新闻", "#f97316"},
		{"技术", "#06b6d4"},
		{"金融", "#10b981"},
		{"政府", "#8b5cf6"},
		{"生活", "#f59e0b"},
		{"娱乐", "#ec4899"},
		{"教育", "#84cc16"},
		{"其他", "#6b7280"},
	}

	for _, t := range defaultTags {
		_, err := s.CreateTag(t.Name, t.Color)
		if err != nil && !strings.Contains(err.Error(), "UNIQUE") {
			return err
		}
	}
	return nil
}

// AutoTagAccounts 根据公众号名称自动打标签
func (s *Store) AutoTagAccounts() error {
	// 获取所有标签
	tags, err := s.GetTags()
	if err != nil {
		return err
	}

	tagMap := make(map[string]int64)
	for _, t := range tags {
		tagMap[t.Name] = t.ID
	}

	// 获取所有公众号
	accounts, err := s.GetAccounts()
	if err != nil {
		return err
	}

	// 关键词映射
	//
	// 匹配顺序影响结果：越具体的越靠前。"股市投资"和"产业行业"放在最前面，
	// 因为它们比"金融"更精准 —— 否则"广发证券"会被"金融"先吃掉。
	rules := map[string][]string{
		"股市投资":   {"ETF", "策市", "看市", "解盘", "后势", "研报", "大户室", "盘前", "指数投资", "价值投资", "期货投研", "金融工程", "策略研究", "券商中国", "证券报", "基金报", "银河策略", "证券", "中金", "华泰睿思", "华尔街见闻", "财经世界"},
		"产业行业":   {"产业", "行业观察", "产业链", "半导体", "投研笔记", "科技评论", "光电前瞻", "光互连", "金属加工", "新能源", "非金属矿", "算力", "信创", "TMT", "远川科技"},
		"交通出行":   {"航空", "机场", "铁路", "12306", "速运", "快递", "闪送", "滴滴", "出行", "公交", "公路客运", "快运"},
		"消费品牌":   {"京东", "肯德基", "瑞幸", "MUJI", "无印良品", "迪卡侬", "名创优品", "汉堡王", "茅台", "影城", "影院", "信用卡"},
		"新疆本地":   {"新疆", "乌鲁木齐", "库尔勒", "疆内", "巴州", "天山行"},
		"工具SaaS": {"Apifox", "ProcessOn", "墨刀", "CSDN", "51CTO", "牛客网", "PMO", "项目管理", "易企秀", "讯飞智文", "脚本之家"},
		"自媒体":    {"自修", "小菜", "课代表", "狮兄", "戴老板", "土狗"},
		"影视内容":   {"美剧", "大片", "DOTA", "崩坏", "HIPHOP"},
		"公用事业":   {"移动", "电信", "联通", "供水", "药房", "大药房", "疾控", "码上检"},
		"旅行票务":   {"旅行", "票务", "同程", "飞常准"},
		"文化阅读":   {"博物馆", "美术馆", "Kindle", "阅读室"},
		"城市服务":   {"本地宝", "城市通卡", "普法", "保密观", "公积金", "人社", "招生"},
		"新闻":     {"日报", "晚报", "晨报", "新闻", "时报", "周刊", "杂志", "观察", "记者", "爆料"},
		"技术":     {"程序", "代码", "开发", "技术", "架构", "算法", "AI", "互联网", "科技", "软件", "开源", "GitHub", "Java", "Python", "前端", "后端"},
		"金融":     {"金融", "银行", "证券", "基金", "保险", "投资", "理财", "股票", "期货", "信托", "支付", "财经", "经济"},
		"政府":     {"政府", "国家", "局", "委", "厅", "公安", "税务", "社保", "政务", "发布", "官方"},
		"生活":     {"生活", "服务", "便民", "医疗", "健康", "出行", "交通", "快递", "购物", "餐饮", "旅游"},
		"娱乐":     {"娱乐", "影视", "音乐", "游戏", "动漫", "明星", "综艺", "电影", "电视"},
		"教育":     {"教育", "学校", "大学", "学院", "培训", "考试", "学习", "知识"},
	}

	for _, acc := range accounts {
		// 跳过已有标签的账号：手动分过的不再被自动分类覆盖
		if existing, err := s.GetAccountTags(acc.GHID); err == nil && len(existing) > 0 {
			continue
		}

		name := strings.ToLower(acc.GHName)
		var tagIDs []int64

		for tagName, keywords := range rules {
			for _, keyword := range keywords {
				if strings.Contains(name, strings.ToLower(keyword)) {
					if tagID, ok := tagMap[tagName]; ok {
						tagIDs = append(tagIDs, tagID)
						break
					}
				}
			}
		}

		// 如果没有匹配到任何标签，归为"其他"
		if len(tagIDs) == 0 {
			if tagID, ok := tagMap["其他"]; ok {
				tagIDs = append(tagIDs, tagID)
			}
		}

		// 设置标签
		if len(tagIDs) > 0 {
			if err := s.SetAccountTags(acc.GHID, tagIDs); err != nil {
				// 继续处理下一个
				continue
			}
		}
	}

	return nil
}

// --- biz_exported_articles CRUD ---

// UpsertExportRecord 插入或更新导出记录（按 article_id 去重）。
//
// status 语义见 ExportStatus* 常量。kind 为失败分类（成功时传空串）。
// attempts 的维护规则（这是「失败可重试」的核心，改之前先想清楚）：
//
//	新记录 + exported           -> 0
//	新记录 + failed             -> 1
//	已有记录 + failed           -> 旧值 + 1（计入这次失败）
//	已有记录 + exported         -> 归 0（成功后不再累计，避免下次偶发失败
//	                               直接撞上重试上限而被判定为永久失败）
//	已有记录 + summary_generated -> 不变（补摘要不算一次归档尝试）
func (s *Store) UpsertExportRecord(articleID int64, sourceURL, mdPath, summaryPath, status, kind, errStr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	summaryAt := int64(0)
	if status == ExportStatusSummarized {
		summaryAt = now
	}
	// exported_at 只在真正导出成功时推进。失败不写 —— 否则管理页上
	// 「已归档时间」会被一次失败刷成今天，掩盖真实的归档时间。
	exportedAt := int64(0)
	if status == ExportStatusExported || status == ExportStatusSummarized {
		exportedAt = now
	}
	attemptSeed := 0
	if status == ExportStatusFailed {
		attemptSeed = 1
	}

	_, err := s.db.Exec(`INSERT INTO biz_exported_articles 
		(article_id, source_url, md_path, summary_path, status, error, kind, attempts, exported_at, summary_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(article_id) DO UPDATE SET
			source_url=excluded.source_url,
			-- 失败时不要把已有路径抹成空串：归档成功、补摘要失败是很常见的
			-- 一条路径，抹掉 md_path 会让「已归档」的文章在管理页看起来像没归档。
			md_path=CASE WHEN excluded.md_path != '' THEN excluded.md_path ELSE biz_exported_articles.md_path END,
			summary_path=CASE WHEN excluded.summary_path != '' THEN excluded.summary_path ELSE biz_exported_articles.summary_path END,
			status=excluded.status,
			error=excluded.error,
			kind=excluded.kind,
			attempts=CASE
				WHEN excluded.status = 'failed' THEN biz_exported_articles.attempts + 1
				WHEN excluded.status = 'exported' THEN 0
				ELSE biz_exported_articles.attempts
			END,
			exported_at=CASE WHEN excluded.exported_at > 0 THEN excluded.exported_at ELSE biz_exported_articles.exported_at END,
			summary_at=CASE 
				WHEN excluded.summary_at > 0 THEN excluded.summary_at
				ELSE biz_exported_articles.summary_at
		END`,
		articleID, sourceURL, mdPath, summaryPath, status, errStr, kind, attemptSeed, exportedAt, summaryAt)
	return err
}

// PushStatus 推送状态常量（与 ExportStatus* 镜像，独立使用）。
//
// 状态机：
//
//	(空) --成功--> pushed
//	(空) --失败--> push_failed  --再次批量推送--> pushed
//
// push_failed 不是终态。下次批量推送会把 push_failed 重新纳入候选（除非
// attempts 用尽或失败类型是「需人工处理」）。
const (
	PushStatusPushed = "pushed"
	PushStatusFailed = "push_failed"
)

// maxPushAttempts 推送自动重试上限。
//
// 与 MD 归档同款：可重试失败（超时 / 限流 / script_failed）如果一直失败，
// 无限重试只会每轮都在同一篇上浪费时间且加重 IMA 端限流。
const maxPushAttempts = 3

// UpsertPushRecord 插入或更新推送记录（按 article_id 去重）。
//
// 只动 pushed_* 字段；md_path / status / attempts 一律保留 —— 链式约束下
// push 失败绝不能把"已归档"的标记弄丢。
//
// attempts 的维护规则（与 UpsertExportRecord 对仗）：
//
//	新记录 + pushed         -> 0
//	新记录 + push_failed    -> 1
//	已有记录 + push_failed   -> 旧值 + 1
//	已有记录 + pushed        -> 归 0
func (s *Store) UpsertPushRecord(articleID int64, mediaID, status, kind, errStr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	pushedAt := int64(0)
	if status == PushStatusPushed {
		pushedAt = now
	}
	attemptSeed := 0
	if status == PushStatusFailed {
		attemptSeed = 1
	}

	_, err := s.db.Exec(`INSERT INTO biz_exported_articles
		(article_id, source_url, md_path, summary_path, status, error, kind, attempts,
		 exported_at, summary_at,
		 pushed_at, pushed_media_id, pushed_status, push_attempts, push_kind)
		VALUES (?, '', '', '', 'exported', '', '', 0, 0, 0, ?, ?, ?, ?, ?)
		ON CONFLICT(article_id) DO UPDATE SET
			-- media_id：失败时不要把已有 media_id 抹成空串 —— 重推成功的关键线索
			pushed_media_id=CASE WHEN excluded.pushed_media_id != '' THEN excluded.pushed_media_id ELSE biz_exported_articles.pushed_media_id END,
			pushed_status=excluded.pushed_status,
			push_kind=excluded.push_kind,
			push_attempts=CASE
				WHEN excluded.pushed_status = 'push_failed' THEN biz_exported_articles.push_attempts + 1
				WHEN excluded.pushed_status = 'pushed' THEN 0
				ELSE biz_exported_articles.push_attempts
			END,
			pushed_at=CASE WHEN excluded.pushed_at > 0 THEN excluded.pushed_at ELSE biz_exported_articles.pushed_at END`,
		articleID, pushedAt, mediaID, status, attemptSeed, kind, errStr)
	return err
}

// IsArticleExported 判定文章是否已经 MD 归档成功（链式 push 的前置条件）。
//
// status IN ('exported', 'summary_generated') 表示归档成功；status='failed'
// 或没有记录都视为未归档。单篇 push handler 与 buildPushPlan 都依赖此判定，
// 必须共用同一个语义。
func (s *Store) IsArticleExported(articleID int64) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var status string
	err := s.db.QueryRow(`SELECT status FROM biz_exported_articles WHERE article_id = ?`, articleID).Scan(&status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == "exported" || status == "summary_generated", nil
}

// GetExportRecord 按 article_id 获取导出记录
func (s *Store) GetExportRecord(articleID int64) (*ExportedArticle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var e ExportedArticle
	err := s.db.QueryRow(`SELECT id, article_id, source_url, md_path, summary_path, status, error, kind, attempts, exported_at, summary_at,
		       pushed_status, pushed_media_id, pushed_at, push_attempts, push_kind
		FROM biz_exported_articles WHERE article_id = ?`, articleID).
		Scan(&e.ID, &e.ArticleID, &e.SourceURL, &e.MDPath, &e.SummaryPath, &e.Status, &e.Error, &e.Kind, &e.Attempts, &e.ExportedAt, &e.SummaryAt, &e.PushedStatus, &e.PushedMediaID, &e.PushedAt, &e.PushAttempts, &e.PushKind)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// GetExportRecordByURL 按 source_url 获取导出记录
func (s *Store) GetExportRecordByURL(url string) (*ExportedArticle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var e ExportedArticle
	err := s.db.QueryRow(`SELECT id, article_id, source_url, md_path, summary_path, status, error, kind, attempts, exported_at, summary_at,
		       pushed_status, pushed_media_id, pushed_at, push_attempts, push_kind
		FROM biz_exported_articles WHERE source_url = ?`, url).
		Scan(&e.ID, &e.ArticleID, &e.SourceURL, &e.MDPath, &e.SummaryPath, &e.Status, &e.Error, &e.Kind, &e.Attempts, &e.ExportedAt, &e.SummaryAt, &e.PushedStatus, &e.PushedMediaID, &e.PushedAt, &e.PushAttempts, &e.PushKind)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// GetExportRecordsByArticleIDs 批量获取导出记录
func (s *Store) GetExportRecordsByArticleIDs(ids []int64) (map[int64]*ExportedArticle, error) {
	if len(ids) == 0 {
		return make(map[int64]*ExportedArticle), nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `SELECT id, article_id, source_url, md_path, summary_path, status, error, kind, attempts, exported_at, summary_at,
		       pushed_status, pushed_media_id, pushed_at, push_attempts, push_kind
		FROM biz_exported_articles WHERE article_id IN (` + strings.Join(placeholders, ",") + `)`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]*ExportedArticle)
	for rows.Next() {
		var e ExportedArticle
		if err := rows.Scan(&e.ID, &e.ArticleID, &e.SourceURL, &e.MDPath, &e.SummaryPath, &e.Status, &e.Error, &e.Kind, &e.Attempts, &e.ExportedAt, &e.SummaryAt, &e.PushedStatus, &e.PushedMediaID, &e.PushedAt, &e.PushAttempts, &e.PushKind); err != nil {
			return nil, err
		}
		result[e.ArticleID] = &e
	}
	return result, rows.Err()
}

// ExportCandidateWhere 批量归档的候选条件（SQL 片段）。
//
// 这里是「失败会不会被永久放弃」的唯一判定点，务必与 GetExportStats 共用，
// 否则会出现「页面说待归档 5 篇、点下去只处理 3 篇」的静默偏差。
//
// 条件：published_at 在窗口内、账号未被隐藏、且**没有成功记录**。
//
// 注意第二项不是 `e.id IS NULL`。历史实现用 `e.id IS NULL`，而导出失败也会写一条
// status='failed' 的记录 —— 于是一篇失败过的文章从此再也不出现在候选集里，
// 也不会出现在 pending 计数里，用户永远不知道该去重试它。这就是静默数据丢失：
// 数字对得上，文件就是少。改成「没有成功记录」后，失败会进入下一轮自动重试；
// 重试上限与「需人工处理」的判定放在 Go 层（见 Service.buildExportPlan），
// 因为它依赖 mdexport 的失败分类，塞进 SQL 会把分类逻辑复制两遍。
const ExportCandidateWhere = `a.published_at >= ? 
		  AND (e.id IS NULL OR e.status = 'failed')
		  AND (e.status IS NULL OR e.status NOT IN ('exported', 'summary_generated'))
		  AND a.gh_id NOT IN (SELECT gh_id FROM biz_accounts WHERE hidden = 1)`

// GetUnexportedArticles 获取最近 days 天内仍需要归档的文章（用于批量导出/迁移）
func (s *Store) GetUnexportedArticles(days, limit int, ghids ...string) ([]Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if days <= 0 {
		days = 30
	}
	if limit <= 0 {
		limit = 100
	}

	since := time.Now().AddDate(0, 0, -days).Unix()
	where := ExportCandidateWhere
	args := []any{since}
	if len(ghids) > 0 {
		placeholders := make([]string, len(ghids))
		for i, g := range ghids {
			placeholders[i] = "?"
			args = append(args, g)
		}
		where += " AND a.gh_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	args = append(args, limit)

	rows, err := s.db.Query(`
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked, a.is_read,
		       COALESCE(e.status, ''), COALESCE(e.pushed_status, '')
		FROM biz_articles a
		LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE `+where+`
		ORDER BY a.published_at DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var a Article
		var bookmarked, isRead int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked, &isRead, &a.ExportStatus, &a.PushStatus); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
		a.IsRead = isRead != 0
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// ExportStats 归档统计（管理页顶部的「已归档 / 待归档 / 需人工处理」）
type ExportStats struct {
	Exported int `json:"exported"` // 已有成功记录
	Pending  int `json:"pending"`  // 本轮批量归档会纳入候选的篇数
	Blocked  int `json:"blocked"`  // 候选里会被判定为「需人工处理」而跳过的篇数
	Days     int `json:"days"`     // 统计窗口（天）
}

// GetExportStats 按给定时间窗统计归档情况。
//
// days <= 0 时回退 30 天。窗口必须由调用方传入实际选择的范围 —— 之前这里硬编码
// 30 天，而页面下拉框可选 90/180，于是「近 180 天待归档」显示的数字永远是按
// 30 天算的，选了更大的范围数字反而不变，看起来像功能没生效。
func (s *Store) GetExportStats(days int) (*ExportStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if days <= 0 {
		days = 30
	}
	stats := &ExportStats{Days: days}

	if err := s.db.QueryRow(`SELECT COUNT(1) FROM biz_exported_articles WHERE status != 'failed'`).Scan(&stats.Exported); err != nil {
		return nil, err
	}

	since := time.Now().AddDate(0, 0, -days).Unix()
	if err := s.db.QueryRow(`
		SELECT COUNT(1) FROM biz_articles a
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE `+ExportCandidateWhere, since).Scan(&stats.Pending); err != nil {
		return nil, err
	}

	// blocked：候选里那些「重试已用尽」或「失败类型需人工处理」的篇数。
	// 必须从候选集里取交集，否则用户看到 blocked=5、pending=0 会以为点一下就能跑完。
	kinds := mdexport.PermanentKinds()
	args := []any{since, maxExportAttempts}
	placeholders := make([]string, len(kinds))
	for i, k := range kinds {
		placeholders[i] = "?"
		args = append(args, k)
	}
	blockedQuery := `
		SELECT COUNT(1) FROM biz_articles a
		JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE a.published_at >= ?
		  AND e.status = 'failed'
		  AND (e.attempts >= ? OR e.kind IN (` + strings.Join(placeholders, ",") + `))
		  AND a.gh_id NOT IN (SELECT gh_id FROM biz_accounts WHERE hidden = 1)`
	if err := s.db.QueryRow(blockedQuery, args...).Scan(&stats.Blocked); err != nil {
		return nil, err
	}

	return stats, nil
}

// --- biz_export_jobs / biz_export_job_items CRUD ---

// CreateExportJob 建任务并批量写入条目（同一事务，避免出现「有任务没条目」的半截状态）
func (s *Store) CreateExportJob(job *ExportJob, items []ExportJobItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	if job.CreatedAt == 0 {
		job.CreatedAt = now
	}
	job.UpdatedAt = now

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO biz_export_jobs
		(id, status, days, max_items, concurrency, degraded, total, created_at, updated_at, finished_at, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Status, job.Days, job.MaxItems, job.Concurrency, boolToInt(job.Degraded),
		job.Total, job.CreatedAt, job.UpdatedAt, job.FinishedAt, job.Error); err != nil {
		return err
	}

	if len(items) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO biz_export_job_items
			(job_id, article_id, title, account, status, kind, error, md_path, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, it := range items {
			if _, err := stmt.Exec(job.ID, it.ArticleID, it.Title, it.Account,
				it.Status, it.Kind, it.Error, it.MDPath, now); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// UpdateExportJobItem 更新单条目状态
func (s *Store) UpdateExportJobItem(it ExportJobItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE biz_export_job_items
		SET status=?, kind=?, error=?, md_path=?, updated_at=?
		WHERE job_id=? AND article_id=?`,
		it.Status, it.Kind, it.Error, it.MDPath, time.Now().Unix(), it.JobID, it.ArticleID)
	return err
}

// GetExportJob 读任务，并填充按条目聚合的实时计数
func (s *Store) GetExportJob(id string) (*ExportJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	job, err := scanExportJob(s.db.QueryRow(`SELECT id, status, days, max_items, concurrency, degraded,
		total, created_at, updated_at, finished_at, error FROM biz_export_jobs WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, nil
	}
	if err := fillExportJobCounts(s.db, job); err != nil {
		return nil, err
	}
	return job, nil
}

// ListExportJobs 读最近的任务（按创建时间倒序），供管理页恢复进度显示
func (s *Store) ListExportJobs(limit int) ([]*ExportJob, error) {
	if limit <= 0 {
		limit = 10
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, status, days, max_items, concurrency, degraded,
		total, created_at, updated_at, finished_at, error
		FROM biz_export_jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*ExportJob
	for rows.Next() {
		job, err := scanExportJob(rows)
		if err != nil {
			return nil, err
		}
		if job == nil {
			continue
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if err := fillExportJobCounts(s.db, job); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

// ListExportJobItems 读任务条目。statuses 为空则返回全部。
func (s *Store) ListExportJobItems(jobID string, statuses ...string) ([]ExportJobItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT job_id, article_id, title, account, status, kind, error, md_path, updated_at
		FROM biz_export_job_items WHERE job_id = ?`
	args := []any{jobID}
	if len(statuses) > 0 {
		placeholders := make([]string, len(statuses))
		for i, st := range statuses {
			placeholders[i] = "?"
			args = append(args, st)
		}
		query += ` AND status IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY updated_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ExportJobItem
	for rows.Next() {
		var it ExportJobItem
		if err := rows.Scan(&it.JobID, &it.ArticleID, &it.Title, &it.Account, &it.Status,
			&it.Kind, &it.Error, &it.MDPath, &it.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// SetExportJobStatus 更新任务状态；finish 为 true 时写入完成时间。
func (s *Store) SetExportJobStatus(id, status, errMsg string, finish bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	finishedAt := int64(0)
	if finish {
		finishedAt = now
	}
	_, err := s.db.Exec(`UPDATE biz_export_jobs
		SET status=?, error=?, updated_at=?, finished_at=CASE WHEN ? > 0 THEN ? ELSE finished_at END
		WHERE id=?`, status, errMsg, now, finishedAt, finishedAt, id)
	return err
}

// SetExportJobConcurrency 记录降级后的并发数（风控降级时调用）
func (s *Store) SetExportJobConcurrency(id string, concurrency int, degraded bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE biz_export_jobs
		SET concurrency=?, degraded=?, updated_at=? WHERE id=?`,
		concurrency, boolToInt(degraded), time.Now().Unix(), id)
	return err
}

// RequeueExportJobUnfinished 把任务里所有「还没成功」的条目改回 pending，返回受影响条数。
//
// 语义是「重试 = 把这一任务里没成功过的全部重新跑一遍」，所以条件写成
// status != 'done' 而不是 status = 'failed'。理由：手动重试的前提是用户
// 已经处理了失败原因（装了抓取器、过了验证码、改好了目录权限），此时
// 之前被判定为「需人工处理」而跳过的条目同样应该重新入队 ——
// 否则用户点完重试看到进度条不动的第一反应是「重试按钮坏了」。
func (s *Store) RequeueExportJobUnfinished(jobID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`UPDATE biz_export_job_items
		SET status=?, kind='', error='', updated_at=?
		WHERE job_id=? AND status != ?`,
		ExportItemPending, time.Now().Unix(), jobID, ExportItemDone)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// MarkInterruptedExportJobs 启动时的修复：把上次进程残留的 running 任务标为
// interrupted，并把 running 条目退回 pending。返回被中断的任务 ID 列表。
//
// 为什么必须做这件事：任务化之后进程被杀（Ctrl-C、崩溃、容器重启）会留下
// status='running' 的僵尸任务，前端一进管理页就轮到它、永远转不完；
// 条目退回 pending 后重跑时会被重新捡起，这就是「断点续跑」的全部实现 ——
// 不需要额外的检查点机制，因为条目状态本身就是检查点。
func (s *Store) MarkInterruptedExportJobs() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT id FROM biz_export_jobs WHERE status = ? ORDER BY created_at DESC`, ExportJobRunning)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE biz_export_jobs
		SET status=?, error=?, updated_at=? WHERE status = ?`,
		ExportJobInterrupted, "服务重启导致中断，可续跑", now, ExportJobRunning); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE biz_export_job_items
		SET status=?, updated_at=? WHERE status = ?`,
		ExportItemPending, now, ExportItemRunning); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

// exportJobScanner 让 QueryRow 与 Rows 共用同一段扫描逻辑
type exportJobScanner interface {
	Scan(dest ...any) error
}

func scanExportJob(sc exportJobScanner) (*ExportJob, error) {
	var job ExportJob
	var degraded int
	err := sc.Scan(&job.ID, &job.Status, &job.Days, &job.MaxItems, &job.Concurrency,
		&degraded, &job.Total, &job.CreatedAt, &job.UpdatedAt, &job.FinishedAt, &job.Error)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	job.Degraded = degraded != 0
	return &job, nil
}

func fillExportJobCounts(db *sql.DB, job *ExportJob) error {
	return db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0)
		FROM biz_export_job_items WHERE job_id = ?`,
		ExportItemDone, ExportItemFailed, ExportItemSkipped, ExportItemRunning, ExportItemPending,
		job.ID).Scan(&job.Succeeded, &job.Failed, &job.Skipped, &job.Running, &job.Pending)
}

// --- biz_push_jobs / biz_push_job_items CRUD ---
//
// 与 biz_export_jobs 镜像结构，但有两个关键差异：
//   1. 链式候选 SQL 必须先经过 export 成功筛选（这是 plan 里定的硬约束）
//   2. 条目表多了 media_id 字段，没有 md_path（推送不写本地）

// PushJob 批量推送任务
type PushJob struct {
	ID          string `json:"id"`
	Status      string `json:"status"` // running / done / failed / canceled / interrupted
	Days        int    `json:"days"`
	MaxItems    int    `json:"maxItems"`
	Concurrency int    `json:"concurrency"`
	Degraded    bool   `json:"degraded"`
	Total       int    `json:"total"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	FinishedAt  int64  `json:"finishedAt"`
	Error       string `json:"error"`

	// 以下为按 item 聚合的实时计数（读取时填充，不落库）
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	Running   int `json:"running"`
	Pending   int `json:"pending"`
}

// Done 已完成（与 ExportJob.Done 对仗）
func (j *PushJob) Done() bool {
	return j.Status != ExportJobRunning
}

// Processed 已产生终态的条目数
func (j *PushJob) Processed() int {
	return j.Succeeded + j.Failed + j.Skipped
}

// Percent 进度百分比（0-100）
func (j *PushJob) Percent() int {
	if j.Total <= 0 {
		return 100
	}
	p := j.Processed() * 100 / j.Total
	if p > 100 {
		p = 100
	}
	return p
}

// PushJobItem 批量推送任务中的单篇条目
type PushJobItem struct {
	JobID     string `json:"jobID"`
	ArticleID int64  `json:"articleID"`
	Title     string `json:"title"`
	Account   string `json:"account"`
	Status    string `json:"status"` // pending / running / done / failed / skipped
	Kind      string `json:"kind"`
	Error     string `json:"error"`
	MediaID   string `json:"mediaID"`
	UpdatedAt int64  `json:"updatedAt"`
}

// PushCandidateWhere 批量推送的候选条件（SQL 片段）。
//
// 链式约束：必须先 export 成功；未 export 失败的不能 push。
// 与 ExportCandidateWhere 对仗 —— 这里把所有判断集中在一个常量里，
// 让页面 pending 计数与实际任务选取范围共用同一份口径。
//
// 复用 imapush.PermanentKinds 在 SQL 层没法直接拼（Go 层判断失败分类），
// 这里只做"是否失败过"的最粗过滤；具体的"是否需人工处理"判断
// 留给 buildPushPlan 在 Go 层做。
const PushCandidateWhere = `a.published_at >= ?
		  AND a.gh_id NOT IN (SELECT gh_id FROM biz_accounts WHERE hidden = 1)
		  AND e.id IS NOT NULL
		  AND e.status IN ('exported','summary_generated')
		  AND (e.pushed_status = '' OR e.pushed_status = 'push_failed')`

// GetPushableArticles 获取链式可推送的文章（用于批量推送/迁移）
func (s *Store) GetPushableArticles(days, limit int) ([]Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if days <= 0 {
		days = 30
	}
	if limit <= 0 {
		limit = 100
	}

	since := time.Now().AddDate(0, 0, -days).Unix()
	rows, err := s.db.Query(`
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked, a.is_read,
		       COALESCE(e.status, ''), COALESCE(e.pushed_status, '')
		FROM biz_articles a
		LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE `+PushCandidateWhere+`
		ORDER BY a.published_at DESC
		LIMIT ?
	`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var a Article
		var bookmarked, isRead int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked, &isRead, &a.ExportStatus, &a.PushStatus); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
		a.IsRead = isRead != 0
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// PushStats 推送统计（管理页顶部）
type PushStats struct {
	Pushed  int `json:"pushed"`
	Pending int `json:"pending"`
	Blocked int `json:"blocked"`
	Days    int `json:"days"`
}

// GetPushStats 按给定时间窗统计推送情况
func (s *Store) GetPushStats(days int) (*PushStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if days <= 0 {
		days = 30
	}
	stats := &PushStats{Days: days}

	// pushed：所有已成功推送的文章数
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM biz_exported_articles WHERE pushed_status = 'pushed'`).Scan(&stats.Pushed); err != nil {
		return nil, err
	}

	// pending：候选集中、未推送、且非永久失败的篇数
	since := time.Now().AddDate(0, 0, -days).Unix()
	if err := s.db.QueryRow(`
		SELECT COUNT(1) FROM biz_articles a
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE `+PushCandidateWhere, since).Scan(&stats.Pending); err != nil {
		return nil, err
	}

	// blocked：候选集中、已失败、且永久失败或 push_attempts 用尽的篇数
	kinds := imapush.PermanentKinds()
	args := []any{since, maxPushAttempts}
	placeholders := make([]string, len(kinds))
	for i, k := range kinds {
		placeholders[i] = "?"
		args = append(args, k)
	}
	blockedQuery := `
		SELECT COUNT(1) FROM biz_articles a
		JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE a.published_at >= ?
		  AND e.status IN ('exported','summary_generated')
		  AND e.pushed_status = 'push_failed'
		  AND (e.push_attempts >= ? OR e.push_kind IN (` + strings.Join(placeholders, ",") + `))
		  AND a.gh_id NOT IN (SELECT gh_id FROM biz_accounts WHERE hidden = 1)`
	if err := s.db.QueryRow(blockedQuery, args...).Scan(&stats.Blocked); err != nil {
		return nil, err
	}

	return stats, nil
}

// CreatePushJob 建任务并批量写入条目
func (s *Store) CreatePushJob(job *PushJob, items []PushJobItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	if job.CreatedAt == 0 {
		job.CreatedAt = now
	}
	job.UpdatedAt = now

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO biz_push_jobs
		(id, status, days, max_items, concurrency, degraded, total, created_at, updated_at, finished_at, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Status, job.Days, job.MaxItems, job.Concurrency, boolToInt(job.Degraded),
		job.Total, job.CreatedAt, job.UpdatedAt, job.FinishedAt, job.Error); err != nil {
		return err
	}

	if len(items) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO biz_push_job_items
			(job_id, article_id, title, account, status, kind, error, media_id, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, it := range items {
			if _, err := stmt.Exec(job.ID, it.ArticleID, it.Title, it.Account,
				it.Status, it.Kind, it.Error, it.MediaID, now); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// UpdatePushJobItem 更新单条目状态
func (s *Store) UpdatePushJobItem(it PushJobItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE biz_push_job_items
		SET status=?, kind=?, error=?, media_id=?, updated_at=?
		WHERE job_id=? AND article_id=?`,
		it.Status, it.Kind, it.Error, it.MediaID, time.Now().Unix(), it.JobID, it.ArticleID)
	return err
}

// GetPushJob 读任务，并填充按条目聚合的实时计数
func (s *Store) GetPushJob(id string) (*PushJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	job, err := scanPushJob(s.db.QueryRow(`SELECT id, status, days, max_items, concurrency, degraded,
		total, created_at, updated_at, finished_at, error FROM biz_push_jobs WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, nil
	}
	if err := fillPushJobCounts(s.db, job); err != nil {
		return nil, err
	}
	return job, nil
}

// ListPushJobs 读最近的任务
func (s *Store) ListPushJobs(limit int) ([]*PushJob, error) {
	if limit <= 0 {
		limit = 10
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, status, days, max_items, concurrency, degraded,
		total, created_at, updated_at, finished_at, error
		FROM biz_push_jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*PushJob
	for rows.Next() {
		job, err := scanPushJob(rows)
		if err != nil {
			return nil, err
		}
		if job == nil {
			continue
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if err := fillPushJobCounts(s.db, job); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

// ListPushJobItems 读任务条目
func (s *Store) ListPushJobItems(jobID string, statuses ...string) ([]PushJobItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT job_id, article_id, title, account, status, kind, error, media_id, updated_at
		FROM biz_push_job_items WHERE job_id = ?`
	args := []any{jobID}
	if len(statuses) > 0 {
		placeholders := make([]string, len(statuses))
		for i, st := range statuses {
			placeholders[i] = "?"
			args = append(args, st)
		}
		query += ` AND status IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY updated_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PushJobItem
	for rows.Next() {
		var it PushJobItem
		if err := rows.Scan(&it.JobID, &it.ArticleID, &it.Title, &it.Account, &it.Status,
			&it.Kind, &it.Error, &it.MediaID, &it.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// SetPushJobStatus 更新任务状态
func (s *Store) SetPushJobStatus(id, status, errMsg string, finish bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	finishedAt := int64(0)
	if finish {
		finishedAt = now
	}
	_, err := s.db.Exec(`UPDATE biz_push_jobs
		SET status=?, error=?, updated_at=?, finished_at=CASE WHEN ? > 0 THEN ? ELSE finished_at END
		WHERE id=?`, status, errMsg, now, finishedAt, finishedAt, id)
	return err
}

// SetPushJobConcurrency 记录降级后的并发数
func (s *Store) SetPushJobConcurrency(id string, concurrency int, degraded bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE biz_push_jobs
		SET concurrency=?, degraded=?, updated_at=? WHERE id=?`,
		concurrency, boolToInt(degraded), time.Now().Unix(), id)
	return err
}

// RequeuePushJobUnfinished 把任务里所有"还没成功"的条目改回 pending
func (s *Store) RequeuePushJobUnfinished(jobID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`UPDATE biz_push_job_items
		SET status=?, kind='', error='', media_id='', updated_at=?
		WHERE job_id=? AND status != ?`,
		ExportItemPending, time.Now().Unix(), jobID, ExportItemDone)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// MarkInterruptedPushJobs 启动时修复：把上次进程残留的 running 任务标为 interrupted
func (s *Store) MarkInterruptedPushJobs() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT id FROM biz_push_jobs WHERE status = ? ORDER BY created_at DESC`, ExportJobRunning)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE biz_push_jobs
		SET status=?, error=?, updated_at=? WHERE status = ?`,
		ExportJobInterrupted, "服务重启导致中断，可续跑", now, ExportJobRunning); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE biz_push_job_items
		SET status=?, updated_at=? WHERE status = ?`,
		ExportItemPending, now, ExportItemRunning); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

func scanPushJob(sc exportJobScanner) (*PushJob, error) {
	var job PushJob
	var degraded int
	err := sc.Scan(&job.ID, &job.Status, &job.Days, &job.MaxItems, &job.Concurrency,
		&degraded, &job.Total, &job.CreatedAt, &job.UpdatedAt, &job.FinishedAt, &job.Error)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	job.Degraded = degraded != 0
	return &job, nil
}

func fillPushJobCounts(db *sql.DB, job *PushJob) error {
	return db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0)
		FROM biz_push_job_items WHERE job_id = ?`,
		ExportItemDone, ExportItemFailed, ExportItemSkipped, ExportItemRunning, ExportItemPending,
		job.ID).Scan(&job.Succeeded, &job.Failed, &job.Skipped, &job.Running, &job.Pending)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// copyFile 复制文件（用于数据库备份）
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// 同步到磁盘
	return destFile.Sync()
}

// GetExportRecordsWithoutSummary 获取已导出但未生成摘要的记录（用于批量生成摘要）
func (s *Store) GetExportRecordsWithoutSummary(limit int) ([]ExportedArticle, error) {
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, article_id, source_url, md_path, summary_path, status, error, kind, attempts, exported_at, summary_at
		FROM biz_exported_articles
		WHERE status = 'exported' AND md_path != '' AND summary_path = ''
		ORDER BY exported_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []ExportedArticle
	for rows.Next() {
		var e ExportedArticle
		if err := rows.Scan(&e.ID, &e.ArticleID, &e.SourceURL, &e.MDPath, &e.SummaryPath, &e.Status, &e.Error, &e.Kind, &e.Attempts, &e.ExportedAt, &e.SummaryAt, &e.PushedStatus, &e.PushedMediaID, &e.PushedAt, &e.PushAttempts, &e.PushKind); err != nil {
			return nil, err
		}
		records = append(records, e)
	}
	return records, rows.Err()
}

// --- Pipeline 状态机（PR1） ---
//
// 设计要点：
//   - status 用单列字符串，5 个正态 + 4 个 failed:<stage>，状态机驱动见 pipeline.go
//   - Worker 按 (status, updated_at ASC) 拉候选，避免重扫全表
//   - failed:<stage> 不在 ticker 中自动重试（反爬保护），只能走 /admin/pipeline/retry/:id
//   - pipeline_error / pipeline_attempt / pipeline_updated_at 由 Worker 维护，
//     不允许外部手动改 —— 改了 Worker 也不会信任

// pipelinePickableStatuses Worker 可拉取的候选状态集合。
//
// 用 IN (...) 而非 LIKE 'failed:%'：状态集合是封闭的，列出来更安全；
// 未来加新状态时这里要同步更新（编译期字符串常量的好处）。
var pipelinePickableStatuses = []string{
	PipelinePending,
	PipelineFailedFetch,
	PipelineFailedMdexport,
	PipelineFailedSummarize,
	PipelineFailedImapush,
}

// PickPipelineBatch 拉取一批待处理的文章（pending + failed:*）。
//
// 按 pipeline_updated_at ASC 排序：先处理最久没动过的，避免某篇反复失败阻塞新文。
// 返回的 Article 已包含 pipeline_* 字段（被 Article 结构的 JSON tag 暴露给前端）。
func (s *Store) PickPipelineBatch(ctx context.Context, limit int) ([]Article, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// 拼 IN (?,?,?,?,?)
	placeholders := make([]string, len(pipelinePickableStatuses))
	args := make([]any, len(pipelinePickableStatuses)+1)
	for i, s := range pipelinePickableStatuses {
		placeholders[i] = "?"
		args[i] = s
	}
	args[len(pipelinePickableStatuses)] = limit

	q := articleRows + `
	WHERE a.pipeline_status IN (` + strings.Join(placeholders, ",") + `)
	ORDER BY a.pipeline_updated_at ASC
	LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArticles(rows)
}

// AdvancePipeline 把单篇文章推进到 nextStatus，清错误，updated_at=now，attempt++。
//
// 设计要点：attempt 计数包含所有阶段累计尝试次数（含失败重试）。
// 一个 article 经历 fetch→mdexport→summarize→imapush 全部成功 = attempt = 4。
// 想要"每阶段单独计数"可以看 stage_logs（未来 PR 加），单 attempt 已经够 ops 看健康度。
func (s *Store) AdvancePipeline(ctx context.Context, articleID int64, nextStatus string) error {
	if nextStatus == "" {
		return errors.New("bizhub: AdvancePipeline: empty nextStatus")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		UPDATE biz_articles
		SET pipeline_status = ?,
		    pipeline_error = '',
		    pipeline_attempt = pipeline_attempt + 1,
		    pipeline_updated_at = ?
		WHERE id = ?`,
		nextStatus, now, articleID)
	return err
}

// MarkPipelineFailed 把文章标为 failed:<stage>，记录错误，updated_at=now，attempt++。
//
// stage 必须是 "fetch" / "mdexport" / "summarize" / "imapush" 之一；
// 函数不强制校验，调错 stage 名会让状态变 "failed:bogus" 这种垃圾值。
// 上游 pipeline.RunStage 已经做了 stage 名常量约束，store 不重复。
func (s *Store) MarkPipelineFailed(ctx context.Context, articleID int64, stage, errStr string) error {
	if stage == "" {
		return errors.New("bizhub: MarkPipelineFailed: empty stage")
	}
	if len(errStr) > 512 {
		errStr = errStr[:512]
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		UPDATE biz_articles
		SET pipeline_status = ?,
		    pipeline_error = ?,
		    pipeline_attempt = pipeline_attempt + 1,
		    pipeline_updated_at = ?
		WHERE id = ?`,
		"failed:"+stage, errStr, now, articleID)
	return err
}

// ResetPipelineToPending 把 failed:<stage> 倒回 pending，让 ticker 再次拉取。
//
// 仅由 /admin/pipeline/retry/:id 触发（手动作业）。
// 设计要点：只把 failed 倒回 pending；如果当前是其他正态，状态不动
// （避免不小心重置正在跑的中间状态）。
func (s *Store) ResetPipelineToPending(ctx context.Context, articleID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
		UPDATE biz_articles
		SET pipeline_status = 'pending',
		    pipeline_error = '',
		    pipeline_updated_at = ?
		WHERE id = ? AND pipeline_status LIKE 'failed:%'`,
		now, articleID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("bizhub: article not in failed state (only failed:* can be retried)")
	}
	return nil
}

// PipelineStatusCount 按 pipeline_status 分组计数。
//
// 返回 map[status]count，未出现的状态不在 map 里（不是 0）。
// 用来给 /admin/pipeline/status 端点画漏斗图。
func (s *Store) PipelineStatusCount(ctx context.Context) (map[string]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT pipeline_status, COUNT(1)
		FROM biz_articles
		WHERE pipeline_status != ''
		GROUP BY pipeline_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// RecentFailedArticles 取最近 N 条失败的文章（任意 failed:*）。
//
// 按 pipeline_updated_at DESC 排，给 /admin/pipeline/status 显示「最近发生了什么」。
func (s *Store) RecentFailedArticles(ctx context.Context, limit int) ([]Article, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	q := articleRows + `
	WHERE a.pipeline_status LIKE 'failed:%'
	ORDER BY a.pipeline_updated_at DESC
	LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArticles(rows)
}

// PipelineStateChanged 文章 pipeline 字段被任意渠道更新后通知订阅方（占位）。
//
// 当前没有任何订阅者；保留接口为了让 PR1 之后想做"实时面板"SSE/WebSocket
// 时不用改 store 接口。Worker 是当前唯一的 writer，但 admin UI 后续可能
// 也要写（比如一键 mark all pushed），到时候订阅会让那个写入路径做正确性校验。
//
// 在 Go 里这种"预留接口"通常用 struct{}{} / chan struct{} 实现，
// 但更标准的做法是直接写注释说明意图、不留空接口。这里选择后者。
// 真正要落地时加一行：`type PipelineObserver interface { OnChange(articleID int64) }`
