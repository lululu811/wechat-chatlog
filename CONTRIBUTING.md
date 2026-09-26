# 贡献指南

## 0. 先读这一段

本仓库是 [sjzar/chatlog](https://github.com/sjzar/chatlog) 的 **fork**，属于个人二次开发项目。

- **不提供任何技术支持。** Issue 不回复、不处理；PR 不保证响应、不保证合并。
- 维护者只有一人，且只在个人使用场景下维护本仓库。
- 遇到问题请先看 [SUPPORT.md](./SUPPORT.md) 的分流建议，多数情况答案是"去上游"。
- 使用本项目产生的一切后果自负，见 [DISCLAIMER.md](./DISCLAIMER.md)。

带着"提个 Issue 等作者修"的预期来，会失望。这不是客气话，是提前说明。

---

## 1. PR 该提到哪里

**这是本仓库唯一重要的规则。** 提错地方等于白提。

| 你改的东西 | 提到哪 |
|---|---|
| 密钥提取、解密、数据库解析、TUI、消息模型、跨平台适配 —— 即上游已有代码 | **上游 [sjzar/chatlog](https://github.com/sjzar/chatlog)** |
| 下列 fork 自有模块（见下节） | 本仓库 |
| 只是自己项目里打了个补丁 | 你的 fork，不要提 PR |

### fork 自有模块清单

改到这些目录的，才属于本仓库的范围：

```
internal/chatlog/bizhub/          公众号汇总（文章库、标签、收藏、LLM 汇总、Web UI）
internal/chatlog/http/chatstat.go  聊天记录统计
internal/chatlog/http/middleware.go HTTP 访问鉴权
internal/chatlog/http/static/      HTTP 首页（API 控制台）
cmd/chatlog/cmd_pipeline.go        bizhub pipeline CLI
frontend/                          公众号汇总前端（Vue 3 + Vite）
.github/workflows/                 CI 与发布流程
```

### 判断标准

拿不准的时候问自己一句：**这段代码在上游存在吗？**

- 存在 → 提给上游。改完之后本仓库需要的话，再单独提一个"同步上游"的 PR。
- 不存在 → 提到这里。

对上游已有代码的修复，即使你顺手在本仓库改了，也**不要**在这里提 PR。本仓库不是上游的补丁分发站 —— 那样做只会让两边都难维护。

---

## 2. 提 PR 之前

1. **先搜上游 Issues。** 大部分"密钥提不出来""微信版本不识别"的问题上游已经有人在跟了。
2. **确认不是环境问题。** 关闭 SIP、给完全磁盘访问权限、`data_key` 填错、微信版本超出支持范围 —— 这些是配置问题，改代码解决不了。
3. **跑通门禁。** 本仓库 CI 会执行：

   ```bash
   gofmt -l .        # 必须无输出
   go vet ./...
   go test ./... -cover
   go test -race ./internal/chatlog/bizhub/...
   make build
   ```

   本地跑一遍再推。`gofmt` 和 `go vet` 是硬门禁，不通过直接被关掉。

4. **改前端要一起交产物。** `frontend/` 改了，`internal/chatlog/bizhub/static/dist/` 是 `go:embed` 进去的构建产物，必须一起更新：

   ```bash
   cd frontend && npm ci && npm run build
   ```

5. **一个 PR 一件事。** 重构 + 改行为 + 换依赖打成一个，评审无从下手。

---

## 3. Commit 规范

沿用 [Conventional Commits](https://www.conventionalcommits.org/)：

```
feat(bizhub): 支持按标签过滤文章流
fix(http): 首页表单在鉴权部署下不再 401
docs(readme): 补充 chatstat 参数上限
refactor(store): 抽出账号标签的批量写入
chore(ci): release 缺 Docker Hub secret 时跳过镜像推送
```

子系统（scope）用受影响模块名：`bizhub` / `http` / `wechat` / `wechatdb` / `ui` / `frontend` / `ci`。

---

## 4. 明确不接受的 PR

不是不礼貌，是维护者确实不会合并：

- 修复上游已有代码的问题（请去上游）
- 新增与公众号管理、聊天记录读取无关的功能
- 引入新的运行时依赖（尤其带 cgo 的库，会破坏现有平台的交叉编译）
- 引入远程服务依赖 —— 本项目的核心前提是数据本地处理，正文与消息不出本机
- 纯格式化的无关改动（`gofmt` 覆盖到的除外）
- 大规模重写 UI 视觉风格
- 携带密钥、Token、`*.db`、真实聊天数据的提交（见 [SECURITY.md](./SECURITY.md)）

---

## 5. 行为准则

参与即视为接受 [CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md)。
