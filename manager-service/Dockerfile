ARG GO_BASE_IMAGE=golang:1.25-alpine
ARG RUNTIME_BASE_IMAGE=ubuntu:22.04
FROM ${GO_BASE_IMAGE} AS builder

# 代理配置（可选，仅用于构建时，不会保留在最终镜像中）
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG http_proxy
ARG https_proxy
ARG NO_PROXY
ARG no_proxy

ARG VERSION=dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-X github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/app.version=${VERSION}" \
      -o asbcp ./cmd/asbcp

FROM ${RUNTIME_BASE_IMAGE}

ARG VERSION=dev
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="AgentSmith Sandbox Control Plane" \
      org.opencontainers.image.description="ASBCP sandbox workload lifecycle service" \
      org.opencontainers.image.source="https://github.com/agentsmith-project/agentsmith-sandbox-control-plane" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.licenses="Apache-2.0"

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

WORKDIR /app
COPY --from=builder /build/asbcp .

# 创建非 root 用户（数字 UID）
RUN groupadd -g 10001 asbcp && useradd -u 10001 -g 10001 -s /bin/false asbcp && \
    chown -R asbcp:asbcp /app

USER 10001
EXPOSE 8080
CMD ["./asbcp"]
