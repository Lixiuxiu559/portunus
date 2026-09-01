# 基础镜像源：默认走 DaoCloud 国内加速；海外/直连顺畅时可用
#   --build-arg BASE_REGISTRY=library   （走官方 docker.io）
ARG BASE_REGISTRY=docker.m.daocloud.io/library

# BUILDPLATFORM=构建机平台：Go 支持交叉编译，构建阶段始终用原生平台跑，
# 目标架构通过 TARGETARCH 传给 go build，避免 QEMU 模拟
FROM --platform=$BUILDPLATFORM ${BASE_REGISTRY}/golang:1.26-alpine AS builder

# 国内网络直连 proxy.golang.org 经常超时，构建时可用 --build-arg GOPROXY 覆盖
ARG GOPROXY=https://goproxy.cn,direct
ARG TARGETARCH

WORKDIR /app
COPY go.mod go.sum ./

# macOS 系统代理会被自动注入为 build-arg，需在联网前 unset 掉，防止走死代理
RUN unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy NO_PROXY no_proxy \
  && go env -w GOPROXY="${GOPROXY}" \
  && go mod download

COPY . .
RUN unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy NO_PROXY no_proxy \
  && CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -o portunus .

FROM ${BASE_REGISTRY}/alpine:3.21
# 替换 apk 源为国内镜像 + unset 注入的代理，避免 dl-cdn.alpinelinux.org 超时
RUN unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy NO_PROXY no_proxy \
  && sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
  && apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/portunus .
EXPOSE 3060
VOLUME /app/data
CMD ["./portunus"]