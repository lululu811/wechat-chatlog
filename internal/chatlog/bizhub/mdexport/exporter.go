// Package mdexport 提供微信公众号文章 → Markdown 导出能力。
// 通过调用外部 shell 脚本（可配置）实现与具体转换工具的解耦。
package mdexport

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Config 导出器配置
type Config struct {
	// Script shell 脚本路径（必填）
	Script string
	// OutputDir 输出根目录（必填），输出结构: <OutputDir>/<account>/<title>/<title>.md
	OutputDir string
	// Timeout 单次导出超时（默认 120s）
	Timeout time.Duration
}

// Result 单次导出结果
type Result struct {
	// MDPath 生成的 .md 文件绝对路径
	MDPath string
	// Account 公众号名（从输出目录结构推断）
	Account string
	// Title 文章标题（从输出目录结构推断）
	Title string
	// Duration 执行耗时
	Duration time.Duration
}

// Exporter 调用外部脚本执行 MD 导出
type Exporter struct {
	cfg Config
}

// New 创建导出器
func New(cfg Config) (*Exporter, error) {
	if cfg.Script == "" {
		return nil, fmt.Errorf("mdexport: script path is required")
	}
	if cfg.OutputDir == "" {
		return nil, fmt.Errorf("mdexport: output dir is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}

	// 确保输出目录存在
	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("mdexport: create output dir: %w", err)
	}

	return &Exporter{cfg: cfg}, nil
}

// Export 执行单篇文章导出
// url: 微信文章 URL
func (e *Exporter) Export(ctx context.Context, url string) (*Result, error) {
	// 规范化：http → https
	if strings.HasPrefix(url, "http://mp.weixin.qq.com/") {
		url = "https://" + strings.TrimPrefix(url, "http://")
	}
	if !strings.HasPrefix(url, "https://mp.weixin.qq.com/") {
		return nil, fmt.Errorf("mdexport: invalid wechat article url: %s", url)
	}

	// 检查脚本是否存在
	if _, err := os.Stat(e.cfg.Script); err != nil {
		return nil, fmt.Errorf("mdexport: script not found: %s (%w)", e.cfg.Script, err)
	}

	// 快照导出前的目录状态，用于检测新增文件
	before, err := scanOutputDir(e.cfg.OutputDir)
	if err != nil {
		before = make(map[string]struct{})
	}

	// 执行脚本
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", e.cfg.Script, url, "-o", e.cfg.OutputDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("mdexport: script failed: %w (stderr: %s)", err, stderr.String())
	}

	// 检测新增的 MD 文件
	after, err := scanOutputDir(e.cfg.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("mdexport: scan output dir: %w", err)
	}

	var newMD string
	for p := range after {
		if _, ok := before[p]; !ok {
			if strings.HasSuffix(p, ".md") {
				newMD = p
				break
			}
		}
	}

	if newMD == "" {
		return nil, fmt.Errorf("mdexport: no new .md file found after export")
	}

	// 从路径推断 account / title
	// 路径格式: <OutputDir>/<account>/<title>/<title>.md
	rel, _ := filepath.Rel(e.cfg.OutputDir, newMD)
	parts := strings.Split(rel, string(filepath.Separator))
	account, title := "", ""
	if len(parts) >= 3 {
		account = parts[0]
		title = parts[1]
	} else if len(parts) == 2 {
		account = parts[0]
		title = strings.TrimSuffix(parts[1], ".md")
	}

	return &Result{
		MDPath:   newMD,
		Account:  account,
		Title:    title,
		Duration: time.Since(start),
	}, nil
}

// Check 检查导出器配置是否可用（脚本存在、输出目录可写）
func (e *Exporter) Check() error {
	if _, err := os.Stat(e.cfg.Script); err != nil {
		return fmt.Errorf("mdexport: script not found: %s", e.cfg.Script)
	}
	info, err := os.Stat(e.cfg.OutputDir)
	if err != nil {
		return fmt.Errorf("mdexport: output dir not accessible: %s", e.cfg.OutputDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("mdexport: output path is not a directory: %s", e.cfg.OutputDir)
	}
	return nil
}

// scanOutputDir 扫描目录下所有 .md 文件的相对路径
func scanOutputDir(dir string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			result[path] = struct{}{}
		}
		return nil
	})
	return result, err
}
