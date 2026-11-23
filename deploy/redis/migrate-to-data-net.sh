#!/usr/bin/env bash
# ============================================================
# 把共享 Redis 迁到「新结构」：容器名 redis + 外部网络 data-net
#
# 背景:
#   short-url 改用共享 Redis 后,容器之间靠容器名 redis 解析
#   (REDIS_ADDR=redis:6379),这要求 redis 容器在 data-net 网络上。
#   老结构(shared-infra 项目 / shared-redis 容器)只在 shared-infra-net,
#   容器名对不上,业务容器会报:
#     lookup redis on 127.0.0.11:53: no such host
#     → 连接 Redis 失败 → 进程退出 → 容器 unhealthy
#
# 用法(在 ECS 上跑):
#   bash migrate-to-data-net.sh
#
# 幂等:已经迁好再跑一次不会重复创建。
# ============================================================
set -euo pipefail

cd "$(dirname "$0")"

echo "▶ 1/5 检查 data-net 网络"
if docker network inspect data-net >/dev/null 2>&1; then
  echo "  data-net 已存在"
else
  docker network create data-net
  echo "  data-net 已创建"
fi

echo "▶ 2/5 检查 .env"
if [ ! -f .env ]; then
  echo "  ✗ .env 不存在 —— 先 cp .env.example .env 并填好 REDIS_PASSWORD / ACR_*"
  exit 1
fi
set -a; source .env; set +a
echo "  REDIS_PASSWORD 已配置"
echo "  REDIS_HOST_PORT=${REDIS_HOST_PORT:-16385}"

echo "▶ 3/5 停掉老结构的 redis 容器"
# 老容器名可能是 redis / shared-redis,两种都清掉。
#
# 数据卷:老结构是 shared-infra_redis-data,新结构是 redis_redis-data。
# **卷不搬** —— 里面只是 Redis 自己的 AOF/RDB 骨架,业务数据全是缓存,
# 丢了会从 MySQL 重新预热,不影响正确性。真要保险就先备份:
#   docker run --rm -v shared-infra_redis-data:/src -v "$PWD":/backup \
#     alpine:3.21 tar czf /backup/redis-backup.tgz -C /src .
for c in redis shared-redis; do
  if docker ps -a --format '{{.Names}}' | grep -qx "$c"; then
    echo "  停掉并删除容器 $c"
    docker rm -f "$c"
  fi
done

echo "▶ 4/5 清掉 short-url 自带的独立 redis（切换前的遗留）"
# short-url-redis 是切换到共享实例之前的独立实例（standalone profile），
# 现在没人连它了。它 restart: unless-stopped，**光 stop 会被自动拉起来**，
# 必须 rm 掉。卷 short-url_redis-data 保留（回滚时还能用）。
if docker ps -a --format '{{.Names}}' | grep -qx short-url-redis; then
  echo "  删除容器 short-url-redis（保留卷 short-url_redis-data）"
  docker rm -f short-url-redis
else
  echo "  没有 short-url-redis，跳过"
fi

echo "▶ 5/5 起新结构的 redis"
docker compose up -d

echo
echo "▶ 验证"
echo "  容器:  $(docker ps --filter name=^redis$ --format '{{.Names}} {{.Status}} {{.Ports}}')"
echo "  网络:  $(docker inspect redis --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}')"

if docker inspect redis --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{"\n"}}{{end}}' | grep -qx data-net; then
  echo "  ✓ redis 在 data-net 上 —— 业务容器可以用 redis:6379 连"
else
  echo "  ✗ redis 不在 data-net 上,业务容器连不上"
  exit 1
fi

echo
echo "▶ 验收(在业务容器里 ping)"
docker exec redis redis-cli -a "${REDIS_PASSWORD}" ping
docker exec redis redis-cli -a "${REDIS_PASSWORD}" -n 3 DBSIZE
echo
echo "完成。short-url 用 REDIS_ADDR=redis:6379 REDIS_DB=3"
