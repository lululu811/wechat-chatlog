package bizhub

import (
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var tmpl *template.Template

func init() {
	tmpl = template.Must(template.ParseFS(templateFS, "templates/*.html"))
}

// RegisterStatic 挂载公众号模块的公共静态资源（bizhub.css / bizhub.js）。
//
// 注意：必须挂在【鉴权中间件之外】。页面本身可以带 ?token= 访问，但 <link> /
// <script src> 发出的子请求不会带上 token，若走鉴权组会 401，页面就会丢样式与脚本。
func RegisterStatic(r gin.IRoutes, urlPrefix string) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("bizhub: 静态资源目录缺失: " + err.Error())
	}

	// 直接用 fs.ReadFile 按相对路径取文件，不走 http.FileServer ——
	// FileServer 会用请求的完整 URL 路径去 Open，而这里的 fs 根已经落在 static/，
	// 两者基准不一致会一律 404。这两个文件很小，整块读出即可。
	r.GET(urlPrefix+"/*filepath", func(c *gin.Context) {
		name := strings.TrimPrefix(c.Param("filepath"), "/")
		if name == "" || strings.Contains(name, "..") {
			c.String(http.StatusNotFound, "Not found")
			return
		}
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			c.String(http.StatusNotFound, "Not found")
			return
		}
		c.Data(http.StatusOK, staticContentType(name), data)
	})
}

func staticContentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// RegisterRoutes 注册 HTTP 路由，getSvc 在请求时动态获取服务实例，为 nil 时返回 503
// llm 为 nil 时汇总接口返回 503 llm not configured
//
// 路径规约（设计说明 §6「接口路径」）。旧路径不删，而是注册成带废弃标记的别名，
// 新旧并行过渡 —— 前端已全部切到新路径，旧路径留一段时间观察调用量再摘：
//
//	语义              旧路径                    新路径
//	生成报告          POST /summary              POST /reports
//	报告列表          GET  /summaries            GET  /reports
//	报告详情          GET  /summaries/:id        GET  /reports/:id
//	动态看点          GET  /feed/summary         GET  /feed/digest
//	批量摘要          POST /summary/generate-all POST /admin/batch/summary
//	可见性            POST /accounts/visibility  POST /admin/accounts/visibility
//	关注              POST /accounts/watch       POST /admin/accounts/watch
//	账号标签（批量）  POST /accounts/tags        POST /admin/accounts/tags
//	归档任务（建）    POST /export/batch         POST /admin/export/jobs
//	归档状态          GET  /export/status        GET  /admin/export/status
//
// 归档任务新增的子资源（无旧路径）：GET /admin/export/jobs、
// GET /admin/export/jobs/:id、POST /admin/export/jobs/:id/retry|cancel。
//
// ⚠ 旧路径 POST /export/batch 的行为已变：它此前同步跑完一整批才返回，
// 现在是建任务并立即返回 202 + 任务快照。老调用方若在等 success/failed 字段会拿到零值。
//
// 单篇摘要 POST /articles/:id/summary 与单篇导出 POST /articles/:id/export 未改名。
func RegisterRoutes(r *gin.RouterGroup, getSvc func() *Service, llm *LLMClient) {
	api := r.Group("/api/v1/biz")
	{
		// 账号与文章
		route(api, http.MethodGet, "/accounts", "", withRecovery(handleGetAccounts, getSvc))
		route(api, http.MethodGet, "/articles", "", withRecovery(handleGetArticles, getSvc))
		route(api, http.MethodGet, "/articles/:id", "", withRecovery(handleGetArticle, getSvc))
		route(api, http.MethodGet, "/search", "", withRecovery(handleSearch, getSvc))
		route(api, http.MethodGet, "/bookmarks", "", withRecovery(handleGetBookmarks, getSvc))
		route(api, http.MethodPost, "/sync", "", withRecovery(handleSync, getSvc))
		route(api, http.MethodGet, "/status", "", withRecovery(handleStatus, getSvc))
		route(api, http.MethodPost, "/articles/:id/bookmark", "",
			withRecovery(handleToggleArticleBookmark, getSvc))
		route(api, http.MethodPost, "/articles/:id/read", "",
			withRecovery(handleMarkArticleRead, getSvc))
		route(api, http.MethodPost, "/articles/mark-read", "",
			withRecovery(handleBatchMarkRead, getSvc))

		// 标签 API
		route(api, http.MethodGet, "/tags", "", withRecovery(handleGetTags, getSvc))
		route(api, http.MethodPost, "/tags", "", withRecovery(handleCreateTag, getSvc))
		route(api, http.MethodPatch, "/tags/:id", "", withRecovery(handleUpdateTag, getSvc))
		route(api, http.MethodDelete, "/tags/:id", "", withRecovery(handleDeleteTag, getSvc))
		route(api, http.MethodGet, "/tags/:id/accounts", "",
			withRecovery(handleGetAccountsByTag, getSvc))
		route(api, http.MethodGet, "/accounts/:ghid/tags", "",
			withRecovery(handleGetAccountTags, getSvc))
		route(api, http.MethodPost, "/accounts/:ghid/tags", "",
			withRecovery(handleSetAccountTags, getSvc))

		// 关注动态
		route(api, http.MethodGet, "/feed", "", withRecovery(handleGetFeed, getSvc))
		route(api, http.MethodGet, "/feed/digest", "/feed/summary", withRecovery(func(c *gin.Context, svc *Service) {
			handleGetFeedDigest(c, svc, llm)
		}, getSvc))
		route(api, http.MethodGet, "/daily-digest", "", withRecovery(func(c *gin.Context, svc *Service) {
			handleGetDailyDigest(c, svc, llm)
		}, getSvc))
		route(api, http.MethodGet, "/sectors", "", withRecovery(handleGetSectorDashboard, getSvc))
		route(api, http.MethodGet, "/timeline", "", withRecovery(handleSearchTimeline, getSvc))

		// 报告
		route(api, http.MethodPost, "/reports", "/summary", withRecovery(func(c *gin.Context, svc *Service) {
			handleGenerateReport(c, svc, llm)
		}, getSvc))
		route(api, http.MethodGet, "/reports", "/summaries", withRecovery(handleListReports, getSvc))
		route(api, http.MethodGet, "/reports/:id", "/summaries/:id", withRecovery(handleGetReport, getSvc))

		// 管理：账号
		route(api, http.MethodGet, "/admin/accounts", "", withRecovery(handleAdminGetAccounts, getSvc))
		route(api, http.MethodPost, "/admin/accounts/visibility", "/accounts/visibility",
			withRecovery(handleSetAccountsVisibility, getSvc))
		route(api, http.MethodPost, "/admin/accounts/watch", "/accounts/watch",
			withRecovery(handleSetAccountsWatched, getSvc))
		route(api, http.MethodPost, "/admin/accounts/tags", "/accounts/tags",
			withRecovery(handleBatchSetAccountTags, getSvc))

		// 管理：批量任务
		route(api, http.MethodPost, "/admin/batch/summary", "/summary/generate-all",
			withRecovery(func(c *gin.Context, svc *Service) {
				handleGenerateBatchSummaries(c, svc, llm)
			}, getSvc))
		// 归档任务。
		//
		// 路径从动作式（POST /export/batch 一把跑完）改成资源式
		// （POST /admin/export/jobs 建任务、GET /admin/export/jobs/:id 看进度）。
		// 旧路径作为废弃别名保留，但行为已变：不再同步阻塞，而是返回任务快照。
		// 这一点必须写清楚 —— 老调用方（脚本）拿到 202 + job 对象，
		// 不能再去读 success/failed 字段。
		//
		// /export/batch 是更早一代的路径，route() 只接一个旧路径，所以下面手工再挂一条：
		// 它已经存在了一段时间，直接 404 会让已经写好的脚本一起坏掉。
		route(api, http.MethodPost, "/admin/export/jobs", "/admin/export/batch",
			withRecovery(handleStartExportJob, getSvc))
		api.POST("/export/batch", deprecatedAlias(http.MethodPost, api.BasePath(),
			"/admin/export/jobs", withRecovery(handleStartExportJob, getSvc)))
		route(api, http.MethodGet, "/admin/export/jobs", "",
			withRecovery(handleListExportJobs, getSvc))
		route(api, http.MethodGet, "/admin/export/jobs/:id", "",
			withRecovery(handleGetExportJob, getSvc))
		route(api, http.MethodPost, "/admin/export/jobs/:id/retry", "",
			withRecovery(handleRetryExportJob, getSvc))
		route(api, http.MethodPost, "/admin/export/jobs/:id/cancel", "",
			withRecovery(handleCancelExportJob, getSvc))
		route(api, http.MethodGet, "/admin/export/status", "/export/status",
			withRecovery(handleExportStatus, getSvc))

		// IMA 推送任务（与 MD 归档镜像：admin/push/jobs）。
		// 单篇推送已注册到 /articles/:id/push（见上），这里只挂批量任务的 5 条。
		route(api, http.MethodPost, "/admin/push/jobs", "",
			withRecovery(handleStartPushJob, getSvc))
		route(api, http.MethodGet, "/admin/push/jobs", "",
			withRecovery(handleListPushJobs, getSvc))
		route(api, http.MethodGet, "/admin/push/jobs/:id", "",
			withRecovery(handleGetPushJob, getSvc))
		route(api, http.MethodPost, "/admin/push/jobs/:id/retry", "",
			withRecovery(handleRetryPushJob, getSvc))
		route(api, http.MethodPost, "/admin/push/jobs/:id/cancel", "",
			withRecovery(handleCancelPushJob, getSvc))
		route(api, http.MethodGet, "/admin/push/status", "",
			withRecovery(handlePushStatus, getSvc))

		// Pipeline worker 控制（PR1 引入）。
		//
		// 路径归到 admin/ 下与 export/push 任务保持一致；命名上「pipeline」
		// 比「pipeline_jobs」更准确，因为 worker 不维护"任务"概念，
		// 它只推进 article 行（pipeline_status 单列状态机）。
		route(api, http.MethodGet, "/admin/pipeline/status", "",
			withRecovery(handlePipelineStatus, getSvc))
		route(api, http.MethodPost, "/admin/pipeline/trigger", "",
			withRecovery(handlePipelineTrigger, getSvc))
		route(api, http.MethodPost, "/admin/pipeline/retry/:id", "",
			withRecovery(handlePipelineRetry, getSvc))

		// 单篇操作（未改名）
		route(api, http.MethodPost, "/articles/:id/export", "",
			withRecovery(handleExportArticle, getSvc))
		route(api, http.MethodPost, "/articles/:id/push", "",
			withRecovery(handlePushArticle, getSvc))
		route(api, http.MethodPost, "/articles/:id/summary", "", withRecovery(func(c *gin.Context, svc *Service) {
			handleGenerateArticleSummary(c, svc, llm)
		}, getSvc))
	}

	r.GET("/biz", withRecoveryPage(handleBizPage, getSvc))
	r.GET("/biz/reports", withRecoveryPage(handleReportsPage, getSvc))
	r.GET("/biz/admin", withRecoveryPage(handleAdminPage, getSvc))
	r.GET("/biz/sectors", withRecoveryPage(handleSectorsPage, getSvc))

	// 旧路由永久重定向，避免旧书签失效（设计说明 §6）。
	// 用 301 而不是 302：这两个路径不会再回来，让浏览器/爬虫直接换掉书签。
	r.GET("/biz/feed", redirectPage("/biz"))
	r.GET("/biz/summary", redirectPage("/biz/reports"))
}

// route 在新路径上注册一条 API；oldPath 非空时，同时在旧路径上注册一个别名。
//
// 为什么是「别名」而不是 301：接口的旧路径是被前端 JS 调用的，浏览器 fetch
// 跟随 301 会丢掉 POST body 与 Authorization 头，302/301 在这里都不安全。
// 所以旧路径直接复用同一个 handler，只在响应上打废弃标记，便于统计何时可以摘。
func route(g *gin.RouterGroup, method, newPath, oldPath string, h gin.HandlerFunc) {
	g.Handle(method, newPath, h)
	if oldPath == "" || oldPath == newPath {
		return
	}
	g.Handle(method, oldPath, deprecatedAlias(method, g.BasePath(), newPath, h))
}

// deprecatedAlias 复用新路径的 handler，并标注本条请求走的是已废弃路径。
//
// 响应头按 RFC 8594 / RFC 8288 给出替代路径，日志里同时留一条 warn ——
// 「旧路径还在被调用」是摘除旧路径前唯一的判断依据。
func deprecatedAlias(method, base, newPath string, h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Deprecation", "true")
		c.Header("Link", "<"+canonicalURL(base, newPath, c)+">; rel=\"successor-version\"")
		log.Warn().
			Str("method", method).
			Str("path", c.Request.URL.Path).
			Str("use", newPath).
			Msg("bizhub: 已废弃的接口路径被调用")
		h(c)
	}
}

// canonicalURL 把规范路径模板（如 /reports/:id）按当前请求的参数值实例化成
// 一条真实可访问的路径（/api/v1/biz/reports/42），供 Link 头指向。
//
// 直接用模板串会给出 /reports/:id 这种带占位符的「路径」，客户端拿去请求会 404，
// 也就失去了 successor-version 的意义。
func canonicalURL(base, pattern string, c *gin.Context) string {
	segs := strings.Split(pattern, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") {
			if v := c.Param(s[1:]); v != "" {
				segs[i] = v
			}
		}
	}
	return strings.TrimSuffix(base, "/") + strings.Join(segs, "/")
}

// listResponse 是全部列表接口的统一返回形状（设计说明 §6「列表返回」）。
//
// 统一成 items + 真实 total + 回显的 limit/offset，而不是每个接口各起一个
// articles / summaries / accounts / tags 字段名：前端读分页信息的地方
// 就从 5 处收敛到 1 处，加新列表接口也不会再引入第 6 种写法。
//
// total 必须是真实 COUNT，不能是 len(items) —— 否则分页时「加载更多」
// 的终止条件永远算不对（items 只是当前页）。limit/offset 固定回显请求值；
// 未分页的列表接口传 0/0，表示「一次给全，没有下一页」。
func listResponse(items any, total, limit, offset int) gin.H {
	return gin.H{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}
}

// redirectPage 301 到目标路径，并原样带上原 query。
//
// 必须带 query：页面是靠 `?token=` 打开的，token 丢了重定向过去也进不去。
func redirectPage(to string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if q := c.Request.URL.RawQuery; q != "" {
			to += "?" + q
		}
		c.Redirect(http.StatusMovedPermanently, to)
	}
}

// withRecovery 包装 handler 添加 panic 恢复
func withRecovery(handler func(c *gin.Context, svc *Service), getSvc func() *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			}
		}()
		svc := getSvc()
		if svc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bizhub service is not ready"})
			return
		}
		handler(c, svc)
	}
}

func withRecoveryPage(handler func(c *gin.Context, svc *Service), getSvc func() *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				c.String(http.StatusInternalServerError, "internal server error")
			}
		}()
		svc := getSvc()
		if svc == nil {
			c.String(http.StatusServiceUnavailable, "bizhub service is not ready")
			return
		}
		handler(c, svc)
	}
}

// handleGetAccounts 获取公众号列表
func handleGetAccounts(c *gin.Context, svc *Service) {
	accounts := svc.GetAccounts()
	if accounts == nil {
		accounts = []Account{}
	}
	c.JSON(http.StatusOK, listResponse(accounts, len(accounts), 0, 0))
}

// handleGetArticles 获取文章列表（懒加载）。
//
// 两个查询参数都可选，对应「全部」模式当资料库用的场景：
//
//	ghid  不传 = 全部公众号（含未关注、含已隐藏）
//	days  不传或 <=0 = 不限时间窗
func handleGetArticles(c *gin.Context, svc *Service) {
	ghid := c.Query("ghid")
	days, _ := strconv.Atoi(c.DefaultQuery("days", "0"))
	if days < 0 {
		days = 0
	}
	if days > 30 {
		days = 30
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	unreadOnly, _ := strconv.ParseBool(c.DefaultQuery("unread_only", "false"))

	filter := ArticleFilter{GHID: ghid, Days: days, Limit: limit, Offset: offset, UnreadOnly: unreadOnly}
	articles, err := svc.ListArticlesWithFilter(filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if articles == nil {
		articles = []Article{}
	}
	// ghid / days 仍在返回里回显：前端切模式时靠它确认后端真实生效的范围，
	// 而不是只看自己发出去的参数。
	resp := listResponse(articles, svc.CountListArticlesWithFilter(filter), limit, offset)
	resp["ghid"] = ghid
	resp["days"] = days
	resp["unreadOnly"] = unreadOnly
	c.JSON(http.StatusOK, resp)
}

// handleGetArticle 获取单篇文章
func handleGetArticle(c *gin.Context, svc *Service) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	article, err := svc.GetArticle(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if article == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "article not found"})
		return
	}
	c.JSON(http.StatusOK, article)
}

// handleSearch 搜索文章（懒加载）
func handleSearch(c *gin.Context, svc *Service) {
	keyword := c.Query("keyword")
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "keyword is required"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	articles, err := svc.SearchArticles(keyword, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if articles == nil {
		articles = []Article{}
	}
	resp := listResponse(articles, svc.CountSearchArticles(keyword), limit, offset)
	resp["keyword"] = keyword
	c.JSON(http.StatusOK, resp)
}

// handleSync 手动触发同步
func handleSync(c *gin.Context, svc *Service) {
	var req struct {
		GHID string `json:"ghid"`
	}
	_ = c.ShouldBindJSON(&req)

	if svc.IsSyncing() {
		c.JSON(http.StatusConflict, gin.H{"error": "sync already in progress"})
		return
	}

	if req.GHID != "" {
		go func() {
			svc.SyncOne(req.GHID)
		}()
		c.JSON(http.StatusOK, gin.H{"message": "syncing single account", "ghid": req.GHID})
	} else {
		go func() {
			svc.SyncAll()
		}()
		c.JSON(http.StatusOK, gin.H{"message": "full sync started"})
	}
}

// handleStatus 获取服务状态
func handleStatus(c *gin.Context, svc *Service) {
	c.JSON(http.StatusOK, gin.H{
		"syncing":      svc.IsSyncing(),
		"articleCount": svc.GetArticleCount(),
		"accountCount": len(svc.GetAccounts()),
		"lastSync":     svc.LastResult(),
	})
}

// handleBizPage 渲染动态页（双模式：动态 / 全部）—— SSR 账号、标签与账号-标签关系。
func handleBizPage(c *gin.Context, svc *Service) {
	accounts := svc.GetAccounts()
	tags := svc.GetTags()

	// 批量预加载所有公众号的标签（一次 DB 遍历，避免前端 N+1 请求）
	allAccountTags := svc.GetAllAccountTags()

	// 标签序列化为 JSON 供前端使用
	tagsJSON, _ := json.Marshal(tags)
	// 标签 -> 公众号 ghid 列表，供前端按标签过滤文章流（后端接口不接标签条件，
	// 而标签关系已经在这次 SSR 里全量拿到了，客户端过滤比再加一个接口便宜）。
	accountTagsJSON, _ := json.Marshal(tagToGHIDs(tags, allAccountTags))

	data := gin.H{
		"Accounts":        accounts,
		"ArticleCount":    svc.GetArticleCount(),
		"Tags":            tags,
		"TagsJSON":        string(tagsJSON),
		"AccountTags":     allAccountTags,
		"AccountTagsJSON": string(accountTagsJSON),
		"Syncing":         svc.IsSyncing(),
		"LastSync":        svc.LastResult(),
		"Now":             time.Now().Format("2006-01-02 15:04:05"),
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(c.Writer, "biz.html", data); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
	}
}

// tagToGHIDs 把「ghid -> 标签」倒排成「标签ID -> ghid 列表」。
//
// 注意 JSON 的 key 一律是字符串：前端要用 tagID（数字）去取，取值时得先转字符串，
// 这一层不能省，否则过滤会静默失效（查不到 key，返回 undefined，全部文章都被过滤掉）。
func tagToGHIDs(tags []Tag, byGHID map[string][]Tag) map[string][]string {
	out := make(map[string][]string, len(tags))
	for ghid, ts := range byGHID {
		for _, t := range ts {
			key := strconv.FormatInt(t.ID, 10)
			out[key] = append(out[key], ghid)
		}
	}
	return out
}

// handleGetTags 获取所有标签
func handleGetTags(c *gin.Context, svc *Service) {
	tags := svc.GetTags()
	if tags == nil {
		tags = []Tag{}
	}
	c.JSON(http.StatusOK, listResponse(tags, len(tags), 0, 0))
}

// handleCreateTag 创建标签
func handleCreateTag(c *gin.Context, svc *Service) {
	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	tag, err := svc.CreateTag(req.Name, req.Color)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, tag)
}

// handleDeleteTag 删除标签
func handleDeleteTag(c *gin.Context, svc *Service) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := svc.DeleteTag(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// handleGetAccountsByTag 根据标签获取公众号（懒加载）
func handleGetAccountsByTag(c *gin.Context, svc *Service) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	accounts, err := svc.GetAccountsByTag(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if accounts == nil {
		accounts = []Account{}
	}
	c.JSON(http.StatusOK, listResponse(accounts, len(accounts), 0, 0))
}

// handleGetAccountTags 获取公众号的标签（懒加载）
func handleGetAccountTags(c *gin.Context, svc *Service) {
	ghid := c.Param("ghid")
	if ghid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ghid is required"})
		return
	}

	tags, err := svc.GetAccountTags(ghid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tags == nil {
		tags = []Tag{}
	}
	c.JSON(http.StatusOK, listResponse(tags, len(tags), 0, 0))
}

// handleSetAccountTags 设置公众号的标签。
// mode 支持 add / remove / replace，缺省 replace（覆盖式，兼容原有前端行为）。
func handleSetAccountTags(c *gin.Context, svc *Service) {
	ghid := c.Param("ghid")
	if ghid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ghid is required"})
		return
	}

	var req struct {
		TagIDs []int64 `json:"tagIDs"`
		Mode   string  `json:"mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	mode := TagModeReplace
	if req.Mode != "" {
		parsed, err := ParseTagAssignMode(req.Mode)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		mode = parsed
	}

	if err := svc.SetAccountTagsMode(ghid, req.TagIDs, mode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "updated", "mode": mode})
}

// handleAdminGetAccounts 获取所有公众号（含隐藏，供管理页使用）
func handleAdminGetAccounts(c *gin.Context, svc *Service) {
	keyword := strings.ToLower(strings.TrimSpace(c.Query("keyword")))
	filter := c.DefaultQuery("filter", "all")

	accounts, err := svc.GetAllAccounts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	tagsMap, err := svc.Store().GetAllAccountTagsMap()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	result := make([]AccountWithTag, 0, len(accounts))
	for _, acc := range accounts {
		if filter == "visible" && acc.Hidden {
			continue
		}
		if filter == "hidden" && !acc.Hidden {
			continue
		}
		if keyword != "" &&
			!strings.Contains(strings.ToLower(acc.GHName), keyword) &&
			!strings.Contains(strings.ToLower(acc.GHID), keyword) {
			continue
		}
		tags := tagsMap[acc.GHID]
		if tags == nil {
			tags = []Tag{}
		}
		result = append(result, AccountWithTag{Account: acc, Tags: tags})
	}

	c.JSON(http.StatusOK, listResponse(result, len(result), 0, 0))
}

// handleSetAccountsVisibility 批量设置公众号隐藏状态
func handleSetAccountsVisibility(c *gin.Context, svc *Service) {
	var req struct {
		GHIDs  []string `json:"ghIDs"`
		Hidden bool     `json:"hidden"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if len(req.GHIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ghIDs is required"})
		return
	}

	if err := svc.SetAccountsHidden(req.GHIDs, req.Hidden); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

// handleBatchSetAccountTags 批量设置公众号标签。
// mode 支持 add / remove / replace，缺省 add——批量追加不会清掉各账号已有的分类。
// 全部账号在同一事务内写入，失败即整体回滚。
func handleBatchSetAccountTags(c *gin.Context, svc *Service) {
	var req struct {
		GHIDs  []string `json:"ghIDs"`
		TagIDs []int64  `json:"tagIDs"`
		Mode   string   `json:"mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if len(req.GHIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ghIDs is required"})
		return
	}

	mode, err := ParseTagAssignMode(req.Mode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := svc.SetAccountsTagsMode(req.GHIDs, req.TagIDs, mode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "updated", "mode": mode, "count": len(req.GHIDs)})
}

// handleUpdateTag 更新标签名称和颜色
func handleUpdateTag(c *gin.Context, svc *Service) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	if err := svc.UpdateTag(id, req.Name, req.Color); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

// handleAdminPage 渲染公众号管理页面（SSR，标签内嵌，账号由前端拉取）
func handleAdminPage(c *gin.Context, svc *Service) {
	tags := svc.GetTags()

	tagsJSON, _ := json.Marshal(tags)

	data := gin.H{
		"Tags":     tags,
		"TagsJSON": string(tagsJSON),
		"Now":      time.Now().Format("2006-01-02 15:04:05"),
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(c.Writer, "admin.html", data); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
	}
}

// handleSetAccountsWatched 批量设置公众号关注状态
func handleSetAccountsWatched(c *gin.Context, svc *Service) {
	var req struct {
		GHIDs   []string `json:"ghIDs"`
		Watched bool     `json:"watched"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if len(req.GHIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ghIDs is required"})
		return
	}

	if err := svc.SetAccountsWatched(req.GHIDs, req.Watched); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

// handleGetFeed 获取关注公众号的文章流（分页；按 published_at >= now - days*24h 过滤）
func handleGetFeed(c *gin.Context, svc *Service) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days <= 0 {
		days = 7
	}
	if days > 30 {
		days = 30
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	articles, err := svc.GetFeedArticles(days, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if articles == nil {
		articles = []Article{}
	}
	resp := listResponse(articles, svc.CountFeedArticles(days), limit, offset)
	resp["days"] = days
	c.JSON(http.StatusOK, resp)
}

// handleGetFeedDigest 返回关注动态的结构化 LLM 汇总；window 默认 7、clamp 1-30；fresh=true 跳过缓存。
func handleGetFeedDigest(c *gin.Context, svc *Service, llm *LLMClient) {
	window, _ := strconv.Atoi(c.DefaultQuery("window", "7"))
	if window <= 0 {
		window = 7
	}
	if window < 1 {
		window = 1
	}
	if window > 30 {
		window = 30
	}
	fresh, _ := strconv.ParseBool(c.DefaultQuery("fresh", "false"))

	summary, err := svc.GenerateFeedSummary(c.Request.Context(), window, llm, fresh)
	if err != nil {
		if errors.Is(err, ErrLLMNotConfigured) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrNoWatchedArticles) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrNotJSON) {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"summary": summary})
}

// handleGetDailyDigest 获取每日精选（LLM 从 watched 账号文章里挑选）
func handleGetDailyDigest(c *gin.Context, svc *Service, llm *LLMClient) {
	fresh, _ := strconv.ParseBool(c.DefaultQuery("fresh", "false"))

	digest, err := svc.GenerateDailyDigest(c.Request.Context(), llm, fresh)
	if err != nil {
		switch {
		case errors.Is(err, ErrLLMNotConfigured):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		case errors.Is(err, ErrNoWatchedArticles):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, ErrNotJSON):
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"digest": digest})
}

// handleGetSectorDashboard 板块看板：按标签聚合 watched 账号的近期文章热度
func handleGetSectorDashboard(c *gin.Context, svc *Service) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days <= 0 {
		days = 7
	}
	if days > 30 {
		days = 30
	}

	sectors, err := svc.GetSectorDashboard(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sectors": sectors, "days": days})
}

// handleSearchTimeline 文章时间线：按关键词搜索 watched 账号文章，按时间排序
func handleSearchTimeline(c *gin.Context, svc *Service) {
	keyword := c.Query("keyword")
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "keyword is required"})
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	articles, total, err := svc.SearchTimeline(keyword, days, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"keyword": keyword,
		"days":    days,
		"total":   total,
		"items":   articles,
	})
}

// handleGenerateReport 同步生成关注公众号的文章汇总
func handleGenerateReport(c *gin.Context, svc *Service, llm *LLMClient) {
	var req SummaryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Days != 1 && req.Days != 3 && req.Days != 7 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "days must be 1, 3 or 7"})
		return
	}
	if req.Scope != "" && req.Scope != "watched" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope must be 'watched' or empty"})
		return
	}
	if llm == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": ErrLLMNotConfigured.Error()})
		return
	}

	summary, err := svc.GenerateSummary(c.Request.Context(), req, llm)
	if err != nil {
		if errors.Is(err, ErrLLMNotConfigured) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrNoWatchedArticles) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"summary": summary})
}

// handleListReports 获取报告列表（不含正文）
func handleListReports(c *gin.Context, svc *Service) {
	summaries, err := svc.ListSummaries()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if summaries == nil {
		summaries = []Summary{}
	}
	c.JSON(http.StatusOK, listResponse(summaries, len(summaries), 0, 0))
}

// handleGetReport 获取单条汇总（含正文）
func handleGetReport(c *gin.Context, svc *Service) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	summary, err := svc.GetSummary(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if summary == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "summary not found"})
		return
	}
	c.JSON(http.StatusOK, summary)
}

// handleReportsPage 渲染报告页（列表由前端拉取，仅 SSR 顶部 nav 需要的 TagsJSON）。
//
// 从 /biz/summary 改名而来：汇总生成的唯一入口收敛到这一页，
// 动态页只留一个跳转入口（设计说明 §3.2）。
func handleReportsPage(c *gin.Context, svc *Service) {
	tags := svc.GetTags()
	tagsJSON, _ := json.Marshal(tags)

	data := gin.H{
		"Tags":     tags,
		"TagsJSON": string(tagsJSON),
		"Now":      time.Now().Format("2006-01-02 15:04:05"),
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(c.Writer, "reports.html", data); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
	}
}

// handleSectorsPage 板块看板页
func handleSectorsPage(c *gin.Context, svc *Service) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(c.Writer, "sectors.html", nil); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
	}
}

// handleToggleArticleBookmark 切换单篇文章收藏状态
func handleToggleArticleBookmark(c *gin.Context, svc *Service) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req struct {
		Bookmarked bool `json:"bookmarked"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if err := svc.ToggleArticleBookmark(id, req.Bookmarked); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":         id,
		"bookmarked": req.Bookmarked,
	})
}

// handleMarkArticleRead 标记单篇文章已读/未读
func handleMarkArticleRead(c *gin.Context, svc *Service) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req struct {
		IsRead bool `json:"isRead"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if err := svc.MarkArticleRead(id, req.IsRead); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":     id,
		"isRead": req.IsRead,
	})
}

// handleBatchMarkRead 批量标记已读。支持两种模式：
//
//	POST {ids: [1,2,3]}              — 按 ID 列表
//	POST {ghid: "gh_xxx", days: 7}   — 按账号 + 时间范围
//	POST {days: 7}                   — 全部账号最近 N 天
func handleBatchMarkRead(c *gin.Context, svc *Service) {
	var req struct {
		IDs  []int64 `json:"ids"`
		GHID string  `json:"ghid"`
		Days int     `json:"days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	affected, err := svc.BatchMarkRead(req.IDs, req.GHID, req.Days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"marked": affected})
}

// handleGetBookmarks 获取已收藏文章列表
func handleGetBookmarks(c *gin.Context, svc *Service) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	articles, err := svc.GetBookmarkedArticles(limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if articles == nil {
		articles = []Article{}
	}
	c.JSON(http.StatusOK, listResponse(articles, svc.CountBookmarkedArticles(), limit, offset))
}

// --- MD 导出 handlers ---

func handleExportArticle(c *gin.Context, svc *Service) {
	if !svc.IsExportConfigured() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "MD export not configured. Set md_export_script and md_export_dir in config."})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid article id"})
		return
	}

	result, err := svc.ExportArticle(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// --- IMA 推送 handlers ---

// handlePushArticle 单篇推送。
//
// 链式校验：
//   1. 必须先配置 IMA（skill_dir + kb_id）
//   2. 文章必须已经 MD 归档成功（status IN 'exported','summary_generated'）
//   否则返回 400 / 409。
//
// 400 / 409 都有明确的文案说明如何修复 —— 用户点了一个被禁用的按钮时，
// 错误文案是仅有的反馈渠道。
func handlePushArticle(c *gin.Context, svc *Service) {
	if !svc.IsPushConfigured() {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "IMA push not configured. Set ima_push_skill_dir and ima_push_kb_id in config.",
		})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid article id"})
		return
	}

	// 链式前置校验：未 export 成功的不能 push。
	//
	// 这里单独拎出来，而不是塞到 PushArticle 里，是为了把校验失败的状态码
	// 控制在 handler —— handler 决定 400 vs 409 vs 500，service 只返回语义。
	exported, err := svc.IsArticleExported(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !exported {
		c.JSON(http.StatusConflict, gin.H{
			"error": "文章尚未归档成功，无法推送。请先执行 MD 归档。",
		})
		return
	}

	result, err := svc.PushArticle(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func handleStartExportJob(c *gin.Context, svc *Service) {
	var req ExportBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	job, err := svc.StartExportJob(req)
	if err != nil {
		switch {
		case errors.Is(err, ErrExportNotConfigured):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, ErrExportJobRunning):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, ErrInvalidExportWindow):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	// 202 而不是 200：任务已接受但**还没跑完**。用 200 会让调用方误以为
	// 返回体里的数字是最终结果。
	c.JSON(http.StatusAccepted, gin.H{"job": job})
}

func handleGetExportJob(c *gin.Context, svc *Service) {
	id := c.Param("id")
	// 条目明细默认只在任务结束后才带回来：轮询期间的返回体要保持小，
	// 一次任务最多 1000 条，每秒带一份明细会把这条轮询通道撑爆。
	withItems := c.Query("items") == "1"

	job, items, err := svc.GetExportJob(id, withItems)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if job == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "归档任务不存在"})
		return
	}
	if !withItems && job.Done() {
		job, items, err = svc.GetExportJob(id, true)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if items == nil {
		items = []ExportJobItem{}
	}

	c.JSON(http.StatusOK, gin.H{
		"job":         job,
		"items":       items,
		"activeJobID": svc.ActiveExportJob(),
		"percent":     job.Percent(),
		"processed":   job.Processed(),
	})
}

func handleListExportJobs(c *gin.Context, svc *Service) {
	limit := 10
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	jobs, err := svc.ListExportJobs(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if jobs == nil {
		jobs = []*ExportJob{}
	}
	c.JSON(http.StatusOK, gin.H{
		"jobs":        jobs,
		"activeJobID": svc.ActiveExportJob(),
	})
}

func handleRetryExportJob(c *gin.Context, svc *Service) {
	job, err := svc.RetryExportJob(c.Param("id"))
	if err != nil {
		switch {
		case errors.Is(err, ErrExportNotConfigured):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, ErrExportJobNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, ErrExportJobRunning):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"job": job})
}

func handleCancelExportJob(c *gin.Context, svc *Service) {
	id, ok := svc.CancelExportJob()
	if !ok {
		c.JSON(http.StatusConflict, gin.H{"error": "当前没有正在运行的归档任务"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"canceled": true, "jobID": id})
}

func handleExportStatus(c *gin.Context, svc *Service) {
	daysParam, _ := strconv.Atoi(c.DefaultQuery("days", "0"))
	days, err := ValidateExportWindow(daysParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !svc.IsExportConfigured() {
		c.JSON(http.StatusOK, gin.H{
			"configured": false,
			"exported":   0,
			"pending":    0,
			"blocked":    0,
			"days":       days,
		})
		return
	}

	stats, err := svc.GetExportStatus(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"configured": true,
		"exported":   stats.Exported,
		"pending":    stats.Pending,
		"blocked":    stats.Blocked,
		"days":       stats.Days,
	})
}

// --- IMA 推送 任务 handlers ---
//
// 路径形态与 MD 归档对仗：admin/push/jobs 与 admin/push/jobs/:id。
// 状态码语义也一致：202 = 任务已接受；404 = 任务不存在；409 = 已有任务在跑 / 任务在跑时拒绝重试。

func handleStartPushJob(c *gin.Context, svc *Service) {
	var req PushBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	job, err := svc.StartPushJob(req)
	if err != nil {
		switch {
		case errors.Is(err, ErrPushNotConfigured):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, ErrPushJobRunning):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, ErrInvalidExportWindow):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"job": job, "activeJobID": svc.ActivePushJob()})
}

func handleGetPushJob(c *gin.Context, svc *Service) {
	id := c.Param("id")
	withItems := c.DefaultQuery("items", "false") == "true"

	job, items, err := svc.GetPushJob(id, withItems)
	if err != nil {
		if errors.Is(err, ErrPushJobNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if withItems && items == nil {
		items = []PushJobItem{}
	}

	if !withItems {
		job2, err := svc.store.GetPushJob(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if job2 == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "推送任务不存在"})
			return
		}
		job2.Succeeded = job.Succeeded
		job2.Failed = job.Failed
		job2.Skipped = job.Skipped
		job2.Running = job.Running
		job2.Pending = job.Pending
		job = job2
	}

	resp := gin.H{"job": job, "activeJobID": svc.ActivePushJob(), "percent": job.Percent(), "processed": job.Processed()}
	if withItems {
		resp["items"] = items
	}
	c.JSON(http.StatusOK, resp)
}

func handleListPushJobs(c *gin.Context, svc *Service) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	jobs, err := svc.ListPushJobs(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if jobs == nil {
		jobs = []*PushJob{}
	}
	c.JSON(http.StatusOK, gin.H{"items": jobs, "activeJobID": svc.ActivePushJob()})
}

func handleRetryPushJob(c *gin.Context, svc *Service) {
	job, err := svc.RetryPushJob(c.Param("id"))
	if err != nil {
		switch {
		case errors.Is(err, ErrPushJobNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, ErrExportJobRunning):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"job": job, "activeJobID": svc.ActivePushJob()})
}

func handleCancelPushJob(c *gin.Context, svc *Service) {
	if err := svc.CancelPushJob(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func handlePushStatus(c *gin.Context, svc *Service) {
	daysParam, _ := strconv.Atoi(c.DefaultQuery("days", "0"))
	days, err := ValidatePushWindow(daysParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !svc.IsPushConfigured() {
		c.JSON(http.StatusOK, gin.H{
			"configured": false,
			"pushed":     0,
			"pending":    0,
			"blocked":    0,
			"days":       days,
		})
		return
	}

	stats, err := svc.GetPushStatus(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"configured": true,
		"pushed":     stats.Pushed,
		"pending":    stats.Pending,
		"blocked":    stats.Blocked,
		"days":       stats.Days,
	})
}

// --- 文章摘要生成 handlers ---

func handleGenerateArticleSummary(c *gin.Context, svc *Service, llm *LLMClient) {
	if llm == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "LLM not configured"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid article id"})
		return
	}

	if err := svc.GenerateArticleSummary(c.Request.Context(), id, llm); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "summary generated", "articleID": id})
}

func handleGenerateBatchSummaries(c *gin.Context, svc *Service, llm *LLMClient) {
	if llm == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "LLM not configured"})
		return
	}

	var req struct {
		Limit int `json:"limit"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Limit <= 0 {
		req.Limit = 50
	}

	success, failed, skipped, errors := svc.GenerateBatchSummaries(c.Request.Context(), llm, req.Limit)
	c.JSON(http.StatusOK, gin.H{
		"success": success,
		"failed":  failed,
		"skipped": skipped,
		"errors":  errors,
	})
}

// --- Pipeline worker handlers（PR1） ---
//
// 三个端点的语义：
//
//	GET  /admin/pipeline/status   — 状态快照：每状态计数 + 最近 20 条失败
//	POST /admin/pipeline/trigger  — 主动跑一次 RunOnce（非阻塞，立刻 200）
//	POST /admin/pipeline/retry/:id — 把单篇 failed:* 倒回 pending，下次 tick 推
//
// trigger 是 non-blocking 设计：worker 在后台 goroutine 跑 RunOnce，
// HTTP handler 只是 chan push。如果阻塞返回耗时（"我触发完了"），
// 会被前端误读为「这一批已经推完了」。
//
// retry 是同步的：DB 操作是单条 UPDATE，毫秒级返回。

// handlePipelineStatus 返回 pipeline 状态快照。
//
// 返回字段：
//   enabled   — worker 是否启用（false 时 ticker 不会触发）
//   counts    — map[status]count，例如 {"pending": 23000, "pushed": 100, "failed:fetch": 5}
//   recent    — 最近 20 条 failed 文章（任意 stage），含 pipeline_error 字段
//
// recent 给运维一个直观的"现在卡在哪"，counts 给"工作量分布"。
func handlePipelineStatus(c *gin.Context, svc *Service) {
	enabled := false
	if w := svc.Worker(); w != nil {
		enabled = w.cfg.Enabled
	}

	counts, err := svc.Store().PipelineStatusCount(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if counts == nil {
		counts = map[string]int{}
	}

	recent, err := svc.Store().RecentFailedArticles(c.Request.Context(), 20)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if recent == nil {
		recent = []Article{}
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled": enabled,
		"counts":  counts,
		"recent":  recent,
	})
}

// handlePipelineTrigger 主动触发一次 worker tick。
//
// 立即返回 202 + {triggered: true}，不等 RunOnce 跑完 —— RunOnce 可能要 30s+。
// 前端要等结果请轮询 /admin/pipeline/status 看 counts 是否变化。
//
// 没启用 worker 时（Worker() == nil 或 cfg.Enabled=false）也允许手动 trigger：
// 这是有意的 —— 「关掉自动、想手动推一批」的运维场景下，trigger 仍然能跑。
// 此时 channeled trigger 没人在接收，调用会立即 return 但 RunOnce 不会跑。
// 现阶段允许这种"空跑"以简化前端；未来可以让 trigger 临时启动 RunOnce。
func handlePipelineTrigger(c *gin.Context, svc *Service) {
	w := svc.Worker()
	if w == nil {
		c.JSON(http.StatusOK, gin.H{
			"triggered": false,
			"reason":    "worker not started (TUI mode or BizWorkerEnabled=false at boot)",
		})
		return
	}
	w.Trigger()
	c.JSON(http.StatusAccepted, gin.H{"triggered": true})
}

// handlePipelineRetry 把单篇 failed:* 文章倒回 pending，由下次 worker tick 推。
//
// 只接受 failed:* 状态的文章（Store.ResetPipelineToPending 校验）。
// 已被推到中间状态（fetched / md_exported / summarized）的不接受重置——
// 这些是"进行中"状态，重置会让 worker 跳过中间 stage。
//
// 与 trigger 的区别：trigger 让 worker 跑一次；retry 是单篇手动重置。
func handlePipelineRetry(c *gin.Context, svc *Service) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid article id"})
		return
	}

	if err := svc.Store().ResetPipelineToPending(c.Request.Context(), id); err != nil {
		// "not in failed state" 是 4xx（用户操作错误）；其他是 5xx。
		if strings.Contains(err.Error(), "not in failed state") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 顺手 trigger 一下让 worker 立刻看到，不用等下一个 tick（最多 5min）。
	if w := svc.Worker(); w != nil {
		w.Trigger()
	}

	c.JSON(http.StatusOK, gin.H{"id": id, "status": "pending"})
}
