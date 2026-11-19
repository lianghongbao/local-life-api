# local-life-api 部署

数据存储和业务分离部署。

```
┌─────────────────────────────────────────────────┐
│  data-net（外部网络，多个项目接入）               │
│                                                 │
│    mysql:3306   → local_life 库                  │
│    redis:6379   → local-life: 前缀的 key         │
│                                                 │
│    local-life-api:8888                          │
└─────────────────────────────────────────────────┘
```

MySQL 和 Redis 是**独立部署**的服务，各自有自己的 compose；业务容器通过
`data-net` 网络访问它们。

## 目录

```
deploy/
├── docker-compose.yml      业务容器(local-life-api)
├── mysql/docker-compose.yml   独立 project: mysql
├── redis/docker-compose.yml   独立 project: redis
└── db/                     MySQL 建库建表脚本
```

## 首次部署

```bash
# 1. 建网络 —— 数据存储和业务容器都要接进来
docker network create data-net

# 2. 起 MySQL
cd deploy/mysql
cp .env.example .env && vi .env   # MYSQL_ROOT_PASSWORD
docker compose up -d

# 3. 起 Redis
cd ../redis
cp .env.example .env && vi .env   # REDIS_PASSWORD
docker compose up -d

# 4. 起业务容器
cd ..
cp .env.example .env && vi .env   # 应用凭证;MYSQL/REDIS 密码跟上面一致
docker compose up -d
```

> 数据存储的 `.env` 和业务容器的 `.env` 里的 `MYSQL_ROOT_PASSWORD` /
> `REDIS_PASSWORD` 必须**一致** —— 应用就是拿这对凭证连的。

## 其他项目接入

任何项目要访问同一套 MySQL / Redis，把自己的 compose 接进 `data-net`，
然后用容器名连：

```yaml
services:
  your-service:
    networks:
      - data-net

networks:
  data-net:
    external: true
    name: data-net
```

```bash
DB_DATASOURCE=root:<pwd>@tcp(mysql:3306)/your_db?charset=utf8mb4&parseTime=True&loc=Local
REDIS_HOST=redis:6379
```

端口没有暴露到宿主，只有接进 `data-net` 的容器能访问。

## 日常操作

```bash
docker compose ps              # 业务容器状态
docker compose logs -f         # 跟日志
docker compose restart         # 重启业务容器
docker compose down            # 停业务容器

cd mysql && docker compose ps  # MySQL 状态
cd redis && docker compose ps  # Redis 状态
```

## 建库建表脚本

`db/` 下的脚本挂在 MySQL 容器的 `/docker-entrypoint-initdb.d/`，
**只在数据目录为空（首次创建）时执行一次**。

```
db/01-create-databases.sql   建 local_life 库
db/02-local-life-tables.sql  全部业务表（内容同源于 docs/sql/init.sql）
```

改了 `db/` 下的 SQL 之后，容器已经存在的话脚本不会自动重跑，手动补一次：

```bash
docker exec -i mysql mysql -uroot -p"$(grep MYSQL_ROOT_PASSWORD mysql/.env | cut -d= -f2)" < db/01-create-databases.sql
```

## CI 部署

`.github/workflows/deploy.yml` —— push 到 main 触发。

- **build**（GitHub-hosted）：docker buildx build + push 到 ACR
- **deploy**（ECS self-hosted runner）：pull 镜像 → 同步 compose → 起容器 → 健康检查

CI **不会**覆盖 ECS 上的 `.env`（只在缺失时从 `.env.example` 生成并直接退出）。
所以 ECS 上的 `.env` 必须提前填好真实凭证。

配置项（Vars / Secrets）见 `.github/workflows/README.md`。

## 相关的坑

- **Redis key 前缀**：所有 key 都带 `local-life:` 前缀，定义在 `internal/keys` 包。
  别的项目接入同一个 Redis 时要用自己的前缀，别撞 key。
- **MySQL 排序规则**：库和表用 `utf8mb4_unicode_ci`（沿用 `docs/sql/init.sql`
  的约定）。
- **建库时的 COLLATE 只是默认值**，表级显式声明优先；没声明的表继承
  **服务器**默认（MySQL 8.0 是 `utf8mb4_0900_ai_ci`）。
- **initdb.d 只在数据目录为空时跑一次**。
- **compose 的 service 必须显式列 `networks`** —— 只在顶层声明 `networks:`
  是不够的，那样容器会接到默认网络，访问不到 `mysql` / `redis`。
- **外部网络要先建** —— `docker network create data-net`。
