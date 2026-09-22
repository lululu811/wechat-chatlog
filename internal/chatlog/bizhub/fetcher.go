package bizhub

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/net/html"
)

// FetchOptions 控制单次/批量抓取
type FetchOptions struct {
	UserAgent   string
	Referer     string
	Timeout     time.Duration
	MaxBytes    int64
	MaxChars    int
	Concurrency int
	Throttle    time.Duration
}

// NewDefaultFetchOptions 返回默认抓取参数（Chrome UA + mp.weixin.qq.com Referer + 15s/2MB/3000/6/100ms）
func NewDefaultFetchOptions() FetchOptions {
	return FetchOptions{
		UserAgent:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		Referer:     "https://mp.weixin.qq.com/",
		Timeout:     15 * time.Second,
		MaxBytes:    2 << 20,
		MaxChars:    3000,
		Concurrency: 6,
		Throttle:    100 * time.Millisecond,
	}
}

// FetchResult 单 URL 抓取结果
type FetchResult struct {
	URL     string
	Title   string
	Content string
	Bytes   int
	Err     error
}

var charsetPattern = regexp.MustCompile(`(?is)<meta[^>]+charset=["']?([a-z0-9\-]+)`)

// FetchAndExtract 单 URL 抓取并提取正文（title/content/bytes）
func FetchAndExtract(ctx context.Context, url string, opts FetchOptions) (title string, content string, bytes int, err error) {
	client := &http.Client{Timeout: opts.Timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", 0, fmt.Errorf("%w: %v", ErrFetchFailed, err)
	}
	if opts.UserAgent != "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	}
	if opts.Referer != "" {
		req.Header.Set("Referer", opts.Referer)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", "", 0, fmt.Errorf("%w: %v", ErrFetchFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", 0, fmt.Errorf("%w: status %d", ErrFetchFailed, resp.StatusCode)
	}

	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 2 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return "", "", 0, fmt.Errorf("%w: read body: %v", ErrFetchFailed, err)
	}
	bytes = len(body)

	charsetPattern.FindSubmatch(body) //nolint:errcheck // 仅用于探测，不强制转码

	title, content = extractHTML(body, opts.MaxChars)
	return title, content, bytes, nil
}

// fetchWithRetry 单 URL 抓取，失败重试 1 次
func fetchWithRetry(ctx context.Context, url string, opts FetchOptions) (title string, content string, bytes int, err error) {
	title, content, bytes, err = FetchAndExtract(ctx, url, opts)
	if err == nil {
		return
	}
	if opts.Throttle > 0 {
		select {
		case <-time.After(opts.Throttle):
		case <-ctx.Done():
			return title, content, bytes, ctx.Err()
		}
	}
	return FetchAndExtract(ctx, url, opts)
}

// FetchBatch 并发批量抓取，每 URL 重试 1 次，URL md5 哈希为 key；整体 ctx 超时 90s。
func FetchBatch(ctx context.Context, urls []string, opts FetchOptions) (map[string]FetchResult, error) {
	results := make(map[string]FetchResult, len(urls))
	if len(urls) == 0 {
		return results, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 6
	}

	batchCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	type job struct{ url string }
	jobs := make(chan job)
	var wg sync.WaitGroup

	var mu sync.Mutex
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if batchCtx.Err() != nil {
					mu.Lock()
					results[HashURL(j.url)] = FetchResult{URL: j.url, Err: batchCtx.Err()}
					mu.Unlock()
					continue
				}
				title, content, bytes, err := fetchWithRetry(batchCtx, j.url, opts)
				mu.Lock()
				results[HashURL(j.url)] = FetchResult{
					URL:     j.url,
					Title:   title,
					Content: content,
					Bytes:   bytes,
					Err:     err,
				}
				mu.Unlock()
				if opts.Throttle > 0 {
					select {
					case <-time.After(opts.Throttle):
					case <-batchCtx.Done():
					}
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, u := range urls {
			select {
			case jobs <- job{url: u}:
			case <-batchCtx.Done():
				return
			}
		}
	}()

	wg.Wait()
	return results, nil
}

// ---------------------------------------------------------------------------
// HTML 提取器（基于 golang.org/x/net/html，健壮处理任意结构）
// ---------------------------------------------------------------------------

var skipNodeTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "iframe": true,
	"header": true, "footer": true, "nav": true, "aside": true, "form": true,
}

var blockNodeTags = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "tr": true, "section": true, "article": true, "main": true,
	"blockquote": true, "pre": true,
}

func extractHTML(src []byte, maxChars int) (title string, content string) {
	doc, err := html.Parse(bytes.NewReader(src))
	if err != nil {
		return "", ""
	}
	title = findTitle(doc)

	// 选主内容：候选（article/main 优先），否则找最长正文段落块
	best := pickMainNode(doc)
	if best == nil {
		best = doc
	}
	paras := collectParagraphs(best, maxChars*2) // 收集足够再截
	content = joinAndTrim(paras, maxChars)
	if len([]rune(strings.TrimSpace(content))) < 50 {
		log.Warn().Int("runes", len([]rune(content))).Str("title", title).Msg("bizhub: extracted content too short, keep status=1")
	}
	return title, content
}

func findTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		return strings.TrimSpace(textOf(n))
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := findTitle(c); t != "" {
			return t
		}
	}
	return ""
}

func pickMainNode(n *html.Node) *html.Node {
	best := n
	bestScore := scoreNode(n)
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode {
			if x.Data == "article" || x.Data == "main" {
				best = x
				bestScore = 1e9
				return
			}
			s := scoreNode(x)
			if s > bestScore && hasLongParagraph(x) {
				best = x
				bestScore = s
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return best
}

func scoreNode(n *html.Node) int {
	s := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			id := attr(c, "id")
			cls := attr(c, "class")
			combined := id + " " + cls
			if strings.Contains(combined, "content") || strings.Contains(combined, "article") ||
				strings.Contains(combined, "rich_media") || strings.Contains(combined, "post") ||
				strings.Contains(combined, "entry") || strings.Contains(combined, "text") {
				s += 50
			}
			if c.Data == "p" {
				s += 5
			}
			s += scoreNode(c)
		}
	}
	return s
}

func hasLongParagraph(n *html.Node) bool {
	found := false
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && x.Data == "p" {
			t := strings.TrimSpace(textOf(x))
			if len([]rune(t)) >= 30 {
				found = true
				return
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			if !found {
				walk(c)
			}
		}
	}
	walk(n)
	return found
}

func collectParagraphs(n *html.Node, maxTotal int) []string {
	var out []string
	var cur strings.Builder
	appendCur := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if cur.Len() > 0 && !strings.HasSuffix(cur.String(), " ") && !strings.HasSuffix(cur.String(), "\n") {
			cur.WriteByte(' ')
		}
		cur.WriteString(s)
	}
	appendSeparator := func(sep string) {
		if cur.Len() == 0 {
			return
		}
		cur.WriteString(sep)
	}

	var walk func(*html.Node, bool)
	walk = func(x *html.Node, inMain bool) {
		if x.Type == html.ElementNode && skipNodeTags[x.Data] {
			return
		}
		if x.Type == html.ElementNode && x.Data == "br" {
			if cur.Len() > 0 {
				cur.WriteByte(' ')
			}
			return
		}
		if x.Type == html.ElementNode && x.Data == "img" {
			alt := strings.TrimSpace(attr(x, "alt"))
			src := strings.TrimSpace(attr(x, "data-src"))
			if src == "" {
				src = strings.TrimSpace(attr(x, "src"))
			}
			if isSkippableImg(alt, src, attr(x, "class")) {
				return
			}
			switch {
			case alt != "" && src != "":
				appendCur("[图片：" + alt + "]")
				appendCur("[图源：" + src + "]")
			case alt != "":
				appendCur("[图片：" + alt + "]")
			case src != "":
				appendCur("[图片]")
				appendCur("[图源：" + src + "]")
			default:
				appendCur("[图片]")
			}
			return
		}
		if x.Type == html.ElementNode && x.Data == "a" {
			href := strings.TrimSpace(attr(x, "href"))
			var linkText strings.Builder
			for c := x.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					linkText.WriteString(c.Data)
				}
			}
			t := strings.TrimSpace(linkText.String())
			if t != "" {
				appendCur(t)
			}
			if href != "" {
				appendCur("[link: " + href + "]")
			}
			return
		}
		if x.Type == html.ElementNode && (x.Data == "td" || x.Data == "th") {
			appendSeparator(" | ")
			for c := x.FirstChild; c != nil; c = c.NextSibling {
				walk(c, inMain)
			}
			return
		}
		if x.Type == html.ElementNode && x.Data == "pre" {
			text := strings.TrimRight(textOf(x), "\n")
			if text != "" {
				appendCur("```")
				appendCur(text)
				appendCur("```")
			}
			return
		}
		if x.Type == html.ElementNode && blockNodeTags[x.Data] {
			if cur.Len() > 0 {
				s := strings.TrimSpace(cur.String())
				if len([]rune(s)) >= 10 {
					out = append(out, s)
				}
				cur.Reset()
			}
			for c := x.FirstChild; c != nil; c = c.NextSibling {
				walk(c, inMain)
			}
			if x.Data != "br" && x.Data != "hr" {
				if cur.Len() > 0 {
					cur.WriteByte(' ')
				}
			}
			if sumRuneLen(out)+cur.Len() >= maxTotal {
				return
			}
			return
		}
		if x.Type == html.TextNode {
			t := strings.TrimSpace(x.Data)
			if t != "" {
				if cur.Len() > 0 && !strings.HasSuffix(cur.String(), " ") {
					cur.WriteByte(' ')
				}
				cur.WriteString(t)
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inMain)
		}
	}
	walk(n, true)
	if cur.Len() > 0 {
		s := strings.TrimSpace(cur.String())
		if len([]rune(s)) >= 10 {
			out = append(out, s)
		}
	}
	return out
}

func isSkippableImg(alt, src, class string) bool {
	la := strings.ToLower(alt)
	lc := strings.ToLower(class)
	if strings.Contains(la, "emoji") || strings.Contains(lc, "we-emoji") {
		return true
	}
	ls := strings.ToLower(src)
	if strings.HasSuffix(ls, "pic_blank.gif") || strings.HasSuffix(ls, "pic_blank.png") {
		return true
	}
	return false
}

func joinAndTrim(paras []string, maxChars int) string {
	if len(paras) == 0 {
		return ""
	}
	if maxChars <= 0 {
		maxChars = 3000
	}
	var sb strings.Builder
	for _, p := range paras {
		r := []rune(p)
		if sb.Len()+len(r)+2 > maxChars {
			remaining := maxChars - sb.Len()
			if remaining > 30 {
				sb.WriteString(string(r[:min(remaining, len(r))]))
			}
			break
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(p)
	}
	return strings.TrimSpace(sb.String())
}

func sumRuneLen(ss []string) int {
	n := 0
	for _, s := range ss {
		n += len([]rune(s))
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
