# ============================================================================
# JaySQL 镜像（多阶段构建）
#
#   阶段 web    : 构建前端 + 文档中心正文（docs/manual/**/*.md → manifest.json）
#   阶段 server : 编译静态二进制（前端产物通过 go:embed 打进二进制）
#   阶段 final  : 精简运行时镜像，非 root 运行
#
# 放置位置：仓库根目录下的 docker/Dockerfile
# 构建上下文必须是「仓库根目录」，Dockerfile 里用不了上级目录，所以：
#   docker build -f docker/Dockerfile -t jaysql:1.99 .
#
# 产物是 Linux 容器（linux/amd64 或 linux/arm64）。Windows / macOS 通过
# Docker Desktop 运行同一个镜像，不需要、也没有 Windows 容器版本。
# ============================================================================

ARG NODE_IMAGE=node:22-alpine
ARG GO_IMAGE=golang:1.23-alpine
ARG RUNTIME_IMAGE=alpine:3.20

# ---------------------------------------------------------------------------
# 阶段 1：前端 + 文档中心
# ---------------------------------------------------------------------------
# --platform=$BUILDPLATFORM：前端产物是静态资源，与目标架构无关，
# 固定在构建机原生架构跑，多平台构建时两个平台共享同一份结果（省一半时间）。
FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web
WORKDIR /src

# 国内构建可传 --build-arg NPM_REGISTRY=https://registry.npmmirror.com
ARG NPM_REGISTRY=
RUN if [ -n "$NPM_REGISTRY" ]; then npm config set registry "$NPM_REGISTRY"; fi

# 为什么要分两次 COPY：
# www/scripts/build-docs.mjs 用 path.resolve(www/scripts, '../..') 反推仓库根，
# 再从 <仓库根>/docs/manual 读 Markdown 并把图片内联成 data URI。
# 因此 www 与 docs 必须保持仓库里的相对位置，不能拆开复制到别的层级。
COPY www/package.json www/package-lock.json ./www/
RUN cd www && npm ci --no-audit --no-fund

COPY docs ./docs
COPY www ./www

# package.json 的 prebuild 会自动先执行 scripts/build-docs.mjs，再执行 vite build
RUN cd www && npm run build

# ---------------------------------------------------------------------------
# 阶段 2：后端
# ---------------------------------------------------------------------------
# --platform=$BUILDPLATFORM：用构建机原生架构的 Go 工具链，靠 GOARCH 交叉编译
# 出目标平台二进制。不这么做的话，arm64 镜像要在 amd64 runner 上走 QEMU 模拟
# 执行 go build，慢 5~10 倍且容易把内存吃满被 OOM kill。
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS server
WORKDIR /src

# Go 1.23.12 项目；CGO_ENABLED=0 已验证可编译（产物为静态链接 ELF）。
# 仓库里所有数据库驱动都是纯 Go 实现（pgx / go-ora / go-mssqldb /
# clickhouse-go / mongo-driver），因此不需要 gcc，镜像不必带构建工具链。
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0 GOOS=linux

RUN apk add --no-cache git ca-certificates

# 先只拷依赖清单：改业务代码时这一层仍能命中缓存
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend ./

# backend/web/dist 在 git 里只保留 index.html 占位（.gitignore 排除了其余文件），
# 必须用真实构建产物整体覆盖，否则页面能打开但 JS/CSS 全部 404。
RUN rm -rf ./web/dist
COPY --from=web /src/www/dist ./web/dist

ARG VERSION=dev
# TARGETARCH 由 BuildKit 注入（docker build --platform 或 buildx 时自动带）；
# 老版 builder 不注入，用 go env GOHOSTARCH 兜底，避免 GOARCH 为空导致报错。
ARG TARGETARCH
RUN ARCH="${TARGETARCH:-$(go env GOHOSTARCH)}" \
 && GOARCH="$ARCH" go build \
      -trimpath \
      -ldflags "-s -w -X main.Version=${VERSION}" \
      -o /out/jaysql ./cmd \
 && GOARCH="$ARCH" go build \
      -trimpath \
      -ldflags "-s -w" \
      -o /out/backup-agent ./internal/backup/agentcmd \
 && ls -lh /out

# ---------------------------------------------------------------------------
# 阶段 3：运行时
# ---------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE}

# tzdata         : alpine 默认没有时区库，缺了 TZ 不生效，日志时间会差 8 小时
# openssh-client : 备份/恢复流程会调用 scp（x/crypto/ssh 走库，但 scp 走系统命令）
RUN apk add --no-cache ca-certificates tzdata openssh-client \
 && addgroup -g 10001 jaysql \
 && adduser  -D -H -u 10001 -G jaysql -h /app -s /sbin/nologin jaysql

# HOME 必须是可写目录：内置 gh-ost 运行时释放到 os.UserCacheDir()
# （即 $HOME/.cache/goinsight/gh-ost），不可写会直接导致 DDL 工单失败。
ENV TZ=Asia/Shanghai \
    HOME=/app

WORKDIR /app

COPY --from=server /out/jaysql /app/jaysql
# 备份 agent 必须和主程序放在同一目录：代码按 GOINSIGHT_AGENT_BINARY →
# /usr/local/bin/backup-agent → <主程序目录>/backup-agent → ./backup-agent 的顺序找它。
# 缺了这个文件，「给节点一键部署 agent」会报「未找到可用于下发的 backup-agent 二进制」。
COPY --from=server /out/backup-agent /app/backup-agent
COPY backend/config.yaml.template /app/config.yaml.template

# 三个需要持久化/可写的目录：
#   /app/media  —— 用户上传的 Logo（代码里是相对路径 ./media）
#   /app/logs   —— 应用日志，对应 config.yaml 的 log.root_dir
#   /app/.cache —— 内置 gh-ost 的释放目录
RUN mkdir -p /app/media /app/logs /app/.cache \
 && chown -R 10001:10001 /app

USER 10001
EXPOSE 8083

# 应用没有 /health 端点，用内嵌静态资源探活。
# start-period 给到 120s：首次启动要 AutoMigrate 约 50 张表并写种子数据。
HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8083/favicon.ico || exit 1

ENTRYPOINT ["/app/jaysql", "-config", "/app/config.yaml"]
