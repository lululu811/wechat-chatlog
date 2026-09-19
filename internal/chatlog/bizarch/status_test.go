package bizarch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLooksLikeArticleFilename(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		// Positive cases — produced by the recommended Clipper template.
		{"付鹏的财经世界-2026-09-07-【40分钟播客】贝森特", true},
		{"Author-2026-01-01-Simple Title", true},
		{"X-2026-12-31-2026 year end review", true},
		// Negative cases.
		{"", false},
		{"README", false},
		{"notes", false},
		{"Author-26-09-07-Title", false},   // year is 2-digit
		{"Author-2026-9-07-Title", false},  // month is single-digit
		{"Author-2026/09/07-Title", false}, // wrong separator
		{"Author2026-13-07-Title", false},  // invalid month
		{"Author2026-09-32-Title", false},  // invalid day
	}
	for _, c := range cases {
		if got := looksLikeArticleFilename(c.in); got != c.want {
			t.Errorf("looksLikeArticleFilename(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCountVaultArticles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Mix of files: 2 should match the article pattern, 3 should not.
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("公众号/Author-2026-09-07-文章一.md", "# article 1")
	write("公众号/Author-2026-09-08-文章二.md", "# article 2")
	write("公众号/Author-2026-09-09-文章三.md", "# article 3")
	write("公众号/draft.md", "draft")                                  // no date
	write("README.md", "readme")                                       // no date
	write("公众号/Author-2026-09-07-文章一.txt", "not even markdown") // wrong ext

	if got := countVaultArticles(dir); got != 3 {
		t.Errorf("countVaultArticles = %d, want 3", got)
	}
}

func TestCountVaultArticles_EmptyPath(t *testing.T) {
	t.Parallel()
	if got := countVaultArticles(""); got != 0 {
		t.Errorf("empty path: got %d, want 0", got)
	}
}

func TestCountVaultArticles_NonExistentPath(t *testing.T) {
	t.Parallel()
	if got := countVaultArticles("/nonexistent/path/xyz"); got != 0 {
		t.Errorf("nonexistent path: got %d, want 0", got)
	}
}

func TestBuildStatus_ShapeIsJSONFriendly(t *testing.T) {
	t.Parallel()
	r := BuildStatus("/vault", "/archive.db", 42, 7)
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Must round-trip cleanly.
	var back StatusReport
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.ArchiveLog != 42 || back.Pending != 7 {
		t.Errorf("round-trip values lost: %+v", back)
	}
}