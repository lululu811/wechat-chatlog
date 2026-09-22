package bizhub

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sjzar/chatlog/internal/chatlog/bizhub/mdexport"
	"github.com/sjzar/chatlog/internal/model"
)

// 本文件是归档跑批的端到端验证：用一个替身脚本把抓取器的行为变成可编程的，
// 于是「成功 / 验证码 / 依赖缺失 / 限流」这些分支都能被确定性地复现。
//
// 为什么必须做这一层：真实脚本要访问微信，不可能进 CI，也不可能在测试里
// 制造「一半成功一半触发验证码」。而归档这条链路上最容易出问题的恰恰是
// 这些分支 —— 失败分类对不对、失败率高了会不会降并发、连续验证码会不会熔断、
// 进程重启后能不能续跑、失败过的文章会不会被永久放弃。
// 这些逻辑全靠一个真实脚本去手工点，等于没有回归。

// fakeExportScript 是导出脚本的替身，遵守和 script/export-md.sh 一样的契约
// （产出到 <out>/甲号/<name>/<name>.md，并向 stdout 报告 MD_PATH）。
//
// 行为由 <out>/../mode 文件决定，所以「用户修好了环境」可以在一轮任务之间切换 ——
// 不需要换脚本、不需要重建导出器，这比替换配置更接近真实：脚本没变，变的是外部环境。
//
//	（无 mode）    -> 按 URL 末段：ok* 成功 / captcha* 验证码 / dep* 依赖缺失
//	                 / slowfail* 普通失败 / sleep* 慢成功
//	all-ok        -> 一律成功（模拟「用户过了验证码 / 装好了抓取器」）
//	all-captcha   -> 一律验证码
const fakeExportScript = `#!/usr/bin/env bash
set -u
url=""; out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    *) url="$1"; shift ;;
  esac
done
name="$(basename "$url")"
mode=""
[ -f "$out/../mode" ] && mode="$(cat "$out/../mode")"

emit() {
  mkdir -p "$out/甲号/$1"
  echo "# $1" > "$out/甲号/$1/$1.md"
  echo "MD_PATH=$out/甲号/$1/$1.md"
}
fail() { echo "$1" >&2; exit "$2"; }

if [ "$mode" = "all-ok" ]; then emit "$name"; exit 0; fi
if [ "$mode" = "all-captcha" ]; then fail "wechat: 环境异常，请完成验证" 3; fi

case "$name" in
  ok*)       emit "$name"; exit 0 ;;
  sleep*)    sleep 0.6; emit "$name"; exit 0 ;;
  captcha*)  fail "wechat: 环境异常，请完成验证" 3 ;;
  dep*)      fail "mdpro: command not found" 2 ;;
  slowfail*) fail "wechat: 抓取失败" 3 ;;
  *)         fail "wechat: 未知失败" 3 ;;
esac
`

// exportTestEnv 一台配好替身脚本的测试台。
type exportTestEnv struct {
	svc      *Service
	outDir   string
	modeFile string
}

// setMode 切换替身脚本的行为（空串 = 清除，回到按 URL 判定）。
func (e *exportTestEnv) setMode(t *testing.T, mode string) {
	t.Helper()
	if mode == "" {
		_ = os.Remove(e.modeFile)
		return
	}
	if err := os.WriteFile(e.modeFile, []byte(mode), 0o644); err != nil {
		t.Fatalf("写入 mode 文件: %v", err)
	}
}

// newExportTestEnv 建测试台（走生产同一条构造路径）。
//
// 用 NewService 而不是手搓 &Service{}：bgCtx 是并发任务的根上下文，
// 手搓会漏掉它 —— 那样 context.WithCancel(nil) 直接 panic，
// 测试反而验证不到生产实际用的那条装配路径。
func newExportTestEnv(t *testing.T, concurrency int) *exportTestEnv {
	t.Helper()

	root := t.TempDir()
	scriptPath := filepath.Join(root, "export-md.sh")
	if err := os.WriteFile(scriptPath, []byte(fakeExportScript), 0o755); err != nil {
		t.Fatalf("写入替身脚本: %v", err)
	}
	outDir := filepath.Join(root, "md")

	svc, err := NewService(nil, filepath.Join(root, "work"))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetConfig(&stubExportConfig{concurrency: concurrency, dir: outDir, script: scriptPath})

	t.Cleanup(func() {
		svc.CancelExportJob()
		waitForExportIdle(t, svc, 5*time.Second)
		_ = svc.Stop()
	})

	return &exportTestEnv{svc: svc, outDir: outDir, modeFile: filepath.Join(root, "mode")}
}

// seedExportArticles 按 names 顺序写入文章，第 0 个最新（published_at 递减）。
//
// 顺序在这里是可控的输入而不是无关细节：归档按 published_at DESC 取数，
// 并发分块的边界直接取决于这个顺序 —— 要测「第一块全成功、第二块混着失败」，
// 就必须能确定每篇文章落在哪一块。
func seedExportArticles(t *testing.T, s *Store, names ...string) map[string]int64 {
	t.Helper()

	if err := s.UpsertAccounts([]Account{{GHID: "gh_1", GHName: "甲号"}}); err != nil {
		t.Fatalf("UpsertAccounts: %v", err)
	}

	base := time.Now().Add(-time.Hour)
	msgs := make([]*model.BizMessage, 0, len(names))
	for i, name := range names {
		msgs = append(msgs, &model.BizMessage{
			GHID:   "gh_1",
			GHName: "甲号",
			Time:   base.Add(-time.Duration(i) * time.Minute),
			Title:  name,
			URL:    "https://mp.weixin.qq.com/s/" + name,
		})
	}
	if _, err := s.UpsertArticles(msgs); err != nil {
		t.Fatalf("UpsertArticles: %v", err)
	}

	articles, err := s.GetUnexportedArticles(30, 100)
	if err != nil {
		t.Fatalf("GetUnexportedArticles: %v", err)
	}
	if len(articles) != len(names) {
		t.Fatalf("种子文章应为 %d 篇，实际 %d 篇", len(names), len(articles))
	}
	byURL := make(map[string]int64, len(articles))
	for _, a := range articles {
		byURL[a.URL] = a.ID
	}
	return byURL
}

// waitForExportJob 轮询任务直到它结束。
func waitForExportJob(t *testing.T, svc *Service, jobID string, timeout time.Duration) *ExportJob {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		job, _, err := svc.GetExportJob(jobID, true)
		if err != nil {
			t.Fatalf("GetExportJob: %v", err)
		}
		if job == nil {
			t.Fatalf("任务 %s 不存在", jobID)
		}
		if job.Done() {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("任务 %s 在 %s 内没有结束（status=%s 成功=%d 失败=%d 待处理=%d）",
				jobID, timeout, job.Status, job.Succeeded, job.Failed, job.Pending)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForExportIdle(t *testing.T, svc *Service, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for svc.ActiveExportJob() != "" {
		if time.Now().After(deadline) {
			t.Fatalf("任务 %s 在 %s 内没有释放（goroutine 可能卡住）",
				svc.ActiveExportJob(), timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// --- 全成功路径 ---

// TestExportJobAllSucceed 全成功路径：条目 done、记录 exported、文件真的落盘。
//
// 「文件真的落盘」这一条不能省：只断言数据库状态的话，一个把 md_path 写对
// 但根本没产出文件的实现也能通过。
func TestExportJobAllSucceed(t *testing.T) {
	env := newExportTestEnv(t, 3)
	byURL := seedExportArticles(t, env.svc.Store(), "ok-1", "ok-2", "ok-3")

	job, err := env.svc.StartExportJob(ExportBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("StartExportJob: %v", err)
	}
	if job.Total != 3 {
		t.Fatalf("任务总数 = %d，期望 3", job.Total)
	}

	done := waitForExportJob(t, env.svc, job.ID, 30*time.Second)
	if done.Status != ExportJobDone {
		t.Errorf("任务状态 = %q，期望 %q（错误信息：%s）", done.Status, ExportJobDone, done.Error)
	}
	if done.Succeeded != 3 || done.Failed != 0 || done.Skipped != 0 {
		t.Errorf("成功 %d / 失败 %d / 跳过 %d，期望 3/0/0", done.Succeeded, done.Failed, done.Skipped)
	}
	if p := done.Percent(); p != 100 {
		t.Errorf("进度 = %d%%，期望 100%%", p)
	}

	for url, id := range byURL {
		rec, err := env.svc.Store().GetExportRecord(id)
		if err != nil {
			t.Fatalf("GetExportRecord: %v", err)
		}
		if rec == nil || rec.Status != ExportStatusExported {
			t.Fatalf("%s 的归档记录状态 = %+v，期望 %s", url, rec, ExportStatusExported)
		}
		if rec.MDPath == "" {
			t.Errorf("%s 归档成功却没有 md_path", url)
			continue
		}
		if _, err := os.Stat(rec.MDPath); err != nil {
			t.Errorf("%s 的 md_path 指向的文件不存在：%v", url, err)
		}
		if !strings.HasPrefix(rec.MDPath, env.outDir) {
			t.Errorf("%s 的 md_path 落在输出目录外：%q", url, rec.MDPath)
		}
		if rec.Kind != "" {
			t.Errorf("%s 归档成功的记录不该带失败分类，实际 %q", url, rec.Kind)
		}
	}

	// 成功之后候选集应为空 —— 再点一次归档不该重复处理
	stats, err := env.svc.GetExportStatus(30)
	if err != nil {
		t.Fatalf("GetExportStatus: %v", err)
	}
	if stats.Pending != 0 {
		t.Errorf("全部归档成功后待归档 = %d，期望 0", stats.Pending)
	}
	if stats.Exported != 3 {
		t.Errorf("已归档 = %d，期望 3", stats.Exported)
	}
}

// --- 熔断 ---

// TestExportJobCaptchaAborts 连续验证码要熔断并停下，剩下的条目以 skipped 露面。
//
// 熔断的意义：继续跑只会把账号彻底打进风控名单。停手 + 把原因摆到界面上，
// 比「跑完 100 篇、100 篇全失败」对用户有用得多。
func TestExportJobCaptchaAborts(t *testing.T) {
	env := newExportTestEnv(t, 3)
	seedExportArticles(t, env.svc.Store(), "captcha-1", "captcha-2", "captcha-3", "captcha-4")

	job, err := env.svc.StartExportJob(ExportBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("StartExportJob: %v", err)
	}

	done := waitForExportJob(t, env.svc, job.ID, 30*time.Second)
	if done.Status != ExportJobFailed {
		t.Errorf("任务状态 = %q，期望 %q", done.Status, ExportJobFailed)
	}
	if done.Error == "" {
		t.Error("熔断的任务必须带一句人能看懂的原因")
	}
	if done.Failed != 3 {
		t.Errorf("失败 = %d，期望 3（第一块 3 篇全部触发验证码）", done.Failed)
	}
	if done.Skipped != 1 {
		t.Errorf("跳过 = %d，期望 1（熔断后剩下 1 篇不再尝试）", done.Skipped)
	}

	_, items, err := env.svc.GetExportJob(job.ID, true)
	if err != nil {
		t.Fatalf("GetExportJob: %v", err)
	}
	for _, it := range items {
		if it.Status == ExportItemPending || it.Status == ExportItemRunning {
			t.Errorf("熔断后不该留下未处理的条目：%s = %s", it.Title, it.Status)
		}
		if it.Kind != string(mdexport.KindCaptcha) {
			t.Errorf("%s 的分类 = %q，期望 %q", it.Title, it.Kind, mdexport.KindCaptcha)
		}
	}

	// 验证码属于「需人工处理」：下一轮计划里应被判为跳过并计入 blocked。
	// 注意数量差 1 的原因 —— 熔断时还没来得及尝试的那篇没有归档记录，
	// 所以它既不算 blocked（没有失败记录），也仍然留在候选里。
	stats, err := env.svc.GetExportStatus(30)
	if err != nil {
		t.Fatalf("GetExportStatus: %v", err)
	}
	if stats.Pending != 4 {
		t.Errorf("待归档 = %d，期望 4", stats.Pending)
	}
	if stats.Blocked != 3 {
		t.Errorf("需人工处理 = %d，期望 3（只有真正尝试过并失败的那 3 篇）", stats.Blocked)
	}

	plan, err := env.svc.buildExportPlan(ExportBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("buildExportPlan: %v", err)
	}
	var pending, skipped int
	for _, it := range plan {
		switch it.Status {
		case ExportItemPending:
			pending++
		case ExportItemSkipped:
			skipped++
		}
	}
	if pending != 1 || skipped != 3 {
		t.Errorf("下一轮计划 pending=%d skipped=%d，期望 1/3", pending, skipped)
	}
}

// --- 依赖缺失 ---

// TestExportJobDependencyFailure 依赖缺失（脚本退出码 2）归到需人工处理。
//
// 这类失败重试一百次也没用，必须让人去装抓取器 —— 所以它不能被当成
// 「可重试」混在自动重试里空转。
func TestExportJobDependencyFailure(t *testing.T) {
	env := newExportTestEnv(t, 1)
	seedExportArticles(t, env.svc.Store(), "dep-1", "dep-2")

	job, err := env.svc.StartExportJob(ExportBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("StartExportJob: %v", err)
	}
	done := waitForExportJob(t, env.svc, job.ID, 30*time.Second)

	if done.Failed != 2 {
		t.Fatalf("失败 = %d，期望 2", done.Failed)
	}
	// 全部失败且同类时，任务错误信息要说出是哪一类，而不是笼统的「全部失败」
	if !strings.Contains(done.Error, mdexport.KindDependency.Label()) {
		t.Errorf("任务错误信息 = %q，期望包含分类标签 %q", done.Error, mdexport.KindDependency.Label())
	}

	_, items, err := env.svc.GetExportJob(job.ID, true)
	if err != nil {
		t.Fatalf("GetExportJob: %v", err)
	}
	for _, it := range items {
		if it.Kind != string(mdexport.KindDependency) {
			t.Errorf("%s 的分类 = %q，期望 %q", it.Title, it.Kind, mdexport.KindDependency)
		}
	}

	// 下一轮计划：需人工处理的条目应被判为跳过（不再空转）
	plan, err := env.svc.buildExportPlan(ExportBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("buildExportPlan: %v", err)
	}
	for _, it := range plan {
		if it.Status != ExportItemSkipped {
			t.Errorf("%s 下一轮仍是 %s，说明「需人工处理」的判定没生效", it.Title, it.Status)
		}
	}
}

// --- 降级 ---

// TestExportJobDegradesOnHighFailureRate 失败率过高时把并发降到 1。
//
// 判据刻意放在「处理满 6 篇之后」：小批量任务动不动就 50% 失败率，
// 一上来就降级会让它们永远跑不快。
func TestExportJobDegradesOnHighFailureRate(t *testing.T) {
	env := newExportTestEnv(t, 3)
	// 顺序即分块：第一块 3 篇全成功，第二块 1 篇普通失败 + 2 篇验证码。
	// 第二块不是「全为需人工处理」，所以触发降级而不会熔断。
	seedExportArticles(t, env.svc.Store(),
		"ok-1", "ok-2", "ok-3", "slowfail-1", "captcha-1", "captcha-2")

	job, err := env.svc.StartExportJob(ExportBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("StartExportJob: %v", err)
	}
	done := waitForExportJob(t, env.svc, job.ID, 30*time.Second)

	if done.Succeeded != 3 || done.Failed != 3 {
		t.Fatalf("成功 %d / 失败 %d，期望 3/3", done.Succeeded, done.Failed)
	}
	if !done.Degraded {
		t.Error("失败率达到 50% 却没有标记降级")
	}
	if done.Concurrency != 1 {
		t.Errorf("并发 = %d，期望降级到 1", done.Concurrency)
	}
}

// --- 重试 ---

// TestExportJobRetryAfterFix 修好问题后重试，之前失败的条目能跑通。
//
// 这是「失败不是终态」的闭环验证：第一次因验证码失败 → 用户过掉验证码 →
// 点重试 → 全部成功。旧实现里这些文章会被 e.id IS NULL 谓词永久排除，
// 这条路径根本不存在。
func TestExportJobRetryAfterFix(t *testing.T) {
	env := newExportTestEnv(t, 2)
	byURL := seedExportArticles(t, env.svc.Store(), "captcha-1", "captcha-2")

	job, err := env.svc.StartExportJob(ExportBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("StartExportJob: %v", err)
	}
	first := waitForExportJob(t, env.svc, job.ID, 30*time.Second)
	if first.Succeeded != 0 {
		t.Fatalf("首轮成功 = %d，期望 0（脚本一定会返回验证码）", first.Succeeded)
	}

	// 「用户过掉了验证码」：脚本没变，环境变了
	env.setMode(t, "all-ok")

	retried, err := env.svc.RetryExportJob(job.ID)
	if err != nil {
		t.Fatalf("RetryExportJob: %v", err)
	}
	if retried.Status != ExportJobRunning {
		t.Errorf("重试后任务状态 = %q，期望 %q（应重新进入运行）", retried.Status, ExportJobRunning)
	}

	second := waitForExportJob(t, env.svc, job.ID, 30*time.Second)
	if second.Status != ExportJobDone {
		t.Errorf("重试后状态 = %q，期望 %q（错误：%s）", second.Status, ExportJobDone, second.Error)
	}
	if second.Succeeded != 2 || second.Failed != 0 {
		t.Errorf("重试后成功 %d / 失败 %d，期望 2/0", second.Succeeded, second.Failed)
	}

	for _, id := range byURL {
		rec, err := env.svc.Store().GetExportRecord(id)
		if err != nil {
			t.Fatalf("GetExportRecord: %v", err)
		}
		if rec == nil || rec.Status != ExportStatusExported {
			t.Fatalf("重试后记录状态 = %+v，期望 %s", rec, ExportStatusExported)
		}
		if rec.Attempts != 0 {
			t.Errorf("归档成功后 attempts = %d，期望归零", rec.Attempts)
		}
	}
}

// TestRetryExportJobRejectsUnknownID 对不存在的任务重试要报错，而不是静默建个空任务。
func TestRetryExportJobRejectsUnknownID(t *testing.T) {
	env := newExportTestEnv(t, 1)
	_, err := env.svc.RetryExportJob("job_不存在")
	if err == nil {
		t.Fatal("对不存在的任务重试应报错")
	}
	// 必须是可识别的哨兵错误：handler 靠它回 404，否则对不存在的任务会回 500，
	// 调用方会以为「服务出错」而不是「这个任务没了」。
	if !errors.Is(err, ErrExportJobNotFound) {
		t.Errorf("错误 = %v，期望 ErrExportJobNotFound", err)
	}
}

// --- 断点续跑 ---

// TestExportJobResumeAfterRestart 服务重启后中断的任务能续跑。
//
// 断点续跑不需要额外的检查点机制：条目状态本身就是检查点。
// 启动时把残留的 running 任务标为 interrupted、running 条目退回 pending，
// 然后重新拉起来即可。
func TestExportJobResumeAfterRestart(t *testing.T) {
	env := newExportTestEnv(t, 2)
	byURL := seedExportArticles(t, env.svc.Store(), "ok-1", "ok-2")

	// 模拟「任务建好了但进程在开跑前就被杀掉」：任务 running、条目全 pending
	ids := make([]int64, 0, len(byURL))
	for _, id := range byURL {
		ids = append(ids, id)
	}
	job := &ExportJob{ID: "job_crashed", Status: ExportJobRunning, Days: 30, MaxItems: 100, Concurrency: 2, Total: len(ids)}
	items := make([]ExportJobItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, ExportJobItem{JobID: "job_crashed", ArticleID: id, Status: ExportItemPending})
	}
	if err := env.svc.Store().CreateExportJob(job, items); err != nil {
		t.Fatalf("CreateExportJob: %v", err)
	}

	// 走生产同一条路径：Start() 内部会调用它
	env.svc.resumeInterruptedExportJobs()

	done := waitForExportJob(t, env.svc, "job_crashed", 30*time.Second)
	if done.Succeeded != 2 {
		t.Errorf("续跑后成功 = %d，期望 2（错误：%s）", done.Succeeded, done.Error)
	}
	if done.Status != ExportJobDone {
		t.Errorf("续跑后状态 = %q，期望 %q", done.Status, ExportJobDone)
	}
}

// TestResumeDoesNotBlockWhenUnconfigured 未配置归档时续跑不该把服务卡住。
func TestResumeDoesNotBlockWhenUnconfigured(t *testing.T) {
	svc, err := NewService(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop() })

	job := &ExportJob{ID: "job_stale", Status: ExportJobRunning, Total: 0}
	if err := svc.Store().CreateExportJob(job, nil); err != nil {
		t.Fatalf("CreateExportJob: %v", err)
	}

	svc.resumeInterruptedExportJobs() // 不应 panic / 不应启动任务

	if svc.ActiveExportJob() != "" {
		t.Errorf("未配置归档却启动了任务：%s", svc.ActiveExportJob())
	}
	got, err := svc.Store().GetExportJob("job_stale")
	if err != nil {
		t.Fatalf("GetExportJob: %v", err)
	}
	if got.Status != ExportJobInterrupted {
		t.Errorf("残留任务状态 = %q，期望 %q（僵尸任务会让前端永远转圈）",
			got.Status, ExportJobInterrupted)
	}
}

// --- 并发互斥与取消 ---

// TestExportJobMutualExclusionAndCancel 同一时刻只跑一个任务，且能取消、能接着跑完。
func TestExportJobMutualExclusionAndCancel(t *testing.T) {
	env := newExportTestEnv(t, 2)
	seedExportArticles(t, env.svc.Store(), "sleep-1", "sleep-2", "sleep-3")

	job, err := env.svc.StartExportJob(ExportBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("StartExportJob: %v", err)
	}
	if env.svc.ActiveExportJob() != job.ID {
		t.Fatalf("活跃任务 = %q，期望 %q", env.svc.ActiveExportJob(), job.ID)
	}

	// 第二个任务必须被挡住：并发跑两批只会互相抢抓取额度、更容易触发风控
	if _, err := env.svc.StartExportJob(ExportBatchRequest{Days: 30, Limit: 100}); err == nil {
		t.Error("已有任务在跑时不该允许再建一个任务")
	} else if !errors.Is(err, ErrExportJobRunning) {
		t.Errorf("错误 = %v，期望 ErrExportJobRunning", err)
	}

	canceled, ok := env.svc.CancelExportJob()
	if !ok || canceled != job.ID {
		t.Fatalf("取消返回 %q/%v，期望 %q/true", canceled, ok, job.ID)
	}
	if _, ok := env.svc.CancelExportJob(); ok {
		t.Error("没有任务在跑时取消应返回 false")
	}

	done := waitForExportJob(t, env.svc, job.ID, 30*time.Second)
	if done.Status != ExportJobCanceled {
		t.Errorf("取消后状态 = %q，期望 %q", done.Status, ExportJobCanceled)
	}
	if done.Succeeded+done.Failed == done.Total {
		t.Fatalf("取消得太快，这个用例失去了意义（应当还剩条目没跑）：成功 %d 失败 %d 共 %d",
			done.Succeeded, done.Failed, done.Total)
	}
	// 「任务行是终态 ⟹ 并发名额已释放」这条不变量，否则用户点重试会被
	// 「已有任务在运行」挡回去，而界面上明明显示任务已经结束。
	if env.svc.ActiveExportJob() != "" {
		t.Errorf("任务已到终态但名额未释放：%s", env.svc.ActiveExportJob())
	}

	// 被取消的任务可以重试接着跑完
	env.setMode(t, "all-ok")
	if _, err := env.svc.RetryExportJob(job.ID); err != nil {
		t.Fatalf("取消后重试: %v", err)
	}
	final := waitForExportJob(t, env.svc, job.ID, 30*time.Second)
	if final.Succeeded != 3 {
		t.Errorf("续跑后成功 = %d，期望 3（错误：%s）", final.Succeeded, final.Error)
	}
}

// --- 入参校验 ---

// TestStartExportJobValidatesWindow 时间窗校验必须显式失败，不能静默截断。
func TestStartExportJobValidatesWindow(t *testing.T) {
	env := newExportTestEnv(t, 1)
	seedExportArticles(t, env.svc.Store(), "ok-1")

	if _, err := env.svc.StartExportJob(ExportBatchRequest{Days: maxExportWindowDays + 1, Limit: 10}); err == nil {
		t.Error("超过时间窗上限应报错")
	} else if !errors.Is(err, ErrInvalidExportWindow) {
		t.Errorf("错误 = %v，期望 ErrInvalidExportWindow", err)
	}

	job, err := env.svc.StartExportJob(ExportBatchRequest{Days: 0, Limit: 10})
	if err != nil {
		t.Fatalf("days=0 应回退到默认 30 天，却报错: %v", err)
	}
	if job.Days != 30 {
		t.Errorf("days = %d，期望回退到 30", job.Days)
	}
	_ = waitForExportJob(t, env.svc, job.ID, 30*time.Second)
}

// TestStartExportJobWithoutConfig 未配置归档时给出可操作的错误，而不是 500。
func TestStartExportJobWithoutConfig(t *testing.T) {
	svc, err := NewService(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop() })

	if _, err := svc.StartExportJob(ExportBatchRequest{Days: 30}); !errors.Is(err, ErrExportNotConfigured) {
		t.Errorf("错误 = %v，期望 ErrExportNotConfigured", err)
	}
	if svc.IsExportConfigured() {
		t.Error("没有注入配置时 IsExportConfigured 应为 false")
	}
}
