package bizarch

import (
	"path/filepath"
	"testing"
)

// TestStore_RoundTrip covers Open → migrate → IsArchived →
// MarkArchived → ArchivedSet → Count → Unmark → Close.
//
// All assertions use a per-test temp directory so they can run in
// parallel and never touch the user's real biz_archived.db.
func TestStore_RoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if s.Path() != filepath.Join(dir, "biz_archived.db") {
		t.Errorf("Path = %q, want %q", s.Path(), filepath.Join(dir, "biz_archived.db"))
	}

	// Initially nothing archived.
	if n, err := s.Count(); err != nil || n != 0 {
		t.Fatalf("Count initial: n=%d err=%v want n=0", n, err)
	}
	if ok, err := s.IsArchived("https://mp.weixin.qq.com/s?biz=x&mid=y"); err != nil || ok {
		t.Errorf("IsArchived before mark: ok=%v err=%v want false", ok, err)
	}

	// Mark one URL.
	if err := s.MarkArchived("gh_x", "https://mp.weixin.qq.com/s?biz=x&mid=y", "Title A", "clipper"); err != nil {
		t.Fatalf("MarkArchived: %v", err)
	}
	if n, _ := s.Count(); n != 1 {
		t.Errorf("Count after mark = %d, want 1", n)
	}
	if ok, _ := s.IsArchived("https://mp.weixin.qq.com/s?biz=x&mid=y"); !ok {
		t.Errorf("IsArchived after mark = false, want true")
	}

	// Mark the same URL again — must be idempotent (UNIQUE constraint
	// + INSERT OR IGNORE).
	if err := s.MarkArchived("gh_x", "https://mp.weixin.qq.com/s?biz=x&mid=y", "Title A", "clipper"); err != nil {
		t.Fatalf("MarkArchived duplicate: %v", err)
	}
	if n, _ := s.Count(); n != 1 {
		t.Errorf("Count after duplicate mark = %d, want 1", n)
	}

	// Mark a second URL.
	if err := s.MarkArchived("gh_y", "https://example.com/2", "Title B", "manual"); err != nil {
		t.Fatalf("MarkArchived #2: %v", err)
	}

	// ArchivedSet should contain both.
	set, err := s.ArchivedSet()
	if err != nil {
		t.Fatalf("ArchivedSet: %v", err)
	}
	if _, ok := set["https://mp.weixin.qq.com/s?biz=x&mid=y"]; !ok {
		t.Errorf("ArchivedSet missing URL 1")
	}
	if _, ok := set["https://example.com/2"]; !ok {
		t.Errorf("ArchivedSet missing URL 2")
	}
	if len(set) != 2 {
		t.Errorf("ArchivedSet size = %d, want 2", len(set))
	}

	// Unmark first URL.
	if err := s.Unmark("https://mp.weixin.qq.com/s?biz=x&mid=y"); err != nil {
		t.Fatalf("Unmark: %v", err)
	}
	if n, _ := s.Count(); n != 1 {
		t.Errorf("Count after unmark = %d, want 1", n)
	}
	if ok, _ := s.IsArchived("https://mp.weixin.qq.com/s?biz=x&mid=y"); ok {
		t.Errorf("IsArchived after unmark = true, want false")
	}

	// Empty URL is rejected.
	if err := s.MarkArchived("gh_x", "", "T", ""); err == nil {
		t.Errorf("MarkArchived empty URL: expected error, got nil")
	}
}

// TestStore_MigrateIsIdempotent ensures repeated Open calls don't
// fail or corrupt the schema.
func TestStore_MigrateIsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()
	if err := s2.MarkArchived("gh_x", "https://example.com/1", "t", ""); err != nil {
		t.Fatalf("MarkArchived after reopen: %v", err)
	}
}

// TestOpen_RequiresWorkDir guards against accidentally using
// "" as workDir (which would put the db in the current directory).
func TestOpen_RequiresWorkDir(t *testing.T) {
	t.Parallel()
	if _, err := Open(""); err == nil {
		t.Errorf("Open(\"\") = nil, want error")
	}
}