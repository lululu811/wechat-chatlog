package bizhub

import (
	"sort"
	"testing"
)

// newTestStore 在临时目录上打开一个 store，测试结束自动清理。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// seedTagStore 建一个含 2 个公众号、3 个标签的 store。
// 返回的 ids 按创建顺序排列，下标 0 对应「第 1 个标签」。
func seedTagStore(t *testing.T) (*Store, []int64) {
	t.Helper()

	s := newTestStore(t)
	if err := s.UpsertAccounts([]Account{
		{GHID: "gh_1", GHName: "甲号"},
		{GHID: "gh_2", GHName: "乙号"},
	}); err != nil {
		t.Fatalf("UpsertAccounts: %v", err)
	}

	names := []string{"AI", "投资", "产品"}
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		tag, err := s.CreateTag(name, "#000000")
		if err != nil {
			t.Fatalf("CreateTag(%s): %v", name, err)
		}
		ids = append(ids, tag.ID)
	}
	return s, ids
}

// pick 把 1-based 的标签序号转换为真实标签 id，便于书写用例。
func pick(ids []int64, idx ...int) []int64 {
	out := make([]int64, 0, len(idx))
	for _, i := range idx {
		out = append(out, ids[i-1])
	}
	return out
}

// tagIdxSet 读取账号当前标签，转换成在 seed 标签中的序号集合。
func tagIdxSet(t *testing.T, s *Store, ids []int64, ghid string) map[int]bool {
	t.Helper()

	tags, err := s.GetAccountTags(ghid)
	if err != nil {
		t.Fatalf("GetAccountTags(%s): %v", ghid, err)
	}
	idxOf := make(map[int64]int, len(ids))
	for i, id := range ids {
		idxOf[id] = i + 1
	}

	out := make(map[int]bool, len(tags))
	for _, tg := range tags {
		i, ok := idxOf[tg.ID]
		if !ok {
			t.Fatalf("账号 %s 出现了 seed 之外的标签 id=%d", ghid, tg.ID)
		}
		out[i] = true
	}
	return out
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func TestSetAccountsTagsMode(t *testing.T) {
	cases := []struct {
		name    string
		initial map[string][]int // ghid -> 初始标签序号
		mode    TagAssignMode
		ghids   []string
		tags    []int // 本次操作的标签序号
		want    map[string][]int
	}{
		{
			name:    "add 保留已有标签并追加新标签",
			initial: map[string][]int{"gh_1": {1, 2}},
			mode:    TagModeAdd,
			ghids:   []string{"gh_1"},
			tags:    []int{3},
			want:    map[string][]int{"gh_1": {1, 2, 3}},
		},
		{
			name:    "add 重复添加已有标签不报错也不产生重复",
			initial: map[string][]int{"gh_1": {1, 2}},
			mode:    TagModeAdd,
			ghids:   []string{"gh_1"},
			tags:    []int{1},
			want:    map[string][]int{"gh_1": {1, 2}},
		},
		{
			name:    "add 对无标签账号直接写入",
			initial: map[string][]int{},
			mode:    TagModeAdd,
			ghids:   []string{"gh_1"},
			tags:    []int{1, 2},
			want:    map[string][]int{"gh_1": {1, 2}},
		},
		{
			name:    "remove 只移除指定标签并保留其余",
			initial: map[string][]int{"gh_1": {1, 2, 3}},
			mode:    TagModeRemove,
			ghids:   []string{"gh_1"},
			tags:    []int{2},
			want:    map[string][]int{"gh_1": {1, 3}},
		},
		{
			name:    "remove 移除未持有的标签不产生副作用",
			initial: map[string][]int{"gh_1": {1}},
			mode:    TagModeRemove,
			ghids:   []string{"gh_1"},
			tags:    []int{2, 3},
			want:    map[string][]int{"gh_1": {1}},
		},
		{
			name:    "replace 清空后写入新标签",
			initial: map[string][]int{"gh_1": {1, 2}},
			mode:    TagModeReplace,
			ghids:   []string{"gh_1"},
			tags:    []int{3},
			want:    map[string][]int{"gh_1": {3}},
		},
		{
			name:    "replace 传入空列表即清空全部标签",
			initial: map[string][]int{"gh_1": {1, 2}},
			mode:    TagModeReplace,
			ghids:   []string{"gh_1"},
			tags:    nil,
			want:    map[string][]int{"gh_1": {}},
		},
		{
			name:    "批量 add 逐个追加且不影响未被操作的账号",
			initial: map[string][]int{"gh_1": {1}, "gh_2": {2}},
			mode:    TagModeAdd,
			ghids:   []string{"gh_1", "gh_2"},
			tags:    []int{3},
			want:    map[string][]int{"gh_1": {1, 3}, "gh_2": {2, 3}},
		},
		{
			name:    "批量 replace 各账号独立覆盖",
			initial: map[string][]int{"gh_1": {1}, "gh_2": {2, 3}},
			mode:    TagModeReplace,
			ghids:   []string{"gh_1", "gh_2"},
			tags:    []int{3},
			want:    map[string][]int{"gh_1": {3}, "gh_2": {3}},
		},
		{
			name:    "批量 remove 只影响被点名的账号",
			initial: map[string][]int{"gh_1": {1, 2}, "gh_2": {1, 2}},
			mode:    TagModeRemove,
			ghids:   []string{"gh_1"},
			tags:    []int{1},
			want:    map[string][]int{"gh_1": {2}, "gh_2": {1, 2}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ids := seedTagStore(t)

			for ghid, idxs := range tc.initial {
				if err := s.SetAccountsTagsMode([]string{ghid}, pick(ids, idxs...), TagModeReplace); err != nil {
					t.Fatalf("准备初始标签失败: %v", err)
				}
			}

			if err := s.SetAccountsTagsMode(tc.ghids, pick(ids, tc.tags...), tc.mode); err != nil {
				t.Fatalf("SetAccountsTagsMode: %v", err)
			}

			for ghid, wantIdx := range tc.want {
				want := make(map[int]bool, len(wantIdx))
				for _, i := range wantIdx {
					want[i] = true
				}
				got := tagIdxSet(t, s, ids, ghid)
				if len(got) != len(want) {
					t.Fatalf("账号 %s 标签 = %v，期望 %v", ghid, sortedKeys(got), sortedKeys(want))
				}
				for k := range want {
					if !got[k] {
						t.Fatalf("账号 %s 标签 = %v，期望 %v", ghid, sortedKeys(got), sortedKeys(want))
					}
				}
			}
		})
	}
}

// TestSetAccountTagsKeepsReplaceSemantics 锁定旧接口的兼容行为：
// 单账号 SetAccountTags 仍然是覆盖式，避免已上线的调用方语义被静默改变。
func TestSetAccountTagsKeepsReplaceSemantics(t *testing.T) {
	s, ids := seedTagStore(t)

	if err := s.SetAccountTags("gh_1", pick(ids, 1, 2)); err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	if err := s.SetAccountTags("gh_1", pick(ids, 3)); err != nil {
		t.Fatalf("覆盖写入: %v", err)
	}

	got := tagIdxSet(t, s, ids, "gh_1")
	if len(got) != 1 || !got[3] {
		t.Fatalf("SetAccountTags 应为覆盖语义，实际标签 = %v，期望 [3]", sortedKeys(got))
	}
}

func TestSetAccountsTagsModeRejectsInvalidMode(t *testing.T) {
	s, ids := seedTagStore(t)

	if err := s.SetAccountsTagsMode([]string{"gh_1"}, pick(ids, 1), TagAssignMode("append")); err == nil {
		t.Fatal("非法模式应当返回错误")
	}

	// 非法模式不应写入任何数据
	got := tagIdxSet(t, s, ids, "gh_1")
	if len(got) != 0 {
		t.Fatalf("非法模式不应写入标签，实际 = %v", sortedKeys(got))
	}
}

func TestSetAccountsTagsModeEmptyTargets(t *testing.T) {
	s, ids := seedTagStore(t)

	if err := s.SetAccountsTagsMode(nil, pick(ids, 1), TagModeAdd); err != nil {
		t.Fatalf("空账号列表应当直接返回 nil，实际: %v", err)
	}
}

func TestParseTagAssignMode(t *testing.T) {
	cases := []struct {
		in      string
		want    TagAssignMode
		wantErr bool
	}{
		{in: "", want: TagModeAdd},
		{in: "add", want: TagModeAdd},
		{in: "ADD", want: TagModeAdd},
		{in: " add ", want: TagModeAdd},
		{in: "remove", want: TagModeRemove},
		{in: "REPLACE", want: TagModeReplace},
		{in: "replace", want: TagModeReplace},
		{in: "append", wantErr: true},
		{in: "delete", wantErr: true},
		{in: "overwrite", wantErr: true},
	}

	for _, tc := range cases {
		got, err := ParseTagAssignMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseTagAssignMode(%q) 期望报错，实际返回 %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTagAssignMode(%q) 意外报错: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseTagAssignMode(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestExportStatsPendingMatchesBatchSelection 「待导出」必须等于批量导出真正会取到的条数。
//
// 两个查询分处不同函数，很容易只改一个：显示 pending 的那个忘了排除已隐藏账号，
// 于是页面说「待导出 5 篇」、点下去只处理 3 篇 —— 差的那两篇没有任何提示，
// 用户只会当成导出漏了。
func TestExportStatsPendingMatchesBatchSelection(t *testing.T) {
	s, byURL := seedExportStore(t)

	stats, err := s.GetExportStats(30)
	if err != nil {
		t.Fatalf("GetExportStats: %v", err)
	}

	cands, err := s.GetUnexportedArticles(30, 100)
	if err != nil {
		t.Fatalf("GetUnexportedArticles: %v", err)
	}

	if stats.Pending != len(cands) {
		t.Errorf("待导出 %d 篇，但批量导出实际只会处理 %d 篇 —— 两条查询的过滤条件不一致",
			stats.Pending, len(cands))
	}
	// 乙号已隐藏，它的那篇不该出现在候选里
	if stats.Pending != 2 {
		t.Errorf("待导出 = %d，期望 2（甲号 2 篇；乙号已隐藏不计）", stats.Pending)
	}
	if len(byURL) != 2 {
		t.Fatalf("种子数据异常：可见文章应 2 篇，实际 %d 篇", len(byURL))
	}
	if stats.Exported != 0 {
		t.Errorf("已归档 = %d，期望 0", stats.Exported)
	}
}
