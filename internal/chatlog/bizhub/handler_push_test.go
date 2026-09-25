package bizhub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// newPushTestEnv 建一个可测 IsPushConfigured / IsArticleExported / PushArticle 行为的最小 Service。
func newPushTestEnv(t *testing.T, kbID string, pushConfigured bool) *Service {
	t.Helper()
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// 插入一个测试文章（不通过 syncer，手工 INSERT）
	_, err = store.db.Exec(`INSERT INTO biz_accounts
		(gh_id, gh_name, updated_at)
		VALUES (?, ?, 0)`, "gh_test", "测试号")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	_, err = store.db.Exec(`INSERT INTO biz_articles
		(gh_id, title, description, url, app_id, local_type, local_id, sort_seq, published_at, synced_at, bookmarked, is_read)
		VALUES (?, ?, '', ?, '', 0, 0, 0, 0, 0, 0, 0)`, "gh_test", "测试文章", "https://mp.weixin.qq.com/s/test")
	if err != nil {
		t.Fatalf("seed article: %v", err)
	}

	cfg := &stubPushConfig{}
	if pushConfigured {
		// 创建真实的 imaskai skill_dir（含 ima_api.cjs 文件）—— IsPushConfigured
		// 会 stat 这个文件来判定。
		skillDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(skillDir, "ima_api.cjs"), []byte("// stub"), 0o755); err != nil {
			t.Fatalf("write fake imaskai: %v", err)
		}
		cfg.skillDir = skillDir
		cfg.kbID = kbID
	}

	svc := &Service{
		store:   store,
		workDir: root,
		config:  cfg,
	}
	svc.bgCtx, svc.bgCancel = context.WithCancel(context.Background())
	t.Cleanup(func() {
		if svc.bgCancel != nil {
			svc.bgCancel()
		}
	})

	return svc
}

// TestHandlePushArticle_RejectsUnexported 是链式校验的关键回归点：
//
//	未 export 的文章不能 push —— 单篇 handler 必须返 409，body 含明确提示。
func TestHandlePushArticle_RejectsUnexported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newPushTestEnv(t, "kb1", true)
	rec := getFirstArticleID(t, svc)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/biz/articles/"+itoa(rec)+"/push", nil)
	c.Params = gin.Params{{Key: "id", Value: itoa(rec)}}

	handlePushArticle(c, svc)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "未归档") {
		t.Errorf("body should explain why push failed, got: %s", w.Body.String())
	}
}

// TestHandlePushArticle_Unconfigured 当 IMA 未配置时返 400。
//
// 400 而不是 409 是为了让前端能区分"是我没配"还是"是文章不行"；
// 否则用户会卡在「为什么这篇不能推」的循环里。
func TestHandlePushArticle_Unconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newPushTestEnv(t, "", false) // IMA 未配置
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/biz/articles/1/push", nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	handlePushArticle(c, svc)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (unconfigured)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not configured") &&
		!strings.Contains(w.Body.String(), "未配置") {
		t.Errorf("body should mention configuration, got: %s", w.Body.String())
	}
}

// TestIsArticleExported 链式前置的真值表。
func TestIsArticleExported(t *testing.T) {
	svc := newPushTestEnv(t, "kb1", true)
	id := getFirstArticleID(t, svc)

	// 没有记录 → false
	got, err := svc.IsArticleExported(id)
	if err != nil {
		t.Fatalf("IsArticleExported: %v", err)
	}
	if got {
		t.Errorf("without record, IsArticleExported should be false")
	}

	// 写入 exported → true
	if err := svc.store.UpsertExportRecord(id, "https://mp.weixin.qq.com/s/test", "/tmp/x.md", "", "exported", "", ""); err != nil {
		t.Fatalf("UpsertExportRecord exported: %v", err)
	}
	got, _ = svc.IsArticleExported(id)
	if !got {
		t.Errorf("status=exported should be exported=true")
	}

	// summary_generated → true
	if err := svc.store.UpsertExportRecord(id, "https://mp.weixin.qq.com/s/test", "/tmp/x.md", "", "summary_generated", "", ""); err != nil {
		t.Fatalf("UpsertExportRecord summarized: %v", err)
	}
	got, _ = svc.IsArticleExported(id)
	if !got {
		t.Errorf("status=summary_generated should be exported=true")
	}

	// failed → false
	if err := svc.store.UpsertExportRecord(id, "https://mp.weixin.qq.com/s/test", "", "", "failed", "", ""); err != nil {
		t.Fatalf("UpsertExportRecord failed: %v", err)
	}
	got, _ = svc.IsArticleExported(id)
	if got {
		t.Errorf("status=failed should be exported=false")
	}
}

// TestIsPushConfigured_ReturnsBool 基础真值校验。
func TestIsPushConfigured_ReturnsBool(t *testing.T) {
	// unconfigured
	svc := newPushTestEnv(t, "", false)
	if svc.IsPushConfigured() {
		t.Error("with empty config, IsPushConfigured should be false")
	}

	// 但 stubExportConfig 的 IMA 方法都返回空 —— 所以即便 dir/script 配上，
	// IMA 仍未配置 —— 这是 stub 的预期行为。
}

// helpers ----------

// stubPushConfig 单测用的最小 Config 实现：能控 IMA / MD 字段。
type stubPushConfig struct {
	skillDir string
	kbID     string
	folderID string
	pushCC   int
	dir      string
	script   string
	exportCC int
}

func (c *stubPushConfig) GetSummaryFetchContent() bool    { return false }
func (c *stubPushConfig) GetSummaryFetchConcurrency() int { return 0 }
func (c *stubPushConfig) GetLLMMaxTokens() int            { return 0 }
func (c *stubPushConfig) GetFeedSummaryCacheHours() int   { return 0 }
func (c *stubPushConfig) GetMDExportDir() string          { return c.dir }
func (c *stubPushConfig) GetMDExportScript() string       { return c.script }
func (c *stubPushConfig) GetMDExportConcurrency() int     { return c.exportCC }
func (c *stubPushConfig) GetIMAPushSkillDir() string      { return c.skillDir }
func (c *stubPushConfig) GetIMAPushKBID() string          { return c.kbID }
func (c *stubPushConfig) GetIMAPushFolderID() string      { return c.folderID }
func (c *stubPushConfig) GetIMAPushConcurrency() int      { return c.pushCC }

// LLM（PR1 pipeline worker 需要）—— 测试 stub 不构造真实 LLM
func (c *stubPushConfig) GetLLMBaseURL() string { return "" }
func (c *stubPushConfig) GetLLMAPIKey() string  { return "" }
func (c *stubPushConfig) GetLLMModel() string   { return "" }

// Pipeline worker（PR1）—— 测试 stub 默认不启用
func (c *stubPushConfig) GetBizWorkerEnabled() bool   { return false }
func (c *stubPushConfig) GetBizWorkerInterval() int   { return 0 }
func (c *stubPushConfig) GetBizWorkerBatchSize() int  { return 0 }

func getFirstArticleID(t *testing.T, svc *Service) int64 {
	t.Helper()
	rec, err := svc.store.ListArticles(ArticleFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListArticles: %v", err)
	}
	if len(rec) == 0 {
		t.Fatal("no articles seeded")
	}
	return rec[0].ID
}

func itoa(i int64) string {
	// 避免直接 import strconv —— handler_test 已经有其它 import 了，
	// 这里用 fmt 风格手动转换更省事。
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
