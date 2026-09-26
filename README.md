<div align="center">

![chatlog](https://github.com/user-attachments/assets/e085d3a2-e009-4463-b2fd-8bd7df2b50c3)

_聊天记录工具，帮助大家轻松使用自己的聊天数据_

[![ImgMCP](https://cdn.imgmcp.com/imgmcp-logo-small.png)](https://imgmcp.com)

[![Go Report Card](https://goreportcard.com/badge/github.com/chenliitaz/chatlog)](https://goreportcard.com/report/github.com/chenliitaz/chatlog)
[![GoDoc](https://godoc.org/github.com/chenliitaz/chatlog?status.svg)](https://godoc.org/github.com/chenliitaz/chatlog)
[![GitHub release](https://img.shields.io/github/release/chenliitaz/chatlog.svg)](https://github.com/chenliitaz/chatlog/releases)
[![GitHub license](https://img.shields.io/github/license/chenliitaz/chatlog.svg)](https://github.com/chenliitaz/chatlog/blob/main/LICENSE)


</div>

> [!NOTE]
> **本仓库是 [sjzar/chatlog](https://github.com/sjzar/chatlog) 的 fork。**
> 上游项目由 Sarv 及社区贡献者创建，本仓库在其基础上增加了公众号汇总（bizhub）、
> 独立 Vue 前端、`chatstat` 统计命令与访问鉴权，并修复了若干并发与安全问题。
> 遵循 Apache-2.0 §4(d)，上游版权声明完整保留在 `LICENSE`，修改说明见 [`NOTICE`](./NOTICE)。
> **本 README 中的安装、镜像、Issue、Discussion 均指向本 fork**；仅少数上游独有的内容
> （如 FAQ issue）保留上游链接。


## Feature

- 从本地数据库文件中获取聊天数据
- 支持 Windows / macOS 系统，兼容微信 3.x / 4.x 版本
- 支持获取数据与图片密钥 (Windows < 4.0.3.36 / macOS < 4.0.3.80)
- 支持图片、语音等多媒体数据解密，支持 wxgf 格式解析
- 支持自动解密数据库，并提供新消息 Webhook 回调
- 提供 Terminal UI 界面，同时支持命令行工具和 Docker 镜像部署
- 提供 HTTP API 服务，可轻松查询聊天记录、联系人、群聊、最近会话等信息
- 支持 MCP Streamable HTTP 协议，可与 AI 助手无缝集成
- 支持多账号管理，可在不同账号间切换
- **公众号汇总（bizhub）**：本地解密 + 抓取公众号文章正文、生成结构化 AI 摘要、收藏与关注管理
- **聊天记录统计**：`chatstat` 命令一键查看近期活跃 talker 与关键词

## Quick Start

### 基本步骤

1. **安装 Chatlog**：[下载预编译版本](#下载预编译版本) 或 [使用 Go 安装](#从源码安装)
2. **运行程序**：执行 `chatlog` 启动 Terminal UI 界面
3. **解密数据**：选择 `解密数据` 菜单项
4. **开启 HTTP 服务**：选择 `开启 HTTP 服务` 菜单项
5. **访问数据**：
   - 通过 [HTTP API](#http-api) 或 [MCP 集成](#mcp-集成) 访问聊天记录
   - 浏览器访问 `http://127.0.0.1:5030/biz` 查看公众号文章汇总（启用 bizhub 模块后）

> 💡 **提示**: 如果电脑端微信聊天记录不全，可以[从手机端迁移数据](#从手机迁移聊天记录)  

### 常见问题快速解决

- **macOS 用户**：获取密钥前需[临时关闭 SIP](#macos-版本说明)
- **Windows 用户**：遇到界面显示问题请[使用 Windows Terminal](#windows-版本说明)
- **集成 AI 助手**：查看 [MCP 集成指南](#mcp-集成)
- **无法获取密钥**：查看 [FAQ](https://github.com/sjzar/chatlog/issues/197)（上游 issue，本仓库未收录该问答）

## 安装指南

### 从源码安装

```bash
go install github.com/chenliitaz/chatlog@latest
```

> 💡 **提示**: 部分功能有 cgo 依赖，编译前需确认本地有 C 编译环境。

### 下载预编译版本

访问 [Releases](https://github.com/chenliitaz/chatlog/releases) 页面下载适合您系统的预编译版本。

## 使用指南

### Terminal UI 模式

最简单的使用方式是通过 Terminal UI 界面操作：

```bash
chatlog
```

操作方法：
- 使用 `↑` `↓` 键选择菜单项
- 按 `Enter` 确认选择
- 按 `Esc` 返回上级菜单
- 按 `Ctrl+C` 退出程序

### 命令行模式

对于熟悉命令行的用户，可以直接使用以下命令：

```bash
# 获取微信数据密钥
chatlog key

# 解密数据库文件
chatlog decrypt

# 启动 HTTP 服务
chatlog server

# 统计最近 N 天聊天活跃度排行（私聊/群聊 + 关键词 + 样本）
chatlog chatstat --days 7 --top 20
```

> 公众号归档不再有独立命令：在 Web 端「管理 → 批量任务」里一键导出。
> 详见 [公众号归档功能说明](docs/biz2md.md) 与 [IMA 知识库推送](docs/biz2ima.md)。

### Docker 部署

由于 Docker 部署时，程序运行环境与宿主机隔离，所以不支持获取密钥等操作，需要提前获取密钥数据。

一般用于 NAS 等设备部署，详细指南可参考 [Docker 部署指南](docs/docker.md)

**0. 获取密钥信息**

```shell
# 从本机运行 chatlog 获取密钥信息
$ chatlog key
Data Key: [c0163e***ac3dc6]
Image Key: [38636***653361]
```

**1. 拉取镜像**

镜像发布在 GitHub Container Registry (ghcr)：

```shell
docker pull ghcr.io/chenliitaz/chatlog:latest
```

> 💡 **镜像地址**: https://ghcr.io/chenliitaz/chatlog

> ℹ️ 镜像的 tag 规则：正式版为 `vX.Y.Z` / `latest`，预览版为 `vX.Y.Z-rcN`（不含 `latest`）。


**2. 运行容器**

```shell
$ docker run -d \
  --name chatlog \
  -p 5030:5030 \
  -v /path/to/your/wechat/data:/app/data \
  ghcr.io/chenliitaz/chatlog:latest
```

**3. 启用公众号汇总 / LLM 摘要（可选）**

容器默认不开启公众号汇总与 LLM 摘要。如需启用，可在挂载目录写入 `chatlog-server.json`：

```json
{
  "platform": "darwin",
  "version": 4,
  "http_addr": "0.0.0.0:5030",
  "data_dir": "/app/data",
  "data_key": "<已获取的 data_key>",
  "work_dir": "/tmp/chatlog-decrypted",
  "auto_decrypt": true,
  "llm_base_url": "https://api.minimaxi.com/anthropic",
  "llm_api_key": "sk-xxx",
  "llm_model": "MiniMax-M3"
}
```

重启容器后访问 `http://localhost:5030/biz`，即可看到公众号汇总页面（顶部 tab：公众号 / 关注动态 / 汇总 / 管理）。详细 LLM 配置见 [公众号汇总（bizhub）](#公众号汇总-bizhub)。

> ⚠️ 建议将 `chatlog-server.json` 权限设为 `0600`，避免 LLM 密钥泄露。

### 从手机迁移聊天记录

如果电脑端微信聊天记录不全，可以从手机端迁移数据：

1. 打开手机微信，进入 `我 - 设置 - 通用 - 聊天记录迁移与备份`
2. 选择 `迁移 - 迁移到电脑`，按照提示操作
3. 完成迁移后，重新运行 `chatlog` 获取密钥并解密数据

> 此操作不会影响手机上的聊天记录，只是将数据复制到电脑端

## 平台特定说明

### Windows 版本说明

如遇到界面显示异常（如花屏、乱码等），请使用 [Windows Terminal](https://github.com/microsoft/terminal) 运行程序

### macOS 版本说明

macOS 用户在获取密钥前需要临时关闭 SIP（系统完整性保护）：

1. **关闭 SIP**：
   ```shell
   # 进入恢复模式
   # Intel Mac: 重启时按住 Command + R
   # Apple Silicon: 重启时长按电源键
   
   # 在恢复模式中打开终端并执行
   csrutil disable
   
   # 重启系统
   ```

2. **安装必要工具**：
   ```shell
   # 安装 Xcode Command Line Tools
   xcode-select --install
   ```

3. **获取密钥后**：可以重新启用 SIP（`csrutil enable`），不影响后续使用

> Apple Silicon 用户注意：确保微信、chatlog 和终端都不在 Rosetta 模式下运行

## HTTP API

启动 HTTP 服务后（默认地址 `http://127.0.0.1:5030`），可通过以下 API 访问数据：

### 聊天记录查询

```
GET /api/v1/chatlog?time=2023-01-01&talker=wxid_xxx
```

参数说明：
- `time`: 时间范围，格式为 `YYYY-MM-DD` 或 `YYYY-MM-DD~YYYY-MM-DD`
- `talker`: 聊天对象标识（支持 wxid、群聊 ID、备注名、昵称等）
- `limit`: 返回记录数量
- `offset`: 分页偏移量
- `format`: 输出格式，支持 `json`、`csv` 或纯文本

### 其他 API 接口

- **联系人列表**：`GET /api/v1/contact`
- **群聊列表**：`GET /api/v1/chatroom`
- **会话列表**：`GET /api/v1/session`

### 聊天记录统计

```
GET /api/v1/chatlog/stat?days=7&top=20
```

参数说明：
- `days`: 统计最近 N 天（默认 7，最大 90）
- `top`: 最多返回多少个 talker（默认 20，最大 100）
- `limit`: 每个 talker 取多少条消息用于分析（默认 200，最大 1000）

返回示例：

```json
{
  "days": 7,
  "totalMessages": 1622,
  "talkerCount": 62,
  "privateCount": 37,
  "groupCount": 25,
  "talkers": [
    {
      "rank": 1, "id": "wxid_xxx", "name": "示例联系人",
      "isChatRoom": false, "count": 200, "sent": 113, "received": 87,
      "firstAt": 1757833443, "lastAt": 1758175443,
      "topKeywords": ["晚安", "今天", "没有"],
      "sampleTexts": ["示例消息一", "示例消息二"]
    }
  ]
}
```

也可使用 CLI 命令快速查看：

```bash
chatlog chatstat --days 7 --top 20
```

### 多媒体内容

聊天记录中的多媒体内容会通过 HTTP 服务进行提供，可通过以下路径访问：

- **图片内容**：`GET /image/<id>`
- **视频内容**：`GET /video/<id>`
- **文件内容**：`GET /file/<id>`
- **语音内容**：`GET /voice/<id>`
- **多媒体内容**：`GET /data/<data dir relative path>`

当请求图片、视频、文件内容时，将返回 302 跳转到多媒体内容 URL。  
当请求语音内容时，将直接返回语音内容，并对原始 SILK 语音做了实时转码 MP3 处理。  
多媒体内容 URL 地址为基于`数据目录`的相对地址，请求多媒体内容将直接返回对应文件，并针对加密图片做了实时解密处理。

## Webhook

需开启自动解密功能，当收到特定新消息时，可以通过 HTTP POST 请求将消息推送到指定的 URL。

> 延迟测试: 本地服务消息回调延迟约 13 秒; 远程同步消息回调延迟约 45 秒。

#### 0. 回调配置

使用 TUI 模式的话，在 `$HOME/.chatlog/chatlog.json` 配置文件中，新增 `webhook` 配置。  
（Windows 用户的配置文件在 `%USERPROFILE%/.chatlog/chatlog.json`)

```json
{
  "history": [],
  "last_account": "wxuser_x",
  "webhook": {
    "host": "localhost:5030",                   # 消息中的图片、文件等 URL host
    "items": [
      {
        "url": "http://localhost:8080/webhook", # 必填，webhook 请求的URL，可配置为 n8n 等 webhook 入口 
        "talker": "wxid_123",                   # 必填，需要监控的私聊、群聊名称
        "sender": "",                           # 选填，消息发送者
        "keyword": ""                           # 选填，关键词
      }
    ]
  }
}
```

使用 server 模式的话，可以通过 `CHATLOG_WEBHOOK` 环境变量进行设置。

```shell
# 方案 1
CHATLOG_WEBHOOK='{"host":"localhost:5030","items":[{"url":"http://localhost:8080/proxy","talker":"wxid_123","sender":"","keyword":""}]}'

# 方案 2（任选一种）
CHATLOG_WEBHOOK_HOST="localhost:5030"
CHATLOG_WEBHOOK_ITEMS='[{"url":"http://localhost:8080/proxy","talker":"wxid_123","sender":"","keyword":""}]'
```

#### 1. 测试效果

启动 chatlog 并开启自动解密功能，测试回调效果

```shell
POST /webhook HTTP/1.1
Host: localhost:8080
Accept-Encoding: gzip
Content-Length: 386
Content-Type: application/json
User-Agent: Go-http-client/1.1

Body:
{
  "keyword": "",
  "lastTime": "2025-08-27 00:00:00",
  "length": 1,
  "messages": [
    {
      "seq": 1756225000000,
      "time": "2025-08-27T00:00:00+08:00",
      "talker": "wxid_123",
      "talkerName": "",
      "isChatRoom": false,
      "sender": "wxid_123",
      "senderName": "Name",
      "isSelf": false,
      "type": 1,
      "subType": 0,
      "content": "测试消息",
      "contents": {
        "host": "localhost:5030"
      }
    }
  ],
  "sender": "",
  "talker": "wxid_123"
}
```

## 公众号汇总（bizhub）

bizhub 模块把关注的公众号文章从微信本地数据库里解出来、抓正文、丢给 LLM 做结构化摘要，并提供 Web UI 进行统一管理。**所有数据本地处理，正文与文章入库到 `~/.chatlog/biz_articles.db`，不依赖任何远程服务**（除非开启 LLM 摘要）。

### 启用

在 `~/.chatlog/chatlog-server.json`（或通过环境变量）配置：

```json
{
  "platform": "darwin",
  "version": 4,
  "data_dir": "/path/to/微信数据目录",
  "data_key": "<data key>",
  "work_dir": "/path/to/decrypted",
  "auto_decrypt": true,
  "llm_base_url": "https://api.minimaxi.com/anthropic",
  "llm_api_key": "sk-xxx",
  "llm_model": "MiniMax-M3",
  "summary_fetch_content": true,
  "summary_fetch_concurrency": 6
}
```

启动 HTTP 服务（`chatlog server`）后，访问 `http://127.0.0.1:5030/biz` 即可看到。

### 四个页面

顶部 tab 导航统一指向四个页面：

| 页面 | 用途 |
|---|---|
| `/biz` | 公众号文章浏览（按账号分组的文章流 + 搜索 + 同步 + 标签筛选） |
| `/biz/feed` | 关注动态（默认 7 天，左侧 LLM 主题/关键词面板 + 文章收藏） |
| `/biz/summary` | 汇总（生成结构化报告、当天「今日看点」 + 历史汇总） |
| `/biz/admin` | 管理（账号显隐 / 星标关注 / 批量打标签 / 标签 CRUD） |

### 关键功能

- **结构化报告**：LLM 输出 `{summary, themes, mustReads, byAccount, highlights}`，前端独立渲染主题卡 / 必读评分 / 按公众号小段
- **自定义指令**：每次生成可附加 prompt（如「只看 AI」「对比上次」「3 句话简报」），结果保存为不同 `summary_type` 记录
- **抓正文 + 缓存**：并行抓取 mp.weixin.qq.com 文章正文（6 路并发、桌面 UA），写入 `biz_article_contents` 表（30 天内复用）
- **图片与外链标注**：正文中的 `<img>` 转为 `[图片]` 占位 + `[图源：URL]`；`<a>` 文本后追加 `[link: URL]`，LLM 看到图片上下文与参考链接
- **关注动态 LLM 汇总**：左侧面板由 LLM 实时生成「headline + themes + keywords + hotTakes」结构化摘要，4 小时缓存（可手动刷新绕过缓存）
- **文章收藏**：每篇文章可单独 ☆/★ 收藏，`/biz/feed` 支持「只看收藏」过滤
- **批量打标签**：管理页支持多选账号批量隐藏/关注/打标签

### bizhub 关键 API

```
# 浏览文章
GET /api/v1/biz/articles?ghid=xxx&limit=50

# 关注动态文章流（默认 7 天）
GET /api/v1/biz/feed?limit=50&days=7

# 同步公众号（后台增量）
POST /api/v1/biz/sync                    # 全量
POST /api/v1/biz/sync { "ghid": "gh_xxx" }  # 单个

# 关注 / 隐藏 账号
POST /api/v1/biz/accounts/watch    { "ghIDs": [...], "watched": true }
POST /api/v1/biz/accounts/visibility { "ghIDs": [...], "hidden": true }

# 文章收藏
POST /api/v1/biz/articles/:id/bookmark { "bookmarked": true }
GET  /api/v1/biz/bookmarks?limit=50

# LLM 汇总（结构化 JSON 输出）
POST /api/v1/biz/summary         { "days": 7, "instruction": "只看 AI 算力" }
GET  /api/v1/biz/summaries              # 历史报告列表
GET  /api/v1/biz/summaries/:id         # 单份报告含结构化字段

# 关注动态 LLM 摘要（4h 缓存，可 ?fresh=true 绕过）
GET  /api/v1/biz/feed/summary?window=7&fresh=true
```

## MCP 集成

Chatlog 支持 MCP (Model Context Protocol) 协议，可与支持 MCP 的 AI 助手无缝集成。  
启动 HTTP 服务后，通过 Streamable HTTP Endpoint 访问服务：

```
GET /mcp
```

### 快速集成

Chatlog 可以与多种支持 MCP 的 AI 助手集成，包括：

- **ChatWise**: 直接支持 Streamable HTTP，在工具设置中添加 `http://127.0.0.1:5030/mcp`
- **Cherry Studio**: 直接支持 Streamable HTTP，在 MCP 服务器设置中添加 `http://127.0.0.1:5030/mcp`

对于不直接支持 Streamable HTTP 的客户端，可以使用 [mcp-proxy](https://github.com/sparfenyuk/mcp-proxy) 工具转发请求：

- **Claude Desktop**: 通过 mcp-proxy 支持，需要配置 `claude_desktop_config.json`
- **Monica Code**: 通过 mcp-proxy 支持，需要配置 VSCode 插件设置

### 详细集成指南

查看 [MCP 集成指南](docs/mcp.md) 获取各平台的详细配置步骤和注意事项。

## Prompt 示例

为了帮助大家更好地利用 Chatlog 与 AI 助手，我们整理了一些 prompt 示例。希望这些 prompt 可以启发大家更有效地查询和分析聊天记录，获取更精准的信息。

查看 [Prompt 指南](docs/prompt.md) 获取详细示例。

同时欢迎大家分享使用经验和 prompt！如果您有好的 prompt 示例或使用技巧，请通过 [Discussions](https://github.com/chenliitaz/chatlog/discussions) 进行分享，共同进步。

## 免责声明

⚠️ **重要提示：使用本项目前，请务必阅读并理解完整的 [免责声明](./DISCLAIMER.md)。**

本项目仅供学习、研究和个人合法使用，禁止用于任何非法目的或未授权访问他人数据。下载、安装或使用本工具即表示您同意遵守免责声明中的所有条款，并自行承担使用过程中的全部风险和法律责任。

### 摘要（请阅读完整免责声明）

- 仅限处理您自己合法拥有的聊天数据或已获授权的数据
- 严禁用于未经授权获取、查看或分析他人聊天记录
- 开发者不对使用本工具可能导致的任何损失承担责任
- 使用第三方 LLM 服务时，您应遵守这些服务的使用条款和隐私政策

**本项目完全免费开源，任何以本项目名义收费的行为均与本项目无关。**

## License

本项目基于 [Apache-2.0 许可证](./LICENSE) 开源。

本仓库 fork 自 [sjzar/chatlog](https://github.com/sjzar/chatlog)，上游版权归原作者所有，
版权声明与本 fork 的修改清单见 [`NOTICE`](./NOTICE)。


## 隐私政策

本项目不收集任何用户数据。所有数据处理均在用户本地设备上进行。使用第三方服务时，请参阅相应服务的隐私政策。

## Thanks

- [sjzar/chatlog](https://github.com/sjzar/chatlog) —— 本项目的上游，作者 Sarv 及全体贡献者
- [@0xlane](https://github.com/0xlane) 的 [wechat-dump-rs](https://github.com/0xlane/wechat-dump-rs) 项目
- [@xaoyaoo](https://github.com/xaoyaoo) 的 [PyWxDump](https://github.com/xaoyaoo/PyWxDump) 项目
- [@git-jiadong](https://github.com/git-jiadong) 的 [go-lame](https://github.com/git-jiadong/go-lame) 和 [go-silk](https://github.com/git-jiadong/go-silk) 项目
- [Anthropic](https://www.anthropic.com/) 的 [MCP]((https://github.com/modelcontextprotocol) ) 协议
- 各个 Go 开源库的贡献者们

## 贡献

欢迎提 Issue 与 PR。本 fork 相对上游的差异见 [`NOTICE`](./NOTICE) 的 "Modifications" 一节，
改动前请先确认上游是否已修复同类问题，避免重复劳动。

开发前置：`go mod download` → `make build` → `make test`（CGO 必须开启）。详见 [AGENTS.md](./AGENTS.md)。