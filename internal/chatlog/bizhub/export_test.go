package bizhub

import (
	"errors"
	"testing"
	"time"

	"github.com/chenliitaz/chatlog/internal/chatlog/bizhub/mdexport"
	"github.com/chenliitaz/chatlog/internal/model"
)

const (
	urlA1 = "https://mp.weixin.qq.com/s/a1"
	urlA2 = "https://mp.weixin.qq.com/s/a2"
	urlB1 = "https://mp.weixin.qq.com/s/b1"
)

// seedExportStore 建一个含 2 个公众号的 store：甲号可见、乙号已隐藏，各带文章。
//
// 返回 url -> articleID 的映射而不是按序号取 ID：候选查询按 published_at 排序，
// 同一秒插入的多篇文章顺序不确定，用 URL 定位才不会写出偶发失败的测试。
func seedExportStore(t *testing.T) (*Store, map[string]int64) {
	t.Helper()

	s := newTestStore(t)
	if err := s.UpsertAccounts([]Account{
		{GHID: "gh_1", GHName: "甲号"},
		{GHID: "gh_2", GHName: "乙号"},
	}); err != nil {
		t.Fatalf("UpsertAccounts: %v", err)
	}
	if err := s.SetAccountsHidden([]string{"gh_2"}, true); err != nil {
		t.Fatalf("SetAccountsHidden: %v", err)
	}

	base := time.Now().Add(-time.Hour)
	msgs := []*model.BizMessage{
		{GHID: "gh_1", GHName: "甲号", Time: base, Title: "甲1", URL: urlA1},
		{GHID: "gh_1", GHName: "甲号", Time: base.Add(time.Minute), Title: "甲2", URL: urlA2},
		{GHID: "gh_2", GHName: "乙号", Time: base.Add(2 * time.Minute), Title: "乙1", URL: urlB1},
	}
	if _, err := s.UpsertArticles(msgs); err != nil {
		t.Fatalf("UpsertArticles: %v", err)
	}

	articles, err := s.GetUnexportedArticles(30, 100)
	if err != nil {
		t.Fatalf("GetUnexportedArticles: %v", err)
	}
	byURL := make(map[string]int64, len(articles))
	for _, a := range articles {
		byURL[a.URL] = a.ID
	}
	if _, ok := byURL[urlA1]; !ok {
		t.Fatalf("种子数据异常：找不到 %s", urlA1)
	}
	if _, ok := byURL[urlA2]; !ok {
		t.Fatalf("种子数据异常：找不到 %s", urlA2)
	}
	return s, byURL
}

func containsArticleID(articles []Article, id int64) bool {
	for _, a := range articles {
		if a.ID == id {
			return true
		}
	}
	return false
}

// TestExportCandidateIncludesFailedArticle 归档失败过的文章必须回到候选集。
//
// 这是本轮迭代修掉的静默数据丢失。旧谓词写的是 `e.id IS NULL`（没有任何导出记录），
// 而导出失败也会写一条 status='failed' 的记录 —— 于是一篇文章只要失败过一次，
// 就永远不再出现在候选集里，也永远不出现在 pending 计数里。数字完全对得上，
// 文件就是少，用户没有任何线索能发现。
func TestExportCandidateIncludesFailedArticle(t *testing.T) {
	s, byURL := seedExportStore(t)
	id := byURL[urlA1]

	if err := s.UpsertExportRecord(id, urlA1, "", "", ExportStatusFailed,
		string(mdexport.KindTimeout), "抓取超时"); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}

	cands, err := s.GetUnexportedArticles(30, 100)
	if err != nil {
		t.Fatalf("GetUnexportedArticles: %v", err)
	}
	if !containsArticleID(cands, id) {
		t.Fatal("失败过的文章没有回到候选集 —— 它会被永久放弃，这就是静默数据丢失")
	}

	stats, err := s.GetExportStats(30)
	if err != nil {
		t.Fatalf("GetExportStats: %v", err)
	}
	if stats.Pending != len(cands) {
		t.Errorf("待归档 %d 篇，但候选查询返回 %d 篇 —— 两条查询过滤条件不一致",
			stats.Pending, len(cands))
	}
	if stats.Exported != 0 {
		t.Errorf("failed 不该被算作已归档，实际 exported = %d", stats.Exported)
	}
}

// TestExportAttemptsLifecycle 重试计数：失败累加、成功归零、补摘要不计。
//
// 归零这条容易被忽略但很重要：如果成功后 attempts 继续累积，一篇曾经失败过
// 三次、后来成功的文章，下次再遇到一次偶发超时就会立刻撞上重试上限，
// 被判定为「永久失败」—— 用户看到一篇明明能归档的文章怎么点都不动。
func TestExportAttemptsLifecycle(t *testing.T) {
	s, byURL := seedExportStore(t)
	id := byURL[urlA1]

	for i := 1; i <= maxExportAttempts; i++ {
		if err := s.UpsertExportRecord(id, urlA1, "", "", ExportStatusFailed,
			string(mdexport.KindTimeout), "抓取超时"); err != nil {
			t.Fatalf("第 %d 次 UpsertExportRecord: %v", i, err)
		}
		rec, err := s.GetExportRecord(id)
		if err != nil {
			t.Fatalf("GetExportRecord: %v", err)
		}
		if rec == nil {
			t.Fatal("失败记录应已落库")
		}
		if rec.Attempts != i {
			t.Errorf("第 %d 次失败后 attempts = %d，期望 %d", i, rec.Attempts, i)
		}
		if rec.ExportedAt != 0 {
			t.Errorf("失败不该推进 exported_at，实际 %d", rec.ExportedAt)
		}
	}
	if err := s.UpsertExportRecord(id, urlA1, "", "", ExportStatusFailed,
		string(mdexport.KindCaptcha), "触发验证码"); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}

	// 补摘要不增加归档尝试次数
	if err := s.UpsertExportRecord(id, urlA1, "a1.md", "a1.summary.md",
		ExportStatusSummarized, "", ""); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}
	rec, err := s.GetExportRecord(id)
	if err != nil {
		t.Fatalf("GetExportRecord: %v", err)
	}
	if rec.Attempts != maxExportAttempts+1 {
		t.Errorf("补摘要后 attempts = %d，期望仍为 %d", rec.Attempts, maxExportAttempts+1)
	}
	if rec.MDPath != "a1.md" {
		t.Errorf("md_path = %q，期望 a1.md", rec.MDPath)
	}

	// 成功归零并清空失败信息
	if err := s.UpsertExportRecord(id, urlA1, "a1.md", "", ExportStatusExported, "", ""); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}
	rec, err = s.GetExportRecord(id)
	if err != nil {
		t.Fatalf("GetExportRecord: %v", err)
	}
	if rec.Attempts != 0 {
		t.Errorf("归档成功后 attempts = %d，期望归零", rec.Attempts)
	}
	if rec.Kind != "" {
		t.Errorf("归档成功后 kind 应清空，实际 %q", rec.Kind)
	}
	if rec.Error != "" {
		t.Errorf("归档成功后 error 应清空，实际 %q", rec.Error)
	}
	if rec.ExportedAt == 0 {
		t.Error("归档成功后 exported_at 应被写入")
	}
}

// TestExportFailureKeepsExistingPath 后续失败不得抹掉已有的 md_path / summary_path。
func TestExportFailureKeepsExistingPath(t *testing.T) {
	s, byURL := seedExportStore(t)
	id := byURL[urlA1]

	if err := s.UpsertExportRecord(id, urlA1, "a1.md", "a1.summary.md",
		ExportStatusSummarized, "", ""); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}
	if err := s.UpsertExportRecord(id, urlA1, "", "", ExportStatusFailed,
		string(mdexport.KindTimeout), "重新归档时超时"); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}

	rec, err := s.GetExportRecord(id)
	if err != nil {
		t.Fatalf("GetExportRecord: %v", err)
	}
	if rec.MDPath != "a1.md" {
		t.Errorf("md_path = %q，失败不该把它抹成空串", rec.MDPath)
	}
	if rec.SummaryPath != "a1.summary.md" {
		t.Errorf("summary_path = %q，失败不该把它抹成空串", rec.SummaryPath)
	}
}

// TestExportStatsBlocked 「需人工处理」的篇数要能算出来。
//
// 为什么单独统计：这些文章仍然算作候选（所以 pending 会包含它们），
// 但批量归档不会再自动跑。如果不把这个数露出来，用户看到 pending=5
// 点一下跑完只剩 2 篇成功，会以为功能坏了。
func TestExportStatsBlocked(t *testing.T) {
	t.Run("需人工处理的失败一次就卡住", func(t *testing.T) {
		s, byURL := seedExportStore(t)
		id := byURL[urlA1]
		if err := s.UpsertExportRecord(id, urlA1, "", "", ExportStatusFailed,
			string(mdexport.KindCaptcha), "触发验证码"); err != nil {
			t.Fatalf("UpsertExportRecord: %v", err)
		}

		stats, err := s.GetExportStats(30)
		if err != nil {
			t.Fatalf("GetExportStats: %v", err)
		}
		if stats.Blocked != 1 {
			t.Errorf("blocked = %d，期望 1", stats.Blocked)
		}
		if stats.Pending != 2 {
			t.Errorf("pending = %d，期望 2（blocked 仍属于候选）", stats.Pending)
		}
	})

	t.Run("重试次数用尽也算卡住", func(t *testing.T) {
		s, byURL := seedExportStore(t)
		id := byURL[urlA1]
		for i := 0; i < maxExportAttempts; i++ {
			if err := s.UpsertExportRecord(id, urlA1, "", "", ExportStatusFailed,
				string(mdexport.KindTimeout), "抓取超时"); err != nil {
				t.Fatalf("UpsertExportRecord: %v", err)
			}
		}

		stats, err := s.GetExportStats(30)
		if err != nil {
			t.Fatalf("GetExportStats: %v", err)
		}
		if stats.Blocked != 1 {
			t.Errorf("blocked = %d，期望 1（尝试 %d 次已到上限）", stats.Blocked, maxExportAttempts)
		}
	})

	t.Run("可重试的失败不算卡住", func(t *testing.T) {
		s, byURL := seedExportStore(t)
		id := byURL[urlA1]
		if err := s.UpsertExportRecord(id, urlA1, "", "", ExportStatusFailed,
			string(mdexport.KindTimeout), "抓取超时"); err != nil {
			t.Fatalf("UpsertExportRecord: %v", err)
		}
		stats, err := s.GetExportStats(30)
		if err != nil {
			t.Fatalf("GetExportStats: %v", err)
		}
		if stats.Blocked != 0 {
			t.Errorf("blocked = %d，期望 0（超时只是第一次，还有重试机会）", stats.Blocked)
		}
	})
}

// TestGetExportStatsRespectsWindow 统计窗口必须跟随入参，不能写死 30 天。
//
// 旧实现把窗口硬编码成 30 天，而管理页下拉框可选 7/30/90/180 ——
// 用户选「近 90 天」拿到的数字和「近 30 天」完全一样，看起来像下拉框没生效。
func TestGetExportStatsRespectsWindow(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertAccounts([]Account{{GHID: "gh_1", GHName: "甲号"}}); err != nil {
		t.Fatalf("UpsertAccounts: %v", err)
	}

	now := time.Now()
	msgs := []*model.BizMessage{
		{GHID: "gh_1", GHName: "甲号", Time: now.AddDate(0, 0, -10), Title: "近", URL: "https://mp.weixin.qq.com/s/recent"},
		{GHID: "gh_1", GHName: "甲号", Time: now.AddDate(0, 0, -60), Title: "远", URL: "https://mp.weixin.qq.com/s/old"},
	}
	if _, err := s.UpsertArticles(msgs); err != nil {
		t.Fatalf("UpsertArticles: %v", err)
	}

	narrow, err := s.GetExportStats(30)
	if err != nil {
		t.Fatalf("GetExportStats(30): %v", err)
	}
	if narrow.Pending != 1 {
		t.Errorf("近 30 天待归档 = %d，期望 1", narrow.Pending)
	}
	if narrow.Days != 30 {
		t.Errorf("Days = %d，期望 30", narrow.Days)
	}

	wide, err := s.GetExportStats(90)
	if err != nil {
		t.Fatalf("GetExportStats(90): %v", err)
	}
	if wide.Pending != 2 {
		t.Errorf("近 90 天待归档 = %d，期望 2（写死 30 天的话这里会还是 1）", wide.Pending)
	}
}

// TestExportJobLifecycle 任务状态机与进度计数。
func TestExportJobLifecycle(t *testing.T) {
	s, byURL := seedExportStore(t)
	ids := make([]int64, 0, len(byURL))
	for _, id := range byURL {
		ids = append(ids, id)
	}

	job := &ExportJob{
		ID: "job_test", Status: ExportJobRunning,
		Days: 30, MaxItems: 100, Concurrency: 3, Total: len(ids),
	}
	items := make([]ExportJobItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, ExportJobItem{JobID: "job_test", ArticleID: id, Title: "t", Status: ExportItemPending})
	}
	if err := s.CreateExportJob(job, items); err != nil {
		t.Fatalf("CreateExportJob: %v", err)
	}

	got, err := s.GetExportJob("job_test")
	if err != nil {
		t.Fatalf("GetExportJob: %v", err)
	}
	if got == nil {
		t.Fatal("任务应已落库")
	}
	if got.Pending != len(ids) {
		t.Errorf("pending = %d，期望 %d", got.Pending, len(ids))
	}
	if got.Done() {
		t.Error("running 的任务不该被判定为完成")
	}
	if p := got.Percent(); p != 0 {
		t.Errorf("percent = %d，期望 0", p)
	}

	// 一篇成功、一篇失败
	if err := s.UpdateExportJobItem(ExportJobItem{
		JobID: "job_test", ArticleID: ids[0], Status: ExportItemDone, MDPath: "a.md",
	}); err != nil {
		t.Fatalf("UpdateExportJobItem: %v", err)
	}
	if err := s.UpdateExportJobItem(ExportJobItem{
		JobID: "job_test", ArticleID: ids[1], Status: ExportItemFailed,
		Kind: string(mdexport.KindTimeout), Error: "抓取超时",
	}); err != nil {
		t.Fatalf("UpdateExportJobItem: %v", err)
	}

	got, err = s.GetExportJob("job_test")
	if err != nil {
		t.Fatalf("GetExportJob: %v", err)
	}
	if got.Succeeded != 1 || got.Failed != 1 {
		t.Errorf("成功 %d 篇、失败 %d 篇，期望各 1", got.Succeeded, got.Failed)
	}
	if got.Processed() != 2 {
		t.Errorf("processed = %d，期望 2", got.Processed())
	}
	if got.Percent() != 100 {
		t.Errorf("percent = %d，期望 100", got.Percent())
	}

	// 重试只把没成功的退回 pending
	n, err := s.RequeueExportJobUnfinished("job_test")
	if err != nil {
		t.Fatalf("RequeueExportJobUnfinished: %v", err)
	}
	if n != 1 {
		t.Errorf("退回 %d 条，期望 1（只有失败那条）", n)
	}
	got, err = s.GetExportJob("job_test")
	if err != nil {
		t.Fatalf("GetExportJob: %v", err)
	}
	if got.Pending != 1 || got.Succeeded != 1 {
		t.Errorf("重试后 pending=%d succeeded=%d，期望 1/1", got.Pending, got.Succeeded)
	}
	if got.Failed != 0 {
		t.Errorf("重试后 failed = %d，期望 0", got.Failed)
	}
}

// TestExportJobPercentEmpty 空任务（没有候选）不应算出 NaN 或用 0 卡在进度条上。
func TestExportJobPercentEmpty(t *testing.T) {
	job := &ExportJob{Total: 0}
	if p := job.Percent(); p != 100 {
		t.Errorf("空任务 percent = %d，期望 100", p)
	}
}

// TestMarkInterruptedExportJobs 重启修复：running 任务标中断、running 条目退回 pending。
//
// 这是「断点续跑」的全部实现 —— 不需要额外的检查点机制，因为条目状态本身就是检查点。
// 如果这一步缺失，进程被杀之后会留下永远 running 的僵尸任务，
// 前端一进管理页就轮到它、进度条永远转不完。
func TestMarkInterruptedExportJobs(t *testing.T) {
	s, byURL := seedExportStore(t)
	ids := make([]int64, 0, len(byURL))
	for _, id := range byURL {
		ids = append(ids, id)
	}

	job := &ExportJob{ID: "job_running", Status: ExportJobRunning, Total: len(ids)}
	items := make([]ExportJobItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, ExportJobItem{JobID: "job_running", ArticleID: id, Status: ExportItemPending})
	}
	if err := s.CreateExportJob(job, items); err != nil {
		t.Fatalf("CreateExportJob: %v", err)
	}

	// 模拟进程被杀：有一条正在跑
	if err := s.UpdateExportJobItem(ExportJobItem{
		JobID: "job_running", ArticleID: ids[0], Status: ExportItemRunning,
	}); err != nil {
		t.Fatalf("UpdateExportJobItem: %v", err)
	}

	interrupted, err := s.MarkInterruptedExportJobs()
	if err != nil {
		t.Fatalf("MarkInterruptedExportJobs: %v", err)
	}
	if len(interrupted) != 1 || interrupted[0] != "job_running" {
		t.Errorf("返回的中断任务 = %v，期望 [job_running]", interrupted)
	}

	got, err := s.GetExportJob("job_running")
	if err != nil {
		t.Fatalf("GetExportJob: %v", err)
	}
	if got.Status != ExportJobInterrupted {
		t.Errorf("任务状态 = %q，期望 %q", got.Status, ExportJobInterrupted)
	}
	if got.Pending != len(ids) {
		t.Errorf("running 条目没有退回 pending：pending = %d，期望 %d", got.Pending, len(ids))
	}
	if got.Done() == false {
		t.Error("interrupted 的任务应被判定为已结束（否则前端会一直等它）")
	}

	// 幂等：再调一次不应重复计入
	again, err := s.MarkInterruptedExportJobs()
	if err != nil {
		t.Fatalf("MarkInterruptedExportJobs 第二次: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("第二次调用返回 %v，期望空（已无 running 任务）", again)
	}
}

// TestBuildExportPlanSkipsHopeless 计划阶段就把「跑也没用」的条目摘出来，但要以
// skipped 状态露面而不是消失。
func TestBuildExportPlanSkipsHopeless(t *testing.T) {
	s, byURL := seedExportStore(t)

	// 甲1：验证码失败一次 —— 需人工处理，计划里应为 skipped
	if err := s.UpsertExportRecord(byURL[urlA1], urlA1, "", "", ExportStatusFailed,
		string(mdexport.KindCaptcha), "触发验证码"); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}
	// 甲2：可重试的失败但次数已用尽 —— 计划里也应为 skipped
	for i := 0; i < maxExportAttempts; i++ {
		if err := s.UpsertExportRecord(byURL[urlA2], urlA2, "", "", ExportStatusFailed,
			string(mdexport.KindTimeout), "抓取超时"); err != nil {
			t.Fatalf("UpsertExportRecord: %v", err)
		}
	}

	svc := &Service{store: s}
	plan, err := svc.buildExportPlan(ExportBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("buildExportPlan: %v", err)
	}
	if len(plan) != 2 {
		t.Fatalf("计划条目 = %d，期望 2", len(plan))
	}
	for _, it := range plan {
		if it.Status != ExportItemSkipped {
			t.Errorf("文章 %d 的状态 = %q，期望 %q（跑也没用，但要露面）",
				it.ArticleID, it.Status, ExportItemSkipped)
		}
		if it.Kind == "" {
			t.Errorf("文章 %d 跳过了却没有带失败分类，用户看不到原因", it.ArticleID)
		}
		if it.Error == "" {
			t.Errorf("文章 %d 跳过了却没有说明文案", it.ArticleID)
		}
	}
}

// TestBuildExportPlanFreshIsPending 没归档过的文章是 pending，不会被误判为跳过。
func TestBuildExportPlanFreshIsPending(t *testing.T) {
	s, _ := seedExportStore(t)

	svc := &Service{store: s}
	plan, err := svc.buildExportPlan(ExportBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("buildExportPlan: %v", err)
	}
	if len(plan) != 2 {
		t.Fatalf("计划条目 = %d，期望 2", len(plan))
	}
	for _, it := range plan {
		if it.Status != ExportItemPending {
			t.Errorf("文章 %d 的状态 = %q，期望 %q", it.ArticleID, it.Status, ExportItemPending)
		}
	}
}

// TestBuildExportPlanRespectsGHIDFilter 指定账号时只处理该账号的文章。
func TestBuildExportPlanRespectsGHIDFilter(t *testing.T) {
	s, _ := seedExportStore(t)

	svc := &Service{store: s}
	plan, err := svc.buildExportPlan(ExportBatchRequest{Days: 30, Limit: 100, GHIDs: []string{"gh_2"}})
	if err != nil {
		t.Fatalf("buildExportPlan: %v", err)
	}
	// 乙号已隐藏，它不在候选集里；按 gh_2 过滤后应为空，
	// 且不能因为过滤把候选切片复用坏（原地过滤的经典坑）。
	if len(plan) != 0 {
		t.Errorf("按已隐藏账号过滤后条目 = %d，期望 0", len(plan))
	}

	plan, err = svc.buildExportPlan(ExportBatchRequest{Days: 30, Limit: 100, GHIDs: []string{"gh_1"}})
	if err != nil {
		t.Fatalf("buildExportPlan: %v", err)
	}
	if len(plan) != 2 {
		t.Errorf("按甲号过滤后条目 = %d，期望 2", len(plan))
	}
}

// TestValidateExportWindow 时间窗必须显式校验，不能静默截断。
//
// 旧实现把 days > 30 静默改成 30，用户选「近 90 天」得到的结果与「近 30 天」
// 一模一样 —— 数字看着正常，实际漏了两个月，这种 bug 用户没有任何线索能发现。
func TestValidateExportWindow(t *testing.T) {
	cases := []struct {
		in      int
		want    int
		wantErr bool
	}{
		{0, 30, false},  // 未指定 -> 默认 30
		{-5, 30, false}, // 非法值 -> 默认 30
		{1, 1, false},   // 下界
		{30, 30, false}, // 常用值
		{90, 90, false}, // 必须原样通过，不能被截断成 30
		{180, 180, false},
		{365, 365, false}, // 上界
		{366, 0, true},    // 超上限 -> 显式报错
		{10000, 0, true},
	}
	for _, tc := range cases {
		got, err := ValidateExportWindow(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ValidateExportWindow(%d) 期望报错，实际返回 %d", tc.in, got)
				continue
			}
			if !errors.Is(err, ErrInvalidExportWindow) {
				t.Errorf("ValidateExportWindow(%d) 的错误应可被 errors.Is 识别，实际 %v", tc.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ValidateExportWindow(%d) 意外报错: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ValidateExportWindow(%d) = %d，期望 %d", tc.in, got, tc.want)
		}
	}
}

// TestShouldAbortChunk 熔断判定：整块全失败且全是需人工处理的失败才停手。
func TestShouldAbortChunk(t *testing.T) {
	cases := []struct {
		name  string
		kinds []mdexport.FailureKind
		want  bool
	}{
		{"没有失败", nil, false},
		{"单篇验证码不够判据", []mdexport.FailureKind{mdexport.KindCaptcha}, false},
		{"两篇都是验证码则熔断", []mdexport.FailureKind{mdexport.KindCaptcha, mdexport.KindCaptcha}, true},
		{"验证码混超时不熔断", []mdexport.FailureKind{mdexport.KindCaptcha, mdexport.KindTimeout}, false},
		{"两篇超时不熔断", []mdexport.FailureKind{mdexport.KindTimeout, mdexport.KindTimeout}, false},
		{"依赖缺失加目录不可写熔断", []mdexport.FailureKind{mdexport.KindDependency, mdexport.KindOutputDir}, true},
	}
	for _, tc := range cases {
		if got := shouldAbortChunk(tc.kinds); got != tc.want {
			t.Errorf("%s: shouldAbortChunk = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestExportConcurrencyClamp 并发配置的边界处理。
func TestExportConcurrencyClamp(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want int
	}{
		{"没配置 -> 默认", nil, defaultExportConcurrency},
		{"配 0 -> 默认", &stubExportConfig{concurrency: 0}, defaultExportConcurrency},
		{"配负数 -> 默认", &stubExportConfig{concurrency: -1}, defaultExportConcurrency},
		{"配 1 -> 1", &stubExportConfig{concurrency: 1}, 1},
		{"配 5 -> 5", &stubExportConfig{concurrency: 5}, 5},
		{"超过上限 -> 收窄到上限", &stubExportConfig{concurrency: 64}, maxExportConcurrency},
	}
	for _, tc := range cases {
		svc := &Service{config: tc.cfg}
		if got := svc.exportConcurrency(); got != tc.want {
			t.Errorf("%s: exportConcurrency = %d，期望 %d", tc.name, got, tc.want)
		}
	}
}

// stubExportConfig 只实现测试需要的 Config 方法，其余返回零值。
type stubExportConfig struct {
	concurrency int
	dir         string
	script      string
}

func (c *stubExportConfig) GetSummaryFetchContent() bool    { return false }
func (c *stubExportConfig) GetSummaryFetchConcurrency() int { return 0 }
func (c *stubExportConfig) GetLLMMaxTokens() int            { return 0 }
func (c *stubExportConfig) GetFeedSummaryCacheHours() int   { return 0 }
func (c *stubExportConfig) GetMDExportDir() string          { return c.dir }
func (c *stubExportConfig) GetMDExportScript() string       { return c.script }
func (c *stubExportConfig) GetMDExportConcurrency() int     { return c.concurrency }
func (c *stubExportConfig) GetIMAPushSkillDir() string      { return "" }
func (c *stubExportConfig) GetIMAPushKBID() string          { return "" }
func (c *stubExportConfig) GetIMAPushFolderID() string      { return "" }
func (c *stubExportConfig) GetIMAPushConcurrency() int      { return 0 }

// LLM（PR1 pipeline worker 需要）—— 测试 stub 不构造真实 LLM
func (c *stubExportConfig) GetLLMBaseURL() string { return "" }
func (c *stubExportConfig) GetLLMAPIKey() string  { return "" }
func (c *stubExportConfig) GetLLMModel() string   { return "" }

// Pipeline worker（PR1）—— 测试 stub 默认不启用
func (c *stubExportConfig) GetBizWorkerEnabled() bool  { return false }
func (c *stubExportConfig) GetBizWorkerInterval() int  { return 0 }
func (c *stubExportConfig) GetBizWorkerBatchSize() int { return 0 }
