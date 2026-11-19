#!/usr/bin/env bash
# ============================================================
# 从「自带 mysql/redis 的旧 compose」迁移到「共享底座」
# ============================================================
# 场景:ECS 上正跑着旧版 deploy/docker-compose.yml(自带 local-life-mysql /
#       local-life-redis),现在要切到共享底座(deploy/infra/)。
#
# 本脚本做两件事:
#   1. 起共享底座(shared-mysql / shared-redis),建好 local_life 库和表
#   2. 把旧 MySQL 里的 local_life 数据搬到共享底座
#
# Redis 不迁移 —— 缓存丢了会自己重建,限流桶之类的短期状态重置也无所谓。
#
# 用法(在 ECS 上,仓库根目录):
#   cd deploy && ./migrate-to-shared-base.sh
#
# set -euo pipefail:任何一步失败就停,别带着半成品往下跑。

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

OLD_MYSQL_CONTAINER=local-life-mysql
NEW_MYSQL_CONTAINER=shared-mysql
DB_NAME=local_life

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

# 载入凭证。必须在任何用到 MYSQL_ROOT_PASSWORD 的地方之前调用。
load_env

# 导数据用的 mysql 客户端镜像。跟共享底座用同一个版本。
# 从 ACR 拉:${ACR_REGISTRY_HOST}/${ACR_NAMESPACE}:mysql-8.0
# ACR 变量没配时回落到 Docker Hub 官方镜像。
# 导数据用的 mysql 客户端镜像。
#
# 不从 ACR 变量拼 —— 拼出来的名字未必在本机存在(比如 ACR 里只推了
# 应用镜像,没推 mysql),拉取会失败。直接用**已经在跑的** shared-mysql
# 容器的镜像 —— 保证存在,版本还跟目标库完全一致。
resolve_mysql_image() {
	if [ -n "${MYSQL_IMAGE:-}" ]; then
		echo "${MYSQL_IMAGE}"
		return
	fi
	local img
	img=$(docker inspect "${NEW_MYSQL_CONTAINER}" --format '{{.Config.Image}}' 2>/dev/null || true)
	if [ -n "${img}" ]; then
		echo "${img}"
	else
		echo "mysql:8.0"
	fi
}

# ============================================================
# 0. 停掉旧容器(腾出端口)
# ============================================================
# 旧 compose 的 local-life-mysql 绑 127.0.0.1:3306,
# 共享底座的 shared-mysql 也要绑同一个端口 —— 两个不能同时跑。
# 所以先停旧的(只是 stop,容器和数据卷都留着,数据还要用)。
#
# 注意:这里**不删**旧容器和卷 —— 迁移完还要拿旧库做校验,
# 确认没问题之后再手工删。
stop_old() {
	local name="$1"
	if docker ps --format '{{.Names}}' | grep -qx "${name}"; then
		echo "  停止 ${name}(容器和数据卷保留,数据还要用来迁移)"
		docker stop "${name}" >/dev/null
	elif docker ps -a --format '{{.Names}}' | grep -qx "${name}"; then
		echo "  ${name} 已停止"
	else
		echo "  ${name} 不存在"
	fi
}

echo "▶ 停掉旧的 mysql / redis(腾出 3306 / 6379 端口)"
stop_old "${OLD_MYSQL_CONTAINER}"
stop_old "local-life-redis"

# 端口还被别的东西占着(比如宿主上原生跑的 redis/mysql,不是容器)——
# 共享底座就起不来。提前查出来,给明确提示,别等 compose 报一堆
# "address already in use" 看不出是哪来的。
#
# 共享底座自己的容器不算冲突 —— 底座已经在跑的时候,端口就是它的。
check_port_free() {
	local port="$1"
	local who
	who=$(docker ps --format '{{.Names}} {{.Ports}}' \
		| grep -E "(127\.0\.0\.1|0\.0\.0\.0|\[::\]):${port}->" \
		| awk '{print $1}' \
		| grep -vxE "shared-mysql|shared-redis" \
		| tr '\n' ' ')
	if [ -n "${who}" ]; then
		echo "❌ 宿主端口 ${port} 被容器占用:${who}"
		echo "   先停掉它:docker stop ${who}"
		return 1
	fi
	return 0
}
check_port_free 3306 || exit 1
check_port_free 6379 || exit 1

# ============================================================
# 1. 起共享底座
# ============================================================
echo ""
echo "▶ 启动共享底座(shared-mysql / shared-redis)..."
if [ ! -d infra ]; then
	echo "❌ 找不到 infra/ 目录 —— 确认当前是 deploy/ 且仓库是最新的" >&2
	exit 1
fi

cd infra
# 底座的 .env 要跟应用的 .env 用同一套凭证。
# 应用 .env 里已经有 MYSQL_ROOT_PASSWORD / REDIS_PASSWORD,
# 这里直接复用,避免两处填不一致。
if [ ! -f .env ]; then
	echo "▶ 生成 infra/.env(复用应用的凭证)..."
	cat > .env <<EOF
MYSQL_ROOT_PASSWORD=${MYSQL_ROOT_PASSWORD}
REDIS_PASSWORD=${REDIS_PASSWORD}
ACR_REGISTRY_HOST=${ACR_REGISTRY_HOST:-}
ACR_NAMESPACE=${ACR_NAMESPACE:-}
EOF
	chmod 600 .env
	echo "✅ infra/.env 已生成"
else
	echo "✓ infra/.env 已存在,不覆盖"
fi

./deploy.sh up
cd ..

MYSQL_IMAGE=$(resolve_mysql_image)
echo "  导数据用镜像:${MYSQL_IMAGE}"

# ============================================================
# 2. 迁移 MySQL 数据
# ============================================================
echo ""
echo "▶ 迁移 MySQL 数据: ${OLD_MYSQL_CONTAINER} → ${NEW_MYSQL_CONTAINER}"

# 旧容器已经被停掉了(端口让给共享底座)。
# docker exec 对停止的容器不рабо,所以起一个临时容器挂旧数据卷来导数据,
# 导完就删 —— 旧容器本身不动,留着做校验和回滚。
if ! docker ps -a --format '{{.Names}}' | grep -qx "${OLD_MYSQL_CONTAINER}"; then
	echo "❌ 找不到旧容器 ${OLD_MYSQL_CONTAINER}"
	echo "   如果旧 MySQL 已经删了,数据没了 —— 跳过迁移即可。"
	exit 1
fi

# 旧 compose 的数据卷名:<项目名>_mysql-data。旧 compose 没有 name: 字段,
# 项目名取目录名 —— 旧版 compose 在 deploy/ 目录下,所以是 deploy_mysql-data。
OLD_VOLUME=$(docker inspect "${OLD_MYSQL_CONTAINER}" \
	--format '{{range .Mounts}}{{if eq .Destination "/var/lib/mysql"}}{{.Name}}{{end}}{{end}}')

if [ -z "${OLD_VOLUME}" ]; then
	echo "❌ 找不到旧 MySQL 的数据卷 —— 容器 ${OLD_MYSQL_CONTAINER} 没挂 /var/lib/mysql"
	exit 1
fi
echo "  旧数据卷:${OLD_VOLUME}"

# 旧库和共享底座的目标库都叫 local_life。
# 用 mysqldump 只导数据不导表结构 —— 共享底座的 init 脚本已经建好表了
# (含最新的 FULLTEXT 索引),导表结构会覆盖掉它。
DUMP_FILE="/tmp/migrate-local_life-$$.sql"

# 旧容器停着,数据卷读不了(socket 连不上 —— 卷里只有数据文件,没有跑着的 mysqld)。
# 所以起一个**临时** MySQL 容器挂旧卷(可写 —— InnoDB 需要写 redo log 才能启动),
# 等它健康,导出数据,再删掉临时容器。
#
# 用可写挂载:InnoDB 启动时会写 redo/ib_logfile,只读挂载会起不来。
TEMP_OLD_CONTAINER="tmp-old-mysql-$$"

echo "  起临时容器读旧数据卷(${TEMP_OLD_CONTAINER})..."
docker rm -f "${TEMP_OLD_CONTAINER}" >/dev/null 2>&1 || true
docker run -d \
	--name "${TEMP_OLD_CONTAINER}" \
	-v "${OLD_VOLUME}:/var/lib/mysql" \
	-e MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD}" \
	"${MYSQL_IMAGE}" \
	--character-set-server=utf8mb4 \
	--collation-server=utf8mb4_unicode_ci \
	> /dev/null

# 等临时 MySQL 起来
TMP_READY=0
for i in $(seq 1 60); do
	if docker exec "${TEMP_OLD_CONTAINER}" \
		mysqladmin ping -h localhost -uroot -p"${MYSQL_ROOT_PASSWORD}" >/dev/null 2>&1; then
		TMP_READY=1
		break
	fi
	sleep 2
done

if [ "${TMP_READY}" != "1" ]; then
	echo "❌ 临时 MySQL 起不来 —— 旧数据卷可能损坏"
	echo "   排查:docker logs ${TEMP_OLD_CONTAINER}"
	docker rm -f "${TEMP_OLD_CONTAINER}" >/dev/null 2>&1 || true
	exit 1
fi
echo "  临时 MySQL 已就绪"

echo "  导出旧库数据..."
docker exec "${TEMP_OLD_CONTAINER}" \
	mysqldump -uroot -p"${MYSQL_ROOT_PASSWORD}" \
	--no-create-info \
	--skip-triggers \
	--single-transaction \
	--default-character-set=utf8mb4 \
	--set-gtid-purged=OFF \
	"${DB_NAME}" > "${DUMP_FILE}"

LINES=$(wc -l < "${DUMP_FILE}")
echo "  导出完成:${LINES} 行 → ${DUMP_FILE}"

# 旧库的校验行数也顺便取出来(临时容器还在,直接读)
# 密码和库名要用 -e 传进容器 —— sh -c 里的 $MYSQL_ROOT_PASSWORD 是容器内的,
# 宿主的变量不会自动进去。不传的话 mysql 连不上,每张表都返回 ERR。
OLD_COUNTS=$(docker exec -e MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD}" -e DB_NAME="${DB_NAME}" \
	"${TEMP_OLD_CONTAINER}" sh -c \
	'for T in "$@"; do printf "%s=" "$T"; mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -N -B -e "SELECT COUNT(*) FROM \`$T\`;" "$DB_NAME" 2>/dev/null || echo ERR; done' \
	sh user post_info shop notice banner admin comment comment_like comment_report admin_operation_log 2>/dev/null)

# 临时容器用完就删
docker rm -f "${TEMP_OLD_CONTAINER}" >/dev/null 2>&1 || true

if [ "${LINES}" -lt 3 ]; then
	echo "  ⚠️ 导出内容为空 —— 旧库可能本来就是空的"
fi

# 把 INSERT INTO 改成 REPLACE INTO。
#
# 为什么:共享底座的 init 脚本已经往 admin 表插了默认管理员(id=1),
# 裸 INSERT 会撞主键(1062 Duplicate entry)然后整个导入中断。
# REPLACE 是"有就覆盖,没有就插" —— 幂等,而且能用旧库里的真实
# admin 行(含可能改过的密码)覆盖掉默认行。
sed -i 's/^INSERT INTO /REPLACE INTO /' "${DUMP_FILE}"

# 先清空目标表,再导。
#
# 为什么:共享底座的 init 脚本建表时插了默认管理员(id=1),而且如果
# 之前跑过一遍迁移,库里可能还留着上一次的残留数据。清空后导入,
# 结果跟旧库**完全一致** —— 行数校验才是可信的。
# 共享底座的库是刚建的,清空没有风险。
echo "  清空目标表..."
docker exec -i "${NEW_MYSQL_CONTAINER}" 	mysql -uroot -p"${MYSQL_ROOT_PASSWORD}" "${DB_NAME}" <<'TRUNCATE_SQL' 2>&1 | grep -v "^mysql: \[Warning\]" || true
SET FOREIGN_KEY_CHECKS=0;
TRUNCATE TABLE `admin_operation_log`;
TRUNCATE TABLE `comment_report`;
TRUNCATE TABLE `comment_like`;
TRUNCATE TABLE `comment`;
TRUNCATE TABLE `banner`;
TRUNCATE TABLE `notice`;
TRUNCATE TABLE `shop`;
TRUNCATE TABLE `post_info`;
TRUNCATE TABLE `user`;
TRUNCATE TABLE `admin`;
SET FOREIGN_KEY_CHECKS=1;
TRUNCATE_SQL

echo "  导入共享底座..."
# --force:单条 SQL 出错不中断整批。REPLACE 已经处理了主键冲突,
# --force 是兜底 —— 万一某个字段类型不兼容,至少其他表的数据能导进去,
# 最后由下面的行数校验告诉你哪张表没对上。
docker exec -i "${NEW_MYSQL_CONTAINER}" \
	mysql -uroot -p"${MYSQL_ROOT_PASSWORD}" \
	--force \
	--default-character-set=utf8mb4 \
	"${DB_NAME}" < "${DUMP_FILE}" 2>&1 | grep -v "^mysql: \[Warning\]" || true

echo "  ✅ 导入完成"

rm -f "${DUMP_FILE}"

# ============================================================
# 3. 验证
# ============================================================
echo ""
echo "▶ 验证数据(逐表比对行数)..."

# OLD_COUNTS 是导出时从旧库取的(临时容器已删)。
NEW_COUNTS=$(docker exec -e MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD}" -e DB_NAME="${DB_NAME}" \
	"${NEW_MYSQL_CONTAINER}" sh -c \
	'for T in "$@"; do printf "%s=" "$T"; mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -N -B -e "SELECT COUNT(*) FROM \`$T\`;" "$DB_NAME" 2>/dev/null || echo ERR; done' \
	sh user post_info shop notice banner admin comment comment_like comment_report admin_operation_log 2>/dev/null)

MISMATCH=0
# 注意 while 在管道里跑的是子 shell,改 MISMATCH 不影响父 shell。
# 所以把结果写进临时文件,循环外面再读。
RESULT_FILE="/tmp/migrate-verify-$$.txt"
: > "${RESULT_FILE}"

paste <(echo "$OLD_COUNTS") <(echo "$NEW_COUNTS") | while IFS=$'\t' read -r OLD NEW; do
	[ -z "$OLD" ] && continue
	T="${OLD%%=*}"
	O="${OLD##*=}"
	N="${NEW##*=}"
	if [ "${O}" = "ERR" ] || [ "${N}" = "ERR" ]; then
		# ERR 是"查询失败",不是"两边都是 0"。
		# 之前这里用字符串相等判断,ERR == ERR 会判成"一致" —— 假通过。
		printf "  %-22s 旧=%-8s 新=%-8s ❌ 查询失败\n" "${T}" "${O}" "${N}"
		echo "MISMATCH" >> "${RESULT_FILE}"
	elif [ "${O}" = "${N}" ]; then
		printf "  %-22s 旧=%-8s 新=%-8s ✓\n" "${T}" "${O}" "${N}"
	else
		printf "  %-22s 旧=%-8s 新=%-8s ⚠️ 不一致\n" "${T}" "${O}" "${N}"
		echo "MISMATCH" >> "${RESULT_FILE}"
	fi
done

if grep -q MISMATCH "${RESULT_FILE}" 2>/dev/null; then
	MISMATCH=1
fi
rm -f "${RESULT_FILE}"

echo ""
if [ "${MISMATCH}" = "1" ]; then
	echo "❌ 校验不通过 —— 上面标了 ⚠️/❌ 的表需要人工核对"
	echo "   旧数据还在(容器和卷都保留着),可以重跑本脚本或手工修。"
	exit 1
else
	echo "✅ 各表行数一致"
fi

echo ""
echo "✅ 迁移完成"
echo ""
echo "下一步:"
echo "  1. 把改动 push 到 main,让 CI 部署新 compose"
echo "     git add -A && git commit -m 'feat: 接入共享 MySQL/Redis 底座' && git push"
echo "  2. CI 跑完后清理旧容器(确认数据没问题再删):"
echo "     docker compose down --remove-orphans"
echo "     # 这会删掉 local-life-mysql / local-life-redis 及其数据卷"
echo ""
echo "⚠️ 旧数据卷 local_life_mysql-data 删掉就找不回来了。"
echo "   确认无误后可以手工清:docker volume rm deploy_mysql-data"
