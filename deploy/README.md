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
# 1. 先起共享底座
cd deploy/infra
cp .env.example .env && vi .env   # MYSQL_ROOT_PASSWORD / REDIS_PASSWORD
./deploy.sh up                     # 建网络 + 起 shared-mysql / shared-redis
cd ..

# 2. 再起应用
cp .env.example .env && vi .env    # 应用凭证 + 共享底座密码（同一个值）
docker compose up -d
```

## 共享底座怎么用

底座的 compose 在 `deploy/infra/`，是**独立的 compose project**（`shared-infra`），
生命周期跟业务应用分开：

```bash
cd deploy/infra
./deploy.sh up        # 起底座（建网络 + 起 shared-mysql / shared-redis）
./deploy.sh status    # 看状态
./deploy.sh down      # 停掉（数据卷保留）
./deploy.sh init-db   # 补执行 init/ 下的建库建表脚本
```

底座**不随业务代码发布重启** —— CI 只管 `local-life-api` 容器。
底座挂了要手工 `cd deploy/infra && ./deploy.sh up` 恢复。

> 底座的 `.env` 里的 `MYSQL_ROOT_PASSWORD` / `REDIS_PASSWORD` 必须跟
> `deploy/.env` 填**同一个值** —— 应用就是拿这对凭证连底座的。

## CI 部署

`.github/workflows/deploy.yml` —— push 到 main 触发。

- **build**（GitHub-hosted）：docker buildx build + push 到 ACR
- **deploy**（ECS self-hosted runner）：pull 镜像 → 同步 compose → 起容器 → 健康检查

CI **不会**覆盖 ECS 上的 `.env`（只在缺失时从 `.env.example` 生成并直接退出）。
所以 ECS 上的 `.env` 必须提前填好真实凭证。

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
