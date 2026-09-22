package bizhub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/sjzar/chatlog/internal/model"
)

// 本文件锁定接口层对外契约（设计说明 §6「命名与接口规约」）：
//
//	1. 接口重命名后，新路径与旧路径同时可用（旧路径内部转发，不 301 ——
//	   fetch 跟随重定向会丢 POST body 与 Authorization 头）；
//	2. 旧路径带废弃标记，便于判断何时可以摘掉；
//	3. 列表接口统一返回 {items, total, limit, offset}，不再各写各的字段名；
//	4. total 是真实 COUNT，不是当前页长度。

// legacyAPIPaths 是接口重命名的新旧对照。
//
// link 是 Link 头应指向的完整路径。带 :id 的路径要实例化成真实 URL
// （/api/v1/biz/reports/1），否则客户端照着 Link 请求会 404。
var legacyAPIPaths = []struct {
	method string
	old    string
	new    string
	link   string
}{
	{http.MethodPost, "/api/v1/biz/summary", "/api/v1/biz/reports", "/api/v1/biz/reports"},
	{http.MethodGet, "/api/v1/biz/summaries", "/api/v1/biz/reports", "/api/v1/biz/reports"},
	{http.MethodGet, "/api/v1/biz/summaries/1", "/api/v1/biz/reports/1", "/api/v1/biz/reports/1"},
	{http.MethodGet, "/api/v1/biz/feed/summary", "/api/v1/biz/feed/digest", "/api/v1/biz/feed/digest"},
	{http.MethodPost, "/api/v1/biz/summary/generate-all", "/api/v1/biz/admin/batch/summary", "/api/v1/biz/admin/batch/summary"},
	{http.MethodPost, "/api/v1/biz/accounts/visibility", "/api/v1/biz/admin/accounts/visibility", "/api/v1/biz/admin/accounts/visibility"},
	{http.MethodPost, "/api/v1/biz/accounts/watch", "/api/v1/biz/admin/accounts/watch", "/api/v1/biz/admin/accounts/watch"},
	{http.MethodPost, "/api/v1/biz/accounts/tags", "/api/v1/biz/admin/accounts/tags", "/api/v1/biz/admin/accounts/tags"},
	{http.MethodPost, "/api/v1/biz/export/batch", "/api/v1/biz/admin/export/batch", "/api/v1/biz/admin/export/batch"},
	{http.MethodGet, "/api/v1/biz/export/status", "/api/v1/biz/admin/export/status", "/api/v1/biz/admin/export/status"},
}

// listEndpoints 是全部返回列表信封的接口。limit/offset 为 0 表示该接口未分页。
var listEndpoints = []string{
	"/api/v1/biz/accounts",
	"/api/v1/biz/articles",
	"/api/v1/biz/bookmarks",
	"/api/v1/biz/tags",
	"/api/v1/biz/tags/1/accounts",
	"/api/v1/biz/accounts/gh_1/tags",
	"/api/v1/biz/admin/accounts",
	"/api/v1/biz/reports",
}

// legacyListKeys 是各接口以前各起一名的列表字段。统一信封后必须全部消失 ——
// 留着任意一个，前端就会重新长出「每个接口记住一个字段名」的耦合。
var legacyListKeys = []string{"articles", "summaries", "accounts", "tags"}

// newAPIRouter 建一个带真实 Service（临时目录上的 store）的路由器。
// 返回 Service 是为了让用例能直接往 store 里塞数据。
func newAPIRouter(t *testing.T) (*gin.Engine, *Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	svc, err := NewService(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Store().Close() })

	r := gin.New()
	RegisterRoutes(r.Group(""), func() *Service { return svc }, nil)
	return r, svc
}

func doJSON(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestLegacyAPIPathsBehaveIdentically 旧路径与新路径必须命中同一个 handler。
//
// 这是「新旧并行过渡」的核心约束：设计说明 §6 要求先让新旧同时可访问、
// 观察一段时间再摘旧路径。如果旧路径直接 404，已经发出去的客户端会一起坏掉。
func TestLegacyAPIPathsBehaveIdentically(t *testing.T) {
	r, _ := newAPIRouter(t)

	for _, tc := range legacyAPIPaths {
		old := doJSON(r, tc.method, tc.old)
		canonical := doJSON(r, tc.method, tc.new)

		if old.Code != canonical.Code {
			t.Errorf("%s %s = %d，但新路径 %s = %d；两者应命中同一个 handler",
				tc.method, tc.old, old.Code, tc.new, canonical.Code)
			continue
		}
		if old.Body.String() != canonical.Body.String() {
			t.Errorf("%s %s 与 %s 返回体不同：\n旧: %s\n新: %s",
				tc.method, tc.old, tc.new, old.Body.String(), canonical.Body.String())
		}
	}
}

// TestLegacyAPIPathIsMarkedDeprecated 旧路径要能被识别出来，新路径不能带这个标记。
//
// Deprecation 头的存在本身就是「别名已注册」的证据：别名包装先设头再调 handler，
// 而路由没注册时 gin 的 404 兜底不会经过这段代码。
func TestLegacyAPIPathIsMarkedDeprecated(t *testing.T) {
	r, _ := newAPIRouter(t)

	for _, tc := range legacyAPIPaths {
		old := doJSON(r, tc.method, tc.old)

		if got := old.Header().Get("Deprecation"); got != "true" {
			t.Errorf("%s %s 缺少 Deprecation 头（拿到 %q）—— 别名没有注册",
				tc.method, tc.old, got)
		}
		if got := old.Header().Get("Link"); !strings.Contains(got, "<"+tc.link+">") {
			t.Errorf("%s %s 的 Link 头 = %q，应包含 <%s>",
				tc.method, tc.old, got, tc.link)
		}

		canonical := doJSON(r, tc.method, tc.new)
		if got := canonical.Header().Get("Deprecation"); got != "" {
			t.Errorf("新路径 %s 不应带 Deprecation 头，拿到 %q", tc.new, got)
		}
	}
}

// TestListEndpointsUseUnifiedEnvelope 列表接口只允许一种返回形状。
func TestListEndpointsUseUnifiedEnvelope(t *testing.T) {
	r, _ := newAPIRouter(t)

	for _, path := range listEndpoints {
		w := doJSON(r, http.MethodGet, path)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, 期望 200（body=%s）", path, w.Code, w.Body.String())
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Errorf("GET %s 返回的不是 JSON 对象: %v", path, err)
			continue
		}

		for _, key := range []string{"items", "total", "limit", "offset"} {
			if _, ok := raw[key]; !ok {
				t.Errorf("GET %s 缺少信封字段 %q（实际有 %v）", path, key, keysOf(raw))
			}
		}
		for _, key := range legacyListKeys {
			if _, ok := raw[key]; ok {
				t.Errorf("GET %s 仍在用旧的列表字段名 %q；统一信封后应只返回 items", path, key)
			}
		}

		// 空列表必须是 []，不能是 null —— 否则前端每个调用点都要写一次 `|| []`。
		if string(raw["items"]) == "null" {
			t.Errorf("GET %s 的 items 是 null，期望 []", path)
		}

		var items []json.RawMessage
		if err := json.Unmarshal(raw["items"], &items); err != nil {
			t.Errorf("GET %s 的 items 不是数组: %s", path, string(raw["items"]))
		}
	}
}

// TestListTotalIsRealCountNotPageLength total 必须走真实 COUNT。
//
// 用 len(items) 冒充 total 时，分页拉取「加载更多」的终止条件永远算不对，
// 而且接口看起来完全正常 —— 这是列表接口最容易埋进去、又最难归因的错。
func TestListTotalIsRealCountNotPageLength(t *testing.T) {
	r, svc := newAPIRouter(t)
	seedArticles(t, svc, 3)

	w := doJSON(r, http.MethodGet, "/api/v1/biz/articles?limit=1&offset=0")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /articles = %d, body=%s", w.Code, w.Body.String())
	}

	var got struct {
		Items  []Article `json:"items"`
		Total  int       `json:"total"`
		Limit  int       `json:"limit"`
		Offset int       `json:"offset"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析返回失败: %v", err)
	}

	if len(got.Items) != 1 {
		t.Errorf("limit=1 时返回 %d 条，期望 1 条", len(got.Items))
	}
	if got.Total != 3 {
		t.Errorf("total = %d，期望 3（真实条数，而不是当前页长度 %d）", got.Total, len(got.Items))
	}
	if got.Limit != 1 || got.Offset != 0 {
		t.Errorf("limit/offset 未回显请求值：limit=%d offset=%d", got.Limit, got.Offset)
	}
}

// TestFeedDigestNeedsLLM 动态看点在「有文章但 LLM 未配置」时返回 503，而不是 500。
//
// 前端据此降级到 n-gram 关键词抽取；若返回 500，前端会当成「服务出错」
// 而不是「功能不可用」，降级分支就永远不会走到。
//
// 注意前置条件：没有关注文章时接口先返回 400（ErrNoWatchedArticles），
// 那是另一条更早的判定，必须先喂进文章才能测到 503 这条。
func TestFeedDigestNeedsLLM(t *testing.T) {
	r, svc := newAPIRouter(t)
	seedArticles(t, svc, 1)
	if err := svc.Store().SetAccountsWatched([]string{"gh_1"}, true); err != nil {
		t.Fatalf("SetAccountsWatched: %v", err)
	}

	w := doJSON(r, http.MethodGet, "/api/v1/biz/feed/digest")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /feed/digest = %d，有文章且 LLM 未配置时期望 503（body=%s）",
			w.Code, w.Body.String())
	}
}

// --- 测试辅助 ---

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// seedArticles 写入 n 篇属于 gh_1 的文章。
//
// URL 必须逐条不同：UpsertArticles 按 source_url 去重，重复 URL 只会留一行 ——
// 那样「total 是真实条数」这条断言会因为夹具自身的问题而假失败。
func seedArticles(t *testing.T, svc *Service, n int) {
	t.Helper()

	if err := svc.Store().UpsertAccounts([]Account{{GHID: "gh_1", GHName: "甲号"}}); err != nil {
		t.Fatalf("UpsertAccounts: %v", err)
	}

	msgs := make([]*model.BizMessage, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, &model.BizMessage{
			GHID:   "gh_1",
			GHName: "甲号",
			Time:   time.Now().Add(-time.Duration(i) * time.Hour),
			Title:  fmt.Sprintf("标题 %d", i+1),
			URL:    fmt.Sprintf("https://mp.weixin.qq.com/s/seed-%d", i+1),
		})
	}
	if _, err := svc.Store().UpsertArticles(msgs); err != nil {
		t.Fatalf("UpsertArticles: %v", err)
	}
}
