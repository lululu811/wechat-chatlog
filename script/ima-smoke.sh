#!/bin/bash
# ima-smoke.sh — imaskai OpenAPI 冒烟脚本
#
# 用途：
#   不依赖 chatlog 业务代码，第一次实测 import_urls 的真实契约
#   （stdout 形态、错误码、stderr 风格、110021 频控时长等），再据此写生产客户端。
#
# 用法：
#   ./script/ima-smoke.sh --knowledge-base-id <kb_id> --url <article_url> [--url <url2> ...]
#
# 必须设置的环境变量（凭证不进聊天 / 不入仓）：
#   IMA_OPENAPI_CLIENTID    Client ID（从 https://ima.qq.com/agent-interface 拿）
#   IMA_OPENAPI_APIKEY      API Key（同一页拿）
#
# 调用约定（与 imaskai ima_api.cjs 一致）：
#   node "<SKILL_DIR>/ima_api.cjs" <apiPath> <bodyJSON> <optsJSON>
#     - apiPath: 形如 "openapi/wiki/v1/import_urls"
#     - bodyJSON: 请求 body 的 JSON 字符串
#     - optsJSON: 含 {clientId, apiKey} 的 JSON 字符串
#
# 错误处理（两层，必须都检查）：
#   1. 进程退出码 != 0 + stderr 是 {"code": -100|-200, "msg": "..."}：
#        -100：程序错误（缺凭证、参数非法、网络错误），msg 可直接展示给用户
#        -200：skill 需要更新，原请求未发出，stdout 含更新上下文
#   2. 进程正常退出但 stdout JSON.code != 0：后端业务错误，直接展示 msg
#
# 这个脚本的设计目的不是"投产"，是"看一次真接口长什么样"：
#   stdout / stderr 原样透传给调用方，不做任何包装 —— 调用方就是 chatlog 未来的 imexport 包。
#   跑完之后用这份 transcript 当事实来源写生产代码，比对着文档猜要稳。

set -euo pipefail

# --- 依赖检查 ---
for bin in node jq; do
    if ! command -v "$bin" >/dev/null 2>&1; then
        echo "smoke: required binary not found: $bin" >&2
        exit 2
    fi
done

# imaskai 安装路径。优先取 SKILL_DIR 环境变量（imaskai 标准约定），否则用默认。
SKILL_DIR="${SKILL_DIR:-$HOME/.claude/skills/ima}"
if [ ! -f "$SKILL_DIR/ima_api.cjs" ]; then
    echo "smoke: imaskai not found at $SKILL_DIR/ima_api.cjs" >&2
    echo "smoke: 请先执行：curl -fL -o /tmp/ima.zip https://app-dl.ima.qq.com/skills/ima-skills-1.1.10.zip && unzip /tmp/ima.zip -d \"\$HOME/.claude/skills/\" && mv \"\$HOME/.claude/skills/ima-skill\" \"\$HOME/.claude/skills/ima\"" >&2
    exit 2
fi

# --- 凭证检查（必须从环境变量来）---
if [ -z "${IMA_OPENAPI_CLIENTID:-}" ] || [ -z "${IMA_OPENAPI_APIKEY:-}" ]; then
    echo "smoke: 缺凭证。请设置环境变量：IMA_OPENAPI_CLIENTID 与 IMA_OPENAPI_APIKEY" >&2
    echo "smoke: 获取地址：https://ima.qq.com/agent-interface" >&2
    echo "smoke: （凭证不入仓、不进聊天记录 —— 只用环境变量传）" >&2
    exit 2
fi

# --- 参数解析 ---
KB_ID=""
URLS=()

usage() {
    cat >&2 <<EOF
Usage: $0 --knowledge-base-id <kb_id> --url <url> [--url <url2> ...]

必填环境变量：
  IMA_OPENAPI_CLIENTID    Client ID
  IMA_OPENAPI_APIKEY      API Key

示例：
  export IMA_OPENAPI_CLIENTID="..."
  export IMA_OPENAPI_APIKEY="..."
  ./script/ima-smoke.sh \\
    --knowledge-base-id "abc123" \\
    --url "https://mp.weixin.qq.com/s/xxx" \\
    --url "https://mp.weixin.qq.com/s/yyy"
EOF
    exit 1
}

while [ $# -gt 0 ]; do
    case "$1" in
        --knowledge-base-id)
            KB_ID="$2"
            shift 2
            ;;
        --knowledge-base-id=*)
            KB_ID="${1#*=}"
            shift
            ;;
        --url)
            URLS+=("$2")
            shift 2
            ;;
        --url=*)
            URLS+=("${1#*=}")
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "smoke: 未知参数 $1" >&2
            usage
            ;;
    esac
done

if [ -z "$KB_ID" ]; then
    echo "smoke: 缺 --knowledge-base-id" >&2
    usage
fi

if [ ${#URLS[@]} -eq 0 ]; then
    echo "smoke: 至少给一个 --url" >&2
    usage
fi

# import_urls 单批 1-10 个，超出必须分批 —— 这是接口硬约束，不是我们设的。
if [ ${#URLS[@]} -gt 10 ]; then
    echo "smoke: import_urls 单批最多 10 个 URL，你给了 ${#URLS[@]}。先按单批跑，留作分批的契约验证。" >&2
    echo "smoke: （生产代码会按 10 个/批自动分批，这里只验一次调用）" >&2
    exit 1
fi

# --- 构造请求 body ---
# folder_id 必填；根目录时传 knowledge_base_id（imaskai 文档明确约定）
URLS_JSON=""
for u in "${URLS[@]}"; do
    # 用 jq -sR 处理每个 URL，避免手写转义
    escaped="$(printf '%s' "$u" | jq -sR .)"
    if [ -z "$URLS_JSON" ]; then
        URLS_JSON="$escaped"
    else
        URLS_JSON="$URLS_JSON,$escaped"
    fi
done
BODY=$(printf '{"knowledge_base_id":"%s","folder_id":"%s","urls":[%s]}' "$KB_ID" "$KB_ID" "$URLS_JSON")

# --- 构造 optsJSON ---
OPTS=$(printf '{"clientId":"%s","apiKey":"%s"}' "$IMA_OPENAPI_CLIENTID" "$IMA_OPENAPI_APIKEY")

# --- 调用 imaskai ---
# 把 stderr 单独捕获，stdout 单独捕获 —— 调用方必须分别检查。
ERR_FILE="$(mktemp -t ima_smoke_err.XXXXXX)"
trap 'rm -f "$ERR_FILE"' EXIT

if ! RESP="$(node "$SKILL_DIR/ima_api.cjs" "openapi/wiki/v1/import_urls" "$BODY" "$OPTS" 2>"$ERR_FILE")"; then
    # 第一层：进程非 0 退出，stderr 是结构化错误
    err_msg="$(cat "$ERR_FILE")"
    err_code="$(echo "$err_msg" | jq -r '.code // empty' 2>/dev/null || echo "")"
    case "$err_code" in
        -100)
            echo "smoke: 程序错误（-100）：$err_msg" >&2
            exit 10
            ;;
        -200)
            echo "smoke: skill 需更新（-200）：原请求未发出" >&2
            echo "$RESP" | jq . >&2   # stdout 里是更新上下文
            exit 11
            ;;
        *)
            echo "smoke: 进程非 0 退出，stderr: $err_msg" >&2
            exit 12
            ;;
    esac
fi

# 第二层：进程正常退出，检查 stdout JSON.code
echo "$RESP" | jq .

# code != 0 时把 msg 透传出去 —— 跟 imaskai 文档约定一致："直接将 msg 展示给用户"
if ! code="$(echo "$RESP" | jq -r '.code // empty')"; then
    echo "smoke: 无法解析 stdout JSON" >&2
    exit 13
fi

if [ "$code" != "0" ]; then
    msg="$(echo "$RESP" | jq -r '.msg // empty')"
    echo "smoke: 后端业务错误 code=$code：$msg" >&2
    exit 20
fi

# 成功 —— 业务侧 data.results 是 URL → {ret_code, media_id} 映射。
echo "smoke: 成功。结果写入 business 字段 data.results（{url: {ret_code, media_id}}）" >&2
echo "$RESP" | jq -r '.data.results'