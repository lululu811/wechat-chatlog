package conf

const (
	DefalutHTTPAddr = "0.0.0.0:5030"
)

type ServerConfig struct {
	Type                    string      `mapstructure:"type"`
	Platform                string      `mapstructure:"platform"`
	Version                 int         `mapstructure:"version"`
	FullVersion             string      `mapstructure:"full_version"`
	DataDir                 string      `mapstructure:"data_dir"`
	DataKey                 string      `mapstructure:"data_key"`
	ImgKey                  string      `mapstructure:"img_key"`
	WorkDir                 string      `mapstructure:"work_dir"`
	HTTPAddr                string      `mapstructure:"http_addr"`
	AuthToken               string      `mapstructure:"auth_token"`
	AutoDecrypt             bool        `mapstructure:"auto_decrypt"`
	LLMBaseURL              string      `mapstructure:"llm_base_url"`
	LLMAPIKey               string      `mapstructure:"llm_api_key"`
	LLMModel                string      `mapstructure:"llm_model"`
	LLMMaxTokens            int         `mapstructure:"llm_max_tokens"`
	SummaryFetchContent     bool        `mapstructure:"summary_fetch_content"`
	SummaryFetchConcurrency int         `mapstructure:"summary_fetch_concurrency"`
	FeedSummaryCacheHours   int         `mapstructure:"feed_summary_cache_hours"`
	MDExportDir             string      `mapstructure:"md_export_dir"`
	MDExportScript          string      `mapstructure:"md_export_script"`
	Webhook                 *Webhook    `mapstructure:"webhook"`
}

var ServerDefaults = map[string]any{}

func (c *ServerConfig) GetDataDir() string {
	return c.DataDir
}

func (c *ServerConfig) GetWorkDir() string {
	return c.WorkDir
}

func (c *ServerConfig) GetPlatform() string {
	return c.Platform
}

func (c *ServerConfig) GetVersion() int {
	return c.Version
}

func (c *ServerConfig) GetDataKey() string {
	return c.DataKey
}

func (c *ServerConfig) GetImgKey() string {
	return c.ImgKey
}

func (c *ServerConfig) GetAutoDecrypt() bool {
	return c.AutoDecrypt
}

func (c *ServerConfig) GetHTTPAddr() string {
	if c.HTTPAddr == "" {
		c.HTTPAddr = DefalutHTTPAddr
	}
	return c.HTTPAddr
}

func (c *ServerConfig) GetAuthToken() string {
	return c.AuthToken
}

func (c *ServerConfig) GetLLMBaseURL() string {
	return c.LLMBaseURL
}

func (c *ServerConfig) GetLLMAPIKey() string {
	return c.LLMAPIKey
}

func (c *ServerConfig) GetLLMModel() string {
	return c.LLMModel
}

// GetLLMMaxTokens 返回 LLM max_tokens 配置；<=0 时调用方回退到默认 4096
func (c *ServerConfig) GetLLMMaxTokens() int {
	return c.LLMMaxTokens
}

// GetSummaryFetchContent 返回 summary 是否抓取正文（默认 true）
func (c *ServerConfig) GetSummaryFetchContent() bool {
	return c.SummaryFetchContent
}

// GetSummaryFetchConcurrency 返回 summary 抓取并发数；<=0 时调用方回退到默认 6
func (c *ServerConfig) GetSummaryFetchConcurrency() int {
	return c.SummaryFetchConcurrency
}

// GetFeedSummaryCacheHours 返回 feed summary 缓存小时数；<=0 时调用方回退到默认 4
func (c *ServerConfig) GetFeedSummaryCacheHours() int {
	return c.FeedSummaryCacheHours
}

// GetMDExportDir 返回 MD 导出目录；未配置时返回空字符串
// （空值表示「归档功能未启用」，调用方据此提示配置，不做默认路径兜底）
func (c *ServerConfig) GetMDExportDir() string {
	return c.MDExportDir
}

// GetMDExportScript 返回 MD 导出脚本路径；未配置时返回空字符串
// （空值表示「归档功能未启用」，调用方据此提示配置，不做默认路径兜底）
func (c *ServerConfig) GetMDExportScript() string {
	return c.MDExportScript
}

func (c *ServerConfig) GetWebhook() *Webhook {
	return c.Webhook
}
