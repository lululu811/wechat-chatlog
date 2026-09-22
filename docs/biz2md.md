# 公众号归档功能说明

> 文件名保留 `biz2md.md`（README 与外部链接指向它），但这里的 biz2md **不再是命令行工具**，
> 而是 chatlog Web 端「**管理 → 批量任务**」里的一个功能。

把关注的公众号历史推送抓成本地 Markdown（含图片），再按需让 LLM 为每篇生成结构化摘要。
全部在浏览器里完成，没有独立命令、没有额外的队列文件、不需要浏览器插件。

---

## 1. 做什么 / 不做什么

**做**

- 从 chatlog 已解密的消息库里挑出「关注账号 × 时间窗口」内**尚未归档**的文章
- 调用可配置的外部抓取脚本，把正文 + 图片落到本地目录
- 用一张状态表记录每篇的归档结果，支持重复执行、自动跳过已完成项
- 为已归档的 Markdown 生成 YAML frontmatter 摘要，供「报告」页汇总消费

**不做（Non-goals）**

- ❌ **不内置抓取器** —— 抓取交给外部脚本，chatlog 只负责「挑谁、记状态」
- ❌ **不直接写 Obsidian vault** —— 输出目录就是普通本地目录，要不要用 Obsidian 打开由你决定
- ❌ **不做定时任务** —— 没有 cron / scheduler，触发永远是「用户点按钮」
- ❌ **不做抓新文章** —— 归档只处理数据库里已有的文章；让新文章入库是「账号同步」的职责
- ❌ **不做归档内容编辑** —— 生成的 `.md` 归你所有，chatlog 只写入、不回读修改

---

## 2. 前置条件

### 2.1 抓取脚本

仓库自带参考实现 [`script/export-md.sh`](../script/export-md.sh)，依赖 `wechat-article-to-markdown`：

```bash
pip install wechat-article-to-markdown
# 或
uv tool install wechat-article-to-markdown
```

脚本路径**可以指向任何满足 §4 契约的 bash 脚本** —— 换抓取器只换脚本，chatlog 代码不用动。

### 2.2 配置

编辑 `~/.chatlog/chatlog-server.json`：

```json
{
  "md_export_dir": "/Users/you/公众号文章",
  "md_export_script": "/abs/path/to/chatlog/script/export-md.sh"
}
```

| 键 | 必填 | 说明 |
|---|---|---|
| `md_export_script` | ✅ | 抓取脚本的**绝对路径**，需能被 `bash` 执行 |
| `md_export_dir` | ✅ | 输出根目录，不存在时自动创建 |

> 两者任一为空即视为「归档功能未启用」：页面显示 ⚠ 未配置，接口返回 400 并提示要设置的键名。
> 这里**没有默认路径兜底** —— 归档会往磁盘写文件，必须由你显式指定目录。

### 2.3 检查清单

```bash
# 1. 脚本可执行
bash /abs/path/to/script/export-md.sh 2>&1 | head -3

# 2. 抓取器已安装
command -v wechat-article-to-markdown

# 3. 服务端认得配置（configured 应为 true）
curl -s localhost:5030/api/v1/biz/admin/export/status
# {"configured":true,"exported":12,"pending":37}

# 4. 输出目录可写
test -w /Users/you/公众号文章 && echo OK
```

---

## 3. 使用

入口只有一个：启动 `chatlog server`，打开 Web 端 → 左侧「**管理**」→ 顶部三分区切到「**批量任务**」。

### 3.1 批量归档

1. 选**时间范围**（近 7 / 30 / 90 / 180 天，默认 30 天）
2. 填**数量上限**（默认 100，上限 1000）
3. 点「**批量导出**」，二次确认后开始

运行中会显示进度条与实时结果（成功 / 失败 / 跳过）。完成后给出耗时与错误清单。

- **可以安全重跑**：已归档的文章按导出记录跳过，不会重复抓取
- **失败会自动重试**：只有 `status = exported` 才跳过，`failed` 的记录下次仍会尝试
- **中途可中断**：关掉页面会取消请求，已完成的条目状态已落库，不会白跑

### 3.2 单篇归档

在文章详情 / 列表的导出按钮上单独触发，走同一套脚本与状态表。适合补捞漏网的一篇。

### 3.3 归档后生成摘要

同一分区下的「**摘要生成**」：为**已归档但还没有摘要**的 Markdown 调 LLM 生成结构化摘要，写入每篇目录下的 `summary.md`（YAML frontmatter + 正文）：

```yaml
title: 标题
account: 公众号名
source_url: https://mp.weixin.qq.com/s/xxx
published_at: 2026-09-20
summary: 200 字以内中文摘要
themes: [主题1, 主题2]
keywords: [关键词...]
must_read: 8
highlights: [关键观点1, 关键观点2]
generated_at: 2026-09-21T20:10:00+08:00
```

每篇约 10–30 秒，会消耗 API 额度。需要先配好 `llm_base_url` / `llm_api_key` / `llm_model`，否则接口返回 503。

---

## 4. 脚本契约

chatlog 通过 `bash <script> <url> -o <output_dir>` 调用脚本：

| 项 | 约定 |
|---|---|
| 入参 | `$1` = 微信文章 URL（只允许 `https://mp.weixin.qq.com/...`） |
| 输出根 | `-o <md_export_dir>`，**必须**是既定根目录 |
| 超时 | 单篇 120 秒，超时按失败计 |
| 退出码 | `0` 成功 / `1` 参数错误 / `2` 依赖缺失 / `3` 抓取失败 / `4` 输出目录不可写 |

**输出结构（硬约定，chatlog 靠它反推账号与标题）**

```
<md_export_dir>/<账号名>/<文章标题>/<文章标题>.md
<md_export_dir>/<账号名>/<文章标题>/images/...
```

判定成功的方式是**前后快照对比**：调用前扫描目录收集所有 `.md` 路径，调用后再次扫描，
新增的路径即为本次产物——**没有新增 `.md` 就判为失败**，即使脚本退出码为 0。

因此脚本必须满足：

1. 退出码为 0 **且** 真的产出了新的 `.md` 文件
2. `.md` 落在 `<根>/<账号>/<标题>/` 两层子目录里（只落一层时账号能推出、标题退化）
3. 不写日志文件到输出根目录（会被当成噪声；日志走 stderr）

---

## 5. 数据与状态

归档状态**只有一个事实来源**：`<work_dir>/biz_articles.db` 里的 `biz_exported_articles` 表。

| 字段 | 说明 |
|---|---|
| `article_id` | 关联 `biz_articles.id`，`UNIQUE` 约束 —— 一篇一条记录 |
| `source_url` | 冗余存 URL，便于按链接反查 |
| `md_path` | 产出的 Markdown 绝对路径 |
| `summary_path` | 生成的摘要路径（未生成时为空） |
| `status` | `exported` / `failed` |
| `error` | 失败原因（脚本 stderr 摘要） |
| `exported_at` / `summary_at` | 时间戳 |

**两条口径必须一致**（有测试守着，见 `store_test.go` 的 `TestExportStatsPendingMatchesBatchSelection`）：

- 页面上的「**待归档 N 篇**」= 最近 30 天内、未归档、**且排除已隐藏账号**的文章数
- 批量导出实际选取的范围 = 同一套条件（`GetUnexportedArticles`）

如果两者口径不一致，会出现「显示 37 篇待归档，点了只跑 12 篇」这种说不清的错位。

---

## 6. HTTP 接口

| 语义 | 新路径 | 旧路径（已废弃） |
|---|---|---|
| 单篇归档 | `POST /api/v1/biz/articles/:id/export` | — |
| 批量归档 | `POST /api/v1/biz/admin/export/batch` | `POST /api/v1/biz/export/batch` |
| 归档状态 | `GET  /api/v1/biz/admin/export/status` | `GET  /api/v1/biz/export/status` |
| 单篇摘要 | `POST /api/v1/biz/articles/:id/summary` | — |
| 批量摘要 | `POST /api/v1/biz/admin/batch/summary` | `POST /api/v1/biz/summary/generate-all` |

旧路径**没有删除**，而是注册成带废弃标记的别名：响应会带 `Deprecation: true` 与
`Link: <新路径>; rel="successor-version"`，并在服务端日志里留一条 warn。
前端已全部切到新路径，旧路径留作过渡期观察，后续再摘。

```bash
# 批量归档（days / limit / ghIDs 都可选，ghIDs 为空则全部可见账号）
curl -X POST localhost:5030/api/v1/biz/admin/export/batch \
  -H 'Content-Type: application/json' \
  -d '{"days":30,"limit":100}'

# 响应
{
  "total": 37, "success": 35, "failed": 2, "skipped": 0,
  "details": [{"articleID": 12, "title": "…", "account": "…", "mdPath": "…", "status": "exported", "duration": "2.1s"}],
  "errors": ["某标题: mdexport: script failed: exit status 3"],
  "duration": "1m18s"
}
```

---

## 7. 故障排查

| 现象 | 原因 | 处理 |
|---|---|---|
| 页面提示 ⚠ 未配置 | `md_export_script` / `md_export_dir` 有任一为空 | 补配置后**重启** `chatlog server`（配置在启动时读取） |
| `mdexport: script not found` | 脚本路径错，或不可执行 | 用绝对路径；`chmod +x` |
| `script failed: exit status 2` | 抓取器未安装 | `pip install wechat-article-to-markdown` |
| `exit status 4` | 输出目录不可写 | 检查目录权限与磁盘空间 |
| `no new .md file found after export` | 脚本跑完了但没产出，或产出目录不在根下 | 手动执行脚本确认输出结构符合 §4 |
| 一直卡住然后超时 | 微信侧返回验证码 / 限流 | 降低 `limit` 分批跑，或换抓取脚本 |
| 重复归档同一篇 | 状态表被清或换过 `work_dir` | 换 `work_dir` 会导致状态丢失，属预期 |
| 摘要接口返回 503 | 未配 LLM | 补 `llm_base_url` / `llm_api_key` / `llm_model` |

---

## 8. 与旧版差异（如果你用过 `chatlog biz2md`）

旧方案是「**命令行 + Obsidian Web Clipper 浏览器插件**」双轨：CLI 扫库产出候选名单
（`--list` / `--queue` / `--status` / `--manifest`），由你在浏览器里正常阅读文章、
让 Clipper 抓正文写 vault，状态记在 `<work_dir>/biz_archived.db`。

现已完全收敛到 Web 一条链路：

| | 旧 | 新 |
|---|---|---|
| 入口 | `chatlog biz2md` 命令 | Web「管理 → 批量任务」 |
| 抓正文 | 浏览器插件（用户浏览时） | 服务端脚本（点击时） |
| 状态存储 | `biz_archived.db` / `biz_archived` | `biz_articles.db` / `biz_exported_articles` |
| 模板 / 队列文件 | `--manifest` 导出 YAML 给 Clipper | 不再需要 |
| 摘要在哪 | 无 | 同篇目录下 `summary.md` |

已删除的代码：`internal/chatlog/bizarch/`、`cmd/chatlog/cmd_biz2md.go`、
`internal/chatlog/conf/bizarchive.go`、`scripts/biz2md-clipper-sim/`、`scripts/biz2md-playwright/`。
`Makefile` 里相关的 `test-pytest` / `test-all` / `biz2md-demo` 目标同步移除。

**没有做状态数据迁移。** 如果你之前用旧 CLI 归档过，新功能会认为那些文章「未归档」而重新跑一遍。
这本身是安全的（重跑幂等，文件会被覆盖），但如果你不想重抓，可以手动清理或接受这次重复开销。

---

## 附录 · 相关代码位置

| 位置 | 职责 |
|---|---|
| `internal/chatlog/bizhub/handler.go` | 路由注册、导出 / 摘要 handlers |
| `internal/chatlog/bizhub/bizhub.go` | `ExportArticle` / `ExportBatch` / `GetExportStatus` |
| `internal/chatlog/bizhub/mdexport/exporter.go` | 脚本调用、快照对比、路径反推 |
| `internal/chatlog/bizhub/store.go` | `biz_exported_articles` 建表与读写 |
| `internal/chatlog/bizhub/summary_gen.go` | 摘要生成与 `summary.md` 落盘 |
| `internal/chatlog/bizhub/templates/admin.html` | 「批量任务」分区前端 |
| `script/export-md.sh` | 参考抓取脚本 |
| `internal/chatlog/bizhub/api_contract_test.go` | 新旧路径行为一致性契约测试 |
