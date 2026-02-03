FROM ubuntu:22.04

# 代理配置（可选，仅用于构建时，不会保留在最终镜像中）
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

# 使用清华源更新并安装包（构建期可使用代理）
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
    python3 \
    curl jq coreutils \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# 下载并安装 kubectl（在 Dockerfile 中直接下载，不依赖构建脚本）
ARG KUBECTL_VERSION=v1.29.0
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    curl -fL -o kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
    && chmod +x kubectl \
    && mv kubectl /usr/local/bin/kubectl \
    && kubectl version --client

COPY gc.sh /gc.sh
RUN chmod +x /gc.sh

ENTRYPOINT ["/gc.sh"]
