# 功能规格：项目内对象操作审计日志

**目录**: `20260626-Wave-Feat-AddAuditLog`
**创建日期**: 2026-06-26
**状态**: Draft — 讨论中，无定论
**输入**: "Wave 需要一个项目级的操作记录，存储到 PG，所有项目内对象的关键操作（创建/改/删）都要记录，方便审计"

---

## 背景

现有几套分散的操作记录系统：

| 系统 | 位置 | 存储方式 | 范围 |
|---|---|---|---|
| OP Console 审计 | `global.op_operation_log` | 独立表，before/after 快照 | 客户/组织/项目级运维操作 |
| AB 模块操作记录 | `ab_feature_flag.details` JSONB 内嵌 | 内嵌在 Feature Flag 行内 | AB 实验/Feature Flag 的状态变更 |
| Metric 历史 | `meta.metric_define_history` | 独立表，old/new define | 指标口径变更 |

现状问题：
- AB 模块的操作记录内嵌在 JSONB 中，无法独立查询、无法跨对象统一审计
- Metric 有独立历史表，但与 AB / OP / 其他对象没有统一规范
- 缺少统一的**项目内对象审计规范**，不同对象各自演化
- Asset Behavior 表仅记录轻量操作（无快照），定位是分析而非审计

新系统定位为**项目内对象的统一审计规范与落盘基础设施**，用于统一收口项目内对象的关键操作记录。这里的“对象”明确包含两类：

- **资产对象**：Chart / Dashboard / Cohort / Experiment / Feature Gate / Feature Config
- **元数据对象**：Metric / Event / Property 等

审计规范**不沿用当前 asset 基础设施的概念作为范围真相**，而是使用审计域自有的对象模型。

这里的“项目级”是**容器边界**，不是“按项目维度浏览历史”的主要查询视角：

- **项目内对象** = 本规格的主线，写入项目内标准审计表，查询按 `object_type + object_id` 查看历史
- **组织 / 项目级管理操作** = 也必须有审计记录，但允许采用独立于项目内标准表的落盘方式
- **账号活跃信息** = 最近登录 / 登出 / 活跃时间，落在 `account` 表或等价账号主表，不进入项目对象审计表

当前 Wave 代码里确实存在 **Asset 域** 和 **Metadata / Catalog 域** 两套分类：

- Asset 域：当前由 `AssetType` + `asset_base_view / asset_view` + `asset_permission / favorites / asset_reference / asset_behavior` 共同定义
- Metadata / Catalog 域：当前由 `CatalogChangeType` 和 QE catalog 刷新链路定义，包含 event / property / metric 等对象

但要注意：**当前 asset 基础设施本身并不完善，不能直接拿来当产品范围的真相来源**。例如：

- `AssetOperator` 当前只注册了 Chart / Dashboard，其他类型仍是 TODO
- `AssetOperator` 接口本身只覆盖 CRUD，不覆盖 copy / status change / relate 等关键操作
- 部分资产能力在不同入口里的覆盖也不一致（如首页搜索的 assetTypes 列表与完整 AssetType 集合并不完全同步）

因此，V1 的范围应理解为：

- **项目内对象审计规范**是主线，不沿用 asset 作为顶层概念
- **AB 与 Metric 必须纳入同一套标准规范**
- **事件、属性等元数据对象**同样属于该规范适用范围
- **组织 / 项目级管理操作**需要审计，但允许单独设计落盘
- **账号最近登录 / 登出 / 活跃时间**走账号表字段，不纳入项目对象审计表

说明：
- `Cohort` 同时属于 Asset 域和 Metadata / Catalog 域
- `Metric` 在当前代码里更偏 Metadata / Catalog，但根据本轮共识需要纳入统一审计规范
- `Event / EventProperty / UserProperty / VirtualProperty / VirtualEvent` 属于元数据对象，不叫资产，但同样适用统一审计规范

---

## 价值定位

当前最明确的直接价值，不是“先做一个对外售卖的企业合规功能”，而是**内部排查问题时，能够快速回答 who / what / when / where**。

用户已明确：当前最真实的使用场景是**内部排查问题**。因此，V1 不应假设自己已经是一个成熟的企业级合规产品，而应优先解决“出了问题后能快速还原最近变更链路”这个核心场景。同时，设计上要显式为另外 3 类更长期的商业价值预留空间。

这次需求的 4 个价值点，按当前判断排序如下：

1. **事故止损 / 根因定位（P0）**：对象被改坏、配置异常、指标口径变化后，内部同学可以沿着单对象历史快速定位最近一次关键变更，不再同时翻多套历史机制
2. **组织放权 / 权责清晰（P1）**：多人协作编辑实验、配置、指标时，团队敢于下放操作权限，因为事后能明确是谁在什么时间做了什么改动
3. **成交门槛 / 企业信任（P1）**：当产品进入更强安全评审或采购流程时，可以证明敏感对象变更具备可追溯能力，而不是临时靠查库和问人
4. **治理基础设施（P2）**：未来若引入审批、变更告警、敏感操作规则、自动化治理，统一审计模型可以作为底座复用

### 设计原则

1. **Troubleshooting First**：V1 的查询路径、索引策略、字段取舍，应优先服务对象历史排查，而不是先做宽泛的人维度报表或 BI 分析
2. **Attribution by Default**：任何进入统一审计规范的记录，都应默认保留对象、操作、操作人、来源、时间和必要变更详情，确保责任链路完整
3. **Enterprise-Ready, Not Enterprise-Heavy**：设计上要支持未来走向更强企业审计能力，但 V1 不为假想合规要求过度建设，避免把当前最核心的排障价值稀释掉
4. **Governance-Extensible**：审计 detail、对象类型和查询模型要保持稳定与可扩展，使后续审批、告警、策略控制可以建立在同一套历史记录之上

---

## 用户故事

### 用户故事 1 — 项目内对象操作自动记录审计日志 (P0)

作为系统，在用户对任意项目内对象执行创建、修改、删除操作时，自动记录一条包含操作人、操作类型、变更详情和时间的审计日志。

**优先级理由**: 这是核心需求，无此功能则系统无意义。系统必须对每个关键操作定义一致且可验证的审计写入策略。

**独立测试**: 创建一个 Chart，修改其名称，删除该 Chart，验证三条审计日志（create/update/delete）均正确写入且内容完整；再更新一个 Metric Define，验证元数据对象也能按同一规范写入。

**验收场景**:

1. **Given** 用户在项目内，**When** 创建一个类型为 `CHART` 的对象，**Then** 审计日志中出现一条 `action_type = "create"` 的记录，detail 优先以结构化 `changes[]` 记录初始字段，必要时可补充 `extra` 或 `snapshot`
2. **Given** 用户已创建了一个 `DASHBOARD` 对象，**When** 修改该 Dashboard 的名称，**Then** 审计日志中出现一条 `action_type = "update"` 的记录，detail 中包含变更前后的字段差异
3. **Given** 用户更新一个 `METRIC` 对象的 define，**When** 提交保存，**Then** 审计日志中出现一条 `action_type = "update"` 的记录，detail 中包含 define 字段的结构化变更
4. **Given** 用户已创建了一个对象，**When** 删除该对象，**Then** 审计日志中出现一条 `action_type = "delete"` 的记录，detail 至少包含对象名称快照；必要时允许补充 `snapshot`，但不强制完整快照

---

### 用户故事 2 — 审计日志查询与检索 (P0)

作为项目管理员，能够从指定对象的历史视角分页查询审计日志，按时间倒序查看单个对象的变更历史。

**优先级理由**: 仅记录可查才有审计价值，无查询能力等同于没有记录。

**独立测试**: 对同一个 Chart 连续执行创建、修改、删除操作，按 `object_type + object_id` 查询，验证仅返回该对象的日志且分页正确。

**验收场景**:

1. **Given** 项目中存在多个对象的审计日志，**When** 按 `object_type = CHART` 且 `object_id = 123` 查询，**Then** 返回结果仅包含该对象的日志
2. **Given** 某对象存在多条审计日志，**When** 按 `created_at DESC` 查询，**Then** 最近的操作排在最前面
3. **Given** 某对象的审计日志量超过一页，**When** 分页查询第 2 页，**Then** 返回第 2 页数据，`total` 字段正确标识该对象的总记录数

---

### 用户故事 3 — 非 CRUD 与历史兼容记录 (P1)

作为系统，对于 AB Feature Flag/Experiment 等对象的状态变更操作（如上线、下线、发布），以及 Metric 的口径变更，也能通过统一审计规范记录，并兼容现有 AB / Metric 历史记录。

**优先级理由**: AB 模块已内嵌操作记录、Metric 已有独立历史表，但都无法纳入统一规范。新系统需要将这些历史机制统一到同一审计模型下。

**独立测试**: 创建一个 AB Feature Flag，执行 debug → online → release 状态变更序列，验证每条状态变更在审计日志中有对应记录；更新一个 Metric Define，验证历史记录按统一规范写入；最后验证 AB 与 Metric 历史数据均可迁移到新规范。

**验收场景**:

1. **Given** 一个 AB Feature Flag 从 DRAFT 状态切换到 DEBUG，**When** 触发上线操作，**Then** 审计日志记录 `action_type = "update"`，detail 的 changes[] 中包含 `{"field": "status", "action": "changed", "before": "DRAFT", "after": "DEBUG"}`
2. **Given** 一个 Experiment 被复制，**When** 执行复制操作，**Then** 审计日志记录 `action_type = "copy"`，detail 中包含原始 Experiment ID 和新 Experiment ID
3. **Given** 一个 Metric Define 发生口径变更，**When** 执行更新，**Then** 审计日志记录 `action_type = "update"`，detail 中包含口径字段的 before / after
4. **Given** 已存在的 AB / Metric / Wave 历史操作记录，**When** 新系统上线并完成历史复制，**Then** 旧数据已复制到新审计规范，升级后的新操作只写新审计表，旧字段或旧表保留不删

---

### 用户故事 4 — 组织 / 项目级管理操作记录 (P1)

作为系统，组织级和项目级的管理操作也需要被审计记录，但允许与项目内对象审计使用不同的存储结构或表。

**优先级理由**: 用户已经明确要求组织、项目的操作也必须有记录；但这些操作不一定与项目内对象共享同一张表，因此需要在规格中单独建模。

**独立测试**: 修改组织配置、调整项目配额或模板，验证系统产生对应的审计记录，且记录链路不依赖 `project_audit_log` 单表假设。

**验收场景**:

1. **Given** 管理员修改组织配置，**When** 保存成功，**Then** 系统产生一条组织级审计记录
2. **Given** 管理员调整项目配额或项目模板，**When** 保存成功，**Then** 系统产生一条项目级管理审计记录
3. **Given** 技术方案决定组织 / 项目级管理操作不复用 `project_audit_log`，**When** 实施方案落地，**Then** 规格仍明确其记录责任、查询责任和存储位置

---

### 用户故事 5 — 审计失败策略明确化 (P1)

作为系统设计者，需要在审计日志写入失败时有明确、可验证的失败策略，并在强审计与 best-effort 之间做显式取舍。

**优先级理由**: 该决策会直接影响事务边界、延迟目标、接口语义和用户体验，必须在技术方案评审中明确。

**独立测试**: 模拟 PostgreSQL 写入超时，根据最终选定策略验证主流程行为、错误返回和日志/告警表现与规格一致。

**验收场景**:

1. **Given** 技术方案选择 best-effort，**When** 数据库审计表写入失败（如连接超时），**Then** 对象主操作仍成功返回，审计失败被记录到日志或告警系统
2. **Given** 技术方案选择强审计，**When** 数据库审计表写入失败，**Then** 对象主操作按约定失败，并向调用方返回可识别的错误

---

## 边界情况

- **批量操作** → 用户批量删除多个同类型对象（如 50 个 Chart），审计应该批量写入，避免逐条插入的性能开销
- **超大变更详情** → detail 字段可能包含大型 JSON（如 Chart 的完整配置），优先使用 LZ4 压缩（复用 `pkg/lib/util/compress.go`），压缩后仍超过 64KB 则逐字段截断并记录警告
- **同对象高频操作** → 同一对象在短时间内被频繁修改（如自动保存场景），需要明确记录每一条变更而不是合并去重
- **操作人或对象名变更** → 审计日志中的 operator_name / object_name 是写入时的快照，历史记录不随名称变更而更新
- **跨项目查询** → 审计表在 meta schema 下按项目隔离，当前不提供跨项目全局查询（如有需要后续可在 global schema 加汇总或由调用方自行跨 schema 查询）
- **协作环境** → 多人同时操作同一对象，审计日志独立记录每条操作，无锁冲突
- **无对象 ID 的操作** → 极少数项目内规范对象可能不关联具体对象 ID，允许 `object_id = 0` 并用 `object_type` 特殊值标识；组织 / 项目级管理操作优先考虑独立记录链路

---

## 需求

### 功能需求

- **FR-001**: 系统 MUST 为项目内对象定义统一审计规范，并在 meta schema 下提供 `project_audit_log` 作为项目内对象的标准落盘表
- **FR-002**: 系统 MUST 提供 `AuditService` 作为公共写入入口，支持 `Log(ctx, input)` 和 `BatchLog(ctx, inputs)` 方法
- **FR-003**: 审计规范 MUST 使用审计域自有的 `ObjectType` 体系，不直接沿用 `def.AssetType` 作为顶层概念；该体系至少能表达资产对象与元数据对象
- **FR-004**: 对已接入现有基础设施的对象类型（如 CHART / DASHBOARD），系统 MUST 在对象操作实现中自动调用审计记录
- **FR-005**: 对非 AssetOperator、元数据对象或需要手动控制写入的场景，系统 MUST 支持模块直接调用 `AuditService.Log()` 记录
- **FR-006**: 系统 MUST 提供按对象视角的审计日志分页查询接口，V1 至少支持 `project_id`、`object_type`、`object_id` 过滤，按 `created_at DESC` 排序
- **FR-007**: AB / Metric / Wave 项目内历史操作记录 MUST 复制到新审计规范中，旧字段或旧表保留不删；升级后新操作只写新审计表
- **FR-008**: 组织 / 项目级管理操作 MUST 具备审计记录能力，但允许采用独立于 `project_audit_log` 的存储结构或表
- **FR-009**: 账号最近登录时间、最近登出时间、最近活跃时间 MUST 记录在 `account` 表或等价账号主表，不要求写入 `project_audit_log`
- **FR-010**: 系统 MUST 在技术方案中明确审计写入的一致性等级与失败策略（强审计或 best-effort）；当前评审前不预设 `LogWithFallback` 为最终结论
- **FR-011**: 系统 MUST 保留支撑内部排障所需的最小归因信息集合：`object_type`、`object_id`、`action_type`、`operator_id`、`operator_name`、`source`、`created_at` 以及必要的结构化变更详情
- **FR-012**: V1 的查询接口与索引策略 MUST 优先优化“单对象最近变更链路”的排查路径，而不是优先满足按操作人、按组织范围的广义分析型查询
- **FR-013**: 审计模型 MUST 为未来的企业信任与治理场景预留扩展点，包括但不限于敏感字段掩盖、可见性控制、保留策略、审批/告警类上层能力，但这些能力当前不作为 V1 上线前置条件

### 非功能需求

- **NFR-001**: 在既定一致性模式下，审计日志写入的额外延迟目标为 < 50ms（P99）；若方案选择强审计，需在评审中重新确认该指标
- **NFR-002**: 项目内标准审计表在 meta schema 下按项目隔离，配合 `(object_type, object_id, created_at DESC)` 索引，单项目千万级日志量下查询响应 < 1s（P99）
- **NFR-003**: 单条 detail 序列化后最大 64KB，超出时优先 LZ4 压缩；压缩后仍超出则逐字段截断并记录警告
- **NFR-004**: 审计日志对用户不可见（无 UI 展示），仅通过 API 供管理端或未来审计页面调用

---

## 关键实体

### project_audit_log（meta schema 表）

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | BIGSERIAL | PK | 自增主键 |
| `project_id` | INTEGER | NOT NULL | 所属项目 ID（数据冗余，meta schema 已隔离） |
| `object_type` | VARCHAR(64) | NOT NULL | 审计对象类型：如 CHART / DASHBOARD / COHORT / EXPERIMENT / FEATURE_GATE / FEATURE_CONFIG / METRIC / TRACKED_EVENT / EVENT_PROPERTY / USER_PROPERTY / VIRTUAL_PROPERTY / VIRTUAL_EVENT |
| `object_id` | INTEGER | NOT NULL | 对象 ID |
| `object_name` | VARCHAR(255) | NOT NULL DEFAULT '' | 记录时的对象名称（快照） |
| `action_type` | VARCHAR(32) | NOT NULL | 操作类型：create / update / delete / copy |
| `operator_id` | INTEGER | NOT NULL | 操作人账号 ID |
| `operator_name` | VARCHAR(255) | NOT NULL DEFAULT '' | 记录时的操作人姓名（快照，不随用户改名而更新） |
| `source` | VARCHAR(32) | NOT NULL DEFAULT '' | 操作来源：web / openapi / mcp / internal |
| `detail_version` | SMALLINT | NOT NULL DEFAULT 1 | 审计详情版本号，支持后续兼容演进 |
| `detail_payload` | TEXT | NOT NULL DEFAULT '' | 版本化序列化审计详情；由应用层维护稳定 envelope，不使用 JSONB，不直接存业务结构体 |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT CURRENT_TIMESTAMP | 记录时间 |

**索引**（V1 精简，后续根据实际查询模式补充）:
- `idx_pal_object` ON `(object_type, object_id, created_at DESC)` — 主索引，按对象查询

### ObjectType（审计域对象类型）

审计域使用自有的 `ObjectType` 字符串体系，不直接复用 `def.AssetType`。当前至少需要表达：

- **资产对象**：`CHART` / `DASHBOARD` / `COHORT` / `EXPERIMENT` / `FEATURE_GATE` / `FEATURE_CONFIG`
- **元数据对象**：`METRIC` / `TRACKED_EVENT` / `VIRTUAL_EVENT` / `EVENT_PROPERTY` / `USER_PROPERTY` / `VIRTUAL_PROPERTY`
- **扩展原则**：后续新增对象类型时，通过增加新的 `ObjectType` 常量接入，无需改变整体审计模型

### 其他审计落点

- **组织 / 项目级管理审计**：允许使用独立于 `project_audit_log` 的存储结构，可以基于现有 `global.op_operation_log` 演进，也可以设计新的 global 级记录表
- **账号活跃字段**：`last_login_at` / `last_logout_at` / `last_active_at` 记录在 `account` 表或等价账号主表，不进入项目对象审计表

### AuditService（公共审计服务）

- `Log(ctx, input)` → `error`：单条写入，失败返回 error
- `BatchLog(ctx, inputs)` → `error`：批量写入
- `ListByQuery(ctx, query)` → `(items, total, error)`：分页查询

若最终选择 best-effort，可在服务层额外提供 `LogWithFallback(ctx, input)` 或等价包装；该接口当前不是已锁定的 V1 契约。

**AuditWriteInput**:
| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| ProjectID | int64 | 是 | |
| ObjectType | string | 是 | 审计域自有类型，不要求必须来自 `def.AssetType` |
| ObjectID | int64 | 是 | |
| ObjectName | string | 否 | 写入时快照 |
| ActionType | string | 是 | |
| OperatorID | int64 | 是 | 自动从 ctx 解析 |
| OperatorName | string | 否 | 写入时快照，自动从 ctx 解析 |
| Detail | `*AuditDetail` | 否 | 审计域详情对象，不直接传业务结构体 |

### action_type 约定

操作类型采用精简枚举，状态变更等非 CRUD 语义通过 `changes[]` 中的字段名和值表达：

- `create` — 创建
- `update` — 更新
- `delete` — 删除
- `copy` — 复制

### detail schema 约定

参考 PostHog 的结构化变更设计，审计详情采用**结构化 diff 优先**的稳定 envelope，避免直接持久化当前业务结构体，降低历史兼容成本：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `name` | string | 否 | 资源的展示名称（记录时的快照） |
| `changes` | Change[] | 否 | 字段级变更列表 |
| `extra` | object | 否 | 领域特定扩展信息（如 copy 的 source） |
| `snapshot` | object | 否 | 必要时补充的稳定快照片段，不要求完整业务结构体 |

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

**delete 场景**（变更可以为空，至少保留 name 快照，必要时补充 snapshot）:
```json
{
    "name": "已删除图表",
    "snapshot": {
        "version": 7,
        "dashboard_id": 42
    }
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
        "source_object_id": 123,
        "source_object_name": "原始名称"
    }
}
```

### 排除字段体系

为避免 `last_accessed_at`、`updated_at` 等噪音字段淹没审计日志，系统需实现三层排除：

| 层级 | 范围 | 说明 |
|------|------|------|
| **通用排除** | 全对象类型 | `id`, `created_at`, `updated_at`, `created_by`, `last_modified_by` 等系统字段不参与 diff |
| **按类型排除** | 按 ObjectType | 各对象类型声明自身不参与审计的字段（如 COHORT 的 `last_calculated_at`） |
| **变更级别排除** | 针对特定实体 | 仅当变更字段出现在此列表时才完全跳过该次记录（如 Dashboard 的 `last_accessed_at`） |

### 敏感字段掩盖

涉及敏感信息的字段，在 `before` / `after` 中统一替换为字符串 `"masked"`：
- Integration 的 `config`, `sensitive_config`
- User 的 `email`, `password`
- 各对象类型自行声明敏感字段列表

### 变更检测（diff 引擎）

系统应提供通用 diff 函数 `ChangesBetween(old, new, objectType)`，比较两个模型实例的稳定审计字段并生成 `[]Change`。对象类型通过注册排除字段、敏感字段和必要的字段映射来控制 diff 行为，避免把当前业务结构直接写入审计载荷。

### 依赖接口

各对象类型需配合提供获取当前对象快照的能力（用于 diff 引擎在 update 前读取旧值）：
- `GetSnapshot(ctx, objectType, objectID) → (interface{}, error)` — 可选接口，非必实现

---

## 成功标准

### 可量化指标

- **SC-001**: 项目内统一审计规范至少覆盖首批对象类型：CHART / DASHBOARD / COHORT / EXPERIMENT / FEATURE_GATE / FEATURE_CONFIG / METRIC；事件与属性对象遵循同一模型接入
- **SC-002**: 审计失败策略在真实故障注入场景下与最终选定方案一致（强审计或 best-effort）
- **SC-003**: P99 审计写入延迟 < 50ms（含 detail 序列化和 DB INSERT；若选择强审计需在评审中重新确认）
- **SC-004**: AB / Metric / Wave 历史操作记录完成复制，新操作全部走新审计表，旧的 `details.operation_records` 或历史表保留不删
- **SC-005**: 单项目百万级审计日志下，按 `object_type + object_id` 的分页查询响应 < 500ms（P99）
- **SC-006**: 组织 / 项目级管理操作具备清晰的审计落点；账号最近登录 / 登出 / 活跃时间具备明确字段落点
- **SC-007**: 单个对象的最近变更链路可以通过统一审计查询路径直接获得 `who / what / when / source`，内部排障不再需要同时翻 AB 内嵌记录、Metric 历史表和 Asset Behavior 等多套机制

---

## 待澄清问题

> 当前为讨论阶段，以下问题待确认：
> - 各对象类型排除字段清单（需各模块确认）
> - 敏感字段掩盖粒度
> - 审计 detail payload 的最终物理编码/压缩方案
> - 审计一致性等级（强审计 vs best-effort）及失败策略
