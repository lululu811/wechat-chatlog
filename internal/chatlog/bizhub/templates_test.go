package bizhub

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// pageFiles 三个业务页面模板，顺序与导航一致。
//
// 四页收敛为三页（设计说明 §2）：/biz 与 /biz/feed 合并成双模式的「动态」，
// /biz/summary 改名「报告」。layout.html 是公共版式片段，不是页面，不在此列。
var pageFiles = []string{"biz.html", "reports.html", "admin.html"}

// tabKeys 导航 key -> 页面模板。与 layout.html 里的 {{template "top-nav" "xxx"}} 对应。
var tabKeys = map[string]string{
	"biz.html":     "biz",
	"reports.html": "reports",
	"admin.html":   "admin",
}

var (
	titleRe = regexp.MustCompile(`<title>(.*?)</title>`)
	// 只匹配静态 h1，排除页内 markdown 渲染器里的 `<h1>${inline(...)}</h1>`
	staticH1Re = regexp.MustCompile(`<h1>([^<$]+)</h1>`)
	// 页面引用公共导航：{{template "top-nav" "biz"}}
	navCallRe = regexp.MustCompile(`\{\{template "top-nav" "([a-z]+)"\}\}`)
	// 顶部导航项定义在 layout.html 里
	navTabRe = regexp.MustCompile(`data-tab="([a-z]+)"`)
)

func readTemplate(t *testing.T, name string) string {
	t.Helper()
	src, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		t.Fatalf("读取 %s: %v", name, err)
	}
	return string(src)
}

// TestPageTitlesAndHeadingsAreDistinct 锁定「一页一职责」的命名约束：
// 三个页面的 <title> 与 h1 必须互不相同。
// 历史问题是 biz.html 与 feed.html 的 <title> 完全一致、三个页面的 h1 都是「公众号汇总」，
// 导致用户无法从标签页与页面上分辨当前所在页面。
func TestPageTitlesAndHeadingsAreDistinct(t *testing.T) {
	titles := make(map[string]string, len(pageFiles))
	headings := make(map[string]string, len(pageFiles))

	for _, name := range pageFiles {
		src := readTemplate(t, name)

		tm := titleRe.FindStringSubmatch(src)
		if tm == nil {
			t.Fatalf("%s 未找到 <title>", name)
		}
		title := strings.TrimSpace(tm[1])
		if prev, dup := titles[title]; dup {
			t.Errorf("%s 与 %s 的 <title> 相同：%q", name, prev, title)
		}
		titles[title] = name

		hm := staticH1Re.FindStringSubmatch(src)
		if hm == nil {
			t.Fatalf("%s 未找到静态 <h1>", name)
		}
		h1 := strings.TrimSpace(hm[1])
		if prev, dup := headings[h1]; dup {
			t.Errorf("%s 与 %s 的 h1 相同：%q", name, prev, h1)
		}
		headings[h1] = name
	}
}

// TestEachPageHighlightsItsOwnTab 导航现在是公共片段（layout.html）+ 服务端渲染高亮。
//
// 改这一轮之前，四个页面各自硬编码一份 nav 并用一段 JS 按 pathname 回填 active ——
// 高亮状态和页面内容分两趟到达，会闪一下，而且改一处漏三处的风险很高。
// 现在每个页面只声明自己的 key，高亮由 layout.html 里的 if eq 决定。
func TestEachPageHighlightsItsOwnTab(t *testing.T) {
	layout := readTemplate(t, "layout.html")

	defined := map[string]bool{}
	for _, m := range navTabRe.FindAllStringSubmatch(layout, -1) {
		defined[m[1]] = true
	}
	for _, key := range tabKeys {
		if !defined[key] {
			t.Errorf("layout.html 的导航缺少 %q 入口", key)
		}
	}
	if !strings.Contains(layout, `{{if eq . "`) {
		t.Error("layout.html 的导航没有按 key 决定高亮 —— 这样每个页面又得自己填 active")
	}

	for _, name := range pageFiles {
		src := readTemplate(t, name)

		all := navCallRe.FindAllStringSubmatch(src, -1)
		if len(all) != 1 {
			t.Errorf("%s 期望恰好引用一次公共导航，实际 %d 次", name, len(all))
			continue
		}
		if got := all[0][1]; got != tabKeys[name] {
			t.Errorf("%s 声明的导航 key 是 %q，期望 %q", name, got, tabKeys[name])
		}

		// 页面里不该再有自己写的 nav 或回填 active 的脚本
		if strings.Contains(src, `class="top-tabs"`) {
			t.Errorf("%s 里还有一份自带导航 —— 应改为 {{template \"top-nav\" ...}}", name)
		}
		if strings.Contains(src, "classList.toggle('active', el.dataset.tab === tabKey)") {
			t.Errorf("%s 还在用脚本回填导航高亮 —— 高亮已由服务端渲染", name)
		}
	}
}

// bulkFns 管理页里的批量操作。批量动作一次影响多个账号 / 消耗额度，
// 按设计说明 §5「批量破坏性操作必须二次确认，文案写明影响范围与数量」。
//
// 归档相关的例外说明：归档任务改造成资源式之后，一次「开始归档」最多会跑
// 上千次真实抓取并写盘，取消会让一批没跑完 —— 三个动作都算破坏性操作，
// 都必须二次确认。批量导出原先是 batchExportMD，现已拆成这三个。
var bulkFns = []string{
	"batchSetVisibility", "batchSetWatch", "confirmAssignTags",
	"startExportJob", "retryExportJob", "cancelExportJob",
	"batchGenerateSummaries",
}

// TestAdminBulkActionsConfirm 批量操作必须二次确认。
//
// 这一条容易被顺手删掉：确认框在自动化里最烦，但它正是「一次点错影响 128 个账号」
// 和「多问一句」之间的差别。用测试钉住，避免以后重构时被当成噪音移除。
func TestAdminBulkActionsConfirm(t *testing.T) {
	src := readTemplate(t, "admin.html")

	for _, fn := range bulkFns {
		idx := strings.Index(src, "function "+fn+"(")
		if idx < 0 {
			t.Errorf("admin.html 里找不到 %s —— 本测试的清单需要同步更新", fn)
			continue
		}
		open := strings.Index(src[idx:], "{")
		if open < 0 {
			t.Errorf("%s 没有函数体", fn)
			continue
		}
		open += idx
		end := matchBrace(src, open)
		if end < 0 {
			t.Errorf("%s 的函数体括号不配对", fn)
			continue
		}
		if !strings.Contains(src[open:end], "confirm(") {
			t.Errorf("%s 是批量操作却没有二次确认 —— 应写出影响范围与数量", fn)
		}
	}
}

// adminSectionRe 匹配分区容器：<section class="admin-section" id="section-x" ...>
var (
	adminSectionRe = regexp.MustCompile(`class="admin-section" id="(section-[a-z]+)"([^>]*)>`)
	adminTabRe     = regexp.MustCompile(`<button[^>]*class="section-tab[^"]*"[^>]*>`)
	ariaControlsRe = regexp.MustCompile(`aria-controls="(section-[a-z]+)"`)
)

// TestAdminPageHasThreeSections 管理页必须是「顶部分区切换」而不是长条堆叠。
//
// 改之前账号表格 / 标签管理 / MD 归档 / 摘要生成四件事竖着排一长条：
// 滚到底才发现后面还有东西，批量导出的进度条也可能在视口外。
// 这里钉住三件事：恰好三个分区、首屏只显示一个、每个标签都指向真实分区。
func TestAdminPageHasThreeSections(t *testing.T) {
	src := readTemplate(t, "admin.html")

	sections := map[string]bool{} // id -> 是否 initial hidden
	for _, m := range adminSectionRe.FindAllStringSubmatch(src, -1) {
		if _, dup := sections[m[1]]; dup {
			t.Errorf("分区 id 重复：%s", m[1])
		}
		sections[m[1]] = strings.Contains(m[2], "hidden")
	}

	if len(sections) != 3 {
		t.Fatalf("管理页应有 3 个分区（账号 / 标签 / 批量任务），实际 %d 个：%v",
			len(sections), sortedStrKeys(sections))
	}

	tabs := adminTabRe.FindAllString(src, -1)
	if len(tabs) != 3 {
		t.Fatalf("管理页应有 3 个分区标签，实际 %d 个", len(tabs))
	}

	joined := strings.Join(tabs, "\n")
	visible := 0
	for id, hidden := range sections {
		if !hidden {
			visible++
		}
		if !strings.Contains(joined, `aria-controls="`+id+`"`) {
			t.Errorf("分区 %s 没有对应的分区标签（缺 aria-controls）", id)
		}
	}
	if visible != 1 {
		t.Errorf("首屏应恰好显示 1 个分区，实际 %d 个可见 —— 分区切换失效就退化成原来的长页面了", visible)
	}

	// 标签不能指向不存在的分区，否则点了没反应且不报错
	for _, tab := range tabs {
		m := ariaControlsRe.FindStringSubmatch(tab)
		if m == nil {
			t.Errorf("分区标签缺少 aria-controls：%s", strings.TrimSpace(tab))
			continue
		}
		if _, ok := sections[m[1]]; !ok {
			t.Errorf("分区标签指向不存在的分区 %s", m[1])
		}
	}
}

// TestAdminSectionStatePersistsInURL 当前分区要能从 URL 恢复。
//
// 与「动态」页的双模式同一套思路：刷新/分享/后退都停在原来的分区。
// 「我正在改标签，一刷新弹回账号列表」是这类后台页最容易丢的状态。
func TestAdminSectionStatePersistsInURL(t *testing.T) {
	src := readTemplate(t, "admin.html")

	for _, want := range []struct{ snippet, why string }{
		{`.get('section')`, "没有从 URL 读分区，刷新后无法恢复"},
		{`.set('section'`, "没有把分区写回 URL，分享/后退拿不到状态"},
		{`history.pushState`, "没有压历史，后退键回不到上一个分区"},
		{`addEventListener('popstate'`, "没有监听 popstate，后退时界面不会跟着变"},
	} {
		if !strings.Contains(src, want.snippet) {
			t.Errorf("%s（缺少 %q）", want.why, want.snippet)
		}
	}
}

// TestAdminTagDeleteShowsImpact 删除标签前必须说明影响范围（设计说明 §3.3）。
//
// 标签被删会从所有持有它的公众号上一并去掉，属于破坏性操作；
// 只说「确认删除？」而不说「正被 N 个公众号使用」等于没给判断依据。
func TestAdminTagDeleteShowsImpact(t *testing.T) {
	src := readTemplate(t, "admin.html")

	body, ok := funcBody(src, "renderTagList")
	if !ok {
		t.Fatal("admin.html 里找不到 renderTagList")
	}

	idx := strings.Index(body, "deletingTagId === t.id")
	if idx < 0 {
		t.Fatal("renderTagList 里找不到删除确认分支")
	}
	confirm := body[idx:]
	if len(confirm) > 900 {
		confirm = confirm[:900]
	}
	if !strings.Contains(confirm, "tagUsageCount(") {
		t.Error("删除标签的确认文案没有给出「该标签被 N 个公众号使用」—— 用户无法判断影响范围")
	}
}

// funcBody 取出顶层函数 fn 的花括号体内文本。
func funcBody(src, fn string) (string, bool) {
	idx := strings.Index(src, "function "+fn+"(")
	if idx < 0 {
		return "", false
	}
	open := strings.Index(src[idx:], "{")
	if open < 0 {
		return "", false
	}
	open += idx
	end := matchBrace(src, open)
	if end < 0 {
		return "", false
	}
	return src[open:end], true
}

func sortedStrKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPageTemplatesRender 用最小数据集渲染三个模板，确保数据绑定没有失效。
func TestPageTemplatesRender(t *testing.T) {
	base := map[string]any{
		"Now": "2026-09-21 20:00:00",
	}

	cases := []struct {
		name     string
		tmplName string
		data     map[string]any
		wantH1   string
	}{
		{
			name:     "动态",
			tmplName: "biz.html",
			data: map[string]any{
				"Accounts":        []Account{},
				"ArticleCount":    0,
				"Tags":            []Tag{},
				"TagsJSON":        "[]",
				"AccountTags":     map[string][]Tag{},
				"AccountTagsJSON": "{}",
				"Syncing":         false,
				"LastSync":        nil,
			},
			wantH1: "<h1>动态</h1>",
		},
		{
			name:     "报告",
			tmplName: "reports.html",
			data: map[string]any{
				"Tags":     []Tag{},
				"TagsJSON": "[]",
			},
			wantH1: "<h1>报告</h1>",
		},
		{
			name:     "管理",
			tmplName: "admin.html",
			data: map[string]any{
				"Tags":     []Tag{},
				"TagsJSON": "[]",
			},
			wantH1: "<h1>管理</h1>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := make(map[string]any, len(base)+len(tc.data))
			for k, v := range base {
				data[k] = v
			}
			for k, v := range tc.data {
				data[k] = v
			}

			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, tc.tmplName, data); err != nil {
				t.Fatalf("渲染 %s 失败: %v", tc.tmplName, err)
			}

			out := buf.String()
			if !strings.Contains(out, tc.wantH1) {
				t.Errorf("%s 输出缺少 %q", tc.tmplName, tc.wantH1)
			}
			if !strings.Contains(out, `href="/"`) {
				t.Errorf("%s 缺少返回 chatlog 原生页的入口", tc.tmplName)
			}
		})
	}
}
