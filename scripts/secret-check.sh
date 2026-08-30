#!/usr/bin/env bash
# 密钥泄漏护栏扫描器（pre-commit 与 make check-secrets 共用）。
#
# 两类检测模式：
#   R1 敏感字段被赋了非空真实值：api_key / api_token / client_secret /
#      encryption_key / password / token 等，值长度 ≥12 且不是占位符
#   R2 带内嵌密码的连接串：postgres://user:pass@… / redis://:pass@…
#
# 白名单（不算泄漏）：
#   - 空值（"" / ''）与注释行
#   - 全大写下划线环境变量名（REQFLOW_LLM_API_KEY 等文档引用）
#   - ${VAR} 引用、your-xxx / <xxx> / changeme 类占位
#
# 用法：
#   secret-check.sh files <路径…>   # 扫描文件当前内容
#   secret-check.sh staged          # 扫描 git 暂存区新增行
#   secret-check.sh tracked         # 扫描 git 追踪的全部文本文件
# 输出：命中项逐行打印到 stderr；退出码 0=干净 / 1=疑似泄漏
set -uo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || .)"

MODE="${1:-}"; shift || true

# 主判定流：单个 awk 进程扫描 stdin 的「file:line:content」，避免逐行派生子进程。
judge() {
  awk -f scripts/secret-check.awk
}

FOUND=""

case "$MODE" in
  files)
    SCAN_FILES=()
    for f in "$@"; do
      [ -f "$f" ] || continue
      SCAN_FILES+=("$f")
    done
    if [ "${#SCAN_FILES[@]}" -gt 0 ]; then
      FOUND+="$(grep -nIH '' -- "${SCAN_FILES[@]}" 2>/dev/null | judge)"
    fi
    ;;
  staged)
    FOUND+="$(git diff --cached --no-color --unified=0 | awk '
      /^\+\+\+ b\// { f = substr($0, 7) }
      /^@@/ { match($0, /\+[0-9]+/); ln = substr($0, RSTART + 1, RLENGTH - 1) - 1 }
      /^\+/ && ! /^\+\+\+/ {
        ln++
        line = substr($0, 2)
        if (line !~ /^[[:space:]]*(#|\/\/|\*)/ && line ~ /[A-Za-z0-9]/) {
          printf "%s:%d:%s\n", f, ln, line
        }
      }
    ' | judge)"
    ;;
  tracked)
    # 与 staged 模式语义对齐：注释行（# / // / * 开头）不判泄漏，
    # 否则会扫到本脚本与文档中自身的示例连接串（护栏自伤）
    FOUND+="$(git grep -n -I -e '' -- . \
      ':(exclude)*.lock' ':(exclude)*.sum' ':(exclude)*.svg' \
      ':(exclude)*.png' ':(exclude)*.ico' ':(exclude)*.woff' \
      ':(exclude)*.woff2' ':(exclude)pnpm-lock.yaml' \
      ':(exclude)cmd/reqflow/dist/**' | awk '
      {
        content = $0
        sub(/^[^:]+:[0-9]+:/, "", content)
        stripped = content
        sub(/^[[:space:]]+/, "", stripped)
        if (stripped ~ /^(#|\/\/|\*)/) next
        print
      }
    ' | judge)"
    ;;
  *)
    echo "用法: $0 {files <路径…> | staged | tracked}" >&2
    exit 2
    ;;
esac

if [ -n "$FOUND" ]; then
  echo "✗ 疑似密钥泄漏：" >&2
  echo "$FOUND" >&2
  echo "" >&2
  echo "⛔ 已阻止。真实密钥只放 config.yaml（已 gitignore）或 REQFLOW_* 环境变量；" >&2
  echo "   config.example.yaml 只允许空值占位。确认误报可用 git commit --no-verify。" >&2
  exit 1
fi
echo "✓ 密钥扫描通过（${MODE}）"
exit 0
