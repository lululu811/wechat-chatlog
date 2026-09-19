package bizarch

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store is the persistent dedup/state log for the biz2md command.
// It lives in its own sqlite file inside chatlog's work directory
// and never touches contact/message/biz_message (which chatlog treats
// as read-only mirrors of WeChat's own databases).
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (or creates) the biz_archived sqlite file at
// <workDir>/biz_archived.db and runs the schema migration. The
// caller must Close() when done.
func Open(workDir string) (*Store, error) {
	if workDir == "" {
		return nil, errors.New("bizarch: workDir is empty")
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(workDir, "biz_archived.db")
	db, err := sql.Open("sqlite3", path+"?_journal=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Path returns the on-disk location of the biz_archived db.
func (s *Store) Path() string { return s.path }

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS biz_archived (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    gh_id TEXT NOT NULL,
    url TEXT NOT NULL UNIQUE,
    title TEXT,
    archived_at INTEGER NOT NULL,
    source TEXT NOT NULL DEFAULT 'clipper'
);
CREATE INDEX IF NOT EXISTS biz_archived_gh ON biz_archived(gh_id);
CREATE INDEX IF NOT EXISTS biz_archived_time ON biz_archived(archived_at);
`
	_, err := s.db.Exec(schema)
	return err
}

// IsArchived returns true if the given URL is already recorded in the
// store. The check is by URL alone — it is the natural unique key
// (two public-account posts can share titles; URLs uniquely identify
// them).
func (s *Store) IsArchived(url string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM biz_archived WHERE url = ?`, url).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ArchivedSet returns the set of URLs already recorded. Callers feed it
// to a filter step so we don't re-emit URLs the user already
// archived.
func (s *Store) ArchivedSet() (map[string]struct{}, error) {
	rows, err := s.db.Query(`SELECT url FROM biz_archived`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]struct{}, 256)
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out[u] = struct{}{}
	}
	return out, rows.Err()
}

// MarkArchived inserts a record. source is informational ("clipper",
// "manual", etc.). Returns nil on success or if the URL already exists.
func (s *Store) MarkArchived(ghID, url, title, source string) error {
	if url == "" {
		return errors.New("bizarch: url is required")
	}
	if source == "" {
		source = "clipper"
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO biz_archived (gh_id, url, title, archived_at, source) VALUES (?, ?, ?, ?, ?)`,
		ghID, url, title, time.Now().Unix(), source,
	)
	return err
}

// Unmark removes a record so it becomes a candidate again. Useful if
// the user clipped to the wrong vault and wants to retry.
func (s *Store) Unmark(url string) error {
	_, err := s.db.Exec(`DELETE FROM biz_archived WHERE url = ?`, url)
	return err
}

// Count returns the number of archived records (used by --status).
func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM biz_archived`).Scan(&n)
	return n, err
}