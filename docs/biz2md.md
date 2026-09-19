# chatlog biz2md

把 chatlog 解密出的公众号历史推送,自动归档到 Obsidian vault 的命令行工具。

> **不是**:chatlog 自带公众号正文抓取 / 自己写 vault。
> **而是**:chatlog 列出"待归档 URL",由 **Obsidian Web Clipper**(浏览器扩展)在用户浏览时把正文抓下来并写入 vault。chatlog 负责"候选名单 + 去重 + 状态查询",Clipper 负责"抓正文 + 写 md"。

---

## 三件套

| 工具 | 谁维护 | 干啥的 |
|---|---|---|
| **chatlog biz2md**(本工具) | chatlog 团队 | 扫 sqlite,列出"该归档什么",记状态 |
| **[Obsidian Web Clipper](https://obsidian.md/clipper)** | Obsidian 官方 | 用户打开 mp.weixin.qq.com 文章时,自动抓正文 + 写 vault |
| **Chatlog + Clipper 的文件沟通** | 你 | 通过队列文件 / vault 文件名 / 配置文件交换信息 |

**为什么不自己抓 mp.weixin.qq.com?**
微信对非微信客户端 UA 抓取有反爬(经常返回"该公众号未授权")。让用户在浏览器里"正常阅读"是最低反爬风险的方式。Clipper 已经在做这件事,无需重复发明轮子。

---

## 安装

### 1. 装 Obsidian Web Clipper

- 官方版: https://obsidian.md/clipper(支持 Chrome / Firefox / Safari / Edge)
- **强烈推荐中文增强版** [nextcaicai/obsidian-clipper-cn](https://github.com/nextcaicai/obsidian-clipper-cn) — 微信公众号 lazy-load 图片、多图保留等场景做了专门优化

装好后:
1. 在 Clipper 弹窗里点击 "Open settings"
2. 配置 vault 路径
3. 启用 "Enable template variables"

### 2. 导入 chatlog 推荐的模板

```bash
chatlog biz2md --manifest > /tmp/clipper-biz.yaml
```

把 `/tmp/clipper-biz.yaml` 内容复制到 Clipper 的"添加模板 → 粘贴 YAML"。

模板要点:
- `triggers: [url: 'https://mp.weixin.qq.com/s']` — 只对公众号文章页生效
- `noteFolder: '公众号/{{author}}'` — 每公众号一个子文件夹
- `noteName: '{{author}}-{{publishedDate | date("YYYY-MM-DD")}}-{{title}}'` — 文件名含作者 + 日期

### 3. 确保 chatlog 已经解密过

```bash
chatlog decrypt -d <data_dir> -k <data_key> -p darwin -v 4 -w <work_dir>
# 或用 TUI 启动一次让 chatlog 自动跑
```

确认 `<work_dir>/db_storage/contact/contact.db` 和 `<work_dir>/db_storage/message/biz_message_0.db` 存在。

---

## 使用

### `--list` 看候选 URL

```bash
chatlog biz2md --list --gh gh_423b608e0744 --since 2026-08-01
```

输出三列: 发布时间、ghID、公众号昵称、URL。

```bash
# 不传 --gh = 全部已关注的公众号
chatlog biz2md --list --limit 50

# JSON 输出(给脚本用)
chatlog biz2md --list --json --gh gh_xxx > /tmp/candidates.json
```

### 触发归档

在浏览器打开 `--list` 列出的 URL,Clipper 自动识别 mp.weixin.qq.com → 套用模板 → 写 vault。

### `--queue` 写队列文件

```bash
chatlog biz2md --queue --gh gh_423b608e0744 --queue-path ~/chatlog-biz-clipper-queue.json
```

写入一个 JSON 数组文件,供未来的 Clipper CLI 读取。本期 Chatlog 不自动读取,**主要给脚本化场景用**。

### `--status` 查归档状态

```bash
chatlog biz2md --status --vault ~/Documents/MyVault
```

输出:

```
vaultPath   : /Users/.../Documents/MyVault
archiveDB   : /Users/.../.chatlog/work/db_storage/../biz_archived.db
archiveLog  : 38 条
vaultFiles  : 35 个
pending     : 12 条
```

含义:

| 字段 | 来源 |
|---|---|
| `archiveLog` | chatlog `biz_archived` sqlite 表的行数(用户手动 / 手动 mark 的总数) |
| `vaultFiles` | vault 下文件名匹配 `<author>-YYYY-MM-DD-...md` 的文件数(Clipper 实际写了多少) |
| `pending` | chatlog `biz_message_*.db` 里有、但 `biz_archived` 表没有的 URL 数 |

`vaultFiles` 与 `archiveLog` 可能有差异——Clipper 可能多次写同一 URL(目前 chatlog 没有自动 reconcile)。

### `--manifest` 输出 Clipper 模板

```bash
chatlog biz2md --manifest
```

打到 stdout,纯文本,可直接重定向到文件。

---

## 配置文件

`~/.chatlog/chatlog.json` 里可以预先填 vault 路径和队列路径:

```json
{
  "platform": "darwin",
  "version": 4,
  "work_dir": "/Users/.../chatlog-work",
  "biz_archive": {
    "vault_path": "/Users/.../Documents/MyVault",
    "queue_path": "/Users/.../chatlog-biz-clipper-queue.json",
    "clipper_template": "公众号归档 (chatlog biz2md)"
  }
}
```

CLI flag 优先于配置文件:

```bash
# 临时换 vault 路径
chatlog biz2md --status --vault /tmp/another-vault

# 临时换 work_dir
chatlog biz2md --list --work-dir /tmp/another-work
```

---

## 数据存储

`biz_archived` 表放在 `<work_dir>/biz_archived.db`(chatlog 自己新建的 sqlite 文件,不污染 contact/message/biz_message 三个微信原 db)。

```sql
CREATE TABLE biz_archived (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  gh_id TEXT NOT NULL,
  url TEXT NOT NULL UNIQUE,
  title TEXT,
  archived_at INTEGER NOT NULL,
  source TEXT NOT NULL DEFAULT 'clipper'  -- clipper / manual
);
CREATE INDEX biz_archived_gh ON biz_archived(gh_id);
CREATE INDEX biz_archived_time ON biz_archived(archived_at);
```

本期 **chatlog 不会主动往这张表写数据**(Clipper 是浏览器扩展,跟 chatlog 是两个进程),由用户在 vault 里看到 md 后**手动 mark**(TODO:后续版本加入 scan vault 反向 reconcile)。

---

## 已知限制

- **覆盖度**: chatlog 本地 biz_message_0.db 只覆盖你**已关注的公众号的部分历史**(macOS 4.x 微信策略,典型 ~58%)。未覆盖的公众号,只能等用户在浏览器里打开时由 Clipper 自动抓。
- **去重**: chatlog 只去重"chatlog 自己记录过的 URL"。Clipper 自己如果在 vault 里写了重复文件,chatlog 不知道。
- **图片**: chatlog 不下载公众号图片,留给 Clipper 模板里 `behavior.download: true`。
- **状态同步**: `archiveLog` ↔ `vaultFiles` 没有自动对齐机制。如果你删了 vault 里的文件但 chatlog 表里还记着,需要手动 `DELETE FROM biz_archived`。

---

## 故障排查

### `chatlog biz2md` 报 "WorkDir 为空"

需要在 `~/.chatlog/chatlog.json` 里设置 `work_dir`,或运行时传 `--work-dir <path>`。

### `--list` 报 "contact.db not found"

chatlog 还没解密过。跑 `chatlog decrypt -d <data_dir> -k <data_key> -p <plat> -v <ver> -w <work_dir>`。

### `--list` 报 "open wechatdb: platform unsupported"

platform / version 不对。看一眼你的 `chatlog.json` 里 `platform` 和 `version` 字段对不对(常见组合:`darwin` + `4` 或 `windows` + `4`)。

### `--status` 输出 `pending = 0`

要么 chatlog 本地没有这个公众号的 biz_message(本地覆盖不到),要么你已经全部归档过了。

---

## 相关资源

- Obsidian Web Clipper: https://obsidian.md/clipper
- 中文增强版: https://github.com/nextcaicai/obsidian-clipper-cn
- chatlog 主项目: https://github.com/sjzar/chatlog
- 公众号覆盖度限制说明: `docs/biz2md.md` 同目录(就是本文件)