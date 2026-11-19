# 共享底座（MySQL + Redis）

一套底座，所有应用共用。应用各自跑自己的 compose，只跑业务容器，
通过 `shared-infra-net` 网络连到底座。

```
┌──────────────────────────────────────────────┐
│  共享底座 (compose project: shared-infra)    │
│                                              │
│   shared-mysql:3306                          │
│     ├── local_life   ← local-life-api        │
│     └── chatroom     ← chatroom 五个服务     │
│                                              │
│   shared-redis:6379                          │
│     ├── local-life:* ← local-life-api        │
│     └── chatroom:*   ← chatroom              │
└──────────────────────────────────────────────┘
                    ▲
                    │ shared-infra-net（external）
        ┌───────────┴───────────┐
        │                       │
  local-life-api          chatroom（5 个服务）
  deploy/docker-compose.yml   deploy/docker-compose.yml
```

## 隔离方式

| | 方式 | 为什么 |
|---|---|---|
| MySQL | 独立 schema | 同一实例天然隔离，跨 schema 查不到对方数据 |
| Redis | **key 前缀** | 见下 |

**Redis 为什么不用 DB 号隔离**：local-life-api 用 go-zero，其 Redis 客户端在
`core/stores/redis/redisclientmanager.go` 里把 `DB` 硬编码成 0，配置层没有
`DB` 字段可填。要按 DB 号隔离就得绕开 `redis.MustNewRedis` 直连 go-redis，
改动侵入生成代码。chatroom 侧虽然能配 `redis.Options.DB`，两边不对称，
统一走 key 前缀更简单可靠。

key 前缀的定义：
- local-life-api → `internal/keys` 包，`local-life:` 前缀
- chatroom → `pkg/keys` 包，`chatroom:` 前缀（本来就是）

> Redis Pub/Sub **不按 DB 隔离**。两个应用目前没有共用的频道名
> （chatroom 用 `chatroom:msg:*`，local-life-api 不用 Pub/Sub），所以不冲突。

## 首次部署

```bash
cd deploy/infra

# 1. 填凭证
cp .env.example .env
vi .env          # MYSQL_ROOT_PASSWORD / REDIS_PASSWORD / ACR 仓库地址

# 2. 起底座（脚本会建好 shared-infra-net 网络）
./deploy.sh up

# 3. 确认两个库都建出来了
docker exec shared-mysql mysql -uroot -p"$(grep MYSQL_ROOT_PASSWORD .env | cut -d= -f2)" \
  -e "SHOW DATABASES;"
# 期望看到 local_life 和 chatroom
```

`deploy.sh` 会自动建 `shared-infra-net` 网络并等两个容器健康。

### 底座之后的部署顺序

底座先起，之后各应用按自己的方式部署：

| 应用 | 命令 | 备注 |
|---|---|---|
| local-life-api | `cd deploy && docker compose up -d` | 只跑业务容器 |
| chatroom | `cd deploy && ./deploy.sh up` | nacos + 五个服务 |

两个应用都**不要**在各自的 compose 里再起 mysql / redis。

> chatroom 的 `./deploy.sh up` 里有一步是给 nacos 建管理员账号 ——
> nacos 3.x 开了鉴权之后首次启动不会自动建管理员，没有管理员的话
> chatroom 五个服务会在启动时 panic。详见 chatroom 的 `deploy/README.md`。

## 日常操作

```bash
./deploy.sh status    # 看容器状态
./deploy.sh down      # 停掉（数据卷保留）
./deploy.sh up        # 改完 .env 后重启
```

## 建库建表脚本

`init/` 下的脚本挂在 mysql 容器的 `/docker-entrypoint-initdb.d/`，
**只在数据目录为空（首次创建）时执行一次**。

```
init/01-create-databases.sql   建 local_life + chatroom 两个 schema
init/02-local-life-tables.sql  local-life-api 的表（来自 docs/sql/init.sql）
init/02-chatroom-tables.sql    chatroom 五个服务的表（来自各服务 migrations/）
```

两个仓库里的 `deploy/infra/` 内容**完全一致**（包括 init/ 下的全部脚本）——
随便用哪一份起底座都能建全两边的表。

改了 `init/` 下的 SQL 之后，底座已经存在的话脚本不会自动重跑，
手动补一次：

```bash
./deploy.sh init-db
```

## 表结构权威

- **local-life-api**：`docs/sql/init.sql` 是权威，`docs/sql/migrations/` 放增量
- **chatroom**：各服务的 ent schema 是权威，服务启动时自动增量对齐；
  `app/<服务>/service/migrations/*.sql` 只是空库初始化快照

服务本身启动时会 auto_migrate 对齐表结构，所以 init 脚本只是为了
「不依赖 Go 运行时也能建空库」。加新表时两边都要同步：
改 ent schema / init.sql，同时更新共享底座的 init 脚本。

## 连接参数

各应用的 compose 从 `.env` 里拿这些值：

```bash
# MySQL
DB_DATASOURCE=root:${MYSQL_ROOT_PASSWORD}@tcp(shared-mysql:3306)/local_life?charset=utf8mb4&parseTime=True&loc=Local
DB_SOURCE=root:${MYSQL_ROOT_PASSWORD}@tcp(shared-mysql:3306)/chatroom?parseTime=true&loc=Local&charset=utf8mb4

# Redis
REDIS_HOST=shared-redis:6379
REDIS_ADDR=shared-redis:6379
```

容器名 `shared-mysql` / `shared-redis` 只在 `shared-infra-net` 网络里可解析，
所以业务容器**必须**接这个网络（compose 里 `networks: [shared-infra-net]`）。

## 端口

两个容器都只绑 `127.0.0.1`，不对外网暴露。远程调试走 SSH 隧道：

```bash
ssh -L 3306:127.0.0.1:3306 -L 6379:127.0.0.1:6379 root@<服务器IP>
```
