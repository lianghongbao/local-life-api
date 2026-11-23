# GitHub Actions 部署配置

`deploy.yml` 是本项目的 CI/CD 流水线,通过 GitHub Secrets + Variables 注入环境差异。

## 架构

两个 job 串联:

```
push main
    │
    ▼
build (GitHub-hosted runner)
    └─ docker buildx build + push → 镜像仓库
    │
    ▼ needs: build
deploy (self-hosted runner on ECS)
    ├─ docker pull
    ├─ 同步 compose 到部署目录
    ├─ 确认 mysql / redis 在跑
    └─ docker compose up -d + health check
```

- **build 跑在 GitHub-hosted runner**(出网方便,适合 docker buildx + 推镜像)
- **deploy 跑在 self-hosted runner**(在 ECS 内,持有 ECS 部署目录的写权限)

## Vars(明文)

路径:`repo Settings → Secrets and variables → Actions → Variables`

| Var | 含义 |
| ---- | ---- |
| `ACR_REGISTRY_HOST` | 镜像仓库公网端点域名,例 `crpi-XXXX.personal.cr.aliyuncs.com` |
| `ACR_NAMESPACE` | 仓库 namespace,例 `<your-ns>/<your-repo-prefix>` |
| `IMAGE_NAME` | 本项目镜像名,例 `local-life-api` |
| `APP_CONTAINER` | compose 里 `container_name`,健康检查寻址用 |
| `BASE_GOLANG_TAG` | builder 基础镜像 tag(Dockerfile 的 `ARG GOLANG_TAG`) |
| `BASE_ALPINE_TAG` | runtime 基础镜像 tag(Dockerfile 的 `ARG ALPINE_TAG`) |
| `HEALTH_PORT` | 健康检查目标端口 |
| `HEALTH_PATH` | 健康检查路径 |
| `DEPLOY_DIR` | ECS 上部署目录(包含 `docker-compose.yml` + `.env`) |
| `RUNNER_LABELS` | self-hosted runner 标签,逗号分隔 |
| `CONCURRENCY_GROUP` | 部署并发组名,例 `deploy-<your-project>` |

## Secrets(加密)

路径:`repo Settings → Secrets and variables → Actions → New repository secret`

| Secret | 用途 |
|--------|------|
| `ACR_USERNAME` | 镜像仓库的 RAM 用户名(build job 推镜像用) |
| `ACR_PASSWORD` | 镜像仓库的 RAM 密码 |

> deploy job 拉镜像用的是 **ECS 上 `${DEPLOY_DIR}/.env` 里的
> `REGISTRY_USERNAME` / `REGISTRY_PASSWORD`** —— 跟 build job 的那对是两套凭证。

## ECS 上要准备的东西

首次部署前:

| 位置 | 内容 |
|------|------|
| `${DEPLOY_DIR}/.env` | 应用凭证 + 镜像仓库拉取凭证。**至少** 含 `REGISTRY_USERNAME` / `REGISTRY_PASSWORD` / `MYSQL_ROOT_PASSWORD` / `REDIS_PASSWORD` |
| `${DEPLOY_DIR}/docker-compose.yml` | 业务容器。由 deploy job 自动从仓库同步,**不需要**在 ECS 上手维护 |
| `${DEPLOY_DIR}/mysql/` `redis/` | 独立部署的 MySQL / Redis compose。同样由 deploy job 同步 |
| `${DEPLOY_DIR}/db/` | MySQL 建库建表脚本。同样由 deploy job 同步 |

`deploy/` 下的 `.env` 首次 deploy 会从仓库 `.env.example` 复制过来,
之后**必须手动填真实凭证**才能正常启动。

## 镜像 tag 约定

镜像全名 = `${ACR_REGISTRY_HOST}/${ACR_NAMESPACE}/${IMAGE_NAME}:<tag>`。

| Tag | 类型 | 用途 |
|-----|------|------|
| `${IMAGE_NAME}-<sha7>` | immutable | 精确版本,由 build job 打 |
| `${IMAGE_NAME}:server-latest` | mutable 指针 | deploy job 拉这个,compose 的 `image:` 也指向它 |

compose 的 `image:` 由 `${ACR_REGISTRY_HOST}/${ACR_NAMESPACE}/${IMAGE_NAME}:server-latest` 拼出
(没配 ACR 时退化成裸名,本地构建用)。两个 tag 都在仓库里直接存在,ECS 上不需要再 re-tag。

## 仓库端点

仓库一般同时提供:

- **公网端点**:`<id>.<region>.cr.aliyuncs.com` —— GitHub-hosted runner 走这个
- **vpc 端点**:`<id>-vpc.<region>.cr.aliyuncs.com` —— 仅在 ECS 内网走这个

两份流水线都按公网端点走:vpc 端点 GitHub-hosted runner 访问不到。

> **经验**:login / push 一定要走公网端点走通后,再考虑要不要把 push 切到 vpc 端点加速。个人版**只支持公网 login**,vpc login 在某些场景会 403。

## 已知约束

- GitHub-hosted runner 只能访问公网端点;内网端点会 connection refused
- self-hosted runner 必须同时打 `RUNNER_LABELS` 里的所有标签,否则 deploy job 找不到 runner,会一直 queued
- 基础镜像(golang / alpine / 依赖服务)必须先 sync 到镜像仓库,否则 build / deploy 会因 `FROM` 拉不到而 fail
- `deploy.yml` 用的是 GitHub Actions 默认 token `${{ secrets.GITHUB_TOKEN }}`,不需要额外配置 PAT
- CI 只管业务容器。**MySQL / Redis 的生命周期是独立的** —— 它们挂了要手工
  `cd ${DEPLOY_DIR} && docker compose restart mysql redis` 恢复
