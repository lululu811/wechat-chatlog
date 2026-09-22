package mdexport

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// 本文件验证两件事：
//
//   1. script/export-md.sh 的契约：产出落在 <output>/<account>/<title>/，
//      并在 stdout 上报 MD_PATH=<最终路径>；暂存目录不残留、内嵌的暂存绝对路径被修正。
//   2. 并发导出时每个调用拿到的都是自己的文件。
//
// 第 2 条是核心回归：产出路径原来靠「输出目录前后差集」判定，而并发下这个判定
// 必然出错 —— A 拍完快照后 B 写好了文件，A 扫描时会看到两个新文件、随机挑一个，
// 有一半概率挑到 B 的。表现是两篇记录指向同一个 md、另一篇的文件成孤儿，
// 但状态都写着「已导出」。所以这里必须真的并发跑一遍。

const fakeCLI = `#!/usr/bin/env bash
set -u
url=""; out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    *) url="$1"; shift ;;
  esac
done
name="$(basename "$url")"
mkdir -p "$out/甲号/$name/images"
{
  echo "# $name"
  echo "![相对图](images/1.png)"
  echo "![绝对图]($out/甲号/$name/images/1.png)"
} > "$out/甲号/$name/$name.md"
echo png > "$out/甲号/$name/images/1.png"
exit 0
`

// scriptPath 返回仓库里真实导出脚本的路径（从包目录往上 4 层到仓库根）。
func scriptPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "..", "..", "script", "export-md.sh")
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("解析脚本路径: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("找不到 script/export-md.sh，跳过：%v", err)
	}
	return abs
}

// withFakeCLI 把替身 wechat-article-to-markdown 放进 PATH（仅本测试进程生效）。
func withFakeCLI(t *testing.T) {
	t.Helper()

	binDir := t.TempDir()
	cli := filepath.Join(binDir, "wechat-article-to-markdown")
	if err := os.WriteFile(cli, []byte(fakeCLI), 0o755); err != nil {
		t.Fatalf("写入替身 CLI: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func newScriptExporter(t *testing.T) (*Exporter, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("导出脚本依赖 bash，Windows 上跳过")
	}
	withFakeCLI(t)

	outDir := t.TempDir()
	exp, err := New(Config{Script: scriptPath(t), OutputDir: outDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return exp, outDir
}

// TestScriptReportsMDPathAndMovesOutput 真实脚本的契约验证。
func TestScriptReportsMDPathAndMovesOutput(t *testing.T) {
	exp, outDir := newScriptExporter(t)

	res, err := exp.Export(context.Background(), "https://mp.weixin.qq.com/s/abc-1")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	want := filepath.Join(outDir, "甲号", "abc-1", "abc-1.md")
	if res.MDPath != want {
		t.Errorf("MDPath = %q，期望 %q", res.MDPath, want)
	}
	if res.Account != "甲号" || res.Title != "abc-1" {
		t.Errorf("account/title = %q/%q，期望 甲号/abc-1", res.Account, res.Title)
	}

	body, err := os.ReadFile(res.MDPath)
	if err != nil {
		t.Fatalf("读取产出: %v", err)
	}
	content := string(body)
	if strings.Contains(content, ".staging") {
		t.Errorf("产出里还残留暂存目录路径，图片会成死链：\n%s", content)
	}
	wantAbs := filepath.Join(outDir, "甲号", "abc-1", "images", "1.png")
	if !strings.Contains(content, wantAbs) {
		t.Errorf("内嵌的绝对图片路径没有被修正到 %q：\n%s", wantAbs, content)
	}
	if !strings.Contains(content, "![相对图](images/1.png)") {
		t.Errorf("相对图片引用被改坏了：\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(outDir, "甲号", "abc-1", "images", "1.png")); err != nil {
		t.Errorf("图片没有跟着搬过来：%v", err)
	}

	// 暂存目录必须清干净：留着会被后续的目录扫描看见，也会一直涨盘子
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("读取输出目录: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging") {
			t.Errorf("暂存目录残留：%s", e.Name())
		}
	}
}

// TestScriptReportsPathUnderConcurrency 并发导出时每个调用拿到自己的文件。
//
// 这条断言看起来平淡（「路径互不相同」），但它正是修复前后差别最大的地方：
// 修复前靠目录差集判定，并发下会稳定地出现两个调用指向同一个 md。
func TestScriptReportsPathUnderConcurrency(t *testing.T) {
	exp, outDir := newScriptExporter(t)

	const n = 6
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		paths   = make([]string, 0, n)
		results = make([]*Result, 0, n)
	)

	// 先跑一次，让导出器确认脚本会上报路径（首次是能力探测，会串行）
	if _, err := exp.Export(context.Background(), "https://mp.weixin.qq.com/s/warmup"); err != nil {
		t.Fatalf("预热导出失败: %v", err)
	}
	if exp.needsExclusiveRun() {
		t.Fatal("脚本明明上报了 MD_PATH，导出器却仍认为需要独占执行 —— 并发形同虚设")
	}

	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			url := "https://mp.weixin.qq.com/s/para-" + string(rune('a'+i))
			res, err := exp.Export(context.Background(), url)
			if err != nil {
				errs[i] = err
				return
			}
			mu.Lock()
			paths = append(paths, res.MDPath)
			results = append(results, res)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 个并发导出失败: %v", i, err)
		}
	}

	seen := make(map[string]int, len(paths))
	for _, p := range paths {
		seen[p]++
	}
	for p, count := range seen {
		if count > 1 {
			t.Errorf("%d 个并发调用报告了同一个产出路径 %q —— 路径判定认错了文件", count, p)
		}
	}
	if len(seen) != n {
		t.Errorf("产出路径去重后 %d 个，期望 %d 个", len(seen), n)
	}

	for _, res := range results {
		if _, err := os.Stat(res.MDPath); err != nil {
			t.Errorf("报告的路径不存在：%q (%v)", res.MDPath, err)
		}
		if !strings.HasPrefix(res.MDPath, outDir) {
			t.Errorf("产出路径跑到输出目录外了：%q", res.MDPath)
		}
		if res.Title == "" || res.Account == "" {
			t.Errorf("account/title 解析为空：%+v", res)
		}
	}
}

// TestParseReportedMDPath 路径上报的解析与校验。
//
// 校验必须严：脚本报了一个不存在的路径时宁可用回退逻辑，
// 也不能把脏路径写进归档记录 —— 那会让管理页显示「已归档」而磁盘上没有文件。
func TestParseReportedMDPath(t *testing.T) {
	outDir := t.TempDir()
	okPath := filepath.Join(outDir, "甲号", "标题", "标题.md")
	if err := os.MkdirAll(filepath.Dir(okPath), 0o755); err != nil {
		t.Fatalf("准备目录: %v", err)
	}
	if err := os.WriteFile(okPath, []byte("# x"), 0o644); err != nil {
		t.Fatalf("准备文件: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "leak.md")
	if err := os.WriteFile(outside, []byte("# y"), 0o644); err != nil {
		t.Fatalf("准备越界文件: %v", err)
	}

	cases := []struct {
		name   string
		stdout string
		want   string
		wantOK bool
	}{
		{"正常", "[export-md] Success\nMD_PATH=" + okPath + "\n", okPath, true},
		{"前面混了日志", "[export-md] 一堆日志\nMD_PATH=" + okPath + "\n", okPath, true},
		{"没有上报", "[export-md] Success\n", "", false},
		{"路径不存在", "MD_PATH=" + filepath.Join(outDir, "nope.md") + "\n", "", false},
		{"不是 md", "MD_PATH=" + filepath.Join(outDir, "a.txt") + "\n", "", false},
		{"相对路径", "MD_PATH=甲号/标题/标题.md\n", "", false},
		{"逃出输出目录", "MD_PATH=" + outside + "\n", "", false},
		{"空值", "MD_PATH=\n", "", false},
	}
	for _, tc := range cases {
		got, ok := parseReportedMDPath(tc.stdout, outDir)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v，期望 %v（got=%q）", tc.name, ok, tc.wantOK, got)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("%s: path = %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

// TestFallbackDiffForLegacyScript 不上报 MD_PATH 的老脚本仍能正常工作（走回退判定）。
//
// 代价是并发会被限制为串行 —— 宁慢不错。这条断言把「正确」钉住，
// 顺便确认那个警告只出现一次（不是每次导出都刷一条）。
func TestFallbackDiffForLegacyScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("依赖 bash，Windows 上跳过")
	}

	script := filepath.Join(t.TempDir(), "legacy.sh")
	body := `#!/usr/bin/env bash
set -u
url=""; out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    *) url="$1"; shift ;;
  esac
done
name="$(basename "$url")"
mkdir -p "$out/甲号/$name"
echo "# $name" > "$out/甲号/$name/$name.md"
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("写入老脚本: %v", err)
	}

	outDir := t.TempDir()
	exp, err := New(Config{Script: script, OutputDir: outDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := exp.Export(context.Background(), "https://mp.weixin.qq.com/s/legacy-1")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	want := filepath.Join(outDir, "甲号", "legacy-1", "legacy-1.md")
	if res.MDPath != want {
		t.Errorf("MDPath = %q，期望 %q", res.MDPath, want)
	}
	if !exp.needsExclusiveRun() {
		t.Error("不支持路径上报的脚本必须走独占执行，否则并发下会认错文件")
	}
}

// TestExportErrorsAreClassified 失败分类要能穿过 ExportError 传出来。
func TestExportErrorsAreClassified(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho '环境异常，请完成验证' >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatalf("写入脚本: %v", err)
	}

	exp, err := New(Config{Script: script, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = exp.Export(context.Background(), "https://mp.weixin.qq.com/s/cap-1")
	if err == nil {
		t.Fatal("预期导出失败")
	}
	if got := KindOf(err); got != KindCaptcha {
		t.Errorf("分类 = %q，期望 %q", got, KindCaptcha)
	}
	if !KindOf(err).NeedsHuman() {
		t.Error("验证码应被判定为需人工处理")
	}
}

// TestExportRejectsNonWechatURL 非公众号链接在本地就挡掉，不浪费一次抓取。
func TestExportRejectsNonWechatURL(t *testing.T) {
	exp, err := New(Config{Script: "/bin/true", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = exp.Export(context.Background(), "https://example.com/a")
	if err == nil {
		t.Fatal("预期拒绝非公众号链接")
	}
	if got := KindOf(err); got != KindInvalidURL {
		t.Errorf("分类 = %q，期望 %q", got, KindInvalidURL)
	}
	if !KindOf(err).NeedsHuman() {
		t.Error("链接不合法属于数据问题，重试无意义，应被判为需人工处理")
	}
}
