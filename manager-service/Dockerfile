FROM golang:1.21-alpine AS builder

# 代理配置（可选，仅用于构建时，不会保留在最终镜像中）
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG http_proxy
ARG https_proxy
ARG NO_PROXY
ARG no_proxy

# 配置 Go 代理到国内源
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN VERSION="$(cat VERSION 2>/dev/null || echo dev)" && \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-X github.com/sandbox/manager/internal/app.version=${VERSION}" \
      -o manager ./cmd/manager

FROM ubuntu:22.04

# 代理配置（可选，用于构建时，不会保留在最终镜像中）
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG http_proxy
ARG https_proxy
ARG NO_PROXY
ARG no_proxy

# 先使用默认源安装 ca-certificates（构建期可使用代理；不写入 ENV，避免保留到运行时）
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# 配置清华 apt 源
RUN echo "deb https://mirrors.tuna.tsinghua.edu.cn/ubuntu/ jammy main restricted universe multiverse" > /etc/apt/sources.list && \
    echo "deb https://mirrors.tuna.tsinghua.edu.cn/ubuntu/ jammy-updates main restricted universe multiverse" >> /etc/apt/sources.list && \
    echo "deb https://mirrors.tuna.tsinghua.edu.cn/ubuntu/ jammy-backports main restricted universe multiverse" >> /etc/apt/sources.list && \
    echo "deb https://mirrors.tuna.tsinghua.edu.cn/ubuntu/ jammy-security main restricted universe multiverse" >> /etc/apt/sources.list

WORKDIR /app
COPY --from=builder /build/manager .

# 创建非 root 用户（数字 UID）
RUN groupadd -g 10001 manager && useradd -u 10001 -g 10001 -s /bin/false manager && \
    chown -R manager:manager /app

USER 10001
EXPOSE 8080
CMD ["./manager"]
