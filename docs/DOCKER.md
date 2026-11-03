# JaySQL Docker 部署实施手册

> 目标：用同一个镜像在 **Linux 服务器** 和 **Windows（Docker Desktop）** 上跑起来。
> 前提：**本次不改任何业务代码**，只新增 4 个部署文件（Dockerfile / .dockerignore / docker-compose.yml / config.yaml）。

---

## 0. 先明确三件事，否则后面全是坑

### 0.1 只有一种镜像：Linux 容器

Docker 里没有"Windows 版 JaySQL 镜像"。Windows 上跑的是 **Docker Desktop 的 Linux 容器（WSL2 后端）**，跑的还是同一个 `linux/amd64` 镜像。

所以"支持 Windows 和 Linux"的真实含义是：**同一份镜像，两种宿主机环境下的挂载路径、访问宿主机的方式、命令行语法不同**。差异集中在第 8 章，镜像本身完全一致。

### 0.2 容器化后，哪些能力成立、哪些要绕开

| 能力 | 容器内 | 说明 |
|---|---|---|
| SQL 工作台、SQL 审核、工单审批 | ✅ | 纯网络调用，与部署形态无关 |
| gh-ost 在线改表 | ✅ | gh-ost 已 go:embed 进二进制，运行时释放到 `$HOME/.cache`，**要求 HOME 可写**（Dockerfile 已处理） |
| 备份 / 恢复（备份节点 = 远程 SSH 机器） | ✅ | 走 Go 的 SSH 库 + `scp`，镜像已装 openssh-client |
| 备份节点「一键部署 agent」 | ✅ | 镜像已带 `backup-agent`，放在主程序同目录（`/app/backup-agent`） |
| 备份 / 恢复（备份节点 = 本机 localhost） | ⚠️ | 容器里没有 `mysqldump` / `xtrabackup`，且流程要切换到数据库进程用户（如 `mysql`）执行。**容器部署请一律用远程 SSH 节点**，详见 §11 |
| 监控引擎层指标采集 | ✅ | 直连目标库 |
| 监控主机层指标（Prometheus） | ✅ | 地址必须改，见 §6 |
| 慢 SQL 采集 | ✅ | 直连目标库 |
| 多实例 / 水平扩容 | ❌ | 工单执行锁与事件总线是**进程内**实现，系统本身就只支持单实例。容器化不改变这一点，**不要 scale 到 2 个副本** |

### 0.3 仓库里的部署文件

| 文件 | 位置 | 作用 |
|---|---|---|
| `Dockerfile` | `docker/Dockerfile` | 三阶段构建（前端 → 后端 → 运行时） |
| `.dockerignore` | 仓库根目录 | 剪掉构建上下文里的垃圾 |
| `docker-compose.yml` | `docker/docker-compose.yml` | 拉官方镜像启动 |
| `docker-compose.build.yml` | `docker/docker-compose.build.yml` | 本地构建的覆盖层（改代码自测用） |
| `config.yaml.example` | `docker/config.yaml.example` | 配置模板（复制成 `config.yaml`） |
| `docker.yml` | `.github/workflows/` | 推 `main` / `v*` 标签时自动构建双架构镜像并推 GHCR |

> `docker/config.yaml` 本身含真实密码，**不要提交**。仓库 `.gitignore` 里已有的 `config.yaml` 规则会覆盖到子目录，无需额外加。

---

## 1. 部署形态选型

```
形态 A（推荐，生产）—— 容器 + 外部已有 MySQL
┌──────────────┐        ┌────────────────────┐
│ 浏览器        │──8083─▶│ jaysql 容器         │
└──────────────┘        │  (linux/amd64)     │
                        └─────┬────────┬─────┘
                              │        │
                    3306      │        │ SSH 22
                        ┌─────▼────┐  ┌▼──────────────┐
                        │ 元数据库  │  │ 被管理的数据库 │
                        │ MySQL 8   │  │ （备份/监控）  │
                        └──────────┘  └───────────────┘

形态 B（试用/单机）—— 元数据库也进容器
┌──────────────┐        ┌──────────────┐   ┌──────────────┐
│ 浏览器        │──8083─▶│ jaysql       │──▶│ mysql 容器    │
└──────────────┘        └──────────────┘   │ (compose 托管)│
                                            └──────────────┘
```

差异只有一个：`config.yaml` 里 `database.host` 填 `mysql`（服务名）还是真实 IP。

---

## 2. 前置条件

| 项 | 要求 | 检查命令 |
|---|---|---|
| Docker Engine | ≥ 20.10 | `docker version` |
| Docker Compose | v2（`docker compose` 子命令，不是 `docker-compose`） | `docker compose version` |
| 磁盘 | ≥ 3 GB（构建缓存 ~1.5 GB；运行镜像约 175 MB） | — |
| 内存 | ≥ 2 GB 可用 | — |
| 端口 | 8083 未被占用 | Linux `ss -lntp \| grep 8083` / Windows `netstat -ano \| findstr :8083` |
| 元数据库 | MySQL 5.7 / 8.0，库已建，账号有 DDL 权限 | — |
| 构建网络 | 需要能访问 npm registry 与 Go module proxy（离线环境见 §12） | — |

Windows 额外一条：Docker Desktop 设置里必须启用 **Use the WSL 2 based engine**（默认已启用）。若未安装 WSL2，先 `wsl --install` 并重启。

---

## 3. 目录规划

### Linux（推荐 `/opt/jaysql`）

```bash
/opt/jaysql/
├── src/                     # git clone 的源码
│   ├── docker/
│   │   ├── Dockerfile
│   │   ├── docker-compose.yml
│   │   ├── config.yaml      # 从 example 复制，含密码，权限 600
│   │   └── config.yaml.example
│   ├── .dockerignore
│   ├── backend/
│   ├── www/
│   └── docs/
└── backup/                  # 元库定期备份落这里（自己定）
```

### Windows（推荐 `D:\jaysql`）

```
D:\jaysql\
├── src\
│   ├── docker\
│   │   ├── Dockerfile
│   │   ├── docker-compose.yml
│   │   ├── config.yaml
│   │   └── config.yaml.example
│   ├── .dockerignore
│   └── ...
└── backup\
```

> **构建速度提醒（Windows）**：源码放在 `D:\` 这类 NTFS 盘上，Docker 构建时文件同步会明显偏慢。想快就把源码放到 WSL2 文件系统里（`\\wsl$\Ubuntu\home\<user>\jaysql`），或直接 `docker build` 时接受第一次慢、后续靠层缓存。

---

## 4. 步骤一：把 4 个部署文件放进仓库

```bash
cd /opt/jaysql/src
mkdir -p docker
# 把 Dockerfile / docker-compose.yml / config.yaml.example 放进 docker/
# 把 .dockerignore 放到仓库根目录（和 .gitignore 同级）
```

确认关键位置没放错——`Dockerfile` 里的 `COPY www/...`、`COPY backend/...`、`COPY docs/...` 都是相对**仓库根**解析的：

```bash
ls -1 .dockerignore docker/Dockerfile docker/docker-compose.yml
# 三个都要存在
```

⚠️ **目录名必须是 `docker`**。`docker-compose.yml` 里写的是 `dockerfile: docker/Dockerfile`，这个路径相对**构建上下文（仓库根）**解析，不是相对 compose 文件所在目录。想把目录叫别的名字，就得同步把那一行和 `context: ..` 一起改掉；改错的表现是 `failed to read dockerfile: open Dockerfile: no such file or directory`。

---

## 5. 步骤二：准备元数据库

### 5.1 用外部已有 MySQL（形态 A）

在目标 MySQL 上执行：

```sql
CREATE DATABASE IF NOT EXISTS jaysql
  DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;

-- host 段按容器网段放开；'%' 最省事，但建议限定到 Docker 网段
CREATE USER 'jaysql'@'%' IDENTIFIED BY '换成强密码';
GRANT ALL PRIVILEGES ON jaysql.* TO 'jaysql'@'%';
FLUSH PRIVILEGES;
```

**只要一个空库 + DDL 权限，不要手工灌表结构。** 首次启动会 `AutoMigrate` 自动建约 50 张表，并写入种子数据（管理员、角色权限、279 条审核规则、监控指标模板、LLM 厂商模板、系统参数）。

`backend/sql/*.sql` 是**存量环境的演进/订正脚本**，新部署不用跑。

### 5.2 用 compose 自带的 MySQL（形态 B）

跳过本节，直接在 `.env` 里设密码（见 §7.2）。

---

## 6. 步骤三：写 `docker/config.yaml`

```bash
cd /opt/jaysql/src/docker
cp config.yaml.example config.yaml
chmod 600 config.yaml
# 生成一个 32 字符的 secret_key
openssl rand -hex 16
```

把生成值填进 `secret_key`。然后**逐项确认这 7 处**（`config.yaml.example` 里都标了「容器差异」）：

| 配置项 | 容器里的正确值 | 写错的后果 |
|---|---|---|
| `app.listen_address` | `0.0.0.0:8083` | 写 `127.0.0.1` → 端口映射进不来，宿主机 curl 直接 refused，日志毫无异常 |
| `app.secret_key` | 32 字符随机串 | 换值 = 所有人掉线 + 库里已加密的密码解不开 |
| `log.root_dir` | `/app/logs` | 没挂卷 → 重启日志全丢 |
| `database.host` | `mysql` 或真实 IP | 填 `127.0.0.1` → 连的是容器自己，报 `connection refused` |
| `monitor.prometheus_url` | 空字符串或真实地址 | 留默认 `127.0.0.1:9090` → 每次采集都超时重试 |
| `notify.notice_url` | 浏览器能访问到的地址 | 留 `127.0.0.1:8083` → 收到钉钉告警的人点开打不开 |
| `ghost.path` | `""`（空） | 写 `/usr/local/bin/gh-ost` 也能跑（会回退内置版），但多一次无谓 stat |

数据库密码就填 §5 里设的那个。这里放的是**元数据库**密码，被管理数据库的密码在页面里录入、落库时 AES 加密，不要写进配置文件。

> 小技巧：`monitor.prometheus_url` 支持用环境变量覆盖，优先级 `GOINSIGHT_PROMETHEUS_URL` > `PROMETHEUS_URL` > 配置文件。所以同一个镜像在测试/生产切换 Prometheus 地址时，不用改配置文件——在 compose 的 `environment` 里加一行就行。

---

## 7. 步骤四：启动

### 7.1 直接拉官方镜像（推荐，不构建）

仓库已配置 GitHub Actions：推 `main` 或 `v*` 标签时会自动构建 `linux/amd64` + `linux/arm64` 双架构镜像并推到 GHCR。用户侧完全不需要 Go / Node 环境：

```bash
docker pull ghcr.io/simajiong/jaysql:latest

# 确认架构自动匹配（amd64 机器应拿到 amd64/linux）
docker image inspect ghcr.io/simajiong/jaysql:latest --format '{{.Architecture}}/{{.Os}}'
```

`docker-compose.yml` 里 `image:` 已经写好了这个地址，直接 `docker compose up -d` 就会拉取，不用加 `--platform`。

> 如果 `docker pull` 提示 `denied` 或要求登录，说明这个包还是 private。仓库维护者需要在
> GitHub → 个人头像 → Packages → jaysql → Package settings 里把可见性改成 **Public**
> （改一次即可，之后所有版本都公开）。用户侧也可临时 `echo $GITHUB_TOKEN | docker login ghcr.io -u <用户名> --password-stdin` 绕过。

### 7.2 本地构建（改完代码自测）

```bash
cd /opt/jaysql/src
docker build -f docker/Dockerfile -t jaysql:1.99 .
```

国内网络加速（两个代理一起给）：

```bash
docker build -f docker/Dockerfile -t jaysql:1.99 \
  --build-arg NPM_REGISTRY=https://registry.npmmirror.com \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  .
```

首次构建约 5–8 分钟（`npm ci` 占大头）。看到下面这行说明后端也编好了：

```
Version:    1.99
```

**验证一下产物架构**（尤其在你的 Mac 上构建、要发给 Linux 用时）：

```bash
docker image inspect jaysql:1.99 --format '{{.Architecture}}/{{.Os}}'
# 期望：amd64/linux 或 arm64/linux
```

Mac（Apple Silicon）上默认构建出来是 `arm64/linux`，要发给 x86 的 Linux 服务器必须显式指定平台：

```bash
docker build --platform linux/amd64 -f docker/Dockerfile -t jaysql:1.99 .
```

### 7.3 启动

**形态 A（外部 MySQL）**

```bash
cd /opt/jaysql/src/docker
docker compose up -d
```

**形态 B（连带 MySQL）**

```bash
cd /opt/jaysql/src/docker
cat > .env <<'EOF'
MYSQL_ROOT_PASSWORD=ChangeMeRootPwd1
MYSQL_PASSWORD=ChangeMeAppPwd1
EOF
docker compose --profile with-db up -d
```

此时 `config.yaml` 的 `database.host` 必须是 `mysql`，`password` 与 `MYSQL_PASSWORD` 一致。

### 7.4 确认启动成功

```bash
docker compose ps
# 期望 jaysql 的 STATUS 里有 (healthy)

docker compose logs -f jaysql
```

日志里应当出现迁移完成、监听 8083。**注意**：首次启动 `AutoMigrate` 期间容器状态是 `starting`，`start_period` 给了 120 秒，别在这个窗口里反复重启。

浏览器打开 `http://<宿主机IP>:8083`，用初始管理员登录：

```
admin / Admin@2026
```

登录后立刻改密码。

---

## 8. Windows 与 Linux 的差异清单

### 8.1 命令行

| 操作 | Linux | Windows (PowerShell) |
|---|---|---|
| 进目录 | `cd /opt/jaysql/src/docker` | `cd D:\jaysql\src\docker` |
| 复制配置 | `cp config.yaml.example config.yaml` | `Copy-Item config.yaml.example config.yaml` |
| 生成密钥 | `openssl rand -hex 16` | `-join ((1..16) \| %{ '{0:x2}' -f (Get-Random -Max 256) })` |
| 看日志 | `docker compose logs -f jaysql` | 同 |
| 查端口 | `ss -lntp \| grep 8083` | `netstat -ano \| findstr :8083` |

### 8.2 访问宿主机上的服务

- **Windows / macOS**：`host.docker.internal` 开箱可用（`docker-compose.yml` 里已加 `extra_hosts`，重复声明也无害）。
- **Linux**：`host.docker.internal` 需要 `extra_hosts: ["host.docker.internal:host-gateway"]` 才有（compose 文件已配）。不想用这个名字，直接把 `database.host` 写成宿主机内网 IP，更直观。

### 8.3 文件换行

Windows 上用记事本编辑 `config.yaml` 会存成 CRLF。YAML 解析器能处理，所以**配置文件不影响**。但如果后续你往镜像里加 `.sh` 脚本，CRLF 会导致 `exec format error`——届时用 VS Code 切成 LF，或在仓库根加 `.gitattributes`：

```
*.sh text eol=lf
```

### 8.4 卷的权限（Linux 才会踩）

compose 用的是**命名卷**，容器内 `/app/media` 等目录由镜像创建、属主已是 UID 10001，不会出问题。

如果你改成 bind mount（想直接在宿主机上看日志），必须先把属主改过来，否则容器内写日志报 `permission denied`：

```yaml
volumes:
  - /opt/jaysql/data/logs:/app/logs
```

```bash
mkdir -p /opt/jaysql/data/{logs,media}
chown -R 10001:10001 /opt/jaysql/data
# SELinux 强制模式（CentOS/RHEL）还要加标签
chcon -Rt container_file_t /opt/jaysql/data   # 或挂载时写 :z
```

Windows 上不要用 bind mount 挂 `/app/logs`，NTFS 权限映射容易出问题，命名卷更省心。看日志用 `docker compose logs` 就够。

### 8.5 防火墙

- Linux（firewalld）：`firewall-cmd --add-port=8083/tcp --permanent && firewall-cmd --reload`
- Windows：Docker Desktop 的端口映射通常自动放行；若外部访问不通，检查 Windows Defender 防火墙的入站规则，以及是否处于「公用网络」配置。

---

## 9. 验证清单（逐条打勾再交付）

```bash
# 1. 容器健康
docker compose ps                              # jaysql (healthy)

# 2. HTTP 可达（宿主机上执行）
curl -I http://127.0.0.1:8083/                  # 200，Content-Type: text/html

# 3. 静态资源正常（防「页面白屏」）
curl -s http://127.0.0.1:8083/ | grep -o 'assets/[^"]*\.js' | head -1
#    拿到文件名后再请求一次，必须 200 而不是 404

# 4. 后端 API 活着（未带 token 应返回 401，不是 500）
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8083/api/v1/profile

# 5. 元库表已建
mysql -h<元库IP> -ujaysql -p jaysql -e "show tables;" | wc -l    # 约 50 行
```

然后进页面把这几件事做一遍：

1. 用 `admin` 登录并改密码
2. 新建一个环境 → 纳管一个测试实例 → 点「连通性测试」通过
3. SQL 工作台执行一条 `select 1`
4. 提一个工单走完审批 → 执行（验证 gh-ost 释放路径可写）
5. 上传一次自定义 Logo，`docker compose restart jaysql` 后 Logo 还在（验证 media 卷持久化）

---

## 10. 升级与回滚

### 升级

```bash
cd /opt/jaysql/src
git pull
# 如果配置文件模板有变化，比对后手工合并，不要直接覆盖 config.yaml
cd docker
docker compose build
docker compose up -d
```

镜像换新、数据卷不动。表结构由启动时的 `AutoMigrate` 自动演进。

**升级前先备份元库**——它是唯一有状态的东西：

```bash
mysqldump -h<元库IP> -ujaysql -p --single-transaction --routines --triggers jaysql \
  > /opt/jaysql/backup/jaysql-$(date +%F-%H%M).sql
```

### 回滚

镜像用**版本号打 tag**（`jaysql:1.99`、`jaysql:2.00`），回滚就是把 compose 里的 tag 改回上一版再 `up -d`：

```bash
docker images | grep jaysql        # 确认旧 tag 还在
# 修改 docker-compose.yml 的 image 与 build.args.VERSION 为旧版本
docker compose up -d
```

⚠️ **回滚有个不可逆风险**：`AutoMigrate` 只增不减，旧版本程序面对新增的列/表通常能跑，但如果新版本做过**数据订正**（不是结构变更），回滚后数据不会自动还原。所以回滚前用上面那条 `mysqldump` 留一份。

---

## 11. 备份能力在容器里的正确用法

这是容器化最容易踩的地方，单独说清。

代码里有两条备份执行路径：

| 路径 | 做法 | 容器里能用吗 |
|---|---|---|
| **远程节点 + SSH 直连**（`backup_node` 指向一台能 SSH 的机器） | 通过 Go 的 SSH 库连过去，在**目标机**上执行 `mysqldump` / `xtrabackup` | ✅ 推荐，容器只需能出 22 端口 |
| **远程节点 + agent 模式** | 把 `backup-agent` 下发到目标机，由它拉任务、上报心跳 | ✅ 镜像已带 `/app/backup-agent`，代码会按「主程序同目录」找到它 |
| **本地节点**（节点就是本机） | 在进程所在机器上 `/bin/sh -lc` 执行，并切换到数据库进程用户（如 `mysql`） | ⚠️ 容器内没有这些工具、也没有该用户 |

**结论：容器部署时，备份节点一律配成远程 SSH 机器（宿主机本身也行，容器 SSH 回宿主机）。**

如果确实需要容器内本地备份，得改镜像（属于镜像定制，不是改业务代码）：

```dockerfile
# 在 runtime 阶段追加
RUN apk add --no-cache mariadb-client postgresql-client bash \
 && addgroup -S mysql && adduser -S -G mysql -h /home/mysql mysql
```

并且要注意：xtrabackup 官方不提供 alpine 包，物理备份基本只能走远程节点或换 `debian:12-slim` 作基础镜像。

### 容器里可用的备份相关环境变量

| 变量 | 作用 |
|---|---|
| `GOINSIGHT_AGENT_BINARY` | 指定要下发到目标机的 `backup-agent` 路径。镜像里已放在 `/app/backup-agent`，一般不用设 |
| `GOINSIGHT_BACKUP_TOOL_HOME` | 备份工具查找目录（本地模式才需要） |
| `GOINSIGHT_BACKUP_PROCESS_MAX_RUNTIME` | 单个备份进程最大运行时长，防挂死 |
| `GOINSIGHT_PROMETHEUS_URL` | 覆盖配置文件里的 `monitor.prometheus_url`（优先级最高） |

---

## 12. 离线 / 内网环境怎么办

内网服务器通常连不了 Docker Hub、npm、Go proxy。两种做法：

### 做法一：直接导出官方镜像（最省事）

内网机器连不上 ghcr.io 时，在一台能联网的机器上拉取后导出：

```bash
# 有网的机器
docker pull --platform linux/amd64 ghcr.io/simajiong/jaysql:latest
docker save ghcr.io/simajiong/jaysql:latest | gzip > jaysql-latest-amd64.tar.gz

# 内网机器
gunzip -c jaysql-latest-amd64.tar.gz | docker load
docker compose up -d --no-build
```

`docker save` 出来大约 175 MB（含内置 gh-ost 的 amd64/arm64 两份 + backup-agent）。
`--no-build` 不是必须的（`docker-compose.yml` 里没有 build 段），但加上能避免误触发。

如果内网机器是 arm64，把 `--platform linux/amd64` 换成 `linux/arm64` 再拉一次。

### 做法二：自己构建后导出

适合"内网要跑的版本和官方发布不同"的场景：

```bash
# 有网的机器（注意指定目标平台）
docker build --platform linux/amd64 -f docker/Dockerfile -t jaysql:1.99 .
docker save jaysql:1.99 | gzip > jaysql-1.99-amd64.tar.gz

# 内网机器
gunzip -c jaysql-1.99-amd64.tar.gz | docker load
docker compose up -d --no-build
```

### 做法三：内网有私有 registry

```bash
docker tag ghcr.io/simajiong/jaysql:latest registry.local/db/jaysql:latest
docker push registry.local/db/jaysql:latest
# 内网机器上把 docker-compose.yml 的 image 改成 registry.local/db/jaysql:latest
```

---

## 13. 排错对照表

| 现象 | 根因 | 处理 |
|---|---|---|
| 容器起一下立刻退出 | 配置没挂上 | `docker compose logs jaysql` 若见 `Config file /app/config.yaml does not exist`，检查卷路径与文件名 |
| 日志 `Access denied for user` | 元库账号密码错 / 该账号不允许从容器网段登录 | 核对 `config.yaml`，`SELECT user,host FROM mysql.user;` 看 host 段 |
| 日志 `Unknown database 'jaysql'` | 库没建 | 执行 §5.1 的 CREATE DATABASE |
| 页面能开但一片空白 | 前端产物没打进去（只拷了 `index.html` 占位） | 确认构建日志走了 `npm run build` 且没跳过 `COPY --from=web`；`curl` 首页取出的 JS 文件名请求一下看是否 404 |
| 登录报 500 | 种子数据没写成功（迁移中断） | 看日志里 AutoMigrate 的报错，多半是账号缺 DDL 权限 |
| 宿主机 `curl` 拒绝连接 | `listen_address` 写成 `127.0.0.1` | 改成 `0.0.0.0:8083` |
| 8083 端口冲突 | 宿主机已占用 | 改 compose 的 `"8083:8083"` 左边端口 |
| 页面显示的时间差 8 小时 | 容器或元库时区是 UTC | 确认 `TZ=Asia/Shanghai`、镜像装了 tzdata；元库加 `--default-time-zone=+08:00` |
| DDL 工单报 gh-ost 不存在 | `$HOME` 不可写，内置 gh-ost 释放失败 | 确认 `HOME=/app` 且 `/app/.cache` 可写（别用只读根文件系统） |
| 备份任务报找不到 mysqldump | 用了本地节点模式 | 改成远程 SSH 节点，见 §11 |
| 恢复时报 `scp: not found` | 镜像缺 openssh-client | 检查 Dockerfile runtime 阶段的 `apk add` 是否被删 |
| 监控告警里的链接点不开 | `notice_url` 是 127.0.0.1 | 改成对外的 IP/域名 |
| 改了 `config.yaml` 不生效 | 程序只在启动时读配置 | `docker compose restart jaysql` |
| Windows 上构建卡在 `npm ci` 很久 | 依赖走公网 + NTFS 同步慢 | 加 `NPM_REGISTRY` 参数；源码移到 WSL2 文件系统 |
| Windows 上外部访问 8083 不通 | 防火墙 / 网络配置文件 | 检查 Defender 入站规则，测试时把网络设为「专用」 |
| `docker pull ghcr.io/...` 报 `denied` / `unauthorized` | GHCR 包还是 private | 维护者去 Package settings 改成 Public，见 §7.1 |
| `docker pull` 卡住或超时 | 国内访问 ghcr.io 不稳定 | 配镜像加速，或按 §12 做法一导出 tar 传过去 |

---

## 14. 可选的加固项

镜像已做的：**非 root（UID 10001）**、静态二进制、不打包任何密钥、健康检查、日志轮转限制。

可以再加（按需）：

```yaml
services:
  jaysql:
    # 应用与 gh-ost 都不需要任何 Linux capabilities
    cap_drop: [ALL]
    # 限制资源，避免监控采集异常时吃满宿主机
    deploy:
      resources:
        limits:
          cpus: "2"
          memory: 2G
```

不建议开 `read_only: true`——gh-ost 释放、media 上传、日志写入都需要写权限。

---

## 15. 落地检查清单

- [ ] 4 个部署文件已放到位，`.dockerignore` 在仓库根
- [ ] `docker/config.yaml` 已从 example 复制，`chmod 600`，未提交到 git
- [ ] `secret_key` 是 32 字符随机值
- [ ] `listen_address` 是 `0.0.0.0:8083`
- [ ] `database.host` 不是 `127.0.0.1`
- [ ] `monitor.prometheus_url` 已改成真实地址或置空
- [ ] `notify.notice_url` 是使用者能访问的地址
- [ ] 元库已建库，账号有 DDL 权限
- [ ] 镜像架构与服务器一致（`docker image inspect` 确认 `amd64/linux`）
- [ ] `docker compose ps` 显示 healthy
- [ ] 已用 `admin` 登录并改掉初始密码
- [ ] 已跑通一次 DDL 工单（验证 gh-ost）
- [ ] 备份节点配成远程 SSH，且成功跑过一次
- [ ] 元库已配好定期 `mysqldump`

---

## 附：为什么这几个设计点这么定

| 设计 | 原因（都对着代码核实过） |
|---|---|
| 必须构建前端，不能只用仓库里的 `backend/web/dist` | `.gitignore` 里 `backend/web/dist/*` 被排除，只剩一个 `index.html` 占位，直接用会白屏 |
| `docs/` 必须参与构建 | `www/scripts/build-docs.mjs` 从仓库根 `docs/manual` 读 Markdown 生成 `manifest.json`，`npm run prebuild` 会自动跑；不 COPY docs 会直接构建失败 |
| 用 `CGO_ENABLED=0` | 所有数据库驱动（pgx / go-ora / go-mssqldb / clickhouse-go / mongo-driver）都是纯 Go；关掉 CGO 后产物是静态链接 ELF，基镜像可以用 alpine，不必带 gcc |
| `HOME=/app` 必须有写权限 | 内置 gh-ost 释放到 `os.UserCacheDir()`，取不到才退到 `/tmp`；写不进去时 DDL 工单直接失败 |
| 健康检查探 `/favicon.ico` | 应用没有 `/health` 端点，探静态资源既能验证 HTTP 就绪，也不触发任何业务逻辑 |
| `start_period: 120s` | 首次启动要 AutoMigrate 约 50 张表并写种子数据，30 秒内探活必然失败并被反复重启 |
| 不打包 `config.yaml` | 配置含 secret_key 与元库密码，进镜像层等于泄露；一律用卷挂载 |
| 把 `backup-agent` 一起打进 `/app/` | 代码按 `GOINSIGHT_AGENT_BINARY` → `/usr/local/bin/backup-agent` → **主程序同目录** → `./backup-agent` 顺序查找。镜像里主程序在 `/app`、WORKDIR 也是 `/app`，两条候选同时命中，节点一键部署 agent 才不会报「未找到可用于下发的 backup-agent 二进制」 |
| 命名卷而非 bind mount | Windows 上 NTFS 权限映射不可靠；Linux 上 bind mount 要额外 `chown 10001` |
