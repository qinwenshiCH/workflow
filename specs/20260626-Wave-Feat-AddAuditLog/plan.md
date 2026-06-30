<!-- /autoplan restore point: /Users/wenshiqin/.gstack/projects/qinwenshiCH-workflow/main-autoplan-restore-20260629-155919.md -->

# 技术方案：项目内对象操作审计日志

## 1. 目标

本方案把 [spec.md](./spec.md) 的产品要求落成可评审、可拆工的技术计划，重点解决 3 条并行链路：

1. **项目内对象统一审计规范**：为 Chart / Dashboard / Cohort / AB / Metric / Event / Property 等对象提供统一写入、统一查询、统一历史迁移。
2. **组织 / 项目级管理审计**：继续保留独立于 `project_audit_log` 的 global 级审计链路，不强塞进项目对象标准表。
3. **账号活跃时间**：`last_login_at` / `last_logout_at` / `last_active_at` 落在 `account` 表或等价账号主表，不进入项目对象审计表。

当前设计前提来自以下已确认共识：

- V1 优先服务**内部排障**，查询主视角是 `object_type + object_id`，不是按人或按组织做分析型报表。
- detail 采用**结构化 diff 优先**的稳定 envelope，必要时允许 `extra` / `snapshot` 补充。
- detail **不使用 JSONB**，也**不直接持久化当前业务结构体**。
- 升级后**新操作只写新审计记录**，旧 AB / Metric / Wave 历史记录做一次性复制，旧字段或旧表保留不删。
- V1 **不新增官方产品通用审计页面**；除 AB / Metric 既有查看能力外，其余查看能力优先通过 OP / 内部接口承接。

## 2. 范围切分

### 2.1 项目内对象标准审计

这一层是本次主线，统一落到 `meta.project_audit_log`。

首批对象范围：

- 资产对象：`CHART`、`DASHBOARD`、`COHORT`、`EXPERIMENT`、`FEATURE_GATE`、`FEATURE_CONFIG`、`PIPELINE`
- 元数据对象：`METRIC`、`TRACKED_EVENT`、`VIRTUAL_EVENT`、`EVENT_PROPERTY`、`USER_PROPERTY`、`VIRTUAL_PROPERTY`

### 2.2 global 级管理审计

这一层覆盖组织 / 项目管理动作，继续允许与项目对象标准审计分层：

- 当前已有成熟基础设施：`apps/web/op/service/audit.go`
- 当前已有明确动作：`update_org_config`、`update_project_init_quota`、`update_project_quota`
- 这条链路的目标是“责任不缺失”，不是要求马上并表

### 2.3 账号活跃字段

这一层只解决账号最近状态，不进入统一审计查询模型：

- 登录成功写 `last_login_at`
- 登出成功写 `last_logout_at`
- 会话活跃写 `last_active_at`

`last_active_at` 不能按每个请求直接落库，建议做**节流更新**，例如同一账号每 5 到 15 分钟最多刷新一次。
本轮 review 收敛为：**同一账号每 15 分钟最多刷新一次**，优先采用同步节流更新，不引入额外批处理链路。

## 3. 非目标

- V1 不做官方产品端的新通用审计 UI
- V1 不优先支持按 `operator_id`、按组织范围、按跨项目的审计分析
- V1 不把组织 / 项目管理动作强行并入 `project_audit_log`
- V1 不从 `asset_behavior` 反推“谁改了什么”，因为它本质不是可靠审计源

## 4. 推荐架构

### 4.1 模块划分

建议新增一套独立于 OP 审计的项目对象审计模块，命名上避免和 `apps/web/op/service/audit.go` 混淆，文中统一用 `auditlog` 代称。

| 模块 | 职责 | 建议落点 | 参考现状 |
|---|---|---|---|
| 审计域类型 | `ObjectType`、`ActionType`、`AuditDetail`、`Change`、query DTO | `apps/web/service/auditlog/types.go` | `spec.md` 的 `project_audit_log` / detail 约定 |
| 公共服务 | `Log` / `BatchLog` / `ListByQuery` | `apps/web/service/auditlog/service.go` | OP 审计的 `apps/web/op/service/audit.go` |
| diff 引擎 | 结构化变更生成、排除字段、敏感字段掩盖 | `apps/web/service/auditlog/diff.go` | `research.md` 对 PostHog 结构化 diff 的总结 |
| 对象注册表 | 各对象的字段投影、排除规则、mask 规则、对象名提取 | `apps/web/service/auditlog/registry.go` | 现有 `AssetOperator` 只覆盖 Chart / Dashboard，不能作为完整真相 |
| 持久化 DAO | `project_audit_log` 表 CRUD + 批量插入 + 分页查询 | `apps/web/dao/auditlog/project_audit_log.go` | `meta.metric_define_history` / `op_operation_log` DAO 风格 |
| OP 查询接口 | 内部排障查询 API | `apps/web/op/controller/project_audit.go` + `apps/web/op/service/project_audit.go` | 现有 OP `ListByQuery` 模式 |
| 迁移任务 | AB / Metric 历史导入，新表回填 | `script/migration/scripts/...` + `apps/web/cmd/...` 或等价任务入口 | 现有 OP 迁移脚本模式 |

### 4.2 数据模型

项目内标准落点继续采用 `spec.md` 已定义的 `meta.project_audit_log`：

- 主键：`id`
- 主查询键：`(project_id, object_type, object_id, created_at DESC)`
- 审计主体：`object_type`、`object_id`、`object_name`，其中 `object_name` 是展示快照
- 归因信息：`operator_id`、`operator_name`、`source`、`created_at`，其中 `operator_name` 是展示快照，`created_at` 明确定义为操作时间 / 审计事件时间
- 详情：`detail_version` + `detail_payload`，其中 `detail_version` 是 payload schema version，V1 固定写 `1`

#### 4.2.1 枚举规范

所有 `project_audit_log` 的枚举值采用字符串类型（非 int），自描述，不依赖代码注释。

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

**action_type** — 操作类型，全小写描述性字符串。**完全开放，由各模块自行定义**，查询端直接按字符串过滤，显示端直接展示无需映射：

```go
// action_type 示例（各模块自行定义，非穷举）
"create", "update", "delete", "copy",                    // 核心 CRUD
"debug", "online", "offline", "release",                 // AB 状态变更
"variant_change",                                        // AB 变体变更
"relate", "unrelate",                                    // Dashoard 关系变更
"stop"                                                   // Pipeline 停止
```

规则只有一条：**同一语义动作不使用两个不同的 action_type**。代码审查时确认即可，不设中心枚举。

内部操作（冲突解决）的 action_type 与正常操作保持一致（如 `offline`），通过 `source="internal"` 区分。`batch_delete` 等批量操作一条对象一条 `delete`，`extra.batch_id` 关联同批。

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

建议 `detail_payload` 先按 **TEXT + 应用层序列化** 实现：

1. 默认写 JSON 文本 envelope
2. 超阈值时先做 LZ4 压缩
3. 若仍超限，再按字段投影规则截断，并把 `extra.truncated_fields`、`extra.payload_hash` 一起写入 warning 上下文

这样能满足“不用 JSONB”和“detail 结构由应用层稳定维护”两个约束。

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

核心字段定义为：`project_id`, `object_type`, `object_id`, `action_type`, `operator_id`, `source`, `created_at`。

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

global 管理审计继续允许沿用 `LogWithFallback` 一类 best-effort 包装，但这条策略**不复用到项目对象标准审计**。

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
  "project_id": 1001,
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

- 请求必须显式带 `project_id`
- 查询权限以 OP / 内部排障权限为准，不能只靠 `object_id` 直读
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
| `global.op_operation_log` | 否 | 作用域不同，继续留在 global 管理审计链路 |

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

以下是对审计范围内所有操作的完整盘点。每条记录进入 `meta.project_audit_log`，标注 `[G]` 表示该操作同时影响 global 审计域（但项目内审计行仍写入 `project_audit_log`）。

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

以下**不进入** `project_audit_log`：
- Pipeline Process（系统级执行，已有 `exec_info` 和 batch_export_run 跟踪）
- Pipeline callback（AB target 状态同步，属基础设施层状态变更）

#### 4.7.5 明确不进入项目审计表的操作

以下操作有变更，但**不写入** `project_audit_log`：

| 操作 | 不记录原因 | 替代落点 |
|------|-----------|---------|
| `asset_behavior` 的 VIEW/MODIFY/DELIVER 记录 | 分析/热度用途，非审计语义 | 保持现有 `asset_behavior` 表 |
| Cohort 定时重算执行（RunCohortJob cron 回调）| 系统自动运维操作，非用户触发 | scheduler 自身日志 |
| Cohort 清理任务（cohort-clean cron）| 系统维护操作 | scheduler 自身日志 |
| AB target pipeline 状态同步（pipeline callback）| 基础设施层状态变更 | 可考虑后续加入 `project_audit_log`，V1 先不做 |
| AB 调度报告任务创建/停止 | 已在 Experiment/Update status→Online/Offline 的 `extra` 中引用 | 不单独成行 |
| Asset 收藏（Add/Remove）| 轻量交互，排障价值低 | 保持现有 `asset_favorite` 表 |
| Asset 权限变更 | V1 先不做，后续可扩展 | 无当前落点 |
| 项目成员增删/角色变更 | global 管理审计域 | `global.op_operation_log` |
| 组织成员管理 | global 管理审计域 | `global.op_operation_log` |

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
| global 管理审计 | `apps/web/op/service/...` 现有 AuditService 链路 |
| 账号活跃 | `apps/web/controller/account/account.go` 的登录 / 登出，以及会话中间件或 account service 活跃刷新点 |
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

- 建 `project_audit_log` 表
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

### Phase 3：global / account 链路补完

- 盘点组织 / 项目管理动作的完整 inventory
- Phase 3 默认继续复用 `global.op_operation_log`，先补 inventory 与查询口径，不新增 global 级标准表
- 为 `account` 增加活跃字段并接入登录 / 登出 / 活跃刷新

虽然本轮 premise gate 保留了 **Phase 0-3 全量承诺**，但工程实现和上线顺序仍必须保持 phase-ordered：

1. Phase 0 先建底座与查询
2. Phase 1 先打通项目对象闭环
3. Phase 2 / Phase 3 再分域扩展

不允许把四个 phase 绑成一次总开关上线。

## 7. 评审结论（原待评审议题）

1. **审计一致性等级**：调用方在 `Log`/`BatchLog` 调用时按需传入 `consistency` 参数决定。API 提供 `Strong` / `Core` / `BestEffort` 三级。审计域不替调用方做选择，但各模块应在接入时显式声明等级。
2. **global 管理审计标准化深度**：Phase 3 继续沿用现有 OP 审计链路，先补 inventory 和查询口径，不同步抽象统一 global 标准表。
3. **detail 大对象字段裁剪策略**：统一采用“对象投影 -> LZ4 压缩 -> 截断并记录 `truncated_fields` / `payload_hash`”三段式。
4. **`last_active_at` 刷新策略**：同一账号 15 分钟最多刷新一次，优先同步节流更新，不引入新的批处理依赖。

## 8. 验证方案

### 8.1 单测

- diff 引擎：字段排除、敏感字段掩盖、create/update/delete/copy 四类 envelope
- detail 序列化：TEXT、压缩、超限截断
- 历史迁移映射：AB 操作类型映射、Metric define 映射、空 operator_name 兜底

### 8.2 集成测试

- Chart / Dashboard / Cohort / AB / Metric 的成功路径写审计
- Phase 2 元数据对象 `TRACKED_EVENT` / `VIRTUAL_EVENT` / `EVENT_PROPERTY` / `USER_PROPERTY` / `VIRTUAL_PROPERTY` 的 create / update / delete 写审计
- OP / 内部对象查询接口分页、排序、过滤
- OP / 内部对象查询接口的 project scope 鉴权、掩码字段读取、删除后对象查询
- 迁移脚本重复执行的幂等性
- 登录 / 登出 / 活跃刷新时间戳的节流行为

### 8.3 故障注入

- 审计 DAO insert 失败
- detail 超大
- operator 信息缺失
- 迁移批次重复执行
- OP 查询权限不足
- account 活跃刷新并发竞争

这里必须对照传入的 `consistency` 参数，验证主业务是回滚还是继续成功。

### 8.4 尚未锁定、需对象 owner 确认的边界

1. Chart `config` 的稳定投影边界——投影哪些字段、排除哪些
2. Dashboard `layout_overrides` 是否全量记录还是仅记录受影响 chart
3. AB `details` 需要暴露到什么摘要层级

## /autoplan Intake（2026-06-29）

### Review Target

- 当前 review target：本文件 `specs/20260626-Wave-Feat-AddAuditLog/plan.md`
- base branch：`main`
- restore point：`/Users/wenshiqin/.gstack/projects/qinwenshiCH-workflow/main-autoplan-restore-20260629-155919.md`

### Scope Classification

- **UI scope：No**
  - 尽管文本里多次出现 `Chart`、`Dashboard`、`layout` 等词，但这里描述的是被审计对象，而不是新页面、新组件或新交互
  - `spec.md` 已明确 V1 不新增官方产品通用审计 UI，因此这次不进入 design review 主流程
- **DX scope：No**
  - 计划里提到的 OP / 内部查询接口属于内部排障面，不是面向开发者售卖或开放的 API / SDK / CLI 产品
  - 因此这次不进入 DX review 主流程

### Review Coverage Note

- 当前 session 受“**Work ALONE. Do not spawn sub-agents**”约束
- 本机存在 `codex-cli 0.134.0`，但本次不启用额外 reviewer voice，按 **single-reviewer mode** 继续
- 这意味着本轮 `/autoplan` 会完整执行 CEO / Eng 的方法论，但不会引入外部 AI reviewer 作为第二声音

## CEO Review — Phase 1 Step 0

### 0A. Premise Challenge

#### 当前计划真正要解决的问题

这份计划真正要解决的，不是“Wave 现在就要做一个企业级合规审计产品”，而是：

1. 把项目内对象的关键变更记录从分散状态收口为**统一规范**
2. 让内部同学在对象出问题时，能沿着**单对象历史**快速回答 `who / what / when / source`
3. 把 AB / Metric / Wave 既有历史债务复制进新规范，结束“多套历史拼接排障”

#### 更直接的 framing

比“做一个大而全的审计体系”更直接的 framing 是：

> 先交付一个**项目内对象审计底座**：
> 统一写入契约、统一对象历史查询、统一历史复制。
> 其他能力只要不阻塞这个目标，就按 phase 明确后移。

这个 framing 更贴近当前已确认的价值排序：

- P0：事故止损 / 根因定位
- P1：组织放权 / 权责清晰
- P1：未来企业信任可扩展
- P2：治理底座

#### 如果什么都不做

如果继续维持现状，排障仍然要同时翻：

- `global.op_operation_log`
- AB 行内 `details.operation_records`
- `meta.metric_define_history`
- 基本不具备审计语义的 `asset_behavior`

结果是：

- 内部排障仍然是“查库 + 猜测 + 问人”
- AB / Metric 继续停留在孤岛实现
- 新对象类型如果继续各自长历史机制，后续统一成本会更高

#### 结论

当前计划的**方向是对的**，但必须把“程序级 roadmap”与“第一批交付 slice”分开。否则“统一审计规范”很容易在实现层面变成同时推动 3 条大链路的宽项目。

### 0B. Existing Code Leverage

| 子问题 | 现有代码 / 机制 | 可直接复用的部分 | 不能直接复用的部分 |
|---|---|---|---|
| global 管理审计 | `apps/web/op/service/audit.go` 已有 `Log` / `LogWithFallback` / `ListByQuery` | 写入 DTO、查询接口形态、operator name 回填、fallback 语义 | 目标表和领域模型是 OP 运维域，不适合直接承担项目对象审计 |
| 项目对象成功写路径 | Chart / Dashboard / Cohort / AB / Metadata service 已集中承载核心 create / update / delete / status 逻辑 | 这些 service 是最自然的 live-write 接入点 | 目前没有统一审计模块，也没有稳定 detail envelope |
| AB 历史 | AB create/update/status 流会把 `FFOperationRecord` 追加到 `details.operation_records` | 这是 AB 唯一真实历史源，适合做一次性 backfill | 历史嵌在行内 JSON 里，不可统一查询，也没有稳定 before / after |
| Metric 历史 | `apps/web/service/metadata/metric.go` 在 define 变化时写 `metric_define_history`；DAO 定义在 `apps/web/dao/metadata/metric_define.go` | 有现成旧历史表，可直接做 backfill 映射 | 只覆盖 define 变更，不是统一审计模型 |
| 资产基础设施 | `apps/web/service/asset/operator.go` 定义 `AssetOperator`；`apps/web/server.go` 只注册了 Chart / Dashboard | 可以作为“现有接入覆盖不完整”的反证样本 | 只覆盖 CRUD，且只注册两个对象，不足以承载完整审计接入 |
| 资产行为日志 | `apps/web/service/asset/behavior.go` 提供 `RecordView/Modify/Delete/Add`，按项目攒批写入 | 说明系统里已有“轻量行为记录”概念 | 该链路是异步批量、弱语义、主要用于 view，不是可靠审计源 |
| 账号登录 / 登出 | `apps/web/controller/account/account.go` 已有 `LoginAccount` / `LogoutAccount` | 账号状态字段未来可直接在这些入口接入 | 当前还没有 `last_login_at / last_logout_at / last_active_at` 字段与刷新节流机制 |

#### leverage 结论

最值得复用的不是某一张现成表，而是三类东西：

1. **成功写路径的业务 service 边界**
2. **OP 审计的查询 / 归因模式**
3. **AB / Metric 的真实旧历史源**

最不该复用成“统一真相”的是两类东西：

1. `asset_behavior`
2. `AssetOperator`

它们都能提供上下文，但都不足以直接定义 V1 审计规范。

### 0C. Dream State Mapping

```text
CURRENT STATE                        THIS PLAN                               12-MONTH IDEAL
分散在 OP / AB / Metric /           统一项目内对象审计底座，                 项目内对象、global 管理操作、
asset_behavior 多套记录里            单对象查询闭环，AB/Metric/Wave 历史      账号活跃、审批/告警/治理都建立在
排障靠人工拼接                       迁移收口，官方产品 UI 暂不展开            同一套稳定审计模型之上
```

#### Dream State 判断

- 这份计划**朝着正确方向前进**
- 但它只有在“Phase 0 + Phase 1 先闭环”时，才是在往 12 个月理想态前进
- 如果把 Phase 2 / Phase 3 一起绑定成第一批交付，它更像一个程序级倡议，而不是一个能落地的 first ship

### 0C-bis. Implementation Alternatives

#### Approach A：Phase 1 Core First

- Summary：先交付 `project_audit_log + auditlog 模块 + Phase 1 live-write + AB/Metric/Wave 历史迁移 + OP 查询接口`，把“单对象排障闭环”一次打透
- Effort：L
- Risk：Med
- Pros：
  - 直接命中当前最真实的 P0 场景
  - 复用现有 service 写路径，不需要先重塑 asset 基础设施
  - 把历史债务收口到一个统一查询面
- Cons：
  - Phase 2 metadata 长尾和 Phase 3 global/account 仍需后续推进
  - 第一版不会得到“所有 agreed scope 一次完成”的心理满足
- Reuses：
  - `apps/web/op/service/audit.go`
  - Chart / Dashboard / Cohort / AB / Metric 现有 service 边界
  - AB `operation_records`
  - `metric_define_history`

#### Approach B：Full Program in One Pass

- Summary：把 Phase 0 / 1 / 2 / 3 作为同一交付推进，项目对象、metadata 长尾、global 管理审计、账号活跃字段同时建设
- Effort：XL
- Risk：High
- Pros：
  - 业务账面上最完整
  - 可以一次性结束“为什么这个对象还没纳入”的范围讨论
- Cons：
  - 首批实现面过宽，真实 touched files 和 touched domains 都明显超出安全范围
  - 风险会从“审计底座”转移成“多域并行改造项目”
  - 任何一个长尾域卡住，都可能拖慢 P0 价值交付
- Reuses：
  - 同 Approach A，但范围更广

#### Approach C：Federated Read First

- Summary：不急着统一写入，先做查询聚合层，把 OP / AB / Metric 旧历史拼成一个“看起来统一”的读接口，再晚点补统一写入
- Effort：M
- Risk：High
- Pros：
  - 表面上最快能给内部查询入口
  - 对现有写路径侵入最小
- Cons：
  - 继续保留多套写入规范，历史债务没有真正收口
  - 新对象仍会面临“写去哪”的分叉
  - 会把“临时聚合器”变成长期兼容负担
- Reuses：
  - 几乎全部复用旧源，但代价是把旧分裂永久化

#### Recommendation

推荐 **Approach A**。

原因不是“它最小”，而是它是**对当前 P0 目标最完整的 first ship**：

- 它完整解决“项目内对象统一写入 + 单对象查询 + 真实历史迁移”
- 它不要求现在就把 global 管理审计和账号活跃链路绑进同一实施批次
- 它最符合“先把内排障闭环做深，再按 phase 扩展”的业务节奏

### 0D. SELECTIVE EXPANSION Analysis

#### Complexity Check

如果把整份计划按“同一批实现”理解，它明显触发复杂度异味：

- touched domains 远超 8 个文件
- 新模块不止一个：`auditlog` 域类型、DAO、service、diff、registry、迁移任务、OP 查询接口
- 还叠加 metadata 长尾、global 管理审计、account 状态字段三条不同链路

所以 CEO 视角下必须把这份文档理解为：

- **一个分阶段 program plan**
- 而不是“一次实现全部范围的交付单”

#### Minimum Set That Achieves the Stated Goal

要完成当前最核心目标，最小且完整的 first ship 是：

1. `meta.project_audit_log`
2. `auditlog` 公共模块（types / service / diff / registry / dao）
3. Chart / Dashboard / Cohort / Experiment / Feature Gate / Feature Config / Metric 的 live-write
4. AB / Metric / Wave 历史复制
5. OP / 内部对象历史查询接口

以下内容都**不应阻塞 first ship**：

- Phase 2 metadata 长尾对象 live-write
- global 管理审计的统一标准化
- `account.last_login_at / last_logout_at / last_active_at`
- 官方产品新审计 UI
- 按操作人 / 跨项目 / 全文检索的分析能力

#### Expansion Scan Auto-Decisions

本轮 `/autoplan` 在 SELECTIVE EXPANSION 下的自动决策如下：

| 候选扩展 | 决策 | 理由 |
|---|---|---|
| Phase 2 metadata 长尾对象 | Defer | 属于同模型扩展，但不是 first ship 的阻塞项 |
| global 管理审计统一标准表 | Defer | 当前 OP 审计已可承接，先保证责任不缺失 |
| 账号活跃字段 | Defer | 重要但与项目对象审计底座不是同一 blast radius |
| 官方产品通用审计 UI | Skip for V1 | 与当前 troubleshooting-first 目标不一致 |
| 按操作人 / 跨项目检索 | Skip for V1 | 当前 spec 明确不是主视角 |
| 审批 / 告警 / 敏感操作治理 | Defer | 应建立在审计底座稳定之后 |

#### First-Ship Not In Scope

- metadata 长尾对象的 live-write 接入
- global 管理审计并表或统一模型
- account 活跃字段
- 官方产品端新审计页面
- 面向人维度或跨项目的分析型查询
- 建立在审计之上的审批 / 告警 / 策略控制

### 0E. Temporal Interrogation

| 实施时段 | 实现者最先会撞到的问题 | 现在就该明确的结论 |
|---|---|---|
| HOUR 1（human） / ~5 min（CC） | `ObjectType` / `action_type` / `detail envelope` 到底怎么定 | 对象枚举、动作枚举、`detail_version=1`、`created_at=事件时间` 已固定 |
| HOUR 2-3（human） / ~10-15 min（CC） | 哪些入口接 live-write、before/after 怎么拿 | 以现有 service 成功写路径为主，不依赖 `AssetOperator` 完整覆盖 |
| HOUR 4-5（human） / ~15-25 min（CC） | AB / Metric / Wave 旧历史怎么映射，缺 before/after 怎么办 | 允许 `changes` 为空，保留 `extra.legacy_source`，不得伪造历史 |
| HOUR 6+（human） / ~30-45 min（CC） | 大字段怎么裁剪、失败策略怎么定、查询接口承诺到哪 | Chart `config` / AB `details` / virtual define 投影边界、强审计 vs best-effort、OP 查询能力边界必须在实现前定掉 |

#### 当时最需要定掉的 4 个问题

1. 强审计 vs best-effort
2. 大字段投影 / 截断边界
3. global 管理审计是否只沿用现有 OP 审计
4. `last_active_at` 的节流更新策略

这 4 个问题已经在本轮 review 中收敛，并写入 `## 7. 评审结论（原待评审议题）`，不再保留为 unresolved set。

### 0F. Mode Selection

- 自动选择模式：**SELECTIVE EXPANSION**
- 选择理由：
  - 这是对现有系统的增强，不是 greenfield 新产品
  - 当前 scope 已有明确 phase 划分，适合“基线 scope + 明确 defer”的 review posture
  - 最重要的不是继续长需求，而是把 first ship 和 follow-up 切清楚
- 对应实施 approach：**Approach A：Phase 1 Core First**

#### CEO Step 0 Interim Verdict

这份计划不需要被推翻。

它需要的是：

1. 明确“first ship = Phase 0 + Phase 1”
2. 明确“Phase 2 / Phase 3 = follow-up program scope”
3. 在这个前提下继续做 Eng review，而不是把 roadmap 当成单个实现单来质疑

这仍然是 CEO 的**推荐切片**。在 premise gate 之后，用户选择保留全量范围，因此后续正文按“全 scope 承诺 + 分 phase rollout”执行。

## Decision Audit Trail

| ID | 分类 | 决策 | 依据 |
|---|---|---|---|
| D-AUTO-01 | Mechanical | UI scope = No | 文档讨论的是审计对象，不是新 UI；`spec.md` 已明确 V1 不做通用审计页面 |
| D-AUTO-02 | Mechanical | DX scope = No | OP / 内部查询接口不是 developer-facing product surface |
| D-AUTO-03 | Mechanical | 本轮按 single-reviewer mode 执行 | 当前 session 有“Work ALONE”约束，且无 Agent 工具可用 |
| D-AUTO-04 | Mechanical | CEO mode = SELECTIVE EXPANSION | 这是既有系统增强，适合基线 scope + 明确 defer |
| D-AUTO-05 | Mechanical | 推荐实现路径 = Approach A | 它对当前 P0 目标最完整，同时 blast radius 可控 |
| D-AUTO-06 | Mechanical | Phase 2 / Phase 3 不阻塞 first ship | 它们不在当前 troubleshooting-first 的最小闭环里 |
| D-USER-01 | Premise Gate | 用户选择保留 Phase 0-3 的全量承诺 | 按 latest reply `B` 继续 review，不把 Phase 2 / 3 从当前方案中移除 |
| D-AUTO-07 | Mechanical | 项目对象 live-write 一致性等级 = 强审计 | 核心价值是排障与追责，best-effort 会制造“业务成功但无审计”的静默缺口 |
| D-AUTO-08 | Mechanical | global 管理审计 V1 继续沿用 OP audit | 先保证责任不缺失，再决定是否统一 global 模型 |
| D-AUTO-09 | Mechanical | 大字段 detail 采用 projection -> compress -> truncate | 满足历史兼容、可读性和存储边界三者平衡 |
| D-AUTO-10 | Mechanical | `last_active_at` 采用 15 分钟同步节流刷新 | 这是最小复杂度且足够稳定的实现 |
| D-AUTO-11 | Mechanical | 全量范围按分 phase rollout 落地 | 用户保留全 scope，不等于允许一次性总开关上线 |
| D-AUTO-12 | Mechanical | 主读索引补 `project_id` 前缀 | 查询入参与权限边界都以 project 为先，索引必须同构 |

## Premise Gate（已确认）

- 用户最新回答：`B`
- 本轮按如下语义落地：**不把 `Phase 2 / Phase 3` 从当前方案承诺里移除**

这不改变 CEO 对风险的判断：

- `Phase 0 + Phase 1` 仍然是风险最低、价值最直接的推荐切片
- 但从现在开始，本文件按 **“全量范围保留 + phase-ordered 实现与 rollout”** 继续评审

换句话说：

1. 业务承诺保留 `Phase 0-3`
2. 工程实现必须拆成可独立 cutover / rollback 的阶段
3. 任何 review finding 都不能以“反正后面再看”为理由留空

## CEO Review — Phase 1 Deep Review

### Section 1. Architecture Review

#### 架构判断

当前方案最健康的总体形态不是“一张表吃掉所有审计场景”，而是三条并行但风格统一的链路：

```text
                        ┌────────────────────────────────────────────┐
                        │            project object writes           │
                        │ chart / dashboard / cohort / ab / metric  │
                        └──────────────────────┬─────────────────────┘
                                               │
                                               ▼
                               ┌────────────────────────────────┐
                               │      auditlog service layer    │
                               │  validate / diff / serialize   │
                               │  snapshot / mask / batch log   │
                               └───────────────┬────────────────┘
                                               │
                 ┌─────────────────────────────┼─────────────────────────────┐
                 │                             │                             │
                 ▼                             ▼                             ▼
      meta.project_audit_log        OP/internal query path         backfill runners
      (project-scoped truth)        (object history read)          (AB / Metric legacy)

                 ┌───────────────────────────────────────────────────────────┐
                 │ global.op_operation_log  +  account.last_* fields        │
                 │ remain separate lanes with aligned audit conventions      │
                 └───────────────────────────────────────────────────────────┘
```

最重要的架构修正有 3 条：

1. **主读索引必须前置 `project_id`**
2. **项目对象 live-write 必须是强审计**
3. **global / account 继续独立落地，但查询与字段规范向统一 envelope 靠拢**

#### 需要显式写进方案的架构约束

- `project_audit_log` 的权限边界是 project-scoped，不允许只靠 `(object_type, object_id)` 查询
- Phase 2 / Phase 3 虽然仍在当前方案范围内，但不能共享一个上线开关
- 迁移 runner、live-write、query path 必须能分别关闭或回滚

#### Dream State Delta

与 12 个月理想态相比，这份计划在本轮 review 后的真实落点是：

- **已经补强**：统一项目对象 envelope、统一对象历史读、旧历史收口、global/account 不再混表
- **仍然刻意不做**：官方产品通用审计 UI、按操作人或跨项目分析、审批/告警/保留策略
- **因此这是一套“内排障优先的审计底座”，不是完整治理产品**

### Section 2. Error & Rescue Map

#### Error & Rescue Registry

| 方法 / codepath | 可能出错点 | 异常 / 失败类型 | Rescued? | Rescue / rollback 动作 | 用户 / 调用方可见结果 |
|---|---|---|---|---|---|
| `auditlog.Log` | 关键字段缺失 | validation error | Y | 直接返回并回滚主事务 | 写操作失败，业务不提交 |
| `auditlog.Log` | detail 序列化失败 | marshal error | Y | 直接返回并回滚主事务 | 写操作失败，业务不提交 |
| `auditlog.Log` | insert `project_audit_log` 失败 | db write error | Y | 直接返回并回滚主事务 | 写操作失败，业务不提交 |
| `auditlog.Log` | `object_name` / `operator_name` enrich 失败 | not found / transient read error | Y | 写空快照 + warning log，核心审计仍提交 | 业务成功，审计完整但展示名缺失 |
| legacy backfill runner | 同一批次重复执行 | duplicate-import risk | Y | 依赖幂等去重键跳过重复写入 | 任务成功，重复记录不落地 |
| legacy backfill runner | 单批转换失败 | malformed legacy payload | Y | 记录失败行并继续下一批，最终输出失败清单 | 任务部分成功，失败清单可追 |
| OP query API | 权限不足 | auth / scope denied | Y | 返回拒绝，不暴露任何 detail | 查询失败，明确报无权限 |
| OP query API | detail 解码失败 | payload decode error | Y | 返回基础字段 + `detail_unavailable` 标记并告警 | 查询可继续，但 detail 不可读 |
| account 活跃刷新 | 高频并发刷新 | write contention | Y | 节流窗口内直接跳过 | 用户无感，字段稍后刷新 |

#### Error Flow Diagram

```text
business mutation
    │
    ├── pre-image load failed ───────▶ abort
    │
    ├── diff build failed ───────────▶ abort
    │
    ├── core audit insert failed ────▶ rollback business tx
    │
    ├── snapshot enrich failed ──────▶ write blank snapshot + warn
    │
    └── commit success ──────────────▶ object change + audit row both durable
```

结论：当前 plan 原本最大的空洞，是**强审计与非核心 enrich 的分层失败策略没有写死**。本轮已补齐。

### Section 3. Security & Threat Model

| Threat | Likelihood | Impact | 本轮结论 |
|---|---|---|---|
| OP 查询接口越权读取其他项目对象历史 | Med | High | 强制 `project_id` 入参 + OP/internal scope 校验；主索引也以 `project_id` 为前缀 |
| 大字段 detail 泄露敏感配置 | Med | High | 所有对象先 projection，再 diff；敏感字段统一 `masked` |
| 迁移脚本回填错误 operator / event time | Med | Med | 允许空展示快照，但禁止伪造 before/after；保留 `legacy_source` 便于追溯 |
| 从 `asset_behavior` 伪造历史 | Low | High | 明确禁止，属于数据可信度风险，不纳入任何迁移路径 |
| account 活跃字段被高频写放大 | High | Med | 15 分钟节流，避免把会话层变成写放大源 |

安全结论不是新增更多安全层，而是**把“哪些数据能被读、哪些字段必须被遮、哪些旧历史不能伪造”写成硬规则**。

### Section 4. Data Flow & Interaction Edge Cases

#### Data Flow Diagram（含 shadow paths）

```text
WRITE REQUEST
    │
    ├── nil request / missing object id ───────▶ reject
    ├── empty mutable fields ──────────────────▶ skip audit or no-op by object rule
    │
    ▼
LOAD PRE-IMAGE
    │
    ├── not found on delete/update ───────────▶ reject
    ├── stale version / concurrent change ────▶ business layer decides conflict
    │
    ▼
BUILD PROJECTION + DIFF
    │
    ├── oversized config / define ────────────▶ project -> compress -> truncate
    ├── sensitive field present ──────────────▶ masked
    │
    ▼
INSERT AUDIT ROW
    │
    ├── db failure ───────────────────────────▶ rollback business tx
    └── success ──────────────────────────────▶ commit
```

#### 必须显式覆盖的 edge cases

- Dashboard `AddChartsToDashboard` / `RemoveChartsFromDashboard` 需要保证 `chart_ids` diff 对空集合、重复 ID、部分无效 ID 都有可解释输出
- Batch delete 必须一对象一日志，且部分失败时不能出现“日志成功但对象没删”或相反
- AB / Metric 历史回填时，旧数据缺 before/after 不算错误，但必须在 `extra` 里把缺口写明
- account 活跃刷新在节流窗口内跳过时，不应被误读为“用户未活跃”

### Section 5. Code Quality Review

#### 代码组织判断

最容易失控的地方不是 DAO，而是**每个对象都手写一套 projection / mask / truncation 逻辑**。所以 registry 必须成为真正的单一入口：

- object projection
- sensitive field mask
- delete snapshot policy
- truncation policy
- copy / relate / release 等 action 的 `extra` 规范

如果这一层散落回 Chart / Dashboard / AB / Metadata service，后续 Phase 2 / Phase 3 会立刻变成重复劳动。

#### 本轮自动折入的质量结论

1. `auditlog` 不是“再造一遍 AssetOperator”，而是**只负责审计表达**
2. 所有对象 service 只负责提供 before / after 或 request context
3. 任何“为了图省事直接序列化当前 GORM struct”的实现都视为违背本方案

### Section 6. Test Review

#### Coverage Diagram

```text
NEW DATA / CODE PATHS
[+] auditlog core
  ├── [GAP->ADD] validation failure rolls back main tx
  ├── [GAP->ADD] enrich failure degrades to blank snapshot
  ├── [GAP->ADD] payload oversize -> compress -> truncate
  └── [GAP->ADD] masked fields remain masked on readback

[+] Phase 1 live-write
  ├── [PARTIAL] Chart create / update / delete
  ├── [PARTIAL] Dashboard create / update / relate / unrelate
  ├── [PARTIAL] Cohort create / update / delete
  ├── [GAP->ADD] AB create / update / release / copy
  └── [GAP->ADD] Metric create / update / delete

[+] Legacy backfill
  ├── [GAP->ADD] AB operation_records replay
  ├── [GAP->ADD] Metric define history replay
  └── [GAP->ADD] idempotent rerun / duplicate skip

[+] Phase 2 metadata live-write
  ├── [GAP->ADD] TrackedEvent create/update/delete
  ├── [GAP->ADD] VirtualEvent create/update/delete
  ├── [GAP->ADD] EventProperty create/update/delete
  ├── [GAP->ADD] UserProperty create/update/delete
  └── [GAP->ADD] VirtualProperty create/update/delete

[+] Phase 3
  ├── [GAP->ADD] OP query auth / mask / pagination
  └── [GAP->ADD] login / logout / last_active_at throttle
```

#### Test Verdict

当前 plan 的原始验证方案只够覆盖 Phase 0 / Phase 1 的 happy path，不足以支撑 premise gate 之后保留的全量范围。需要显式补三类测试：

1. **Phase 2 metadata 对象全覆盖**
2. **Phase 3 account/global 行为测试**
3. **幂等迁移 + 失败注入 + 掩码读回测试**

### Section 7. Performance Review

性能风险不在常规 QPS，而在 3 个位置：

1. **对象历史查询索引**：若没有 `project_id` 前缀，OP 查询会走错访问路径
2. **大字段 payload**：Chart `config`、AB `details`、virtual define 是主要体积热点
3. **回填任务**：AB 历史可能很长，必须按批处理，单批次失败可继续

本轮没有引入缓存要求；更高优先级的是先把索引、批量大小、payload 上限写死。

### Section 8. Observability & Debuggability Review

V1 如果要真服务内部排障，至少要有这组指标和日志：

- `auditlog_write_total{object_type,action,result}`
- `auditlog_write_latency_ms`
- `auditlog_payload_truncated_total{object_type,field}`
- `auditlog_backfill_total{source,result}`
- `auditlog_backfill_lag_seconds`
- `account_activity_refresh_total{result}`

最小日志要求：

- live-write 失败：包含 `project_id`、`object_type`、`object_id`、`action_type`
- backfill 失败：包含 `legacy_source`、源主键或唯一键
- query decode 失败：包含 `audit_log_id` 与 `detail_version`

### Section 9. Deployment & Rollout Review

#### Deployment Sequence

```text
1. migrate schema
2. deploy auditlog core + query API dark
3. enable Phase 1 live-write
4. run AB / Metric backfill
5. switch object history queries to new table
6. enable Phase 2 metadata live-write
7. enable Phase 3 global/account supplements
```

#### Rollback Flow

```text
incident detected
    │
    ├── query bug only ─────────────▶ disable query API / revert read path
    ├── live-write bug ─────────────▶ disable affected phase flag, revert code
    ├── backfill bug ───────────────▶ stop runner, keep source of truth in old store
    └── account refresh bug ────────▶ disable throttle writer, keep login/logout unaffected
```

部署结论：

- Phase 1、Phase 2、Phase 3 至少要有 server-side enable 开关
- backfill 不能和 schema migration 绑在同一不可逆步骤里
- 旧历史源保留不删，保证回滚时查询与追责不失真

### Section 10. Long-Term Trajectory Review

#### 1-year 判断

如果按本轮修正后的方案落地，1 年后新同学看到的将会是：

- 项目对象有统一 envelope 与 query 面
- global 管理审计仍独立，但没有被错误并入
- account 活跃字段是低耦合的账号域能力

真正的长期债务只剩两类：

1. 是否需要把 global 审计也抽象成统一模型
2. 是否需要把 object-centric 查询扩展成 operator / org / cross-project 分析

这两类都被明确留在当前边界之外，属于**有意识的后置**，不是未思考。

### Section 11. Design & UX Review

UI scope 未命中，本节跳过，不进入 `plan-design-review` 主流程。

### CEO Completion Summary

```text
+====================================================================+
|            MEGA PLAN REVIEW — CEO COMPLETION SUMMARY               |
+====================================================================+
| Mode selected        | SELECTIVE EXPANSION (user kept full scope)   |
| Step 0               | premise gate = keep Phase 0-3 commitment     |
| Section 1  (Arch)    | 3 key architecture corrections folded in     |
| Section 2  (Errors)  | 9 core paths mapped, 0 silent gaps left      |
| Section 3  (Security)| 4 threats clarified, 0 unowned               |
| Section 4  (Data/UX) | 4 critical edge cases made explicit          |
| Section 5  (Quality) | registry-driven design locked                |
| Section 6  (Tests)   | coverage gaps identified for Phase 2/3       |
| Section 7  (Perf)    | index/payload/backfill hotspots named        |
| Section 8  (Observ)  | minimum metrics/logs added                   |
| Section 9  (Deploy)  | phased rollout + rollback defined            |
| Section 10 (Future)  | long-term debt isolated to 2 conscious bets  |
| Section 11 (Design)  | SKIPPED (no UI scope)                        |
+--------------------------------------------------------------------+
| NOT in scope         | written                                      |
| What already exists  | leverage map already written                 |
| Dream state delta    | written                                      |
| Error/rescue registry| written                                      |
| Failure modes        | written                                      |
| Outside voice        | skipped (single-reviewer mode)               |
| Unresolved decisions | 0                                             |
+====================================================================+
```

## NOT in scope

- 官方产品通用审计页面：V1 仍以 OP / 内部查询为主
- 按操作人 / 跨项目 / 全文检索：不属于当前 object-centric 排障主视角
- 审批 / 告警 / 保留策略 / 敏感治理：建立在底座稳定之后
- 用 `asset_behavior` 或访问日志反推历史：数据可信度不达标
- 新建统一 global 标准表：Phase 3 先补 inventory，不额外并表
- account 活跃分析报表：当前只补字段，不展开分析产品

## Failure Modes Registry

| Codepath | Failure mode | Rescued? | Test? | User sees? | Logged? |
|---|---|---|---|---|---|
| project object live-write | audit insert fail after business mutate | Y | Y | 业务失败并回滚 | Y |
| snapshot enrich | operator/object display lookup fail | Y | Y | 审计存在但名称为空 | Y |
| OP query | unauthorized project read | Y | Y | 明确无权限 | Y |
| OP query | detail decode fail | Y | Y | 基础字段可见，detail unavailable | Y |
| AB backfill | duplicate replay | Y | Y | 无感，记录去重 | Y |
| AB backfill | malformed legacy item | Y | Y | 任务报告失败项 | Y |
| metric backfill | old/new define null mismatch | Y | Y | 任务报告失败项 | Y |
| account activity | hot account repeated refresh | Y | Y | 用户无感 | Y |

结论：本轮折入后 **0 个 critical gap**。

## Eng Review — Phase 3

### Step 0. Scope Challenge

用户通过 premise gate 明确保留 `Phase 0-3` 的完整承诺，因此本轮 Eng review 不再尝试减 scope，而是把复杂度消化为**并行工作流 + 分阶段 rollout**。

#### 当前 touched modules

| 子问题 | 主要模块 |
|---|---|
| 项目对象 live-write | `apps/web/service/chart`、`dashboard`、`cohort`、`ab`、`metadata` |
| 审计底座 | `apps/web/service/auditlog`、`apps/web/dao/auditlog` |
| OP 查询 | `apps/web/op/controller`、`apps/web/op/service` |
| 历史回填 | `apps/web/cmd` / `script/migration/scripts` |
| global 审计复用 | `apps/web/op/service/audit.go` |
| account 活跃字段 | `apps/web/controller/account` + account 域 |

#### What already exists

- OP 审计的 `Log` / `LogWithFallback` / `ListByQuery`：可复用写入 DTO、查询风格、operator name 回填模式
- Chart / Dashboard / Cohort / AB / Metric service：已具备集中成功写路径，可直接插入审计
- `metric_define_history`：是 Metric 历史回填的天然源
- AB `details.operation_records`：是 AB 历史回填的天然源
- `asset_behavior`：只能作为“不能拿来当审计”的反例

### 1. Architecture Review

#### Engineering Dependency Graph

```text
controllers
    │
    ▼
domain services (chart/dashboard/cohort/ab/metadata/account)
    │
    ├── existing DAOs
    ├── auditlog registry / diff / serializer
    │        │
    │        ▼
    │   project_audit_log DAO
    │
    └── OP query service ───▶ project_audit_log read

backfill runners ───────────▶ legacy sources (AB / metric history) + project_audit_log write
```

#### 工程结论

- `auditlog` 必须是 service-layer dependency，不应该倒灌 controller
- backfill runner 只依赖 registry / serializer / DAO，不依赖 controller
- account 活跃刷新不要依赖 `auditlog`，避免把字段级刷新误塞进对象审计路径

### 2. Code Quality Review

#### 主要质量风险

1. **对象接入点多，但规则必须单源化**
2. **Phase 2 metadata 若手写 5 套 projection，很快会失控**
3. **delete/copy/release/relate 这类动作若各自拼 `extra`，可读性会漂**

#### 本轮折入的质量要求

- registry 提供统一 `ProjectionBuilder` / `MaskRules` / `ExtraBuilder`
- object service 只提交业务上下文，不直接拼装 detail envelope
- 所有 action_type 归一后，动作语义必须在 `changes[]` 或 `extra` 可读

### 3. Test Review

#### Test Plan Verdict

需要把原有 `8. 验证方案` 从“Phase 1 够用”提升成“全 phase 覆盖”。最关键的缺口是：

- Phase 2 metadata 没有被纳入集成测试声明
- Phase 3 account / global 没有明确失败注入
- OP query 的授权、masking、decode failure 缺测试项

#### Worktree-Test Coverage Diagram

```text
PHASE 0
  ├── auditlog unit tests
  │   ├── validation / rollback
  │   ├── projection / mask
  │   └── compress / truncate / hash
  └── OP query integration
      ├── pagination / sort
      ├── project scope auth
      └── masked detail readback

PHASE 1
  ├── chart / dashboard / cohort live-write
  ├── experiment / gate / config live-write
  ├── metric live-write
  └── AB / metric backfill idempotence

PHASE 2
  ├── tracked_event
  ├── virtual_event
  ├── event_property
  ├── user_property
  └── virtual_property

PHASE 3
  ├── global OP audit inventory regression
  └── login / logout / active throttle
```

### 4. Performance Review

#### Hotspots

- `project_audit_log` 读路径：索引必须与 `project_id` 查询同构
- payload 写路径：大字段先投影再压缩，避免每次读取都解巨型 detail
- backfill 路径：按批次写入并输出失败清单，不能一次全量 in-memory

### Engineering Parallelization Strategy

#### Dependency Table

| Step | Modules touched | Depends on |
|---|---|---|
| A. schema + auditlog core | `service/auditlog`、`dao/auditlog`、migration | — |
| B. OP query path | `op/controller`、`op/service` | A |
| C. Phase 1 live-write（asset + ab + metric） | `service/chart`、`dashboard`、`cohort`、`ab`、`metadata/metric` | A |
| D. legacy backfill runners | `cmd` / `script/migration`、`service/auditlog` | A |
| E. Phase 2 metadata live-write | `service/metadata/*` | A |
| F. Phase 3 global/account | `op/service/*`、`controller/account`、account 域 | A |

#### Parallel Lanes

- Lane A: `A -> B`
- Lane B: `C`
- Lane C: `D`
- Lane D: `E`
- Lane E: `F`

#### Execution Order

1. 先完成 Lane A 的底座
2. 之后 `C + D + E + F` 可并行分 worktree 推进
3. 合入顺序建议：`C` 与 `D` 先，`E` 次之，`F` 最后

#### Conflict Flags

- `C` 与 `E` 都会触碰 `service/metadata`，如果 Metric 与 Phase 2 metadata 由不同 worktree 推进，需要先分清目录 ownership
- `B` 与 `F` 都会触碰 `op/service`，建议 `B` 先落 query 基线，`F` 再补 global inventory

### Eng Completion Summary

```text
- Step 0: Scope Challenge — 全量范围保留，但转为 phase-ordered execution
- Architecture Review: 4 engineering constraints folded in
- Code Quality Review: registry 作为唯一 detail 规范入口
- Test Review: full-phase coverage diagram produced, 8 key gaps promoted to tasks
- Performance Review: 3 hotspots fixed in plan
- NOT in scope: written
- What already exists: written
- Failure modes: 0 critical gaps after fold-in
- Outside voice: skipped (single-reviewer mode)
- Parallelization: 5 lanes, 4 can run in parallel after底座完成
- Unresolved decisions: 0
```

## Implementation Tasks

Synthesized from this review's findings. Each task derives from a concrete finding above.

- [ ] **T1 (P1, human: ~1 day / CC: ~20min)** — audit storage — 把 `project_audit_log` 主读索引改成 `project_id + object_type + object_id + created_at`
  - Surfaced by: CEO Section 1 / Eng Architecture — query 入参与权限边界都以 project 为先
  - Files: `specs/20260626-Wave-Feat-AddAuditLog/plan.md`, future `dao/auditlog` migration
  - Verify: explain 输出索引命中；对象查询走 project-scoped path
- [ ] **T2 (P1, human: ~1 day / CC: ~20min)** — audit consistency — 为 `Log`/`BatchLog` 实现 `consistency` 参数（Strong / Core / BestEffort），并确保各调用方按场景传入正确等级。Strong/Core 模式下核心字段失败回滚事务，Core 模式下 detail 失败降级，BestEffort 全降级为 warning。
  - Surfaced by: CEO Section 2 — 不能接受“业务成功但无审计”
  - Files: future `service/auditlog`, `service/chart`, `service/dashboard`, `service/cohort`, `service/ab`, `service/metadata`
  - Verify: Strong 时核心字段失败回滚；Core 时核心字段失败回滚但 detail 失败降级；BestEffort 时全降级 warning
- [ ] **T3 (P1, human: ~1 day / CC: ~25min)** — registry — 建立 registry-driven projection/mask/truncate 策略，覆盖 Chart/AB/Virtual* 大字段
  - Surfaced by: CEO Section 5 / Performance — 不能把 detail 逻辑散落到对象 service
  - Files: future `service/auditlog/registry.go`, `diff.go`
  - Verify: payload 过大时可预测地 compress/truncate，并保留 `truncated_fields` / `payload_hash`
- [ ] **T4 (P1, human: ~1 day / CC: ~20min)** — legacy migration — 为 AB / Metric 回填定义幂等去重键与失败清单输出
  - Surfaced by: CEO Section 2 / Section 9 — backfill 可重复执行，且失败可追
  - Files: future `cmd` / `script/migration` runners
  - Verify: 重复跑不新增重复行；坏数据产出失败清单
- [ ] **T5 (P1, human: ~0.5 day / CC: ~10min)** — OP query — 明确 project scope auth、masked readback、decode failure fallback
  - Surfaced by: CEO Section 3 / Eng Test Review — query 既是排障入口，也是权限边界
  - Files: future `op/controller/project_audit.go`, `op/service/project_audit.go`
  - Verify: 无权限拒绝；detail decode 失败时返回基础字段 + unavailable 标记
- [ ] **T6 (P1, human: ~1 day / CC: ~25min)** — test matrix — 把 Phase 2 metadata 与 Phase 3 account/global 正式纳入集成测试与故障注入
  - Surfaced by: CEO Section 6 / Eng Test Review — 原验证方案只够 Phase 1
  - Files: `specs/20260626-Wave-Feat-AddAuditLog/plan.md`, future integration tests
  - Verify: 全 phase codepath 都有 unit/integration/fault-injection 对应项
- [ ] **T7 (P2, human: ~0.5 day / CC: ~10min)** — observability — 增加 audit write/backfill/query/account activity 的最小指标与结构化日志
  - Surfaced by: CEO Section 8 — 没有 day-1 observability 就无法服务排障
  - Files: future `service/auditlog`, `op/service`, backfill runners
  - Verify: 指标和日志字段可支撑单对象问题回放
- [ ] **T8 (P2, human: ~0.5 day / CC: ~10min)** — rollout controls — 为 Phase 1/2/3 建独立 enable 开关与 rollback runbook
  - Surfaced by: CEO Section 9 — 全 scope 保留不等于允许一次总开关
  - Files: future config / rollout docs / runbooks
  - Verify: 任一 phase 可独立关闭，不影响已稳定 phase

## Cross-Phase Themes

- **Theme: scope kept full, rollout kept phased** — 这是本轮 premise gate 之后最重要的统一结论
- **Theme: completeness beats convenience** — 强审计、project-first index、full-phase tests 都是同一价值判断
- **Theme: registry is the load-bearing abstraction** — 如果 registry 做不好，Phase 2 / Phase 3 会立即出现重复与漂移

## GSTACK REVIEW REPORT

| Review | Trigger | Why | Runs | Status | Findings |
|--------|---------|-----|------|--------|----------|
| CEO Review | `/plan-ceo-review` | Scope & strategy | 1 | CLEAR (via /autoplan) | mode: `SELECTIVE_EXPANSION`; premise gate 保留 Phase 0-3，全量承诺改为分 phase rollout；0 critical gaps |
| Codex Review | `/codex review` | Independent 2nd opinion | 0 | SKIPPED | single-reviewer mode，本轮未启用额外 reviewer voice |
| Eng Review | `/plan-eng-review` | Architecture & tests (required) | 1 | CLEAR (PLAN via /autoplan) | 8 个 engineering issues 已折入计划；0 critical gaps |
| Design Review | `/plan-design-review` | UI/UX gaps | 0 | SKIPPED | no UI scope |
| DX Review | `/plan-devex-review` | Developer experience gaps | 0 | SKIPPED | no developer-facing scope |

- **VERDICT:** CEO + ENG CLEARED — 可以进入实现，但必须按 phase-ordered rollout 执行
NO UNRESOLVED DECISIONS
