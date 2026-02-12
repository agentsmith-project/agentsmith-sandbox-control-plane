FROM ubuntu:22.04 AS base

# 代理配置（可选，仅用于构建时，不会保留在最终镜像中）
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG http_proxy
ARG https_proxy
ARG NO_PROXY
ARG no_proxy

# 定义版本变量，便于更新和维护
ARG NODE_VERSION=20.18.0
ARG RIPGREP_VERSION=14.1.0
ARG FD_VERSION=9.0.0
ARG BAT_VERSION=0.24.0

# 先使用默认源安装 ca-certificates（支持 HTTPS）
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*

# 配置清华 apt 源 - 直接重写 sources.list
RUN echo "deb https://mirrors.tuna.tsinghua.edu.cn/ubuntu/ jammy main restricted universe multiverse" > /etc/apt/sources.list && \
    echo "deb https://mirrors.tuna.tsinghua.edu.cn/ubuntu/ jammy-updates main restricted universe multiverse" >> /etc/apt/sources.list && \
    echo "deb https://mirrors.tuna.tsinghua.edu.cn/ubuntu/ jammy-backports main restricted universe multiverse" >> /etc/apt/sources.list && \
    echo "deb https://mirrors.tuna.tsinghua.edu.cn/ubuntu/ jammy-security main restricted universe multiverse" >> /etc/apt/sources.list

# 使用清华源更新并安装系统包（分层安装以提高缓存效率）
# 第一层：基础工具和依赖（很少变化）
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    apt-get update && apt-get install -y --no-install-recommends \
    # 基础工具
    curl ca-certificates gnupg wget \
    # 系统管理工具
    bash bash-completion zsh tar coreutils tini \
    vim nano emacs-nox git htop procps strace lsof tree less \
    grep sed gawk findutils \
    # JSON 处理工具
    jq \
    # 其他实用工具
    parallel \
    # 网络工具
    netcat-openbsd iputils-ping dnsutils telnet openssh-client rsync \
    # 压缩工具
    unzip zip gzip bzip2 xz-utils \
    # 其他实用工具
    file sudo \
    && rm -rf /var/lib/apt/lists/*

# 第二层：开发工具和构建依赖（较少变化）
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    apt-get update && apt-get install -y --no-install-recommends \
    # Python 开发工具
    python3 python3-venv python3-pip python3-dev \
    # C/C++ 开发工具
    build-essential gcc g++ make cmake pkg-config libc6-dev \
    # Autotools (autoconf, automake, libtool)
    autoconf automake libtool m4 gettext \
    # 其他构建工具
    bison flex patch diffutils \
    && rm -rf /var/lib/apt/lists/*

# 第三层：系统库和字体（较少变化）
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    apt-get update && apt-get install -y --no-install-recommends \
    # OpenCV 系统依赖
    libgl1-mesa-glx libglib2.0-0 libsm6 libxext6 libxrender-dev libgomp1 \
    # 图像处理库
    libjpeg-dev libpng-dev libtiff-dev libopenexr-dev \
    # PostgreSQL 客户端库（用于 psycopg2）
    libpq-dev \
    # Redis CLI
    redis-tools \
    # 中文字体支持
    fonts-noto-cjk fonts-noto-cjk-extra \
    fonts-wqy-microhei fonts-wqy-zenhei \
    fontconfig \
    && rm -rf /var/lib/apt/lists/* && \
    # 更新字体缓存
    fc-cache -fv

# 配置清华 pip 源（为 root 和未来用户配置）
RUN mkdir -p /root/.pip /etc/pip && \
    echo "[global]" > /root/.pip/pip.conf && \
    echo "index-url = https://pypi.tuna.tsinghua.edu.cn/simple" >> /root/.pip/pip.conf && \
    echo "trusted-host = pypi.tuna.tsinghua.edu.cn" >> /root/.pip/pip.conf && \
    echo "[global]" > /etc/pip/pip.conf && \
    echo "index-url = https://pypi.tuna.tsinghua.edu.cn/simple" >> /etc/pip/pip.conf && \
    echo "trusted-host = pypi.tuna.tsinghua.edu.cn" >> /etc/pip/pip.conf

# 全局安装 uv (Python 包管理器) - 单独层以便缓存
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    pip3 install --no-cache-dir uv

# 安装现代命令行工具（ripgrep, fd, bat）- 下载到文件再解压，避免 curl|tar 管道截断导致损坏；带重试
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    CURL_OPTS="-fSL --retry 3 --retry-delay 2 --retry-max-time 300" && \
    curl $CURL_OPTS -o /tmp/rg.tar.gz "https://github.com/BurntSushi/ripgrep/releases/download/${RIPGREP_VERSION}/ripgrep-${RIPGREP_VERSION}-x86_64-unknown-linux-musl.tar.gz" && \
    tar -xz -C /tmp -f /tmp/rg.tar.gz && \
    cp /tmp/ripgrep-${RIPGREP_VERSION}-x86_64-unknown-linux-musl/rg /usr/local/bin/ && chmod +x /usr/local/bin/rg && \
    curl $CURL_OPTS -o /tmp/fd.tar.gz "https://github.com/sharkdp/fd/releases/download/v${FD_VERSION}/fd-v${FD_VERSION}-x86_64-unknown-linux-musl.tar.gz" && \
    tar -xz -C /tmp -f /tmp/fd.tar.gz && \
    cp /tmp/fd-v${FD_VERSION}-x86_64-unknown-linux-musl/fd /usr/local/bin/ && chmod +x /usr/local/bin/fd && \
    curl $CURL_OPTS -o /tmp/bat.tar.gz "https://github.com/sharkdp/bat/releases/download/v${BAT_VERSION}/bat-v${BAT_VERSION}-x86_64-unknown-linux-musl.tar.gz" && \
    tar -xz -C /tmp -f /tmp/bat.tar.gz && \
    cp /tmp/bat-v${BAT_VERSION}-x86_64-unknown-linux-musl/bat /usr/local/bin/ && chmod +x /usr/local/bin/bat && \
    rm -rf /tmp/rg.tar.gz /tmp/fd.tar.gz /tmp/bat.tar.gz /tmp/ripgrep-* /tmp/fd-* /tmp/bat-*

# 安装 Node.js LTS 版本（直接下载到文件再解压，带重试）
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    curl -fSL --retry 3 --retry-delay 2 --retry-max-time 600 -o /tmp/node.tar.xz "https://npmmirror.com/mirrors/node/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" && \
    tar -xJ -C /usr/local --strip-components=1 -f /tmp/node.tar.xz && rm -f /tmp/node.tar.xz && \
    # 配置 npm 使用清华源
    npm config set registry https://registry.npmmirror.com && \
    # 全局安装常用 Node.js 开发工具
    npm install -g yarn pnpm typescript ts-node @types/node eslint prettier && \
    # 全局安装文档处理工具包
    npm install -g \
    # Word 文档处理
    docx mammoth \
    # Excel 文档处理
    exceljs xlsx \
    # PDF 处理
    pdf-lib \
    # Markdown 和 HTML 处理
    marked cheerio \
    # 其他常用工具
    axios lodash moment && \
    npm cache clean --force

# 安装常用的科研和数据分析 Python 包
# 分层安装以提高缓存效率：先安装基础包，再安装大型框架
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    pip3 install --no-cache-dir \
    # 核心科学计算库
    numpy scipy pandas \
    # 数据可视化
    matplotlib seaborn plotly bokeh \
    # Jupyter 生态系统
    jupyter jupyterlab ipython notebook \
    # 机器学习
    scikit-learn scikit-image \
    # 计算机视觉
    opencv-python opencv-contrib-python \
    # 图像处理
    pillow imageio \
    # 统计分析
    statsmodels \
    # 符号计算
    sympy \
    # 网络分析
    networkx \
    # 进度条
    tqdm \
    # 配置文件处理
    pyyaml toml \
    # HTTP 请求
    requests httpx aiohttp \
    # 数据处理
    xarray dask \
    # 数据库
    sqlalchemy pymongo \
    # PostgreSQL 客户端
    psycopg2-binary \
    # Redis 客户端
    redis \
    # 时间处理
    python-dateutil pytz \
    # Web 框架
    flask fastapi django \
    # 其他常用工具
    click rich typer \
    # 测试工具
    pytest pytest-cov pytest-asyncio pytest-mock \
    # 代码质量
    black flake8 mypy ruff pylint \
    # 类型检查
    types-requests types-pyyaml \
    # 环境管理
    python-dotenv \
    # 日志
    loguru \
    # 异步
    aiofiles \
    # 命令行工具
    invoke \
    # Jira API client
    jira \
    # 文档生成
    sphinx mkdocs python-docx

# 安装深度学习框架（单独层，因为体积大且变化频率不同）
RUN export HTTP_PROXY="${HTTP_PROXY}" HTTPS_PROXY="${HTTPS_PROXY}" http_proxy="${http_proxy}" https_proxy="${https_proxy}" NO_PROXY="${NO_PROXY}" no_proxy="${no_proxy}" && \
    pip3 install --no-cache-dir \
    torch torchvision torchaudio \
    tensorflow

FROM base AS final

# 创建用户和配置（合并操作以减少层数）
RUN groupadd -g 10001 runner && \
    useradd -m -u 10001 -g 10001 runner && \
    # 创建 workspace 目录
    mkdir -p /workspace && \
    chown -R runner:runner /workspace && \
    # 为 runner 用户配置 pip 源
    mkdir -p /home/runner/.pip && \
    echo "[global]" > /home/runner/.pip/pip.conf && \
    echo "index-url = https://pypi.tuna.tsinghua.edu.cn/simple" >> /home/runner/.pip/pip.conf && \
    echo "trusted-host = pypi.tuna.tsinghua.edu.cn" >> /home/runner/.pip/pip.conf && \
    chown -R runner:runner /home/runner/.pip

WORKDIR /workspace

USER runner
ENTRYPOINT ["/usr/bin/tini","--"]
CMD ["sleep","infinity"]
