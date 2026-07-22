# GitHub Actions 部署配置

`deploy.yml` 是 CI/CD 模板,通过 GitHub Secrets + Variables 注入环境差异,可直接复制到任何 go-zero / 类似后端项目复用。

## 架构

两个 job 串联:

```
push main
    │
    ▼
build (GitHub-hosted runner)
    ├─ docker build + push  →  公网镜像仓库
    └─ 输出 image_tag
    │
    ▼ needs: build
deploy (self-hosted runner on ECS)
    ├─ docker pull
    ├─ docker compose up -d  (--no-deps,不重启依赖服务)
    └─ health check
```

- **build 跑在 GitHub-hosted runner**(出网方便,适合 docker buildx + 推镜像)
- **deploy 跑在 self-hosted runner**(在 ECS 内,持有 ECS 部署目录的写权限)

## Vars(明文,每个项目改这些)

路径:`repo Settings → Secrets and variables → Actions → Variables`

| Var | 含义 |
| ---- | ---- |
| `REGISTRY_HOST` | 镜像仓库公网端点域名,例 `crpi-XXXX.personal.cr.aliyuncs.com` |
| `REGISTRY_NAMESPACE` | 仓库 namespace,例 `<your-ns>/<your-repo-prefix>` |
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
| `REGISTRY_USERNAME` | 镜像仓库的 RAM 用户名 |
| `REGISTRY_PASSWORD` | 镜像仓库的 RAM 密码 |

## ECS self-hosted runner 的 `deploy/` 目录

首次部署前要准备的本地文件:

| 文件 | 作用 |
|------|------|
| `.env` | 应用凭证 + 镜像仓库拉取凭证。**至少** 含 `REGISTRY_USERNAME` / `REGISTRY_PASSWORD` / `REGISTRY_HOST` / `REGISTRY_NAMESPACE` / `IMAGE_NAME` |
| `docker-compose.yml` | 由 deploy job 自动从仓库同步过来,**不需要**在 ECS 上手维护 |

首次 deploy 会从仓库 `.env.example` 复制成 `.env`,之后**手动填真实凭证**才能正常启动。

## 自定义步骤

复用到新项目时:

1. 复制 `deploy.yml` 到目标仓库的 `.github/workflows/deploy.yml`
2. 配好上面的 Vars + Secrets(11 + 2)
3. ECS 上的 self-hosted runner 装好、注册好,**标签**必须命中 `RUNNER_LABELS` 里写的那一组
4. base 镜像(golang / alpine / 任何外部依赖镜像)已经 sync 到 `${REGISTRY_NAMESPACE}`
5. 项目里要有 `Dockerfile`、`docker-compose.yml`、`.env.example`,再加 `entrypoint.sh`(可选,看主程序是否需要非 root 用户写日志目录)
6. push 代码即触发整个 build + deploy 流水线

## 镜像 tag 约定

| Tag | 类型 | 用途 |
|-----|------|------|
| `${IMAGE_NAME}-<sha7>` | immutable | 精确版本,由 build job 打 |
| `${IMAGE_NAME}-latest` | mutable 指针 | deploy job 拉这个 |

ECS 本地 re-tag 成 `${IMAGE_NAME}:server-latest`,供 `docker-compose.yml` 的 `image:` 引用。

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
