#!/usr/bin/env bash
# ReqFlow v1 一键启动：依赖容器 → 等待就绪 → 构建应用 → 运行
# 前置：Docker、Go、pnpm；Linux 上 OpenSearch 需要 vm.max_map_count>=262144
# （sudo sysctl -w vm.max_map_count=262144）
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> [1/4] 启动依赖容器（PostgreSQL + OpenSearch）"
if command -v docker >/dev/null 2>&1; then
  docker compose up -d
else
  echo "错误：未检测到 docker，请先安装 Docker" >&2
  exit 1
fi

echo "==> [2/4] 等待依赖就绪"
for i in $(seq 1 60); do
  docker exec reqflow-pg pg_isready -U reqflow -d reqflow >/dev/null 2>&1 && break
  [ "$i" = 60 ] && { echo "错误：PostgreSQL 未就绪" >&2; exit 1; }
  sleep 1
done
for i in $(seq 1 120); do
  curl -sf http://127.0.0.1:9200/_cluster/health >/dev/null 2>&1 && break
  [ "$i" = 120 ] && { echo "错误：OpenSearch 未就绪" >&2; exit 1; }
  sleep 1
done
echo "依赖就绪。"

echo "==> [3/4] 构建应用（前端 + 嵌入式后端二进制）"
make build

echo "==> [4/4] 启动 ReqFlow :8080"
if [ ! -f config.yaml ]; then
  echo "提示：未检测到 config.yaml，启动时会自动生成默认配置。"
fi
if [ -f config.yaml ] && grep -q 'api_key: ""' config.yaml; then
  echo "提醒：config.yaml 中 llm.api_key / embedding.api_key 尚未填写，"
  echo "      文件抽取与语义检索需要这两个 Key（编辑 config.yaml 后重启生效）。"
fi
exec ./bin/reqflow
