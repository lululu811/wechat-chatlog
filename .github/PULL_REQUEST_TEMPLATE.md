<!--
  这个模板的作用是让提错地方的人在提交前就看到分流规则。
  规则全文见 CONTRIBUTING.md 第 1 节。
-->

## ⚠️ 提交前先确认

本仓库是 [sjzar/chatlog](https://github.com/sjzar/chatlog) 的 fork，**不提供技术支持，PR 不保证被审阅或合并**。

如果你修改的是**上游已有的代码**（密钥提取、解密、数据库解析、TUI、消息模型、跨平台适配等），
请关闭这个 PR，改提给上游：<https://github.com/sjzar/chatlog>

本仓库只接收 fork 自有模块的改动：

- `internal/chatlog/bizhub/`（公众号汇总）
- `internal/chatlog/http/chatstat.go`、`middleware.go`、`static/`
- `cmd/chatlog/cmd_pipeline.go`
- `frontend/`
- `.github/workflows/`

---

### 这个 PR 属于哪个范围

<!-- 必填。若勾选"上游已有代码"，请不要提交。 -->

- [ ] 改动只涉及上方列出的 fork 自有模块
- [ ] 我确认改动不涉及上游已有代码

### 变更内容

<!-- 做了什么，为什么。 -->

### 验证方式

<!-- 必填：贴出你实际跑过的命令与结果。CI 会跑 gofmt / vet / test / race / build，
     本地先跑一遍能省掉一轮来回。 -->

```bash
gofmt -l .
go vet ./...
go test ./... -cover
go test -race ./internal/chatlog/bizhub/...
make build
```

输出：

```
```

### 涉及前端

- [ ] 不涉及前端
- [ ] 涉及：已执行 `cd frontend && npm ci && npm run build`，并把
      `internal/chatlog/bizhub/static/dist/` 的产物一并提交

### 安全自查

- [ ] 提交内容不含任何密钥、Token、`*.db`、真实聊天数据或真实 wxid
      （详见 [SECURITY.md](../SECURITY.md)）
- [ ] 不引入新的远程服务依赖（本项目前提是数据本地处理）
- [ ] 不引入新的运行时依赖，尤其是带 cgo 的库

### 关联

<!-- Issue 编号、上游 PR 链接，或"无"。 -->
