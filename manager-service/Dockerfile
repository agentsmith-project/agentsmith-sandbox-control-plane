ARG GO_BASE_IMAGE=golang:1.25-alpine
ARG RUNTIME_BASE_IMAGE=ubuntu:22.04
FROM ${GO_BASE_IMAGE} AS builder

# Optional build-only proxy args. Defaults stay empty for public release builds.
ARG ASBCP_BUILD_HTTP_PROXY=""
ARG ASBCP_BUILD_HTTPS_PROXY=""
ARG ASBCP_BUILD_NO_PROXY=""

ARG VERSION=dev

WORKDIR /build
COPY go.mod go.sum ./
RUN export HTTP_PROXY="${ASBCP_BUILD_HTTP_PROXY}" HTTPS_PROXY="${ASBCP_BUILD_HTTPS_PROXY}" http_proxy="${ASBCP_BUILD_HTTP_PROXY}" https_proxy="${ASBCP_BUILD_HTTPS_PROXY}" NO_PROXY="${ASBCP_BUILD_NO_PROXY}" no_proxy="${ASBCP_BUILD_NO_PROXY}" && \
    go mod download

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

# Optional build-only proxy args. Defaults stay empty for public release builds.
ARG ASBCP_BUILD_HTTP_PROXY=""
ARG ASBCP_BUILD_HTTPS_PROXY=""
ARG ASBCP_BUILD_NO_PROXY=""

# Install ca-certificates with the default upstream source; proxy is opt-in and build-only.
RUN export HTTP_PROXY="${ASBCP_BUILD_HTTP_PROXY}" HTTPS_PROXY="${ASBCP_BUILD_HTTPS_PROXY}" http_proxy="${ASBCP_BUILD_HTTP_PROXY}" https_proxy="${ASBCP_BUILD_HTTPS_PROXY}" NO_PROXY="${ASBCP_BUILD_NO_PROXY}" no_proxy="${ASBCP_BUILD_NO_PROXY}" && \
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
