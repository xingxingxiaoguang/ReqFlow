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

FIELD_RE='(^|[^a-zA-Z0-9])(api[_-]?key|api[_-]?token|client[_-]?secret|encryption[_-]?key|secret|passwo(r)?d|token)[[:space:]]*[:=]'
VALUE_RE='[:=][[:space:]]*["'"'"']?[A-Za-z0-9+/=_.:@~-]{12,}'
WHITELIST_RE='(REQFLOW|SILICONFLOW|MINERU|PINGCODE)_[A-Z_]+|[A-Z][A-Z_]{11,}|\$\{[A-Z_]+\}|your[-_]|changeme|placeholder|<[^>]+>|^([a-zA-Z][a-zA-Z0-9]*\.)+[a-zA-Z][a-zA-Z0-9]*$|^&?[a-z][a-zA-Z0-9]*$'
DSN_RE='(postgres(ql)?|mysql|redis|mongodb(\+srv)?)://[^/@[:space:]"'"'"']+:[^/@[:space:]"'"'"']+@'

# 提取「值」部分用于白名单精确判断（去掉字段名前缀与引号逗号）
extract_value() {
  sed -E 's/^.*[[:space:]"'"'"'(]?(api[_-]?key|api[_-]?token|client[_-]?secret|encryption[_-]?key|secret|passwo(r)?d|token)[[:space:]]*[:=][[:space:]]*//I' \
    | tr -d '"' | tr -d "'" | awk '{print $1}' | sed -E 's/[,#].*$//'
}

# 单行判定：疑似泄漏返回 0
hit() {
  local line="$1" val dsn user pass
  # R2: 带密码 DSN；用户名==密码（reqflow:reqflow 等本地开发占位）放行
  if dsn=$(echo "$line" | grep -oE "$DSN_RE" | head -1); [ -n "$dsn" ]; then
    user=$(echo "$dsn" | sed -E 's#^[a-z+]+://([^:@/[:space:]]+):.*#\1#')
    pass=$(echo "$dsn" | sed -E 's#^[a-z+]+://[^:@/[:space:]]+:([^@/[:space:]]+)@.*#\1#')
    [ "$user" = "$pass" ] && return 1
    return 0
  fi
  echo "$line" | grep -Eqi "$FIELD_RE" || return 1
  echo "$line" | grep -Eq "$VALUE_RE" || return 1
  val=$(echo "$line" | extract_value)
  [ -n "$val" ] && echo "$val" | grep -Eq "$WHITELIST_RE" && return 1
  return 0
}

# 主判定流：stdin 逐行「file:line:content」，命中打印到 stdout
judge() {
  local f ln content
  while IFS= read -r entry; do
    [ -z "$entry" ] && continue
    f="${entry%%:*}"; rest="${entry#*:}"
    ln="${rest%%:*}"; content="${rest#*:}"
    if hit "$content"; then
      echo "$f:$ln"
      echo "  ${content:0:120}"
    fi
  done
}

FOUND=""

case "$MODE" in
  files)
    for f in "$@"; do
      [ -f "$f" ] || continue
      FOUND+="$(grep -nH '' "$f" 2>/dev/null | judge)"
    done
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
    # 注意：macOS bash 3.2 在 $() 内嵌套 case 的 ) 解析有怪癖，用 awk 过滤替代
    FOUND+="$(git ls-files | awk '
      /\.lock$|\.sum$|\.svg$|\.png$|\.ico$|\.woff2?$|pnpm-lock\.yaml|^cmd\/reqflow\/dist\// { next }
      { print }
    ' | while IFS= read -r f; do
      [ -f "$f" ] || continue
      file "$f" 2>/dev/null | grep -q text || continue
      grep -nH '' "$f" 2>/dev/null
    done | judge)"
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
