package bizhub

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// ArticleSummary 单篇文章的结构化摘要
type ArticleSummary struct {
	Summary    string   `yaml:"summary" json:"summary"`
	Themes     []string `yaml:"themes" json:"themes"`
	Keywords   []string `yaml:"keywords" json:"keywords"`
	MustRead   int      `yaml:"must_read" json:"mustRead"`
	Highlights []string `yaml:"highlights" json:"highlights"`
	DetailBody string   `yaml:"-" json:"detailBody"` // MD body (not in frontmatter)
}

const articleSummarySystemPrompt = "你是公众号内容分析师。用户会给你一篇微信公众号文章的 Markdown 全文。" +
	"你必须严格输出合法 JSON，不得出现 JSON 之外的任何文字；如需包裹请使用 \x60\x60\x60json\x60\x60\x60。" +
	"schema 必须严格遵守：{summary:string(200字以内的中文摘要), themes:[string(3-6个主题标签)], keywords:[string(6-10个高频实体词或短语)], " +
	"mustRead:number(1-10的评分，10为必读), highlights:[string(3-5条关键观点，每条一句话)]}。" +
	"summary 必须是独立可读的摘要，不依赖标题；highlights 必须是从文章中提炼的具体观点而非泛泛描述。"

// GenerateArticleSummary 为单篇已导出的文章生成摘要
func (s *Service) GenerateArticleSummary(ctx context.Context, articleID int64, llm *LLMClient) error {
	if llm == nil {
		return ErrLLMNotConfigured
	}

	// 获取文章信息和导出记录
	article, err := s.store.GetArticle(articleID)
	if err != nil {
		return err
	}
	if article == nil {
		return fmt.Errorf("article not found: %d", articleID)
	}

	exportRecord, err := s.store.GetExportRecord(articleID)
	if err != nil {
		return err
	}
	if exportRecord == nil || exportRecord.MDPath == "" {
		return fmt.Errorf("article not exported yet: %d", articleID)
	}

	// 读取 MD 文件
	mdContent, err := os.ReadFile(exportRecord.MDPath)
	if err != nil {
		return fmt.Errorf("read md file: %w", err)
	}

	// 截断过长内容（LLM 上下文保护）
	content := string(mdContent)
	if len([]rune(content)) > 8000 {
		content = string([]rune(content)[:8000]) + "\n\n[... 内容过长，已截断 ...]"
	}

	// 构建 prompt
	var sb strings.Builder
	fmt.Fprintf(&sb, "以下是公众号文章的 Markdown 全文：\n\n")
	fmt.Fprintf(&sb, "公众号：%s\n", article.GHName)
	fmt.Fprintf(&sb, "标题：%s\n", article.Title)
	fmt.Fprintf(&sb, "发布时间：%s\n\n", time.Unix(article.PublishedAt, 0).Format("2006-01-02 15:04"))
	fmt.Fprintf(&sb, "---\n\n%s\n", content)
	fmt.Fprintf(&sb, "\n---\n\n请按 schema 输出 JSON。")

	// 调用 LLM
	text, _, _, err := llm.CompleteWithSystem(ctx, articleSummarySystemPrompt, sb.String())
	if err != nil {
		return err
	}

	// 解析 JSON
	summaryData, ok := ParseStructured(text)
	if !ok {
		// 降级：把 LLM 输出直接当摘要
		log.Warn().Int64("articleID", articleID).Msg("bizhub: summary output not parseable as json")
		summaryData = nil
	}

	// 构建 ArticleSummary
	var as ArticleSummary
	if summaryData != nil {
		// 解析 JSON 到 struct
		var parsed struct {
			Summary    string   `json:"summary"`
			Themes     []string `json:"themes"`
			Keywords   []string `json:"keywords"`
			MustRead   int      `json:"mustRead"`
			Highlights []string `json:"highlights"`
		}
		if err := yaml.Unmarshal(summaryData, &parsed); err == nil {
			as.Summary = parsed.Summary
			as.Themes = parsed.Themes
			as.Keywords = parsed.Keywords
			as.MustRead = parsed.MustRead
			as.Highlights = parsed.Highlights
		}
	}
	if as.Summary == "" {
		as.Summary = truncateRunes(text, 200)
	}
	as.DetailBody = text

	// 生成 summary.md 文件（YAML frontmatter + MD body）
	summaryPath := exportRecord.MDPath[:len(exportRecord.MDPath)-3] + "_summary.md"
	// 或者放在同目录下的 summary.md
	dir := exportRecord.MDPath[:strings.LastIndex(exportRecord.MDPath, "/")]
	summaryPath = dir + "/summary.md"

	yamlBytes, err := yaml.Marshal(map[string]interface{}{
		"title":        article.Title,
		"account":      article.GHName,
		"source_url":   article.URL,
		"published_at": time.Unix(article.PublishedAt, 0).Format("2006-01-02"),
		"summary":      as.Summary,
		"themes":       as.Themes,
		"keywords":     as.Keywords,
		"must_read":    as.MustRead,
		"highlights":   as.Highlights,
		"generated_at": time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}

	var fileContent strings.Builder
	fileContent.WriteString("---\n")
	fileContent.Write(yamlBytes)
	fileContent.WriteString("---\n\n")
	if as.DetailBody != "" {
		fileContent.WriteString(as.DetailBody)
		fileContent.WriteString("\n")
	}

	if err := os.WriteFile(summaryPath, []byte(fileContent.String()), 0644); err != nil {
		return fmt.Errorf("write summary file: %w", err)
	}

	// 更新 DB 记录（补摘要不算一次归档尝试，kind 保持为空）
	if err := s.store.UpsertExportRecord(articleID, article.URL, exportRecord.MDPath, summaryPath, ExportStatusSummarized, "", ""); err != nil {
		log.Warn().Err(err).Int64("articleID", articleID).Msg("bizhub: update export record with summary path failed")
	}

	return nil
}

// GenerateBatchSummaries 批量为已导出但未生成摘要的文章生成摘要
func (s *Service) GenerateBatchSummaries(ctx context.Context, llm *LLMClient, limit int) (success, failed, skipped int, errors []string) {
	if llm == nil {
		return 0, 0, 0, []string{"llm not configured"}
	}
	if limit <= 0 {
		limit = 50
	}

	// 获取已导出但没有摘要的文章
	records, err := s.store.GetExportRecordsWithoutSummary(limit)
	if err != nil {
		return 0, 0, 0, []string{err.Error()}
	}

	for _, r := range records {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := s.GenerateArticleSummary(ctx, r.ArticleID, llm); err != nil {
			failed++
			errors = append(errors, fmt.Sprintf("article %d: %v", r.ArticleID, err))
			log.Warn().Err(err).Int64("articleID", r.ArticleID).Msg("bizhub: generate summary failed")
		} else {
			success++
		}
	}
	return
}
