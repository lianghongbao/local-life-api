# local-life-api

小城便利贴后端服务(C 端用户端 + 管理后台),基于 [go-zero](https://github.com/zeromicro/go-zero) 框架构建。

提供三类核心能力:

- **C 端接口**:微信小程序登录、信息发布/浏览、商家入驻/浏览、评论互动、文件上传凭证
- **管理后台接口**:内容审核、用户管理、公告、轮播、账号管理、评论 feed 直接管控
- **统一基础设施**:JWT 鉴权、OSS V4 直传、统一响应信封、限流

---

## 目录

- [技术栈](#技术栈)
- [项目结构](#项目结构)
- [快速开始](#快速开始)
- [环境要求](#环境要求)
- [配置](#配置)
- [数据库](#数据库)
- [接口文档](#接口文档)
  - [C 端用户接口](#c-端用户接口)
  - [评论接口](#评论接口)
  - [管理后台接口](#管理后台接口)
  - [公共服务接口](#公共服务接口)
- [统一响应格式](#统一响应格式)
- [OSS 文件上传](#oss-文件上传)
- [用户标识体系](#用户标识体系)
- [代码生成](#代码生成)
- [测试](#测试)
- [Docker 部署](#docker-部署)

---

## 技术栈

| 组件 | 版本/选型 | 说明 |
|------|----------|------|
| 语言 | Go 1.24+ | |
| Web 框架 | [go-zero](https://github.com/zeromicro/go-zero) | REST 路由 + 配置加载 |
| 响应包装 | `zeromicro/x` | 统一 `{code, msg, data}` 信封 |
| 数据库 | MySQL 8.0 | 字符集 `utf8mb4` |
| 缓存 | Redis 7+ | go-zero 的 `CacheConn`,所有带缓存的 model 都走它 |
| 对象存储 | 阿里云 OSS | 直传 + STS 临时凭证 + V4 签名 |
| 鉴权 | JWT(HS256) | 主载荷为 `openid`,独立 Admin token |
| API 规范 | go-zero `.api` 文件 | `api/*.api` 是接口契约的 source of truth |

---

## 项目结构

```
local-life-api/
├── api/                # .api 接口契约(go-zero 规范)
│   ├── base.api        # 公共 type(CodeErrorResponse 等)
│   ├── mini.api        # C 端 + 鉴权 + 用户
│   ├── admin.api       # 后台账号/通用
│   ├── comment.api     # C 端评论
│   ├── admin_comment*.api  # 后台评论
│   ├── post.api        # 信息(发布)
│   ├── shop.api        # 商家
│   ├── notice.api      # 公告
│   ├── banner.api      # 轮播
│   ├── user.api        # 用户中心
│   ├── oss.api         # 上传凭证
│   ├── geo.api         # 地理位置(逆地理编码等)
│   ├── home.api        # 首页聚合
│   └── health.api      # 健康检查
├── common/             # 通用工具
│   ├── oss/            # OSS V4 PostObject + STS + URLPrefix 拼装
│   ├── jwtx/           # JWT 生成/解析(user / admin 两套)
│   ├── wx/             # 微信小程序 code2session
│   ├── middleware/     # 统一响应 + 鉴权 + CORS
│   ├── errorx/         # 业务错误码
│   ├── bcryptx/        # 密码 bcrypt
│   ├── httpx/          # xhttp.JsonBaseResponseCtx 响应包装
│   └── response/       # zeromicro/x 信封工厂
├── internal/
│   ├── handler/        # HTTP handler(goctl 生成,xhttp 包装)
│   ├── logic/          # 业务逻辑(手写)
│   │   ├── shared/     # 跨域共享逻辑(uploadtoken、avatar 规范化等)
│   │   ├── user/       # C 端用户域
│   │   └── admin/      # 管理后台域
│   ├── model/          # 数据访问层(goctl mysql 生成,含 CacheConn)
│   ├── keys/           # Redis key 前缀(所有 key 统一走这里)
│   ├── svc/            # ServiceContext 依赖注入
│   ├── types/          # 请求/响应 DTO(goctl 生成)
│   ├── config/         # 配置文件解析 target
│   ├── middleware/     # 限流、JWT 鉴权
│   └── etc/            # etcd 注册(预留,未启用)
├── docs/
│   ├── openapi.json    # Swagger 2.0 完整导出(由 goctl 生成,覆盖全部 47 个路径)
│   └── sql/            # init.sql(完整当前 schema)+ migrations/(增量脚本占位)
├── etc/                # 运行时配置(.yaml)
├── deploy/             # 应用 compose + .env.example
│   ├── db/             # MySQL 建库建表脚本(首次创建时执行)
│   ├── mysql/          # 独立部署的 MySQL
│   ├── redis/          # 独立部署的 Redis
├── Dockerfile          # 容器镜像构建
├── entrypoint.sh       # 容器启动脚本
├── logs/               # 运行日志(按天滚动)
├── mini.go             # 主入口(go-zero 默认)
├── go.mod / go.sum     # Go 模块
└── README.md           # 本文档
```

---

## 快速开始

```bash
# 1. 克隆 & 进入目录
cd local-life-api

# 2. 安装依赖
go mod download

# 3. 准备配置(本地开发用 dev 模板)
cp etc/mini-api-dev.example.yaml etc/mini-api-dev.local.yaml
# 编辑 .local.yaml,填入 MySQL / Redis / 微信 / OSS 凭证

# 4. 准备数据库
#    生产环境用 docker compose 起的 mysql/redis。
mysql -uroot -p < docs/sql/init.sql

# 5. 编译并启动
go build -o local-life-api .
GO_CONFIG_FILE=etc/mini-api-dev.yaml ./local-life-api

# 6. 健康检查
curl http://127.0.0.1:8888/api/v1/health
```

服务默认监听 `:8888`,可通过环境变量或 YAML 的 `Port` 字段调整。

---

## 环境要求

| 组件 | 最低版本 | 说明 |
|------|---------|------|
| Go | 1.24 | 编译期必需 |
| MySQL | 8.0 | 主存储,需支持 utf8mb4 |
| Redis | 7.0 | 限流 + 自带缓存(可选) |
| 阿里云 OSS | - | 文件存储(bucket 私有读写,见 `etc/*.example.yaml`) |
| 微信小程序 | - | 需要 AppID + AppSecret 用于 `jscode2session` |
| 阿里云 RAM | - | 需要 STS AssumeRole 给小程序角色颁发临时凭证 |

本地推荐:WSL2 / Docker Desktop / 原生 Linux,Windows 直接编译可运行但日志按天 rolling 在 Windows 上有 rename 权限坑。

---

## 配置

配置文件位于 `etc/`,命名约定 `<name>-<env>.yaml`:

| 文件 | 用途 |
|------|------|
| `mini-api-dev.example.yaml` | dev 模板(committed),含 `${VAR:-default}` 占位符 |
| `mini-api-dev.local.yaml` | 本地真实凭证(**gitignore**) |
| `mini-api-dev.yaml` | dev 默认值(committed) |
| `mini-api.example.yaml` | prod 模板 |
| `mini-api.yaml` | prod 占位符(部署平台注入 env) |

切换配置:

```bash
GO_CONFIG_FILE=etc/mini-api.yaml ./local-life-api        # prod
GO_CONFIG_FILE=etc/mini-api-dev.yaml ./local-life-api    # dev
```

### 关键配置项

```yaml
Name: mini-api
Host: 0.0.0.0
Port: 8888

# MySQL(用环境变量注入,不要把真实 DSN 写进 committed 文件)
DB:
  DataSource: "${DB_DATASOURCE}"        # 格式: user:pass@tcp(host:3306)/dbname?charset=utf8mb4&parseTime=True&loc=Local
  Policy: round-robin

# Redis(go-zero 的 CacheConn,带缓存的 model 走它)
CacheRedis:
  - Host: "${REDIS_HOST}"               # 格式: host:port
    Type: node
    Pass: "${REDIS_PASSWORD}"
    Weight: 100

# JWT(用户端和后台端各一套密钥)
Auth:
  AccessSecret: "${AUTH_ACCESS_SECRET}" # 生产必须 ≥32 字节随机串
  AccessExpire: 7200
AdminAuth:
  AccessSecret: "${ADMIN_AUTH_SECRET}"
  AccessExpire: 7200
RefreshSecret: "${REFRESH_SECRET}"

# 微信小程序
WxMini:
  AppID:     "${WX_APP_ID}"
  AppSecret: "${WX_APP_SECRET}"

# 阿里云 OSS(所有真实值通过环境变量注入)
OSS:
  Endpoint:        "${OSS_ENDPOINT}"   # 例如: oss-cn-<region>.aliyuncs.com
  Bucket:          "${OSS_BUCKET}"     # 你的 bucket 名
  Region:          "${OSS_REGION}"     # 例如: cn-<region>
  AccessKeyID:     "${OSS_ACCESS_KEY_ID}"
  AccessKeySecret: "${OSS_ACCESS_KEY_SECRET}"
  RoleArn:         "${OSS_ROLE_ARN}"   # 阿里云 RAM STS 角色 ARN,格式见阿里云文档
  UploadHost:      "${OSS_UPLOAD_HOST}" # 直传目标
  URLPrefix:       "${OSS_URL_PREFIX}" # CDN 加速域名,例如 https://cdn.example.com
  Hosts:                              # 图片入库 URL 域名白名单
    - "${OSS_ALLOWED_HOST}"           # 与 URLPrefix 域名一致
```

> **所有真实凭证必须通过环境变量 / `.local.yaml` (gitignore) 注入**。`etc/*.example.yaml` 是脱敏模板,committed 进仓库。
>
> Redis 的所有 key 都带 `local-life:` 前缀,统一走 `internal/keys` 包。详见 [internal/model/README.md](internal/model/README.md)。

---

## 数据库

数据库名:`local_life`,字符集 `utf8mb4`,排序规则 `utf8mb4_unicode_ci`。

### 初始化(dev / 新环境)

```bash
mysql -uroot -p < docs/sql/init.sql
```

跑完即获得完整 schema + 默认管理员账号(`admin / <change-me>`,上线后必须改密码)。

### 已部署实例增量迁移

`docs/sql/migrations/` 是占位目录,默认情况下**不会有任何文件**。当一个 schema 变更需要同步到已经上线的实例时:

1. 新增 `docs/sql/migrations/YYYY-MM-DD-<description>.sql`(追加式 DDL,不 DROP 表)
2. **把同一份变更同步进 `init.sql`**,保证新部署依然能跑 `init.sql` 拿到一致结构

### 主要数据表

| 表 | 说明 |
|----|------|
| `user` | C 端用户,主键 `id`,业务标识 `openid`,预留 `unionid` |
| `admin` | 后台账号(`username` + bcrypt 密码) |
| `post_info` | 信息(社区帖子/求助/闲置/招聘等),含经纬度 + 行政区划六字段 |
| `shop` | 商家(入驻申请 + 审核),含经纬度 + 行政区划六字段 |
| `notice` | 公告 |
| `banner` | 轮播图 |
| `comment` | 评论(含父子回复,`status` 1=正常 2=已删除 3=审核中) |
| `comment_like` | 评论点赞 |
| `comment_report` | 举报记录(>=3 自动隐藏) |
| `admin_operation_log` | admin 操作审计(评论审核 keep/hide/delete 等) |

---

## 接口文档

完整 OpenAPI Schema 导出(Swagger 2.0,覆盖全部 47 个路径):[docs/openapi.json](docs/openapi.json)

再生命令:

```bash
goctl api swagger --api api/mini.api --dir docs --filename openapi
```

> `mini.api` 是统一入口(import 了其余 13 个 .api 文件),所以一条命令覆盖全部端点。

所有接口统一前缀:

| 前缀 | 用途 | 鉴权 |
|------|------|------|
| `/api/v1` | C 端用户接口 | 部分 `JWT(user)` |
| `/api/v1/admin` | 管理后台接口 | `JWT(admin)`,前缀独立 token |

### C 端用户接口

挂在 `/api/v1/` 前缀下。

| Method | Path | 说明 | 鉴权 |
|--------|------|------|------|
| POST | `/login` | 微信小程序登录(`code` → `openid` → JWT) | ❌ |
| GET  | `/home` | 首页聚合(公告 / 轮播 / 精选商家) | ❌ |
| GET  | `/posts` | 公开信息列表(已审核通过) | ❌ |
| GET  | `/posts/:id` | 信息详情 | ❌ |
| GET  | `/shops` | 商家列表(已审核通过) | ❌ |
| GET  | `/shops/:id` | 商家详情 | ❌ |
| GET  | `/notices` | 公告列表 | ❌ |
| GET  | `/notices/:id` | 公告详情 | ❌ |
| GET  | `/user/profile` | 当前用户资料 | ✅ |
| POST | `/user/profile` | 更新昵称/头像 | ✅ |
| GET  | `/user/posts` | 我的发布 | ✅ |
| GET  | `/user/shops` | 我的商家 | ✅ |
| POST | `/posts` | 发布信息 | ✅ |
| PUT  | `/posts/:id` | 编辑自己的发布 | ✅ |
| PUT  | `/posts/:id/offline` | 下架发布 | ✅ |
| POST | `/shops` | 入驻商家 | ✅ |
| PUT  | `/shops/:id` | 编辑自己的商家 | ✅ |
| POST | `/oss/upload-token` | OSS 上传凭证(`biz` 枚举) | ✅ |
| POST | `/geocode/reverse` | 经纬度 → 行政区(adcode) | ❌ |
| GET  | `/health` | 健康检查 | ❌ |

### 评论接口

挂在 `/api/v1/posts/:post_id/comments`。

| Method | Path | 说明 | 鉴权 |
|--------|------|------|------|
| GET    | `/` | 拉一级评论 + top N 子回复 | ❌ |
| GET    | `/:root_id/replies` | 二次拉子回复 | ❌ |
| POST   | `/` | 发评论 / 回复 | ✅ |
| DELETE | `/:comment_id` | 删除评论(作者 / admin) | ✅ |
| POST   | `/:comment_id/like` | 点赞 toggle | ✅ |
| POST   | `/:comment_id/report` | 举报(默认 ≥3 次自动隐藏) | ✅ |

**限流**:

- 发评论 10/分钟/user
- 点赞 60/分钟/user
- 举报 10/小时/user

### 管理后台接口

挂在 `/api/v1/admin/` 前缀下,**独立 Admin JWT**。

| 类别 | 接口 |
|------|------|
| 登录 | `POST /login` |
| 用户管理 | `GET /users`、`GET /users/:id`(含 `openid`)、`PUT /users/:id/ban`(封禁/解封) |
| 信息审核 | `GET /posts`、`PUT /posts/:id/audit`、`DELETE /posts/:id` |
| 商家审核 | `GET /shops`、`PUT /shops/:id/audit` |
| 公告管理 | `GET /notices`、`POST /notices`、`PUT /notices/:id`、`DELETE /notices/:id`、`GET /notices/:id` |
| 轮播图 | `GET /banners`、`POST /banners`、`PUT /banners/:id`、`DELETE /banners/:id` |
| 账号管理 | `GET /accounts`、`POST /accounts`、`PUT /accounts/:id`、`PUT /accounts/:id/password`、`DELETE /accounts/:id` |
| 评论举报队列 | `GET /comments`、`POST /comments/:comment_id/audit`(`action=keep / hide / delete`) |
| 评论 feed 直管 | `GET /comments/feed`、`POST /comments/:comment_id/hide`、`POST /comments/:comment_id/restore`、`DELETE /comments/:comment_id` |
| 上传凭证 | `POST /oss/upload-token`(`biz` 仅 `banner / notice`,UID 固定 0) |

### 公共服务接口

| 接口 | 说明 |
|------|------|
| `GET /api/v1/health` | 健康检查,返回 `{status:ok}` |
| `POST /api/v1/geocode/reverse` | 经纬度 → adcode + 地址,内部缓存腾讯地图 Result |
| `POST /api/v1/oss/upload-token` | 颁发 OSS PostObject V4 凭证(详见下文) |

---

## 统一响应格式

所有接口(无论 C 端还是 admin)返回如下信封:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": { /* 业务载荷 */ }
}
```

| code | 含义 | 前端处理 |
|------|------|---------|
| `0` | 业务成功 | 读 `data` |
| `401` | JWT 无效/过期 | 清登录态,引导重新登录 |
| `403` | 权限不足(管理员鉴权失败) | 退到无权限页 |
| `1001` | 参数错误 | 展示 `msg`,常用于 OSS URL 校验失败 |
| 其他 | 业务错误 | 直接展示 `msg` |

实现层:`internal/handler` 用 `xhttp.JsonBaseResponseCtx` 包装,`logic` 用 `errorx.NewCodeError(code, msg)` 抛错,中间件统一拦截兜底。

---

## OSS 文件上传

C 端和 admin 共用 `internal/logic/shared/uploadtoken.Builder`,接口同形但 `biz` 枚举和 `UID` 不同。

### 响应字段(`GetUploadTokenResponse`)

| 字段 | JSON key | 用途 |
|------|----------|------|
| `host` | `host` | 直传 OSS 的目标 host(bucket 域名) |
| `file_key` | `file_key` | 推荐路径(`<biz>/<uid>/<uuid><ext>`) |
| `url` | `url` | **完整访问 URL** = `URLPrefix + fileKey`,永久有效 |
| `policy` | `policy` | Base64 编码的 PostObject Policy |
| `oss` | `oss` | STS 临时凭证(`access_key_id` / `security_token` / `expiration`) |
| `v4` | `v4` | V4 签名头(`x-oss-credential` / `x-oss-date` / `x-oss-signature` 等) |
| `expire_at` | `expire_at` | 整套凭证过期 Unix 时间戳(秒) |

### 前端直传流程

```
1. 选文件
2. POST /api/v1/oss/upload-token  拿 host / file_key / url / oss / v4
3. wx.uploadFile({
     url: host,
     formData: { key, policy, 'x-oss-*': ... }
   })   ← 直传 OSS,不走后端
4. 把 url 提交到入库存量接口(POST /posts /shops /banners ...)
```

### 入库校验

后端在落库前对 `image` 字段做严格校验,**任一不满足返回 `code=1001`**:

- `host` 必须命中 `OSS.Hosts` 白名单
- 路径必须以 `<biz>/<uid>/` 或 `<biz>/0/` 开头
- 后缀必须在白名单内(jpg / png / webp / gif / mp4 …)

### `biz` 枚举

| 值 | 路径前缀 | 谁能调 |
|----|----------|--------|
| `post` | `post/<uid>/` | C 端 `JWT(user)` |
| `shop` | `shop/<uid>/` | C 端 `JWT(user)` |
| `avatar` | `avatar/<uid>/` | C 端 `JWT(user)` |
| `banner` | `banner/0/` | C 端 + Admin |
| `notice` | `notice/0/` | Admin |

### 图片访问(URLPrefix)

后端不做签名,所有图片 URL 由后端用 `OSS.URLPrefix` 配置直拼:

```
config.URLPrefix + file_key  →  完整访问 URL
```

默认 `URLPrefix` 是 CDN 加速域名(由阿里云 CDN 回源 OSS bucket)。`Client.URL(fileKey)` / `Client.URLs(keys)` 是统一拼装入口,**所有图片返回接口都走这条路径**,自动带 HTTPS。

---

## 用户标识体系

后端使用三层标识体系,业务标识用 `openid`,内部关联用 `user.id`。

| 字段 | 用途 | 对外暴露 |
|------|------|---------|
| `user.id` | DB 主键自增,内部关联用 | 响应给前端展示 |
| `user.openid` | 微信唯一标识(**业务标识符**) | 仅自家客户端 |
| `user.unionid` | 同主体跨端合并(预留) | 仅自家客户端 |

### 关键约定

- **JWT 主载荷是 `openid`,不是 `user.id`**:业务 logic 通过 `openid` 反查 `user.id`
- **C 端与 Admin 各自一套 JWT 体系**:C 端 token 载荷含 `openid`,Admin token 载荷只含 admin ID,两套互不干扰
- **业务身份获取走统一接口**:logic 层不直接读 JWT,统一通过 context 辅助函数取身份

---

## 代码生成

`api/mini.api` 通过 `import` 引入所有子模块的 `.api` 文件,重生时只需要对 `mini.api` 跑一次 goctl,即可覆盖全部端点。

修改任一 `.api` 文件后:

```bash
# 重生全部 handler / types(覆盖 api/ 下 import 的所有 .api)
goctl api go -api api/mini.api -dir . --style go_zero --home .

# 加列后重生 model(整表)
goctl model mysql datasource \
    -url "${MYSQL_DSN}" \
    -table user -dir ./internal/model --style go_zero

# 重生后跑一次 mod tidy 和 build 验证
go mod tidy
go build ./...
```

> 业务 `logic` 是手写的,goctl 不会覆盖。

---

## 测试

```bash
# 全部
go test ./...

# 单独模块
go test ./internal/logic/user/...
go test ./internal/logic/admin/...

# 覆盖率
go test ./... -cover
```

测试重点:

- `internal/logic/shared/uploadtoken` — OSS V4 签名正确性
- `internal/logic/shared/avatarnormalize` — 头像 URL 规范化(admin 必须用此路径)
- `common/jwtx` — JWT 签发与解析(用户 / admin 两套)
- 业务 logic 单测:评论举报阈值、商家审核流、信息上下架

---

## Docker 部署

数据存储和业务分离部署 —— MySQL / Redis 是**独立部署**的服务，
各自有自己的 compose；业务容器通过 `data-net` 网络访问它们。

```
data-net（外部网络）
  mysql:3306   → local_life 库
  redis:6379   → local-life: 前缀的 key
  local-life-api:8888
```

### 构建

```bash
# 多阶段构建,产物在 bin/
docker build -t local-life-api-latest .

# 或本地构建 + docker load(更省网络)
CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/local-life-api .
```

### 启动

```bash
# 1. 建网络 —— 数据存储和业务容器都接进来
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

# 看日志
docker compose logs -f local-life-api
```

### 健康检查

```bash
curl http://127.0.0.1:8888/api/v1/health
# {"code":0,"msg":"ok","data":{"status":"ok","items":[{"name":"db","status":"ok"},{"name":"redis","status":"ok"}]}}
```

### CI/CD

`.github/workflows/deploy.yml`,push 到 main 触发:

1. **build**(GitHub-hosted runner):`docker buildx build` → push 到阿里云 ACR
2. **deploy**(ECS self-hosted runner):pull 镜像 → 同步 compose → 起容器 → 健康检查

配置项(Vars / Secrets)、首次部署步骤见 [deploy/README.md](deploy/README.md)
和 [.github/workflows/README.md](.github/workflows/README.md)。

---

## 相关文档

- [部署说明 deploy/README.md](deploy/README.md) — compose + CI 部署

- [CI 配置 .github/workflows/README.md](.github/workflows/README.md) — Vars / Secrets
- [Redis key 约定 internal/model/README.md](internal/model/README.md) — key 前缀
- [小程序端 local-life/](../local-life/) — 微信小程序工程
- [管理后台 local-life-admin/](../local-life-admin/) — Vue3 后台工程
