#!/usr/bin/env bash
# 监控远端仓库，有更新就自动拉取。用法：./scripts/watch.sh [间隔秒数，默认 30]
cd "$(dirname "$0")/.." || exit 1
interval=${1:-30}
echo "监控 $(git rev-parse --abbrev-ref HEAD) 分支，每 ${interval} 秒检查一次，Ctrl+C 退出"
while true; do
  if git fetch -q && [ "$(git rev-parse HEAD)" != "$(git rev-parse @{u})" ]; then
    echo "[$(date +%H:%M:%S)] 发现更新："
    git log --oneline HEAD..@{u}
    # 只做快进合并：本地有冲突的改动时不会覆盖，只提示
    git pull -q --ff-only && echo "已拉取，重新运行项目即可生效" || echo "拉取失败，请先处理本地改动"
  fi
  sleep "$interval"
done
