# syntax=docker/dockerfile:1
# JSON-PING multi-arch image: linux/amd64, linux/arm64, linux/arm/v7
# Go 交叉编译在构建机原生架构上完成，不走 QEMU 模拟，速度快。

ARG GO_VERSION=1.26          # 与 go.mod 保持一致

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
WORKDIR /src
COPY . .
# TARGETVARIANT 形如 v7 → GOARM=7；amd64/arm64 时为空，无影响
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -tags timetzdata \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/json-ping . \
 && mkdir -p /out/data

FROM scratch
# HTTPS 告警（webhook）需要 CA 证书；时区数据已通过 timetzdata 编进二进制
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/json-ping /json-ping
# scratch 里无法 mkdir/chown，数据目录在构建阶段建好再带权限拷入
COPY --from=build --chown=65532:65532 /out/data /data

VOLUME /data
EXPOSE 8080
USER 65532:65532

# scratch 没有 curl/wget，需要二进制自带 healthcheck 子命令
HEALTHCHECK --interval=30s --timeout=3s CMD ["/json-ping", "healthcheck"]

ENTRYPOINT ["/json-ping"]
CMD ["-data", "/data", "-listen", ":8080"]
