# local-life-api 部署

MySQL / Redis 走**共享底座**（`deploy/infra/`），本目录的 compose 只跑业务容器。

```
┌─────────────────────────────────────────────────┐
│  共享底座 (compose project: shared-infra)       │
│    shared-mysql:3306 → local_life 库             │
│    shared-redis:6379 → local-life: 前缀的 key    │
└─────────────────────────────────────────────────┘
                    ▲ shared-infra-net
        ┌───────────┴────────────────────────┐
        │  local-life (project: local-life)  │
        │    local-life-api:8888             │
        └────────────────────────────────────┘
```

## 首次部署

```bash
cd deploy/infra
cp .env.example .env && vi .env   # MYSQL_ROOT_PASSWORD / REDIS_PASSWORD
./deploy.sh up                     # 建网络 + 起 shared-mysql / shared-redis
cd ..
cp .env.example .env && vi .env    # 应用凭证 + 共享底座密码（同一个值）
docker compose up -d
```

## 从旧版迁移（旧 compose 自带 mysql/redis）

旧版 compose 自带 `local-life-mysql` / `local-life-redis`，容器名和端口跟
共享底座**完全一样**（`127.0.0.1:3306` / `6379`），所以两个不能同时跑。

**在 ECS 上跑这一个命令就行**（在仓库根目录）：

```bash
./deploy/setup-shared-base.sh
```

它做四件事：

1. `git pull` 拉最新代码（拿到 `deploy/infra/` 和迁移脚本）
2. 停掉旧的 mysql / redis（**只 stop，不删** —— 数据还要用）
3. 起共享底座（`shared-mysql` / `shared-redis`，建库建表）
4. 把旧 MySQL 的 `local_life` 数据搬过去，逐表校验行数

脚本会打印每张表的新旧行数对比。**全部 ✓ 之后再往下走**；有任何 ⚠️/❌
就停下来查（旧数据还在容器和卷里，可以重跑或手工修）。

> Redis 不迁移 —— 缓存丢了会自己重建，限流桶这类短期状态重置也无所谓。
>
> 迁移是**幂等**的：导入前会清空目标表，重复跑结果一致。

底层脚本是 `deploy/migrate-to-shared-base.sh`，`setup-shared-base.sh` 只是
加了 git pull 和收尾提示。单独跑也行：

```bash
cd deploy && ./migrate-to-shared-base.sh
```

### 迁移完之后

```bash
# 1. push 触发 CI 部署新 compose
git add -A && git commit -m 'feat: 接入共享 MySQL/Redis 底座' && git push

# 2. CI 绿了之后，清理旧容器（确认数据没问题再删）
docker compose down --remove-orphans
```

> `docker compose down --remove-orphans` 会删掉 `local-life-mysql` /
> `local-life-redis` **及其数据卷**。删了旧数据就找不回来了 ——
> 确认共享底座里的数据没问题再做这一步。

## CI 部署

`.github/workflows/deploy.yml` —— push 到 main 触发。

- **build**（GitHub-hosted）：docker buildx build + push 到 ACR
- **deploy**（ECS self-hosted runner）：pull 镜像 → 同步 compose → 起容器 → 健康检查

CI **不会**覆盖 ECS 上的 `.env`（只在缺失时从 `.env.example` 生成并直接退出）。
所以 ECS 上的 `.env` 必须提前填好真实凭证。

CI 只管业务容器。**共享底座的生命周期是独立的** —— 底座挂了/重建了，
业务应用不会跟着重启，得手工 `cd deploy/infra && ./deploy.sh up`。

配置项（Vars / Secrets）见 `.github/workflows/README.md`。

## 共享底座相关的坑

- **Redis key 前缀**：本应用用 `local-life:` 前缀，chatroom 用 `chatroom:`。
  共用一个 redis 实例靠这个隔开。
- **MySQL 排序规则**：本应用的库和表用 `utf8mb4_unicode_ci`（沿用
  `docs/sql/init.sql` 的约定）。chatroom 用 `utf8mb4_bin`（用户名大小写敏感）。
- **建库时的 COLLATE 只是默认值**，表级显式声明优先；没声明的表继承
  **服务器**默认（MySQL 8.0 是 `utf8mb4_0900_ai_ci`）。
- **initdb.d 只在数据目录为空时跑一次**。底座已存在时用
  `cd deploy/infra && ./deploy.sh init-db` 补。

## 常用命令

```bash
docker compose ps              # 状态
docker compose logs -f         # 跟日志
docker compose restart         # 重启应用
docker compose down            # 停应用（共享底座不受影响）
cd deploy/infra && ./deploy.sh status
cd deploy/infra && ./deploy.sh up      # 底座挂了用这个恢复
```
