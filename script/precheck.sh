#!/bin/bash
# chatlog 密钥提取 — 前置环境检查
# 检查 SIP / 二进制 / 微信进程 / 沙盒读权限 / FDA 授权是否就位
# 用法: ./script/precheck.sh   （可用 CHATLOG_BIN 指定二进制路径）

set -e

# 二进制默认取仓库内 bin/chatlog，可用 CHATLOG_BIN 覆盖（已装到 PATH 也行）
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${CHATLOG_BIN:-$REPO_ROOT/bin/chatlog}"


CYAN='\033[0;36m'
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'
FAIL=0

check_pass() { echo -e "${GREEN}✓${NC} $1"; }
check_fail() { echo -e "${RED}✗${NC} $1"; FAIL=$((FAIL+1)); }
check_warn() { echo -e "${YELLOW}⚠${NC} $1"; }

echo -e "${CYAN}╔══════════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║  chatlog 密钥提取前置检查                              ${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════════════╝${NC}"

# ─── 1. SIP 状态 ───
echo
echo -e "${YELLOW}[1/5] SIP 状态${NC}"
SIP=$(csrutil status 2>&1)
if echo "$SIP" | grep -q "disabled"; then
  check_pass "SIP 已禁用"
else
  check_fail "SIP 仍启用 — 进 Recovery: csrutil disable"
fi

# ─── 2. chatlog binary ───
echo
echo -e "${YELLOW}[2/5] chatlog binary${NC}"
BIN="/Users/chenlei/002_tools/chatlog/bin/chatlog"
if [ -x "$BIN" ]; then
  check_pass "binary: $BIN"
  $BIN version 2>&1 | head -2 || true
else
  check_fail "binary 不存在 — 跑 make build"
fi

# ─── 3. 微信进程 ───
echo
echo -e "${YELLOW}[3/5] 微信进程${NC}"
WX_PID=$(pgrep -f "/Applications/WeChat.app/Contents/MacOS/WeChat$" | head -1)
if [ -z "$WX_PID" ]; then
  check_fail "微信未运行 — 先启动并登录"
else
  check_pass "WeChat PID=$WX_PID"
fi

# ─── 4. 微信 ID + 沙盒路径读权限 ───
echo
echo -e "${YELLOW}[4/5] 微信沙盒路径可读性${NC}"
WXID_DIR=$(find ~/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files -maxdepth 1 -mindepth 1 -type d 2>/dev/null | head -1)
if [ -z "$WXID_DIR" ]; then
  check_fail "找不到微信 ID 目录 — 确认微信已登录"
else
  WXID=$(basename "$WXID_DIR")
  check_pass "wxid=$WXID"
  DB_FILE="$WXID_DIR/db_storage/message/message_0.db"
  if [ -r "$DB_FILE" ]; then
    check_pass "可读 $DB_FILE — 当前 shell 能访问沙盒"
  else
    check_fail "shell 也读不到 — TCC 沙盒保护"
    check_warn "  路径: 系统设置 → 隐私与安全性 → 完全磁盘访问权限"
    check_warn "  给 'zsh' 或 '/bin/bash' 加权限(不是 chatlog),然后本脚本会读得到"
  fi
fi

# ─── 5. chatlog 自己能不能读 ───
echo
echo -e "${YELLOW}[5/5] chatlog FDA 授权测试${NC}"
if [ -n "$DB_FILE" ] && [ -f "$DB_FILE" ]; then
  # 让 chatlog 进程试着读
  RESULT=$($BIN key --help 2>&1 | head -1)
  if [ -n "$RESULT" ]; then
    check_pass "chatlog 自身可启动"
  fi
  check_warn "真正测试需要跑 try_extract_key.sh(那个脚本会判断)"
fi

# ─── 总结 ───
echo
echo -e "${CYAN}══════════════════════════════════════════════════${NC}"
if [ $FAIL -eq 0 ]; then
  echo -e "${GREEN}环境就位 — 下一步: ./script/try_extract_key.sh${NC}"
else
  echo -e "${RED}失败 $FAIL 项 — 修完后再跑${NC}"
fi
