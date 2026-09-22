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
# stdout：
#   只在最后输出一行 MD_PATH=<md 的绝对路径>，供调用方直接取用产出路径。
#   其余日志一律走 stderr —— 调用方只看 stdout 的这一行，混进别的就会解析失败。
#
# 为什么要把产出路径报出来（而不是让调用方自己扫目录）：
#   调用方判断「这次跑出了哪个文件」原来靠在 <output_dir> 上做前后快照 diff。
#   并发调用时这个办法必然错：A 在 t0 拍快照、t20 扫描，中间 B 在 t15 写好了文件，
#   于是 A 会看到两个新文件、随手挑一个（map 遍历顺序随机）—— 有可能挑到 B 的。
#   结果就是两篇文章的记录指向同一个 md，另一篇的文件成了孤儿，而且状态都是「已导出」。
#   所以改成各次调用用私有暂存目录产出、再整体搬到最终位置，由脚本自己报告路径。
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

if ! command -v wechat-article-to-markdown &>/dev/null; then
    echo "wechat-article-to-markdown not found in PATH" >&2
    echo "Install: pip install wechat-article-to-markdown" >&2
    echo "     or: uv tool install wechat-article-to-markdown" >&2
    exit 2
fi

echo "[export-md] URL: $URL" >&2
echo "[export-md] Output: $OUTPUT_DIR" >&2

# --- 私有暂存目录 ---
# 名字以 . 开头：调用方扫描输出目录时会跳过点目录，暂存中的文件不会被误认成产出。
STAGE="$(mktemp -d "$OUTPUT_DIR/.staging.XXXXXX")" || {
    echo "Cannot create staging dir under: $OUTPUT_DIR" >&2
    exit 4
}
trap 'rm -rf "$STAGE"' EXIT

# --- 执行转换（产出落在暂存目录）---
set +e
wechat-article-to-markdown "$URL" -o "$STAGE" 2>&1 | while IFS= read -r line; do
    echo "[export-md] $line" >&2
done
EXIT_CODE=${PIPESTATUS[0]}
set -e

if [ $EXIT_CODE -ne 0 ]; then
    echo "[export-md] Failed with exit code $EXIT_CODE" >&2
    exit 3
fi

# --- 定位产出 ---
# 用 -print -quit 而不是 find | head：head 提前关闭管道会让 find 收到 SIGPIPE，
# 在 set -o pipefail 下整条管道会被判为失败。
MD="$(find "$STAGE" -type f -name '*.md' -print -quit)"
if [ -z "$MD" ]; then
    echo "[export-md] No .md produced under: $STAGE" >&2
    exit 3
fi

# 暂存目录里的相对路径形如 <account>/<title>/<title>.md。
# 搬运的粒度取 <account>/<title> 这一级：images/ 就在它旁边，
# 整棵子树一起搬，md 里对图片的相对引用不会失效。
REL_DIR="$(dirname "${MD#"$STAGE"/}")"
if [ "$REL_DIR" = "." ] || [ -z "$REL_DIR" ]; then
    # 工具直接落在暂存目录根下，没有两级结构 —— 补一级再搬，保持输出结构一致
    REL_DIR="$(basename "$MD" .md)"
    mkdir -p "$STAGE/$REL_DIR"
    mv "$MD" "$STAGE/$REL_DIR/"
fi

DEST="$OUTPUT_DIR/$REL_DIR"
mkdir -p "$(dirname "$DEST")"
if [ -e "$DEST" ]; then
    # 同名文章已存在：合并进去而不是覆盖整棵树，避免把已有的 images/ 冲掉
    mkdir -p "$DEST"
    cp -R "$STAGE/$REL_DIR/." "$DEST/"
else
    mv "$STAGE/$REL_DIR" "$DEST"
fi

# --- 修正可能内嵌的暂存绝对路径 ---
# 工具把图片地址写成绝对路径时会指向已经删掉的暂存目录，搬完就成了死链。
# 替换目标是 $OUTPUT_DIR（而不是 $DEST）：暂存里的绝对路径是
# <STAGE>/<account>/<title>/images/x.png，而 $DEST 本身已经等于
# <OUTPUT_DIR>/<account>/<title>，直接换成 $DEST 会多叠一层目录。
if grep -rq -- "$STAGE" "$DEST" 2>/dev/null; then
    grep -rl -- "$STAGE" "$DEST" 2>/dev/null | while IFS= read -r f; do
        sed -i.bak "s|$STAGE|$OUTPUT_DIR|g" "$f" && rm -f "$f.bak"
    done
    echo "[export-md] 已修正产出中内嵌的暂存路径" >&2
fi

echo "[export-md] Success" >&2
echo "MD_PATH=$DEST/$(basename "$MD")"
exit 0
