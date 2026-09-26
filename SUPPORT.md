# 支持说明

## 本仓库不提供任何技术支持

这一条没有例外，也不接受"例外申请"。

具体表现为：

- **Issue 不回复、不处理。** 提了不会被看到，也不会有"已读"回执。
- **PR 不保证审阅、不保证合并。** 提交后没有回应是常态，不是被拒绝。
- **不提供使用指导。** 不会有人告诉你怎么配置、怎么装依赖、你的环境该怎么改。
- **不保证可用性。** 随时可能改坏、删功能、或整个仓库不再维护，没有弃用周期承诺。

维护者只有一人，在个人项目里使用本仓库的产物。没有值班、没有客服、没有 SLA。

---

## 问题该去哪里

按问题的**归属**分流，而不是按你的疑问是否紧急：

| 你的问题 | 去哪 |
|---|---|
| 密钥提取失败、微信版本不识别、解密报错 | [上游 Issues](https://github.com/sjzar/chatlog/issues) |
| 数据库解析、消息格式、跨平台编译 | [上游 Issues](https://github.com/sjzar/chatlog/issues) |
| TUI 显示、快捷键、终端兼容 | [上游 Issues](https://github.com/sjzar/chatlog/issues) |
| MCP 集成、HTTP API 的通用用法 | [上游 Issues](https://github.com/sjzar/chatlog/issues) / [上游 Discussions](https://github.com/sjzar/chatlog/discussions) |
| 公众号汇总（bizhub）、`chatstat`、HTTP 首页、访问鉴权 | 本仓库 —— 但仍然不处理，见下条 |
| 你的部署环境、网络、代理、磁盘权限 | 没有任何地方能帮你，问 AI 或搜索引擎 |
| 你的代码写错了 | 同上 |

---

## 关于在本仓库提 Issue

技术上可以提（GitHub 的功能），但**提了不会有回应**，这一点请务必理解。

本仓库保留 Issues 只是 fork 的默认行为，不构成任何支持承诺。如果你在这里提了 Issue 然后等待回复，你会一直等下去 —— 这不是 bug，是本项目的设计。

在等 Issue 的时间里，请先做这几件事，它们能解决大部分"看起来像 bug"的情况：

1. `chatlog version` 确认二进制来源正确（不要用上游 Release 的旧包）
2. 确认微信版本在支持矩阵内（见 README「平台特定说明」）
3. macOS 上确认 SIP 已按需关闭、且**给终端本身**（不是 chatlog）开了完全磁盘访问权限
4. 确认 `data_key` / `img_key` 没有抄错
5. 用 `--debug` 重跑一遍，看完整日志

---

## 使用风险自负

本工具涉及读取本机微信数据、进程内存以及 LLM 摘要。合规边界见 [DISCLAIMER.md](./DISCLAIMER.md)。

处理你自己的数据、遵守所在地法律法规，是使用者的责任。
