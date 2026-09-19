package bizarch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveQueuePath_Priority(t *testing.T) {
	t.Parallel()

	// CLI override wins.
	if got, _ := ResolveQueuePath("/cli/path", "/conf/path"); got != "/cli/path" {
		t.Errorf("CLI override: got %q", got)
	}
	// Conf wins over default.
	if got, _ := ResolveQueuePath("", "/conf/path"); got != "/conf/path" {
		t.Errorf("conf override: got %q", got)
	}
	// Fallback to ~/chatlog-biz-clipper-queue.json.
	got, err := ResolveQueuePath("", "")
	if err != nil {
		t.Fatalf("ResolveQueuePath default: %v", err)
	}
	if !strings.HasSuffix(got, DefaultQueuePath) {
		t.Errorf("default path = %q, want suffix %q", got, DefaultQueuePath)
	}
}

func TestWriteQueue_Atomic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "q.json")
	cands := []Candidate{{
		GHID:        "gh_x",
		GHName:      "测试号",
		Title:       "文章一",
		URL:         "https://mp.weixin.qq.com/s?biz=b&mid=m",
		PublishedAt: time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC),
	}}

	if err := WriteQueue(path, cands, false); err != nil {
		t.Fatalf("WriteQueue: %v", err)
	}
	// No leftover .tmp file.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("expected no .tmp file, got %v", err)
	}

	// File is valid JSON with the expected shape.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	var got QueueFile
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Source != "chatlog biz2md --queue" {
		t.Errorf("Source = %q", got.Source)
	}
	if len(got.Items) != 1 || got.Items[0].GHID != "gh_x" {
		t.Errorf("Items mismatch: %+v", got.Items)
	}
	if got.Items[0].PublishedAt.IsZero() {
		t.Errorf("PublishedAt not preserved")
	}
}

func TestWriteQueue_DryRunDoesNotWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "should-not-exist.json")
	if err := WriteQueue(path, []Candidate{{GHID: "gh_x"}}, true); err != nil {
		t.Fatalf("WriteQueue dry-run: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("dry-run should not write file: %v", err)
	}
}

func TestWriteQueue_CreatesParentDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "subdir", "q.json")
	if err := WriteQueue(path, nil, false); err != nil {
		t.Fatalf("WriteQueue with nested parent: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file, got %v", err)
	}
}