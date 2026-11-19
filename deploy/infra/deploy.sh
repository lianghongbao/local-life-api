#!/usr/bin/env bash
# ============================================================
# 共享底座（MySQL + Redis）部署脚本
# ============================================================
# local-life-api 和 chatroom 两个仓库各存一份内容相同的本脚本。
# 底座只起一套 —— compose 项目名固定 shared-infra，两边跑的是同一个 project。
#
#   ./deploy.sh up       首次部署 / 改配置后重启
#   ./deploy.sh status   看状态
#   ./deploy.sh init-db  手动补执行建库建表脚本（底座已存在时用）
#   ./deploy.sh down     停掉（保留数据卷）
#
# set -euo pipefail：任何一步失败就停，别带着半成品往下跑。

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

# 共享网络名 —— 业务应用 compose 也接这个
NETWORK=shared-infra-net
# compose 项目名 —— 固定不变，两仓库各一份文件才不会起出两套容器
PROJECT=shared-infra

load_env() {
	if [ ! -f .env ]; then
		echo "❌ 缺少 .env —— 先 cp .env.example .env 并填好凭证" >&2
		exit 1
	fi
	set -a
	# shellcheck disable=SC1091
	source .env
	set +a
}

ensure_network() {
	# external: true 的网络得先存在。幂等 —— 已存在就跳过。
	if ! docker network inspect "$NETWORK" >/dev/null 2>&1; then
		echo "▶ 创建共享网络 $NETWORK"
		docker network create "$NETWORK" >/dev/null
	else
		echo "✓ 共享网络 $NETWORK 已存在"
	fi
}

wait_healthy() {
	local container="$1"
	echo "⏳ 等待 $container 健康..."
	for _ in $(seq 1 30); do
		# 容器不在（还在起）时 health status 会是空字符串，不算成功
		local status
		status=$(docker inspect -f '{{.State.Health.Status}}' "$container" 2>/dev/null || echo "starting")
		if [ "$status" = "healthy" ]; then
			echo "✅ $container 健康"
			return 0
		fi
		sleep 2
	done
	echo "⚠️ $container 30 次未通过健康检查，请 docker logs $container 排查" >&2
	return 1
}

cmd_up() {
	load_env
	ensure_network
	docker compose -p "$PROJECT" up -d
	wait_healthy shared-mysql
	wait_healthy shared-redis
	echo ""
	echo "✅ 共享底座已就绪"
	echo "   mysql  shared-mysql:3306   库: local_life / chatroom"
	echo "   redis  shared-redis:6379"
	echo ""
	echo "各应用连共享底座用这两个容器名（同在 $NETWORK 网络里）："
	echo "   DB_DATASOURCE=root:<pwd>@tcp(shared-mysql:3306)/<库名>?..."
	echo "   REDIS_HOST=shared-redis:6379"
}

cmd_init_db() {
	# 底座已经跑过的时候，initdb.d 里的脚本不会再执行。
	# 改了 init/ 下的 SQL 之后用这个补一次。
	load_env
	echo "▶ 补执行 init/ 下的建库建表脚本"
	for f in init/*.sql; do
		echo "  - $f"
		docker exec -i shared-mysql \
			mysql -uroot -p"${MYSQL_ROOT_PASSWORD}" <"$f"
	done
	echo "✅ init/ 下的脚本已执行"
}

cmd_status() {
	docker compose -p "$PROJECT" ps
}

cmd_down() {
	docker compose -p "$PROJECT" stop
	echo "✅ 已停止（数据卷保留）"
}

case "${1:-up}" in
up)
	cmd_up
	;;
status)
	cmd_status
	;;
init-db)
	cmd_init_db
	;;
down)
	cmd_down
	;;
*)
	echo "用法: $0 {up|status|init-db|down}" >&2
	exit 2
	;;
esac
