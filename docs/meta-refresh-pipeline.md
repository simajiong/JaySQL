# 库清单元数据：从「定时同步」改为「事件驱动 + 读时兜底」

## 1. 问题

新建库之后，工单提交、授权申请、资源树、授权管理页最长要等 5 分钟才能选到它。

根因不在 SQLServer，而在架构：`insight_instance_schemas` 是一张**只由 cron 全量刷新的快照表**，
四个读接口全部直接读它，链路上没有任何「写后失效」或「读时校验」：

| 环节 | 改造前 |
| --- | --- |
| 写入口 | 无感知：建库成功不回写快照 |
| 读入口 | 直接查快照，不看新鲜度 |
| 唯一刷新 | `crontab.sync_db_metas`，默认 `*/5 * * * *` |

把周期调小不是解法：那是全局全量扫描，实例越多单轮越慢，而且延迟依旧存在。

## 2. 改造后的三条刷新路径

```
写操作成功 ─┐
读接口 TTL 到期 ─┼─→ 刷新器（singleflight 单飞）─→ 快照表 + 状态表 ─→ 四个读接口
定时对账  ─┘
```

### L1 写后失效（主路径，秒级）

凡是经过平台改变了库清单的操作，成功后立刻异步刷新该实例：

| 位置 | 文件 |
| --- | --- |
| SQL 工作台执行成功 | `internal/das/services/schema_change_hook.go` + `execute_*.go`（5 个引擎） |
| 工单执行成功 | `internal/orders/services/execute_tasks.go` |
| 数据源新建 | `internal/common/services/instance.go`（`AdminCreateInstancesService`） |
| 数据源编辑 | `internal/common/services/instance.go`（`AdminUpdateInstancesService`） |

判定只看**库级 DDL**：`CREATE|DROP|ALTER DATABASE`、`CREATE|DROP|ALTER SCHEMA`、
`CREATE|DROP USER`（Oracle 的 schema 即用户）、`sp_renamedb`（SQL Server 改库名）。
`CREATE TABLE` / `ALTER TABLE` / `CREATE INDEX` 一律不触发——它们不动库清单，
每次都连一遍目标实例的所有库，代价远大于收益。

### L2 读时惰性（兜底，TTL 60s）

四个读接口在查询前调用 `tasks.EnsureFresh(instanceID)`：

| 接口 | 文件 |
| --- | --- |
| 工单提交的库/schema 下拉 | `internal/orders/services/commit.go` |
| 授权申请的库下拉 | `internal/das/services/access_request_options.go` |
| SQL 工作台资源树 | `internal/das/services/resources.go` |
| 授权管理页库列表 | `internal/das/services/admin.go` |

快照超过 TTL 时**异步**触发一次刷新，本次请求仍返回现有快照（stale-while-revalidate）。
页面耗时不受影响，只影响下一次读取。

### L3 定时对账（降级为兜底）

`crontab.sync_db_metas` 默认从 `*/5` 改为 `*/30`，只负责「平台完全无感知的变更」
（DBA 用客户端直连建库）与长期没人访问的实例。

## 3. 配套机制

### 3.1 singleflight 去重

同一实例并发触发只真正跑一次（`golang.org/x/sync/singleflight`，已在 go.mod）。
没有它，四个页面同时打开就是对同一个 SQLServer 实例跑四遍同步——
一次同步要按库逐个开连接查 `sys.schemas`。

### 3.2 同步状态表

新表 `insight_instance_meta_state`（一实例一行）：

| 列 | 用途 |
| --- | --- |
| `last_synced_at` | 最近一次成功时间；为空表示从未成功 |
| `last_attempt_at` | 最近一次发起时间，L2 的节流依据 |
| `last_status` | `never` / `success` / `failed` |
| `last_error` | 失败原因（截断 512 字符，不含堆栈） |
| `resource_count` | 本次采到的库/模式条数 |
| `syncing_since` | 同步中标记；超过 5 分钟视为进程异常退出的脏标记 |

写入用 `ON DUPLICATE KEY UPDATE`（`markMetaState`），并发安全且一实例恒为一行。

### 3.3 手动刷新入口

| 接口 | 说明 |
| --- | --- |
| `GET /api/v1/das/instances/:id/meta-status` | 只读状态 |
| `POST /api/v1/das/instances/:id/refresh-meta` | 同步刷新并返回结果，每实例 10 秒限流 |

权限用 `RequireAnyPermission("sql.console.access","order.submit","das.access.apply","system.das.view")`。
放这么宽是有意的：工单提交人与权限申请人往往没有 `sql.console.access`，
只卡那一个权限点，刷新按钮对他们就是摆设。接口不返回任何数据内容，
数据可见性仍由 `insight_das_data_grants` 决定。

## 4. 前端改动

| 页面 | 改动 |
| --- | --- |
| 工单提交（`OrderCreate.vue`） | 库/Schema 刷新按钮先调 `refresh-meta` 再重读，按钮转 loading |
| 授权申请（`AccessApply.vue`） | 库下拉旁新增刷新按钮，刷新后自动剔除已不存在的已选库 |
| SQL 工作台（`ConsoleLeft.vue`） | 用户主动刷新节点树时先重采，展开节点时不重采 |

改造前这些刷新按钮只是重读快照表，快照没过期时怎么点都看不到新建的库。

## 5. 配置

```yaml
crontab:
    sync_db_metas: "*/30 * * * *"   # 定时对账
    meta_refresh_ttl: 60            # 读时惰性窗口（秒）；0=默认60，负数=关闭
```

## 6. 上线

1. 发布后端（启动时 `AutoMigrate` 会自动建 `insight_instance_meta_state`）。
2. 若服务账号无 DDL 权限，手工执行 `backend/sql/20261109_instance_meta_state.sql`。
3. 前端 `npm run build`。
4. 回滚脚本：`backend/sql/20261109_instance_meta_state_rollback.sql`
   （只删状态表，不影响库列表与授权数据；旧版代码不读它，回退时其实可以保留）。

## 7. 验证记录

| 检查 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `go test ./...` | 40 个包全绿 |
| `npm run build` | 通过 |
| 迁移脚本真实执行 | MySQL 8.0 建表成功，列/默认值/注释符合预期 |
| 真实库集成测试 | `TestRefreshInstanceMetaIntegration` 通过（成功路径 + 幂等 + 失败路径） |

集成测试默认跳过，需要 DSN：

```bash
JAYSQL_IT_MYSQL_DSN='root:pwd@tcp(127.0.0.1:3306)/jaysql_it?charset=utf8mb4&parseTime=True&loc=Local' \
  go test ./internal/common/tasks -run TestRefreshInstanceMetaIntegration -v
```

## 8. 已知限制

- **多进程部署**：singleflight 与手动刷新节流都在进程内，多副本下各自去重，
  最坏情况是 N 个副本各跑一次同一实例的同步。快照写入是幂等的，不影响正确性。
- **`GetSchemasService`（已授权库列表）未接入 L2**：它是跨实例的聚合查询，
  逐实例触发会变成 N 次采集；该页面的库来自已有授权，新建库本来也要先授权才能看到。
- **失败节流**：同步失败同样受 TTL 约束，一个连不上的实例不会被每次页面请求反复重试；
  想立刻重试请走手动刷新接口。
