#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || .)"

scan() {
  printf 'fixture:1:%s\n' "$1" | awk -f scripts/secret-check.awk
}

expect_hit() {
  local name="$1" content="$2"
  if [ -z "$(scan "$content")" ]; then
    echo "✗ secret scanner 应命中：${name}" >&2
    exit 1
  fi
}

expect_clean() {
  local name="$1" content="$2"
  if [ -n "$(scan "$content")" ]; then
    echo "✗ secret scanner 误报：${name}" >&2
    exit 1
  fi
}

expect_hit "字段密钥" "api_key = sk-test-abc""defghijk""lmnop"
expect_hit "非占位 DSN" "dsn = postgres://reqflow:not-a-real-""secret@localhost/db"
expect_clean "环境变量名" "api_key = REQFLOW_LLM_API_KEY"
expect_clean "短值" "token = short"
expect_clean "本地同名 DSN" "dsn = postgres://reqflow:reqflow@localhost/db"

echo "✓ 密钥扫描器规则测试通过"
