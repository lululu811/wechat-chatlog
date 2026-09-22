package chatlog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(chatstatCmd)
	chatstatCmd.Flags().StringVar(&chatstatServer, "server", "http://127.0.0.1:5030", "HTTP 服务地址")
	chatstatCmd.Flags().IntVar(&chatstatDays, "days", 7, "统计最近 N 天")
	chatstatCmd.Flags().IntVar(&chatstatTop, "top", 20, "最多显示前 N 名（私聊与群聊各自排序）")
	chatstatCmd.Flags().BoolVar(&chatstatJSON, "json", false, "输出原始 JSON（默认渲染文本报告）")
}

var (
	chatstatServer string
	chatstatDays   int
	chatstatTop    int
	chatstatJSON   bool
)

var chatstatCmd = &cobra.Command{
	Use:   "chatstat",
	Short: "统计最近 N 天微信聊天记录",
	Long: "汇总过去若干天内的聊天活跃度排行：每个人/群的聊天条数、发出/收到、最后活跃、关键词、样本消息。\n" +
		"通过 HTTP 服务读取解密后的微信数据库（不会同步落盘）。",
	Run: func(cmd *cobra.Command, args []string) {
		endpoint := fmt.Sprintf("%s/api/v1/chatlog/stat?days=%d&top=%d",
			strings.TrimRight(chatstatServer, "/"), chatstatDays, chatstatTop)

		client := &http.Client{Timeout: 120 * time.Second}
		resp, err := client.Get(endpoint)
		if err != nil {
			log.Fatal().Err(err).Msgf("调用 %s 失败", endpoint)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatal().Err(err).Msg("读取响应失败")
			return
		}
		if resp.StatusCode != http.StatusOK {
			log.Fatal().Int("status", resp.StatusCode).Str("body", string(body)).Msg("接口返回错误")
			return
		}

		if chatstatJSON {
			fmt.Println(string(body))
			return
		}

		var payload chatStatPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			log.Fatal().Err(err).Msg("解析 JSON 失败")
			return
		}
		renderChatStat(payload)
	},
}

type chatStatPayload struct {
	Days          int              `json:"days"`
	StartTime     int64            `json:"startTime"`
	EndTime       int64            `json:"endTime"`
	TotalMessages int              `json:"totalMessages"`
	TalkerCount   int              `json:"talkerCount"`
	PrivateCount  int              `json:"privateCount"`
	GroupCount    int              `json:"groupCount"`
	Talkers       []chatStatTalker `json:"talkers"`
}

type chatStatTalker struct {
	Rank        int      `json:"rank"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	IsChatRoom  bool     `json:"isChatRoom"`
	Count       int      `json:"count"`
	Sent        int      `json:"sent"`
	Received    int      `json:"received"`
	FirstAt     int64    `json:"firstAt"`
	LastAt      int64    `json:"lastAt"`
	TopKeywords []string `json:"topKeywords"`
	SampleTexts []string `json:"sampleTexts"`
}

func renderChatStat(p chatStatPayload) {
	startStr := time.Unix(p.StartTime, 0).Format("2006-01-02")
	endStr := time.Unix(p.EndTime, 0).Format("2006-01-02")
	fmt.Printf("\n=== 微信聊天周报（最近 %d 天 · %s ~ %s）===\n\n", p.Days, startStr, endStr)
	fmt.Printf("总消息数 %d · 聊天对象 %d 个（私聊 %d · 群聊 %d）\n\n",
		p.TotalMessages, p.TalkerCount, p.PrivateCount, p.GroupCount)

	if len(p.Talkers) == 0 {
		fmt.Println("这段时间没有任何聊天记录。")
		return
	}

	// 私聊与群聊分开展示
	private := filterByGroup(p.Talkers, false)
	groups := filterByGroup(p.Talkers, true)

	fmt.Println("── 私聊 Top ──")
	renderTopTable(private)
	fmt.Println()

	fmt.Println("── 群聊 Top ──")
	renderTopTable(groups)
	fmt.Println()

	renderSamples(p.Talkers)
}

func filterByGroup(t []chatStatTalker, isGroup bool) []chatStatTalker {
	out := make([]chatStatTalker, 0, len(t))
	for _, x := range t {
		if x.IsChatRoom == isGroup {
			out = append(out, x)
		}
	}
	return out
}

func renderTopTable(list []chatStatTalker) {
	if len(list) == 0 {
		fmt.Println("  (无)")
		return
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].LastAt > list[j].LastAt
	})
	fmt.Printf("  %-3s %-22s %6s %5s %5s %-12s %s\n",
		"#", "对方", "总数", "发出", "收到", "最后活跃", "关键词")
	for i, t := range list {
		last := "-"
		if t.LastAt > 0 {
			last = time.Unix(t.LastAt, 0).Format("01-02 15:04")
		}
		kw := "-"
		if len(t.TopKeywords) > 0 {
			kw = strings.Join(t.TopKeywords[:chatStatMinInt(6, len(t.TopKeywords))], " · ")
		}
		fmt.Printf("  %-3d %-22s %6d %5d %5d %-12s %s\n",
			i+1, truncate(t.Name, 20), t.Count, t.Sent, t.Received, last, kw)
	}
}

func renderSamples(talkers []chatStatTalker) {
	// 显示前 5 个 talker 的样本消息（包含一些发言）
	fmt.Println("── 样本消息（最近活跃 top 5）──")
	shown := 0
	for _, t := range talkers {
		if shown >= 5 {
			break
		}
		if len(t.SampleTexts) == 0 {
			continue
		}
		fmt.Printf("\n【%s】 %d 条 · 发出 %d / 收到 %d\n", t.Name, t.Count, t.Sent, t.Received)
		for _, s := range t.SampleTexts {
			clean := strings.TrimSpace(s)
			if clean == "" {
				continue
			}
			fmt.Printf("  · %s\n", truncate(clean, 90))
		}
		shown++
	}
}

func truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

var _ = url.QueryEscape
var _ = os.Exit

func chatStatMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
