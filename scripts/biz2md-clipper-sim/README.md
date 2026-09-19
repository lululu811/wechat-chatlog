# biz2md-clipper-sim

**这是什么**:`chatlog biz2md` 的命令行联调/演示工具。

**它不是什么**:**不是** Obsidian Web Clipper 的替代品。真实生产环境请用 [Obsidian Web Clipper](https://obsidian.md/clipper)(官方浏览器扩展)或 [nextcaicai/obsidian-clipper-cn](https://github.com/nextcaicai/obsidian-clipper-cn)(中文增强版)。

## 为什么需要它

Obsidian Web Clipper 是 **MV3 浏览器扩展**,只能在 Chrome / Firefox 里跑。它依赖:

1. 真实浏览器会话(用户的微信登录 cookie)
2. 真实 mp.weixin.qq.com DOM(`#js_content` `#js_name` `#publish_time`)
3. 微信**不会**给非微信客户端 UA 返回正文(命令行 `curl` 拿到的是 TCaptcha 验证码页)

这意味着**纯命令行环境无法跑通真实 Clipper 链路**。本工具用于:

- 在 CI / 自动化里验证 chatlog 端到 chatlog 内部 + vault 写入的协议链路
- 在没有微信账号的开发者机器上做集成测试
- 给文档提供可重放的 demo

## 它跟真 Clipper 的差距

| 维度 | 本工具 | 真实 Obsidian Web Clipper |
|---|---|---|
| 触发 | 命令行主动跑 | 用户打开浏览器文章 → 扩展自动触发 |
| 正文来源 | chatlog sqlite 的 `desc` 字段(240 字摘要) | 浏览器渲染后的 DOM 全文 |
| 反爬绕过 | ❌ 不需要(本地 sqlite) | ✅ 自动(微信 cookie + 正常 UA) |
| 图片下载 | ❌ 跳过 | ✅ 模板里 `behavior.download: true` |
| 写 vault | python `Path.write_text()` | Obsidian 内置 CLI `obsidian create ...` |
| 写 biz_archived | python 直连 sqlite | 暂无(V2 reconcile) |

## 使用

```bash
# 1. 安装依赖
pip install -r requirements.txt

# 2. 编辑 clipper_sim.py 顶部的 CHATLOG_DB / CHATLOG_HTTP / VAULT 常量

# 3. 跑
python clipper_sim.py
```

默认会:取"付鹏的财经世界"(`gh_423b608e0744`)最近 3 条推送 → 拼 frontmatter + desc → 写到 vault → mark biz_archived。

## 输出

vault 里会出现形如:

```
<vault>/公众号/付鹏的财经世界-2026-09-07-【40分钟播客】贝森特为什么讲日本已成功摆脱通缩？.md
```

每篇 md 顶部有清晰的 NOTE 说明"本文件由 demo 工具生成,正文为 desc 摘要"。

## 切换到真实 Obsidian Web Clipper

1. 装扩展: <https://obsidian.md/clipper>(或 [nextcaicai/obsidian-clipper-cn](https://github.com/nextcaicai/obsidian-clipper-cn))
2. 配置 vault 路径
3. 导入模板:
   ```bash
   ./bin/chatlog biz2md --manifest > /tmp/clipper.yaml
   # 把内容粘贴到 Clipper 模板编辑器
   ```
4. 触发归档:
   ```bash
   ./bin/chatlog biz2md --list --gh gh_xxx
   # 在浏览器打开 URL,Clipper 自动写 vault
   ```
5. 验证:
   ```bash
   ./bin/chatlog biz2md --status --vault ~/path/to/vault
   ```

## 测试

```bash
pip install pytest
pytest tests/ -v
```

测试覆盖:

- `sanitize_filename`: windows 非法字符替换、过长截断
- `compose_md`: frontmatter 字段顺序、body 中 desc 引用
- `mark_archived`: sqlite 写入去重(INDEX + UNIQUE)
- 集成测试:在临时 chatlog fixture 上跑完整流程

## 不做的事

- ❌ 不抓 mp.weixin.qq.com(反爬挡)
- ❌ 不下载图片
- ❌ 不替代真 Clipper
- ❌ 不进入 chatlog 主二进制(独立脚本)

## 维护策略

本工具**只在 chatlog 接口或 sqlite 字段变更时更新**。如果你发现 chatlog 改了 biz_message 表 schema 或 conf.BizArchive 字段,请同步更新 `clipper_sim.py`。