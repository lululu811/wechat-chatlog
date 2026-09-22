package bizhub

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"

	"github.com/sjzar/chatlog/internal/model"
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

// ExportedArticle MD 导出记录（biz_exported_articles 表）
type ExportedArticle struct {
	ID          int64  `json:"id"`
	ArticleID   int64  `json:"articleID"`
	SourceURL   string `json:"sourceURL"`
	MDPath      string `json:"mdPath"`
	SummaryPath string `json:"summaryPath"`
	Status      string `json:"status"` // exported / summary_generated / failed
	Error       string `json:"error"`
	ExportedAt  int64  `json:"exportedAt"`
	SummaryAt   int64  `json:"summaryAt"`
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
    exported_at  INTEGER NOT NULL DEFAULT 0,
    summary_at   INTEGER NOT NULL DEFAULT 0,
    UNIQUE(article_id)
);

CREATE INDEX IF NOT EXISTS idx_exported_articles_source_url ON biz_exported_articles(source_url);
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
	GHID   string
	Days   int
	Limit  int
	Offset int
}

const articleRows = `
	SELECT a.id, a.gh_id, COALESCE(acc.gh_name, ''), a.title, a.description, a.url,
	       a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked
	FROM biz_articles a
	LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id`

// articleWhere 生成 WHERE 子句与参数。列表查询与计数必须共用它，
// 否则 total 与实际返回条数会不一致（这类不一致只在翻页到头时才暴露）。
func articleWhere(f ArticleFilter) (string, []any) {
	var conds []string
	var args []any
	if f.GHID != "" {
		conds = append(conds, "a.gh_id = ?")
		args = append(args, f.GHID)
	}
	if f.Days > 0 {
		conds = append(conds, "a.published_at >= ?")
		args = append(args, time.Now().Add(-time.Duration(f.Days)*24*time.Hour).Unix())
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
		var bookmarked int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
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
	var bookmarked int
	err := s.db.QueryRow(`
		SELECT a.id, a.gh_id, COALESCE(acc.gh_name, ''), a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked
		FROM biz_articles a
		LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		WHERE a.id = ?
	`, id).Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	a.Bookmarked = bookmarked != 0
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
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked
		FROM biz_articles a
		LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id
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
		var bookmarked int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
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
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked
		FROM biz_articles a
		JOIN biz_accounts acc ON a.gh_id = acc.gh_id
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
		var bookmarked int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
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
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked
		FROM biz_articles a
		JOIN biz_accounts acc ON a.gh_id = acc.gh_id
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
		var bookmarked int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
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
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked
		FROM biz_articles a
		LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id
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
		var bookmarked int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
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

// UpsertContent 写入或更新文章抓取结果（按 url_hash 主键）
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
		{"新闻", "#ef4444"},
		{"技术", "#3b82f6"},
		{"金融", "#10b981"},
		{"政府", "#8b5cf6"},
		{"生活", "#f59e0b"},
		{"娱乐", "#ec4899"},
		{"教育", "#06b6d4"},
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
	rules := map[string][]string{
		"新闻": {"日报", "晚报", "晨报", "新闻", "时报", "周刊", "杂志", "观察", "记者", "爆料"},
		"技术": {"程序", "代码", "开发", "技术", "架构", "算法", "AI", "互联网", "科技", "软件", "开源", "GitHub", "Java", "Python", "前端", "后端"},
		"金融": {"金融", "银行", "证券", "基金", "保险", "投资", "理财", "股票", "期货", "信托", "支付", "财经", "经济"},
		"政府": {"政府", "国家", "局", "委", "厅", "公安", "税务", "社保", "政务", "发布", "官方"},
		"生活": {"生活", "服务", "便民", "医疗", "健康", "出行", "交通", "快递", "购物", "餐饮", "旅游"},
		"娱乐": {"娱乐", "影视", "音乐", "游戏", "动漫", "明星", "综艺", "电影", "电视"},
		"教育": {"教育", "学校", "大学", "学院", "培训", "考试", "学习", "知识"},
	}

	for _, acc := range accounts {
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

// UpsertExportRecord 插入或更新导出记录（按 article_id 去重）
func (s *Store) UpsertExportRecord(articleID int64, sourceURL, mdPath, summaryPath, status, errStr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	summaryAt := int64(0)
	if status == "summary_generated" {
		summaryAt = now
	}

	_, err := s.db.Exec(`INSERT INTO biz_exported_articles 
		(article_id, source_url, md_path, summary_path, status, error, exported_at, summary_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(article_id) DO UPDATE SET
			source_url=excluded.source_url,
			md_path=excluded.md_path,
			summary_path=excluded.summary_path,
			status=excluded.status,
			error=excluded.error,
			exported_at=excluded.exported_at,
			summary_at=CASE 
				WHEN excluded.summary_at > 0 THEN excluded.summary_at
				ELSE biz_exported_articles.summary_at 
			END`,
		articleID, sourceURL, mdPath, summaryPath, status, errStr, now, summaryAt)
	return err
}

// GetExportRecord 按 article_id 获取导出记录
func (s *Store) GetExportRecord(articleID int64) (*ExportedArticle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var e ExportedArticle
	err := s.db.QueryRow(`SELECT id, article_id, source_url, md_path, summary_path, status, error, exported_at, summary_at 
		FROM biz_exported_articles WHERE article_id = ?`, articleID).
		Scan(&e.ID, &e.ArticleID, &e.SourceURL, &e.MDPath, &e.SummaryPath, &e.Status, &e.Error, &e.ExportedAt, &e.SummaryAt)
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
	err := s.db.QueryRow(`SELECT id, article_id, source_url, md_path, summary_path, status, error, exported_at, summary_at 
		FROM biz_exported_articles WHERE source_url = ?`, url).
		Scan(&e.ID, &e.ArticleID, &e.SourceURL, &e.MDPath, &e.SummaryPath, &e.Status, &e.Error, &e.ExportedAt, &e.SummaryAt)
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
	query := `SELECT id, article_id, source_url, md_path, summary_path, status, error, exported_at, summary_at 
		FROM biz_exported_articles WHERE article_id IN (` + strings.Join(placeholders, ",") + `)`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]*ExportedArticle)
	for rows.Next() {
		var e ExportedArticle
		if err := rows.Scan(&e.ID, &e.ArticleID, &e.SourceURL, &e.MDPath, &e.SummaryPath, &e.Status, &e.Error, &e.ExportedAt, &e.SummaryAt); err != nil {
			return nil, err
		}
		result[e.ArticleID] = &e
	}
	return result, rows.Err()
}

// GetUnexportedArticles 获取最近 days 天内未导出的文章（用于批量导出/迁移）
func (s *Store) GetUnexportedArticles(days, limit int) ([]Article, error) {
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
		SELECT a.id, a.gh_id, acc.gh_name, a.title, a.description, a.url, a.app_id, a.local_type, a.local_id, a.sort_seq, a.published_at, a.synced_at, a.bookmarked
		FROM biz_articles a
		LEFT JOIN biz_accounts acc ON a.gh_id = acc.gh_id
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE a.published_at >= ? 
		  AND e.id IS NULL
		  AND a.gh_id NOT IN (SELECT gh_id FROM biz_accounts WHERE hidden = 1)
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
		var bookmarked int
		if err := rows.Scan(&a.ID, &a.GHID, &a.GHName, &a.Title, &a.Desc, &a.URL, &a.AppID, &a.LocalType, &a.LocalID, &a.SortSeq, &a.PublishedAt, &a.SyncedAt, &bookmarked); err != nil {
			return nil, err
		}
		a.Bookmarked = bookmarked != 0
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// GetExportStats 获取导出统计
func (s *Store) GetExportStats() (exported, pending int, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	err = s.db.QueryRow(`SELECT COUNT(1) FROM biz_exported_articles WHERE status != 'failed'`).Scan(&exported)
	if err != nil {
		return
	}

	// pending = 近 30 天未导出的文章数。
	//
	// 过滤条件必须与 GetUnexportedArticles（批量导出真正取数的地方）逐条对齐，
	// 否则页面显示「待导出 5 篇」、点下去只处理 3 篇 —— 差的那两篇属于已隐藏账号，
	// 用户没有别的线索能看出来，只会当成导出漏了。
	since := time.Now().AddDate(0, 0, -30).Unix()
	err = s.db.QueryRow(`
		SELECT COUNT(1) FROM biz_articles a
		LEFT JOIN biz_exported_articles e ON a.id = e.article_id
		WHERE a.published_at >= ?
		  AND e.id IS NULL
		  AND a.gh_id NOT IN (SELECT gh_id FROM biz_accounts WHERE hidden = 1)
	`, since).Scan(&pending)
	return
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

	rows, err := s.db.Query(`SELECT id, article_id, source_url, md_path, summary_path, status, error, exported_at, summary_at 
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
		if err := rows.Scan(&e.ID, &e.ArticleID, &e.SourceURL, &e.MDPath, &e.SummaryPath, &e.Status, &e.Error, &e.ExportedAt, &e.SummaryAt); err != nil {
			return nil, err
		}
		records = append(records, e)
	}
	return records, rows.Err()
}
