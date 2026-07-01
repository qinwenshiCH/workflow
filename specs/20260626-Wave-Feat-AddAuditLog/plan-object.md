<!-- /autoplan restore point: /Users/wenshiqin/.gstack/projects/qinwenshiCH-workflow/main-autoplan-restore-20260701-123106.md -->

# 技术方案：项目对象活动（meta.activity_log）

> 本方案聚焦项目内对象活动。组织/项目管理活动见 [plan-org.md](./plan-org.md)，账号活跃字段见 [plan-account.md](./plan-account.md)。

## 1. 目标

为 Chart / Dashboard / Cohort / AB / Metric / Pipeline / Event / Property 等项目内对象提供统一写入、统一查询、统一历史迁移。

当前设计前提来自以下已确认共识：

- V1 优先服务**内部排障**，查询主视角是 `item_type + item_id`，不是按人或按组织做分析型报表。
- detail 采用**结构化 diff 优先**的稳定 envelope，必要时允许 `extra` / `snapshot` 补充；top-level envelope 由活动模块统一拥有。
- detail **不直接持久化当前业务结构体**；V1 先以可读 JSON envelope 落盘，不默认引入应用层压缩。
- 升级后**新操作只写新活动记录**，旧 AB / Metric / Wave 历史记录做一次性复制，旧字段或旧表保留不删。
- V1 **不新增官方产品通用活动页面**；除 AB / Metric 既有查看能力外，其余查看能力优先通过 OP / 内部接口承接。

## 2. 范围切分

### 2.1 项目内对象标准活动

这一层是本次主线，统一落到 `meta.activity_log`。

首批对象范围：

- 资产对象：`CHART`、`DASHBOARD`、`COHORT`、`EXPERIMENT`、`FEATURE_GATE`、`FEATURE_CONFIG`、`PIPELINE`
- 元数据对象：`METRIC`、`TRACKED_EVENT`、`VIRTUAL_EVENT`、`EVENT_PROPERTY`、`USER_PROPERTY`、`VIRTUAL_PROPERTY`

## 3. 非目标

- V1 不做官方产品端的新通用活动 UI
- V1 不优先支持按 `operator_id`、按组织范围、按跨项目的活动分析
- V1 不从 `asset_behavior` 反推”谁改了什么”，因为它本质不是可靠活动源

## 4. 推荐架构

### 4.1 模块划分

建议新增一套独立于 OP 审计的项目对象活动模块，命名上避免和 `apps/web/op/service/audit.go` 混淆，文中统一用 `activity` 代称。

| 模块 | 职责 | 建议落点 | 参考现状 |
|---|---|---|---|
| 活动域类型 | `ItemType`、`ActionType`、`ActivityDetail`、`Change`、query DTO | `apps/web/service/activity/types.go` | `spec.md` 的 `activity_log` / detail 约定 |
| 公共服务 | `Log` / `BatchLog` / `ListByQuery` | `apps/web/service/activity/activity.go` | OP 审计的 `apps/web/op/service/audit.go` |
| diff 引擎 | 结构化变更生成、排除字段、敏感字段掩盖 | `apps/web/service/activity/diff.go` | `research.md` 对 PostHog 结构化 diff 的总结 |
| 对象注册表 | 各对象的字段投影、排除规则、mask 规则、对象名提取 | `apps/web/service/activity/registry.go` | 现有 `AssetOperator` 只覆盖 Chart / Dashboard，不能作为完整真相 |
| 持久化 DAO | `activity_log` 表 CRUD + 批量插入 + 分页查询 | `apps/web/dao/activity/object.go` | `meta.metric_define_history` / `op_operation_log` DAO 风格 |
| OP 查询接口 | 内部排障查询 API | `apps/web/op/controller/project_activity.go` + `apps/web/op/service/project_activity.go` | 现有 OP `ListByQuery` 模式 |
| 迁移任务 | AB / Metric 历史导入，新表回填 | `script/migration/scripts/...` + `apps/web/cmd/...` 或等价任务入口 | 现有 OP 迁移脚本模式 |

### 4.2 数据模型

项目内标准落点继续采用 `spec.md` 已定义的 `meta.activity_log`：

- 主键：`id`
- 主查询键：`(item_type, item_id, occurred_at DESC, id DESC)`
- 活动主体：`item_type`、`item_id`、`item_name`，其中 `item_name` 是展示快照
- 动作信息：基础 `action_type`
- 归因信息：`operator_id`、`operator_name`、`source`、`correlation_id`、`occurred_at`，其中 `operator_name` 是展示快照，`occurred_at` 明确定义为操作时间 / 活动事件时间
- 表在 project schema（meta）内，`project_id` 字段冗余，不设此列
- 详情：`detail_payload` 使用 `TEXT` 存稳定 JSON envelope；查询接口统一返回解析后的 `detail`；V1 不引入 `detail_version`

#### 4.2.1 枚举规范

所有 `activity_log` 的枚举值采用字符串类型（非 int），自描述，不依赖代码注释。

**ItemType** — 活动域对象的类型，UPPER_SNAKE_CASE：

```go
const (
    ItemTypeChart           = "CHART"
    ItemTypeDashboard       = "DASHBOARD"
    ItemTypeCohort          = "COHORT"
    ItemTypeExperiment      = "EXPERIMENT"
    ItemTypeFeatureGate     = "FEATURE_GATE"
    ItemTypeFeatureConfig   = "FEATURE_CONFIG"
    ItemTypeMetric          = "METRIC"
    ItemTypeTrackedEvent    = "TRACKED_EVENT"
    ItemTypeVirtualEvent    = "VIRTUAL_EVENT"
    ItemTypeEventProperty   = "EVENT_PROPERTY"
    ItemTypeUserProperty    = "USER_PROPERTY"
    ItemTypeVirtualProperty = "VIRTUAL_PROPERTY"
    ItemTypePipeline        = "PIPELINE"
)
```

命名规则：
- 与业务域对象名对齐，不使用缩写（如 `EXP` → `EXPERIMENT`）
- 与 OP audit 的 `target_type`（小写，运维域）不做对齐，两者作用域不同

**action_type** — 基础操作类型，全小写描述性字符串。**所有常量统一定义在 `activity/types.go`**，模块不自行定义。基础集合保持稳定，不随对象域膨胀。

```go
// apps/web/service/activity/types.go

// 基础动作（默认提供）
ActionTypeCreate = "create"
ActionTypeUpdate = "update"
ActionTypeDelete = "delete"
ActionTypeCopy   = "copy"
```

设计要点：
- **守口到活动模块**：一个文件看完所有基础 `action_type`
- **基础动作稳定**：`create` / `update` / `delete` / `copy` 是 V1 的固定主枚举
- **扩展动作从严准入**：只有当 `item_type + detail` 明显不足以表达语义时，才允许在活动模块统一注册新的 action_type
- **领域语义不额外落字段**：如 `online`、`release`、`relate`、`stop` 等语义，通过 `item_type` 和 `detail.changes/extra` 表达，不新增 `action_name`
- 内部操作（冲突解决）复用相同 `action_type`，再用 `source="internal"` 区分

**source** — 操作来源，全小写：

```go
const (
    SourceWeb      = "web"       // 用户通过 Web 界面操作
    SourceOpenAPI  = "openapi"   // 用户通过 OpenAPI/MCP 接口操作
    SourceInternal = "internal"  // 系统内部操作（冲突解决、迁移回调等）
    SourceBackfill = "backfill"  // 历史迁移回填
)
```

`web` 和 `openapi` 都代表用户主动操作。`internal` 代表系统行为，但如由用户触发，仍应尽量继承原始用户。`backfill` 用于历史数据迁移。

`detail_payload` 在 V1 使用 **TEXT + 可读 JSON envelope**：

1. SerializeDetail 生成稳定 JSON（如 `{changes, extra, snapshot}`）
2. 通过字段投影和大小预算控制 payload
3. 若超限，优先截断明确的大字段，并把 `extra.truncated_fields`、`extra.payload_hash` 写入上下文

V1 不默认启用应用层 LZ4。等真实写入规模证明 `TEXT` 成为瓶颈后，再评估 codec 升级。

`detail` 顶层 envelope 由活动模块维护，业务方只负责提供投影后的 `changes` / `extra` / `snapshot`。当前不新增 `detail_version` 字段，避免把版本治理扩散到每个业务接入点。

#### 4.2.2 为什么 V1 不默认压缩

- **收益**：排障时可直接查库和比对 JSON；迁移、回填、跨语言消费都更简单；不需要额外 codec 兼容治理
- **成本**：行宽、WAL、复制带宽和冷热数据占用会更高；超大对象 detail 必须靠预算控制
- **当前取舍**：V1 先用 `TEXT + 投影 + 截断 + payload 监控`。只有在真实规模证明存储和 IO 成本已经成为瓶颈时，再在 `SerializeDetail` / `ParseDetail` 层引入可替换 codec，不改业务调用契约

### 4.3 写入模型与一致性等级

统一公共契约保持精简：

```go
Log(ctx, input) error
BatchLog(ctx, inputs) error
ListByQuery(ctx, query) ([]ActivityLogItem, int64, error)
```

其中 `Log` / `BatchLog` 只负责：

1. 校验公共字段
2. 填充 item / operator 展示快照
3. 生成或接收活动事件时间
4. 序列化 detail
5. 调 DAO 写入

#### 4.3.1 一致性等级：由活动模块中心化决策

活动写入的一致性不再由调用方自由传参，而是由活动模块内部的 **policy registry** 中心化决策。策略可以按接入点注册，也可以按 `item_type + action_type` 配默认值，但不要求为此新增落库字段。

```go
type WritePolicy string

const (
    WritePolicyRequiredFull WritePolicy = "required_full"
    WritePolicyRequiredCore WritePolicy = "required_core"
    WritePolicyBestEffort   WritePolicy = "best_effort"
)
```

三种策略的含义：

| 策略 | 活动主行 | detail_payload | 适合场景 |
|------|---------|---------------|---------|
| `required_full` | 失败→回滚 | 失败→回滚 | 删除/发布/高风险权限变更 |
| `required_core` | 失败→回滚 | 失败→降级（空 detail 或截断 + warning） | 常规 CRUD，排障核心场景 |
| `best_effort` | 失败→warning | 失败→warning | 内部冲突解决、低优先级历史回填、非关键附属操作 |

核心字段定义为：`item_type`, `item_id`, `action_type`, `operator_id`, `source`, `occurred_at`（表在 project schema 内，无 `project_id` 列）。

推荐策略由活动模块维护，而不是由业务调用方自行拍板：

| 写入策略 | 推荐对象/场景 |
|---------|-------------|
| `required_full` | AB 发布/上线、Dashboard/Cohort/Chart 删除 |
| `required_core` | 常规对象 create/update/copy、Metadata CRUD |
| `best_effort` | 历史回填、明确非关键的内部附属操作 |

#### 4.3.2 BatchLog 原子性

当单次业务操作涉及多个对象时（如 `CopyDashboard` 同时创建新 dashboard 和多个 chart），需要产生多条活动记录。

`BatchLog` 的原子性约定：
- 同一业务事务内的多条活动记录，通过 `BatchLog` 一次性写入
- `BatchLog` 在 `required_full` / `required_core` 策略下：任一记录的核心字段写入失败 → 整体失败 → 业务事务回滚
- `BatchLog` 在 `best_effort` 策略下：失败折入 warning，不影响业务事务
- 不允许”dashboard 的 copy 记录成功但 chart 的 copy 记录缺失”
- `BatchLog` 自动为同批记录补同一个 `correlation_id`，用于后续排障串联；不要求业务侧维护 `operation_group_id`

#### 4.3.3 活动日志保留策略

V1 不实现自动清理，但预留扩展位：

- `occurred_at` 字段天然支持按时间范围分区
- 索引以 `(item_type, item_id, occurred_at DESC, id DESC)` 结尾，partition pruning 可直接利用
- 未来需要 TTL 清理时，按 `occurred_at` 月份分区，直接 drop 旧分区即可
- V1 上线后根据实际写入速率评估是否需要分区，不在首批实现

global 管理活动也复用相同的基础事件模型，但可配置不同的默认策略。详见 [plan-org.md](./plan-org.md)。

### 4.4 diff 引擎

建议统一提供：

```go
ChangesBetween(oldValue, newValue, itemType) []Change
```

核心原则：

1. **业务对象先投影，再 diff**，不能把现有 GORM struct 直接序列化进活动
2. **排除噪音字段**，例如 `id`、`created_at`、`updated_at`、`created_by`、`updated_by`，以及纯缓存字段、仅由后台异步刷新的派生字段
3. **默认掩盖敏感字段**：各类 integration/config 中的敏感配置、用户邮箱、密码、token、密钥类字段，统一替换为 `"masked"`
4. **只保留稳定字段名**，避免未来业务结构调整把历史读挂

推荐每个对象类型都注册一个 `ActivityProjectionBuilder`，输出稳定 map；敏感字段规则由 registry 声明，再由 redaction 引擎统一应用：

- Chart：`name`、`description`、`query_type`、`api_request`、`config`、`version`
- Dashboard：`name`、`description`、`version`、`chart_ids`、`layout_overrides`
- Cohort：`name`、`description`、`rule_config`、`calc_mode`、`calc_time`、`cohort_version`
- AB：公共字段 + 领域摘要，不直接把整个 `details` 原样落盘
- Metric / Event / Property：只投影用户真正关心的配置字段

### 4.5 查询接口

V1 推荐在 OP 或等价内部链路提供对象历史查询接口，优先保持简单分页模型，最小输入输出如下：

保留 `total` 的原因：

- OP / 内部排障需要先判断“这个对象到底有多少历史”，再决定是否继续翻页或切换排查方向
- 当前查询是单对象时间序列，直接做 count 的复杂度可控，没必要为了 V1 先改成 cursor-only
- 如果未来真出现超深翻页压力，可以增量加 cursor 版本，而不是倒逼现有内部使用方迁移

**Request**

```json
{
  "item_type": "CHART",
  "item_id": 123,
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
      "item_type": "CHART",
      "item_id": 123,
      "item_name": "DAU 趋势",
      "action_type": "update",
      "operator_id": 7,
      "operator_name": "alice",
      "source": "web",
      "detail": {
        "name": "DAU 趋势",
        "changes": [
          {"field": "name", "action": "changed", "before": "DAU", "after": "DAU 趋势"}
        ]
      },
      "occurred_at": "2026-06-29T10:00:00+08:00"
    }
  ]
}
```

V1 不承诺：

- `operator_id` 维度筛选
- 跨项目检索
- 全文检索 detail

补充约束：

- 查询权限以 OP / 内部排障权限为准，不能只靠 `item_id` 直读
- 表在 project schema 内天然隔离，不额外校验 project scope
- 响应中的 `detail` 必须保持掩码后的读视图；不允许在 read path 重新暴露被 masking 的原值
- 响应字段名统一为 `detail`，不向调用方暴露底层存储名 `detail_payload`

### 4.6 历史迁移

迁移原则：

1. **一次性复制旧历史**
2. **迁移后查询只读新活动表**
3. **升级后新写入只写新活动表**
4. **旧字段或旧表保留，不做双写**

迁移源建议如下：

| 历史源 | 是否迁移到新项目活动 | 原因 |
|---|---|---|
| `ab_feature_flag.details.operation_records` | 是 | 这是 AB 唯一真实历史源，必须统一收口 |
| `meta.metric_define_history` | 是 | 属于明确历史债务，且 Metric 已纳入统一规范 |
| `meta.asset_behavior` | 否 | 当前实际只有 VIEW 有效，且不具备可靠活动语义 |
| `global.op_operation_log` | 否 | 作用域不同（OP 配置操作），继续留在 global 管理活动链路；客户侧管理操作已进入 `global.activity_log` |

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
- `occurred_at` 直接回填原始 `OperateAt`

迁移必须具备**幂等去重键**，建议最少包含：

- `legacy_source`
- `item_type`
- `item_id`
- `legacy_action_type`
- `operator_id`
- `occurred_at`

对于 Chart / Dashboard / Cohort / Event / Property 这类**当前没有可靠旧操作历史源**的对象：

- 不从 `asset_behavior`、访问记录或其他派生数据中“伪造历史”
- 历史迁移范围只覆盖**真实存在且可解释的旧操作记录**
- 上线后从新活动表开始连续记账

### 4.7 活动场景全量目录

以下是对活动范围内所有操作的完整盘点。每条记录进入 `meta.activity_log`，标注 `[G]` 表示该操作同时影响 global 活动域（但项目内活动行仍写入 `activity_log`）。

说明：
- 下表中的 `action_type` 统一指基础动作（`create/update/delete/copy`）
- 如出现 `online` / `release` / `variant_change` / `relate` / `stop` 等更细语义，均表示该场景的业务语义说明，不是额外落库字段
- 对应记录仍只落基础 `action_type`，细节通过 `detail.changes/extra` 表达

#### 4.7.1 资产对象

**CHART**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建 Chart | `create` | web/openapi/mcp | `name`, `type`, `query_type`, `api_request`, `config`, `version` | 若关联 dashboard 则记 `dashboard_ids` | `config` 可能需要大字段投影 |
| 更新 Chart | `update` | web/openapi/mcp | 仅变更字段的 before/after | 无 | 若只有噪音字段变化则跳过 |
| 删除 Chart | `delete` | web/openapi/mcp | 至少 `name` 快照 | `snapshot.version`, `snapshot.dashboard_ids` | 读 DB 获取删除前快照 |
| 批量删除 | `delete` × N | web/openapi/mcp | 同上，每条对象一行 | `extra.batch_id`, `extra.batch_index` | 通过 `BatchLog` 写入，共享事务 |
| 复制 Chart | `copy` | web/openapi/mcp | 可为空 | `extra.source_item_id`, `extra.source_item_name`, `extra.target_name` | copy 产生新对象 ID |

**DASHBOARD**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建 Dashboard | `create` | web/openapi/mcp | `name`, `description`, `version` | 若含初始 chart 则记 `chart_ids` | |
| 更新 Dashboard（含 chart/layout 变更）| `update` | web/openapi/mcp | `name`, `description`, `chart_ids`, `layout_overrides` 中有变更的字段 | 无 | `chart_ids` diff 需处理空集合、重复 ID |
| 轻量修改 meta | `update` | web/openapi/mcp | `name` 或 `description` 的 before/after | 无 | `PatchDashboardMeta` |
| 仅更新 layout | `update` | web/openapi/mcp | `layout_overrides` 变更 | 无 | `SetDashboardChartLayouts` |
| 添加 Chart 到 Dashboard | `update` | web/openapi/mcp | `chart_ids` before/after | `extra.added_chart_ids` | 记在 Dashboard 活动中；被添加的 Chart 自身不产生活动 |
| 从 Dashboard 移除 Chart | `update` | web/openapi/mcp | `chart_ids` before/after | `extra.removed_chart_ids` | 同上 |
| Chart 添加到多个 Dashboard | `update` × N | web/openapi/mcp | 每个 Dashboard 产生一条 | 同上 | `AddChartToMultipleDashboards` |
| 删除 Dashboard | `delete` | web/openapi/mcp | 至少 `name` 快照 | `snapshot.version`, `snapshot.chart_count` | |
| 批量删除 | `delete` × N | web/openapi/mcp | 同上 | `extra.batch_id` | |
| 复制 Dashboard | `copy` | web/openapi/mcp | 可为空 | `extra.source_item_id`, `extra.copy_charts` | 若 `copyCharts=true` 则每个被复制的 Chart 也产一条 |

**COHORT**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建 Cohort | `create` | web/openapi/mcp | `name`, `description`, `rule_config`, `calc_mode`, `calc_time`, `cohort_version` | `extra.scheduler_job_id` | 含调度任务创建 |
| 更新 Cohort（含规则变更）| `update` | web/openapi/mcp | 变更字段 before/after | 若调度参数变更则记 `extra.scheduler_job_updated` | |
| 更新 Cohort（仅调度参数） | `update` | web/openapi/mcp | `calc_mode`/`calc_time` before/after | `extra.scheduler_job_action: updated/created` | |
| 删除 Cohort | `delete` | web/openapi/mcp | 至少 `name` 快照 | `snapshot.rule_summary`, `extra.scheduler_job_deleted` | 读 DB 获取删除前快照 |
| Cohort 手动重算触发 | — | — | — | — | **不进入活动表**，这是 create/update 的内部副作用 |
| Cohort 定时重算执行 | — | — | — | — | **不进入活动表**，是 cron 调度回调，属系统运维日志 |
| Cohort 清理任务 | — | — | — | — | **不进入活动表**，属系统运维日志 |

#### 4.7.2 AB 对象

**EXPERIMENT**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建实验 | `create` | web/openapi/mcp | `ffkey`, `name`, `status`, `enabled`, `traffic`, `version` | `extra.subject_id`, `extra.layer_id` | |
| 更新实验配置 | `update` | web/openapi/mcp | 变更字段 before/after | 若 `details` 变更则仅投影摘要 | |
| 状态变更：Debug | `update` | web/openapi/mcp | `status` before/after | `extra.transition = "debug"`；若触发冲突解决则补 `extra.conflict_resolution` | |
| 状态变更：Online | `update` | web/openapi/mcp | `status`, `enabled` before/after | `extra.transition = "online"`；`extra.conflict_resolution`, `extra.exposure_property_created` | |
| 状态变更：Offline | `update` | web/openapi/mcp | `status`, `enabled` before/after | `extra.transition = "offline"`；`extra.buckets_released` | 释放 layer buckets |
| 状态变更：Delete | `delete` | web/openapi/mcp | `status` before/after, 至少 `name` 快照 | `extra.buckets_released`, `extra.references_removed` | |
| Release | `update` | web/openapi/mcp | `status`, `release_plan` before/after | `extra.transition = "release"`；`extra.release_scope`（流量分配变更摘要） | 涉及流量分配和 bucket 释放 |
| 复制实验 | `copy` | web/openapi/mcp | 可为空 | `extra.source_item_id`, `extra.source_ffkey` | |
| 内部下线（冲突解决）| `update` | internal | `status`, `enabled` before/after | `extra.transition = "offline"`；`extra.reason: conflict_resolution`, `extra.conflict_ffkey` | 必须记录，source=`internal` |
| 内部删除（冲突解决）| `delete` | internal | `status` before/after, 至少 `name` 快照 | `extra.reason: conflict_resolution`, `extra.conflict_ffkey` | 必须记录，source=`internal` |

**FEATURE_GATE**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建开关 | `create` | web/openapi/mcp | `ffkey`, `name`, `status`, `enabled`, `traffic`, `version` | 无 | |
| 更新开关配置 | `update` | web/openapi/mcp | 变更字段 before/after | 无 | |
| 状态变更：Debug/Online/Offline | `update` | web/openapi/mcp | `status`, `enabled` before/after | `extra.transition` + `extra.conflict_resolution` | |
| 状态变更：Delete | `delete` | web/openapi/mcp | 至少 `name` 快照 | `extra.references_removed` | |
| Release | `update` | web/openapi/mcp | `status`, `release_plan` before/after | `extra.transition = "release"` | |
| 复制开关 | `copy` | web/openapi/mcp | 可为空 | `extra.source_item_id` | |
| 内部下线/删除（冲突解决）| `update` 或 `delete` | internal | 同 Experiment | `extra.transition` + `extra.reason: conflict_resolution` | 必须记录 |

**FEATURE_CONFIG**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建配置 | `create` | web/openapi/mcp | `ffkey`, `name`, `status`, `enabled`, `traffic`, `version`, 变体摘要 | 无 | |
| 更新配置 | `update` | web/openapi/mcp | 变更字段 before/after | 无 | |
| 变体变更 | `update` | web/openapi/mcp | 变体字段 before/after | `extra.change_kind = "variant_change"`；`extra.changed_variant_keys` | 高价值场景，必须能看出改了哪个 variant |
| 状态变更/Release/复制 | 同 Experiment | | | | |
| 内部冲突解决 | 同 Experiment | internal | | | 必须记录 |

#### 4.7.3 元数据对象

所有元数据对象遵循统一模式，差异仅在 `changes[]` 字段列表。

**METRIC**

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建指标 | `create` | web/openapi/mcp | `name`, `description`, `define`, `precision` | 无 | `define` 是活动核心字段 |
| 更新指标 | `update` | web/openapi/mcp | 变更字段 before/after | 若仅 `define` 变更则 `changes=[{field:"define",...}]` | 现有 `metric_define_history` 将迁移到新活动表 |
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

Pipeline 目前**完全没有操作活动**。现有的执行级日志（`exec_info` JSONB、`pipeline_batch_export_run`、`pipeline_batch_export_backfill`）只记录系统运行历史，不记录谁创建/更新/删除了 pipeline。以下 CRUD 操作为用户主动操作，需接入活动：

| 场景 | action_type | 触发来源 | detail.changes[] 最小集合 | extra | 备注 |
|------|------------|---------|--------------------------|-------|------|
| 创建 Pipeline | `create` | web/openapi | `name`, `type`, `pipeline_type`, `data_type` | `extra.work`, `extra.data_source_id` | |
| 更新 Pipeline | `update` | web/openapi | 变更字段 before/after | 无 | 包括名称/描述更新 |
| 删除 Pipeline | `delete` | web/openapi | 至少 `name` 快照 | `snapshot.pipeline_type`, `snapshot.work` | 软删除 |
| 停止 Pipeline | `update` | web/openapi/mcp | `exec_status` before/after | `extra.change_kind = "stop"`；`extra.stop_reason` | 用户主动停止 |

以下**不进入** `activity_log`：
- Pipeline Process（系统级执行，已有 `exec_info` 和 batch_export_run 跟踪）
- Pipeline callback（AB target 状态同步，属基础设施层状态变更）

#### 4.7.5 明确不进入项目活动表的操作

以下操作有变更，但**不写入** `activity_log`：

| 操作 | 不记录原因 | 替代落点 |
|------|-----------|---------|
| `asset_behavior` 的 VIEW/MODIFY/DELIVER 记录 | 分析/热度用途，非活动语义 | 保持现有 `asset_behavior` 表 |
| Cohort 定时重算执行（RunCohortJob cron 回调）| 系统自动运维操作，非用户触发 | scheduler 自身日志 |
| Cohort 清理任务（cohort-clean cron）| 系统维护操作 | scheduler 自身日志 |
| AB target pipeline 状态同步（pipeline callback）| 基础设施层状态变更 | 可考虑后续加入 `activity_log`，V1 先不做 |
| AB 调度报告任务创建/停止 | 已在 Experiment/Update status→Online/Offline 的 `extra` 中引用 | 不单独成行 |
| Asset 收藏（Add/Remove）| 轻量交互，排障价值低 | 保持现有 `asset_favorite` 表 |
| Asset 权限变更 | V1 先不做，后续可扩展 | 无当前落点 |
| 项目成员增删/角色变更 | global 管理活动域 | `global.activity_log` |
| 组织成员管理 | global 管理活动域 | `global.activity_log` |

## 5. 接入策略

### 5.1 公共接入原则

接入不依赖 `AssetOperator` 全覆盖，因为现状只注册了 Chart / Dashboard，且接口只覆盖 CRUD。

所以写入分两类：

1. **可借已有对象服务集中接入的路径**：在对象 service 成功路径直接调用 `activity.Log`
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

以下基础设施不能作为活动覆盖的判断标准：

| 基础设施 | 当前现状 | 不能当真相的原因 |
|----------|---------|----------------|
| `AssetOperator` | 只注册了 Chart / Dashboard，接口只含 CRUD | 覆盖不到 Cohort、AB、copy、status_change、relate/unrelate |
| `asset_behavior` | 主要是 view 行为，modify / delete / add 基本死代码 | 不是可靠活动系统 |
| AB 自带历史页 | 只能看 AB，且数据嵌在 JSONB 里 | 不能代表统一活动已完成 |
| Metric history 表 | 只覆盖 define 变更 | 不能代表元数据对象已统一活动 |
| Pipeline `exec_info` / `batch_export_run` | 仅系统执行日志 | 不记录谁做了 CRUD 操作 |

## 5.4 开发者体验（DX）

### 5.4.1 接入文档

V1 需提供以下开发者文档，确保业务团队能独立完成接入：

| 文档 | 内容 | 交付形式 |
|------|------|---------|
| **ActivityService 接入指南** | Log() / BatchLog() 的调用契约、必填字段、detail 构建规范、常见错误 | Markdown 纳入项目文档 |
| **对象类型接入模板** | 每个 ItemType 的 Log() 调用示例（含 create/update/delete/copy 四类动作） | Go 示例代码文件 |
| **WritePolicy 选择指南** | required_full / required_core / best_effort 的适用场景、选择决策树、默认值 | Markdown 纳入项目文档 |
| **diff 引擎使用说明** | ChangesBetween 的调用方式、排除字段注册、敏感字段掩盖配置 | Markdown + 注释示例 |

### 5.4.2 测试辅助工具

V1 需提供以下测试基础设施，降低接入门槛：

| 工具 | 说明 | 用途 |
|------|------|------|
| `activitytest.MockService` | 内存实现 ActivityService 接口，写入不依赖 DB | 单元测试中验证写入调用 |
| `activitytest.AssertLogWritten` | 断言某条活动记录已被写入的 helper | 集成测试中验证活动行为 |
| `activitytest.AssertChangesContains` | 断言 detail.changes 中包含特定字段变更的 helper | 验证 diff 正确性 |

MockService 的行为约定：
- `Log()` 默认直接返回 nil（不检查写入），调用方可配置预期的 error 返回值（测试写入失败场景）
- `BatchLog()` 默认逐条追加到内存切片，调用方可读取已写入记录进行断言
- 不提供与真实 DAO 行为完全一致的 mock（那是集成测试的职责），只验证"是否调用了 Log"以及"传入了什么参数"

## 6. 交付阶段

### Phase 0：基础设施

- 建 `activity_log` 表
- 建 `activity` 域类型、DAO、公共服务、序列化器、diff 引擎
- 建 OP / 内部查询 API
- 项目对象 live-write 统一走活动模块中心化 policy；Phase 0 要先建好 policy registry、默认规则和 override 机制，再接对象写入

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

> 组织/项目管理活动和账号活跃字段为独立链路，见 [plan-org.md](./plan-org.md) 和 [plan-account.md](./plan-account.md)，不阻塞 Phase 0/1/2。

## 7. 评审结论

经 autoplan 全流程审查（CEO Review + Eng Review + DX Review + Final Approval Gate），以下议题已锁定：

1. **活动一致性等级**：采用中心化 `WritePolicy`（`required_full` / `required_core` / `best_effort`），由活动模块按对象和动作语义决策，不由调用方自由传参。

2. **detail 大对象字段裁剪策略**：统一采用”对象投影 -> JSON envelope -> 超限截断并记录 `truncated_fields` / `payload_hash`”三段式。

3. **detail_payload 存储格式**：暂不锁定，移入 `discussion.md` 按规模分阶段评估（JSONB / TEXT+LZ4 / 保持 TEXT），V1 先用 TEXT，后续可按需原地 ALTER。详见 [discussion.md](./discussion.md) D-P4。

4. **CDC/Outbox/Trigger 方案**：已排除，保留显式 `Log()`/`BatchLog()` 调用路径。排除理由见 4.3.4 节。

5. **开发者体验（DX）**：补充接入文档（4 份）和测试辅助工具，详见 5.4 节。

6. **设计原则**：新增第 5 条 Enterprise Traceability，详见 spec.md。

## 8. 验证方案

### 8.1 单测

- diff 引擎：字段排除、敏感字段掩盖、create/update/delete/copy 四类 envelope
- detail 序列化：JSON envelope、超限截断、降级 warning
- 历史迁移映射：AB 操作类型映射、Metric define 映射、空 operator_name 兜底

### 8.2 集成测试

- Chart / Dashboard / Cohort / AB / Metric 的成功路径写活动
- Phase 2 元数据对象 `TRACKED_EVENT` / `VIRTUAL_EVENT` / `EVENT_PROPERTY` / `USER_PROPERTY` / `VIRTUAL_PROPERTY` 的 create / update / delete 写活动
- OP / 内部对象查询接口分页、排序、过滤
- OP / 内部对象查询接口的 project scope 鉴权、掩码字段读取、删除后对象查询
- 迁移脚本重复执行的幂等性

### 8.3 故障注入

- 活动 DAO insert 失败
- detail 超大
- operator 信息缺失
- 迁移批次重复执行
- OP 查询权限不足

这里必须对照中心化 `WritePolicy`，验证主业务是回滚、detail 降级还是继续成功。

### 8.4 尚未锁定、需对象 owner 确认的边界

1. Chart `config` 的稳定投影边界——投影哪些字段、排除哪些
2. Dashboard `layout_overrides` 是否全量记录还是仅记录受影响 chart
3. AB `details` 需要暴露到什么摘要层级
4. ~~是否记录 IP 地址~~ — 已定：V1 不记录


> **审查历史**: 本 plan 已通过 autoplan 全流程审查（CEO Review + Eng Review + DX Review + Final Approval Gate），审查记录已归档。详见 [decisions.md](./decisions.md)。
