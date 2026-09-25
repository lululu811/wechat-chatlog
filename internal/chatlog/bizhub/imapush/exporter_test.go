package imapush

import (
	"errors"
	"strings"
	"testing"
)

func TestNew_RequiresSkillDir(t *testing.T) {
	_, err := New(Config{KnowledgeBaseID: "kb1"})
	if err == nil || !strings.Contains(err.Error(), "skill dir") {
		t.Errorf("expected skill dir required error, got: %v", err)
	}
}

func TestNew_RequiresKBID(t *testing.T) {
	_, err := New(Config{SkillDir: "/tmp"})
	if err == nil || !strings.Contains(err.Error(), "knowledge base") {
		t.Errorf("expected knowledge base id required error, got: %v", err)
	}
}

func TestNew_FolderIDDefaultsToKBID(t *testing.T) {
	exp, err := New(Config{SkillDir: "/tmp", KnowledgeBaseID: "kb1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp.cfg.FolderID != "kb1" {
		t.Errorf("FolderID = %q, expected default to KB ID %q", exp.cfg.FolderID, "kb1")
	}
}

func TestNew_RespectsExplicitFolderID(t *testing.T) {
	exp, err := New(Config{SkillDir: "/tmp", KnowledgeBaseID: "kb1", FolderID: "sub"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp.cfg.FolderID != "sub" {
		t.Errorf("FolderID = %q, expected %q", exp.cfg.FolderID, "sub")
	}
}

func TestFailureKind_NeedsHuman(t *testing.T) {
	cases := []struct {
		kind FailureKind
		want bool
	}{
		{KindCredential, true},
		{KindSkillUpdate, true},
		{KindUpstreamFailed, true},
		{KindInvalidURL, true},
		{KindTimeout, false},
		{KindRateLimited, false},
		{KindScriptFailed, false},
		{KindDependency, true},
		{KindUnknown, false},
	}
	for _, tc := range cases {
		if got := tc.kind.NeedsHuman(); got != tc.want {
			t.Errorf("%s.NeedsHuman() = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestFailureKind_Retryable(t *testing.T) {
	cases := []struct {
		kind FailureKind
		want bool
	}{
		{KindCredential, false},
		{KindSkillUpdate, false},
		{KindUpstreamFailed, false},
		{KindInvalidURL, false},
		{KindTimeout, true},
		{KindRateLimited, true},
		{KindScriptFailed, true},
		{KindDependency, false},
		{KindUnknown, true},
	}
	for _, tc := range cases {
		if got := tc.kind.Retryable(); got != tc.want {
			t.Errorf("%s.Retryable() = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestPermanentKinds_ListStable(t *testing.T) {
	// 名单变化会让 store 层的 SQL 拼接发生兼容性问题。
	// 这里的断言把名单固化下来 —— 改前先想清楚。
	want := []string{"credential", "skill_update", "upstream_failed", "invalid_url", "dependency"}
	got := PermanentKinds()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got: %v)", len(got), len(want), got)
	}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("[%d] = %q, want %q", i, got[i], k)
		}
	}
}

func TestParseKind_EmptyMapsToUnknown(t *testing.T) {
	if got := ParseKind(""); got != KindUnknown {
		t.Errorf("ParseKind(\"\") = %q, want %q", got, KindUnknown)
	}
}

func TestParseKind_UnknownStringPreserved(t *testing.T) {
	if got := ParseKind("something_else"); got != FailureKind("something_else") {
		t.Errorf("ParseKind passthrough failed: %q", got)
	}
}

func TestKindOf_NilAndNonPushError(t *testing.T) {
	if got := KindOf(nil); got != "" {
		t.Errorf("KindOf(nil) = %q, want empty", got)
	}
	if got := KindOf(&PushError{Kind: KindCredential}); got != KindCredential {
		t.Errorf("KindOf(PushError) = %q, want %q", got, KindCredential)
	}
	if got := KindOf(errors.New("plain error")); got != KindUnknown {
		t.Errorf("KindOf(plain) = %q, want %q", got, KindUnknown)
	}
}

func TestClassifyProcessErr_SkillUpdate(t *testing.T) {
	stderr := `{"code":-200,"msg":"发现新版本 skill：1.2.0"}`
	if got := classifyProcessErr(1, stderr, false); got != KindSkillUpdate {
		t.Errorf("got %q, want %q", got, KindSkillUpdate)
	}
}

func TestClassifyProcessErr_Credential(t *testing.T) {
	cases := []string{
		`{"code":-100,"msg":"missing credentials"}`,
		`{"code":-100,"msg":"未找到 IMA 凭证（clientId / apiKey）"}`,
		`{"code":-100,"msg":"凭证缺失"}`,
	}
	for _, s := range cases {
		if got := classifyProcessErr(1, s, false); got != KindCredential {
			t.Errorf("stderr=%s: got %q, want %q", s, got, KindCredential)
		}
	}
}

func TestClassifyProcessErr_Dependency(t *testing.T) {
	cases := []string{
		`{"code":-100,"msg":"imaskai not found"}`,
		`{"code":-100,"msg":"command not found"}`,
		`{"code":-100,"msg":"no such file or directory"}`,
	}
	for _, s := range cases {
		if got := classifyProcessErr(1, s, false); got != KindDependency {
			t.Errorf("stderr=%s: got %q, want %q", s, got, KindDependency)
		}
	}
}

func TestClassifyProcessErr_Timeout(t *testing.T) {
	if got := classifyProcessErr(-1, "", true); got != KindTimeout {
		t.Errorf("timedOut=true: got %q, want %q", got, KindTimeout)
	}
	if got := classifyProcessErr(1, "deadline exceeded", false); got != KindTimeout {
		t.Errorf("timeout keyword: got %q, want %q", got, KindTimeout)
	}
}

func TestClassifyProcessErr_ScriptFailedFallback(t *testing.T) {
	if got := classifyProcessErr(2, "random failure", false); got != KindScriptFailed {
		t.Errorf("got %q, want %q", got, KindScriptFailed)
	}
}

func TestClassifyProcessErr_ExitZeroReturnsUnknown(t *testing.T) {
	if got := classifyProcessErr(0, "", false); got != KindUnknown {
		t.Errorf("got %q, want %q (process layer only classifies when exit != 0)", got, KindUnknown)
	}
}

func TestClassifyBusinessErr_RateLimited(t *testing.T) {
	cases := []string{"请求过于频繁，请稍后重试", "110021", "rate limit", "frequent"}
	for _, m := range cases {
		if got := classifyBusinessErr(110021, m); got != KindRateLimited {
			t.Errorf("msg=%q: got %q, want %q", m, got, KindRateLimited)
		}
	}
}

func TestClassifyBusinessErr_InvalidURL(t *testing.T) {
	cases := []string{"url 不合法", "invalid url", "链接无效"}
	for _, m := range cases {
		if got := classifyBusinessErr(400, m); got != KindInvalidURL {
			t.Errorf("msg=%q: got %q, want %q", m, got, KindInvalidURL)
		}
	}
}

// TestClassifyBusinessErr_KnowledgeBasePriority 钉死"知识库错误优先于 url 不合法"的顺序。
//
// 这是真实踩过的坑：IMA 返 "invalid knowledge_base_id..."，msg 同时含
// "invalid" 和 "knowledge_base"，老的顺序会把 URL 错误识别为 InvalidURL，
// 但实际是知识库配置错 —— 应归到 UpstreamFailed，永久失败，需人改 KB ID。
func TestClassifyBusinessErr_KnowledgeBasePriority(t *testing.T) {
	cases := []string{
		"invalid knowledge_base_id. You MUST correct the value before retrying.",
		"知识库不存在",
		"knowledge_base not found",
		"invalid kb_id format",
		"folder_id not found",
	}
	for _, m := range cases {
		if got := classifyBusinessErr(220004, m); got != KindUpstreamFailed {
			t.Errorf("msg=%q: got %q, want %q (KB 配置错，不应归到 URL 不合法)", m, got, KindUpstreamFailed)
		}
	}
}

func TestClassifyBusinessErr_UpstreamFailed(t *testing.T) {
	cases := []string{"知识库不存在", "knowledge_base not found", "未知业务错误"}
	for _, m := range cases {
		if got := classifyBusinessErr(500, m); got != KindUpstreamFailed {
			t.Errorf("msg=%q: got %q, want %q", m, got, KindUpstreamFailed)
		}
	}
}

func TestClassifyBusinessErr_ZeroIsUnknown(t *testing.T) {
	if got := classifyBusinessErr(0, "anything"); got != KindUnknown {
		t.Errorf("got %q, want %q (code 0 = success, not a failure)", got, KindUnknown)
	}
}

func TestParseImportResponse_BatchLevelError(t *testing.T) {
	stdout := `{"code":110021,"msg":"频率限制"}`
	urls := []string{"https://mp.weixin.qq.com/s/1", "https://mp.weixin.qq.com/s/2"}
	results, err := parseImportResponse(stdout, urls, 0)
	if err != nil {
		t.Fatalf("parseImportResponse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	for i, r := range results {
		if r.URL != urls[i] {
			t.Errorf("[%d].URL = %q, want %q", i, r.URL, urls[i])
		}
		if r.Kind != KindRateLimited {
			t.Errorf("[%d].Kind = %q, want %q", i, r.Kind, KindRateLimited)
		}
		if r.RetCode != 110021 {
			t.Errorf("[%d].RetCode = %d, want 110021", i, r.RetCode)
		}
		if r.MediaID != "" {
			t.Errorf("[%d].MediaID should be empty on failure", i)
		}
	}
}

func TestParseImportResponse_PerURLSuccess(t *testing.T) {
	stdout := `{"code":0,"msg":"success","data":{"results":{
		"https://mp.weixin.qq.com/s/1":{"url":"https://mp.weixin.qq.com/s/1","ret_code":0,"media_id":"m_abc"},
		"https://mp.weixin.qq.com/s/2":{"url":"https://mp.weixin.qq.com/s/2","ret_code":0,"media_id":"m_def"}
	}}}`
	urls := []string{"https://mp.weixin.qq.com/s/1", "https://mp.weixin.qq.com/s/2"}
	results, err := parseImportResponse(stdout, urls, 0)
	if err != nil {
		t.Fatalf("parseImportResponse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	for _, r := range results {
		if r.RetCode != 0 {
			t.Errorf("%s: RetCode = %d, want 0", r.URL, r.RetCode)
		}
		if r.Kind != "" {
			t.Errorf("%s: Kind should be empty on success, got %q", r.URL, r.Kind)
		}
		if r.MediaID == "" {
			t.Errorf("%s: MediaID should be set on success", r.URL)
		}
	}
}

func TestParseImportResponse_PartialSuccess(t *testing.T) {
	stdout := `{"code":0,"msg":"success","data":{"results":{
		"https://mp.weixin.qq.com/s/1":{"url":"https://mp.weixin.qq.com/s/1","ret_code":0,"media_id":"m_abc"},
		"https://mp.weixin.qq.com/s/2":{"url":"https://mp.weixin.qq.com/s/2","ret_code":400,"msg":"url 不合法"}
	}}}`
	urls := []string{"https://mp.weixin.qq.com/s/1", "https://mp.weixin.qq.com/s/2"}
	results, err := parseImportResponse(stdout, urls, 0)
	if err != nil {
		t.Fatalf("parseImportResponse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	byURL := make(map[string]PushResult, len(results))
	for _, r := range results {
		byURL[r.URL] = r
	}
	if r := byURL["https://mp.weixin.qq.com/s/1"]; r.Kind != "" || r.MediaID != "m_abc" {
		t.Errorf("url 1: kind=%q mediaID=%q, want empty+m_abc", r.Kind, r.MediaID)
	}
	if r := byURL["https://mp.weixin.qq.com/s/2"]; r.Kind != KindInvalidURL || r.MediaID != "" {
		t.Errorf("url 2: kind=%q mediaID=%q, want invalid_url+empty", r.Kind, r.MediaID)
	}
}

func TestParseImportResponse_MissingURLsAreUnknown(t *testing.T) {
	stdout := `{"code":0,"msg":"success","data":{"results":{
		"https://mp.weixin.qq.com/s/1":{"url":"https://mp.weixin.qq.com/s/1","ret_code":0,"media_id":"m_abc"}
	}}}`
	urls := []string{"https://mp.weixin.qq.com/s/1", "https://mp.weixin.qq.com/s/2"}
	results, err := parseImportResponse(stdout, urls, 0)
	if err != nil {
		t.Fatalf("parseImportResponse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (missing URL filled with unknown)", len(results))
	}
	have2 := false
	for _, r := range results {
		if r.URL == "https://mp.weixin.qq.com/s/2" {
			have2 = true
			if r.Kind != KindUnknown {
				t.Errorf("missing URL kind = %q, want %q", r.Kind, KindUnknown)
			}
		}
	}
	if !have2 {
		t.Errorf("missing URL not present in results")
	}
}

func TestParseImportResponse_InvalidJSONReturnsError(t *testing.T) {
	_, err := parseImportResponse("not json", []string{"x"}, 0)
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
	var pe *PushError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PushError, got %T", err)
	}
	if pe.Kind != KindScriptFailed {
		t.Errorf("Kind = %q, want %q", pe.Kind, KindScriptFailed)
	}
}

func TestBuildImportBody(t *testing.T) {
	body, err := buildImportBody("kb1", "folder1", []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("buildImportBody: %v", err)
	}
	for _, want := range []string{`"knowledge_base_id":"kb1"`, `"folder_id":"folder1"`, `"u1"`, `"u2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

func TestCheck_MissingSkillDir(t *testing.T) {
	exp, _ := New(Config{SkillDir: "/nonexistent-skill-dir-xyz", KnowledgeBaseID: "kb1"})
	err := exp.Check()
	if err == nil {
		t.Fatal("expected error for missing skill dir")
	}
	var pe *PushError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PushError, got %T", err)
	}
	if pe.Kind != KindDependency {
		t.Errorf("Kind = %q, want %q", pe.Kind, KindDependency)
	}
}

func TestCheck_EmptyKBID(t *testing.T) {
	dir := t.TempDir()
	exp := &Exporter{cfg: Config{SkillDir: dir, KnowledgeBaseID: ""}}
	err := exp.Check()
	if err == nil {
		t.Fatal("expected error for empty KB ID")
	}
}
