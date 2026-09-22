# 公众号归档功能说明

> 文件名保留 `biz2md.md`（README 与外部链接指向它），但这里的 biz2md **不再是命令行工具**，
> 而是 chatlog Web 端「**管理 → 批量任务**」里的一个功能。

把关注的公众号历史推送抓成本地 Markdown（含图片），再按需让 LLM 为每篇生成结构化摘要。
全部在浏览器里完成，没有独立命令、没有额外的队列文件、不需要浏览器插件。

---

## 1. 做什么 / 不做什么

**做**

- 从 chatlog 已解密的消息库里挑出「关注账号 × 时间窗口」内**尚未成功归档**的文章
- 调用可配置的外部抓取脚本，把正文 + 图片落到本地目录
- 把批量归档做成**服务端任务**：进度可轮询、关掉页面不中断、服务重启可续跑
- 用一张状态表记录每篇的归档结果，区分「可重试的失败」与「需人工处理的失败」
- 为已归档的 Markdown 生成 YAML frontmatter 摘要，供「报告」页汇总消费

**不做（Non-goals）**

- ❌ **不内置抓取器** —— 抓取交给外部脚本，chatlog 只负责「挑谁、记状态」
- ❌ **不直接写 Obsidian vault** —— 输出目录就是普通本地目录，要不要用 Obsidian 打开由你决定
- ❌ **不做定时任务** —— 没有 cron / scheduler，触发永远是「用户点按钮」
- ❌ **不做抓新文章** —— 归档只处理数据库里已有的文章；让新文章入库是「账号同步」的职责
- ❌ **不做归档内容编辑** —— 生成的 `.md` 归你所有，chatlog 只写入、不回读修改
- ❌ **不并发跑多个归档任务** —— 同一时刻只允许一个任务在跑（见 §3.1）

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
  "md_export_script": "/abs/path/to/chatlog/script/export-md.sh",
  "md_export_concurrency": 3
}
```

| 键 | 必填 | 说明 |
|---|---|---|
| `md_export_script` | ✅ | 抓取脚本的**绝对路径**，需能被 `bash` 执行 |
| `md_export_dir` | ✅ | 输出根目录，不存在时自动创建 |
| `md_export_concurrency` | ❌ | 批量归档并发数，默认 `3`，上限 `8`，超出按 8 处理 |

> 前两者任一为空即视为「归档功能未启用」：页面显示 ⚠ 未配置，接口返回 400 并提示要设置的键名。
> 这里**没有默认路径兜底** —— 归档会往磁盘写文件，必须由你显式指定目录。
>
> 并发不是越高越好：抓取器打的是微信，并发越高越容易触发风控。批内失败率超过 50%
> 会自动降级为串行（见 §3.1），所以调得过高通常只会换来一次降级。

### 2.3 检查清单

```bash
# 1. 脚本可执行
bash /abs/path/to/script/export-md.sh 2>&1 | head -3

# 2. 抓取器已安装
command -v wechat-article-to-markdown

# 3. 服务端认得配置（configured 应为 true）
curl -s 'localhost:5030/api/v1/biz/admin/export/status?days=30'
# {"blocked":0,"configured":true,"days":30,"exported":1296,"pending":306}

# 4. 输出目录可写
test -w /Users/you/公众号文章 && echo OK
```

---

## 3. 使用

入口只有一个：启动 `chatlog server`，打开 Web 端 → 左侧「**管理**」→ 顶部三分区切到「**批量任务**」。

### 3.1 批量归档

1. 选**时间范围**（近 7 / 30 / 90 / 180 天，默认 30 天）
2. 填**数量上限**（默认 100，上限 1000）
3. 点「**开始归档**」，二次确认后**服务端开始跑任务**，页面立即返回

运行中显示三段式进度条（成功 / 失败 / 跳过）与实时计数；跑完给出结果摘要，
未成功的条目按**失败分类**列在下面。

时间范围直接决定实际处理范围与「待归档」计数，`days > 365` 会**显式报错**而不是静默截断。

#### 任务在服务端跑，关页面不影响

归档任务是**服务端任务**，不是一次 HTTP 请求：

- 关掉页面、切走标签页，任务照常推进
- 重新打开管理页，会自动把最近一次任务接回来继续显示进度（10 分钟内结束的也会展示结果）
- **服务重启可续跑**：启动时把上次残留的 `running` 任务标为 `interrupted`、`running` 条目退回待处理，
  然后自动重新拉起。断点续跑不需要额外的检查点机制 —— 条目状态本身就是检查点

#### 失败分两类

| 分类 | 典型 `kind` | 会不会自动重试 |
|---|---|---|
| **可重试** | `timeout` / `rate_limited` / `script_failed` / `no_output` / `unknown` | ✅ 下次批量时自动回到候选 |
| **需人工处理** | `captcha`（验证码）/ `dependency`（抓取器未安装）/ `output_dir`（目录不可写）/ `invalid_url` | ❌ 停下来等你处理 |

此外还有**重试次数上限**：可重试的失败累计 3 次后也会停下，避免在同一篇上空转、
反复给微信送请求。

页面上：

- 「**已归档 N 篇 · 近 X 天待归档 M 篇 · 其中 K 篇需人工处理**」——
  `K` 是候选集里那些已经卡住的篇数，`M` 包含它
- 未成功的条目会带分类标签列出（`触发验证码` / `抓取器未安装` / `超时` …），
  需人工处理的那类标签是红色
- 处理完之后点「**重试未成功项**」：把这次任务里**所有没成功过**的条目重新入队
  （包括之前被判「需人工处理」而跳过的 —— 你已经把环境修好了，它们该重来）

#### 并发与熔断

批内**每处理 6 篇**评估一次失败率，达到 50% 就把并发降到 1，避免持续加码给微信；
如果某一批**整批失败且全是需人工处理的失败**（典型是连续验证码），直接熔断停手，
剩下没跑的条目标为「跳过」，等你去处理。

同一时刻只允许一个归档任务在跑 —— 同时跑两批只会互相抢抓取额度。第二个请求会拿到 `409`。

#### 其他

- **可以安全重跑**：已有成功记录的文章不会再被选中
- **中途可以取消**：点「取消任务」，已经跑完的不回退，没跑的留在待处理，之后可点重试接着跑

### 3.2 单篇归档

在文章详情 / 列表的导出按钮上单独触发，走同一套脚本与状态表。适合补捞漏网的一篇。

单篇归档的失败同样会记录分类与尝试次数，因此**下一次批量归档会把它捡回来重试**。

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
| 超时 | 单篇 120 秒（批量任务另有一层 180 秒兜底），超时按 `timeout` 类失败计 |
| 退出码 | `0` 成功 / `1` 参数错误 / `2` 依赖缺失 / `3` 抓取失败 / `4` 输出目录不可写 |
| stdout | **只在最后输出一行 `MD_PATH=<md 绝对路径>`**，其余日志一律走 stderr |

**输出结构（硬约定，chatlog 靠它反推账号与标题）**

```
<md_export_dir>/<账号名>/<文章标题>/<文章标题>.md
<md_export_dir>/<账号名>/<文章标题>/images/...
```

### 为什么要求上报 `MD_PATH`

早期实现判断「这次跑出了哪个文件」靠的是**在输出根目录上做前后快照 diff**。
这个办法在并发下必然出错：A 在 t0 拍快照、t20 扫描，中间 B 在 t15 写好了文件，
于是 A 会看到两个新文件、按 map 遍历顺序随机挑一个 —— 有可能挑到 B 的。
结果就是两篇文章的归档记录指向同一个 `.md`，另一篇的文件成了孤儿，
而且两条记录的状态都写着「已导出」。**数字对得上，文件就是不对。**

现在优先用脚本自报的路径；只有当脚本不上报时，才退回快照 diff，
并且**强制串行执行**（宁慢不错），同时在日志里给一条 warn 提示该换脚本契约。

### 脚本必须满足

1. 退出码为 0 **且** 真的产出了新的 `.md` 文件
2. `.md` 落在 `<根>/<账号>/<标题>/` 两层子目录里（只落一层时账号能推出、标题退化）
3. 在 stdout 最后输出 `MD_PATH=<md 绝对路径>`；路径必须在输出根目录内、真实存在
4. 不在输出根目录下留下临时目录（参考实现用 `<根>/.staging.XXXXXX`，
   以点开头，扫描时会跳过；成功搬运后即删除）
5. 不写日志文件到输出根目录（会被当成噪声；日志走 stderr）

参考实现 [`script/export-md.sh`](../script/export-md.sh) 已经满足以上全部：
它先在私有暂存目录里产出，再把 `<账号>/<标题>` 整棵子树搬到最终位置 ——
这样并发调用互不干扰，图片的相对引用也不会失效；如果抓取器把图片地址写成绝对路径，
脚本还会把内嵌的暂存路径改写成最终路径。

---

## 5. 数据与状态

归档状态的事实来源是 `<work_dir>/biz_articles.db` 里的两张表：

### `biz_exported_articles` —— 逐篇归档结果

| 字段 | 说明 |
|---|---|
| `article_id` | 关联 `biz_articles.id`，`UNIQUE` 约束 —— 一篇一条记录 |
| `source_url` | 冗余存 URL，便于按链接反查 |
| `md_path` | 产出的 Markdown 绝对路径 |
| `summary_path` | 生成的摘要路径（未生成时为空） |
| `status` | `exported` / `summary_generated` / `failed` |
| `kind` | 失败分类（见 §3.1），成功时为空 |
| `attempts` | 累计归档尝试次数，用于重试上限判定 |
| `error` | 失败原因（脚本 stderr 摘要） |
| `exported_at` / `summary_at` | 时间戳 |

**状态机**

```
(无记录) --归档成功--> exported --生成摘要--> summary_generated
(无记录) --归档失败--> failed  --再次批量归档--> exported
```

`failed` **不是终态**。下次批量归档会把失败记录重新纳入候选，直到：
失败类型被判定为「需人工处理」，或 `attempts` 达到上限（3）。

**`attempts` 的维护规则**（改之前先想清楚，这里错一步就会丢数据）

| 事件 | attempts |
|---|---|
| 新记录 + 成功 | 0 |
| 新记录 + 失败 | 1 |
| 已有记录 + 失败 | 旧值 + 1 |
| 已有记录 + 成功 | **归零** —— 否则一次偶发失败会立刻撞上限，被误判为永久失败 |
| 已有记录 + 补摘要 | 不变（补摘要不算一次归档尝试） |

### `biz_export_jobs` / `biz_export_job_items` —— 批量任务

任务与逐篇结果都落库，这是「进度可轮询 + 重启可续跑」的全部依据。

| 表 | 关键字段 |
|---|---|
| `biz_export_jobs` | `id` / `status`（`running` / `done` / `failed` / `canceled` / `interrupted`）/ `days` / `max_items` / `concurrency` / `degraded` / `total` / 时间戳 |
| `biz_export_job_items` | `(job_id, article_id)` 主键 / `status`（`pending` / `running` / `done` / `failed` / `skipped`）/ `kind` / `error` / `md_path` |

### 两条口径必须一致

有测试守着（`store_test.go` 的 `TestExportStatsPendingMatchesBatchSelection`）：

- 页面上的「**待归档 N 篇**」= 所选时间窗内、**没有成功记录**、且**排除已隐藏账号**的文章数
- 批量归档实际选取的范围 = 同一套条件（`ExportCandidateWhere`）

如果两者口径不一致，会出现「显示 37 篇待归档，点了只跑 12 篇」这种说不清的错位。

> 历史实现这里写的是 `e.id IS NULL`（**完全没有归档记录**），而归档失败也会写一条
> `status = failed` 的记录 —— 于是一篇文章只要失败过一次，就永远不再出现在候选集里，
> 也永远不出现在 pending 计数里。用户没有任何线索能发现：数字完全对得上，文件就是少。
> 这就是本轮修掉的静默数据丢失。

---

## 6. HTTP 接口

| 语义 | 新路径 | 旧路径（已废弃） |
|---|---|---|
| 单篇归档 | `POST /api/v1/biz/articles/:id/export` | — |
| **建归档任务** | `POST /api/v1/biz/admin/export/jobs` | `POST /api/v1/biz/admin/export/batch`、`POST /api/v1/biz/export/batch` |
| 任务列表 | `GET  /api/v1/biz/admin/export/jobs?limit=10` | — |
| 任务详情 | `GET  /api/v1/biz/admin/export/jobs/:id` | — |
| 重试未成功项 | `POST /api/v1/biz/admin/export/jobs/:id/retry` | — |
| 取消任务 | `POST /api/v1/biz/admin/export/jobs/:id/cancel` | — |
| 归档状态 | `GET  /api/v1/biz/admin/export/status?days=30` | `GET  /api/v1/biz/export/status` |
| 单篇摘要 | `POST /api/v1/biz/articles/:id/summary` | — |
| 批量摘要 | `POST /api/v1/biz/admin/batch/summary` | `POST /api/v1/biz/summary/generate-all` |

旧路径**没有删除**，而是注册成带废弃标记的别名：响应会带 `Deprecation: true` 与
`Link: <新路径>; rel="successor-version"`，并在服务端日志里留一条 warn。
前端已全部切到新路径，旧路径留作过渡期观察，后续再摘。

> ⚠ **`POST /export/batch` 的行为已变**：它此前同步跑完一整批才返回 `{total, success, failed, …}`，
> 现在是**建任务并立即返回 `202` + 任务快照**。老脚本若在等 `success` / `failed` 字段会拿到空值，
> 需要改成「建任务 → 轮询 `GET /jobs/:id`」。

```bash
# 建任务（days / limit / ghIDs 都可选；ghIDs 为空则处理所有可见账号）
curl -X POST localhost:5030/api/v1/biz/admin/export/jobs \
  -H 'Content-Type: application/json' \
  -d '{"days":30,"limit":100}'
# 202
# {"job":{"id":"job_20260922T101530_a1b2c3d4","status":"running","total":37,"concurrency":3,…}}

# 轮询进度（items 默认只在任务结束后带回，避免每秒搬一份上千条的明细）
curl -s localhost:5030/api/v1/biz/admin/export/jobs/job_20260922T101530_a1b2c3d4
# {"job":{"status":"running","succeeded":12,"failed":3,"skipped":0,"pending":22,"concurrency":1,"degraded":true,…},
#  "items":[], "activeJobID":"job_…", "percent":40, "processed":15}

# 重试未成功项 / 取消
curl -X POST localhost:5030/api/v1/biz/admin/export/jobs/<id>/retry
curl -X POST localhost:5030/api/v1/biz/admin/export/jobs/<id>/cancel
```

**状态码约定**

| 码 | 场景 |
|---|---|
| `202` | 任务已接受（建任务 / 重试成功，但还没跑完） |
| `400` | 未配置归档；`days > 365` |
| `404` | 任务 ID 不存在 |
| `409` | 已有任务在跑；没有可取消的任务 |

---

## 7. 故障排查

| 现象 | 原因 | 处理 |
|---|---|---|
| 页面提示 ⚠ 未配置 | `md_export_script` / `md_export_dir` 有任一为空 | 补配置后**重启** `chatlog server`（配置在启动时读取） |
| `mdexport: script not found` / `抓取器未安装` | 脚本路径错、不可执行，或抓取器没装 | 用绝对路径 + `chmod +x`；`pip install wechat-article-to-markdown` |
| `输出目录不可写` | 目录权限或磁盘空间 | 检查目录权限与磁盘空间 |
| `脚本无产出` | 脚本退出码 0 但没写出 `.md` | 手动执行脚本确认输出结构符合 §4 |
| `触发验证码` | 微信侧风控 | 停一会儿，过掉验证码后点「重试未成功项」；同时把 `md_export_concurrency` 调低 |
| 任务显示「被中断」 | 服务重启时任务还没跑完 | 正常现象，重启后会自动续跑；不想跑就等人处理或换 `work_dir` |
| 界面上「失败 30 篇」但看不到原因 | —— | 失败清单就在进度条下面；需人工处理的那类标签是红色 |
| 点了「重试」但进度不动 | 任务里有大量「需人工处理」的条目 | 先修环境（装抓取器 / 过验证码 / 改目录权限），再重试 |
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
| `internal/chatlog/bizhub/handler.go` | 路由注册、归档任务 / 单篇导出 / 摘要 handlers |
| `internal/chatlog/bizhub/bizhub.go` | `StartExportJob` / `runExportJob`（worker 池、降级、熔断）/ `RetryExportJob` / `CancelExportJob` / `resumeInterruptedExportJobs` |
| `internal/chatlog/bizhub/mdexport/exporter.go` | 脚本调用、失败分类、`MD_PATH` 解析与回退快照 diff |
| `internal/chatlog/bizhub/store.go` | 三张表建表与读写、候选条件 `ExportCandidateWhere`、任务 CRUD |
| `internal/chatlog/bizhub/summary_gen.go` | 摘要生成与 `summary.md` 落盘 |
| `internal/chatlog/bizhub/templates/admin.html` | 「批量任务」分区前端（进度轮询、失败清单、重试 / 取消） |
| `script/export-md.sh` | 参考抓取脚本（私有暂存 → 搬运 → 上报 `MD_PATH`） |
| `internal/chatlog/bizhub/export_test.go` | 候选集 / 重试计数 / 任务计数的存储层测试 |
| `internal/chatlog/bizhub/export_runner_test.go` | 归档跑批端到端测试（替身脚本驱动全部分支） |
| `internal/chatlog/bizhub/mdexport/script_contract_test.go` | 脚本契约与**并发下产出路径不串号**的回归测试 |
| `internal/chatlog/bizhub/api_contract_test.go` | 新旧路径行为一致性契约测试 |
| `.github/workflows/ci.yml` | CI：gofmt + `go vet` + `go test` + `-race` + build |
