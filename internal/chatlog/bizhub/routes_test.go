package bizhub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 本文件锁定「四页收敛为三页」之后的对外契约（设计说明 §2 / §6）：
//
//	/biz          动态（原 /biz + /biz/feed 合并，双模式）
//	/biz/reports  报告（原 /biz/summary 改名）
//	/biz/admin    管理（保留）
//
// 旧路由必须永久重定向，否则用户收藏的书签会直接 404 —— 这是合并页面时
// 最容易漏、又最影响真实使用的一步。

// newRouter 只注册路由，不准备 Service —— 页面 handler 在 svc 为 nil 时返回 503，
// 而重定向路由根本用不到 svc，所以这里够用。
func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group(""), func() *Service { return nil }, nil)
	return r
}

func TestLegacyPagesRedirectPermanently(t *testing.T) {
	r := newRouter()

	cases := []struct {
		from string
		to   string
	}{
		{"/biz/feed", "/biz"},
		{"/biz/summary", "/biz/reports"},
	}

	for _, tc := range cases {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.from, nil))

		if w.Code != http.StatusMovedPermanently {
			t.Errorf("GET %s = %d, 期望 301", tc.from, w.Code)
			continue
		}
		if got := w.Header().Get("Location"); got != tc.to {
			t.Errorf("GET %s 重定向到 %q, 期望 %q", tc.from, got, tc.to)
		}
	}
}

// TestLegacyRedirectKeepsQuery 重定向必须原样带上 query。
//
// 页面是靠 `?token=` 打开的，重定向把 token 丢掉的话，用户点旧书签会落到一个
// 能打开但所有接口 401 的页面上 —— 比直接 404 还难排查。
func TestLegacyRedirectKeepsQuery(t *testing.T) {
	r := newRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/biz/feed?token=abc123&x=1", nil))

	loc := w.Header().Get("Location")
	for _, want := range []string{"/biz?", "token=abc123", "x=1"} {
		if !strings.Contains(loc, want) {
			t.Errorf("重定向 Location = %q, 缺少 %q", loc, want)
		}
	}
}

// TestMergedPageRoutesRegistered 新路由必须在（且只在）预期的路径上。
func TestMergedPageRoutesRegistered(t *testing.T) {
	r := newRouter()

	for _, path := range []string{"/biz", "/biz/reports", "/biz/admin"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		// svc 为 nil，但路由命中后由 withRecoveryPage 统一返回 503。
		// 如果路由没注册，这里会是 404。
		if w.Code == http.StatusNotFound {
			t.Errorf("GET %s = 404，路由没注册", path)
		}
	}
}
