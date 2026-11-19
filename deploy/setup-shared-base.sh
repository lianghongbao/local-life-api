#!/usr/bin/env bash
# ============================================================
# 一键起共享底座 + 迁移旧版数据(在 ECS 上手工跑一次)
# ============================================================
# 用途:
#   ECS 上正跑着旧版 compose(自带 local-life-mysql / local-life-redis),
#   现在要切到共享底座(deploy/infra/)。跑这一个脚本搞定。
#
# 它做四件事:
#   1. 从 git 拉最新代码(拿到 deploy/infra/ 和迁移脚本)
#   2. 停掉旧的 mysql / redis(只 stop,数据卷保留)
#   3. 起共享底座(shared-mysql / shared-redis),建库建表
#   4. 把旧 MySQL 的 local_life 数据搬过去,逐表校验行数
#
# Redis 不迁移 —— 缓存丢了会自己重建。
#
# 用法(在 ECS 上,local-life-api 仓库根目录):
#   ./deploy/setup-shared-base.sh
#
# 跑完之后:
#   git push 触发 CI → CI 起新版应用连共享底座
#   确认没问题后:cd deploy && docker compose down --remove-orphans 清理旧容器
#
# set -euo pipefail:任何一步失败就停,别带着半成品往下跑。

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY_DIR="${REPO_DIR}/deploy"

cd "${REPO_DIR}"

# ============================================================
# 1. 拉最新代码
# ============================================================
echo "▶ 拉最新代码..."
if [ -d .git ]; then
	# 拉不到不算致命 —— 底座文件可能已经在本地了(比如手工 cp 过)。
	# 下面会检查文件在不在,在的话继续。
	git pull --ff-only 2>&1 | sed 's/^/  /' || echo "  ⚠️ git pull 失败,继续用当前代码"
else
	echo "❌ ${REPO_DIR} 不是 git 仓库 —— 先 git clone 再跑" >&2
	exit 1
fi

# 底座和迁移脚本可能在这次提交里才加进来
if [ ! -f "${DEPLOY_DIR}/infra/docker-compose.yml" ]; then
	echo "❌ deploy/infra/ 不存在 —— 确认代码是最新的(git pull 过了)" >&2
	exit 1
fi
if [ ! -f "${DEPLOY_DIR}/migrate-to-shared-base.sh" ]; then
	echo "❌ deploy/migrate-to-shared-base.sh 不存在 —— 确认代码是最新的" >&2
	exit 1
fi

# ============================================================
# 2. 应用 .env 检查
# ============================================================
if [ ! -f "${DEPLOY_DIR}/.env" ]; then
	echo "❌ 缺少 deploy/.env —— 应用凭证还没填,先填好再跑" >&2
	exit 1
fi

# ============================================================
# 3. 跑迁移
# ============================================================
chmod +x "${DEPLOY_DIR}/migrate-to-shared-base.sh"
"${DEPLOY_DIR}/migrate-to-shared-base.sh"

echo ""
echo "════════════════════════════════════════════════"
echo "✅ 共享底座已就绪,数据已迁移"
echo ""
echo "接下来:"
echo "  1. 本地提交并 push,让 CI 部署新 compose:"
echo "     git add -A && git commit -m 'feat: 接入共享 MySQL/Redis 底座' && git push"
echo ""
echo "  2. CI 绿了之后验证应用:"
echo "     docker exec local-life-api wget -qO- http://localhost:8888/api/v1/health"
echo "     # 期望 db=ok redis=ok"
echo ""
echo "  3. 确认没问题后清理旧容器(会删旧数据卷,不可恢复):"
echo "     cd deploy && docker compose down --remove-orphans"
echo "════════════════════════════════════════════════"
