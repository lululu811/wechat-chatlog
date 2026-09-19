package bizarch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultQueuePath is used when conf.BizArchive.QueuePath is empty.
const DefaultQueuePath = "chatlog-biz-clipper-queue.json"

// ResolveQueuePath picks a queue file path with priority:
//   1. explicit CLI override (queuePath)
//   2. conf.BizArchive.QueuePath
//   3. ~/chatlog-biz-clipper-queue.json
func ResolveQueuePath(queuePath, configQueuePath string) (string, error) {
	if queuePath != "" {
		return queuePath, nil
	}
	if configQueuePath != "" {
		return configQueuePath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("bizarch: cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, DefaultQueuePath), nil
}

// WriteQueue serializes the candidates into the JSON queue file
// (atomic via temp+rename). When dryRun is true nothing is written;
// instead the JSON is printed to stdout for piping into other tools.
func WriteQueue(path string, cands []Candidate, dryRun bool) error {
	qf := QueueFile{
		GeneratedAt: time.Now().UTC(),
		Source:      "chatlog biz2md --queue",
		Items:       cands,
	}
	body, err := json.MarshalIndent(qf, "", "  ")
	if err != nil {
		return err
	}
	if dryRun {
		_, err = os.Stdout.Write(body)
		_, _ = os.Stdout.Write([]byte("\n"))
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}