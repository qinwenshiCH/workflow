<!-- /autoplan restore point: /Users/wenshiqin/.gstack/projects/qinwenshiCH-workflow/main-autoplan-restore-20260629-155919.md -->

# 技术方案：项目对象审计（object_audit_log）

> 本方案聚焦项目内对象审计。组织/项目管理审计见 [plan-org.md](./plan-org.md)，账号活跃字段见 [plan-account.md](./plan-account.md)。

## 1. 目标

为 Chart / Dashboard / Cohort / AB / Metric / Pipeline / Event / Property 等项目内对象提供统一写入、统一查询、统一历史迁移。

当前设计前提来自以下已确认共识：

- V1 优先服务**内部排障**，查询主视角是 `object_type + object_id`，不是按人或按组织做分析型报表。
- detail 采用**结构化 diff 优先**的稳定 envelope，必要时允许 `extra` / `snapshot` 补充。
- detail **不使用 JSONB**，也**不直接持久化当前业务结构体**。
- 升级后**新操作只写新审计记录**，旧 AB / Metric / Wave 历史记录做一次性复制，旧字段或旧表保留不删。
- V1 **不新增官方产品通用审计页面**；除 AB / Metric 既有查看能力外，其余查看能力优先通过 OP / 内部接口承接。

## 2. 范围切分

### 2.1 项目内对象标准审计

这一层是本次主线，统一落到 `meta.object_audit_log`。

首批对象范围：

- 资产对象：`CHART`、`DASHBOARD`、`COHORT`、`EXPERIMENT`、`FEATURE_GATE`、`FEATURE_CONFIG`、`PIPELINE`
- 元数据对象：`METRIC`、`TRACKED_EVENT`、`VIRTUAL_EVENT`、`EVENT_PROPERTY`、`USER_PROPERTY`、`VIRTUAL_PROPERTY`

## 3. 非目标

- V1 不做官方产品端的新通用审计 UI
- V1 不优先支持按 `operator_id`、按组织范围、按跨项目的审计分析
- V1 不从 `asset_behavior` 反推”谁改了什么”，因为它本质不是可靠审计源

## 4. 推荐架构

### 4.1 模块划分

建议新增一套独立于 OP 审计的项目对象审计模块，命名上避免和 `apps/web/op/service/audit.go` 混淆，文中统一用 `auditlog` 代称。

| 模块 | 职责 | 建议落点 | 参考现状 |
|---|---|---|---|
| 审计域类型 | `ObjectType`、`ActionType`、`AuditDetail`、`Change`、query DTO | `apps/web/service/auditlog/types.go` | `spec.md` 的 `object_audit_log` / detail 约定 |
| 公共服务 | `Log` / `BatchLog` / `ListByQuery` | `apps/web/service/auditlog/service.go` | OP 审计的 `apps/web/op/service/audit.go` |
| diff 引擎 | 结构化变更生成、排除字段、敏感字段掩盖 | `apps/web/service/auditlog/diff.go` | `research.md` 对 PostHog 结构化 diff 的总结 |
| 对象注册表 | 各对象的字段投影、排除规则、mask 规则、对象名提取 | `apps/web/service/auditlog/registry.go` | 现有 `AssetOperator` 只覆盖 Chart / Dashboard，不能作为完整真相 |
| 持久化 DAO | `object_audit_log` 表 CRUD + 批量插入 + 分页查询 | `apps/web/dao/auditlog/object_audit_log.go` | `meta.metric_define_history` / `op_operation_log` DAO 风格 |
| OP 查询接口 | 内部排障查询 API | `apps/web/op/controller/project_audit.go` + `apps/web/op/service/project_audit.go` | 现有 OP `ListByQuery` 模式 |
| 迁移任务 | AB / Metric 历史导入，新表回填 | `script/migration/scripts/...` + `apps/web/cmd/...` 或等价任务入口 | 现有 OP 迁移脚本模式 |

### 4.2 数据模型

项目内标准落点继续采用 `spec.md` 已定义的 `meta.object_audit_log`：

- 主键：`id`
- 主查询键：`(object_type, object_id, created_at DESC)`
- 审计主体：`object_type`、`object_id`、`object_name`，其中 `object_name` 是展示快照
- 归因信息：`operator_id`、`operator_name`、`source`、`created_at`，其中 `operator_name` 是展示快照，`created_at` 明确定义为操作时间 / 审计事件时间
- 表在 project schema（meta）内，`project_id` 字段冗余，不设此列
- 详情：`detail_version` + `detail_payload`，其中 `detail_version` 是 payload schema version，V1 固定写 `1`

#### 4.2.1 枚举规范

所有 `object_audit_log` 的枚举值采用字符串类型（非 int），自描述，不依赖代码注释。

**ObjectType** — 被审计对象的类型，UPPER_SNAKE_CASE：

```go
const (
    ObjectTypeChart           = "CHART"
    ObjectTypeDashboard       = "DASHBOARD"
    ObjectTypeCohort          = "COHORT"
    ObjectTypeExperiment      = "EXPERIMENT"
    ObjectTypeFeatureGate     = "FEATURE_GATE"
    ObjectTypeFeatureConfig   = "FEATURE_CONFIG"
    ObjectTypeMetric          = "METRIC"
    ObjectTypeTrackedEvent    = "TRACKED_EVENT"
    ObjectTypeVirtualEvent    = "VIRTUAL_EVENT"
    ObjectTypeEventProperty   = "EVENT_PROPERTY"
    ObjectTypeUserProperty    = "USER_PROPERTY"
    ObjectTypeVirtualProperty = "VIRTUAL_PROPERTY"
    ObjectTypePipeline        = "PIPELINE"
)
```

命名规则：
- 与业务域对象名对齐，不使用缩写（如 `EXP` → `EXPERIMENT`）
- 与 OP audit 的 `target_type`（小写，运维域）不做对齐，两者作用域不同

**action_type** — 操作类型，全小写描述性字符串。**所有常量统一定义在 `auditlog/types.go`**，模块不自行定义。各模块按需使用已有常量或在此文件注册新常量（PR review gate 确保语义不重叠）。

```go
// apps/web/service/auditlog/types.go

// 核心 CRUD（默认提供）
ActionTypeCreate = "create"
ActionTypeUpdate = "update"
ActionTypeDelete = "delete"
ActionTypeCopy   = "copy"

// AB 状态变更
ActionTypeDebug        = "debug"
ActionTypeOnline       = "online"
ActionTypeOffline      = "offline"
ActionTypeRelease      = "release"
ActionTypeVariantChange = "variant_change"

// Dashboard 关系变更
ActionTypeRelate   = "relate"
ActionTypeUnrelate = "unrelate"

// Pipeline
ActionTypeStop = "stop"
```

设计要点：
- **守口到审计模块**：一个文件看完所有 action_type，查询端可直接按这些值过滤
- **默认 4 个基础类型**：`create` / `update` / `delete` / `copy` 覆盖大部分场景
- **模块注册扩展**：需要特定语义时在此文件加 const，PR review 确认无重复
- 内部操作（冲突解决）复用同 action_type（如 `offline`），用 `source="internal"` 区分

**source** — 操作来源，全小写：

```go
const (
    SourceWeb      = "web"       // 用户通过 Web 界面操作
    SourceOpenAPI  = "openapi"   // 用户通过 OpenAPI/MCP 接口操作
    SourceInternal = "internal"  // 系统内部操作（冲突解决、迁移回调等）
    SourceBackfill = "backfill"  // 历史迁移回填
)
```

`web` 和 `openapi` 都代表用户主动操作，通过 `operator_id` 可定位操作人。`internal` 代表系统行为，但 `operator_id` 应继承触发该内部操作的原始用户。`backfill` 用于历史数据迁移，没有对应的操作人。

`detail_payload` 使用 **BYTEA + LZ4 压缩**：

1. SerializeDetail 先序列化为 JSON（使用稳定 envelope，含 `{version, changes, extra}`）
2. 超阈值（单条序列化后 > 64KB）时做 LZ4 压缩
3. 若压缩后仍超限，按字段投影规则截断，并把 `extra.truncated_fields`、`extra.payload_hash` 一起写入 warning 上下文

BYTEA 是 LZ4 压缩后二进制数据的自然选择，且与”detail 不直接在 DB 层搜索”的设计一致。如果只存 JSON，jsonb 也有可读性优势；但设计上 detail 不在数据库过滤/搜索，BYTEA 无实际阻碍。

### 4.3 写入模型与一致性等级

统一公共契约保持精简：

```go
Log(ctx, input) error
BatchLog(ctx, inputs) error
ListByQuery(ctx, query) ([]AuditLogItem, int64, error)
```

其中 `Log` / `BatchLog` 只负责：

1. 校验公共字段
2. 填充 object / operator 展示快照
3. 生成或接收审计事件时间
4. 序列化 detail，并写入当前 `detail_version`
5. 调 DAO 写入

#### 4.3.1 一致性等级：调用方按需决定

审计写入的一致性不是系统级策略，而是**每个调用方在调用时按需决定的参数**。`Log`/`BatchLog` 接口接受一个 `consistency` 参数：

```go
type AuditConsistency int

const (
    // 核心字段+detail 都失败则回滚业务事务
    ConsistencyStrong AuditConsistency = iota
    // 核心字段失败则回滚，detail 失败允许降级（写空/截断+warning）
    ConsistencyCore AuditConsistency = iota
    // 失败只记 warning，不阻塞业务
    ConsistencyBestEffort AuditConsistency = iota
)

Log(ctx, input, consistency) error
BatchLog(ctx, inputs, consistency) error
```

三种一致性等级的含义：

| 等级 | 核心字段 | detail_payload | 适合场景 |
|------|---------|---------------|---------|
| `Strong` | 失败→回滚 | 失败→回滚 | 删除/权限变更/发布等高风险操作 |
| `Core` | 失败→回滚 | 失败→降级（写空+warning） | 常规 CRUD，排障核心场景 |
| `BestEffort` | 失败→warning | 失败→warning | 非关键操作，metadata 长尾，批量低风险变更 |

核心字段定义为：`object_type`, `object_id`, `action_type`, `operator_id`, `source`, `created_at`（表在 project schema 内，无 `project_id` 列）。

**各对象域的推荐一致性等级（调用方应在接入时显式传入，不自定则不予备案）：**

| 一致性等级 | 推荐对象/场景 |
|-----------|-------------|
| `Strong` | AB status→Online/Release、Dashboard/Cohort/Chart 删除 |
| `Core` | 资产对象常规 update、AB 常规 update/copy、Metadata 所有 CRUD |
| `BestEffort` | AB 内部冲突解决（internalOffline/internalDelete）、历史迁移回填、低优先级批量操作 |

这样设计让一致性策略透明可追溯，避免”一刀切强审计过于刚硬、整个 best-effort 又怕关键操作丢记录”的两难。

#### 4.3.2 BatchLog 原子性

当单次业务操作涉及多个对象时（如 `CopyDashboard` 同时创建新 dashboard 和多个 chart），需要产生多条审计记录。

`BatchLog` 的原子性约定：
- 同一业务事务内的多条审计记录，通过 `BatchLog` 一次性写入
- `BatchLog` 在 `ConsistencyStrong` / `ConsistencyCore` 模式下：任一记录的核心字段写入失败 → 整体失败 → 业务事务回滚
- `BatchLog` 在 `ConsistencyBestEffort` 模式下：失败折入 warning，不影响业务事务
- 不允许”dashboard 的 copy 记录成功但 chart 的 copy 记录缺失”

#### 4.3.3 审计日志保留策略

V1 不实现自动清理，但预留扩展位：

- `created_at` 字段天然支持按时间范围分区
- 索引以 `(object_type, object_id, created_at DESC)` 结尾，partition pruning 可直接利用
- 未来需要 TTL 清理时，按 `created_at` 月份分区，直接 drop 旧分区即可
- V1 上线后根据实际写入速率评估是否需要分区，不在首批实现

global 管理审计继续沿用 `LogWithFallback`，这条策略**不复用到项目对象标准审计**。详见 [plan-org.md](./plan-org.md)。

### 4.4 diff 引擎

建议统一提供：

```go
ChangesBetween(oldValue, newValue, objectType) []Change
```

核心原则：

1. **业务对象先投影，再 diff**，不能把现有 GORM struct 直接序列化进审计
2. **排除噪音字段**，例如 `id`、`created_at`、`updated_at`、`created_by`、`updated_by`，以及纯缓存字段、仅由后台异步刷新的派生字段
3. **默认掩盖敏感字段**：各类 integration/config 中的敏感配置、用户邮箱、密码、token、密钥类字段，统一替换为 `"masked"`
4. **只保留稳定字段名**，避免未来业务结构调整把历史读挂

推荐每个对象类型都注册一个 `AuditProjectionBuilder`，输出稳定 map：

- Chart：`name`、`description`、`query_type`、`api_request`、`config`、`version`
- Dashboard：`name`、`description`、`version`、`chart_ids`、`layout_overrides`
- Cohort：`name`、`description`、`rule_config`、`calc_mode`、`calc_time`、`cohort_version`
- AB：公共字段 + 领域摘要，不直接把整个 `details` 原样落盘
- Metric / Event / Property：只投影用户真正关心的配置字段

### 4.5 查询接口

V1 推荐在 OP 或等价内部链路提供对象历史查询接口，最小输入输出如下：

**Request**

```json
{
  "object_type": "CHART",
  "object_id": 123,
  "page": 1,
  "page_size": 20
}
```

**Response**

```json
{
  "total": 2,
  "items": [
    {
      "id": 9001,
      "object_type": "CHART",
      "object_id": 123,
      "object_name": "DAU 趋势",
      "action_type": "update",
      "operator_id": 7,
      "operator_name": "alice",
      "source": "web",
      "detail_version": 1,
      "detail": {
        "name": "DAU 趋势",
        "changes": [
          {"field": "name", "action": "changed", "before": "DAU", "after": "DAU 趋势"}
        ]
      },
      "created_at": "2026-06-29T10:00:00+08:00"
    }
  ]
}
```

V1 不承诺：

- `operator_id` 维度筛选
- 跨项目检索
- 全文检索 detail

补充约束：

- 查询权限以 OP / 内部排障权限为准，不能只靠 `object_id` 直读
- 表在 project schema 内天然隔离，不额外校验 project scope
- 响应中的 `detail` 必须保持掩码后的读视图；不允许在 read path 重新暴露被 masking 的原值

### 4.6 历史迁移

迁移原则：

1. **一次性复制旧历史**
2. **迁移后查询只读新审计表**
3. **升级后新写入只写新审计表**
4. **旧字段或旧表保留，不做双写**

迁移源建议如下：

| 历史源 | 是否迁移到新项目审计 | 原因 |
|---|---|---|
| `ab_feature_flag.details.operation_records` | 是 | 这是 AB 唯一真实历史源，必须统一收口 |
| `meta.metric_define_history` | 是 | 属于明确历史债务，且 Metric 已纳入统一规范 |
| `meta.asset_behavior` | 否 | 当前实际只有 VIEW 有效，且不具备可靠审计语义 |
| `global.op_operation_log` | 否 | 作用域不同（OP 配置操作），继续留在 global 管理审计链路；客户侧管理操作已进入 `mgmt_audit_log` |

迁移时的映射规则：

- AB `CREATE` → `create`
- AB `UPDATE` / `DEBUG` / `ONLINE` / `OFFLINE` / `RELEASE` / `VARIANT_CHANGE` → `update`
- AB `COPY` → `copy`
- AB `DELETE` → `delete`
- Metric history → `update`，`changes = [{field: "define", before, after}]`

对于历史 AB 记录缺少 before / after 的场景：

- 允许 `changes` 为空
- 必须保留 `name`、`source = "internal"`、`extra.legacy_source`
- `operator_name` 在迁移时尽量回填；查不到则允许空字符串
- `created_at` 直接回填原始 `OperateAt`

迁移必须具备**幂等去重键**，建议最少包含：

- `legacy_source`
- `object_type`
- `object_id`
- `legacy_action_type`
- `operator_id`
- `created_at`

对于 Chart / Dashboard / Cohort / Event / Property 这类**当前没有可靠旧操作历史源**的对象：

- 不从 `asset_behavior`、访问记录或其他派生数据中“伪造历史”
- 历史迁移范围只覆盖**真实存在且可解释的旧操作记录**
- 上线后从新审计表开始连续记账

### 4.7 审计场景全量目录

以下是对审计范围内所有操作的完整盘点。每条记录进入 `meta.object_audit_log`，标注 `[G]` 表示该操作同时影响 global 审计域（但项目内审计行仍写入 `object_audit_log`）。

#### 4.7.1 资产对象

**CHART**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建 Chart | `create` | web/openapi/mcp | `name`, `type`, `query_type`, `api_request`, `config`, `version` | 若关联 dashboard 则记 `dashboard_ids` | `config` 可能需要大字段投影 |
| 更新 Chart | `update` | web/openapi/mcp | 仅变更字段的 before/after | 无 | 若只有噪音字段变化则跳过 |
| 删除 Chart | `delete` | web/openapi/mcp | 至少 `name` 快照 | `snapshot.version`, `snapshot.dashboard_ids` | 读 DB 获取删除前快照 |
| 批量删除 | `delete` × N | web/openapi/mcp | 同上，每条对象一行 | `extra.batch_id`, `extra.batch_index` | 通过 `BatchLog` 写入，共享事务 |
| 复制 Chart | `copy` | web/openapi/mcp | 可为空 | `extra.source_object_id`, `extra.source_object_name`, `extra.target_name` | copy 产生新对象 ID |

**DASHBOARD**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建 Dashboard | `create` | web/openapi/mcp | `name`, `description`, `version` | 若含初始 chart 则记 `chart_ids` | |
| 更新 Dashboard（含 chart/layout 变更）| `update` | web/openapi/mcp | `name`, `description`, `chart_ids`, `layout_overrides` 中有变更的字段 | 无 | `chart_ids` diff 需处理空集合、重复 ID |
| 轻量修改 meta | `update` | web/openapi/mcp | `name` 或 `description` 的 before/after | 无 | `PatchDashboardMeta` |
| 仅更新 layout | `update` | web/openapi/mcp | `layout_overrides` 变更 | 无 | `SetDashboardChartLayouts` |
| 添加 Chart 到 Dashboard | `relate` | web/openapi/mcp | `chart_ids` before/after | `extra.added_chart_ids` | 记在 Dashboard 审计中；被添加的 Chart 自身不产生审计 |
| 从 Dashboard 移除 Chart | `unrelate` | web/openapi/mcp | `chart_ids` before/after | `extra.removed_chart_ids` | 同上 |
| Chart 添加到多个 Dashboard | `update` × N | web/openapi/mcp | 每个 Dashboard 产生一条 | 同上 | `AddChartToMultipleDashboards` |
| 删除 Dashboard | `delete` | web/openapi/mcp | 至少 `name` 快照 | `snapshot.version`, `snapshot.chart_count` | |
| 批量删除 | `delete` × N | web/openapi/mcp | 同上 | `extra.batch_id` | |
| 复制 Dashboard | `copy` | web/openapi/mcp | 可为空 | `extra.source_object_id`, `extra.copy_charts` | 若 `copyCharts=true` 则每个被复制的 Chart 也产一条 |

**COHORT**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建 Cohort | `create` | web/openapi/mcp | `name`, `description`, `rule_config`, `calc_mode`, `calc_time`, `cohort_version` | `extra.scheduler_job_id` | 含调度任务创建 |
| 更新 Cohort（含规则变更）| `update` | web/openapi/mcp | 变更字段 before/after | 若调度参数变更则记 `extra.scheduler_job_updated` | |
| 更新 Cohort（仅调度参数） | `update` | web/openapi/mcp | `calc_mode`/`calc_time` before/after | `extra.scheduler_job_action: updated/created` | |
| 删除 Cohort | `delete` | web/openapi/mcp | 至少 `name` 快照 | `snapshot.rule_summary`, `extra.scheduler_job_deleted` | 读 DB 获取删除前快照 |
| Cohort 手动重算触发 | — | — | — | — | **不进入审计表**，这是 create/update 的内部副作用 |
| Cohort 定时重算执行 | — | — | — | — | **不进入审计表**，是 cron 调度回调，属系统运维日志 |
| Cohort 清理任务 | — | — | — | — | **不进入审计表**，属系统运维日志 |

#### 4.7.2 AB 对象

**EXPERIMENT**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建实验 | `create` | web/openapi/mcp | `ffkey`, `name`, `status`, `enabled`, `traffic`, `version` | `extra.subject_id`, `extra.layer_id` | |
| 更新实验配置 | `update` | web/openapi/mcp | 变更字段 before/after | 若 `details` 变更则仅投影摘要 | |
| 状态变更：Debug | `debug` | web/openapi/mcp | `status` before/after | `extra.conflict_resolution`（若触发了冲突解决） | |
| 状态变更：Online | `online` | web/openapi/mcp | `status`, `enabled` before/after | `extra.conflict_resolution`, `extra.exposure_property_created` | |
| 状态变更：Offline | `offline` | web/openapi/mcp | `status`, `enabled` before/after | `extra.buckets_released` | 释放 layer buckets |
| 状态变更：Delete | `delete` | web/openapi/mcp | `status` before/after, 至少 `name` 快照 | `extra.buckets_released`, `extra.references_removed` | |
| Release | `release` | web/openapi/mcp | `status`, `release_plan` before/after | `extra.release_scope`（流量分配变更摘要） | 涉及流量分配和 bucket 释放 |
| 复制实验 | `copy` | web/openapi/mcp | 可为空 | `extra.source_object_id`, `extra.source_ffkey` | |
| 内部下线（冲突解决）| `offline` | internal | `status`, `enabled` before/after | `extra.reason: conflict_resolution`, `extra.conflict_ffkey` | 必须记录，source=`internal` |
| 内部删除（冲突解决）| `delete` | internal | `status` before/after, 至少 `name` 快照 | `extra.reason: conflict_resolution`, `extra.conflict_ffkey` | 必须记录，source=`internal` |

**FEATURE_GATE**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建开关 | `create` | web/openapi/mcp | `ffkey`, `name`, `status`, `enabled`, `traffic`, `version` | 无 | |
| 更新开关配置 | `update` | web/openapi/mcp | 变更字段 before/after | 无 | |
| 状态变更：Debug/Online/Offline | `debug`/`online`/`offline` | web/openapi/mcp | `status`, `enabled` before/after | `extra.conflict_resolution` | |
| 状态变更：Delete | `delete` | web/openapi/mcp | 至少 `name` 快照 | `extra.references_removed` | |
| Release | `release` | web/openapi/mcp | `status`, `release_plan` before/after | 无 | |
| 复制开关 | `copy` | web/openapi/mcp | 可为空 | `extra.source_object_id` | |
| 内部下线/删除（冲突解决）| `offline`/`delete` | internal | 同 Experiment | `extra.reason: conflict_resolution` | 必须记录 |

**FEATURE_CONFIG**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建配置 | `create` | web/openapi/mcp | `ffkey`, `name`, `status`, `enabled`, `traffic`, `version`, 变体摘要 | 无 | |
| 更新配置 | `update` | web/openapi/mcp | 变更字段 before/after | 无 | |
| 变体变更 | `variant_change` | web/openapi/mcp | 变体字段 before/after | `extra.changed_variant_keys` | 高价值场景，必须能看出改了哪个 variant |
| 状态变更/Release/复制 | 同 Experiment | | | | |
| 内部冲突解决 | 同 Experiment | internal | | | 必须记录 |

#### 4.7.3 元数据对象

所有元数据对象遵循统一模式，差异仅在 `changes[]` 字段列表。

**METRIC**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建指标 | `create` | web/openapi/mcp | `name`, `description`, `define`, `precision` | 无 | `define` 是审计核心字段 |
| 更新指标 | `update` | web/openapi/mcp | 变更字段 before/after | 若仅 `define` 变更则 `changes=[{field:"define",...}]` | 现有 `metric_define_history` 将迁移到新审计表 |
| 删除指标 | `delete` | web/openapi/mcp | 至少 `name` 快照 | `snapshot.define_summary` | |

**TRACKED_EVENT**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建事件 | `create` | catalog/web/openapi | `name`, `display_name`, `description`, 事件分类/核心属性摘要 | 外部绑定信息 | |
| 更新事件 | `update` | catalog/web/openapi | 变更字段 before/after | 无 | |
| 删除事件 | `delete` | catalog/web/openapi | 至少 `name` 快照 | `snapshot.display_name`, `snapshot.category` | |

**VIRTUAL_EVENT**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建虚拟事件 | `create` | web/openapi | `name`, `display_name`, `virtual_define` 投影摘要 | 引用对象摘要 | `virtual_define` 需稳定化投影 |
| 更新虚拟事件 | `update` | web/openapi | 变更字段 before/after | 无 | |
| 删除虚拟事件 | `delete` | web/openapi | 至少 `name` 快照 | `snapshot.define_summary` | |

**EVENT_PROPERTY / USER_PROPERTY**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建属性 | `create` | catalog/web/openapi | `name`, `display_name`, `data_type`, `description`, 关联事件/作用域摘要 | 绑定范围 | |
| 更新属性 | `update` | catalog/web/openapi | 变更字段 before/after | 无 | 若字段含敏感值统一掩盖 |
| 删除属性 | `delete` | catalog/web/openapi | 至少 `name` 快照 | `snapshot.display_name`, `snapshot.data_type` | |

**VIRTUAL_PROPERTY**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建虚拟属性 | `create` | web/openapi | `name`, `display_name`, `data_type`, `virtual_define` 投影摘要 | `extra.table_type` | |
| 更新虚拟属性 | `update` | web/openapi | 变更字段 before/after | 无 | |
| 删除虚拟属性 | `delete` | web/openapi | 至少 `name` 快照 | `snapshot.define_summary` | |

#### 4.7.4 基础设施对象

**PIPELINE**

Pipeline 目前**完全没有操作审计**。现有的执行级日志（`exec_info` JSONB、`pipeline_batch_export_run`、`pipeline_batch_export_backfill`）只记录系统运行历史，不记录谁创建/更新/删除了 pipeline。以下 CRUD 操作为用户主动操作，需接入审计：

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建 Pipeline | `create` | web/openapi | `name`, `type`, `pipeline_type`, `data_type` | `extra.work`, `extra.data_source_id` | |
| 更新 Pipeline | `update` | web/openapi | 变更字段 before/after | 无 | 包括名称/描述更新 |
| 删除 Pipeline | `delete` | web/openapi | 至少 `name` 快照 | `snapshot.pipeline_type`, `snapshot.work` | 软删除 |
| 停止 Pipeline | `stop` | web/openapi/mcp | `exec_status` before/after | `extra.stop_reason` | 用户主动停止 |

以下**不进入** `object_audit_log`：
- Pipeline Process（系统级执行，已有 `exec_info` 和 batch_export_run 跟踪）
- Pipeline callback（AB target 状态同步，属基础设施层状态变更）

#### 4.7.5 明确不进入项目审计表的操作

以下操作有变更，但**不写入** `object_audit_log`：

| 操作 | 不记录原因 | 替代落点 |
|------|-----------|---------|
| `asset_behavior` 的 VIEW/MODIFY/DELIVER 记录 | 分析/热度用途，非审计语义 | 保持现有 `asset_behavior` 表 |
| Cohort 定时重算执行（RunCohortJob cron 回调）| 系统自动运维操作，非用户触发 | scheduler 自身日志 |
| Cohort 清理任务（cohort-clean cron）| 系统维护操作 | scheduler 自身日志 |
| AB target pipeline 状态同步（pipeline callback）| 基础设施层状态变更 | 可考虑后续加入 `object_audit_log`，V1 先不做 |
| AB 调度报告任务创建/停止 | 已在 Experiment/Update status→Online/Offline 的 `extra` 中引用 | 不单独成行 |
| Asset 收藏（Add/Remove）| 轻量交互，排障价值低 | 保持现有 `asset_favorite` 表 |
| Asset 权限变更 | V1 先不做，后续可扩展 | 无当前落点 |
| 项目成员增删/角色变更 | global 管理审计域 | `global.mgmt_audit_log` |
| 组织成员管理 | global 管理审计域 | `global.mgmt_audit_log` |

## 5. 接入策略

### 5.1 公共接入原则

接入不依赖 `AssetOperator` 全覆盖，因为现状只注册了 Chart / Dashboard，且接口只覆盖 CRUD。

所以写入分两类：

1. **可借已有对象服务集中接入的路径**：在对象 service 成功路径直接调用 `auditlog.Log`
2. **非 CRUD 或分发型路径**：在具体业务 service 中手工记录，不能等待 infra 自动覆盖

### 5.2 每类对象的接入落点

| 对象域 | 主要接入点 |
|---|---|
| Chart | `apps/web/service/chart/chart.go` 的 `CreateChartWithDashboards`、`UpdateChart`、`Delete`、`BatchDelete`、`CopyCharts` |
| Dashboard | `apps/web/service/dashboard/dashboard.go` 的 `CreateDashboard`、`UpdateDashboardWithChartsAndLayouts`、`PatchDashboardMeta`、`SetDashboardChartLayouts`、`DeleteDashboard`、`BatchDeleteDashboard`、`CopyDashboard`、`AddChartsToDashboard`、`RemoveChartsFromDashboard` |
| Cohort | `apps/web/service/cohort/cohort_service.go` 的 `CreateRuleCohort`、`UpdateRuleCohort`、`DeleteCohort` |
| AB | `apps/web/service/ab/ab_exp.go`、`ab_gate.go`、`ab_config.go` 的 Create / Update / StatusUpdate / StatusRelease / Copy |
| Metric | `apps/web/service/metadata/metric.go` 的 `CreateMetric`、`UpdateMetric`、`DeleteMetric` |
| Event / Property | `apps/web/service/metadata/*.go` 的 Create / Update / Delete 路径 |
| Pipeline | `apps/web/service/pipeline/pipeline.go` 的 `Create` / `Update` / `Delete` / `Stop` |

### 5.3 明确不依赖的基础设施

以下基础设施不能作为审计覆盖的判断标准：

| 基础设施 | 当前现状 | 不能当真相的原因 |
|----------|---------|----------------|
| `AssetOperator` | 只注册了 Chart / Dashboard，接口只含 CRUD | 覆盖不到 Cohort、AB、copy、status_change、relate/unrelate |
| `asset_behavior` | 主要是 view 行为，modify / delete / add 基本死代码 | 不是可靠审计系统 |
| AB 自带历史页 | 只能看 AB，且数据嵌在 JSONB 里 | 不能代表统一审计已完成 |
| Metric history 表 | 只覆盖 define 变更 | 不能代表元数据对象已统一审计 |
| Pipeline `exec_info` / `batch_export_run` | 仅系统执行日志 | 不记录谁做了 CRUD 操作 |

## 6. 交付阶段

### Phase 0：基础设施

- 建 `object_audit_log` 表
- 建 `auditlog` 域类型、DAO、公共服务、序列化器、diff 引擎
- 建 OP / 内部查询 API
- 项目对象 live-write 按调用方传入的 `consistency` 参数执行，高风险操作推荐 `Strong`，常规 CRUD 推荐 `Core`；global / OP audit 继续保留 fallback 风格

### Phase 1：高价值对象 + 历史迁移

- 接入 Chart / Dashboard / Cohort
- 接入 Experiment / Feature Gate / Feature Config
- 接入 Metric
- 完成 AB / Metric 历史导入
- 打通对象维度查询闭环

这是能满足当前排障主场景的最小可用版本。

### Phase 2：metadata 长尾对象

- 接入 `TRACKED_EVENT`
- 接入 `VIRTUAL_EVENT`
- 接入 `EVENT_PROPERTY`
- 接入 `USER_PROPERTY`
- 接入 `VIRTUAL_PROPERTY`

这部分使用同一套模型，不另起规范，但可以在落地顺序上滞后于 P0/P1 对象。

Phase 0 先建底座与查询，Phase 1 打通项目对象闭环，Phase 2 扩展 metadata 长尾。不允许把所有 phase 绑成一次总开关上线。

> 组织/项目管理审计和账号活跃字段为独立链路，见 [plan-org.md](./plan-org.md) 和 [plan-account.md](./plan-account.md)，不阻塞 Phase 0/1/2。

## 7. 评审结论（原待评审议题）

1. **审计一致性等级**：调用方在 `Log`/`BatchLog` 调用时按需传入 `consistency` 参数决定。API 提供 `Strong` / `Core` / `BestEffort` 三级。审计域不替调用方做选择，但各模块应在接入时显式声明等级。
2. **detail 大对象字段裁剪策略**：统一采用”对象投影 -> LZ4 压缩 -> 截断并记录 `truncated_fields` / `payload_hash`”三段式。

## 8. 验证方案

### 8.1 单测

- diff 引擎：字段排除、敏感字段掩盖、create/update/delete/copy 四类 envelope
- detail 序列化：BYTEA + LZ4 压缩、超限截断
- 历史迁移映射：AB 操作类型映射、Metric define 映射、空 operator_name 兜底

### 8.2 集成测试

- Chart / Dashboard / Cohort / AB / Metric 的成功路径写审计
- Phase 2 元数据对象 `TRACKED_EVENT` / `VIRTUAL_EVENT` / `EVENT_PROPERTY` / `USER_PROPERTY` / `VIRTUAL_PROPERTY` 的 create / update / delete 写审计
- OP / 内部对象查询接口分页、排序、过滤
- OP / 内部对象查询接口的 project scope 鉴权、掩码字段读取、删除后对象查询
- 迁移脚本重复执行的幂等性

### 8.3 故障注入

- 审计 DAO insert 失败
- detail 超大
- operator 信息缺失
- 迁移批次重复执行
- OP 查询权限不足

这里必须对照传入的 `consistency` 参数，验证主业务是回滚还是继续成功。

### 8.4 尚未锁定、需对象 owner 确认的边界

1. Chart `config` 的稳定投影边界——投影哪些字段、排除哪些
2. Dashboard `layout_overrides` 是否全量记录还是仅记录受影响 chart
3. AB `details` 需要暴露到什么摘要层级
4. ~~是否记录 IP 地址~~ — 已定：V1 不记录


> **审查历史**: 本 plan 已通过 autoplan 审查（CEO + Eng），审查记录已归档。

