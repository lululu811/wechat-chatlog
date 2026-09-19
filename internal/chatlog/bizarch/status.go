package bizarch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StatusReport is the JSON-shaped output of PrintStatus. It is also
// the table view used by the CLI's text output.
type StatusReport struct {
	// ArchiveLog is the count of rows in biz_archived (chatlog's
	// own dedup/state table).
	ArchiveLog int `json:"archiveLog"`

	// VaultFiles is the count of .md files inside the Obsidian vault
	// that look like clipped articles. Heuristic: filename matches
	// the Clipper template "<author>-<YYYY-MM-DD>-<title>.md".
	VaultFiles int `json:"vaultFiles"`

	// Pending is the count of Candidate URLs that exist in chatlog's
	// biz_message store but not yet in the archive log.
	Pending int `json:"pending"`

	// VaultPath is the resolved Obsidian vault path (echoed so the
	// user can sanity-check).
	VaultPath string `json:"vaultPath"`

	// ArchiveDB is the absolute path of the biz_archived sqlite file.
	ArchiveDB string `json:"archiveDB"`
}

// BuildStatus computes the report. It does not touch chatlog's
// decrypted biz_message store directly — instead it asks the caller
// for the pending count (already computed by ListPending), so this
// function is purely local-filesystem and avoids re-querying chatlog.
//
// If vaultPath is empty the VaultFiles field is 0 (we never silently
// fall back to a default).
func BuildStatus(vaultPath, archiveDB string, archiveLog, pending int) StatusReport {
	return StatusReport{
		ArchiveLog: archiveLog,
		VaultFiles: countVaultArticles(vaultPath),
		Pending:    pending,
		VaultPath:  vaultPath,
		ArchiveDB:  archiveDB,
	}
}

// countVaultArticles walks vaultPath looking for files whose name
// matches "<author>-YYYY-MM-DD-<title>.md" (the format our --manifest
// template recommends). Files in subdirectories count too. A file
// that does not match is silently skipped — we are inferring vault
// state from filenames only.
func countVaultArticles(vaultPath string) int {
	if vaultPath == "" {
		return 0
	}
	info, err := os.Stat(vaultPath)
	if err != nil || !info.IsDir() {
		return 0
	}
	count := 0
	_ = filepath.WalkDir(vaultPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // tolerate partial vault reads
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".md") {
			return nil
		}
		base := strings.TrimSuffix(name, ".md")
		if looksLikeArticleFilename(base) {
			count++
		}
		return nil
	})
	return count
}

// looksLikeArticleFilename returns true when the basename contains
// at least one "YYYY-MM-DD" substring, anywhere in the string.
//
// This is intentionally lenient because:
//   - author / title can contain hyphens themselves (e.g. titles
//     like "李迅雷-2025-年度策略" or "A-B-C-2026-09-07-..."),
//   - so the date is not necessarily at a fixed position.
// The false-positive risk is low for .md files in an Obsidian
// vault — random notes almost never contain YYYY-MM-DD.
func looksLikeArticleFilename(s string) bool {
	if len(s) < 10 {
		return false
	}
	for i := 0; i+10 <= len(s); i++ {
		if s[i+4] != '-' || s[i+7] != '-' {
			continue
		}
		if !isAllDigits(s, i, 4) {
			continue
		}
		if !isAllDigits(s, i+5, 2) {
			continue
		}
		if !isAllDigits(s, i+8, 2) {
			continue
		}
		// Sanity-bound the month / day so we don't accept e.g.
		// "9999-99-99". Cheap range checks.
		month := int(s[i+5]-'0')*10 + int(s[i+6]-'0')
		day := int(s[i+8]-'0')*10 + int(s[i+9]-'0')
		if month < 1 || month > 12 || day < 1 || day > 31 {
			continue
		}
		// The char before the date must not be a digit — otherwise
		// we'd accept "Author2026-09-07" where 2026 is just the tail
		// of "Author2026". Either the date starts at index 0 or the
		// preceding char is not a digit.
		if i > 0 {
			prev := s[i-1]
			if prev >= '0' && prev <= '9' {
				continue
			}
		}
		return true
	}
	return false
}

func isAllDigits(s string, off, n int) bool {
	for k := 0; k < n; k++ {
		c := s[off+k]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// PrintStatus writes the report as a key/value table (default) or
// JSON (when jsonOut is true). Writes to stdout.
func PrintStatus(r StatusReport, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	fmt.Fprintf(os.Stdout, "vaultPath   : %s\n", r.VaultPath)
	fmt.Fprintf(os.Stdout, "archiveDB   : %s\n", r.ArchiveDB)
	fmt.Fprintf(os.Stdout, "archiveLog  : %d 条\n", r.ArchiveLog)
	fmt.Fprintf(os.Stdout, "vaultFiles  : %d 个\n", r.VaultFiles)
	fmt.Fprintf(os.Stdout, "pending     : %d 条\n", r.Pending)
	return nil
}