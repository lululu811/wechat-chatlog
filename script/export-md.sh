#!/bin/bash
# export-md.sh — 微信公众号文章转 Markdown
#
# 契约：
#   export-md.sh <url> -o <output_dir>
#
# 输出结构：
#   <output_dir>/<account>/<title>/<title>.md
#   <output_dir>/<account>/<title>/images/...
#
# 依赖：
#   pip install wechat-article-to-markdown
#   或: uv tool install wechat-article-to-markdown
#
# 退出码：
#   0  成功
#   1  参数错误
#   2  依赖缺失
#   3  抓取失败（验证码、超时等）
#   4  输出目录不可写

set -euo pipefail

# --- 参数解析 ---
URL=""
OUTPUT_DIR=""

usage() {
    echo "Usage: $0 <url> -o <output_dir>" >&2
    exit 1
}

if [ $# -lt 3 ]; then
    usage
fi

URL="$1"
shift

while [ $# -gt 0 ]; do
    case "$1" in
        -o)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage
            ;;
    esac
done

if [ -z "$URL" ] || [ -z "$OUTPUT_DIR" ]; then
    usage
fi

# --- 前置检查 ---

# 检查输出目录
if [ ! -d "$OUTPUT_DIR" ]; then
    mkdir -p "$OUTPUT_DIR" 2>/dev/null || {
        echo "Cannot create output dir: $OUTPUT_DIR" >&2
        exit 4
    }
fi

if [ ! -w "$OUTPUT_DIR" ]; then
    echo "Output dir not writable: $OUTPUT_DIR" >&2
    exit 4
fi

# 检查 wechat-article-to-markdown CLI
if ! command -v wechat-article-to-markdown &>/dev/null; then
    echo "wechat-article-to-markdown not found in PATH" >&2
    echo "Install: pip install wechat-article-to-markdown" >&2
    echo "     or: uv tool install wechat-article-to-markdown" >&2
    exit 2
fi

# --- 执行转换 ---
echo "[export-md] URL: $URL" >&2
echo "[export-md] Output: $OUTPUT_DIR" >&2

wechat-article-to-markdown "$URL" -o "$OUTPUT_DIR" 2>&1 | while IFS= read -r line; do
    echo "[export-md] $line" >&2
done

EXIT_CODE=${PIPESTATUS[0]}

if [ $EXIT_CODE -ne 0 ]; then
    echo "[export-md] Failed with exit code $EXIT_CODE" >&2
    exit 3
fi

echo "[export-md] Success" >&2
exit 0
