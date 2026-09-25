# 公众号文章推送 IMA 知识库

> 把已 MD 归档的文章 URL 推送到 IMA 知识库。在浏览器里完成，没有独立命令、没有队列文件。

**这是 `biz2md.md`（MD 归档）的下游功能**。推送的前提是文章已经 MD 归档成功 —— 这是有意为之的硬约束，目的是在本地有 md 备份、可观测、可重放。本文档与 [biz2md.md](./biz2md.md) 配合阅读。

---

## 1. 做什么 / 不做什么

### 做

- 通过 imaskai skill（`~/.claude/skills/ima/`）的 `openapi/wiki/v1/import_urls` 端点，把已归档文章的 URL 推送到指定 IMA 知识库
- 单篇推送：`POST /api/v1/biz/articles/:id/push`，UI 入口在每张文章卡片的 `📤 IMA` 按钮（仅当文章已归档时启用）
- 批量推送：任务化（与 MD 归档一致），进度可轮询、可重试、可取消、可降级、可熔断、可续跑
- 失败分类：`credential` / `skill_update` / `rate_limited` / `upstream_failed` / `invalid_url` / `timeout` / `dependency` / `script_failed` / `unknown`，沿用 mdexport 的 FailureKind 模式
- 数据状态：`pushed_status ∈ {pushed, push_failed}`，重试上限 3 次，与 MD 归档对仗
- 凭证管理：完全走 imaskai 自己的约定（`~/.config/ima/{client_id,api_key}` 或环境变量），chatlog 不持有、不轮换
- UI：admin 页面「批量任务」区有第二张卡片「IMA 推送」，与「MD 归档」并列
- 与 export 任务互不阻塞：用户可以同时跑一个 MD 归档任务和一个 IMA 推送任务

### 不做（Non-goals）

- ❌ **不绕过 imaskai 直接调 ima.qq.com** —— imaskai 已处理凭证、update check、COS 临时凭证、错误协议。绕过它会失去凭证轮换与升级提示
- ❌ **不做"未归档直接 push"** —— 链式：未 export 成功的不能 push。IMA 自己会抓原文，但强制走 export 是为了让本地有 md 备份（这是 plan 里定的硬约束）
- ❌ **不做 UI 选 KB / folder** —— 与 MD 归档一致，所有路径配置在 `chatlog-server.json`，切换 = 改配置 + 重启
- ❌ **不做定时 / cron** —— 触发永远是用户点按钮
- ❌ **不做推送内容编辑** —— IMA 自己抓，不传 md
- ❌ **不并发跑多个推送任务** —— 同一时刻只允许一个任务在跑

---

## 2. 前置条件

### 2.1 imaskai skill

仓库**不**内置 imaskai，需要单独装：

```bash
# 1. 下载 imaskai skill 包
curl -fL -o /tmp/ima.zip https://app-dl.ima.qq.com/skills/ima-skills-1.1.10.zip

# 2. 解压到 ~/.claude/skills/
unzip /tmp/ima.zip -d "$HOME/.claude/skills/"
mv "$HOME/.claude/skills/ima-skill" "$HOME/.claude/skills/ima"

# 3. 验证 ima_api.cjs 存在
ls -la "$HOME/.claude/skills/ima/ima_api.cjs"
```

> **版本要求 ≥ 1.1.10**。imaskai 的 `-200 update available` 错误码会触发「imaskai 需升级」分类，提示你去升级；但 chatlog 不强制升级，所以保持最新版本是你的责任。

### 2.2 IMA 凭证

获取地址：<https://ima.qq.com/agent-interface>

二选一配置：

**方式 A — 配置文件（推荐）：**

```bash
mkdir -p ~/.config/ima
echo "your_client_id" > ~/.config/ima/client_id
echo "your_api_key" > ~/.config/ima/api_key
chmod 600 ~/.config/ima/*
```

**方式 B — 环境变量：**

```bash
export IMA_OPENAPI_CLIENTID="your_client_id"
export IMA_OPENAPI_APIKEY="your_api_key"
```

凭证优先级：环境变量 → 配置文件。

> **不要**把凭证写入 `chatlog-server.json`。imaskai 自己管凭证，chatlog 转发请求时不会看到 client_id / api_key —— 切换凭证不必重启 chatlog。

### 2.3 chatlog-server.json 配置

```json
{
  "ima_push_skill_dir": "~/.claude/skills/ima",
  "ima_push_kb_id": "你的 IMA 知识库 ID",
  "ima_push_folder_id": "可选；默认与 KB ID 相同（根目录）",
  "ima_push_concurrency": 3
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `ima_push_skill_dir` | 是 | imaskai skill 路径。chatlog 会 stat `<dir>/ima_api.cjs` 校验存在 |
| `ima_push_kb_id` | 是 | IMA 知识库 ID。在 IMA 端「知识库设置」可以查到 |
| `ima_push_folder_id` | 否 | 目标文件夹 ID。根目录时与 KB ID 相同 |
| `ima_push_concurrency` | 否 | 批量推送并发数；默认 3，上限 8 |

启动后 `chatlog server` 会把配置读进 `Service`；改配置要**重启**才生效。

---

## 3. 协议（与 imaskai 调用契约）

chatlog 调用 `node <skill_dir>/ima_api.cjs openapi/wiki/v1/import_urls <body> <opts>`，body 与 opts 是 JSON 字符串。

### 3.1 body 格式

```json
{
  "knowledge_base_id": "<kb_id>",
  "folder_id": "<kb_id 或 folder_id>",
  "urls": ["https://mp.weixin.qq.com/s/xxx", "..."]
}
```

- `folder_id` **必填**（imaskai / IMA OpenAPI 硬约束）；根目录时传 `knowledge_base_id`
- `urls` 单批 1-10 个，chatlog 在内部按 10 个/批自动分批

### 3.2 opts 格式

chatlog 总是传 `"{}"`，让 imaskai 自己读环境变量或配置文件里的凭证。这样 chatlog 不持有凭证，凭证轮换不必重启 chatlog。

### 3.3 错误协议（两层）

**第一层 — 进程层**（进程退出码 != 0，stderr 是结构化 JSON）：

```json
{"code": -100, "msg": "未找到 IMA 凭证..."}   // 凭证缺失
{"code": -200, "msg": "发现新版本 skill：1.2.0"}  // imaskai 需升级
```

**第二层 — 业务层**（进程正常退出，stdout 是业务响应）：

```json
{"code": 0, "msg": "success", "data": {"results": {"<url>": {"url": "...", "ret_code": 0, "media_id": "m_abc"}}}}
{"code": 110021, "msg": "频率限制..."}   // 整批业务错误
```

详见 `imapush/script_contract_test.go` 里的替身脚本示例。

---

## 4. 链式约束

### 4.1 候选 SQL（`PushCandidateWhere`）

```sql
a.published_at >= ?
  AND a.gh_id NOT IN (SELECT gh_id FROM biz_accounts WHERE hidden = 1)
  AND e.id IS NOT NULL
  AND e.status IN ('exported','summary_generated')
  AND (e.pushed_status = '' OR e.pushed_status = 'push_failed')
```

**未 export 成功的文章永远进不了 push 候选集**。这与 `ExportCandidateWhere` 对仗但更严：
- `ExportCandidateWhere` 选「待 export」的（status 不对、缺记录、failed 都算）
- `PushCandidateWhere` 选「待 push」的（必须先 export 成功，且 push 状态为空或失败）

### 4.2 三层校验

链式校验在三层都生效：

1. **UI**：`📤 IMA` 按钮在 `exportStatus` ∉ {exported, summary_generated} 时 disabled
2. **API**：单篇 handler 在 `IsArticleExported(id)` 为 false 时返 409
3. **批量任务**：`PushCandidateWhere` 在 SQL 层把未 export 排除

任何一层都生效 —— 即便用户绕过 UI 直接调 API，handler 也会拦下。

### 4.3 为什么是链式

**用户决策记录**：plan 阶段用户拍板"链式（push 前必须先 export 成功）"。

**为什么不是独立**：用户决策时主要权衡：
- ✅ 链式：本地有 md 备份（IMA 端删除 / 改密后文章仍可读）；可观测（先看本地 md 是否 OK）；失败可重放（用本地 md 重新 import）
- ❌ 独立：少一步操作（不需要先归档）

我们选了链式。**这是有意为之的强约束**，文档与 UI 都需要明确告知用户「推送前请先归档」。

---

## 5. 数据与状态

### 5.1 `biz_exported_articles` 加列

`biz_exported_articles` 表增加 5 列（push 状态独立于 export 状态）：

| 列 | 说明 |
|---|---|
| `pushed_at` | 推送成功时间戳 |
| `pushed_media_id` | IMA 返回的 media_id |
| `pushed_status` | `pushed` / `push_failed` / 空字符串 |
| `push_attempts` | 累计推送尝试次数 |
| `push_kind` | 失败分类（imapush.FailureKind），成功时为空 |

### 5.2 状态机

**`pushed_status` 状态机**（与 MD 归档的 `status` 解耦）：

```
(空) --成功--> pushed
(空) --失败--> push_failed  --再次批量推送--> pushed
push_failed --attempts 用尽--> 永久失败，不再自动重试
push_failed --permanent kind--> 永久失败，需人改配置
```

### 5.3 `biz_push_jobs` / `biz_push_job_items`

新建两张任务表（不与 `biz_export_jobs` 共享 —— 见 plan §4.5.2 关于聚合 SQL 复杂度的设计取舍）：

| 表 | 关键字段 |
|---|---|
| `biz_push_jobs` | `id` / `status` / `days` / `max_items` / `concurrency` / `degraded` / `total` / 时间戳 |
| `biz_push_job_items` | `(job_id, article_id)` 主键 / `status` / `kind` / `error` / `media_id` |

字段镜像 export 任务；差异：无 `md_path`，多 `media_id`。

---

## 6. HTTP 接口

| 语义 | 路径 | 状态码 |
|---|---|---|
| 单篇推送 | `POST /api/v1/biz/articles/:id/push` | 200 / 400 / 409 |
| 建推送任务 | `POST /api/v1/biz/admin/push/jobs` | 202 / 400 / 409 |
| 任务列表 | `GET /api/v1/biz/admin/push/jobs?limit=10` | 200 |
| 任务详情 | `GET /api/v1/biz/admin/push/jobs/:id?items=true` | 200 / 404 |
| 重试未成功项 | `POST /api/v1/biz/admin/push/jobs/:id/retry` | 202 / 404 / 409 |
| 取消任务 | `POST /api/v1/biz/admin/push/jobs/:id/cancel` | 200 / 409 |
| 推送状态 | `GET /api/v1/biz/admin/push/status?days=N` | 200 |

**状态码约定**：

| 码 | 场景 |
|---|---|
| 200 | 成功 / 正常响应 |
| 202 | 任务已接受（建任务 / 重试成功，但还没跑完） |
| 400 | 未配置 IMA；`days > 365` |
| 404 | 任务 ID 不存在 |
| 409 | 单篇：未 export（链式硬约束）；任务：已有任务在跑；重试时任务在跑 |
| 500 | IMA / imaskai 调用失败 |

**单篇 409 关键**：

```json
{
  "error": "文章尚未归档成功，无法推送。请先执行 MD 归档。"
}
```

这条文案直接引导用户回到 MD 归档卡片 —— 链式校验的 UX 闭环。

```bash
# 单篇推送
curl -X POST localhost:5030/api/v1/biz/articles/123/push

# 建推送任务
curl -X POST localhost:5030/api/v1/biz/admin/push/jobs \
  -H 'Content-Type: application/json' \
  -d '{"days":30,"limit":100}'
# 202
# {"job":{"id":"job_20260923T101530_a1b2c3d4","status":"running","total":37,"concurrency":3,…}}

# 轮询进度
curl -s localhost:5030/api/v1/biz/admin/push/jobs/job_20260923T101530_a1b2c3d4?items=true
# {"job":{"status":"running","succeeded":12,"failed":3,…}, "items":[…], "activeJobID":"job_…", "percent":40, "processed":15}
```

---

## 7. 故障排查

| 现象 | 原因 | 处理 |
|---|---|---|
| 页面提示 ⚠ IMA 推送未配置 | `ima_push_skill_dir` / `ima_push_kb_id` 有任一为空 | 补配置后**重启** `chatlog server` |
| `IMA 凭证缺失` | `~/.config/ima/{client_id,api_key}` 缺 / 错；或环境变量没设 | 按 §2.2 二选一配置凭证（凭证本身在 IMA 端管理，chatlog 不持有） |
| `imaskai 需升级` | imaskai 版本 < 1.2.0（或其他新版本） | 按 §2.1 重新下载 imaskai skill 包 |
| `依赖缺失` | `node` 不在 PATH；`ima_api.cjs` 不在 skill_dir | 装 node 18+；检查 skill_dir 路径 |
| `被限流`（code=110021） | IMA 端频控 | 把 `ima_push_concurrency` 调低；连续触发会自动熔断 |
| `IMA 业务错误` | KB ID 错 / URL 不合法 / 资源不存在 | 改 `ima_push_kb_id`；检查 url 末段；这种情况永久失败，需重配 |
| 文章 `📤 IMA` 按钮禁用 | 链式约束：未归档 | 先去 MD 归档卡片归档 |
| 点了推送返 409 | 链式硬约束 | 同上 |
| 任务显示「被中断」 | 服务重启时任务还没跑完 | 正常现象，重启后会自动续跑；不想跑就等人处理或换 `work_dir` |
| 界面上「失败 30 篇」但看不到原因 | —— | 失败清单就在进度条下面；需人工处理的那类标签是红色 |
| 点了「重试」但进度不动 | 任务里有大量「需人工处理」的条目 | 先修环境（装 imaskai / 设凭证 / 改 KB ID），再重试 |
| 重复推送同一篇 | 状态表被清或换过 `work_dir` | 换 `work_dir` 会导致状态丢失，属预期 |

---

## 8. 与 MD 归档的关系

推送是 MD 归档的下游。两件事的差异：

| | MD 归档 | IMA 推送 |
|---|---|---|
| **输入** | 微信公众号 URL | 微信公众号 URL（必须是已归档的） |
| **外部依赖** | shell 脚本 + wechat-article-to-markdown | imaskai skill + IMA OpenAPI |
| **网络调用** | 抓取公众号文章原文 | 把 URL 推给 IMA，让 IMA 自己抓取入库 |
| **副作用** | 落本地 md + 图片 | 在 IMA 知识库里加一条 media |
| **失败模式** | 验证码、超时、依赖缺失、目录不可写 | 凭证缺失、KB 不存在、限流、单 URL 失败、网络错 |
| **成功标识** | md 文件绝对路径 | media_id（IMA 返回的） |
| **并发控制** | 同前快照 diff 并发不安全 → 脚本上报 MD_PATH | import_urls 单批 1-10 URL → 自动按 10/批分批 |

两者**独立任务槽位**（`activeExportJob` / `activePushJob`），互不阻塞。用户可以同时跑一个 MD 归档任务和一个 IMA 推送任务 —— 这在批量迁移场景下很有用（一边归档一边把已归档的推上去）。

---

## 9. 设计取舍记录

| 决策 | 备选 | 选择 | 理由 |
|---|---|---|---|
| 链式依赖 | 独立 push | 链式（plan 阶段用户拍板） | 本地有 md 备份；可重放；可观测 |
| 任务表新建 | 复用 export 任务 + job_type 字段 | 新建 `biz_push_jobs` | 聚合 SQL 不被 job_type 过滤污染；两张表独立演进 |
| 凭证不入配置 | 存进 chatlog-server.json | 走 imaskai 自己的 env / config | imaskai 已处理；chatlog 不持有；轮换不必重启 chatlog |
| KB / folder 配置 | UI 切换 | 配置驱动 | 与 MD 归档一致；切换 = 改配置 + 重启，避免 UI 复杂度 |
| 批量任务并发槽位 | 与 export 共享 | 独立槽位 | push 候选是「已 export」，不会撞资源；同时跑有意义 |
| 失败分类与 export 共享 | 共享 FailureKind | 独立 imapush.FailureKind | IMA 错误图谱与微信抓取器不同（无验证码）；共享会让 store 聚合 SQL 拿到无意义的「抓取器」分类 |
