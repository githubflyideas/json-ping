# syntax=docker/dockerfile:1
# json-ping multi-arch image: linux/amd64, linux/arm64, linux/arm/v7
# Go 在构建机原生架构上交叉编译，不走 QEMU。

# 需 ≥ go.mod 中的 go 版本
ARG GO_VERSION=1.24

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
WORKDIR /src
COPY . .
# TARGETVARIANT 形如 v7 → GOARM=7；amd64/arm64 时为空
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -tags timetzdata \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/json-ping .

# busybox:musl 约 1MB：提供 sh / vi / wget，
# 目标列表就是文本文件，docker exec 进去 vi 即可修改
FROM busybox:1.37-musl

LABEL org.opencontainers.image.title="json-ping" \
      org.opencontainers.image.description="SmokePing-like latency monitor: one binary, plain JSONL files, targets edited in vi" \
      org.opencontainers.image.source="https://github.com/githubflyideas/json-ping"

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/json-ping /usr/local/bin/json-ping

# 程序以工作目录为根：./targets/ 放目标列表，./data/ 放 JSONL。
# 首次启动若没有 targets/ping.list，程序自动生成一个 demo。
RUN mkdir -p /data/targets /data/data && chown -R 65532:65532 /data
WORKDIR /data

ENV TZ=UTC
VOLUME /data
EXPOSE 8517

# 默认非 root；bind mount 时推荐 --user $(id -u):$(id -g) 与宿主目录属主对齐
USER 65532:65532

# /api/version 不经过登录，开启 user=/passwd= 后健康检查依然有效
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
  CMD wget -q -O /dev/null http://127.0.0.1:8517/api/version || exit 1

ENTRYPOINT ["json-ping"]
# 追加参数时整体替换 CMD，例如：
#   docker run ... githubflyideas/json-ping --listen 0.0.0.0:8517 user=admin passwd=admin
CMD ["--listen", "0.0.0.0:8517"]
