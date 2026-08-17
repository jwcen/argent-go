#!/usr/bin/env bash
# argent-go 一键打包部署：
#   前端构建 → go:embed 进 web/static → 交叉编译 linux/amd64 → scp → 重启 systemd
# 用法：
#   ./deploy.sh        完整构建并部署
#   ./deploy.sh -s     只重启服务（代码/前端没变时）
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
FRONTEND_DIR="${ARGENT_WEB:-$ROOT/../argent-web}"
BINARY=/tmp/argent-linux-amd64
ECS_HOST=115.29.192.164
ECS_BIN=/opt/argent-go/bin/argent
ECS_CMD="${ECS_CMD:-/Users/jcen/bin/ecs_cmd}"

RESTART_ONLY=0
[[ "${1:-}" == "-s" ]] && RESTART_ONLY=1

if [[ "$RESTART_ONLY" -eq 0 ]]; then
  echo "==> [1/4] 构建前端 ($FRONTEND_DIR)"
  ( cd "$FRONTEND_DIR" && npm install --no-audit --no-fund && npm run build )

  echo "==> [2/4] 嵌入前端到 web/static"
  rm -rf "$ROOT/web/static"
  mkdir -p "$ROOT/web/static"
  cp -r "$FRONTEND_DIR/dist/." "$ROOT/web/static/"

  echo "==> [3/4] 交叉编译 linux/amd64 (CGO_ENABLED=0)"
  ( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=local go build -ldflags="-s -w" -o "$BINARY" ./cmd/argent )
fi

echo "==> [4/4] scp 二进制到 ECS 并重启 argent-go"
# 先停服务再传：运行中二进制被占用，scp 会因 ETXTBSY 失败。
"$ECS_CMD" "systemctl stop argent-go"
scp "$BINARY" "root@$ECS_HOST:$ECS_BIN"
"$ECS_CMD" "systemctl start argent-go && sleep 2 && systemctl is-active argent-go"

echo "==> 部署完成：http://$ECS_HOST:8889/"
