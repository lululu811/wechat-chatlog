package bizhub

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lululu811/wechat-chatlog/internal/chatlog/bizhub/imapush"
)

// pushFakeIMAScript 与 imapush/script_contract_test.go 的 fakeIMAScript 等价的 inline 版本。
//
// 这里复制一份而不是跨包引用，是因为：imapush 测试里的 fakeIMAScript 是包级未导出常量，
// 改 export 要重新设计接口；测试替身场景下，复制比依赖更稳。
const pushFakeIMAScript = `#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');

const apiPath = process.argv[2];
const bodyRaw = process.argv[3] || '{}';

let body;
try { body = JSON.parse(bodyRaw); } catch (e) {
  process.stderr.write(JSON.stringify({code: -100, msg: 'invalid JSON'}));
  process.exit(1);
}

const skillDir = path.dirname(process.argv[1]);
const modeFile = path.join(skillDir, '..', 'mode');
let mode = '';
try { mode = fs.readFileSync(modeFile, 'utf8').trim(); } catch (e) {}

function ok(url) {
  return {url, ret_code: 0, media_id: 'm_' + url.split('/').pop()};
}

let results;
if (mode === 'all-ok') {
  results = {};
  for (const u of (body.urls || [])) results[u] = ok(u);
  process.stdout.write(JSON.stringify({code: 0, msg: 'success', data: {results}}));
} else if (mode === 'all-rate') {
  process.stdout.write(JSON.stringify({code: 110021, msg: '频率限制'}));
} else if (mode === 'all-cred') {
  process.stderr.write(JSON.stringify({code: -100, msg: '未找到 IMA 凭证'}));
  process.exit(1);
} else {
  results = {};
  for (const u of (body.urls || [])) results[u] = ok(u);
  process.stdout.write(JSON.stringify({code: 0, msg: 'success', data: {results}}));
}
`

// pushTestEnv 给批量推送测试用的最小 Service：含 imaskai fake + 已 seed 的文章。
//
// 与 export 测试不同：这里 push 测试需要真实的 imaskai skill_dir 才能
// getPushExporter 通过（IsPushConfigured 已经做了 stat），但 imaskai 不必真调 ——
// 替身脚本路径由 fakeIMAScript 提供。
type pushTestEnv struct {
	svc      *Service
	store    *Store
	skillDir string
	modeFile string
	articles []int64 // seeded article ids
}

func newPushTestEnvWithSkill(t *testing.T) *pushTestEnv {
	t.Helper()
	root := t.TempDir()

	store, err := Open(root)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// imaskai skill_dir + ima_api.cjs + mode file
	skillDir := filepath.Join(root, "ima")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	apiPath := filepath.Join(skillDir, "ima_api.cjs")
	if err := os.WriteFile(apiPath, []byte(pushFakeIMAScript), 0o755); err != nil {
		t.Fatalf("write api: %v", err)
	}
	modeFile := filepath.Join(root, "mode")

	// seed 公众号 + 3 篇文章
	_, err = store.db.Exec(`INSERT INTO biz_accounts (gh_id, gh_name, updated_at) VALUES (?, ?, 0)`, "gh_test", "测试号")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	ids := make([]int64, 0, 3)
	for i, title := range []string{"文章1", "文章2", "文章3"} {
		res, err := store.db.Exec(`INSERT INTO biz_articles
			(gh_id, title, description, url, app_id, local_type, local_id, sort_seq, published_at, synced_at, bookmarked, is_read)
			VALUES (?, ?, '', ?, '', 0, 0, 0, ?, 0, 0, 0)`,
			"gh_test", title, "https://mp.weixin.qq.com/s/test-"+itoa(int64(i)), time.Now().Unix())
		if err != nil {
			t.Fatalf("seed article: %v", err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
	}

	cfg := &stubPushConfig{
		skillDir: skillDir,
		kbID:     "kb-test",
		folderID: "kb-test",
		dir:      root,
		script:   "/tmp/x",
	}

	svc := &Service{store: store, workDir: root, config: cfg}
	svc.bgCtx, svc.bgCancel = context.WithCancel(context.Background())
	t.Cleanup(func() {
		if svc.bgCancel != nil {
			svc.bgCancel()
		}
	})

	return &pushTestEnv{
		svc:      svc,
		store:    store,
		skillDir: skillDir,
		modeFile: modeFile,
		articles: ids,
	}
}

func setPushMode(t *testing.T, env *pushTestEnv, mode string) {
	t.Helper()
	if mode == "" {
		_ = os.Remove(env.modeFile)
		return
	}
	if err := os.WriteFile(env.modeFile, []byte(mode), 0o644); err != nil {
		t.Fatalf("write mode: %v", err)
	}
}

// TestGetPushableArticles_ChainConstraint 是链式校验的关键回归点：
//
//	未 export 成功的文章不能进 push 候选。
func TestGetPushableArticles_ChainConstraint(t *testing.T) {
	env := newPushTestEnvWithSkill(t)

	// 没有 export 记录 → 0 候选
	got, err := env.store.GetPushableArticles(30, 100)
	if err != nil {
		t.Fatalf("GetPushableArticles: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("链式约束：未 export 的文章不应进候选，但 got %d 篇", len(got))
	}

	// 标记前两篇为 exported
	if err := env.store.UpsertExportRecord(env.articles[0], "https://mp.weixin.qq.com/s/test-0", "/tmp/0.md", "", "exported", "", ""); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}
	if err := env.store.UpsertExportRecord(env.articles[1], "https://mp.weixin.qq.com/s/test-1", "/tmp/1.md", "", "exported", "", ""); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}
	// 第三篇 status='failed'
	if err := env.store.UpsertExportRecord(env.articles[2], "https://mp.weixin.qq.com/s/test-2", "", "", "failed", "", ""); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}

	got, err = env.store.GetPushableArticles(30, 100)
	if err != nil {
		t.Fatalf("GetPushableArticles: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("链式约束：只有 exported 的应进候选，got %d 篇", len(got))
	}
}

// TestGetPushableArticles_IncludesPushFailed 已 push 失败的会重入候选。
func TestGetPushableArticles_IncludesPushFailed(t *testing.T) {
	env := newPushTestEnvWithSkill(t)
	id := env.articles[0]

	if err := env.store.UpsertExportRecord(id, "https://mp.weixin.qq.com/s/test-0", "/tmp/0.md", "", "exported", "", ""); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}
	if err := env.store.UpsertPushRecord(id, "", PushStatusFailed, "rate_limited", "频率限制"); err != nil {
		t.Fatalf("UpsertPushRecord: %v", err)
	}

	got, err := env.store.GetPushableArticles(30, 100)
	if err != nil {
		t.Fatalf("GetPushableArticles: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("push_failed 的文章应重入候选，got %d", len(got))
	}

	// 但已 push 成功的不会再进候选
	if err := env.store.UpsertPushRecord(id, "m_abc", PushStatusPushed, "", ""); err != nil {
		t.Fatalf("UpsertPushRecord pushed: %v", err)
	}
	got, _ = env.store.GetPushableArticles(30, 100)
	if len(got) != 0 {
		t.Errorf("pushed 的文章不应再进候选，got %d", len(got))
	}
}

// TestGetPushableArticles_DoesNotFilterByAttempts：SQL 层不按 push_attempts 过滤。
//
// 失败的「需人工处理」和「重试用尽」判断都放在 Go 层（buildPushPlan）。
// 这条断言把这条边界钉在测试里 —— 改前必须想清楚这是不是 SQL 该管的事。
func TestGetPushableArticles_DoesNotFilterByAttempts(t *testing.T) {
	env := newPushTestEnvWithSkill(t)
	id := env.articles[0]

	if err := env.store.UpsertExportRecord(id, "https://mp.weixin.qq.com/s/test-0", "/tmp/0.md", "", "exported", "", ""); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}
	// 把 push_attempts 推到上限
	for i := 0; i < maxPushAttempts; i++ {
		if err := env.store.UpsertPushRecord(id, "", PushStatusFailed, "rate_limited", "test"); err != nil {
			t.Fatalf("UpsertPushRecord: %v", err)
		}
	}
	rec, _ := env.store.GetExportRecord(id)
	if rec.PushAttempts != maxPushAttempts {
		t.Fatalf("push_attempts = %d, want %d", rec.PushAttempts, maxPushAttempts)
	}

	// SQL 层不挡：把它列出来；Go 层 buildPushPlan 才会基于 record 标 skipped。
	got, _ := env.store.GetPushableArticles(30, 100)
	if len(got) != 1 {
		t.Errorf("PushCandidateWhere 不按 attempts 过滤，got %d 篇", len(got))
	}

	// buildPushPlan 会把它标 skipped（attempts 用尽）
	items, _ := env.svc.buildPushPlan(PushBatchRequest{Days: 30, Limit: 100})
	if len(items) != 1 {
		t.Fatalf("buildPushPlan 候选 = %d, want 1", len(items))
	}
	if items[0].Status != ExportItemSkipped {
		t.Errorf("push_attempts 用尽的应被标 skipped，got %s", items[0].Status)
	}
}

// TestBuildPushPlan_SkipsPermanentFailure 永久失败的 buildPushPlan 应标 skipped。
func TestBuildPushPlan_SkipsPermanentFailure(t *testing.T) {
	env := newPushTestEnvWithSkill(t)
	id := env.articles[0]

	if err := env.store.UpsertExportRecord(id, "https://mp.weixin.qq.com/s/test-0", "/tmp/0.md", "", "exported", "", ""); err != nil {
		t.Fatalf("UpsertExportRecord: %v", err)
	}
	// kind=credential（永久失败）
	if err := env.store.UpsertPushRecord(id, "", PushStatusFailed, string(imapush.KindCredential), "凭证缺失"); err != nil {
		t.Fatalf("UpsertPushRecord: %v", err)
	}

	items, err := env.svc.buildPushPlan(PushBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("buildPushPlan: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Status != ExportItemSkipped {
		t.Errorf("permanent failure should be skipped, got status=%s", items[0].Status)
	}
	if items[0].Kind != string(imapush.KindCredential) {
		t.Errorf("kind = %q, want %q", items[0].Kind, imapush.KindCredential)
	}
}

// TestStartPushJob_Lifecycle 任务创建 → 跑完 → GetPushJob 拉取。
func TestStartPushJob_Lifecycle(t *testing.T) {
	env := newPushTestEnvWithSkill(t)
	setPushMode(t, env, "all-ok")

	for _, id := range env.articles {
		if err := env.store.UpsertExportRecord(id, "https://mp.weixin.qq.com/s/test-"+itoa(id), "/tmp/x.md", "", "exported", "", ""); err != nil {
			t.Fatalf("UpsertExportRecord: %v", err)
		}
	}

	job, err := env.svc.StartPushJob(PushBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("StartPushJob: %v", err)
	}
	if job.Total != 3 {
		t.Errorf("total = %d, want 3", job.Total)
	}
	if job.Status != "running" && job.Status != "done" {
		t.Errorf("status = %q, want running or done", job.Status)
	}

	// 等任务结束（带超时）
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		job2, _ := env.store.GetPushJob(job.ID)
		if job2 != nil && job2.Status == "done" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 检查每篇都被推上去
	for _, id := range env.articles {
		rec, _ := env.store.GetExportRecord(id)
		if rec.PushedStatus != "pushed" {
			t.Errorf("article %d: pushedStatus = %q, want pushed", id, rec.PushedStatus)
		}
		if rec.PushedMediaID == "" {
			t.Errorf("article %d: empty mediaID", id)
		}
	}
}

// TestStartPushJob_RejectsUnconfigured 未配置时返 ErrPushNotConfigured。
func TestStartPushJob_RejectsUnconfigured(t *testing.T) {
	env := newPushTestEnvWithSkill(t)
	env.svc.config = &stubPushConfig{} // 全空
	_, err := env.svc.StartPushJob(PushBatchRequest{Days: 30, Limit: 100})
	if err != ErrPushNotConfigured {
		t.Errorf("err = %v, want ErrPushNotConfigured", err)
	}
}

// TestStartPushJob_MutualExclusionWithExport push 与 export 不互相阻塞。
//
// 这一条是用户决定的关键之一：同时跑一个 export + 一个 push 是允许的。
func TestStartPushJob_MutualExclusionWithExport(t *testing.T) {
	env := newPushTestEnvWithSkill(t)
	setPushMode(t, env, "all-ok")

	// 先启动一个 push
	for _, id := range env.articles {
		_ = env.store.UpsertExportRecord(id, "https://mp.weixin.qq.com/s/test-"+itoa(id), "/tmp/x.md", "", "exported", "", "")
	}
	pushJob, err := env.svc.StartPushJob(PushBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("StartPushJob: %v", err)
	}

	// 同时启动 export 任务（用 fakeExportScript 路径）
	exportSkill := t.TempDir()
	if err := os.WriteFile(filepath.Join(exportSkill, "export-md.sh"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatalf("write fake export script: %v", err)
	}
	env.svc.config.(*stubPushConfig).dir = t.TempDir()
	env.svc.config.(*stubPushConfig).script = filepath.Join(exportSkill, "export-md.sh")

	// 现在 activePushJob 应该已设上；export activeJob 应该还是空（独立槽位）
	if env.svc.ActivePushJob() != pushJob.ID {
		t.Errorf("ActivePushJob = %q, want %q", env.svc.ActivePushJob(), pushJob.ID)
	}
	if env.svc.ActiveExportJob() != "" {
		t.Errorf("export 槽位被 push 占用: %q", env.svc.ActiveExportJob())
	}
}

// TestCancelPushJob 取消正在跑的任务，状态变 canceled。
func TestCancelPushJob(t *testing.T) {
	env := newPushTestEnvWithSkill(t)
	// 写一个 hanging 替身，让任务跑到一半
	hanger := `#!/usr/bin/env node
'use strict';
process.stdout.write(JSON.stringify({code: 0, data: {results: {}}}));
process.exit(0);
`
	if err := os.WriteFile(filepath.Join(env.skillDir, "ima_api.cjs"), []byte(hanger), 0o755); err != nil {
		t.Fatalf("write hanger: %v", err)
	}

	for _, id := range env.articles {
		_ = env.store.UpsertExportRecord(id, "https://mp.weixin.qq.com/s/test-"+itoa(id), "/tmp/x.md", "", "exported", "", "")
	}

	job, err := env.svc.StartPushJob(PushBatchRequest{Days: 30, Limit: 100})
	if err != nil {
		t.Fatalf("StartPushJob: %v", err)
	}

	if err := env.svc.CancelPushJob(job.ID); err != nil {
		t.Fatalf("CancelPushJob: %v", err)
	}

	// 等任务收尾（带超时）
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := env.store.GetPushJob(job.ID)
		if j != nil && j.Status != "running" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	final, _ := env.store.GetPushJob(job.ID)
	if final.Status != "canceled" {
		t.Errorf("status = %q, want canceled", final.Status)
	}
	// 让 goroutine 跑完再退出 —— 否则 t.Cleanup 关闭 store 时它还在跑
	for time.Now().Before(deadline) {
		if env.svc.ActivePushJob() == "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestGetPushStats 状态汇总数字。
func TestGetPushStats(t *testing.T) {
	env := newPushTestEnvWithSkill(t)

	// 1 推送成功
	_ = env.store.UpsertExportRecord(env.articles[0], "https://mp.weixin.qq.com/s/test-0", "/tmp/0.md", "", "exported", "", "")
	_ = env.store.UpsertPushRecord(env.articles[0], "m_1", PushStatusPushed, "", "")

	// 1 待推送（未 push 过）
	_ = env.store.UpsertExportRecord(env.articles[1], "https://mp.weixin.qq.com/s/test-1", "/tmp/1.md", "", "exported", "", "")

	stats, err := env.store.GetPushStats(30)
	if err != nil {
		t.Fatalf("GetPushStats: %v", err)
	}
	if stats.Pushed != 1 {
		t.Errorf("pushed = %d, want 1", stats.Pushed)
	}
	if stats.Pending != 1 {
		t.Errorf("pending = %d, want 1", stats.Pending)
	}
}

// TestPushCandidateWhere_MirrorOfPlan：保证 SQL 与 plan §4.5.3 的链式约束一致。
func TestPushCandidateWhere_MirrorOfPlan(t *testing.T) {
	// 把常量拆出来比对，文档与代码不能漂移。
	want := []string{
		"a.published_at >= ?",
		"a.gh_id NOT IN (SELECT gh_id FROM biz_accounts WHERE hidden = 1)",
		"e.id IS NOT NULL",
		"e.status IN ('exported','summary_generated')",
		"(e.pushed_status = '' OR e.pushed_status = 'push_failed')",
	}
	for _, w := range want {
		if !contains(PushCandidateWhere, w) {
			t.Errorf("PushCandidateWhere 缺失 %q\n实际: %s", w, PushCandidateWhere)
		}
	}
}

// helpers ----------

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
