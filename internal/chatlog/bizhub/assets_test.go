package bizhub

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 本文件锁定「公共静态资源层」这一结构约定：
//
//	internal/chatlog/bizhub/static/bizhub.css         跨页共享样式
//	internal/chatlog/bizhub/static/bizhub.js          跨页共享脚本（基础层）
//	internal/chatlog/bizhub/static/bizhub-report.js   报告渲染模块
//
// 背景：重构前四个页面各自内联了完整的样式与脚本，同样的按钮/导航/轻提示/
// 报告渲染器在 3-4 个文件里各写一遍。这些重复是后续所有改动的主要成本，
// 也是同一条规则在不同页面悄悄走样的根源。
//
// 分层理由：报告数据结构（structured/highlights/themes/mustReads/byAccount）
// 是这套接口里最不稳定的一块，单独成模块后，改报告只动一个文件。

const (
	cssPath    = "static/bizhub.css"
	jsPath     = "static/bizhub.js"
	reportPath = "static/bizhub-report.js"

	cssLinkTag      = `<link rel="stylesheet" href="/biz/static/bizhub.css">`
	baseScriptTag   = `<script src="/biz/static/bizhub.js"></script>`
	reportScriptTag = `<script src="/biz/static/bizhub-report.js"></script>`
)

// reportFns 是报告渲染模块对外暴露的全部函数名。
// 这个清单同时用于三处断言：模块确实导出了它、bizhub.js 不再声明它、
// 模板里没有残留的裸调用。任何一处漏改都会被测出来。
var reportFns = []string{
	"getStructured", "getHighlights", "getThemes", "getMustReads", "getByAccount",
	"hasStructured", "scoreNumber", "scoreStyle", "articleLabel",
	"renderHighlightsBlock", "renderSummaryBlock", "renderThemesBlock",
	"renderMustReadsBlock", "renderByAccountBlock", "renderStructuredBody",
	"renderFallbackBody", "renderMetaFooter", "reportCardHTML",
	"toggleThemeCard", "metaSnippet",
}

// pagesUsingReport 哪些页面需要报告渲染模块。admin 只做账号/标签/批量导出，
// 不渲染报告，所以不引入 —— 少下载一个文件。
//
// biz.html 引入它只为一件事：生成汇总后校验返回体到底是不是结构化报告
// （BizHub.Report.hasStructured），正文本身不在这页渲染（设计说明 §3.2）。
var pagesUsingReport = map[string]bool{
	"biz.html": true, "reports.html": true, "admin.html": false,
}

// staticScripts 公共脚本层里所有会发请求的文件。裸 fetch 守卫要连它们一起扫 ——
// 只扫模板会漏掉这一层（bizhub.js 里的导出/摘要/同步就曾经是裸 fetch）。
var staticScripts = []string{"static/bizhub.js", "static/bizhub-report.js"}

// TestCommonAssetsAreEmbedded 公共资源必须真的被打进二进制。
func TestCommonAssetsAreEmbedded(t *testing.T) {
	css, err := fs.ReadFile(staticFS, cssPath)
	if err != nil {
		t.Fatalf("读取 %s: %v", cssPath, err)
	}
	if len(css) < 5000 {
		t.Errorf("%s 只有 %d 字节，疑似被清空", cssPath, len(css))
	}
	for _, want := range []string{":root", ".top-tabs", ".toast", "@keyframes"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("%s 缺少预期内容 %q", cssPath, want)
		}
	}

	js, err := fs.ReadFile(staticFS, jsPath)
	if err != nil {
		t.Fatalf("读取 %s: %v", jsPath, err)
	}
	if len(js) < 5000 {
		t.Errorf("%s 只有 %d 字节，疑似被清空", jsPath, len(js))
	}
	for _, want := range []string{"function toast(", "function formatDate(", "const SYNC_DOTS"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("%s 缺少预期内容 %q", jsPath, want)
		}
	}

	report, err := fs.ReadFile(staticFS, reportPath)
	if err != nil {
		t.Fatalf("读取 %s: %v", reportPath, err)
	}
	if len(report) < 5000 {
		t.Errorf("%s 只有 %d 字节，疑似被清空", reportPath, len(report))
	}
	for _, want := range []string{"global.BizHub.Report = {", "function reportCardHTML(", "function renderStructuredBody("} {
		if !strings.Contains(string(report), want) {
			t.Errorf("%s 缺少预期内容 %q", reportPath, want)
		}
	}

	// 模板里不应再出现这些跨页共享的规则正文（说明抽取彻底）
	src, err := templateFS.ReadFile("templates/biz.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), ".toast-container {") {
		t.Errorf("biz.html 仍内联着已抽到 %s 的规则", cssPath)
	}
}

// TestReportModuleExposesAllMovedFunctions 报告模块必须导出全部搬移过来的函数，
// 且基础层不能再声明同名函数 —— 防止「搬了一半」这种最难查的状态：
// 页面调 BizHub.Report.xxx 拿到 undefined，报错信息指向调用点而非模块。
func TestReportModuleExposesAllMovedFunctions(t *testing.T) {
	report, err := fs.ReadFile(staticFS, reportPath)
	if err != nil {
		t.Fatal(err)
	}
	rs := string(report)
	base, err := fs.ReadFile(staticFS, jsPath)
	if err != nil {
		t.Fatal(err)
	}
	bs := string(base)

	for _, fn := range reportFns {
		if !strings.Contains(rs, "function "+fn+"(") {
			t.Errorf("%s 没有定义 %s", reportPath, fn)
		}
		if !strings.Contains(rs, fn+": "+fn) {
			t.Errorf("%s 的导出对象里缺少 %s", reportPath, fn)
		}
		if strings.Contains(bs, "function "+fn+"(") {
			t.Errorf("%s 仍声明着 %s —— 它已经属于 %s，重复声明会让页面拿到哪个版本变得不确定",
				jsPath, fn, reportPath)
		}
	}

	// 模块必须只依赖基础层的公开工具，不能反过来依赖页面私有实现之外的东西。
	// 这里只把「实际用到的跨层名字」列出来，改错方向时至少能看见是什么在依赖谁。
	for _, dep := range []string{"esc(", "formatDate(", "formatDateTime(", "rangeLabel(", "renderMarkdown("} {
		if !strings.Contains(rs, dep) {
			t.Logf("提示：%s 里不再出现 %s，若已不再需要该依赖可精简注释", reportPath, dep)
		}
	}
}

// TestAllBizAPICallsGoThroughAPIFetch 业务接口调用必须走公共的 apiFetch。
//
// 背景：页面是靠 `?token=` 打开的，而浏览器不会把页面 URL 的 query 带到子请求上。
// 直接 fetch('/api/v1/biz/...') 在配了 auth token 的部署下会全部 401 ——
// 症状是页面框架能打开、内容永远是空的，很难第一眼看出是鉴权问题。
// 统一走 apiFetch 后由它补 token，这条测试防止以后又冒出裸 fetch。
func TestAllBizAPICallsGoThroughAPIFetch(t *testing.T) {
	// fetch( 后面紧跟一个指向 /api/v1/biz/ 的字符串或模板串字面量
	bareRe := regexp.MustCompile("\\bfetch\\(\\s*[`'\"]/?api/v1/biz/")

	for _, name := range pageFiles {
		src, err := templateFS.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range bareRe.FindAllIndex(src, -1) {
			line := bytes.Count(src[:m[0]], []byte("\n")) + 1
			t.Errorf("%s:%d 直接 fetch 了业务接口 —— 应改用 apiFetch(...)，否则漏带 token",
				name, line)
		}
	}

	// 公共脚本层同样必须走 apiFetch：它是被所有页面共享的一层，
	// 漏一个就等于所有页面在同一处漏带 token。
	for _, name := range staticScripts {
		src, err := fs.ReadFile(staticFS, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range bareRe.FindAllIndex(src, -1) {
			line := bytes.Count(src[:m[0]], []byte("\n")) + 1
			t.Errorf("%s:%d 直接 fetch 了业务接口 —— 应改用 apiFetch(...)，否则漏带 token",
				name, line)
		}
	}

	// 公共层必须真的提供 apiFetch 与给动态链接用的 pageHref
	js, err := fs.ReadFile(staticFS, jsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"function apiFetch(", "function authToken(", "function pageHref(", "propagateAuthToken()"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("%s 缺少 %q —— token 修复依赖它", jsPath, want)
		}
	}
}

// TestNoBareReportCallsInTemplates 模板里不能残留裸调用。
// 报告函数搬进模块后就不再挂全局，裸调用会在运行时抛 ReferenceError ——
// 而这种错误在「只看某一页」时很容易漏掉。
func TestNoBareReportCallsInTemplates(t *testing.T) {
	nameRe := make([]string, 0, len(reportFns))
	for _, fn := range reportFns {
		nameRe = append(nameRe, regexp.QuoteMeta(fn))
	}
	// 前面不是标识符字符、也不是 . 或 $ —— 这样 BizHub.Report.fn( 不会被误判
	bareRe := regexp.MustCompile(`(?:^|[^\w$.])(` + strings.Join(nameRe, "|") + `)\s*\(`)

	for name := range pagesUsingReport {
		src, err := templateFS.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)

		if hits := bareRe.FindAllStringSubmatch(s, -1); len(hits) > 0 {
			seen := map[string]bool{}
			for _, h := range hits {
				if seen[h[1]] {
					continue
				}
				seen[h[1]] = true
				t.Errorf("%s 里出现报告函数的裸调用 %s( —— 应写作 BizHub.Report.%s(...)",
					name, h[1], h[1])
			}
		}
	}

	// 用到了报告函数的页面必须引入模块，没用到的页面不该引入（白下载一个文件）
	for name, needReport := range pagesUsingReport {
		src, err := templateFS.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		hasCall := strings.Contains(s, "BizHub.Report.")
		hasTag := strings.Contains(s, reportScriptTag)
		if hasCall && !hasTag {
			t.Errorf("%s 调用了 BizHub.Report.* 却没有引入 %s", name, reportPath)
		}
		if needReport != hasTag {
			t.Errorf("%s 引入报告模块 = %v，但期望 %v（%s）", name, hasTag, needReport, reportPath)
		}
	}
}

// TestStaticRouteServesAssets 静态路由必须能取到两个文件。
func TestStaticRouteServesAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterStatic(r, "/biz/static")

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/biz/static/bizhub.css", ":root"},
		{"/biz/static/bizhub.js", "function toast"},
		{"/biz/static/bizhub-report.js", "BizHub.Report"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tc.path, w.Code)
			continue
		}
		if !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("GET %s 的响应里没有 %q", tc.path, tc.want)
		}
	}

	// 目录本身不给内容
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/biz/static/", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /biz/static/ = %d, want 404", w.Code)
	}
}

// TestPagesLoadCommonAssetsWithCorrectOrder 每个页面都要引入公共资源，
// 且顺序必须是「公共样式表 → 内联样式」「公共脚本 → 内联脚本」。
//
// 顺序不能反：公共样式表必须先加载，页面内联规则才能就近覆盖它；
// 公共脚本必须先加载，页面才能在同一轮解析里直接调用这些函数。
//
// 样式表这一环现在由 layout.html 的 doc-head 片段承载，所以断言改成：
// layout.html 里确实有公共样式表，每个页面在 <style> 之前引用了 doc-head。
func TestPagesLoadCommonAssetsWithCorrectOrder(t *testing.T) {
	layout, err := templateFS.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(layout), cssLinkTag) {
		t.Errorf("layout.html 里没有公共样式表 %q", cssLinkTag)
	}

	for _, name := range pageFiles {
		src, err := templateFS.ReadFile("templates/" + name)
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		s := string(src)

		headAt := strings.Index(s, `{{template "doc-head"}}`)
		styleAt := strings.Index(s, "<style>")
		switch {
		case headAt < 0 && !strings.Contains(s, cssLinkTag):
			t.Errorf("%s 既没引用 doc-head，也没有公共样式表", name)
		case headAt >= 0 && (styleAt < 0 || headAt > styleAt):
			t.Errorf("%s 的 doc-head 必须出现在内联 <style> 之前", name)
		}

		scriptAt := strings.Index(s, baseScriptTag)
		inlineAt := strings.Index(s, "<script>")
		if scriptAt < 0 {
			t.Errorf("%s 没有引入公共脚本", name)
		} else if inlineAt < 0 || scriptAt > inlineAt {
			t.Errorf("%s 的公共脚本必须出现在内联 <script> 之前", name)
		}

		// 报告模块必须夹在「基础层」与「页面内联脚本」之间：
		// 它自己要用基础层的 esc/formatDate，页面的内联脚本又要用它的 BizHub.Report。
		if reportAt := strings.Index(s, reportScriptTag); reportAt >= 0 {
			if reportAt < scriptAt {
				t.Errorf("%s 的报告模块必须排在基础层之后（它依赖 esc/formatDate/rangeLabel/renderMarkdown）", name)
			}
			if inlineAt < 0 || reportAt > inlineAt {
				t.Errorf("%s 的报告模块必须出现在内联 <script> 之前", name)
			}
		}
	}
}

// ---------------------------------------------------------------- 重复度守卫

// cssDupDebt / jsDupDebt 是重构后【仍然存在】的跨页重复清单。
//
// 这一轮（合并 /biz 与 /biz/feed 为双模式单页、抽 layout.html）已经把它从
// CSS 47 项 / JS 13 项压到 CSS 14 项 / JS 2 项 —— 因为四页里最重的重复
// （整段文章流渲染、导航、报告页控件）都是「同一能力在两个页面里各写一遍」，
// 页面合并后这些重复直接消失，而不是被搬进公共层。
//
// 剩下这些不是抄写走样，而是各页面的组件规格确实不同：典型例子是 admin 的 .btn
// 没有 display:inline-flex，因此不能把三页版本搬到公共层，否则会真实改变 admin 的渲染。
// 要消除它们得先统一组件规格（属设计决策），而不是抽文件。
//
// 这个清单只允许缩短，不允许变长：新增跨页重复会让测试失败，提醒先抽公共层。
var cssDupDebt = []string{
	".account-name",
	".btn", ".btn-accent", ".btn-ghost", ".btn-sm", ".btn:disabled",
	".empty-state",
	".header-right",
	".instruction-hint",
	".layout",
	".main-card",
	".search-box",
	".seg-control button",
	".sk-line",
}

var jsDupDebt = []string{
	"esc", "generateSummary",
}

var (
	styleTagRe  = regexp.MustCompile(`(?s)<style>(.*?)</style>`)
	scriptTagRe = regexp.MustCompile(`(?s)<script>(.*?)</script>`)
	commentRe   = regexp.MustCompile(`(?s)/\*.*?\*/`)
	atBlockRe   = regexp.MustCompile(`@(?:media|supports|layer)\b[^{]*\{`)
	keyframesRe = regexp.MustCompile(`@keyframes\s+[\w-]+`)
	funcRe      = regexp.MustCompile(`(?m)^\s*(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(`)
)

// TestCrossPageDuplicationDoesNotGrow 跨页重复只减不增。
func TestCrossPageDuplicationDoesNotGrow(t *testing.T) {
	selSets := map[string]map[string]bool{}
	fnSets := map[string]map[string]bool{}

	for _, name := range pageFiles {
		src, err := templateFS.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		selSets[name] = map[string]bool{}
		for _, m := range styleTagRe.FindAllStringSubmatch(s, -1) {
			for k := range inlineSelectors(m[1]) {
				selSets[name][k] = true
			}
		}
		fnSets[name] = map[string]bool{}
		for _, m := range scriptTagRe.FindAllStringSubmatch(s, -1) {
			for _, fm := range funcRe.FindAllStringSubmatch(m[1], -1) {
				fnSets[name][fm[1]] = true
			}
		}
	}

	cssDup := crossPageDup(selSets, func(k string) bool {
		// @keyframes 会合法地在多页出现过（其实已抽到公共层，这里兜底）
		return keyframesRe.MatchString(k)
	})
	jsDup := crossPageDup(fnSets, nil)

	assertNoNewDup(t, "CSS 选择器", cssDup, cssDupDebt)
	assertNoNewDup(t, "JS 函数", jsDup, jsDupDebt)
}

func assertNoNewDup(t *testing.T, what string, got, debt []string) {
	t.Helper()
	debtSet := make(map[string]bool, len(debt))
	for _, d := range debt {
		debtSet[d] = true
	}
	var added []string
	for _, g := range got {
		if !debtSet[g] {
			added = append(added, g)
		}
	}
	if len(added) > 0 {
		t.Errorf("出现新的跨页重复 %s：%v\n"+
			"请把跨页共用的部分抽到 internal/chatlog/bizhub/static/ 下的公共资源，\n"+
			"而不是在第二个页面里再写一遍。若确实是页面专属、只是恰好同名，请更新债务清单并说明原因。",
			what, added)
	}
	if len(debt) > len(got) {
		t.Logf("提示：%s 的跨页重复债务已从 %d 项降到 %d 项，可以收紧 cssDupDebt/jsDupDebt 了",
			what, len(debt), len(got))
	}
}

func crossPageDup(sets map[string]map[string]bool, skip func(string) bool) []string {
	count := map[string]int{}
	for _, m := range sets {
		for k := range m {
			count[k]++
		}
	}
	var out []string
	for k, n := range count {
		if n < 2 {
			continue
		}
		if skip != nil && skip(k) {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// inlineSelectors 提取一段 CSS 里的选择器：@media/@supports 递归展开，
// @keyframes 整体跳过（动画名不参与选择器冲突），其它原样保留。
func inlineSelectors(css string) map[string]bool {
	out := map[string]bool{}
	css = commentRe.ReplaceAllString(css, "")
	i := 0
	for i < len(css) {
		j := strings.Index(css[i:], "{")
		if j < 0 {
			break
		}
		j += i
		sel := strings.TrimSpace(css[i:j])
		end := matchBrace(css, j)
		if end < 0 {
			break
		}
		switch {
		case strings.HasPrefix(sel, "@keyframes"), strings.HasPrefix(sel, "@font-face"):
			// 忽略
		case atBlockRe.MatchString(sel + "{"):
			for k := range inlineSelectors(css[j+1 : end]) {
				out[k] = true
			}
		case strings.HasPrefix(sel, "@"):
			// 其它 at-rule，忽略
		default:
			if s := normalizeSel(sel); s != "" {
				out[s] = true
			}
		}
		i = end + 1
	}
	return out
}

func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// normalizeSel 只做空白归一，保留伪类/伪元素 —— :hover 与 :focus 变体是彼此
// 独立的规则，参与层叠，不能当成同一条。
func normalizeSel(sel string) string {
	sel = strings.Join(strings.Fields(sel), " ")
	return strings.TrimSpace(sel)
}
