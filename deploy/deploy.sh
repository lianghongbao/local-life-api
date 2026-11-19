#!/usr/bin/env bash
# ============================================================
# 部署脚本
# ============================================================
# 起 MySQL / Redis / 业务容器。三个 compose 各管各的，靠 data-net 网络互通。
#
#   ./deploy.sh up       首次部署 / 改配置后应用
#   ./deploy.sh status   看状态
#   ./deploy.sh down     全停（数据卷保留）
#   ./deploy.sh logs     跟日志
#
# set -euo pipefail：任何一步失败就停，别带着半成品往下跑。

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

NETWORK=data-net

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

# 外部网络不会自动创建，多个项目的容器要接同一张网络才能互相访问。
ensure_network() {
	if docker network inspect "${NETWORK}" >/dev/null 2>&1; then
		echo "✓ 网络 ${NETWORK} 已存在"
	else
		echo "▶ 创建网络 ${NETWORK}"
		docker network create "${NETWORK}" >/dev/null
	fi
}

wait_healthy() {
	local container="$1"
	echo "⏳ 等待 ${container} 健康..."
	for _ in $(seq 1 30); do
		local status
		status=$(docker inspect -f '{{.State.Health.Status}}' "${container}" 2>/dev/null || echo "starting")
		if [ "${status}" = "healthy" ]; then
			echo "✅ ${container} 健康"
			return 0
		fi
		sleep 2
	done
	echo "⚠️ ${container} 30 次未通过健康检查 —— docker logs ${container} 排查" >&2
	return 1
}

cmd_up() {
	load_env
	ensure_network

	# MySQL —— 先起，业务容器要等它
	if [ ! -f mysql/.env ]; then
		echo "❌ 缺少 mysql/.env —— 先 cp mysql/.env.example mysql/.env 并填好凭证" >&2
		exit 1
	fi
	echo "▶ 启动 MySQL..."
	(cd mysql && docker compose up -d)
	wait_healthy mysql

	# Redis
	if [ ! -f redis/.env ]; then
		echo "❌ 缺少 redis/.env —— 先 cp redis/.env.example redis/.env 并填好凭证" >&2
		exit 1
	fi
	echo "▶ 启动 Redis..."
	(cd redis && docker compose up -d)
	wait_healthy redis

	# 业务容器
	echo "▶ 启动业务容器..."
	docker compose up -d

	echo ""
	echo "✅ 部署完成"
	docker compose ps
}

cmd_status() {
	echo "── 业务容器 ──"
	docker compose ps
	echo ""
	echo "── MySQL ──"
	(cd mysql && docker compose ps)
	echo ""
	echo "── Redis ──"
	(cd redis && docker compose ps)
}

cmd_down() {
	docker compose down
	(cd mysql && docker compose down)
	(cd redis && docker compose down)
	echo "✅ 已停止（数据卷保留）"
}

cmd_logs() {
	docker compose logs -f "${@:-}"
}

case "${1:-up}" in
up)
	cmd_up
	;;
status)
	cmd_status
	;;
down)
	cmd_down
	;;
logs)
	shift
	cmd_logs "$@"
	;;
*)
	echo "用法: $0 {up|status|down|logs [服务名]}" >&2
	exit 2
	;;
esac
