# Wave 知识库

本文件记录 Wave 项目的关键事实，用于 spec 阶段自动核对。涉及 Wave 落地的 artifact 必须对照此文件，未覆盖的假设需先探索 Wave 代码库核实，核实结果同步追加到这里。

## Schema

| 类型 | 命名 | 示例 |
|------|------|------|
| global | `schema_global` | `schema_global` |
| meta（per-project） | `schema_{projectID}` | `schema_3208` |
| data（per-project） | `schema_{projectID}` | `schema_3208` |

- meta 和 data 前缀相同（`schema_`），但在不同数据库（`sw_meta` vs `sw_data`）
- 配置项：`pg_global_schema: schema_global`、`pg_meta_schema_prefix: schema_`、`pg_data_schema_prefix: schema_`
- 代码位置：`pkg/config/inf_cfg.go`

## Migration

- **迁移脚本目录**：`script/migration/scripts/`
- **命名格式**：`{dbtype}_v{version}_{description}.{sql|go}`
  - 示例：`global_v20260604_account_api_token_expires_at.sql`、`meta_v20260602_tracking_plan_archive.sql`
- **bootstrap DDL**：`script/sql/pgsql/global.sql` / `meta.sql` / `pgdata.sql`
  - global.sql 以 `SET search_path TO schema_global;` 开头
  - 通过 `go:embed` 嵌入到 Go 二进制（`script/sql/pgsql/tables.go`）
- **框架**：Go migration framework（`script/migration/`）
  - `ExecGlobalSQL(ctx, sql)` 自动设 `search_path`，SQL 里不需要 schema 前缀
  - SQL-only 迁移脚本由 `loader.go` 自动加载并包装 schema 上下文
  - 注册式：实现 `Migration` 接口后注册到 `registry.go`
  - ORM/DAO 层：`fmt.Sprintf("%s.%s", schema, table)`
- **禁止 `CREATE INDEX CONCURRENTLY`**：现有 migration 运行在事务中；新建表用普通 `CREATE INDEX IF NOT EXISTS`
- **数据库**：global schema 在 `sw_meta` 数据库中

## 配置

- 配置文件：`configs/inf/inf.{env}.yml`
- 配置结构体：`pkg/config/inf_cfg.go`

## 项目生命周期与运行面

- `project.status` 现有值为 `INITIALIZING`、`ENABLE`、`DISABLE`；旧项目 Delete 是生产代码中 `DISABLE` 的主要写入点，没有发现独立的租户“暂停项目”功能。
- 旧项目 Delete 会删除 usage metering Job、Doris Database、Meta/Data PG Schema、Kafka Topic，软删除项目成员和项目主记录，并从 PM 移除项目；它不能直接作为可恢复 Delete。
- 旧 Project Delete 先写 `status=DISABLE`，随后把主记录 `is_deleted=true`，因此历史软删除主记录通常是 `DISABLE,true`；`GetByIDWithDeleted` 可直接读取。当前 worktree 未发现自动物理删除 project 主记录的生产清理机。
- Project migration 使用 `GetAllNotDeletedProjects`，因此所有 `is_deleted=false` 项目（包括 `DISABLE`）都会继续执行 Meta PG/Doris migration；当前 framework 没有 Data PG migration 类型。
- PM 的直接 Hook 使用方包括 Edge、Connector、ABOL、MA ConfigSync、QE Catalog 和 Dispatch；ADTOL 通过每次请求的 PM Token 查询阻断项目。
- PM Redis 中 `sys:{pm}:projects` 是可用 project ID 集合索引，`sys:{pm}:info:<pid>` 是包含 Secret、状态、Schema/Database/Topic、配额和配置的 `pm.Info` JSON 快照，`sys:{pm}:info_change` 是 set/delete 变更频道；文档中不再把前两者统称为含义模糊的 membership/info。
- 现有 `OrganizationFilter` 主要为 Account API Token 提取组织 ID；普通会话会提前放行，且中间件不检查组织状态。Role 等组织资源也并非全部经过 `OrgService.getOrgOrFail`，组织 Delete 不能只在 Service 层加门禁。
- 不能只依赖 PM Delete/Update Hook：MCP 根路由、Internal S2S API、LiveEvent 长连接、Wagent Redis Stream Worker，以及 Scheduler 已加载 cron/notify 和 Worker 均存在需要单独门禁的路径。
- C1 由 Dispatch 间接接收项目拓扑删除并关闭 Pipeline，但 `Node.OnProjectDelete` 只删 `counts`，现有 `refreshTopo` 对 Redis 中已移除项目只跳过、不标记 changed，可能不重写 task map；此外 C1 项目 metadata map 仍需单独驱逐。
- Project 初始化会显式创建 `df_<pid>_{raw_event,event,other,error}` 四类 Kafka Topic，并设置分区和 retention；C1 loader 是当前唯一显式启用 `AllowAutoTopicCreation=true` 的生产 writer，Edge 使用默认 false、Connector 显式 false。关闭 C1 自动创建前需要检查并按初始化配置补齐全部 `ENABLE` 项目的预期 Topic。
- Scheduler 生产 Worker 分布在 Web、Connector、MA；项目门禁和长期任务取消应优先在 Scheduler Master/Worker 中心入口完成，不逐组件建设生命周期协调器。
- Scheduler 当前有 11 个生产 JobType 注册：Web 8 个（cohort、cohort-clean、ab-report、asset-metrics、asset-ref-wal、events-view、event-stat、usage-metering），Connector 1 个（pipeline），MA 2 个（ma-time-fire、ma-event-trigger）。
- Internal S2S Pipeline 接口同时包含新工作命令、在途查询和结果/进度回写：`run/start`、`load-file/create`、`ma/materialize-fanout` 会创建新工作；`pipeline/run/load-file/backfill` 的 update/finish/advance/complete 只收敛既有执行状态、日志和统计。当前没有通用 `cleanup` 回调。
- Scheduler 的 job/job-instance/job-task notify 使用全局 Redis List/ZSet，project ID 编码在 value/member 中；Instance heartbeat 和 Task lease 则使用带 project ID 的独立 Key。Project Purge 不能只扫描 `p:<pid>:*`，需要 Scheduler 按自身编码定向清理。
- MA 运行资源同时使用共享和独享 Redis，项目 Key 形如 `ma:{p:<pid>}:*`，Web 没有独享 Redis 的连接配置；完整 Purge 需要由 MA 自己通过窄的内部接口同步清理两个 Redis，不能在 PM Delete Hook 中删除。
- `cmd/ma` 的健康端口使用原生 `http.ServeMux`，当前不初始化 Global DB，也不挂 Web 的 Gin `internalauth`；单个 Web→MA Purge endpoint 复用专用环境变量 Secret 比为 MA 增加 Global DB/scope 依赖更小。
- MA 除 ConfigSync view 外还有按项目的 cohort index/watcher、event matcher、feedback 进程内队列和 token cache；现有 ConfigSync PM Delete Hook `OnProjectDelete` 只做 `Untrack`，不足以驱逐这些进程内状态。
- Connector 在 Wave 自管 OSS 使用四类项目根前缀：`load/<pid>/`、`backfill/<pid>/`、`events_cron/<pid>/`、`users_cron/<pid>/`；Project Purge 需全部清理，客户自有目标 bucket 不在 Wave 删除范围。
- Wagent 大部分运行 Key 使用 `p:<pid>:`，但 token quota 使用 `wagent:quota:{pid:week}`，execution rate-limit Key 的 project ID 位于中段；只删通用项目 prefix 会残留数据。
- Wagent MCP tool list cache 的 key 包含 project ID，但它只是进程内 TTL cache，不能自行产生工作；新执行门禁足以使 Delete 项目不可用，无需为它新增 PM Delete/Update Hook。
- LiveEvent 每次项目 WebSocket 会创建 `live-event-<pid>-<timestamp>-{raw,events,errors}` Kafka consumer group；关闭连接不会立即删除 broker group metadata，Project Purge 需按项目前缀清理。
- 共享 Redis 中项目权限（`sol:perm:v5:account_perm:pid:<pid>:`）、资产权限、QE refresh lock、Project→Org cache 和 Account API Token scope cache 都不完全落在 `p:<pid>:` 下；Purge 需要按各自既有 key 规则清理，不能只做一次通用 prefix scan。
- `apps` 顶级目录共有 `abol/adtol/c1/connector/edge/ma/simulator/web` 八个；`apps/simulator` 不初始化 PM、不注册生产 Scheduler handler，也不拥有 Wave 项目持久资源。
- `pkg/dal/dorisx` 的 `ddlLocks` 是按 project ID 缓存的进程内 mutex map；它不保存业务数据，不能在仍有 goroutine 持锁时安全按项目替换，生命周期中应视为随进程释放的同步残留，而非 Purge 资源。
- Migration runner 的 `DBType` 只有 `global/meta/doris`：Global migration 执行一次，Meta/Doris migration 按项目执行，结果写入 Global PG `migration_history`；没有 Data PG migration 类型。Pipeline 是项目业务资源，只有某个 migration 修改 Pipeline 表时二者才发生结构升级关系。
