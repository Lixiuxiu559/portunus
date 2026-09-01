# 基础镜像源：默认走 DaoCloud 国内加速；海外/直连顺畅时可用
#   --build-arg BASE_REGISTRY=library   （走官方 docker.io）
ARG BASE_REGISTRY=docker.m.daocloud.io/library

# ─── 构建阶段：Node 构建前端静态产物 ───
# Vite 产物与架构无关，构建机平台直接跑即可
FROM --platform=$BUILDPLATFORM ${BASE_REGISTRY}/node:22-alpine AS builder

# 国内网络直连 npm registry 经常超时，这里可用 --build-arg NPM_REGISTRY 覆盖
ARG NPM_REGISTRY=https://registry.npmmirror.com

WORKDIR /app
COPY package.json pnpm-lock.yaml ./

# 注意：macOS 系统代理（如未启动的 clash 127.0.0.1:7890）会被自动注入为 build-arg，
# 且优先级高于 ENV 空值赋值，因此必须在联网前 unset 掉，否则 npm/pnpm 会尝试走死代理。
# npmmirror 为国内源，直连即可，无需代理。
RUN unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy NO_PROXY no_proxy \
  && npm config set registry "${NPM_REGISTRY}" \
  && npm install -g pnpm@9 \
  && pnpm config set registry "${NPM_REGISTRY}" \
  && pnpm install --frozen-lockfile

COPY . .
RUN unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy NO_PROXY no_proxy \
  && pnpm run build:web

# ─── 运行阶段：nginx 托管静态 + 反代后端 ───
FROM ${BASE_REGISTRY}/nginx:1.27-alpine
COPY --from=builder /app/dist/renderer /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80