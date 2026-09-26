#!/bin/bash
# chatlog 密钥提取 — 试跑 + 智能诊断
# 前提: precheck.sh 全过
# 用法: ./script/try_extract_key.sh   （可用 CHATLOG_BIN 指定二进制路径）

set -e

# 二进制默认取仓库内 bin/chatlog，可用 CHATLOG_BIN 覆盖（已装到 PATH 也行）
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${CHATLOG_BIN:-$REPO_ROOT/bin/chatlog}"


CYAN='\033[0;36m'
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

WX_PID=$(pgrep -f "/Applications/WeChat.app/Contents/MacOS/WeChat$" | head -1)

if [ -z "$WX_PID" ]; then
  echo -e "${RED}✗ 微信未运行 — 先启动微信并登录${NC}"
  exit 1
fi

echo -e "${CYAN}═══ 尝试提取密钥 (PID=$WX_PID) ═══${NC}"
echo -e "${YELLOW}预计 20-40 秒,微信会卡顿,请耐心等待...${NC}\n"

# 抓所有输出(包括 stderr)
OUT_FILE=$(mktemp)
$BIN key -p "$WX_PID" --debug > "$OUT_FILE" 2>&1 || true
OUTPUT=$(cat "$OUT_FILE")

echo "── 原始输出 ──"
echo "$OUTPUT" | tail -40
echo "── 结束 ──"

echo
echo -e "${CYAN}═══ 智能诊断 ═══${NC}\n"

# 情况 1:拿到 key
if echo "$OUTPUT" | grep -q "Data Key: \["; then
  KEY=$(echo "$OUTPUT" | grep "Data Key:" | sed -E 's/.*Data Key: \[([a-f0-9]+)\].*/\1/')
  IMGKEY=$(echo "$OUTPUT" | grep "Image Key:" | sed -E 's/.*Image Key: \[([a-f0-9]+)\].*/\1/')
  echo -e "${GREEN}🎉 拿到密钥!${NC}"
  echo
  echo -e "${GREEN}  Data Key:  $KEY${NC}"
  echo -e "${GREEN}  Image Key: $IMGKEY${NC}"
  echo
  echo -e "${BLUE}保存到临时文件:${NC}"
  echo "$KEY" > /tmp/chatlog-data-key
  echo "$IMGKEY" > /tmp/chatlog-img-key
  echo -e "  /tmp/chatlog-data-key: $KEY"
  echo -e "  /tmp/chatlog-img-key: $IMGKEY"
  echo
  echo -e "${BLUE}下一步 — 解密数据库:${NC}"
  echo "  WXID_DIR=\$(find ~/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files -maxdepth 1 -mindepth 1 -type d | head -1)"
  echo "  DATA_DIR=\"\$WXID_DIR/db_storage\""
  echo "  WORK_DIR=~/.chatlog/work/decrypted"
  echo "  \$BIN decrypt -d \"\$DATA_DIR\" -k $KEY -i $IMGKEY -p darwin -v 4 -w \"\$WORK_DIR\""
  echo
  echo -e "${BLUE}或者直接启动 HTTP+MCP 服务:${NC}"
  echo "  \$BIN server -a 127.0.0.1:5030 -d \"\$DATA_DIR\" -k $KEY -i $IMGKEY -p darwin -v 4 --auto-decrypt"
  rm -f "$OUT_FILE"
  exit 0
fi

# 情况 2:SIP 仍启用
if echo "$OUTPUT" | grep -qi "ErrSIPEnabled\|SIP"; then
  echo -e "${RED}✗ SIP 仍启用${NC}"
  echo "  重新进 Recovery: csrutil disable → 重启"
fi

# 情况 3:沙盒文件读不到
if echo "$OUTPUT" | grep -qi "operation not permitted\|permission denied"; then
  echo -e "${RED}✗ chatlog 仍读不到沙盒文件${NC}"
  echo
  echo -e "${YELLOW}两条路:${NC}"
  echo
  echo "A. 授 Full Disk Access:"
  echo "   系统设置 → 隐私与安全性 → 完全磁盘访问权限"
  echo "   点 + → Cmd+Shift+. → 加 $BIN"
  echo
  echo "B. (FDA 在 macOS 27 可能因 chatlog 无签名被拒) 直接复制 db:"
  echo "   WXID_DIR=\$(find ~/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files -maxdepth 1 -mindepth 1 -type d | head -1)"
  echo "   mkdir -p ~/.chatlog/work/data"
  echo "   cp -R \"\$WXID_DIR/db_storage\" ~/.chatlog/work/data/"
  echo "   cp -R \"\$WXID_DIR/contact\" ~/.chatlog/work/data/ 2>/dev/null || true"
  echo "   然后把上面 decrypt 命令的 -d 换成 ~/.chatlog/work/data"
fi

# 情况 4:pattern / validator 不匹配
if echo "$OUTPUT" | grep -qi "pattern\|validator\|valid key\|no.*key.*found"; then
  echo -e "${YELLOW}⚠️  4.1.34 pattern 不适配 (chatlog 上游还没适配这个版本)${NC}"
  echo
  echo "这是上游问题,chatlog V4 extractor 用硬编码 pattern:"
  echo "  https://github.com/lululu811/wechat-chatlog/blob/main/internal/wechat/key/darwin/v4.go"
  echo
  echo "路径 C — 自己分析新 pattern:"
  echo "  1. dump 微信进程内存: lldb -p $WX_PID -o 'process save-core /tmp/wx-core' -o 'quit'"
  echo "  2. grep 32 字节 hex key 候选: python3 脚本搜索 + chatlog decrypt.Validator 验证"
  echo "  3. 把新 pattern 提到 chatlog issue #131 / 直接 PR"
fi

# 兜底
echo
echo -e "${BLUE}把上面整段输出粘给 Mavis,我给你下一步${NC}"
rm -f "$OUT_FILE"
