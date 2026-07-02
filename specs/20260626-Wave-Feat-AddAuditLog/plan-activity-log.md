# 技术方案：活动日志 V1 最小闭环

> 统一的活动基础设施，覆盖 project item activity、global item activity、账号活跃字段。
> V1 只做可落地的活动记录闭环：写入、落库、查询、迁移。治理平台化能力只保留扩展点，不进入主实现。

---

## 1. 范围与链路分界

三条可独立实施的链路：

| 链路 | 存储 | 覆盖范围 |
|------|------|---------|
| **项目 item 活动**（主线） | `meta.activity_log` | Chart / Dashboard / Cohort / AB / Metric / Pipeline / Event / Property ... |
| **Global item 活动** | `global.activity_log` | 组织/项目生命周期、成员管理、Account API Token... |
| **账号活跃字段** | `global.account` 表 3 列 | last_login_at / last_logout_at / last_active_at |

**OP 操作记录**（`global.op_operation_log`）维持现状不变，OP 需要保持独立，OP 人员配置操作继续走既有链路。

### 1.1 首批对象范围

| 类别 | 对象 |
|------|------|
| 资产对象 | `chart` / `dashboard` / `cohort` / `experiment` / `feature_gate` / `feature_config` / `pipeline` / `campaign` |
| 元数据对象 | `metric` / `tracked_event` / `virtual_event` / `event_property` / `user_property` / `virtual_property` |
| Global item | `organization` / `project` / `org_member` / `project_member` / `account_api_token` / `account` |

### 1.2 非目标

- V1 不做官方产品端的新通用活动 UI（仅保留 AB / Metric 既有查看能力，其余走 OP / 内部接口）
- V1 不优先支持按 `operator_id`、按组织范围、按跨项目的活动分析
- V1 不从 `asset_behavior` 反推活动历史（其本质不是可靠活动源）
- V1 不做分区、TTL、审批、告警、可见性策略、复杂 redaction registry、`activity_log_target` 查询投影
- V1 不先做通用审计平台；只做业务活动日志的标准落点和最小查询闭环

---

## 2. 整体架构

### 2.1 三层架构

| 层 | 职责 | 代码位置 |
|----|------|---------|
| **业务 service** | 读旧快照 → 执行变更 → 读新快照 → 投影原始值 + 声明规则 → 调用 ActivityService | 业务域 service 包 |
| **activity 模块** | 接收原始投影 + 规则 → ChangesBetween → ApplyMaskRules → 补齐 operator → 敏感字段兜底 → 序列化 → 写入 | `service/activity/` |
| **存储层** | `meta.activity_log`（INTEGER PK）/ `global.activity_log`（BIGINT PK），两表结构一致 | DAO + PG |

边界规则：业务 service 决定"写不写"和"阻不阻塞"，activity 模块只执行写入策略。

### 2.2 写入流水线

活动日志写入在业务侧视角只有**四步**，Detail 构造由 ActivityService 统一完成。

| 步骤 | 谁执行 | 发生了什么 | 场景差异 |
| --- | --- | --- | --- |
| ① **业务执行** | 业务 service | 读旧值 → DB 变更 → 读新值 | Create: 无旧值；Update: 有旧+新；Delete: 有旧无新 |
| ② **投影 + 规则声明** | 业务 service | DAO struct → 投影为原始 `map[string]any`（不脱敏）；声明脱敏函数和 snapshot | 三场景共用同一投影函数；Create 的 old=nil，Delete 的 new=nil |
| ③ **调用写入** | 业务 service | `WriteLog` / `BatchWriteLog`，传原始投影 + 规则 | PolicyKey 由注册声明 |
| ④ **处理结果** | 业务 service | `err != nil` 时按 WritePolicy 决策：`blocking` 回滚事务、`best_effort` 记 warning | 由 PolicyKey 决定 |

ActivityService 内部（业务不感知）：校验 → `ChangesBetween(oldProj, newProj)` → 按声明对敏感字段 ApplyMaskRules → 补齐 operator/source → 敏感字段兜底拦截 → 组装 Detail（input.Snapshot 非空时追加）→ 序列化 → INSERT。

> **关于脱敏**：脱敏在 ChangesBetween **之后**做。原因是：投影时脱敏会丢失敏感字段的变更事件（old/new 都被抹成 `"***"`，ChangesBetween 认为没变）。先投影原始值 → 检测到变更 → 再抹值，才能同时保留"敏感字段被改了"的事实和防止敏感值泄露。详见 §5.7。

### 2.3 为什么不采用 CDC / Outbox / Trigger / pgAudit

| 方案 | 做法 | 为什么不适合 |
|------|------|-------------|
| DB Trigger | 表上挂触发器，INSERT/UPDATE/DELETE 时自动写日志 | 作为**行级历史留痕**可行，但不适合作为业务活动日志主方案：运行在数据库层面，拿不到完整请求上下文——不知道谁操作的、从哪个入口来的、出于什么意图（发布还是回滚），只有行级数据。详见 [plan-postgre-trigger.md](./plan-postgre-trigger.md) |
| pgAudit | PostgreSQL 扩展记录 SQL / 对象访问审计日志 | 作为**数据库层安全审计**可行，但不适合作为业务活动日志或行级历史主方案：输出是 PostgreSQL 日志，不是应用表；没有 OLD/NEW 快照，不知道 Wave 业务 action，且官方语义是 best-effort、非事务一致。详见 [plan-pg-audit.md](./plan-pg-audit.md) |
| CDC / WAL 订阅 | 监听数据库 WAL 日志流（如 Debezium），推送每行变更 | 适合做数据同步，但 WAL 内容只有"column X 从 A 变 B"，没操作人、没业务语义，且引入 replication slot 增加运维复杂度 |
| Outbox | 业务先写 outbox 表，异步 worker 消费后再写活动表 | 解耦了业务和活动，但引入消息投递、重试、幂等等额外基础设施。V1 主诉求是"即时排障"，异步反而增加不确定性 |

### 2.4 为什么不引入异步解耦（事件驱动 / 消息队列）

V1 不采用异步写入，不是因为"异步不好"，而是因为异步解决的问题在 V1 阶段不是真实瓶颈，且异步引入的代价与 V1 的核心价值冲突。

| 考虑 | 分析 |
|------|------|
| **写入量级** | 活动日志记录的是"人的操作"——改配置、发实验、增删对象。单次线上操作写 1-2 条记录，额外延迟 1-5ms。频率比业务数据写入低数个数量级，不到引入消息队列的阈值 |
| **排障即时性** | V1 首要价值是内部排障和根因定位。"我刚刚改了 Chart，怎么没生效"——同步写入保证事务提交即可查。异步引入最终一致性，排障时出现"活动还没写到"的体验不可接受 |
| **WritePolicy 已覆盖失败场景** | `best_effort` 策略下活动写入失败不阻塞业务，记 warning 继续。这已经解决了"活动拖慢主业务"的核心关切 |
| **异步的工程成本** | 消息队列 + 消费者 + 投递重试 + 幂等处理 + DLQ + 消费延迟监控 + 消费端故障排查。这些在 V1 都是没有明确收益的额外复杂度 |
| **事务一致性** | 当前方案在同一事务内保证活动和业务数据一致性。异步方案是最终一致，可能出现"业务写入成功但活动没记录"的窗口 |

**架构层面的正确做法是预留扩展点，不提前实现：**

```go
// ActivityWriter 接口，当前同步实现，未来可替换为异步实现
type ActivityWriter interface {
    Write(ctx context.Context, input *WriteInput) error
    BatchWrite(ctx context.Context, inputs []*WriteInput) error
}
```

V1 的 `syncWriter` 在当前事务内直接写入；如果未来某个域的活动量级增长到同步写入成为瓶颈，只需提供异步的 `asyncWriter` 实现（推 Kafka / Redis stream）。ActivityService 组合 `ActivityWriter`，业务代码不感知变化。

---


## 3. 数据模型

### 3.1 meta.activity_log（project schema）

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| `id` | BIGSERIAL | PK | |
| `item_type` | VARCHAR(64) | NOT NULL | 活动域自有类型，全小写，不沿用 `def.AssetType` |
| `item_id` | INTEGER | NOT NULL | meta schema 对象使用 INTEGER PK |
| `item_name` | VARCHAR(255) | NOT NULL DEFAULT '' | 写入时展示快照 |
| `action_type` | VARCHAR(32) | NOT NULL | `create / update / delete / copy`；扩展动作须统一注册 |
| `operator_id` | INTEGER | NOT NULL | 操作人 ID，不设外键。系统操作（无真实用户）填 0 |
| `operator_name` | VARCHAR(255) | NOT NULL DEFAULT '' | 写入时展示快照。系统操作填空字符串 |
| `source` | VARCHAR(32) | NOT NULL DEFAULT '' | `ui / api_token / internal / scheduler / backfill` |
| `correlation_id` | VARCHAR(64) | NOT NULL DEFAULT '' | 跨对象关联标识，基础设施自动生成或继承上下文 |
| `detail_payload` | TEXT | NOT NULL DEFAULT '{}' | 稳定 JSON envelope；V1 不使用 PG JSONB；查询返回解析后 `detail` |
| `occurred_at` | TIMESTAMPTZ | NOT NULL | 事件发生时间（历史迁移时回填原始事件时间） |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT CURRENT_TIMESTAMP | 入库时间 |

```sql
-- meta schema 的 item_id/operator_id 使用 INTEGER，与 meta 表主键类型一致。
CREATE TABLE IF NOT EXISTS meta.activity_log (
    id              BIGSERIAL    PRIMARY KEY,
    item_type       VARCHAR(64)  NOT NULL,
    item_id         INTEGER      NOT NULL,
    item_name       VARCHAR(255) NOT NULL DEFAULT '',
    action_type     VARCHAR(32)  NOT NULL,
    operator_id     INTEGER      NOT NULL,
    operator_name   VARCHAR(255) NOT NULL DEFAULT '',
    source          VARCHAR(32)  NOT NULL DEFAULT '',
    correlation_id  VARCHAR(64)  NOT NULL DEFAULT '',
    detail_payload  TEXT         NOT NULL DEFAULT '{}',
    occurred_at     TIMESTAMPTZ  NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_activity_log_item ON meta.activity_log (item_type, item_id, occurred_at DESC);
```

说明：
- 表在 project schema（meta）内，`project_id` 列冗余，不设
- `occurred_at` 与 `created_at` 语义明确区分

### 3.2 global.activity_log（web global schema）

结构同 `meta.activity_log`，仅 ID 类型使用 `BIGINT`（与 global schema PK 类型对齐）：

```sql
CREATE TABLE IF NOT EXISTS global.activity_log (
    id              BIGSERIAL    PRIMARY KEY,
    item_type       VARCHAR(64)  NOT NULL,
    item_id         BIGINT       NOT NULL,    -- global schema 对象使用 BIGINT PK
    item_name       VARCHAR(255) NOT NULL DEFAULT '',
    action_type     VARCHAR(32)  NOT NULL,
    operator_id     BIGINT       NOT NULL,    -- global schema 账号使用 BIGINT PK
    operator_name   VARCHAR(255) NOT NULL DEFAULT '',
    source          VARCHAR(32)  NOT NULL DEFAULT '',
    correlation_id  VARCHAR(64)  NOT NULL DEFAULT '',
    detail_payload  TEXT         NOT NULL DEFAULT '{}',
    occurred_at     TIMESTAMPTZ  NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

**索引**：
- `(item_type, item_id, occurred_at DESC)`

### 3.3 枚举规范

所有常量统一定义在 `activity/types.go`。命名使用字符串类型（非 int），自描述。

所有常量的**字符串值**统一为全小写。规则只一条：活动日志枚举值一律小写。

**ItemType** — 全小写，与 Go 常量标识符（PascalCase）区分：

```go
// Go 常量标识符保持 PascalCase，字符串值全小写
// 资产对象
ItemTypeChart           = "chart"
ItemTypeDashboard       = "dashboard"
ItemTypeCohort          = "cohort"
ItemTypeExperiment      = "experiment"
ItemTypeFeatureGate     = "feature_gate"
ItemTypeFeatureConfig   = "feature_config"
ItemTypePipeline        = "pipeline"
ItemTypeCampaign        = "campaign"

// 元数据对象
ItemTypeMetric          = "metric"
ItemTypeTrackedEvent    = "tracked_event"
ItemTypeVirtualEvent    = "virtual_event"
ItemTypeEventProperty   = "event_property"
ItemTypeUserProperty    = "user_property"
ItemTypeVirtualProperty = "virtual_property"

// Global schema item
ItemTypeOrganization    = "organization"
ItemTypeProject         = "project"
ItemTypeOrgMember       = "org_member"
ItemTypeProjectMember   = "project_member"
ItemTypeAccount         = "account"
ItemTypeAccountAPIToken = "account_api_token"
```

**ActionType** — 全小写描述性字符串：

```go
// 基础动作
ActionTypeCreate = "create"
ActionTypeUpdate = "update"
ActionTypeDelete = "delete"
ActionTypeCopy   = "copy"

// 扩展动作——状态流转（通过扩展注册评审）
ActionTypeOnline   = "online"     // AB 发布上线
ActionTypeOffline  = "offline"    // AB 下线
ActionTypeDebug    = "debug"      // AB 调试模式
ActionTypeRelease  = "release"    // AB 发布完成
ActionTypeLaunch   = "launch"     // Campaign 启动
ActionTypePause    = "pause"      // Campaign 暂停
ActionTypeResume   = "resume"     // Campaign 恢复
ActionTypeFinish   = "finish"     // Campaign 完成
ActionTypeStop     = "stop"       // Pipeline 停止
```

扩展原则：基础动作（create/update/delete/copy）覆盖对象的 CRUD 语义；状态流转类动作（如 online / launch / stop）在有明确状态迁移语义时注册为扩展 action_type。未注册字符串在 `WriteLog` / `BatchWriteLog` 入口直接拒绝。

注册流程：
1. 业务 owner 提交注册说明（动作名、适用 ItemType、无法用基础动作 + detail 表达的理由）
2. 活动模块 owner 审核命名，避免近义词分裂（如 `publish` / `release` / `online`）
3. 同一 PR 补齐：常量定义、detail 最小 schema、迁移映射、单测和接入示例

**Source** — 全小写：

```go
SourceUI       = "ui"         // 浏览器 Web UI 操作（Session 认证）
SourceAPIToken = "api_token"  // Account API Token 鉴权的编程访问
SourceInternal = "internal"   // 系统内部操作（冲突解决、迁移回调等）
SourceScheduler = "scheduler" // 定时任务自动操作
SourceBackfill = "backfill"   // 历史迁移回填
```

### 3.4 关键约定

- `item_name` / `operator_name` 是**写入时展示快照**——对象删除或操作人改名后仍可追溯，历史不回写
- `occurred_at` = 活动事件时间，`created_at` = 入库时间
- V1 不记录 IP 地址（`operator_id` 在排障场景已够用）
- V1 不引入 `detail_version`（serializer/parser 兼容由活动模块统一维护）

---


## 4. 写入模型

### 4.1 核心类型定义

所有类型统一定义在活动的 `types.go`。

```go
package activity

// ——— Detail 类型 ———
type Detail struct {
    Changes  []Change        `json:"changes,omitempty"`
    Snapshot json.RawMessage `json:"snapshot,omitempty"`
}

type Change struct {
    Field  string `json:"field"`
    Action string `json:"action"` // "created" | "changed" | "deleted"
    Before any    `json:"before,omitempty"`
    After  any    `json:"after,omitempty"`
}

const ChangeCreated = "created"
const ChangeChanged = "changed"
const ChangeDeleted = "deleted"

// 脱敏规则声明：字段名 → 脱敏函数。可用内置函数（MaskValue/MaskEmail）或自定义。
type MaskRules map[string]func(rawValue any) any

// ——— ActivityService 接口 ———
// ItemType 已在 registry 中声明所属 schema，Service 内部自动推导目标表。
// 调用方不需要区分 meta.activity_log 还是 global.activity_log。
type Service interface {
    WriteLog(ctx context.Context, input WriteInput) error
    BatchWriteLog(ctx context.Context, inputs []WriteInput) error
    ListByQuery(ctx context.Context, query Query) ([]Item, int64, error)
}

// ——— 写入输入 ———
// 业务方传原始投影 + 规则声明，ActivityService 统一完成 ChangesBetween、脱敏、Detail 组装。
type WriteInput struct {
    ItemType      string
    ItemID        int64
    ItemName      string    // 写入时快照，为空时从业务对象自动补齐
    ActionType    string
    PolicyKey     string    // 已注册接入场景（不落库），用于解析 WritePolicy

    // 标准路径：ActivityService 内部执行 ChangesBetween → ApplyMaskRules → 组装 Detail
    OldProjection map[string]any   // 投影原始值，Create 为 nil
    NewProjection map[string]any   // 投影原始值，Delete 为 nil
    MaskRules     MaskRules        // 字段名 → 脱敏函数
    Snapshot      *json.RawMessage // 可选：调用方按需传入的原始实体 dump，不走投影

    // 快捷路径：迁移/回填等特殊场景，跳过 ChangesBetween + ApplyMaskRules
    PreBuiltChanges []Change

    OccurredAt    *time.Time // 为空时取 time.Now()
    CorrelationID string     // 为空时自动生成 UUID
    // OperatorID/OperatorName/Source 不在 input 中作为可选字段暴露
    // ActivityService 统一从 ctx 解析并覆盖；回填场景通过 WithContext 模式设置
}

// ——— 查询 ———
// 与 WriteLog 一样，ItemType 在 registry 中已声明所属 schema（meta/global），
// ListByQuery 内部通过 ItemType 查 registry 推导目标表，调用方不需要指定。
type Query struct {
    ItemType   string   `json:"item_type"`
    ItemID     int64    `json:"item_id"`
    ActionType []string `json:"action_type,omitempty"` // 空 = 不过滤
    Page       int      `json:"page"`
    PageSize   int      `json:"page_size"`
}

type Item struct {
    ID            int64        `json:"id"`
    ItemType      string       `json:"item_type"`
    ItemID        int64        `json:"item_id"`
    ItemName      string       `json:"item_name"`
    ActionType    string       `json:"action_type"`
    OperatorID    int64        `json:"operator_id"`
    OperatorName  string       `json:"operator_name"`
    Source        string       `json:"source"`
    Detail        *Detail      `json:"detail"` // 已反序列化，非原始 TEXT
    OccurredAt    time.Time    `json:"occurred_at"`
}
```

`OperatorID` / `OperatorName` / `Source` 不在 input 中作为可选字段暴露——ActivityService 内部统一从 ctx 解析并覆盖，调用方不要自行填充。如需覆盖（如迁移回填），使用 `WithActivityContext` 将操作人注入 ctx：

```go
// 回填/migration 场景：ctx 里的操作人是脚本账号，需要覆写为历史操作人
ctx = activity.WithActivityContext(ctx, activity.ActivityContext{
    OperatorID:    oldRecord.OperatorID,      // 历史操作人
    OperatorName:  oldRecord.OperatorName,
    Source:        "backfill",
})

// 后续 WriteLog 将继承 ctx 中的 operator/source
// 普通 Web 请求则自动从请求鉴权中间件解析，无需手动调用
```

`WriteInput.CorrelationID` 为空时 ActivityService 自动生成 UUID；需要跨对象关联时调用方显式传入同一值。

写入目标表由 Service 根据 `ItemType` 注册表自动判断，调用方不感知 `meta.activity_log` 与 `global.activity_log` 的差异。

### 4.2 调用方视角

业务方只需把 DAO struct 投影为原始 `map[string]any`，声明脱敏规则，交给 ActivityService：

```go
// 业务传原始投影 + 规则，ActivityService 统一构造
input := activity.WriteInput{
    OldProjection: oldProj,               // 原始值，不脱敏
    NewProjection: newProj,               // 原始值，不脱敏
    MaskRules: activity.MaskRules{        // 字段名 → 脱敏函数
        "token_hash": func(v any) any { return "***" },
        "email":      activity.MaskEmail,
    },
    Snapshot: snapshotJSON,               // 可选：调用方按需传入原始实体
}
```

谁负责什么：

| 模块 | 负责 |
|------|------|
| 业务 service | 在正确事务点读取 old/new 快照；投影为 `map[string]any`；声明脱敏规则；按需传入 `Snapshot` |
| ActivityService | 接收原始投影 + 规则 → `ChangesBetween` → `ApplyMaskRules` → 组装 `*Detail` → 校验 → 序列化 → 落库 |

内置脱敏函数：

| 函数 | 效果 |
|------|------|
| `func(v any) any { return "***" }` | 固定占位值替换 |
| `activity.MaskEmail` | 保留首字符 + `***`（如 `a***@example.com`） |
| `activity.MaskTruncate(maxLen)` | 截断到指定长度 |

关键约束：
- **业务原始 struct 不直接进入 ActivityService**。业务方必须将 DAO struct 投影为 `map[string]any`，再填进 `WriteInput.OldProjection`/`NewProjection`。
- **投影保留原始值**（敏感字段也不脱敏）——ChangesBetween 需要真实值才能正确检测变更。脱敏在 ActivityService 内部 `ChangesBetween → ApplyMaskRules` 的顺序完成。
- **`PreBuiltChanges` 快捷路径**：用于迁移/回填等场景，跳过 ChangesBetween 和 ApplyMaskRules，直接使用已造好的 changes。V1 只有 backfill 场景走此路径。
- 如果只有噪音字段变化，ChangesBetween 返回空列表，ActivityService 自动跳过写入。

### 4.3 写入流程（ActivityService 内部）

```text
WriteLog(ctx, input)
│
├─ ① 校验 ItemType / ActionType / PolicyKey 已注册且 scope 匹配
├─ ② 从 ctx 解析 operator / source / correlation_id，补齐展示快照
│
├─ ③ 构造 Detail
│   ├─ (a) PreBuiltChanges 非空 → 直接用（快捷路径）
│   └─ (b) 标准路径：
│       ├─ ChangesBetween(input.OldProjection, input.NewProjection)
│       ├─ ApplyMaskRules(changes, input.MaskRules)
│       └─ 组装 Detail{Changes}；input.Snapshot 非空则追加到 Detail
│
├─ ④ 敏感字段兜底拦截（拒绝敏感字段名进入 detail）
├─ ⑤ 64KB 预算检查 + TEXT 序列化
├─ ⑥ 按 PolicyKey 取得 WritePolicy
├─ ⑦ DAO INSERT
└─ ⑧ 记录 metrics/log，按 policy 返回 error/warning/nil
```

### 4.4 写入失败策略（轻量映射）

V1 保留 `PolicyKey`，但它只是稳定接入场景名，不是复杂策略系统。业务 owner 在接入时注册该场景的失败返回行为；ActivityService 只负责按注册表执行，调用方不能在运行时自由传策略。

```go
// policy.go

type WritePolicy string

const (
    WritePolicyBlocking  WritePolicy = "blocking"
    WritePolicyBestEffort WritePolicy = "best_effort"
)

// ActivityPolicyKey 是稳定接入场景名，不由调用方运行时指定
type ActivityPolicyKey string

// 全局注册表，包加载时由业务模块 register 填充
var policyRegistry = map[ActivityPolicyKey]WritePolicy{}

func RegisterPolicy(key ActivityPolicyKey, policy WritePolicy) {
    if _, ok := policyRegistry[key]; ok {
        panic(fmt.Sprintf("activity: duplicate policy key: %s", key))
    }
    policyRegistry[key] = policy
}

func resolvePolicy(key ActivityPolicyKey) (WritePolicy, bool) {
    p, ok := policyRegistry[key]
    return p, ok
}
```

| 策略 | 活动主行 | detail_payload | 返回语义 |
|------|---------|---------------|----------|
| `blocking` | 失败→返回 error | 失败→返回 error | 调用方应让业务事务失败 |
| `best_effort` | 失败→warning | 失败→warning | 调用方不阻塞主业务 |

核心字段定义为：`item_type, item_id, action_type, operator_id, source, occurred_at`。

**注册机制**：

每个 PolicyKey 在 `init()` 中注册一次，未注册 key 在写入入口直接拒绝：

```go
// service/activity/policy.go — 初始注册表（示例）

const (
    PolicyChartUpdate      ActivityPolicyKey = "chart.update"
    PolicyChartDelete      ActivityPolicyKey = "chart.delete"
    PolicyDashboardUpdate  ActivityPolicyKey = "dashboard.update"
    PolicyMetricUpdate     ActivityPolicyKey = "metric.update"
    PolicyAbStatusOnline   ActivityPolicyKey = "ab.status_online"
    PolicyAbRelease        ActivityPolicyKey = "ab.release"
    PolicyActivityBackfill ActivityPolicyKey = "activity.backfill"

    // Global item
    PolicyOrgMemberCreate  ActivityPolicyKey = "org_member.create"
    PolicyAccountAPITokenUpdate ActivityPolicyKey = "account_api_token.update"
)

func init() {
    RegisterPolicy(PolicyChartUpdate,      WritePolicyBlocking)
    RegisterPolicy(PolicyChartDelete,      WritePolicyBlocking)
    RegisterPolicy(PolicyDashboardUpdate,  WritePolicyBlocking)
    RegisterPolicy(PolicyMetricUpdate,     WritePolicyBlocking)
    RegisterPolicy(PolicyAbStatusOnline,   WritePolicyBlocking)
    RegisterPolicy(PolicyAbRelease,        WritePolicyBlocking)
    RegisterPolicy(PolicyActivityBackfill, WritePolicyBestEffort)

    RegisterPolicy(PolicyOrgMemberCreate,  WritePolicyBlocking)
    RegisterPolicy(PolicyAccountAPITokenUpdate, WritePolicyBlocking)
}
```

维护方式：
- 每个接入点定义自己的 `ActivityPolicyKey` 常量；策略值由业务 owner 在接入 PR 中声明
- ActivityService 只审核命名、返回语义和测试覆盖，不替业务决策策略强度
- 未注册 key 在写入入口直接拒绝，异常时由 write-only feature flag 兜底关闭写入
- 策略变更走代码评审，不允许调用方运行时随意切换

**注册保障**：`WriteLog` 入口拒绝未注册 key 是最后防线，但防护太晚。真正的保障是 init-time test——用反射遍历所有 `ActivityPolicyKey` 常量，断言每个都调了 `RegisterPolicy`。业务方加新 key 忘注册 → 测试直接失败。

**V1 初始示例**：

| PolicyKey | 策略 | 原因 |
|-----------|------|------|
| `ab.release` / `chart.delete` | `blocking` | 关键操作缺活动直接阻断排障链路 |
| `chart.update` / `dashboard.update` / `metric.update` | `blocking` | 日常编辑缺 changes 排障同样抓瞎 |
| `activity.backfill` | `best_effort` | 历史回填不应阻断迁移任务 |

删除、权限、成员管理等场景不预设强弱，接入时由对应业务 owner 明确。

### 4.5 批量写入原子性

- 同一业务事务内的多条活动记录，通过 `BatchWriteLog` 一次性写入
- `blocking` 下：写入失败 → 整体返回 error；是否回滚业务事务由调用方事务边界决定
- `best_effort` 下：失败折入 warning，不影响业务事务
- 同批活动记录自动共享 `correlation_id`
- 单次 `BatchInsert` 不超过 **500 行**，超出由调用方自行分批


> 业务方负责读旧值 → 投影为原始 map；ActivityService 统一完成 ChangesBetween → ApplyMaskRules → 组装 Detail。

### 4.6 旧值从哪来

**大部分 Update 方法本来就有一条"读旧值"的代码**——用于权限校验、乐观锁、或业务判断。

```go
func (s *MetricService) UpdateMetric(ctx, id, req) error {
    metric := s.dao.Get(id)             // ← 本来就有的读，不是为了 activity log
    if metric.OrgID != userOrg(ctx) {   //    权限校验，复用这个读取
        return ErrForbidden
    }
    // ... 业务变更 ...
}
```

Activity log 复用的就是这次读，不是额外多查一次。关键理解：**不是"为了写日志加读"，而是"本来就读了，顺手投影"。**

**如果方法本来没有读（直接调 dao.Update 的薄方法）：**

```go
func (s *MetricService) PauseSchedule(ctx, id uint64) error {
    return s.dao.UpdateField(id, "schedule_status", "paused")
}
```

这种情况需要增加一次"预热读"——在执行变更之前读一次当前值。不改 DAO、不改事务边界，只在方法体开头加一行 `s.dao.Get(id)`。

**改动评估：**

| 方法类型 | 改动量 | 具体 |
|---------|--------|------|
| 已有 `dao.Get` 的 Update | +3 行 | 在 Get 后加投影；在 Update 后加新值投影 + 写入 |
| 没有 `dao.Get` 的 Update | +4 行 | 预热读 + 投影 + 写入 |
| Create | +3 行 | Create 返回 model 后，投影 + 写入（无旧值） |
| Delete | +2 行 | Delete 前投影 + 写入（无需新值） |

接入成本在每点 3-10 行代码，不改 DAO 层、不改 DB schema、不改已有事务边界。


## 5. Detail

### 5.1 Envelope 结构

```json
// Update 场景：changes
{
  "changes": [{"field": "name", "action": "changed", "before": "旧名称", "after": "新名称"}]
}

// Create 场景：changes（初始字段清单）
{
  "changes": [
    {"field": "name", "action": "created", "after": "DAU趋势"},
    {"field": "type", "action": "created", "after": "line"}
  ]
}

// Delete 场景：snapshot（调用方按需传入）
{
  "snapshot": {"name": "DAU趋势", "type": "line", "version": 3, "dashboard_ids": [5, 8]}
}
```

两容器的语义定位和约束见 [§5.9](#59-detail-envelope-两容器语义)。简短总结：

- `changes` — 字段级 before/after diff，Update 的主路径，Create 的初始字段清单
- `snapshot` — 调用方按需传入的原始实体 dump（`json.Marshal(entity)`），与投影无关，系统不解构

**Change 结构**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `field` | string | 是 | 变更的字段名（稳定字段名，非 GORM 字段名） |
| `action` | string | 是 | `created` / `changed` / `deleted` |
| `before` | any | 否 | 旧值（首次创建时为 null） |
| `after` | any | 否 | 新值（被删除时为 null） |

### 5.2 场景示例

> 完整场景 JSON 示例见 [reference-activity-log.md](./reference-activity-log.md)。changes/snapshot 产出规则统一在 [§5.9](#59-detail-envelope-两容器语义)。

### 5.3 排除字段体系

三层排除（完整说明和代码落位见 [§5.6](#56-三层排除的落位)）：通用排除在 `ChangesBetween` 内部硬编码 → 按类型排除靠投影函数自然排除 → 变更级别排除在调用时通过 `SkipNoiseFields` 传入。

### 5.4 敏感字段处理

V1 不做复杂 redaction registry。原则是：敏感字段默认不进入 `Detail`；确实需要展示线索时，由业务 service 写入已脱敏值，ActivityService 只做最后的安全兜底。

| 字段 | 处理 |
|------|------|
| 邮箱 | 复用 `ulog.MaskEmail()`，保留首字符 + `***` |
| 密码/密钥/token | 不读 DB 明文，`before`/`after` 统一写 `"masked"` |
| Account API Token 原文 | 永不进入活动 detail、日志或持久化 payload |
| `token_hash` | 不进入 `Detail` |
| `token_hint` | 可作为 snapshot 线索，但不得扩大长度或反推原 token |
| Integration 敏感配置 | V1 默认不接入；后续接入前先定义字段 allowlist |
| Webhook/endpoint 含凭据 | 默认不进入 `Detail`；如必须进入，先在业务代码中剔除凭据片段 |
| OAuth client credentials | 不进入 `Detail` |
| `last_used_at` | 默认不进入 update diff，避免使用行为噪音污染管理操作历史 |

活动模块提供 `MaskedValue = "***"` 常量作为自定义 `MaskFunc` 返回值。V1 不尝试读取或恢复实际敏感值。

---


### 5.5 投影函数定义在哪

每个接入点在自己的 service 包下定义 activity 投影函数，与业务代码同包：

```
metric/
├── metric_service.go       ← 业务方法
├── activity_proj.go        ← toActivityProj
chart/
├── chart_service.go
├── activity_proj.go        ← toActivityProj
```

**投影 = 天然的排除声明。** 不写进 activityProj 的字段，ChangesBetween 就不会记录。

```go
// 例：metric/activity_proj.go
func toActivityProj(m *Metric) map[string]any {
    return map[string]any{
        "name":      m.Name,
        "define":    m.Define,
        "precision": m.Precision,
        // ↓ 以下字段没有写进 activityProj，自然排除
        // last_calculated_at, run_at, created_at, updated_at, version
    }
}
```

### 5.6 三层排除的落位

| 层级 | 范围 | 实现位置 | 维护者 |
|------|------|---------|--------|
| 通用排除 | 全类型 | `ChangesBetween` 内部固定 | activity 模块 |
| 按类型排除 | 按 ItemType | 投影函数——不投影 = 排除 | 各业务 service |
| 变更级别排除 | 特定字段 | 调用时传入 `SkipNoiseFields` | 各业务 service |

**通用排除字段写在 ChangesBetween 内部：**

```go
// service/activity/changes_between.go
var commonExcludedKeys = map[string]bool{
    "id": true, "created_at": true, "updated_at": true,
    "created_by": true, "last_modified_by": true,
    "version": true, "lock_version": true,
}

func ChangesBetween(old, new_ map[string]any) ([]Change, error) {
    // 收集所有 key（去重），跳过 commonExcludedKeys 中的字段
    keys := map[string]bool{}
    for k := range old { if !commonExcludedKeys[k] { keys[k] = true } }
    for k := range new_ { if !commonExcludedKeys[k] { keys[k] = true } }
    // 对每个 key 比较 old/new 值，生成 Change 列表
    // ...
}
```

**为什么通用排除放在 ChangesBetween 而不是投影函数里：**
- 通用排除是安全兜底——即使投影函数不小心把 `version` 写进了 map，ChangesBetween 也会忽略
- 一处修改全局生效，不需要在每个投影函数里重复

### 5.7 敏感字段在哪脱敏

**两层防护：**

**第一层（主要防线）——业务方声明 `MaskRules`：**

投影函数**保留原始值**（不脱敏）：

```go
// apitoken/activity_proj.go（示意）
func toActivityProj(t *AccountAPIToken) map[string]any {
    return map[string]any{
        "name":       t.Name,
        "role":       t.Role,
        "token_hash": t.TokenHash,  // ← 原始值！不脱敏。准确 diff 的前提
        "hint":       t.Hint,
    }
}
```

调用时声明脱敏规则，由 ActivityService 统一执行：

```go
// 调用处：CreateToken / UpdateToken
input := WriteInput{
    OldProjection: oldProj,
    NewProjection: newProj,
    MaskRules: activity.MaskRules{
        "token_hash": activity.MaskValue("***"),  // → 所有值替换为 "***"
    },
    // ...
}
```

内置脱敏函数见 §4.2。业务方也可自定义：

```go
MaskRules: activity.MaskRules{
    "email": func(raw any) any {
        s := raw.(string)
        if len(s) == 0 { return s }
        return string(s[0]) + "***@" + strings.SplitN(s, "@", 2)[1]
    },
}
```

**为什么不在投影中脱敏：** 投影时把 `token_hash` 写成 `"***"`，ChangesBetween 看到 old 和 new 都是 `"***"` → 认为没变 → 丢失"token_hash 被改了"这个事件。投影保留原始值 → ChangesBetween 检测到变更 → ApplyMaskRules 抹值，三件事分离，既保留变更事件又防止敏感值泄露。

**第二层（兜底防线）——ActivityService 字段名拦截：**

```go
// service/activity/sensitive.go
// 即使业务方忘了声明 MaskRules，ActivityService 也拒绝以下字段名进入 detail
var sensitiveFieldNames = map[string]bool{
    "password": true, "secret": true, "token": true,
    "token_hash": true, "encrypted": true, "credential": true,
    "private_key": true, "api_key": true,
}
```

第二层在写入流程（§4.3 第 ④ 步）执行：如果投影 map 的 key 命中了此列表，拒绝写入并返回 error。这是最后一道防线，不应依赖。

### 5.8 代码模板

> Create / Update / Delete 三种场景的完整 Go 代码模板见 [reference-activity-log.md](./reference-activity-log.md)。

### 5.9 Detail Envelope 两容器语义

`detail` 的 JSON envelope 固定为两个顶层容器，定位明确：

| 容器 | 定位 | 谁构造 | 使用场景 | 稳定性契约 |
| --- | --- | --- | --- | --- |
| `changes[]` | **字段级 before/after diff** | `ChangesBetween` 自动生成 | Create/Update 场景 | keys = 投影字段名（稳定）；before/after = 当时值（自包含，不依赖未来 schema） |
| `snapshot` | **原始实体 dump（可选）** | 调用方按需传入 | Delete/任何需要记录完整状态时 | 系统不解构，调用方自行维护格式兼容 |

**核心规则：**

1. **Changes 是系统拥有的结构化字段，snapshot 是调用方拥有的不透明字段。** ActivityService 负责解析 changes；snapshot 只存储不解读，查询时原样返回。

2. **两者不互斥。** 可以同时存在（如 Update + snapshot）：`{"changes": [...], "snapshot": {...}}`。Create 只产生 changes；Delete 由调用方决定是否传 snapshot。

3. **Snapshot 不走投影。** 调用方直接 `json.Marshal(entity)` 传入原始实体全量，不需要写投影函数。调用方自行决定 snapshot 内容（完整 entity、关键字段子集、或自定义结构）。

4. **64KB 预算共享（V1 默认 64KB，可配置）。** `changes[]` + `snapshot` 合并受 64KB 约束。

### 5.10 配置归属总结

| 配置 | 定义位置 | 维护者 | 说明 |
|------|---------|--------|------|
| 通用排除字段 | `service/activity/changes_between.go` | activity 模块 | `ChangesBetween` 内部固定，全类型有效 |
| 投影函数（按类型排除） | `service/<type>/activity_proj.go` | 各业务 service | 不投影 = 自然排除 |
| `MaskRules` 声明 | 各业务 service 调用处 | 各业务 service | 选内置函数或自定义；按字段名匹配投影 key |
| 敏感字段兜底名单 | `service/activity/sensitive.go` | activity 模块 | 写入前拒绝未脱敏字段名 |
| WritePolicy | `service/activity/policy.go` | 业务 owner + activity 审核 | `init()` 中注册 |
| ItemType 白名单 | `service/activity/registry.go` | 接入时加一行 + review | 声明 type → 所属 schema（meta/global） |

**新增一个接入点的最小 checklist：**

```
 1. 在 service/<type>/activity_proj.go 写投影函数（返回原始值，不脱敏）
 2. 在 service/activity/policy.go 注册 PolicyKey + WritePolicy
 3. 在 service/activity/registry.go 加一行 ItemType 白名单（如未加过）
 4. 在业务方法中插入投影 + 调用 Write（+3~4 行，按需声明 MaskRules）
```

### 5.11 权责边界

| 模块 | 负责 | 不负责 |
|------|------|--------|
| 业务 service | 读 old/new 快照 → 投影为原始 `map[string]any` → 声明 `MaskRules` → 按需传入 `Snapshot` → 调用 `WriteLog` → 按 Policy 处理结果 | 调 `ChangesBetween`、调 `ApplyMaskRules`、拼 `detail_payload` |
| ActivityService | `ChangesBetween` → `ApplyMaskRules` → 组装 Detail（含 input.Snapshot 非空时追加）→ 序列化 → INSERT → 按 PolicyKey 返回 error/warning | 理解各业务 struct 含义 |
| `ChangesBetween` | 比较两投影 map、生成 `[]Change`、跳过通用排除字段 | 持有业务事务、读业务表、理解业务 struct |

---


## 6. 查询接口

### 6.1 项目 item 查询

**Request**：
```json
{
  "item_type":   "chart",
  "item_id":     123,
  "action_type": ["update"],      // 可选，筛选 action_type
  "page":        1,
  "page_size":   20
}
```

**Response**：
```json
{
  "total": 2,
  "items": [
    {
      "id": 9001,
      "item_type": "chart",
      "item_id": 123,
      "item_name": "DAU 趋势",
      "action_type": "update",
      "operator_id": 7,
      "operator_name": "alice",
      "source": "ui",
      "detail": {
        "changes": [{"field": "name", "action": "changed", "before": "DAU", "after": "DAU 趋势"}]
      },
      "occurred_at": "2026-06-29T10:00:00+08:00"
    }
  ]
}
```

V1 不承诺：`operator_id` 维度筛选、跨项目检索、全文检索 detail。

查询权限以 OP / 内部排障权限为准，响应中 `detail` 必须保持掩码后的读视图。

### 6.2 Global item 查询

支持按 org / project / account 维度查询（V1 不承诺 operator_id 筛选）：

```json
{
  "org_id": 1,
  "item_type": "org_member",
  "page": 1,
  "page_size": 20
}
```

**查询权限**：
- OP / 内部排障链路可按 org / project / account 查询
- 组织管理员可查看所属 org/project 的 global item 活动
- 账号本人可查看自己的 Account API Token 活动

保留 `total` 的原因：OP / 内部排障需要先判断历史规模再决定是否继续翻页。当前查询是单对象或单维度时间序列，count 成本可控，不为了 V1 先改成 cursor-only。

### 6.3 查询处理流程

```mermaid
sequenceDiagram
    participant C as OP / 内部接口调用方
    participant Q as ActivityQueryService
    participant P as Permission Resolver
    participant D as Activity DAO
    participant S as Detail Codec

    C->>Q: item_type + item_id/scope + page
    Q->>P: 校验是否可查看该对象活动
    P-->>Q: allow / deny
    Q->>D: count + page query
    D-->>Q: rows(detail_payload)
    Q->>S: ParseDetail(detail_payload)
    S-->>Q: *Detail
    Q-->>C: total + items(detail)
```

查询约束：

- 查询接口只面向 OP / 内部排障或等价受控入口，V1 不提供官方产品通用活动页。
- `detail_payload` parse 失败不能把原始 TEXT 直接暴露给前端；返回降级 detail、记录 error metric，并保留 row 的 who / what / when。
- `total` 与分页列表使用同一过滤条件；排序固定为 `(occurred_at DESC, id DESC)`，避免同一毫秒内顺序不稳定。
- 权限校验可以依赖业务对象或 scope resolver，但活动表读路径不应该反向依赖 detail TEXT。

---


## 7. 项目内对象活动场景目录

> 完整场景目录（CHART / DASHBOARD / COHORT / AB / METRIC / PIPELINE / CAMPAIGN 等 8 个对象的 CRUD 和状态变更场景）见 [reference-activity-log.md](./reference-activity-log.md)。

## 8. Global Item 活动场景目录

> 完整 Global item 场景（组织/项目生命周期、成员管理、Account API Token）见 [reference-activity-log.md](./reference-activity-log.md)。

## 9. 账号活跃字段

3 个 `TIMESTAMPTZ NULL` 列加在 `global.account` 表，不入 `activity_log`：

```sql
ALTER TABLE global.account
  ADD COLUMN last_login_at  TIMESTAMPTZ DEFAULT NULL,
  ADD COLUMN last_logout_at TIMESTAMPTZ DEFAULT NULL,
  ADD COLUMN last_active_at TIMESTAMPTZ DEFAULT NULL;
```

| 字段 | 写入时机 | 写入方式 |
|------|---------|---------|
| `last_login_at` | 登录成功（密码 + OAuth） | controller 在 `generateToken` 后调用 `accountDao.UpdateFields` |
| `last_logout_at` | 登出成功 | controller 在 `session.Delete` 后调用 `UpdateFields` |
| `last_active_at` | 会话活跃（每个认证请求） | 中间件 `SessionMiddleware` — `AuthenticateSession` 成功后 Redis SetNX 15 分钟节流 |

`last_active_at` 节流策略：
- 每个认证请求 → Redis `SetNX("last_active_throttle:{aid}", 15min)`
  - SetNX 成功 → DB 写一次
  - SetNX 失败 → 跳过，零 DB 开销
- Redis 不可用 → 跳过本次刷新 + warning 日志 + 指标上报，**不降级为每次请求写 DB**

3 个字段均为 NULLABLE，老账号迁移后全部为 NULL。

账号活跃数据流：

```mermaid
flowchart LR
    login["登录成功"] --> login_db["更新 last_login_at"]
    logout["登出成功"] --> logout_db["更新 last_logout_at"]
    request["认证请求"] --> auth["AuthenticateSession 成功"]
    auth --> redis["Redis SetNX 15min"]
    redis -->|成功| active_db["更新 last_active_at"]
    redis -->|key 已存在| skip["跳过 DB 写入"]
    redis -->|Redis 故障| warn["warning + metric<br/>不写 DB"]
```

这里不写 `activity_log`，因为登录/活跃是账号状态字段，不是某个 item 的业务变更。安全审查若需要登录历史，应另立登录事件或安全日志，不把 `last_active_at` 复用成审计事件流。

---

## 10. 历史迁移

> 完整迁移方案（迁移原则、映射规则、AB/Metric 历史提取脚本、幂等控制、验证 SQL）见 [reference-activity-log.md](./reference-activity-log.md)。

### 10.1 迁移原则

1. 一次性复制旧历史
2. 迁移后查询只读新活动表
3. 升级后新写入只写新活动表
4. 旧字段或旧表保留，不做双写

### 10.2 迁移源

| 历史源 | 迁移到新项目活动 | 原因 |
|--------|----------------|------|
| `ab_feature_flag.details.operation_records` | **是** | 现存存储最差，JSONB 内嵌数组无法独立查询 |
| `meta.metric_define_history` | **否** | 现存表已独立可查，迁移 fidelity 未必更高，收益低 |
| `ma_operation_log` | **否** | 现存表已是规范日志，新操作已走 activity_log |
| `meta.asset_behavior` | **否** | 只有 VIEW 有效，不具备可靠活动语义 |
| `global.op_operation_log` | **否** | 作用域不同，继续留在 OP 操作记录链路 |

### 10.3 映射规则

| 源操作 | → action_type |
|--------|-------------|
| AB `CREATE` | `create` |
| AB `UPDATE`, `VARIANT_CHANGE` | `update`（配置变更） |
| AB `DEBUG` | `debug` |
| AB `ONLINE` | `online` |
| AB `OFFLINE` | `offline` |
| AB `RELEASE` | `release` |
| AB `COPY` | `copy` |
| AB `DELETE` | `delete` |

对历史 AB 记录缺少 before/after 的场景：
- 允许 `changes` 为空
- 必须保留 `name`、`source = "backfill"`、`snapshot.legacy_source`
- `operator_name` 尽量回填，查不到允许空字符串
- `occurred_at` 直接回填原始操作时间

**幂等去重键**：`legacy_source` + `item_type` + `item_id` + `legacy_action_type` + `operator_id` + `occurred_at`

### 10.4 Chart/Dashboard/Cohort/Event/Property

该类对象**当前没有可靠旧操作历史源**，不从 `asset_behavior` 或访问记录伪造历史。上线后从新表开始连续记账。

### 10.5 迁移数据流

```mermaid
flowchart LR
    legacy["旧历史源<br/>AB operation_records"] --> scan["批量扫描"]
    scan --> map["映射 ItemType / ActionType / Detail"]
    map --> dedupe["生成幂等去重键"]
    dedupe --> insert["BatchWriteLog<br/>source=backfill"]
    insert --> table["meta.activity_log"]
    table --> verify["抽样查询 + 数量校验"]
```

迁移闭环要求：

- 回填必须走 activity 模块的 serializer，不允许脚本直接拼 `detail_payload` SQL。
- `occurred_at` 使用旧历史事件时间，`created_at` 使用回填入库时间。
- 每批回填记录 `legacy_source`，便于问题排查和回滚识别。
- 迁移完成后，AB 的历史查询入口只读新活动表；旧字段保留但不再作为在线查询源。
- 验证至少包含总量对账、重复执行幂等、抽样 detail 可读、按对象查询能串起旧历史四项。

### 10.6 具体迁移脚本示例

#### AB operation_records 提取

AB 历史存储在 `ab_feature_flag.details` 的 JSONB `operation_records` 数组中。每条记录的原始结构：

```json
{
  "action": "UPDATE",
  "name": "someone@example.com",
  "timestamp": 1680000000,
  "old_value": "...",
  "new_value": "..."
}
```

**SQL 提取**（分批扫描，避免全表锁）：

```sql
-- 每批 500 条
SELECT
    f.id              AS feature_flag_id,
    f.name            AS flag_name,
    f.typ             AS flag_type,       -- 1=gate, 2=config, 3=exp
    jsonb_array_elements(f.details->'operation_records') AS record
FROM ab_feature_flag f
WHERE f.id > :last_id
  AND f.details->'operation_records' IS NOT NULL
  AND jsonb_array_length(f.details->'operation_records') > 0
ORDER BY f.id
LIMIT 500;
```

**Go 映射伪代码**：

```go
type ABOperationRecord struct {
    Action    string `json:"action"`
    Name      string `json:"name"`      // operator_name
    Timestamp int64  `json:"timestamp"` // occurred_at (unix)
    OldValue  string `json:"old_value"`
    NewValue  string `json:"new_value"`
}

func mapABRecordToActivity(flagID int64, flagName, flagType string, rec ABOperationRecord) (*activity.WriteInput, error) {
    actionType := mapABAction(rec.Action)

    var changes []activity.Change
    if rec.OldValue != "" || rec.NewValue != "" {
        changes, _ = activity.ChangesBetween(
            map[string]any{"value": rec.OldValue},
            map[string]any{"value": rec.NewValue},
        )
    }

    return &activity.WriteInput{
        ItemType:         itemTypeForABFlag(flagType),
        ItemID:           flagID,
        ItemName:         flagName,
        ActionType:       actionType,
        PolicyKey:        string(activity.PolicyActivityBackfill),
        PreBuiltChanges:  changes,                        // 迁移走快捷路径
        Snapshot:          buildLegacySnapshot(rec),
        OccurredAt:       time.Unix(rec.Timestamp, 0),
    }, nil
}

func buildLegacySnapshot(rec ABOperationRecord) *json.RawMessage {
    data, _ := json.Marshal(map[string]any{
        "legacy_source":      "ab_feature_flag.details.operation_records",
        "legacy_action_type": rec.Action,
    })
    return (*json.RawMessage)(&data)
}

func mapABAction(action string) string {
    switch action {
    case "CREATE":         return "create"
    case "UPDATE", "VARIANT_CHANGE": return "update"  // 配置变更
    case "DEBUG":          return "debug"             // 状态流转→独立 action_type
    case "ONLINE":         return "online"
    case "OFFLINE":        return "offline"
    case "RELEASE":        return "release"
    case "COPY":           return "copy"
    case "DELETE":         return "delete"
    default:               return "update" // fallback
    }
}

func itemTypeForABFlag(flagType int) string {
    switch flagType {
    case 1:   return "feature_gate"
    case 2:   return "feature_config"
    case 3:   return "experiment"
    default:  return "experiment"
    }
}
```

#### 批量回填与幂等控制

```go
func BackfillABHistory(ctx context.Context, batchSize int) (int, error) {
    var lastID int64
    total := 0
    for {
        rows, err := dao.ScanABOperationRecords(ctx, lastID, batchSize)
        if err != nil || len(rows) == 0 {
            break
        }
        lastID = rows[len(rows)-1].FeatureFlagID

        var inputs []activity.WriteInput
        for _, row := range rows {
            input := mapABRecordToActivity(row.FlagID, row.FlagName, row.FlagType, row.Record)
            inputs = append(inputs, *input)
        }

        // 使用 BatchWrite 走统一 SerializeDetail → DAO insert
        // 幂等性由 DB 层 unique index 或写入前按去重键去重保障
        err = activitySvc.BatchWriteLog(ctx, inputs)
        if err != nil {
            return total, fmt.Errorf("batch %d: %w", lastID, err)
        }
        total += len(rows)
    }
    return total, nil
}
```

#### 迁移验证

```sql
-- 总量对账
SELECT COUNT(*) FROM meta.activity_log WHERE source = 'backfill' AND detail_payload LIKE '%"legacy_source":"ab_feature_flag%';

-- 按对象抽样
SELECT * FROM meta.activity_log
WHERE item_type = 'FEATURE_GATE' AND item_id = 15
ORDER BY occurred_at DESC;

-- 幂等校验（重复执行去重键不应该增加记录）
SELECT item_type, item_id, action_type, operator_id, occurred_at, COUNT(*)
FROM meta.activity_log
WHERE source = 'backfill'
GROUP BY item_type, item_id, action_type, operator_id, occurred_at
HAVING COUNT(*) > 1;
```

#### 关闭旧查询入口

迁移完成后，在所有历史查询入口处切换数据源：

```diff
- list := dao.GetABOperationRecords(ctx, flagID)
+ list := activitySvc.ListByQuery(ctx, activity.Query{
+     ItemType: itemTypeForABFlag(flag.Typ),
+     ItemID:   flag.ID,
+ })
```

---

## 11. 接入策略

> 完整接入 SOP、主要接入点清单、开发者体验（文档、测试工具、Feature Flag）见 [reference-activity-log.md](./reference-activity-log.md)。

### 11.1 接入 SOP

项目对象接入必须按以下步骤，不能只在某个 service 里临时拼一条 JSON：

| 步骤 | 要做什么 | 产物 |
|------|----------|------|
| 1 | 确认对象身份 | `ItemType`、`item_id`、`item_name` 规则 |
| 2 | 确认动作语义 | 基础 `action_type`；必要时走扩展 action_type 注册流程 |
| 3 | 注册接入场景 | `PolicyKey` |
| 4 | 构造 Detail | 手写 `*Detail` 或使用 `ChangesBetween` 生成 `changes` |
| 5 | 构造 Detail（投影 + 计算 Change 列表） | 固定 `ItemType/ActionType/PolicyKey`，调用方只传业务快照 |
| 6 | 接业务事务 | create 用创建后对象；update/delete 在修改前读旧快照；批量用 `BatchWrite*` |
| 7 | 补验证 | 成功写入、无变更不写、失败策略、查询返回 detail/total |

最小代码形态：

```go
// 项目 item — 传原始投影，ActivityService 做 diff
err = activitySvc.WriteLog(ctx, activity.WriteInput{
    ItemType:      activity.ItemTypeChart,
    ItemID:        chart.ID,
    ItemName:      chart.Name,
    ActionType:    activity.ActionTypeUpdate,
    PolicyKey:     string(activity.PolicyChartUpdate),
    OldProjection: oldProj,                            // 原始值，不脱敏
    NewProjection: newProj,                            // 原始值，不脱敏
})

// Global item — 手写 changes（无投影函数，少量字段）
err = activitySvc.WriteLog(ctx, activity.WriteInput{
    ItemType:   activity.ItemTypeOrgMember,
    ItemID:     accountID,
    ItemName:   member.DisplayName,
    ActionType: activity.ActionTypeCreate,
    PolicyKey:  string(activity.PolicyOrgMemberCreate),
    PreBuiltChanges: []activity.Change{                // 手写 changes
        {Field: "level", Action: activity.ChangeCreated, After: "member"},
        {Field: "role_ids", Action: activity.ChangeCreated, After: []int64{1, 2}},
    },
})

// Global item — 有敏感字段，声明 MaskRules
err = activitySvc.WriteLog(ctx, activity.WriteInput{
    ItemType:      activity.ItemTypeAccountAPIToken,
    ItemID:        token.ID,
    ItemName:      token.Label,
    ActionType:    activity.ActionTypeUpdate,
    PolicyKey:     string(activity.PolicyAccountAPITokenUpdate),
    OldProjection: oldProj,
    NewProjection: newProj,
    MaskRules:     activity.MaskRules{
        "token_hash": activity.MaskValue("***"),
    },
})
```

### 11.2 主要接入点

| 对象域 | 主要接入方法 |
|--------|-------------|
| Chart | Create / Update / Delete / BatchDelete / Copy |
| Dashboard | Create / Update / PatchMeta / SetLayouts / Delete / BatchDelete / Copy / AddCharts / RemoveCharts |
| Cohort | Create / Update / Delete |
| AB | Create / Update / StatusRelease / StatusOnline / Stop / Copy |
| Metric | Create / Update / Delete |
| Event / Property | Create / Update / Delete |
| Pipeline | Create / Update / Delete / Stop |
| Campaign (MA) | Create / Update / Delete / Launch / Pause / Resume / Finish |
| Org member | Upsert / BatchUpdateLevel / BatchReplaceSupervisor / BatchDelete |
| Project member | BatchUpsert / BatchUpdateRoles / BatchDelete |
| Org lifecycle | Init / Archive / 配置变更 |
| Project lifecycle | Create / 配置变更 / Archive |
| Account API Token | Create / Update / Enable / Disable / Refresh / Delete |

### 11.3 不依赖的基础设施

以下基础设施不能作为活动覆盖的判断标准：

| 基础设施 | 问题 |
|----------|------|
| `AssetOperator` | 只注册了 Chart / Dashboard，接口仅含 CRUD；覆盖不到 Cohort / AB / copy / status_change |
| `asset_behavior` | 主要是 view 行为，modify/delete/add 基本死代码，不是可靠活动系统 |
| AB 自带历史页 | 只能看 AB，且数据嵌在 JSONB 里 |
| Metric history 表 | 只覆盖 define 变更 |
| Pipeline `exec_info` | 仅系统执行日志，不记录谁做了 CRUD |

### 11.4 开发者体验

V1 需提供：

| 文档 | 内容 | 形式 |
|------|------|------|
| ActivityService 接入指南 | 调用契约、必填字段、detail 构建规范、常见错误 | Markdown |
| 对象类型接入模板 | 每个 ItemType 的写入调用示例 | Go 示例代码 |
| WritePolicy 选择指南 | blocking/best_effort 适用场景、决策树 | Markdown |
| Detail helper 使用说明 | `ChangesBetween` 调用方式、稳定投影约定、敏感字段禁入规则 | Markdown + 注释 |

测试辅助工具：

| 工具 | 说明 |
|------|------|
| `activitytest.MockService` | 内存实现 ActivityService，不依赖 DB |
| `activitytest.AssertLogWritten` | 断言某条活动记录已被写入 |
| `activitytest.AssertChangesContains` | 断言 detail.changes 包含特定字段变更 |

MockService 行为约定：
- `WriteLog` 默认返回 nil，调用方可配置预期 error（测试写入失败）
- `BatchWriteLog` 默认追加到内存切片，调用方可读取断言
- 不提供与真实 DAO 一致的 mock（那是集成测试的职责）

### 11.5 Write-only Feature Flag

部署时增加 write-only feature flag 保护，异常时可快速关闭活动写入而不影响业务逻辑。

---

## 12. 交付阶段

### Phase 0：基础设施

- 建 `meta.activity_log` 表
- 建 `activity` 域类型、DAO、公共服务、TEXT serializer、轻量 Detail helper
- 建 `PolicyKey` 简单映射和 write-only feature flag
- 建 OP / 内部查询 API
- DAO BatchInsert 上限 500 行
- 打通最小闭环：写入一条项目 item 活动后，可通过查询 API 读回 `total/items/detail`

### Phase 1：高价值对象 + 历史迁移

- 接入 Chart / Dashboard / Cohort
- 接入 Experiment / Feature Gate / Feature Config
- 接入 Metric
- 完成 AB / Metric 历史导入
- 打通对象维度查询闭环（最小可用版本）

### Phase 2：Metadata 长尾对象

- 接入 TRACKED_EVENT / VIRTUAL_EVENT / EVENT_PROPERTY / USER_PROPERTY / VIRTUAL_PROPERTY

### 附加链路

- Global item 活动（与 Phase 1 独立交付）：组织/项目生命周期、成员管理、Account API Token
- 账号活跃字段（独立交付，小而简单）：`last_login_at` / `last_logout_at` / `last_active_at`

不允许把所有 phase 绑成一次总开关上线。

---

## 13. 验证方案

### 13.1 单测

- Detail helper：create/update/delete/copy 四类 envelope、稳定投影比较、敏感字段禁入
- detail 序列化：JSON envelope、超限截断、降级 warning
- 历史迁移映射：AB 操作类型映射、Metric define 映射、空 operator_name 兜底

### 13.2 集成测试

- 核心对象 Chart / Dashboard / Cohort / AB / Metric 的成功路径写活动
- 最小闭环 E2E：执行一次对象 update → 写入活动 → `ListByQuery` 返回同一条记录、`total=1`、detail 可解析
- Phase 2 元数据对象 create/update/delete 写活动
- Global item 活动（组织、项目、成员、token）各操作路径写活动
- 账号活跃字段三个时间戳刷新验证
- `last_active_at` Redis 节流验证（15 分钟内只写一次 DB）
- OP / 内部查询接口分页、排序、过滤；project scope 鉴权、掩码字段读取
- 迁移脚本重复执行的幂等性

### 13.3 故障注入

- 活动 DAO insert 失败（验证 blocking → 返回 error，best_effort → warning）
- detail 序列化失败（验证 blocking → 返回 error，best_effort → warning）
- detail 超大 → 截断 + 字段列表记录在日志中
- operator 信息缺失 → 兜底处理
- 迁移批次重复执行 → 幂等键去重
- OP 查询权限不足 → 拒绝访问
- Redis 不可用 → `last_active_at` 跳过，不倒灌 DB

### 13.4 尚未锁定、需对象 owner 确认的边界

1. Chart `config` 的稳定投影边界
2. Dashboard `layout_overrides` 是否全量记录还是仅记录受影响 chart
3. AB `details` 需要暴露到什么摘要层级

---

## 14. 后续扩展与讨论入口

以下内容不进入 V1 主实现，只保留讨论入口：

- TEXT `detail_payload` 是否启用压缩：见 [discussion.md](./discussion.md) D-P4
- Global 聚合查询是否引入 `activity_log_target`：见 [discussion.md](./discussion.md) D-P5
- 分区、TTL、保留策略、审批/告警/可见性控制：后续按真实查询量和客户需求再开独立方案
