package bizhub

import (
	"context"
	"database/sql"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lululu811/wechat-chatlog/internal/wechatdb"
)

// Syncer 负责从 wechatdb 同步数据到本地 store
type Syncer struct {
	wechatDB *wechatdb.DB
	store    *Store
	workDir  string
}

// NewSyncer 创建同步器
func NewSyncer(wechatDB *wechatdb.DB, store *Store, workDir string) *Syncer {
	return &Syncer{
		wechatDB: wechatDB,
		store:    store,
		workDir:  workDir,
	}
}

// SyncAll 全量同步所有公众号文章
func (s *Syncer) SyncAll() (*SyncResult, error) {
	if s.wechatDB == nil {
		return nil, ErrNoWechatDB
	}

	// 获取所有公众号联系人
	ghList, err := s.getGHIDs()
	if err != nil {
		return nil, err
	}

	result := &SyncResult{
		StartTime: time.Now(),
		Accounts:  make([]AccountSyncResult, 0, len(ghList)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 用于批量更新账号信息
	accounts := make([]Account, 0, len(ghList))

	for _, gh := range ghList {
		accResult := AccountSyncResult{
			GHID:   gh.ID,
			GHName: gh.Name,
		}

		// 获取该公众号的所有消息
		end := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		msgs, err := s.wechatDB.GetBizMessages(ctx, gh.ID, time.Time{}, end, 0)
		if err != nil {
			accResult.Error = err.Error()
			result.Accounts = append(result.Accounts, accResult)
			continue
		}

		// 设置 GHID 和 GHName
		for _, m := range msgs {
			if m != nil {
				m.GHID = gh.ID
				m.GHName = gh.Name
			}
		}

		// 写入 store
		count, err := s.store.UpsertArticles(msgs)
		if err != nil {
			accResult.Error = err.Error()
		} else {
			accResult.NewCount = count
			accResult.TotalCount = len(msgs)
		}

		// 记录账号信息
		accounts = append(accounts, Account{
			GHID:   gh.ID,
			GHName: gh.Name,
		})

		result.Accounts = append(result.Accounts, accResult)
		result.TotalArticles += accResult.TotalCount
		result.NewArticles += accResult.NewCount
	}

	// 批量更新账号信息
	if len(accounts) > 0 {
		if err := s.store.UpsertAccounts(accounts); err != nil {
			result.Error = err.Error()
		}
	}

	result.EndTime = time.Now()
	return result, nil
}

// SyncOne 同步单个公众号
func (s *Syncer) SyncOne(ghid string) (*AccountSyncResult, error) {
	if s.wechatDB == nil {
		return nil, ErrNoWechatDB
	}

	// 获取公众号名称
	ghName, err := s.getGHName(ghid)
	if err != nil {
		ghName = ghid
	}

	result := &AccountSyncResult{
		GHID:   ghid,
		GHName: ghName,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	end := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	msgs, err := s.wechatDB.GetBizMessages(ctx, ghid, time.Time{}, end, 0)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}

	// 设置 GHID 和 GHName
	for _, m := range msgs {
		if m != nil {
			m.GHID = ghid
			m.GHName = ghName
		}
	}

	count, err := s.store.UpsertArticles(msgs)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}

	result.NewCount = count
	result.TotalCount = len(msgs)

	// 更新账号信息
	if err := s.store.UpsertAccounts([]Account{{GHID: ghid, GHName: ghName}}); err != nil {
		result.Error = err.Error()
	}

	return result, nil
}

// SyncResult 同步结果
type SyncResult struct {
	StartTime     time.Time           `json:"startTime"`
	EndTime       time.Time           `json:"endTime"`
	TotalArticles int                 `json:"totalArticles"`
	NewArticles   int                 `json:"newArticles"`
	Accounts      []AccountSyncResult `json:"accounts"`
	Error         string              `json:"error,omitempty"`
}

// AccountSyncResult 单个公众号的同步结果
type AccountSyncResult struct {
	GHID       string `json:"ghID"`
	GHName     string `json:"ghName"`
	TotalCount int    `json:"totalCount"`
	NewCount   int    `json:"newCount"`
	Error      string `json:"error,omitempty"`
}

// ghIDEntry 公众号联系人
type ghIDEntry struct {
	ID   string
	Name string
}

// getGHIDs 从 contact.db 获取所有公众号
func (s *Syncer) getGHIDs() ([]ghIDEntry, error) {
	dbPath := filepath.Join(s.workDir, "db_storage", "contact", "contact.db")
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT username, COALESCE(nick_name, '') FROM contact WHERE username LIKE 'gh_%' AND delete_flag = 0 ORDER BY nick_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ghIDEntry
	for rows.Next() {
		var e ghIDEntry
		if err := rows.Scan(&e.ID, &e.Name); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// getGHName 获取单个公众号名称
func (s *Syncer) getGHName(ghid string) (string, error) {
	dbPath := filepath.Join(s.workDir, "db_storage", "contact", "contact.db")
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return "", err
	}
	defer db.Close()

	var name string
	err = db.QueryRow(`SELECT COALESCE(nick_name, '') FROM contact WHERE username = ?`, ghid).Scan(&name)
	if err != nil {
		return ghid, nil
	}
	return name, nil
}
