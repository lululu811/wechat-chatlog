package bizhub

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestFetchOneWeChat(t *testing.T) {
	url := "http://mp.weixin.qq.com/s?__biz=MzUxNjE1NjI1MA==&mid=2247493903&idx=1&sn=4d153c93eff0a32ea90ce4f74599c77f&chksm=f8045b12fc8de8dac3c1a593e1f2c2a0c23e561a84a350c574f2ec77ca11cbfd0290102905e4&scene=0&xtrack=1"
	opts := NewDefaultFetchOptions()
	title, content, n, err := FetchAndExtract(context.Background(), url, opts)
	if err != nil {
		t.Fatalf("fetch err: %v", err)
	}
	t.Logf("bytes=%d title=%q contentBytes=%d\n", n, title, len(content))
	t.Logf("content first 300: %s\n", truncate(content, 300))
	if len([]rune(strings.TrimSpace(content))) < 50 {
		t.Fatalf("content too short")
	}
}

func TestParseHTMLFile(t *testing.T) {
	b, err := os.ReadFile("/tmp/wp2.html")
	if err != nil {
		t.Skip("no /tmp/wp2.html")
	}
	title, content := extractHTML(b, 2000)
	t.Logf("title=%q\ncontentBytes=%d\n", title, len(content))
	t.Logf("first 300: %s\n", truncate(content, 300))
	if len([]rune(strings.TrimSpace(content))) < 50 {
		t.Errorf("content too short: %d", len(content))
	}
}

func TestParseSimpleHTML(t *testing.T) {
	in := []byte(`<html><head><title>Hello</title></head><body><div id="js_content"><p>这是一段很长的段落用于测试提取功能是否正常工作。</p><p>第二段同样有足够多的字数让 flushParagraph 不被过滤掉。</p></div></body></html>`)
	title, content := extractHTML(in, 2000)
	t.Logf("title=%q\ncontentBytes=%d first=%s\n", title, len(content), truncate(content, 200))
	if title != "Hello" {
		t.Errorf("title wrong: %q", title)
	}
	if len([]rune(strings.TrimSpace(content))) < 30 {
		t.Errorf("content too short")
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
