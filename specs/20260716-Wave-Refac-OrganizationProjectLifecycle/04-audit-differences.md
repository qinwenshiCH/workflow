# 审计差异声明：项目资源全景代码验证

> 审计时间：2026-07-22
> 代码基线：`/Users/wenshiqin/wave-worktrees/delete_org_project/`
> 审计方式：6 个 agent 并发调查，逐一比对 spec 4.x 声明与实际 Go 代码

---

## 总结

> 说明：QE Catalog、LiveEvent、Asset Behavior 均为 `apps/web` 子模块，非独立部署组件，所有改动在 web 代码库内完成，无需跨团队沟通。

| 分类 | 组件 | 结果 |
|------|------|------|
| Web 全模块 | Project/Organization DAO、PM SetInfo/DeleteInfo、入口门禁、Wagent、MA 控制面、权限/Token cache、QE Catalog、LiveEvent、Asset Behavior | ✅ 一致 (6/9)，⚠️ 3/9（QE/LiveEvent/Asset Behavior） |
| 在线入口 | apps/edge、apps/adtol、apps/abol | ⚠️ 1/3 有差异 |
| 数据与异步 | apps/c1、apps/connector | ⚠️ 1/2 有差异 |
| 独立工具 | apps/simulator | ✅ 一致 |
| 共享骨架 | pkg/pm、pkg/scheduler、pkg/dispatch、pkg/dal 各 client | ⚠️ 3/4 有差异 |
| 异步执行 | apps/ma | ✅ 一致（运行资源；Purge endpoint 待实现） |

---

## 1. apps/web 核心

### 1.1 Project/Organization DAO — ✅ 一致

Global PG 表和结构体字段与 spec 一致。差异说明：

- **Project `Delete` 只改 `is_deleted=true`，不改 `status`** — `apps/web/dao/global/project.go:212-218`
  - 现有 `Delete` 是软删除操作，仅设 `is_deleted`。PM 的 `Info.Status` 读取 `status` 字段而非 `is_deleted`，因此仅调用 DAO.Delete 不会让 PM 感知项目不可用。
  - 本 spec 要求新增生命周期 DAO 方法（写 `DISABLE` + `PURGED`），与现有 DAO.Delete 是不同操作路径，不冲突。

### 1.2 PM SetInfo/DeleteInfo — ✅ 一致

`pkg/pm/project_manager.go:213-250` — 同时操作集合索引（`HDel sys:{pm}:projects`）和运行时快照（`Del sys:{pm}:info:<pid>`），再发布 `info_change`，与 spec 完全一致。

### 1.3 入口门禁 — ✅ 一致

- `ProjectFilter` — `pkg/ginx/middleware/project.go:85-118`，以 PM 是否含项目作门禁 ✅
- `OrganizationFilter` — `pkg/ginx/middleware/organization.go:43-69` ✅
- `authorizeProjectContext` — `apps/web/mcp/tools/context.go:31-51` ✅
- 差异说明：Internal S2S 的 `requireInternalProject()`（`apps/web/service/pipeline/internal_metadata.go:20-25`）对所有 handler 统一检查项目存在，未按 spec 4.5.2 表格细分"新工作命令/在途查询/结果回写"三类。这属于代码归属合理，spec 表格分类粒度更细但入口行为一致。

### 1.4 Wagent — ✅ 一致

- `wagent_conversation/message` 表 — `apps/web/wagent/dao/conversation.go:51-75`, `message.go:46-64` ✅
- execution/compaction Stream/DLQ Key — `apps/web/wagent/service/runtime/execution.go:893-923` ✅
- executor running map — `apps/web/wagent/service/executor.go` ✅
- 补充：Key 格式为 `wagent:{wagent}:execution:<id>`，非 `p:<pid>` 前缀；compaction stream/DLQ (execution.go:917-923) 和 events zset 在 spec 中未明确列出。

### 1.5 MA 控制面 — ✅ 一致

- `ma_campaign` 表 — `apps/web/ma/dao/campaign.go:19-40` ✅
- Scheduler Job 管理 — `apps/web/ma/service/scheduler_job.go` ✅
- 差异说明：`audience_config` 是 campaign 表的一个 JSON 字段（campaign.go:27），不是独立资源实体；spec 将"audience/config"并列列出易误导。`ma_fanout_batch` DAO 表存在但未在 4.5.8 台账中列出（属于 4.5.2 表格中的 `fanout batch`）。

### 1.6 权限与 Token cache — ✅ 一致

- `sol:perm:v5:*` — `apps/web/service/permission/cache.go:22-26` ✅
- asset permission cache — `apps/web/service/asset/permission/cache.go:23-28` ✅
- Token scope cache — `apps/web/service/account/apitoken/service.go:132-217` ✅

---

## 2. apps/web 本地模块

### 2.1 QE Catalog — ⚠️ 有差异

| 检查项 | spec 声明 | 实际代码 | 差异 |
|--------|----------|---------|------|
| `catalogs[projectID]` | 进程内状态 | `apps/web/qe/catalog/catalog.go:53` — `var catalogs = make(map[int64]*ProjectMetaCatalog)` | ✅ 一致 |
| MetaCache 8 个字段 | 进程内状态 | `catalog.go:247-270` — `ProjectMetaCatalog` 含 8 个 MetaCache | ✅ 一致 |
| refresh lock Key | 运行资源 | `pkg/dal/redisx/redis.go:112` — `sys:catalog:refresh:lock:<pid>` | ✅ 一致 |
| **Delete 行为** | **"Delete Hook 驱逐"** | **`catalog.go:219-221` — `OnProjectDelete` 空实现，注释"不需要特殊处理"** | **⚠️ 代码没做驱逐，与前文约 50 行处声明的实现意图不一致** |

补充遗漏：
- `KeySysCatalogRefreshChannel = "sys:catalog:refresh"` — Redis pubsub channel（`redisx/redis.go:111`），跨节点刷新通知
- `KeySysViewRefreshLockPrefix = "sys:view:refresh:lock:"` — 第二个 Redis 锁（`redisx/redis.go:113`）
- `CatalogNotifier` 后台 goroutine — `notifier.go:74-98`
- `refreshAll` ticker goroutine — 每 30 分钟定时全量刷新，`catalog.go:140-163`

### 2.2 LiveEvent — ⚠️ 有差异

| 检查项 | spec 声明 | 实际代码 | 差异 |
|--------|----------|---------|------|
| 项目 WebSocket | 运行资源 | `apps/web/service/liveevent/liveevent.go:94` | ✅ 一致 |
| Kafka consumer | 运行资源 | `liveevent.go:95` — `consumers map[int64]*projectConsumer` | ✅ 一致 |
| group 格式 | `live-event-<pid>-<timestamp>-*` | `liveevent.go:250-285` — 最多 3 个 group：`...-raw`、`...-events`、`...-errors` | ✅ 一致（`-*` 通配可覆盖） |
| **Delete 行为** | **"Delete Hook 关闭"** | **没有 PM Hook 注册，无 per-project 清理** | **⚠️ 代码未实现 spec 描述的 OnProjectDelete 行为** |

补充遗漏：
- `eventMap sync.Map` — 进程内 `trace_id → pipeline_id` 映射，TTL 10 分钟（`liveevent.go:99`）
- `cleanupEventCache` goroutine — 每 5 分钟清理过期 eventMap（`liveevent.go:127-150`）

### 2.3 Asset Behavior — ⚠️ 过程偏差，不建议独立台账

> Asset Behavior 是 Web 进程内的 goroutine manager（`apps/web/service/asset/behavior.go`），**不是独立组件**。它不拥有单独的数据存储——写入的是 Meta PG 已有的 `asset_behavior` 表，没有独享 Redis/Kafka 等外部资源。进程退出后 goroutine 自然释放。
>
> **建议：从 4.5.6 独立资源台账降级为 Web 运行资源收敛的一行补充说明**，不单独绘制 Mermaid 状态图。batcher 的 drain/flush/close 合并到 Web 运行资源收敛的通用描述中。

| 检查项 | spec 声明 | 实际代码 | 差异 |
|--------|----------|---------|------|
| 项目 batcher/goroutine | 运行资源 | `apps/web/service/asset/behavior.go:30` — `batchers map[int64]*projectBatcher` | ✅ 一致 |
| 行为数据 | 持久资源 | `behavior.go:15-21`, `dao/asset/behavior.go:38` — `asset_behavior` 表 | ✅ 一致 |
| **Delete 行为** | **"Delete Hook drain、flush、Close"** | **无 PM Hook 注册，只有全局 `Close()`（`behavior.go:209-216`）** | **⚠️ 代码没有 per-project 关闭入口，需补充 PM Hook 注册 + per-project Close** |

补充遗漏：
- `ch` channel（容量 1000）— 每个 `projectBatcher` 的缓冲通道（`behavior.go:39`）

---

## 3. 在线入口

### 3.1 apps/edge — ⚠️ 有差异

| 检查项 | spec 声明 | 实际代码 | 差异 |
|--------|----------|---------|------|
| Raw/Event/Error Topic | 持久资源 | Kafka Topic 名来自 `pm.Info.Conf.Env.KafkaTopic*` | ✅ 一致 |
| 全局 producer | 运行资源 | edge 启动时创建 | ✅ 一致 |
| `token2id` | 进程内状态 | 代码中存在 | ✅ 一致 |
| `pipelineVersion` | 进程内状态 | 代码中存在 | ✅ 一致 |
| `internalSecrets` | 进程内状态 | 代码中存在 | ✅ 一致 |
| **Delete 行为** | **"Delete Hook 驱逐"** | **`service.go:223-226` — `OnProjectDelete` 显式空操作，注释"暂时不需要删除，影响不大"** | **⚠️ 代码声明了空操作，与 spec 描述不一致** |

### 3.2 apps/adtol — ✅ 一致

仅依赖 `PM.Token2ProjectID` 入口门禁，不拥有任何项目资源。`apps/adtol/api/router.go` 已验证。

### 3.3 apps/abol — ✅ 一致

- `abCore[projectID]` — `apps/abol/service/abol.go` ✅
- metadata loop — PM Update Hook 创建 / Delete Hook 停止 ✅
- Meta AB 配置持久资源 — 存在 ✅
- 项目 Redis target cache — 存在 ✅

---

## 4. 数据与异步执行

### 4.1 apps/c1 — ⚠️ 有差异

| 检查项 | spec 声明 | 实际代码 | 差异 |
|--------|----------|---------|------|
| Meta/Data PG | 持久资源 | `main.go:76-89` | ✅ 一致 |
| Doris Database | 持久资源 | `pkg/dal/dorisx/ddl.go:200-204` — `DropDatabase` 存在但当前无调用者 | ✅ 一致 |
| **AllowAutoTopicCreation** | **应为 `false`** | **`loader/kafka_loader.go:74` — `AllowAutoTopicCreation: true`** | **⚠️ 尚未关闭，spec 第 310/1819 行要求本期关闭** |
| C1 extractor group | 跨项目共享 | `extractor/extractor.go:45` — 所有项目共用 `cfg.ExtractorKafkaGroupID` | ✅ 一致 |
| Redis task map | 持久资源 | `pkg/dispatch/rdb.go:63-86` | ✅ 一致 |
| Tasker | 运行资源 | `main.go:125-139` | ✅ 一致 |
| consumer/loader | 运行资源 | extractor per-pipeline，loader 全局共享（`pipeline.go:72-74`） | ✅ 一致 |
| metadata store/topology | 进程内状态 | `metadata/metadata.go:48-51` + `pkg/dispatch/node.go:44` | ✅ 一致 |
| DDL mutex | 进程内状态 | `pkg/dal/dorisx/ddl.go:254-266` | ✅ 一致 |

### 4.2 apps/connector — ✅ 一致

全部 7 类资源验证一致，包括：
- Meta pipeline/run/backfill ✅
- Scheduler Instance/Task/lease ✅
- 派生 Topic 和消费组 ✅
- OSS 四类前缀 ✅
- Kafka runner/consumer（PM gate + heartbeat 取消）✅
- 批导临时对象 ✅
- 客户目标副本（不清理）✅

---

## 5. apps/ma — ✅ 一致（运行资源；Purge 待实现）

| 检查项 | spec 声明 | 实际代码 | 差异 |
|--------|----------|---------|------|
| Meta campaign/audience/config | 持久资源 | `lifecycle/store.go:26` — `ma_campaign` 表 | ✅ 一致 |
| 共享 Redis Key | `ma:{p:<pid>}:*` | `redisx/hashtag.go:25-35` — `HashTagKey("ma", pid, ...)` | ✅ 一致 |
| 独享 Redis Key | `ma:{p:<pid>}:*` | `server.go:62` — `rt.exclusiveRedis` | ✅ 一致 |
| Kafka group 格式 | `{groupPrefix}.<pid>` | `eventconsumer/coordinator.go:131-133` — `ma-trigger.<pid>` | ✅ 一致 |
| Scheduler handler (两组) | 运行资源 | `server.go:250-251` — coordinator 收敛 | ✅ 一致（heartbeat 取消为框架行为） |
| event consumer/watcher/sweeper/materializer | 运行资源 | 各服务实现 | ✅ 一致 |
| config/cohort/matcher/feedback cache | 进程内状态 | configsync + coordinator + dispatch | ✅ 一致（matcher/feedback 清理依赖 scheduler ctx.Done() 间接收敛，非直接 PM Hook） |
| **Purge endpoint + 5.4 全部改动** | **本 change 实现** | **当前基线不存在** | **⚠️ 待实现，符合预期** |

---

## 6. 共享运行骨架

### 6.1 pkg/pm — ✅ 一致

- `sys:{pm}:projects` — `pkg/dal/redisx/redis.go:103` ✅
- `sys:{pm}:info:<pid>` — `redis.go:105`，`pm.Info` 结构体含 `Secret/Conf(Env,Quota)/Status` ✅
- `sys:{pm}:info_change` — `redis.go:104`，Pub/Sub ✅
- 进程内 map — `project_manager.go:79` — `Manager.projects map[int64]*Info` ✅
- 快照对账 — `project_manager.go:380` — `loadAllProjects` + `autoSubscribe` ✅

### 6.2 pkg/scheduler — ✅ 一致

- Meta PG Job/Instance/Task DAO ✅
- Redis notify/delayed key — `pkg/scheduler/rdb.go:93-100` ✅
- heartbeat/lease key — `makeJobInstanceKey`/`makeJobTaskKey` ✅
- Master cron — `master.go:179` — `startLeaderLoop` ✅
- PM gate — `server.go:38` — `pm.DefaultManager()`，所有 repair 循环使用 `m.pmManager.ProjectIDList()` ✅
- Heartbeat 取消 — `worker.go:200-252` — `quitLease chan error` + `cancel()` ✅

### 6.3 pkg/dispatch — ⚠️ 部分差异

| 检查项 | spec 声明 | 实际代码 | 差异 |
|--------|----------|---------|------|
| Redis task map | 持久资源 | `rdb.go:108` — `sys:{dispatch}:task:<service>:<host>` | ✅ 一致 |
| Pub/Sub | 运行资源 | `rdb.go:109` — `sys:{dispatch}:task_change:` | ✅ 一致 |
| TaskManager | 运行资源 | `manager.go:16-28` | ✅ 一致 |
| counts/topology | 进程内状态 | `node.go:44` — `counts map[int64]counter` | ✅ 一致 |
| Delete 重写 map | — | `node.go:109-115` — 删除 counts + refreshTopo 重写 Redis | ✅ 一致 |
| **Restore 按 PM quota 重建** | — | **dispatch 不直读 `pm.Info.Conf.Quota`，通过外部 `ParseInfoHostsAndTasksHandler` 回调** | **⚠️ 间接实现，spec 表述易误导为 dispatch 直读 quota** |

### 6.4 pkg/dal — ⚠️ 有差异

| client | spec 声明 | 实际代码 | 差异 |
|--------|----------|---------|------|
| **redisx** | "窄 prefix delete 和 Key 解析" | Key 解析存在（`hashtag.go:40-50`），**Scan+Del 批量 prefix delete 不存在** | **⚠️ 无批量前缀删除函数** |
| **kafkax** | "Topic/group admin，group 幂等删除" | `TopicList`（`admin.go:169-192`）✅，`DeleteTopics`（`admin.go:201-227`）✅，**`DeleteConsumerGroup` 不存在** | **⚠️ 有 Topic 管理，无 Group 删除函数** |
| dorisx | `DROP DATABASE IF EXISTS` | `ddl.go:200-204` — `DropDatabase` | ✅ 一致 |
| pgsqlx | `DROP SCHEMA IF EXISTS CASCADE` | `pgdata/init.go:238-248` — `DropSchema`，`252-268` — `DropSchemas` | ✅ 一致 |

---

## 7. apps/simulator — ✅ 一致

确认不持有任何 Wave 项目资源：无 `pkg/pm` 引用，无 PG/Redis/Kafka/Doris 依赖，`Scheduler` 结构体（`scheduler.go:14-25`）是自有的 YAML 配置驱动简易调度器，与 `pkg/scheduler` 无关。

---

## 关于 PM OnProjectDelete Hook

PM Hook 基础设施**已经存在**，注册 API 为 `pm.DefaultManager().RegistProjectInfoHooker(hooker)`。多个组件已在正常使用：

| 组件 | 已注册？ | `OnProjectDelete` 实现 |
|------|---------|----------------------|
| pkg/dispatch | ✅ | ✅ `node.go:109-115` — 驱逐 counts |
| apps/ma | ✅ | ✅ `sync.go:269-274` — Untrack |
| apps/web/wagent | ✅ | ✅ 有实际执行逻辑 |
| QE Catalog | ✅ | ❌ `catalog.go:219-221` — 空实现 |
| apps/edge | ✅ | ❌ `service.go:223-226` — 显式空操作 |
| LiveEvent | ❌ | N/A |
| Asset Behavior | ❌ | N/A |

QE/LiveEvent/Asset Behavior 和 edge 的差异都属于 **代码需要补充实现**，而不是 spec 需要修改。这些组件都是 `apps/web` 的子模块，没有跨团队依赖，在此 change 中一并补充 per-project 清理逻辑即可。

## 差异汇总表

### A 类：代码需要补充实现（spec 描述是正确的目标行为）

| # | 组件 | 缺失实现 | 代码位置 | 工作量评估 |
|---|------|---------|---------|-----------|
| A1 | QE Catalog | `OnProjectDelete` 非空实现——驱逐项目 catalog 和 MetaCache | `apps/web/qe/catalog/catalog.go:219-221` | 小 |
| A2 | LiveEvent | 注册 PM Hook + 实现 per-project 关闭 WebSocket/Consumer | `apps/web/service/liveevent/` | 中 |
| A3 | Asset Behavior | 注册 PM Hook + 实现 per-project drain/flush/Close batcher | `apps/web/service/asset/behavior.go` | 小 |
| A4 | apps/edge | `OnProjectDelete` 非空实现——驱逐 token2id/pipelineVersion/internalSecrets | `apps/edge/service.go:223-226` | 小 |

### B 类：代码基线尚未完成，属于本 change 待实现（spec 预期中）

| # | 组件 | spec 要求 | 代码现状 | 备注 |
|---|------|----------|---------|------|
| B1 | apps/c1 | `AllowAutoTopicCreation=false` | `true` (`loader/kafka_loader.go:74`) | 第 5 章实现，第 4 章已声明"本期关闭" |
| B2 | apps/ma | Purge endpoint + 5.4 全部改动 | 基线不存在 | 第 5、6 章实现，第 4 章已声明"新增" |
| B3 | Asset Behavior | 建议降级处理 | 4.5.6 独立台账配合独立 Mermaid 图过于重 | 合并到 Web 运行资源收敛描述 |

### C 类：shared infrastructure 缺失

| # | 组件 | spec 声明 | 实际代码 | 是否必须 | 原因 |
|---|------|----------|---------|---------|------|
| C1 | `pkg/dal/redisx` | 窄 prefix delete | 不存在 | ❌ 不必须 | 各 owner 可自用 `SCAN` + `DEL` 实现；`HashTagKey` 的 `{p:<pid>}` 已保证同项目键在同一 slot |
| C2 | `pkg/dal/kafkax` | group 幂等删除 | 不存在 `DeleteConsumerGroup` | ✅ 必须补充 | MA Purge 第 4 步需要删除 `ma-trigger.<pid>` 消费组；无此函数则 group 元数据残留 |

### D 类：规格或表述微调

| # | 组件 | 说明 | 建议 |
|---|------|------|------|
| D1 | dispatch | Restore 按 PM quota 重建间接通过外部回调 | 补充说明 dispatch 不直读 quota |
| D2 | apps/web MA | `audience_config` 是 campaign JSON 字段 | 修正台账表述 |
| D3 | apps/web MA | `ma_fanout_batch` 表未在 4.5.8 列出 | 补充资源清单 |
| D4 | LiveEvent | 每个 project 最多 3 个 group（`-raw/-events/-errors`） | 补充前缀说明 |
| D5 | LiveEvent | `eventMap` 和 `cleanupEventCache` goroutine 未列出 | 补充资源清单 |
| D6 | QE Catalog | `CatalogNotifier`、`refreshAll` ticker、`KeySysViewRefreshLockPrefix` 未列出 | 补充资源清单 |
| D7 | Wagent | compaction stream/DLQ、events zset 未列出 | 补充资源清单 |
