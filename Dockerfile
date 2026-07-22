# ============================================================
# 生产镜像
# 多阶段构建: builder (Go 编译) → runtime (alpine 运行)
# ============================================================

# base 镜像源 + 版本号通过 --build-arg 传入,默认指向个人 ACR
ARG REGISTRY=crpi-6j67kctprgon1bvt.cn-guangzhou.personal.cr.aliyuncs.com/lhb-ns/lhb-hub
ARG GOLANG_TAG=golang-1.25-alpine
ARG ALPINE_TAG=alpine-3.21

# ============================================================
# stage 1: builder
# ============================================================
FROM ${REGISTRY}:${GOLANG_TAG} AS builder

ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.org
ENV GOTOOLCHAIN=local

WORKDIR /src

# 单独 COPY go.mod / go.sum 最大化缓存命中
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

# CGO 关掉,产物是静态二进制,无需在 alpine 里再装 libc
ENV GOCACHE=/root/.cache/go-build
ENV GOMODCACHE=/go/pkg/mod
RUN mkdir -p /root/.cache/go-build /go/pkg/mod && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -trimpath -o /out/local-life-api .

# ============================================================
# stage 2: runtime
# ============================================================
# ARG 不会跨阶段传递,这里必须重新声明
ARG REGISTRY
ARG ALPINE_TAG
FROM ${REGISTRY}:${ALPINE_TAG}

# ca-certificates: 调 https API(微信/腾讯地图/OSS)需要
# tzdata:        让日志时间戳对齐 Asia/Shanghai
RUN apk --no-cache add ca-certificates tzdata
ENV TZ=Asia/Shanghai

WORKDIR /app

COPY --from=builder /out/local-life-api .
COPY etc/mini-api.yaml /app/etc/mini-api.yaml

# uid/gid 固定为 1000,跟 entrypoint.sh 的 app 用户对得上
RUN addgroup -g 1000 app && adduser -u 1000 -G app -D app && \
    mkdir -p /data/logs/mini-api && \
    chown -R app:app /app /data/logs/mini-api

COPY --chown=root:root entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8888

ENTRYPOINT ["/entrypoint.sh"]
