#!/usr/bin/env bash
set -euo pipefail

echo "=== Spec 指挥台 - 环境初始化 ==="
echo ""

# ── git ──
if ! command -v git &>/dev/null; then
  echo "[FAIL] git 未安装，请先安装 git"
  exit 1
fi
echo "[OK] git: $(git --version)"

# ── node ──
if command -v node &>/dev/null; then
  echo "[OK] node: $(node --version)"
fi

# ── gstack（工作流必备：审查 / QA / 安全等）──
echo ""
if command -v gstack &>/dev/null; then
  echo "[OK] gstack: 已安装"
else
  echo "[..] 安装 gstack..."
  curl -fsSL https://gstack.run/install | bash
  echo "[OK] gstack 安装完成"
fi

# ── spec 模板 ──
echo ""
if [ -f "specs/_template/spec.md" ]; then
  echo "[OK] spec 模板就绪"
else
  echo "[WARN] spec 模板缺失"
fi

# ── 命令检查 ──
cmd_count=$(find .claude/commands -name "*.md" 2>/dev/null | wc -l | tr -d ' ')
echo "[OK] speckit 命令: ${cmd_count} 个"

echo ""
echo "=== 初始化完成 ==="
echo "gstack + speckit 已就绪。"
