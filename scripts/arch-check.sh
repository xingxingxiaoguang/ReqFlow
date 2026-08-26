#!/usr/bin/env bash
# 架构围栏：四层依赖方向白名单校验（make lint-arch）
# 铁律：业务层（app/port/domain）永远不知道 infra 的存在；
#       httpgin 只准调 app；domain 零内部依赖、零三方依赖。
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0
check() { # check <描述> <目录> <禁止的 import 正则>
  local desc="$1" dir="$2" pat="$3"
  if grep -rEn "\"reqflow/internal/($pat)\"" "$dir" --include='*.go' >/dev/null 2>&1; then
    echo "✗ 架构违规：$desc"
    grep -rEn "\"reqflow/internal/($pat)\"" "$dir" --include='*.go' | head -5
    fail=1
  else
    echo "✓ $desc"
  fi
}

# 业务层不得 import infra
check "app 不依赖 infra"              internal/app   'infra'
check "port 不依赖 infra/app"         internal/port  'infra|app'
# domain 零内部依赖
check "domain 不依赖任何内部包"       internal/domain '.*internal'
# 入站适配只准进业务用例层（不摸仓储/三方客户端/基建/端口/领域）
check "httpgin 只依赖 app" internal/infra/httpgin 'infra/(repository|llm|embedding|parser|config|database|crypto)|port|domain'
# 出站实现只准依赖 port/domain，不得反向触达业务用例
check "仓储不依赖 app"                internal/infra/repository 'app'
check "llm 不依赖 app"                internal/infra/llm        'app'
check "embedding 不依赖 app"          internal/infra/embedding  'app'
check "parser 不依赖 app"             internal/infra/parser     'app'
# 基建不感知业务
check "config 不依赖内部包"           internal/infra/config     '.*internal/(app|port|domain|infra/(repository|llm|embedding|parser|httpgin))'
check "database 不依赖内部包"         internal/infra/database   '.*internal/(app|port|domain|infra/(repository|llm|embedding|parser|httpgin))'
check "log 不依赖内部包"              internal/infra/log        '.*internal/(app|port|domain)'

# domain 零三方依赖（仅标准库）。以 `go list std` 全集做差集判定：
# Go 1.25 起标准库自带版本化内部包（如 crypto/internal/entropy/v1.0.0），
# 按路径含点判三方会误伤，差集是精确解。
dom_deps=$(go list -deps ./internal/domain/... | grep -v '^reqflow/internal/domain' | sort -u | comm -23 - <(go list std | sort -u) || true)
if [ -n "$dom_deps" ]; then
  echo "✗ 架构违规：domain 引入三方依赖：$dom_deps"
  fail=1
else
  echo "✓ domain 零三方依赖"
fi

exit $fail
