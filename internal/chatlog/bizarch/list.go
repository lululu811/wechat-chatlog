package bizarch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/sjzar/chatlog/internal/wechatdb"
)

// ListOpts tunes ListPending. Zero values get safe defaults:
//   - ghID == "" means "all public accounts"
//   - since is parsed from "YYYY-MM-DD" if non-empty
//   - perGH caps how many biz messages we fetch per ghID
//   - total caps the final result list size
type ListOpts struct {
	GHID  string
	Since string // "YYYY-MM-DD"
	Total int    // 0 -> unlimited
	PerGH int    // 0 -> 50
}

// ListPending walks chatlog's public-account store, filters out URLs
// already in the biz_archived table, and returns the remaining
// Candidate list ordered by published time descending.
//
// ghdb is opened from the work dir and closed inside this function.
// store is the dedup log; pass nil to disable dedup (all candidates
// returned, including archived ones — only useful for debugging).
func ListPending(workDir, platform string, version int, opts ListOpts, store *Store) ([]Candidate, error) {
	if opts.PerGH <= 0 {
		opts.PerGH = 50
	}
	if opts.Total <= 0 {
		opts.Total = 1_000_000
	}

	ghdb, err := wechatdb.New(workDir, platform, version)
	if err != nil {
		return nil, fmt.Errorf("bizarch: open wechatdb: %w", err)
	}
	defer ghdb.Close()

	ghList, err := ghIDsFromContact(workDir, opts.GHID)
	if err != nil {
		return nil, err
	}
	if len(ghList) == 0 {
		return nil, nil
	}

	// Parse since filter (inclusive lower bound).
	var sinceTime time.Time
	if opts.Since != "" {
		sinceTime, err = time.Parse("2006-01-02", opts.Since)
		if err != nil {
			return nil, fmt.Errorf("bizarch: --since must be YYYY-MM-DD: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fetch messages per ghID. We deliberately use a generous end-time
	// bound (year 9999) and rely on PerGH + sort to trim.
	end := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

	var archived map[string]struct{}
	if store != nil {
		archived, err = store.ArchivedSet()
		if err != nil {
			return nil, fmt.Errorf("bizarch: read archived set: %w", err)
		}
	}

	var out []Candidate
	for _, gh := range ghList {
		msgs, err := ghdb.GetBizMessages(ctx, gh.ID, sinceTime, end, opts.PerGH)
		if err != nil {
			// Skip ghIDs that have no biz_message table (chatlog's
			// GetBizMessages returns "no biz messages found" for those).
			// Don't fail the whole list because of one bad ghID.
			continue
		}
		for _, m := range msgs {
			if m == nil || m.URL == "" {
				continue
			}
			if _, ok := archived[m.URL]; ok {
				continue
			}
			out = append(out, Candidate{
				GHID:        m.GHID,
				GHName:      m.GHName,
				Title:       m.Title,
				URL:         m.URL,
				PublishedAt: m.Time,
			})
		}
		if len(out) >= opts.Total {
			break
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].PublishedAt.After(out[j].PublishedAt)
	})
	if len(out) > opts.Total {
		out = out[:opts.Total]
	}
	return out, nil
}

// ghIDEntry is one row read from contact.db.
type ghIDEntry struct {
	ID   string
	Name string
}

// ghIDsFromContact reads the chatlog mirror contact.db directly so we
// can list every followed public account without depending on a new
// wechatdb API. delete_flag = 0 skips accounts the user already
// unfollowed (chatlog treats them as gone).
func ghIDsFromContact(workDir, onlyGhID string) ([]ghIDEntry, error) {
	if workDir == "" {
		return nil, fmt.Errorf("bizarch: workDir is empty")
	}
	dbPath := filepath.Join(workDir, "db_storage", "contact", "contact.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("bizarch: contact.db not found at %s (run `chatlog decrypt` first)", dbPath)
	}
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	q := `SELECT username, COALESCE(nick_name, '') FROM contact WHERE username LIKE 'gh_%' AND delete_flag = 0`
	args := []any{}
	if onlyGhID != "" {
		q += ` AND username = ?`
		args = append(args, onlyGhID)
	}
	q += ` ORDER BY nick_name`

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ghIDEntry
	for rows.Next() {
		var e ghIDEntry
		if err := rows.Scan(&e.ID, &e.Name); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PrintList writes the candidates either as a tab-separated table
// (default) or JSON (when jsonOut is true). When writer is nil the
// function writes to stdout.
func PrintList(cands []Candidate, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cands)
	}
	for _, c := range cands {
		ts := c.PublishedAt.Format("2006-01-02 15:04")
		name := c.GHName
		if name == "" {
			name = c.GHID
		}
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\n", ts, c.GHID, name, c.URL)
	}
	return nil
}