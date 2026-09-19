package chatlog

import (
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/sjzar/chatlog/internal/chatlog/bizarch"
	"github.com/sjzar/chatlog/internal/chatlog/conf"
)

func init() {
	rootCmd.AddCommand(biz2mdCmd)

	biz2mdCmd.Flags().StringVar(&bizGH, "gh", "", "公众号 gh_id(留空=全部已关注)")
	biz2mdCmd.Flags().StringVar(&bizSince, "since", "", "起始时间 YYYY-MM-DD")
	biz2mdCmd.Flags().IntVar(&bizLimit, "limit", 100, "列表条数上限(0=不限)")
	biz2mdCmd.Flags().IntVar(&bizPerGH, "per-gh", 50, "每个公众号最多取多少条")
	biz2mdCmd.Flags().StringVar(&bizVault, "vault", "", "Obsidian vault 根目录")
	biz2mdCmd.Flags().StringVar(&bizQueuePath, "queue-path", "", "队列文件输出路径(默认 ~/chatlog-biz-clipper-queue.json)")
	biz2mdCmd.Flags().BoolVar(&bizJSON, "json", false, "JSON 输出")
	biz2mdCmd.Flags().BoolVar(&bizDryRun, "dry-run", false, "仅打印,不写文件(--queue 用)")

	biz2mdCmd.PersistentFlags().BoolVar(&bizList, "list", false, "列出待归档候选 URL")
	biz2mdCmd.PersistentFlags().BoolVar(&bizQueue, "queue", false, "把候选写入队列文件")
	biz2mdCmd.PersistentFlags().BoolVar(&bizStatus, "status", false, "显示归档状态")
	biz2mdCmd.PersistentFlags().BoolVar(&bizManifest, "manifest", false, "打印推荐 Clipper 模板")

	// Optional overrides so the user can target a non-default config
	// without editing the config file. These mirror cmd_decrypt flags.
	biz2mdCmd.Flags().StringVar(&bizWorkDir, "work-dir", "", "WorkDir override")
	biz2mdCmd.Flags().StringVar(&bizPlatform, "platform", "", "platform override (windows/darwin)")
	biz2mdCmd.Flags().IntVar(&bizVersion, "version", 0, "version override (3/4)")
	biz2mdCmd.Flags().StringVar(&bizDataDir, "data-dir", "", "DataDir override")
	biz2mdCmd.Flags().StringVar(&bizDataKey, "data-key", "", "DataKey override")
}

var (
	bizGH        string
	bizSince     string
	bizLimit     int
	bizPerGH     int
	bizVault     string
	bizQueuePath string
	bizJSON      bool
	bizDryRun    bool
	bizList      bool
	bizQueue     bool
	bizStatus    bool
	bizManifest  bool
)

var biz2mdCmd = &cobra.Command{
	Use:   "biz2md",
	Short: "公众号文章归档协调(配合 Obsidian Web Clipper 使用)",
	Long: `biz2md 列出已关注的公众号历史推送(来自 chatlog 本地 sqlite),
让你把这些 URL 交给 Obsidian Web Clipper 抓取并写入 Obsidian vault。

典型用法:
  1. 装 Obsidian Web Clipper(中文增强版)
     https://github.com/nextcaicai/obsidian-clipper-cn
  2. chatlog biz2md --manifest > /tmp/clipper.yaml
     把内容复制到 Obsidian Web Clipper 模板里
  3. chatlog biz2md --list --gh gh_423b608e0744
     看'付鹏的财经世界'最近有哪些推送可归档
  4. 在浏览器打开列表里的 URL,Clipper 自动抓 + 写 vault
  5. chatlog biz2md --status --vault ~/path/to/vault
     查归档状态

需要 chatlog 之前做过解密(workDir 下要有 db_storage/)。
`,
	Run: func(cmd *cobra.Command, args []string) {
		// --manifest 不需要 conf / store / wechatdb,纯文本输出
		if bizManifest {
			bizarch.PrintManifest()
			return
		}

		// 其余动作需要 conf
		cfg, err := loadBiz2mdConfig()
		if err != nil {
			log.Err(err).Msg("failed to load server config")
			os.Exit(1)
		}
		if cfg.workDir == "" {
			fmt.Fprintln(os.Stderr, "biz2md: WorkDir 为空 — 必须在 config 或 CLI flag 里提供")
			os.Exit(1)
		}

		store, err := bizarch.Open(cfg.workDir)
		if err != nil {
			log.Err(err).Msg("open biz_archived store")
			os.Exit(1)
		}
		defer store.Close()

		switch {
		case bizStatus:
			runStatus(cfg, store)
		case bizList:
			runList(cfg, store, false)
		case bizQueue:
			runList(cfg, store, true)
		default:
			_ = cmd.Help()
		}
	},
}

// biz2mdCfg is the resolved view of conf.ServerConfig that biz2md
// actually consumes. Decoupled from conf.ServerConfig so the test
// suite can construct it directly.
type biz2mdCfg struct {
	workDir  string
	platform string
	version  int
	vault    string
}

// loadBiz2mdConfig loads conf.ServerConfig (using the same routine as
// `chatlog server`) and applies CLI overrides for workdir / vault.
func loadBiz2mdConfig() (*biz2mdCfg, error) {
	cmdConf := getBiz2mdCmdCfg()
	sc, _, err := conf.LoadServiceConfig("", cmdConf)
	if err != nil {
		return nil, err
	}
	c := &biz2mdCfg{
		workDir:  sc.GetWorkDir(),
		platform: sc.GetPlatform(),
		version:  sc.GetVersion(),
		vault:    bizVault,
	}
	if c.vault == "" {
		c.vault = sc.GetBizArchive().VaultPath
	}
	return c, nil
}

func getBiz2mdCmdCfg() map[string]any {
	cmdConf := map[string]any{}
	if v := cmdFlagOrEnv("work-dir"); v != "" {
		cmdConf["work_dir"] = v
	}
	if v := cmdFlagOrEnv("platform"); v != "" {
		cmdConf["platform"] = v
	}
	if v := cmdFlagOrEnv("version"); v != "" {
		cmdConf["version"] = v
	}
	if v := cmdFlagOrEnv("data-dir"); v != "" {
		cmdConf["data_dir"] = v
	}
	if v := cmdFlagOrEnv("data-key"); v != "" {
		cmdConf["data_key"] = v
	}
	return cmdConf
}

// cmdFlagOrEnv reads --work-dir / CHATLOG_WORK_DIR style fallback.
// Kept simple — we only forward known keys that LoadServiceConfig
// already understands.
func cmdFlagOrEnv(name string) string {
	switch name {
	case "work-dir":
		if bizWorkDir != "" {
			return bizWorkDir
		}
	case "platform":
		if bizPlatform != "" {
			return bizPlatform
		}
	case "version":
		if bizVersion != 0 {
			return fmt.Sprintf("%d", bizVersion)
		}
	case "data-dir":
		if bizDataDir != "" {
			return bizDataDir
		}
	case "data-key":
		if bizDataKey != "" {
			return bizDataKey
		}
	}
	return ""
}

// Optional override flags (only consulted by loadBiz2mdConfig so the
// user can run biz2md against a non-default work dir / platform,
// without depending on the config file).
var (
	bizWorkDir  string
	bizPlatform string
	bizVersion  int
	bizDataDir  string
	bizDataKey  string
)

func runList(cfg *biz2mdCfg, store *bizarch.Store, writeQueue bool) {
	opts := bizarch.ListOpts{
		GHID:  bizGH,
		Since: bizSince,
		Total: bizLimit,
		PerGH: bizPerGH,
	}
	cands, err := bizarch.ListPending(cfg.workDir, cfg.platform, cfg.version, opts, store)
	if err != nil {
		log.Err(err).Msg("list pending")
		os.Exit(1)
	}
	if writeQueue {
		qpath, err := bizarch.ResolveQueuePath(bizQueuePath, "")
		if err != nil {
			log.Err(err).Msg("resolve queue path")
			os.Exit(1)
		}
		if err := bizarch.WriteQueue(qpath, cands, bizDryRun); err != nil {
			log.Err(err).Msg("write queue")
			os.Exit(1)
		}
		if !bizDryRun {
			fmt.Fprintf(os.Stdout, "wrote %d candidates → %s\n", len(cands), qpath)
		}
		return
	}
	if err := bizarch.PrintList(cands, bizJSON); err != nil {
		log.Err(err).Msg("print list")
		os.Exit(1)
	}
}

func runStatus(cfg *biz2mdCfg, store *bizarch.Store) {
	// Pending needs the candidate list — fetch it once.
	opts := bizarch.ListOpts{GHID: bizGH, Since: bizSince, Total: 1_000_000, PerGH: bizPerGH}
	cands, err := bizarch.ListPending(cfg.workDir, cfg.platform, cfg.version, opts, store)
	if err != nil {
		log.Err(err).Msg("status: list pending")
		os.Exit(1)
	}
	archiveCount, err := store.Count()
	if err != nil {
		log.Err(err).Msg("status: store count")
		os.Exit(1)
	}
	report := bizarch.BuildStatus(cfg.vault, store.Path(), archiveCount, len(cands))
	if err := bizarch.PrintStatus(report, bizJSON); err != nil {
		log.Err(err).Msg("status: print")
		os.Exit(1)
	}
}