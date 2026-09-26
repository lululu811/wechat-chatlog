# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

chatlog is a Go tool for extracting, decrypting, and serving WeChat chat history from local SQLite database files. It supports Windows and macOS, WeChat 3.x and 4.x, and exposes data via Terminal UI, HTTP API, MCP Streamable HTTP, and Webhook.

Module: `github.com/chenliitaz/chatlog` · Go 1.24 · **CGO required** (mattn/go-sqlite3, sjzar/go-silk, sjzar/go-lame).

## Common commands

```bash
go mod download                  # deps
make build                       # current platform -> bin/chatlog
make crossbuild                  # darwin/linux/windows × amd64/arm64
make test                        # go test ./... -cover
make lint                        # golangci-lint run ./...
make tidy                        # go mod tidy
make all                         # clean + lint + tidy + test + build
go run main.go                   # run TUI
chatlog key / decrypt / server   # CLI subcommands
chatlog chatstat --days 7 --top 20
```

CGO must be enabled; cross-build requires a C toolchain for the target (e.g. `gcc-aarch64-linux-gnu` for linux/arm64). Release is cut by pushing a `vX.Y.Z` tag — `.github/workflows/release.yml` (goreleaser) handles the rest.

## Architecture

### Request / data flow

```
cmd/chatlog/*  (cobra commands: root/key/decrypt/server/chatstat/dumpmemory/version)
       │
       ▼
internal/chatlog/Manager        ── orchestrates everything
   ├── ctx.Context              ── runtime state + config (conf.ServerConfig / config.Manager)
   ├── wechat.Service           ── wraps internal/wechat (process detection, key extraction, decrypt, auto-decrypt via filemonitor)
   ├── database.Service         ── opens decrypted sqlite files via wechatdb.DB
   ├── http.Service             ── gin router (route.go) + middleware + /mcp endpoint
   │      └── bizhub.Service    ── public-account sync/fetcher/LLM summarizer, mounted into http
   └── App (tview TUI)          ── menu / form / modal UI; calls back into Manager
```

### Subsystem boundaries

- **`internal/wechat/`** — finding WeChat processes and pulling keys out of them.
  - `process/{darwin,windows}/detector*.go` — per-OS process discovery.
  - `key/{darwin,windows}/v{3,4}*.go` — per-OS × per-version key extraction; `key/extractor.go` is the version-agnostic entry point (`NewExtractor(platform, version)`).
  - `decrypt/{darwin,windows}/v{3,4}.go` + `decrypt/decryptor.go` — SQLCipher-style DB decryption; `decrypt/validator.go` sanity-checks candidate keys.
  - `Account` is the high-level facade: `GetKey` / `DecryptDatabase`.

- **`internal/wechatdb/`** — reading decrypted SQLite data.
  - `datasource/` splits by `{darwinv3, windowsv3, v4}` because each WeChat version lays out DB files differently (including split-DB merging via `datasource/dbm/`).
  - `repository/` provides version-agnostic queries (messages, contacts, chatrooms, sessions, media) on top of datasources.
  - `DB` is the public entry; supports `fsnotify` callbacks for auto-decrypt.

- **`internal/chatlog/`** — orchestration layer.
  - `conf/` holds typed config for TUI vs. server mode (env overrides, `chatlog-server.json`, `CHATLOG_WEBHOOK*`).
  - `ctx/` holds runtime `Context` (current account, keys, paths, status flags).
  - `database/service.go` owns the `wechatdb.DB` lifecycle and exposes paged queries.
  - `http/` — `route.go` registers `/api/v1/*`, `/mcp`, and static/media routes; `chatstat.go` adds `/api/v1/chatlog/stat`.
  - `bizhub/` — public-account pipeline: `syncer` (from WeChat DB) → `fetcher` (mp.weixin.qq.com, 6-way concurrency) → `llm.go` (structured `{summary, themes, mustReads, …}` output) → `store.go` (`biz_articles.db` in workDir) → `mdexport/` (Markdown archive). Routes live in `handler.go`; pages in `templates/`, shared assets in `static/`.
  - `webhook/` — HTTP POST callbacks on new messages matching talker/sender/keyword filters.
  - `wechat/service.go` — thin adapter between `internal/wechat` and `Manager`.

- **`internal/mcp/`** — hand-rolled MCP Streamable HTTP + stdio (jsonrpc, SSE, initialize, tool, resource, prompt). Registered through `/mcp` by `http/mcp.go`.

- **`internal/model/`** — DTOs. The `*_darwinv3.go` / `*_v4.go` variants reflect per-version DB schemas; protobuf files in `model/wxproto/` decode packed `BytesExtra` etc.

- **`internal/ui/`** — tview/tcell widgets (menu, submenu, form, infobar, footer, help, style).

- **`internal/errors/`** — centralized error constructors. Business code should import these rather than `errors.New`/`fmt.Errorf` for typed errors; split across `wechat_errors.go`, `wechatdb_errors.go`, `http_errors.go`, `mcp.go`, `middleware.go`, `os_errors.go`.

- **`pkg/`** — reusable utilities that have no internal dependencies.
  - `config/` — JSON+env config loader with struct tags (`struct_kerys.go`).
  - `appver/` — per-OS WeChat version detection helpers.
  - `filemonitor/` — fsnotify wrapper used by auto-decrypt.
  - `util/dat2img/` — wxgf format + AES/XOR image decryption (v4).
  - `util/silk/`, `util/lz4/`, `util/zstd/` — audio and compression codecs.
  - `version/version.go` — set by `-ldflags` at build time.

### Cross-cutting conventions

- `internal/` is private to the module — don't import it from `pkg/`.
- Per-OS code uses `_darwin.go` / `_windows.go` / `_others.go` suffixes; build tags are not used.
- Per-WeChat-version code is split into `v3` / `v4` / `darwinv3` / `windowsv3` files. Adding a new version means adding a new `datasource/` impl, new `model/*_vN.go`, new `key/.../vN*.go`, and new `decrypt/.../vN*.go`.
- Logging: `internal/` uses `logrus` (`github.com/sirupsen/logrus`), `pkg/util` uses `zerolog`, `main` uses stdlib `log`. Don't mix within a package.
- Naming: lowercase package names without underscores; WeChat-facing symbols keep `Wx*`/`WX*` prefix for historical reasons.
- Config precedence: CLI flag > env var (`CHATLOG_*`) > `chatlog-server.json` > `~/.chatlog/chatlog.json` > defaults. See `pkg/config/` and `internal/chatlog/conf/`.

## Testing

- Unit tests live next to source as `*_test.go`. Run with `make test`.
- No end-to-end harness exists — for MCP/HTTP changes, manually verify via `go run main.go` → `decrypt` → `server`, then hit `/api/v1/*` and `/mcp`.
- Coverage: `go test ./... -coverprofile=cov.out`; no enforced threshold.

## Commit & PR conventions

- Branch off `main`; don't push to `main` directly.
- Conventional commits (`feat:` / `fix:` / `docs:` / `refactor:` / `chore:`), Chinese or English — be consistent.
- Tags: `vX.Y.Z` triggers goreleaser.
- PR description should name the subsystems touched (`internal/wechatdb`, `mcp`, `bizhub`, ...).

## Security constraints

- Only process the user's own local WeChat data. Never send chat content off-machine.
- Keys must never leave the local machine; `internal/wechat` has no network calls.
- `.env` and decrypted `*.db` files are gitignored; never commit keys, tokens, or decrypted DBs.
- When adding LLM integration (bizhub), credentials go through `llm_api_key` config — never hard-code or log them.
