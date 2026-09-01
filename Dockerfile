# 基础镜像源：默认走 DaoCloud 国内加速；海外/直连顺畅时可用
#   --build-arg BASE_REGISTRY=library   （走官方 docker.io）
ARG BASE_REGISTRY=docker.m.daocloud.io/library

# ─── 后端构建：Go 交叉编译（BUILDPLATFORM 原生跑构建，不用 QEMU）───
FROM --platform=$BUILDPLATFORM ${BASE_REGISTRY}/golang:1.26-alpine AS go-builder

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

# ─── 前端构建：Node + Vite（产物与架构无关）───
FROM --platform=$BUILDPLATFORM ${BASE_REGISTRY}/node:22-alpine AS web-builder

ARG NPM_REGISTRY=https://registry.npmmirror.com

WORKDIR /app
COPY web/package.json web/pnpm-lock.yaml ./

RUN unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy NO_PROXY no_proxy \
  && npm config set registry "${NPM_REGISTRY}" \
  && npm install -g pnpm@9 \
  && pnpm config set registry "${NPM_REGISTRY}" \
  && pnpm install --frozen-lockfile

COPY web/ .
RUN unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy NO_PROXY no_proxy \
  && pnpm run build:web

# ─── 运行时：nginx 托管前端 + 反代 /api /v1 到本机后端 ───
FROM ${BASE_REGISTRY}/nginx:1.27-alpine

# 替换 apk 源为国内镜像 + unset 注入的代理，避免 dl-cdn.alpinelinux.org 超时
RUN unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy NO_PROXY no_proxy \
  && sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
  && apk add --no-cache ca-certificates tzdata

# 后端二进制
COPY --from=go-builder /app/portunus /app/portunus
# 前端静态产物
COPY --from=web-builder /app/dist/renderer /usr/share/nginx/html
# nginx：静态托管 + 反代 127.0.0.1:3060（同容器内的后端）
COPY web/nginx.conf /etc/nginx/conf.d/default.conf

# 后端启动脚本：nginx 由主进程（CMD）拉起，后台先起 Go 服务
COPY <<'EOF' /docker-entrypoint.d/40-start-backend.sh
#!/bin/sh
mkdir -p /app/data
PORTUNUS_SERVER_HOST=127.0.0.1 PORTUNUS_SERVER_PORT=3060 PORTUNUS_DATABASE_PATH=/app/data/portunus.db /app/portunus &
EOF
RUN chmod +x /docker-entrypoint.d/40-start-backend.sh

EXPOSE 80
VOLUME /app/data
CMD ["nginx", "-g", "daemon off;"]