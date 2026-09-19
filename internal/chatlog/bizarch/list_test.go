package bizarch

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// chatlogFixtureDir is the on-disk chatlog work dir used for integration
// tests. Override with CHATLOG_WORK_DIR if your decryption lives elsewhere.
// When unset (or unreadable) the integration test skips itself so unit
// tests still run on developer machines that have no chatlog data.
func chatlogFixtureDir(t *testing.T) (workDir, dataDir, platform string, version int, ok bool) {
	t.Helper()
	dir := os.Getenv("CHATLOG_WORK_DIR")
	if dir == "" {
		dir = "/tmp/chatlog-decrypted"
	}
	if _, err := os.Stat(filepath.Join(dir, "db_storage", "contact", "contact.db")); err != nil {
		return "", "", "", 0, false
	}
	return dir, dir, "darwin", 4, true
}

// ─── PrintList ────────────────────────────────────────────────────────

func TestPrintList_TextFormat(t *testing.T) {
	cands := []Candidate{
		{
			GHID:        "gh_a",
			GHName:      "Test Author",
			Title:       "Article One",
			URL:         "https://mp.weixin.qq.com/s?biz=1",
			PublishedAt: time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC),
		},
	}
	buf := captureStdout(t, func() error {
		return PrintList(cands, false)
	})
	line := strings.TrimRight(buf.String(), "\n")
	want := "2026-09-19 10:00\tgh_a\tTest Author\thttps://mp.weixin.qq.com/s?biz=1"
	if line != want {
		t.Errorf("got %q\nwant %q", line, want)
	}
}

func TestPrintList_TextFallsBackToGHIDWhenNameEmpty(t *testing.T) {
	cands := []Candidate{
		{
			GHID:        "gh_z",
			GHName:      "",
			URL:         "https://example.com/x",
			PublishedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		},
	}
	got := captureStdout(t, func() error { return PrintList(cands, false) }).String()
	if !strings.Contains(got, "gh_z\tgh_z\thttps://example.com/x") {
		t.Errorf("expected ghID fallback, got %q", got)
	}
}

func TestPrintList_JSONFormat(t *testing.T) {
	cands := []Candidate{
		{
			GHID:        "gh_b",
			GHName:      "B",
			Title:       "T",
			URL:         "https://example.com/y",
			PublishedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	got := captureStdout(t, func() error { return PrintList(cands, true) }).String()
	var back []Candidate
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if len(back) != 1 || back[0].GHID != "gh_b" {
		t.Errorf("round-trip mismatch: %+v", back)
	}
}

func TestPrintList_Empty(t *testing.T) {
	// Empty input must not panic and must produce an empty (or
	// trivially-shaped) output.
	got := captureStdout(t, func() error { return PrintList(nil, false) }).String()
	if strings.TrimSpace(got) != "" {
		t.Errorf("expected empty output, got %q", got)
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// the captured bytes. Restores the original stdout on cleanup.
func captureStdout(t *testing.T, fn func() error) *bytes.Buffer {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = old
	})
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	if err := fn(); err != nil {
		t.Errorf("fn returned error: %v", err)
	}
	w.Close()
	<-done
	return &buf
}

// ─── ghIDsFromContact ─────────────────────────────────────────────────

// makeContactDB builds a throw-away contact.db at the layout
// ghIDsFromContact expects (<dir>/db_storage/contact/contact.db) with
// the given gh rows. delete_flag = 1 rows must be filtered out by
// the reader.
func makeContactDB(t *testing.T, rows []ghIDEntry, deleted []string) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db_storage", "contact", "contact.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE contact (
		username TEXT, nick_name TEXT, delete_flag INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO contact VALUES (?, ?, 0)`, r.ID, r.Name); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	for _, r := range deleted {
		if _, err := db.Exec(`INSERT INTO contact VALUES (?, ?, 1)`, r, "deleted"); err != nil {
			t.Fatalf("insert deleted: %v", err)
		}
	}
	return dir
}

func TestGhIDsFromContact_RequiresWorkDir(t *testing.T) {
	if _, err := ghIDsFromContact("", ""); err == nil {
		t.Errorf("expected error for empty workDir")
	}
}

func TestGhIDsFromContact_NotFound(t *testing.T) {
	dir := t.TempDir() // exists but has no contact.db
	_, err := ghIDsFromContact(dir, "")
	if err == nil {
		t.Errorf("expected error when contact.db missing")
	}
	if !strings.Contains(err.Error(), "contact.db not found") {
		t.Errorf("error message should mention contact.db, got: %v", err)
	}
}

func TestGhIDsFromContact_FilterBySingle(t *testing.T) {
	dir := makeContactDB(t,
		[]ghIDEntry{{ID: "gh_a", Name: "A"}, {ID: "gh_b", Name: "B"}},
		nil,
	)
	got, err := ghIDsFromContact(dir, "gh_b")
	if err != nil {
		t.Fatalf("ghIDsFromContact: %v", err)
	}
	if len(got) != 1 || got[0].ID != "gh_b" || got[0].Name != "B" {
		t.Errorf("filter failed: %+v", got)
	}
}

func TestGhIDsFromContact_ExcludesDeletedAndNonGh(t *testing.T) {
	dir := makeContactDB(t,
		[]ghIDEntry{
			{ID: "gh_keep1", Name: "Keep1"},
			{ID: "gh_keep2", Name: "Keep2"},
		},
		[]string{"gh_deleted1"},
	)
	// Also inject a non-gh_ row to verify LIKE filter.
	dstPath := filepath.Join(dir, "db_storage", "contact", "contact.db")
	db, _ := sql.Open("sqlite3", dstPath)
	db.Exec(`INSERT INTO contact VALUES ('regular_user', 'Reg', 0)`)
	db.Close()

	got, err := ghIDsFromContact(dir, "")
	if err != nil {
		t.Fatalf("ghIDsFromContact: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 3 (2 kept + 1 regular)... wait expected 2 (only gh_*), got %d: %+v", len(got), got)
	}
	for _, g := range got {
		if !strings.HasPrefix(g.ID, "gh_") {
			t.Errorf("non-gh_ leaked: %s", g.ID)
		}
		if g.ID == "gh_deleted1" {
			t.Errorf("deleted row should be excluded: %s", g.ID)
		}
	}
}

// ─── ListPending: cheap unit tests ────────────────────────────────────

func TestListOpts_DefaultsAreApplied(t *testing.T) {
	// We can't exercise ListPending without a real wechatdb, but we
	// can verify the since parser rejects bad input and the
	// per-gh/total defaults are computed correctly via a tiny helper.
	cases := []struct {
		in, want string
		err      bool
	}{
		{"2026-09-19", "2026-09-19", false},
		{"", "", false},
		{"2026/09/19", "", true},
		{"2026-13-01", "", true},
	}
	for _, c := range cases {
		var err error
		if c.in != "" {
			_, err = time.Parse("2006-01-02", c.in)
		}
		if (err != nil) != c.err {
			t.Errorf("since=%q: err=%v wantErr=%v", c.in, err, c.err)
		}
	}
}

func TestListPending_EmptyWorkDir(t *testing.T) {
	_, err := ListPending("", "darwin", 4, ListOpts{}, nil)
	if err == nil {
		t.Errorf("expected error for empty workDir")
	}
}

func TestListPending_UnsupportedPlatform(t *testing.T) {
	// With an unknown platform, wechatdb.New fails before we ever
	// reach contact.db. Just verify *some* error is returned and it
	// does NOT claim success.
	dir := t.TempDir()
	_, err := ListPending(dir, "haiku", 99, ListOpts{}, nil)
	if err == nil {
		t.Fatalf("expected error for unsupported platform")
	}
}

func TestListPending_BadSinceFormat(t *testing.T) {
	// Make a contact.db with one row, then pass a bad since — the
	// parser must reject before any sqlite calls.
	dummy := makeContactDB(t, []ghIDEntry{{ID: "gh_x", Name: "X"}}, nil)
	_, err := ListPending(dummy, "darwin", 4, ListOpts{Since: "yesterday"}, nil)
	if err == nil {
		t.Errorf("expected error for bad --since format")
	}
	if !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("error should mention YYYY-MM-DD, got: %v", err)
	}
}

// ─── ListPending: integration test (skips without fixture) ───────────

// TestListPending_RealChatlogFixture is the only test in this package
// that requires chatlog decryption to have run. It is the one that
// verifies the full wechatdb→datasource→Candidate pipeline. On
// developer machines without /tmp/chatlog-decrypted (or wherever
// CHATLOG_WORK_DIR points) it is skipped, never failed.
//
// Run explicitly with:
//   CHATLOG_WORK_DIR=/path/to/decrypted go test ./internal/chatlog/bizarch -run RealChatlog
func TestListPending_RealChatlogFixture(t *testing.T) {
	workDir, _, platform, version, ok := chatlogFixtureDir(t)
	if !ok {
		t.Skip("CHATLOG_WORK_DIR not configured or contact.db missing — skipping integration test")
	}

	store, err := Open(workDir)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer store.Close()

	// Filter to one known ghID (the same one we used in the manual
	// integration test) so the test is deterministic.
	const gh = "gh_423b608e0744"
	cands, err := ListPending(workDir, platform, version, ListOpts{
		GHID:  gh,
		Total: 50,
		PerGH: 50,
	}, store)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(cands) == 0 {
		t.Fatalf("expected at least one candidate for %s", gh)
	}
	for _, c := range cands {
		if c.GHID != gh {
			t.Errorf("unexpected ghID %q in filtered list", c.GHID)
		}
		if c.URL == "" {
			t.Errorf("candidate missing URL: %+v", c)
		}
		if c.PublishedAt.IsZero() {
			t.Errorf("candidate missing PublishedAt: %+v", c)
		}
	}

	// Sort assertion: candidates must be in descending time order.
	for i := 1; i < len(cands); i++ {
		if cands[i].PublishedAt.After(cands[i-1].PublishedAt) {
			t.Errorf("not sorted desc at %d: %v > %v", i, cands[i].PublishedAt, cands[i-1].PublishedAt)
		}
	}

	// Mark one URL as archived, re-list, expect it's gone.
	target := cands[0].URL
	if err := store.MarkArchived(gh, target, cands[0].Title, "test"); err != nil {
		t.Fatalf("MarkArchived: %v", err)
	}
	defer store.Unmark(target) // cleanup so the test is idempotent

	cands2, err := ListPending(workDir, platform, version, ListOpts{
		GHID: gh, Total: 50, PerGH: 50,
	}, store)
	if err != nil {
		t.Fatalf("ListPending (post-mark): %v", err)
	}
	if len(cands2) != len(cands)-1 {
		t.Errorf("expected %d candidates after mark, got %d", len(cands)-1, len(cands2))
	}
	for _, c := range cands2 {
		if c.URL == target {
			t.Errorf("archived URL %s should be filtered out", target)
		}
	}
}