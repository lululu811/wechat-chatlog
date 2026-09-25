package chatlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// init 注册 pipeline 子命令到 rootCmd。
//
// pipeline 子命令直接调 server 的 /api/v1/biz/admin/pipeline/* HTTP 接口，
// 不在 CLI 进程内读 DB / 起 worker —— 这意味着必须有一个 chatlog server 在跑。
// 设计取舍：
//   - 用 HTTP 而不是直读 DB：CLI 跟"看 admin 页面"看到同一份实时数据；
//     直读 DB 会有 DB 文件锁竞争（WAL 多读单写）。
//   - 用 server 而不是独立 worker：CLI 操作直接落到 server 进程的 worker 上，
//     trigger 立刻可见、retry 立刻被 worker 拉到。
//
// 不依赖 --addr 时默认 http://127.0.0.1:5030，与 serverCmd 默认值一致。
func init() {
	rootCmd.AddCommand(pipelineCmd)
	pipelineCmd.PersistentFlags().StringVar(&pipelineAddr, "addr", "http://127.0.0.1:5030", "chatlog server base URL")
	pipelineCmd.PersistentFlags().StringVar(&pipelineToken, "token", "", "chatlog auth token (if server requires)")
}

var (
	pipelineAddr  string
	pipelineToken string
)

var pipelineCmd = &cobra.Command{
	Use:   "pipeline",
	Short: "Inspect and control the bizhub pipeline worker (requires chatlog server running)",
	Long: `Pipeline worker 推进 fetch → mdexport → summarize → imapush 四阶段。

子命令：
  status   — 漏斗图 + 最近 20 条失败
  trigger  — 主动跑一次 worker tick（不等）
  retry    — 把单篇 failed:* 倒回 pending，下次 tick 推

所有子命令通过 HTTP 调 server 端的 /api/v1/biz/admin/pipeline/* 接口。
Server 进程里实际跑 worker，CLI 只发指令。`,
}

var pipelineStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show pipeline status (counts per stage + last 20 failed)",
	Run:   runPipelineStatus,
}

var pipelineTriggerCmd = &cobra.Command{
	Use:   "trigger",
	Short: "Manually trigger a worker tick (returns immediately)",
	Run:   runPipelineTrigger,
}

var pipelineRetryCmd = &cobra.Command{
	Use:   "retry <article-id>",
	Short: "Reset a failed article back to pending and trigger worker",
	Args:  cobra.ExactArgs(1),
	Run:   runPipelineRetry,
}

func init() {
	pipelineCmd.AddCommand(pipelineStatusCmd, pipelineTriggerCmd, pipelineRetryCmd)
}

// pipelineResp 是 server /admin/pipeline/status 的响应结构（mirror）。
//
// 字段含义见 handlePipelineStatus 注释。这里只 mirror CLI 实际展示的字段，
// 未来 server 加新字段时 CLI 用 json.RawMessage 兜底不报错。
type pipelineResp struct {
	Enabled       bool            `json:"enabled"`
	Counts        map[string]int  `json:"counts"`
	FailedByStage map[string]int  `json:"failed_by_stage"`
	Totals        map[string]int  `json:"totals"`
	Recent        json.RawMessage `json:"recent"`
}

func runPipelineStatus(cmd *cobra.Command, args []string) {
	body, status, err := pipelineHTTPGet("/api/v1/biz/admin/pipeline/status")
	if err != nil {
		fmt.Fprintf(os.Stderr, "request failed: %v\n", err)
		os.Exit(1)
	}
	if status != http.StatusOK {
		fmt.Fprintf(os.Stderr, "server returned %d: %s\n", status, string(body))
		os.Exit(1)
	}

	var resp pipelineResp
	if err := json.Unmarshal(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "parse response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("worker enabled: %v\n\n", resp.Enabled)

	fmt.Println("=== Funnel (5 stages + 4 failed) ===")
	stageOrder := []string{"pending", "fetched", "md_exported", "summarized", "pushed"}
	maxVal := 0
	for _, s := range stageOrder {
		if v := resp.Counts[s]; v > maxVal {
			maxVal = v
		}
	}
	if maxVal == 0 {
		maxVal = 1 // 防止除零
	}
	for _, s := range stageOrder {
		v := resp.Counts[s]
		bar := strings.Repeat("█", int(float64(v)/float64(maxVal)*40))
		fmt.Printf("  %-15s %6d  %s\n", s, v, bar)
	}

	fmt.Println("\n=== Failed by stage ===")
	failedOrder := []string{"fetch", "mdexport", "summarize", "imapush"}
	for _, s := range failedOrder {
		v := resp.FailedByStage[s]
		marker := ""
		if v > 0 {
			marker = " ⚠"
		}
		fmt.Printf("  failed:%-10s %6d%s\n", s, v, marker)
	}

	fmt.Println("\n=== Totals ===")
	for _, k := range []string{"all", "in_flight", "completed", "failed"} {
		fmt.Printf("  %-12s %6d\n", k, resp.Totals[k])
	}

	// recent 字段是 article 列表，原样 dump JSON 不强解（CLI 不应承担结构耦合）
	if len(resp.Recent) > 0 && string(resp.Recent) != "[]" {
		fmt.Println("\n=== Recent failed (last 20) ===")
		fmt.Println(string(resp.Recent))
	}
}

func runPipelineTrigger(cmd *cobra.Command, args []string) {
	body, status, err := pipelineHTTPPost("/api/v1/biz/admin/pipeline/trigger", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request failed: %v\n", err)
		os.Exit(1)
	}
	if status != http.StatusAccepted && status != http.StatusOK {
		fmt.Fprintf(os.Stderr, "server returned %d: %s\n", status, string(body))
		os.Exit(1)
	}

	fmt.Println("triggered.")
	fmt.Println(string(body))
}

func runPipelineRetry(cmd *cobra.Command, args []string) {
	id := args[0]
	path := "/api/v1/biz/admin/pipeline/retry/" + id

	body, status, err := pipelineHTTPPost(path, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request failed: %v\n", err)
		os.Exit(1)
	}
	if status == http.StatusConflict {
		fmt.Fprintf(os.Stderr, "conflict: %s\n", string(body))
		os.Exit(2)
	}
	if status != http.StatusOK {
		fmt.Fprintf(os.Stderr, "server returned %d: %s\n", status, string(body))
		os.Exit(1)
	}

	fmt.Printf("article %s reset to pending, worker triggered\n", id)
	fmt.Println(string(body))
}

// pipelineHTTPGet / pipelineHTTPPost 走同一套 auth + UA + 超时；
// 抽出来是为了 status / trigger / retry 三处共享。

func pipelineHTTPGet(path string) ([]byte, int, error) {
	return pipelineHTTPDo(http.MethodGet, path, nil)
}

func pipelineHTTPPost(path string, payload []byte) ([]byte, int, error) {
	return pipelineHTTPDo(http.MethodPost, path, payload)
}

func pipelineHTTPDo(method, path string, payload []byte) ([]byte, int, error) {
	url := pipelineAddr + path

	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if pipelineToken != "" {
		req.Header.Set("Authorization", "Bearer "+pipelineToken)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return respBody, resp.StatusCode, nil
}
