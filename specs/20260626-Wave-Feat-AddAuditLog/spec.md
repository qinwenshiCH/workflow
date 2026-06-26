# 功能规格：项目级操作审计日志

**目录**: `20260626-Wave-Feat-AddAuditLog`
**创建日期**: 2026-06-26
**状态**: Draft — 讨论中，无定论
**输入**: "Wave 需要一个项目级的操作记录，存储到 PG，所有项目内资产的关键操作（创建/改/删）都要记录，方便审计"

---

## 背景

现有两个分散的操作记录系统：

| 系统 | 位置 | 存储方式 | 范围 |
|---|---|---|---|
| OP Console 审计 | `global.op_operation_log` | 独立表，before/after 快照 | 客户/组织/项目级运维操作 |
| AB 模块操作记录 | `ab_feature_flag.details` JSONB 内嵌 | 内嵌在 Feature Flag 行内 | AB 实验/Feature Flag 的状态变更 |

现状问题：
- AB 模块的操作记录内嵌在 JSONB 中，无法独立查询、无法跨资产审计
- 缺少统一的资产审计入口，各资产类型要记录操作需自建机制
- Asset Behavior 表仅记录轻量操作（无快照），定位是分析而非审计

新系统定位为**项目内资产的审计基础设施**，统一收口所有资产类型的关键操作记录。

---

## 用户故事

### 用户故事 1 — 资产操作自动记录审计日志 (P0)

作为系统，在用户对任意项目内资产执行创建、修改、删除操作时，自动记录一条包含操作人、操作类型、变更详情和时间的审计日志。

**优先级理由**: 这是核心需求，无此功能则系统无意义。审计日志必须随每个关键操作可靠写入。

**独立测试**: 创建一个 Chart，修改其名称，删除该 Chart，验证三条审计日志（create/update/delete）均正确写入且内容完整。

**验收场景**:

1. **Given** 用户在项目内，**When** 创建一个类型为 CHART 的资产，**Then** 审计日志中出现一条 `action_type = "create"` 的记录，detail 中包含新建资产的初始快照
2. **Given** 用户已创建了一个 DASHBOARD 资产，**When** 修改该 Dashboard 的名称，**Then** 审计日志中出现一条 `action_type = "update"` 的记录，detail 中包含变更前后的字段差异
3. **Given** 用户已创建了一个资产，**When** 删除该资产，**Then** 审计日志中出现一条 `action_type = "delete"` 的记录，detail 中包含被删除资产的最后快照

---

### 用户故事 2 — 审计日志查询与检索 (P0)

作为项目管理员，能够按资产类型、操作人、操作类型、时间范围等条件分页查询审计日志，追溯资产变更历史。

**优先级理由**: 仅记录可查才有审计价值，无查询能力等同于没有记录。

**独立测试**: 按资产类型 CHART、操作人 ID、时间范围三个过滤条件组合查询，验证返回结果精确匹配且分页正确。

**验收场景**:

1. **Given** 项目中有多条审计日志，**When** 按资产类型（如 CHART）过滤查询，**Then** 返回结果仅包含该类型的日志
2. **Given** 项目中有多条审计日志，**When** 按操作人 ID 和时间范围组合查询，**Then** 返回结果匹配该操作人在指定时间内的所有操作
3. **Given** 审计日志量超过一页，**When** 分页查询第 2 页，**Then** 返回第 2 页数据，total 字段正确标识总记录数
4. **Given** 按 `created_at DESC` 排序，**When** 查询审计日志列表，**Then** 最近的操作排在最前面

---

### 用户故事 3 — 非 CRUD 操作记录 (P1)

作为系统，对于 AB Feature Flag/Experiment 等资产的状态变更操作（如上线、下线、发布），也能通过审计系统记录，兼容现有 AB 模块的内嵌操作记录。

**优先级理由**: AB 模块已内嵌操作记录但无法统一查询。新系统需要替代其内嵌记录以提供全局审计视图。P1 因为可以先实现 CRUD 再覆盖 AB。

**独立测试**: 创建一个 AB Feature Flag，执行 debug → online → release 状态变更序列，验证每条状态变更在审计日志中有对应记录且 detail 包含状态变化。

**验收场景**:

1. **Given** 一个 AB Feature Flag 从 DRAFT 状态切换到 DEBUG，**When** 触发上线操作，**Then** 审计日志记录 `action_type = "update"`，detail 的 changes[] 中包含 `{"field": "status", "action": "changed", "before": "DRAFT", "after": "DEBUG"}`
2. **Given** 一个 Experiment 被复制，**When** 执行复制操作，**Then** 审计日志记录 `action_type = "copy"`，detail 中包含原始 Experiment ID 和新 Experiment ID
3. **Given** 已存在的 AB 内嵌操作记录，**When** 新系统上线并完成数据迁移，**Then** 旧数据已全量复制到新审计表，历史记录不受影响，旧字段保留不删

---

### 用户故事 4 — 审计日志容错 (P1)

作为系统，在审计日志写入失败时不影响主业务流程，保证资产操作不被阻塞。

**优先级理由**: 审计不能成为核心业务流程的依赖。如果 PG 写入审计失败，资产创建/更新/删除仍需成功完成。

**独立测试**: 模拟 PostgreSQL 写入超时，验证资产创建操作仍然成功返回，审计写入失败被静默吞掉并记录错误日志。

**验收场景**:

1. **Given** 数据库审计表写入失败（如连接超时），**When** 用户执行资产创建操作，**Then** 资产创建成功，审计写入失败被兜底处理并记录 error log
2. **Given** 审计写入失败，**When** 检查业务主流程，**Then** 主流程不受影响，事务不包含审计写入

---

## 边界情况

- **批量操作** → 用户批量删除多个同类型资产（如 50 个 Chart），审计应该批量写入，避免逐条插入的性能开销
- **超大变更详情** → detail 字段可能包含大型 JSON（如 Chart 的完整配置），优先使用 LZ4 压缩（复用 `pkg/lib/util/compress.go`），压缩后仍超过 64KB 则逐字段截断并记录警告
- **同资产高频操作** → 同一资产在短时间内被频繁修改（如自动保存场景），需要明确记录每一条变更而不是合并去重
- **操作人或资产名变更** → 审计日志中的 operator_name / asset_name 是写入时的快照，历史记录不随名称变更而更新
- **跨项目查询** → 审计表在 meta schema 下按项目隔离，当前不提供跨项目全局查询（如有需要后续可在 global schema 加汇总或由调用方自行跨 schema 查询）
- **协作环境** → 多人同时操作同一资产，审计日志独立记录每条操作，无锁冲突
- **无资产 ID 的操作** → 极少数操作可能不关联具体资产 ID（如全局配置变更），允许 asset_id 为 0 并用 asset_type 特殊值标识

---

## 需求

### 功能需求

- **FR-001**: 系统 MUST 在 meta schema 下提供 `project_audit_log` 表，统一存储所有资产类型的操作记录
- **FR-002**: 系统 MUST 提供 `AuditService` 作为公共写入入口，支持 `Log(ctx, input)` 和 `BatchLog(ctx, inputs)` 方法
- **FR-003**: 对已接入 AssetOperator 的资产类型（CHART / DASHBOARD），系统 MUST 在 CreateAsset / UpdateAsset / DeleteAsset 实现中自动调用审计记录
- **FR-004**: 对非 AssetOperator 或需要手动控制写入的场景，系统 MUST 支持模块直接调用 `AuditService.Log()` 记录
- **FR-005**: 系统 MUST 提供审计日志分页查询接口，支持按 project_id、asset_type、action_type、operator_id、date_from、date_to 过滤，按 created_at 排序
- **FR-006**: 系统 MUST 在审计写入失败时不阻塞主业务流程，采用 LogWithFallback 模式（先尝试写入，失败则兜底仅记录核心字段并打 error log）
- **FR-007**: AB 模块 MUST 完成全量迁移：存量 `details.operation_records` 全量复制到新审计表，旧字段保留不删；迁移后新操作直接写入新审计表；前端查询统一查新表

### 非功能需求

- **NFR-001**: 审计日志写入延迟 < 50ms（P99），通过异步/非阻塞模式确保不影响主操作延迟
- **NFR-002**: 审计表在 meta schema 下按项目隔离，配合 `(asset_type, asset_id, created_at DESC)` 索引，单项目千万级日志量下查询响应 < 1s（P99）
- **NFR-003**: 单条 detail 序列化后最大 64KB，超出时优先 LZ4 压缩；压缩后仍超出则逐字段截断并记录警告
- **NFR-004**: 审计日志对用户不可见（无 UI 展示），仅通过 API 供管理端或未来审计页面调用

---

## 关键实体

### project_audit_log（meta schema 表）

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | BIGSERIAL | PK | 自增主键 |
| `project_id` | INTEGER | NOT NULL | 所属项目 ID（数据冗余，meta schema 已隔离） |
| `asset_type` | VARCHAR(32) | NOT NULL | 资产类型：CHART / DASHBOARD / COHORT / EXPERIMENT / FEATURE_GATE / FEATURE_CONFIG |
| `asset_id` | INTEGER | NOT NULL | 资产 ID |
| `asset_name` | VARCHAR(255) | NOT NULL DEFAULT '' | 记录时的资产名称（快照） |
| `action_type` | VARCHAR(32) | NOT NULL | 操作类型：create / update / delete / copy |
| `operator_id` | INTEGER | NOT NULL | 操作人账号 ID |
| `operator_name` | VARCHAR(255) | NOT NULL DEFAULT '' | 记录时的操作人姓名（快照，不随用户改名而更新） |
| `source` | VARCHAR(32) | NOT NULL DEFAULT '' | 操作来源：web / openapi / mcp / internal |
| `detail` | JSONB | NOT NULL DEFAULT '{}' | 结构化变更详情，按资产类型约定 schema |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT CURRENT_TIMESTAMP | 记录时间 |

**索引**（V1 精简，后续根据实际查询模式补充）:
- `idx_pal_asset` ON `(asset_type, asset_id, created_at DESC)` — 主索引，按资产查询

### AuditService（公共审计服务）

- `Log(ctx, input)` → `error`：单条写入，失败返回 error
- `LogWithFallback(ctx, input)`：包装 Log，失败时静默兜底
- `BatchLog(ctx, inputs)` → `error`：批量写入
- `ListByQuery(ctx, query)` → `(items, total, error)`：分页查询

**AuditWriteInput**:
| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| ProjectID | int64 | 是 | |
| AssetType | def.AssetType | 是 | |
| AssetID | int64 | 是 | |
| AssetName | string | 否 | 写入时快照 |
| ActionType | string | 是 | |
| OperatorID | int64 | 是 | 自动从 ctx 解析 |
| OperatorName | string | 否 | 写入时快照，自动从 ctx 解析 |
| Detail | interface{} | 否 | JSON 序列化后写入 detail 字段 |

### action_type 约定

操作类型采用精简枚举，状态变更等非 CRUD 语义通过 `changes[]` 中的字段名和值表达：

- `create` — 创建
- `update` — 更新
- `delete` — 删除
- `copy` — 复制

### detail schema 约定

参考 PostHog 的结构化变更设计，detail 采用 `changes: [{field, action, before, after}]` 格式，精确到字段级变更，避免全量 snapshot 的存储浪费：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `name` | string | 否 | 资源的展示名称（记录时的快照） |
| `changes` | Change[] | 否 | 字段级变更列表 |
| `extra` | object | 否 | 领域特定扩展信息（如 copy 的 source） |

**Change 结构**:

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `field` | string | 是 | 变更的字段名 |
| `action` | string | 是 | `created` / `changed` / `deleted` |
| `before` | any | 否 | 旧值（该字段首次创建时为 null） |
| `after` | any | 否 | 新值（该字段被删除时为 null） |

**create 场景**（初始状态的字段列表）:
```json
{
    "name": "新图表",
    "changes": [
        {"field": "name", "action": "created", "after": "新图表"},
        {"field": "type", "action": "created", "after": "line"},
        {"field": "dashboard_id", "action": "created", "after": 42}
    ]
}
```

**update 场景**（仅包含有变更的字段）:
```json
{
    "name": "新名称",
    "changes": [
        {"field": "name", "action": "changed", "before": "旧名称", "after": "新名称"},
        {"field": "description", "action": "changed", "before": "旧描述", "after": "新描述"}
    ]
}
```

**delete 场景**（变更可以为空，只保留 name 快照）:
```json
{
    "name": "已删除图表"
}
```

**AB 状态变更 / 上线场景**（通过 changes[] 中的字段级变更表达状态变化）:
```json
{
    "name": "experiment-v2",
    "changes": [
        {"field": "status", "action": "changed", "before": "DRAFT", "after": "RUNNING"},
        {"field": "rollout_percentage", "action": "changed", "before": 0, "after": 100}
    ]
}
```

**copy 场景**:
```json
{
    "name": "副本名称",
    "extra": {
        "source_asset_id": 123,
        "source_asset_name": "原始名称"
    }
}
```

### 排除字段体系

为避免 `last_accessed_at`、`updated_at` 等噪音字段淹没审计日志，系统需实现三层排除：

| 层级 | 范围 | 说明 |
|------|------|------|
| **通用排除** | 全资产类型 | `id`, `created_at`, `updated_at`, `created_by`, `last_modified_by` 等系统字段不参与 diff |
| **按类型排除** | 按 AssetType | 各资产类型声明自身不参与审计的字段（如 COHORT 的 `last_calculated_at`） |
| **变更级别排除** | 针对特定实体 | 仅当变更字段出现在此列表时才完全跳过该次记录（如 Dashboard 的 `last_accessed_at`） |

### 敏感字段掩盖

涉及敏感信息的字段，在 `before` / `after` 中统一替换为字符串 `"masked"`：
- Integration 的 `config`, `sensitive_config`
- User 的 `email`, `password`
- 各资产类型自行声明敏感字段列表

### 变更检测（diff 引擎）

系统应提供通用 diff 函数 `ChangesBetween(old, new, assetType)`，通过反射或结构体标签比较两个模型实例的字段值，自动生成 `[]Change`。资产类型通过注册排除字段和敏感字段来控制 diff 行为。

### 依赖接口

各资产类型需配合提供获取当前资产快照的能力（用于 diff 引擎在 update 前读取旧值）：
- `GetSnapshot(ctx, assetType, assetID) → (interface{}, error)` — 可选接口，非必实现

---

## 成功标准

### 可量化指标

- **SC-001**: 覆盖全部 6 种资产类型（CHART / DASHBOARD / COHORT / EXPERIMENT / FEATURE_GATE / FEATURE_CONFIG）的 CRUD 操作审计
- **SC-002**: 审计写入不影响主业务流程——模拟 DB 故障时，资产创建/修改/删除仍然成功
- **SC-003**: P99 审计写入延迟 < 50ms（含 JSON 序列化和 DB INSERT）
- **SC-004**: AB 模块完成迁移，新操作全部走新审计表，旧的 `details.operation_records` 内嵌记录保留不删
- **SC-005**: 单项目百万级审计日志下，带过滤条件的分页查询响应 < 500ms（P99）

---

## 待澄清问题

> 当前为讨论阶段，以下问题待确认：
> - 各资产类型排除字段清单（需各模块确认）
> - 敏感字段掩盖粒度
> - AB 历史数据全量迁移实施方案
