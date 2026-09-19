# AGENTS.md

聊天记录工具 — 微信本地数据库解密、TUI / HTTP API / MCP 服务，支持多账号、图片/语音 (wxgf/silk) 解码。

Module: `github.com/sjzar/chatlog` · Go 1.24 · CGO required (mattn/go-sqlite3, go-silk, go-lame)

## Setup commands

- Install deps: `go mod download`
- Run (dev):   `go run main.go`                        # 启动 TUI
- Build:       `make build`                            # 当前平台到 bin/chatlog
- Cross-build: `make crossbuild`                       # darwin/linux/windows × amd64/arm64
- Test:        `make test`                             # `go test ./... -cover`
- Lint:        `make lint`                             # golangci-lint run ./...
- Tidy:        `make tidy`                             # go mod tidy
- Full check:  `make all`                              # clean + lint + tidy + test + build
- Release:     `.github/workflows/release.yml` (goreleaser, push tag 触发)

CGO 必须开启；交叉编译前确认目标平台 C 工具链可用（linux 需要 gcc-aarch64/aarch64-linux-gnu 等）。

## Project layout

- `cmd/chatlog/` — cobra 命令入口（`Execute()`）
- `internal/chatlog/` — 核心业务逻辑（解密、查询、合并）
- `internal/wechat/` — 微信客户端检测、密钥提取
- `internal/wechatdb/` — 微信数据库解析（sqlite 读取、消息/联系人/群聊）
- `internal/mcp/` — MCP Streamable HTTP 服务（mark3labs/mcp-go）
- `internal/ui/` — TUI（tview + tcell）
- `internal/model/` — 业务模型与 DTO
- `internal/errors/` — 统一错误类型
- `pkg/{appver,config,filecopy,filemonitor,util,version}/` — 可复用工具
- `script/` — 打包 / docker entrypoint
- `docs/` — `docker.md`、`mcp.md`、`prompt.md`

## Code style

- Go 标准 layout：`cmd/` + `internal/` + `pkg/`；`internal/` 仅供本模块，禁止外部 import
- `internal/errors` 提供统一定义，不在业务代码里 `errors.New`
- 日志：`internal` 用 `logrus`，`pkg/util` 用 `zerolog`，根 `main` 用 stdlib `log`
- 命名：包名小写无下划线；微信相关保留 `Wx*` 前缀（历史原因）
- 提交前 `make lint` + `make test`，CI 失败不合并

## Testing instructions

- 单元测试：`make test`（`go test ./... -cover`，每个包就近平铺 `*_test.go`）
- 加新行为先写测试；外部依赖（文件系统、sqlite、网络）通过表驱动用例覆盖
- 端到端 / 集成：当前没有 e2e harness，改动 MCP/HTTP API 时手工 `go run main.go` 验证 `decrypt` + `http` 子命令
- Coverage 报告通过 `go test ./... -coverprofile=cov.out` 生成，不强制阈值

## PR & commit conventions

- 从 `main` 切分支；不要直接 push 到 `main`
- Commit：conventional commits（`feat:` / `fix:` / `docs:` / `refactor:` / `chore:`），中英文均可但保持一致
- Tag 走 `vX.Y.Z`，发布由 `.github/workflows/release.yml`（goreleaser）触发
- PR 通过 `gh pr create`；描述写明改动的子系统（`internal/wechatdb` / `mcp` / `ui`）

## Security

- 仅处理**用户自己**的本地微信数据；不要上传任何聊天内容到远程
- 密钥提取仅在用户授权的本机执行；`internal/wechat` 永远不向网络外发密钥
- `.env` 已在 `.gitignore`；任何密钥/Token 都禁止入仓
- 平台支持矩阵（当前）：Windows、macOS；Linux 在 crossbuild 范围内但无官方包