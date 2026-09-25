package imapush

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// nodeAvailable 检测 node 二进制是否可用（PATH 中或已知路径）。
//
// 测试里需要 node —— 跳过条件是清晰可知的，不留模糊空间。
func nodeAvailable() bool {
	if _, err := exec.LookPath("node"); err == nil {
		return true
	}
	// 兜底：常见 homebrew / nvm 路径
	for _, p := range []string{
		"/usr/local/bin/node",
		"/opt/homebrew/bin/node",
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// fakeIMAScript 是 imaskai 的替身，遵守与 ima_api.cjs 一致的调用契约。
//
// 调用形态：node ima_api.cjs <apiPath> <bodyJSON> <optsJSON>
//
// 行为由 <skill_dir>/../mode 文件决定（与 export_runner_test.go 的 fakeExportScript 同思路）：
//   all-ok         -> 全部成功
//   all-rate       -> 整批频控
//   all-cred       -> 凭证缺失（-100）
//   all-skillup    -> skill 需升级（-200）
//   all-dep        -> 依赖缺失（imaskai not found）
//   (空)           -> 按 URL 末段：ok* 成功 / rate* 限流 / bad* URL 不合法 / err* 单条失败
const fakeIMAScript = `#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');

const apiPath = process.argv[2];
const bodyRaw = process.argv[3] || '{}';
const optsRaw = process.argv[4] || '{}';

let body;
try {
  body = JSON.parse(bodyRaw);
} catch (e) {
  process.stderr.write(JSON.stringify({code: -100, msg: 'invalid JSON body'}));
  process.exit(1);
}

try {
  JSON.parse(optsRaw);
} catch (e) {
  process.stderr.write(JSON.stringify({code: -100, msg: 'invalid options JSON'}));
  process.exit(1);
}

// mode file in skill_dir parent
const skillDir = path.dirname(process.argv[1]);
const modeFile = path.join(skillDir, '..', 'mode');
let mode = '';
try { mode = fs.readFileSync(modeFile, 'utf8').trim(); } catch (e) {}

function emitResults(results) {
  process.stdout.write(JSON.stringify({code: 0, msg: 'success', data: {results}}));
}

function ok(url, mediaId) {
  return {url, ret_code: 0, media_id: mediaId || ('m_' + Buffer.from(url).toString('base64').slice(0, 8))};
}

function fail(url, ret_code, msg) {
  return {url, ret_code, msg};
}

function classifyByURL(url) {
  const last = url.split('/').pop();
  if (last.startsWith('ok')) return ok(url);
  if (last.startsWith('rate')) return fail(url, 400, '110021 频率限制');
  if (last.startsWith('bad')) return fail(url, 400, 'url 不合法');
  if (last.startsWith('err')) return fail(url, 500, 'unknown');
  return ok(url);
}

let results;
if (mode === 'all-ok') {
  results = {};
  for (const u of (body.urls || [])) results[u] = ok(u);
  emitResults(results);
} else if (mode === 'all-rate') {
  process.stdout.write(JSON.stringify({code: 110021, msg: '频率限制'}));
} else if (mode === 'all-cred') {
  process.stderr.write(JSON.stringify({code: -100, msg: '未找到 IMA 凭证（clientId / apiKey）'}));
  process.exit(1);
} else if (mode === 'all-skillup') {
  process.stderr.write(JSON.stringify({code: -200, msg: '发现新版本 skill：1.2.0'}));
  process.exit(1);
} else if (mode === 'all-dep') {
  process.stderr.write(JSON.stringify({code: -100, msg: 'imaskai not found at /nonexistent'}));
  process.exit(1);
} else if (mode === 'api-mismatch') {
  // 验证 apiPath 是否被正确传入
  if (apiPath !== 'openapi/wiki/v1/import_urls') {
    process.stderr.write(JSON.stringify({code: -100, msg: 'unexpected apiPath: ' + apiPath}));
    process.exit(1);
  }
  results = {};
  for (const u of (body.urls || [])) results[u] = ok(u);
  emitResults(results);
} else {
  results = {};
  for (const u of (body.urls || [])) results[u] = classifyByURL(u);
  emitResults(results);
}
`

// writeFakeIMASkill 在临时目录写入 imaskai 替身 + ima_api.cjs
func writeFakeIMASkill(t *testing.T) (skillDir string, modeFile string) {
	t.Helper()
	root := t.TempDir()
	skillDir = filepath.Join(root, "ima")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	apiPath := filepath.Join(skillDir, "ima_api.cjs")
	if err := os.WriteFile(apiPath, []byte(fakeIMAScript), 0o755); err != nil {
		t.Fatalf("write api: %v", err)
	}
	modeFile = filepath.Join(root, "mode")
	return skillDir, modeFile
}

func setIMAMode(t *testing.T, modeFile, mode string) {
	t.Helper()
	if mode == "" {
		_ = os.Remove(modeFile)
		return
	}
	if err := os.WriteFile(modeFile, []byte(mode), 0o644); err != nil {
		t.Fatalf("write mode: %v", err)
	}
}

func TestPushAllSucceed(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node binary not available")
	}
	skillDir, _ := writeFakeIMASkill(t)
	exp, err := New(Config{SkillDir: skillDir, KnowledgeBaseID: "kb1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	urls := []string{
		"https://mp.weixin.qq.com/s/ok-1",
		"https://mp.weixin.qq.com/s/ok-2",
	}
	results, err := exp.Push(context.Background(), urls)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for _, r := range results {
		if r.MediaID == "" {
			t.Errorf("%s: empty media id", r.URL)
		}
		if r.Kind != "" {
			t.Errorf("%s: Kind = %q, want empty on success", r.URL, r.Kind)
		}
	}
}

func TestPushCredentialError(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node binary not available")
	}
	skillDir, modeFile := writeFakeIMASkill(t)
	setIMAMode(t, modeFile, "all-cred")
	exp, _ := New(Config{SkillDir: skillDir, KnowledgeBaseID: "kb1"})

	urls := []string{"https://mp.weixin.qq.com/s/1", "https://mp.weixin.qq.com/s/2"}
	results, err := exp.Push(context.Background(), urls)
	if err == nil {
		t.Fatal("expected error")
	}
	var pe *PushError
	if !asError(err, &pe) {
		t.Fatalf("expected *PushError, got %T", err)
	}
	if pe.Kind != KindCredential {
		t.Errorf("Kind = %q, want %q", pe.Kind, KindCredential)
	}
	// 进程层失败：所有 URL 都按同一 kind 标失败
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for _, r := range results {
		if r.Kind != KindCredential {
			t.Errorf("%s: Kind = %q, want %q", r.URL, r.Kind, KindCredential)
		}
	}
}

func TestPushSkillUpdateError(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node binary not available")
	}
	skillDir, modeFile := writeFakeIMASkill(t)
	setIMAMode(t, modeFile, "all-skillup")
	exp, _ := New(Config{SkillDir: skillDir, KnowledgeBaseID: "kb1"})

	_, err := exp.PushOne(context.Background(), "https://mp.weixin.qq.com/s/1")
	if err == nil {
		t.Fatal("expected error")
	}
	var pe *PushError
	if !asError(err, &pe) {
		t.Fatalf("expected *PushError, got %T", err)
	}
	if pe.Kind != KindSkillUpdate {
		t.Errorf("Kind = %q, want %q", pe.Kind, KindSkillUpdate)
	}
}

func TestPushDependencyError(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node binary not available")
	}
	skillDir, modeFile := writeFakeIMASkill(t)
	setIMAMode(t, modeFile, "all-dep")
	exp, _ := New(Config{SkillDir: skillDir, KnowledgeBaseID: "kb1"})

	_, err := exp.PushOne(context.Background(), "https://mp.weixin.qq.com/s/1")
	if err == nil {
		t.Fatal("expected error")
	}
	var pe *PushError
	if !asError(err, &pe) {
		t.Fatalf("expected *PushError, got %T", err)
	}
	if pe.Kind != KindDependency {
		t.Errorf("Kind = %q, want %q", pe.Kind, KindDependency)
	}
}

func TestPushRateLimited(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node binary not available")
	}
	skillDir, modeFile := writeFakeIMASkill(t)
	setIMAMode(t, modeFile, "all-rate")
	exp, _ := New(Config{SkillDir: skillDir, KnowledgeBaseID: "kb1"})

	results, err := exp.Push(context.Background(), []string{"u1", "u2"})
	// 频控是业务层错误（stdout code != 0），不返回 error
	if err != nil {
		t.Fatalf("expected no error for business-level rate limit, got: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for _, r := range results {
		if r.Kind != KindRateLimited {
			t.Errorf("%s: Kind = %q, want %q", r.URL, r.Kind, KindRateLimited)
		}
	}
}

func TestPushPartialSuccess(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node binary not available")
	}
	skillDir, _ := writeFakeIMASkill(t)
	exp, _ := New(Config{SkillDir: skillDir, KnowledgeBaseID: "kb1"})

	urls := []string{
		"https://mp.weixin.qq.com/s/ok-1",  // ok
		"https://mp.weixin.qq.com/s/bad-1", // invalid
		"https://mp.weixin.qq.com/s/ok-2",  // ok
	}
	results, err := exp.Push(context.Background(), urls)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	byURL := make(map[string]PushResult, len(results))
	for _, r := range results {
		byURL[r.URL] = r
	}
	if r := byURL["https://mp.weixin.qq.com/s/ok-1"]; r.Kind != "" || r.MediaID == "" {
		t.Errorf("ok-1: kind=%q mediaID=%q, want empty+non-empty", r.Kind, r.MediaID)
	}
	if r := byURL["https://mp.weixin.qq.com/s/bad-1"]; r.Kind != KindInvalidURL || r.MediaID != "" {
		t.Errorf("bad-1: kind=%q mediaID=%q, want invalid_url+empty", r.Kind, r.MediaID)
	}
	if r := byURL["https://mp.weixin.qq.com/s/ok-2"]; r.Kind != "" || r.MediaID == "" {
		t.Errorf("ok-2: kind=%q mediaID=%q, want empty+non-empty", r.Kind, r.MediaID)
	}
}

func TestPushBatchingBy10(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node binary not available")
	}
	skillDir, modeFile := writeFakeIMASkill(t)
	// mode = "api-mismatch" 时替身会校验 apiPath，但**也会写所有 URL 的 result**。
	// 这里改用 hook 计数：跑 22 个 URL，替身在每次被调用时往 counter 文件 +1。
	setIMAMode(t, modeFile, "")

	// 写一个会自增计数器的替身（覆盖之前的 fakeIMAScript）
	root := filepath.Dir(skillDir)
	countFile := filepath.Join(root, "calls")
	counter := `#!/usr/bin/env node
'use strict';
const fs = require('fs');
try { fs.appendFileSync(` + "`" + countFile + "`" + `, 'x'); } catch (e) {}
const body = JSON.parse(process.argv[3] || '{}');
const results = {};
for (const u of (body.urls || [])) results[u] = {url: u, ret_code: 0, media_id: 'm_' + u};
process.stdout.write(JSON.stringify({code: 0, msg: 'success', data: {results}}));
`
	if err := os.WriteFile(filepath.Join(skillDir, "ima_api.cjs"), []byte(counter), 0o755); err != nil {
		t.Fatalf("write counter api: %v", err)
	}

	exp, _ := New(Config{SkillDir: skillDir, KnowledgeBaseID: "kb1"})
	urls := make([]string, 22)
	for i := range urls {
		urls[i] = "https://mp.weixin.qq.com/s/" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
	}

	results, err := exp.Push(context.Background(), urls)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(results) != 22 {
		t.Fatalf("results = %d, want 22", len(results))
	}

	raw, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if got := len(raw); got != 3 {
		t.Errorf("node 被调用 %d 次，期望 3 次（22 个 URL / 单批 10 = 10+10+2）", got)
	}
}

func TestPushAPIPathPassedCorrectly(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node binary not available")
	}
	skillDir, modeFile := writeFakeIMASkill(t)
	setIMAMode(t, modeFile, "api-mismatch")
	exp, _ := New(Config{SkillDir: skillDir, KnowledgeBaseID: "kb1"})

	_, err := exp.PushOne(context.Background(), "https://mp.weixin.qq.com/s/1")
	if err != nil {
		t.Errorf("PushOne: %v", err)
	}
}

func TestPushBodyAndOptsFormat(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node binary not available")
	}
	skillDir, modeFile := writeFakeIMASkill(t)
	// 自定义替身：把 body / opts 写到文件供 Go 侧断言
	root := filepath.Dir(skillDir)
	bodyFile := filepath.Join(root, "body.json")
	optsFile := filepath.Join(root, "opts.json")
	hook := `#!/usr/bin/env node
'use strict';
const fs = require('fs');
fs.writeFileSync(` + "`" + bodyFile + "`" + `, process.argv[3] || '{}');
fs.writeFileSync(` + "`" + optsFile + "`" + `, process.argv[4] || '{}');
process.stdout.write(JSON.stringify({code: 0, data: {results: {}}}));
`
	if err := os.WriteFile(filepath.Join(skillDir, "ima_api.cjs"), []byte(hook), 0o755); err != nil {
		t.Fatalf("write hook api: %v", err)
	}
	setIMAMode(t, modeFile, "")

	exp, _ := New(Config{SkillDir: skillDir, KnowledgeBaseID: "kb-id-1", FolderID: "folder-1"})
	_, err := exp.PushOne(context.Background(), "https://mp.weixin.qq.com/s/abc")
	if err != nil {
		t.Fatalf("PushOne: %v", err)
	}

	bodyRaw, err := os.ReadFile(bodyFile)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var body struct {
		KnowledgeBaseID string   `json:"knowledge_base_id"`
		FolderID        string   `json:"folder_id"`
		URLs            []string `json:"urls"`
	}
	if err := json.Unmarshal(bodyRaw, &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if body.KnowledgeBaseID != "kb-id-1" {
		t.Errorf("knowledge_base_id = %q, want %q", body.KnowledgeBaseID, "kb-id-1")
	}
	if body.FolderID != "folder-1" {
		t.Errorf("folder_id = %q, want %q", body.FolderID, "folder-1")
	}
	if len(body.URLs) != 1 || body.URLs[0] != "https://mp.weixin.qq.com/s/abc" {
		t.Errorf("urls = %v, want [https://mp.weixin.qq.com/s/abc]", body.URLs)
	}

	optsRaw, err := os.ReadFile(optsFile)
	if err != nil {
		t.Fatalf("read opts: %v", err)
	}
	// opts 应该是个合法 JSON 对象（即便内容为空）
	var opts map[string]any
	if err := json.Unmarshal(optsRaw, &opts); err != nil {
		t.Errorf("opts 不是合法 JSON: %v (%s)", err, optsRaw)
	}
}

func TestPushTimeout(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node binary not available")
	}
	if runtime.GOOS == "windows" {
		t.Skip("依赖 unix 进程组清理")
	}
	skillDir, _ := writeFakeIMASkill(t)

	// 写一个永远不返回的替身（不退出，持有 stdout）
	hanger := `#!/usr/bin/env node
'use strict';
// 不写 stdout、不退出 —— 等着被超时杀掉
setInterval(() => {}, 1000);
`
	if err := os.WriteFile(filepath.Join(skillDir, "ima_api.cjs"), []byte(hanger), 0o755); err != nil {
		t.Fatalf("write hanger: %v", err)
	}

	exp, _ := New(Config{
		SkillDir:        skillDir,
		KnowledgeBaseID: "kb1",
		Timeout:         500 * time.Millisecond,
	})

	done := make(chan error, 1)
	go func() {
		_, err := exp.PushOne(context.Background(), "https://mp.weixin.qq.com/s/1")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected timeout error")
		}
		var pe *PushError
		if !asError(err, &pe) {
			t.Fatalf("expected *PushError, got %T: %v", err, err)
		}
		if pe.Kind != KindTimeout {
			t.Errorf("Kind = %q, want %q", pe.Kind, KindTimeout)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Push 超时后仍未返回 —— 进程组清理可能没生效")
	}
}

// helpers ----------

func asError(err error, target **PushError) bool {
	if err == nil {
		return false
	}
	if pe, ok := err.(*PushError); ok {
		*target = pe
		return true
	}
	return false
}
