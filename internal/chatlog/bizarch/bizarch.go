// Package bizarch coordinates between chatlog's local public-account
// (公众号) message store and an external Obsidian Web Clipper browser
// extension. It does NOT scrape mp.weixin.qq.com itself; that is the
// Clipper's job. bizarch only:
//
//   - Lists candidate URLs that live in chatlog's biz_message_0.db
//   - Tracks which URLs have already been archived (biz_archived table)
//   - Writes a JSON queue file that a future Clipper watcher can read
//   - Reports archive status by scanning an Obsidian vault directory
//   - Prints a recommended Clipper template so users can copy-paste it
//
// All filesystem paths come from conf.BizArchive (CLI flag > config file
// > defaults).
package bizarch

import "time"

// Candidate is one pending public-account article that chatlog has
// metadata for but the vault (or our archived log) does not yet have.
type Candidate struct {
	GHID        string    `json:"ghID"`
	GHName      string    `json:"ghName"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`
}

// QueueFile is the JSON shape written to the queue path by
// WriteQueue. The "generatedAt" field is informational; the "items"
// array is what a future Clipper watcher would consume.
type QueueFile struct {
	GeneratedAt time.Time   `json:"generatedAt"`
	Source      string      `json:"source"` // always "chatlog biz2md --queue"
	Items       []Candidate `json:"items"`
}