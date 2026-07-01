# 技术方案：Global Item 活动（global.activity_log）

> 本方案描述 web global schema 下的 item activity 表。它不是 org 专用日志，也不是 OP operation log 的替代品；V1 首批 item_type 聚焦组织、项目、成员、权限同步、账号 API Token，后续可扩展到 integration、billing config 等 global 级对象。

## 1. 落点

两条独立活动链路，按操作主体和存储边界分界：

| 链路 | 存储 | 覆盖范围 |
|------|------|---------|
| **OP 操作活动** | `global.op_operation_log`（不变） | OP 人员对 org/project 的配置操作（update_org_config、配额、客户管理等） |
| **Global item 活动** | `global.activity_log`（新表） | web global schema 内的 item 活动；V1 首批为组织创建/归档、项目创建/删除、成员管理、权限同步、账号 API Token |

分界原则：OP 人员在 OP 后台的操作走 `op_operation_log`；客户（含组织管理员）在业务系统中的操作走 `activity_log`。OP 未来通过内部接口在同一页面展示两类日志。

字段集 = `meta.activity_log` + `org_id` + `project_id` + `account_id`。global item 活动与项目 item 活动共享基础事件模型：稳定 `action_type`（V1 默认 create/update/delete/copy），领域语义通过 `item_type` 和 `detail` 表达；detail 使用同一套 envelope 与 TEXT codec。

scope 设计原则：

- org-scoped item 填 `org_id`，例如 `ORGANIZATION` / `ORG_MEMBER`
- project-scoped item 同时填 `org_id` 与 `project_id`，例如 `PROJECT` / `PROJECT_MEMBER`
- account-scoped item 填 `account_id`，例如 `ACCOUNT_API_TOKEN`
- 账号 API Token 没有天然 `org_id`，因此 `org_id` 不能是所有 global activity 的必填字段

邀请流程：邀请建在自有表上。接受邀请 → 触发生效操作 → 写入 `activity_log`。邀请本身（创建/发送/撤回）不落活动。

## 2. 新表：`global.activity_log`

```sql
CREATE TABLE IF NOT EXISTS activity_log (
    id              BIGSERIAL PRIMARY KEY,
    org_id          BIGINT       DEFAULT NULL,       -- org-scoped item 填写
    project_id      BIGINT       DEFAULT NULL,       -- 非 project-scoped item 不填
    account_id      BIGINT       DEFAULT NULL,       -- account-scoped item 填写，如 Account API Token
    item_type       VARCHAR(64)  NOT NULL,           -- 'ORG_MEMBER' / 'ORGANIZATION' / 'PROJECT' / ...
    item_id         BIGINT       NOT NULL,           -- member account_id / org_id / project_id / token id / global item id
    item_name       VARCHAR(255) NOT NULL DEFAULT '',-- 展示快照
    action_type     VARCHAR(32)  NOT NULL,           -- 基础动作枚举，V1 主要使用 create / update / delete
    operator_id     BIGINT       NOT NULL,
    operator_name   VARCHAR(255) NOT NULL DEFAULT '',
    source          VARCHAR(32)  NOT NULL DEFAULT 'web',
    correlation_id  VARCHAR(64)  NOT NULL DEFAULT '',
    detail_payload  TEXT         NOT NULL DEFAULT '{}',
    occurred_at     TIMESTAMPTZ  NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_activity_org
    ON activity_log(org_id, item_type, occurred_at DESC, id DESC)
    WHERE org_id IS NOT NULL;
CREATE INDEX idx_activity_project
    ON activity_log(project_id, item_type, occurred_at DESC, id DESC)
    WHERE project_id IS NOT NULL;
CREATE INDEX idx_activity_account
    ON activity_log(account_id, item_type, occurred_at DESC, id DESC)
    WHERE account_id IS NOT NULL;
CREATE INDEX idx_activity_operator
    ON activity_log(operator_id, occurred_at DESC, id DESC);
```

- `action_type` 复用共享基础枚举；global item 细语义通过 `item_type` + `detail` 表达，不新增 `action_name`
- `idx_activity_project` 支撑"查某个 project 的成员变更"场景
- `idx_activity_account` 支撑"查某个账号的 API Token 变更"场景
- `idx_activity_operator` 支撑内部排障时按操作人反查 global item 活动
- scope 合法性由 `ActivityService` 按 item registry 校验；DB 不用复杂 CHECK 承载业务枚举规则，避免后续扩展频繁改 DDL

## 3. 能力矩阵

### 3.1 V1 item_type

| item_type | action_type | V1 | 说明 |
|------------|-------------|:--:|------|
| `ORGANIZATION` | `create` | ✓ | 组织初始化 |
| | `update` | ✓ | 组织信息修改、配置变更 |
| | `delete` | ✓ | 组织归档 |
| `ORG_MEMBER` | `create` | ✓ | 添加成员 |
| | `update` | ✓ | 变更成员级别 / 替换主管 |
| | `delete` | ✓ | 移除成员 |
| `PROJECT` | `create` | ✓ | 创建项目 |
| | `update` | ✓ | 项目信息修改、配置变更 |
| | `delete` | ✓ | 删除项目 |
| `PROJECT_MEMBER` | `create` | ✓ | 将成员加入项目 |
| | `update` | ✓ | 变更成员在项目中的角色 |
| | `delete` | ✓ | 将成员移出项目 |
| `ACCOUNT_API_TOKEN` | `create` | ✓ | 创建账号 API Token / Account API Key |
| | `update` | ✓ | 更新 label / scope / expires_at，或 enable / disable 状态变更 |
| | `delete` | ✓ | 软删除 token |

后续可扩展的 global item_type 示例：`ORG_INVITE`、`ORG_ROLE`、`INTEGRATION`、`BILLING_CONFIG`。这些不是 V1 范围，但它们应复用同一张 `global.activity_log`，不要另建“org log”或“integration log”孤岛。

**V1 不做项说明**：

- 邀请创建/发送/撤回 — 邀请建在自有表上，接受邀请后触发生效操作走 activity_log
- 预设角色变更 — 极低频，后续如接入则注册 `ORG_ROLE`
- 组织级联操作子项（Archive 内部删 member/project/RBAC） — 顶层 `delete ORGANIZATION` 已记录

### 3.2 新 global item 接入流程

每个 global item 接入必须先回答 5 个问题，再写代码：

| 问题 | 规则 |
|------|------|
| 这个 item 属于哪个 scope？ | org-scoped 填 `org_id`；project-scoped 填 `org_id + project_id`；account-scoped 填 `account_id` |
| `item_type` 是什么？ | 用业务对象名，不用动作名；如 `PROJECT_MEMBER`，不是 `ADD_PROJECT_MEMBER` |
| `item_id` 指向谁？ | 成员类用成员 `account_id`，组织类用 `org_id`，项目类用 `project_id`，账号 token 类用 token id；复合身份放到 `detail.extra` |
| 是否需要批量写？ | 批量成员变更用 `BatchWriteGlobalItemLog`，同批共享 `correlation_id`；不引入业务维护的 `operation_group_id` |
| 策略是什么？ | 用 `PolicyKey` 注册，不在调用处传 `required_core/best_effort` |

实现步骤：

1. 在 `activity/types.go` 注册 `ItemType`，若基础动作不够再按 action_type 注册流程评审。
2. 在 `activity/registry.go` 注册字段投影、脱敏和 item_name 提取规则。
3. 在 `activity/policy.go` 注册 `PolicyKey`，例如 `global.project_member.create`。
4. 在对应领域 service 中新增包装函数，固定 `OrgID/ProjectID/ItemType/ActionType/PolicyKey`。
5. 在业务事务成功路径调用包装函数；update/delete 前必须读旧快照或最小展示快照。
6. 补单测和集成测试：成功写入、无变化不写、批量写、失败策略、查询返回 total。

### 3.3 V1 接入点

#### 组织成员（4 项）

item_type = `"ORG_MEMBER"`，item_id = `account_id`，project_id = NULL。

| action_type | 接入位置 | 触发条件 | item_name | detail |
|------------|---------|---------|------------|--------|
| `create` | `organization/member.go` `Upsert` | 成员不存在 | `display_name` | `{"level":"<level>","role_ids":[<ids>]}` |
| `update` | `organization/member.go` `BatchUpdateLevel` | level 实际变更 | `display_name` | `{"old_level":"<old>","new_level":"<new>"}` |
| `update` | `organization/member.go` `BatchReplaceSupervisor` | supervisor 集合变更 | `display_name` | `{"change_kind":"supervisor_added"}` 或 `{"change_kind":"supervisor_removed"}` |
| `delete` | `organization/member.go` `DeleteByOrgAndAccounts` | 成员被软删除 | `display_name` | `{}` |

**Upsert 的 create vs update 判定**：先读已有成员状态。不存在 → `create`；已存在且 level/roles 变更 → `update`；已存在且未变更 → 不记。

**级联项目成员删除**：`DeleteByOrgAndAccounts` 级联删除该成员在所有项目下的 membership。只记一次 `action_type=delete, item_type=ORG_MEMBER`，项目级删除不重复记。

#### 项目成员（3 项）

item_type = `"PROJECT_MEMBER"`，item_id = `account_id`，project_id = 项目 ID。

| action_type | 接入位置 | 触发条件 | item_name | detail |
|------------|---------|---------|------------|--------|
| `create` | `project/member.go` `BatchUpsert` | 成员在项目中不存在 | `display_name` | `{"roles":[<ids>]}` |
| `update` | `project/member.go` `BatchUpdateRoles` | 角色实际变更 | `display_name` | `{"old_roles":[<ids>],"new_roles":[<ids>]}` |
| `delete` | `project/member.go` `BatchDeleteByProjectAndAccounts` | 成员被移出项目 | `display_name` | `{}` |

**org member 和 project member 是否重复？** 不重复。一个用户可以是 org member（能看到 org 下的项目列表）但没有某个项目的成员资格（不能访问该项目数据）。`create PROJECT_MEMBER` 是独立的权限授予操作，排障价值等同于 org member。两者的操作链不同："Alice 什么时候被加到 org 的"和"Alice 什么时候被加到 project X 的"是两个不同的问题。

**`UpdateAccountProjectAuths`**（全量同步用户的跨项目授权）：视作一条 global item activity 记录。内部批处理逻辑不细拆到单行写入，而是记录一次 `action_type=update, item_type=ORG_MEMBER`，detail 包含变更摘要（涉及项目数、成员数）。

#### 组织/项目生命周期（6 项）

| action_type | item_type | item_id | project_id | 接入位置 | item_name | detail |
|------------|------------|-----------|----------|---------|------------|--------|
| `create` | `ORGANIZATION` | org_id | NULL | `organization/organization.go` `Init` | org name | `{"creator_id":<id>}` |
| `update` | `ORGANIZATION` | org_id | NULL | 组织信息/配置变更入口 | org name | 按 diff 记录变更字段 |
| `delete` | `ORGANIZATION` | org_id | NULL | `organization/organization.go` `Archive` | org name | `{"status_before":"active","status_after":"archived"}` |
| `create` | `PROJECT` | project_id | project_id | `project/create.go` `Create` | project name | `{"org_id":<id>}` |
| `update` | `PROJECT` | project_id | project_id | 项目信息/配置变更入口 | project name | 按 diff 记录变更字段 |
| `delete` | `PROJECT` | project_id | project_id | `project/delete.go` `Archive` | project name | `{"org_id":<id>}` |

#### 账号 API Token（5 项）

item_type = `"ACCOUNT_API_TOKEN"`，item_id = token id，account_id = token owner account id，org_id/project_id = NULL。

Account API Token 是 web global schema 内的账号级 item，不属于某个组织或项目。当前代码位置：

- DAO：`apps/web/dao/global/account_api_token.go`
- Service：`apps/web/service/account/apitoken/service.go`
- Controller：`apps/web/controller/account/account_api_token.go`

| action_type | 接入位置 | 触发条件 | item_name | detail |
|------------|---------|---------|------------|--------|
| `create` | `CreateTokenWithExpiry` / `CreateTokenNoQuotaWithExpiry` | token 创建成功 | token label | `changes` 记录 `label/status/scopes/expires_at` 初始值；`extra.token_hint` 可记录 |
| `update` | `UpdateTokenWithExpiry` | label / scopes / expires_at 实际变更 | token label | `changes` 记录可展示字段 before/after |
| `update` | `EnableToken` / `DisableToken` / `DisableByRawToken` | status 实际变更 | token label | `changes: [{"field":"status","before":"ACTIVE","after":"DISABLED"}]` |
| `update` | `RefreshToken` | 新 token 创建并禁用旧 token | new token label | `extra.refresh_from_token_id` + 旧 token status change；同事务内两条记录共享 `correlation_id` |
| `delete` | `DeleteToken` | token 被软删除 | token label | `snapshot` 记录 `status/scopes/expires_at/token_hint` 的安全快照 |

敏感字段规则：

| 字段 | 处理 |
|------|------|
| `token` 原文 | 永不进入活动 detail，也不进入日志 |
| `token_hash` | `drop`，不进入投影 |
| `token_hint` | 可记录，用于排障识别是哪一个 token，但不得扩大长度或反推出原 token |
| `scopes` | 解析为稳定结构，只记录 feature/resource 范围摘要 |
| `last_used_at` | 默认不进入 update diff，避免使用行为噪音污染管理操作历史 |

`RefreshToken` 会在一个事务中创建新 token 并禁用旧 token。建议记录两条活动：

1. `create ACCOUNT_API_TOKEN`：item_id = new token id，detail.extra.refresh_from_token_id = old token id
2. `update ACCOUNT_API_TOKEN`：item_id = old token id，changes 记录 status 从 `ACTIVE` 到 `DISABLED`

两条记录由 ActivityService 自动生成或继承同一个 `correlation_id`，不引入业务维护的 `operation_group_id`。

## 4. 操作人解析

| 场景 | operator_id / operator_name 来源 |
|------|-------------------------------|
| Web 用户操作 | `pvctx.Aid(ctx)` / `pvctx.Aname(ctx)` |
| 注册时自动创建组织 | 注册用户本人 |
| Account API Token 管理 | token owner；系统初始化 token 继承 `accountID` |
| 系统同步 / 回填 | 优先继承触发任务的账号；查不到则按约定写默认值 |

`operator_name` 是写入时的展示快照，不随用户后续改名回写。

## 5. 错误处理与事务

global item 活动不再要求调用方在早期泛接口和 fallback 接口之间二选一。统一调用 `ActivityService.WriteGlobalItemLog/BatchWriteGlobalItemLog`，由活动模块内部策略决定：

- `required_full`：失败回滚业务事务
- `required_core`：主活动行失败回滚，detail 可降级
- `best_effort`：失败只记日志 + 指标

global item 活动默认推荐 `required_core` 或 `best_effort`，具体由中心化 policy registry 按 `PolicyKey` 配置，而不是在业务代码里随意选择。

**批量操作约束**：单次 `BatchInsert` 不超过 500 行（见 DAO 节约束），避免大事务锁表。

**detail_payload** 存储为 `TEXT`。大小约束与 `meta.activity_log` 对齐：通过字段投影与大小预算控制单条 detail；超限时优先截断明确的大字段并记录 warning。TEXT 内是否启用应用层压缩跟项目 item 活动保持同一 codec 决策。查询接口统一返回解析后的 `detail`，不暴露存储层字段名。

## 6. 查询接口

新增 OP / 内部查询端点，按 org / project / account 维度拉取 global item 活动记录，供内部排障使用。查询模型优先保持简单分页，返回 `total`。

保留 `total` 的原因：

- global item 活动的使用者同样是内部排障和审查同学，需要先判断历史规模
- 当前查询量级主要是 org / member / account token 维度，count 成本可控
- 对外不暴露 cursor-only 契约，降低 OP 端接入复杂度

**Request**：
```json
{
  "org_id": 1,
  "project_id": null,
  "account_id": null,
  "item_type": "ORG_MEMBER",
  "operator_id": 7,
  "page": 1,
  "page_size": 20
}
```

**Response**：
```json
{
  "total": 5,
  "items": [
    {
      "id": 1001,
      "org_id": 1,
      "project_id": null,
      "account_id": null,
      "item_type": "ORG_MEMBER",
      "item_id": 42,
      "item_name": "Alice",
      "action_type": "create",
      "operator_id": 7,
      "operator_name": "Bob",
      "source": "web",
      "detail": {"extra": {"level": "member", "role_ids": [1, 2]}},
      "occurred_at": "2026-07-01T10:00:00Z"
    }
  ]
}
```

账号 API Token 查询示例：

```json
{
  "account_id": 42,
  "item_type": "ACCOUNT_API_TOKEN",
  "page": 1,
  "page_size": 20
}
```

查询权限：

- OP / 内部排障链路可按 org / project / account 查询
- 组织管理员可查看所属 org/project 的 global item 活动日志
- 账号本人可查看自己的 Account API Token 活动；普通成员不可查看他人账号 token 活动

## 7. DAO

新增 `apps/web/dao/activity/global.go`（~70 行），复用 `globaldb.TableDao` 模式：

```go
func (d *GlobalItemActivityDao) Insert(ctx context.Context, log *GlobalItemActivity) error
func (d *GlobalItemActivityDao) BatchInsert(ctx context.Context, logs []*GlobalItemActivity) error
func (d *GlobalItemActivityDao) ListByOrg(ctx context.Context, orgID int64, itemType string, page, pageSize int) ([]*GlobalItemActivity, int64, error)
func (d *GlobalItemActivityDao) ListByProject(ctx context.Context, projectID int64, itemType string, page, pageSize int) ([]*GlobalItemActivity, int64, error)
func (d *GlobalItemActivityDao) ListByAccount(ctx context.Context, accountID int64, itemType string, page, pageSize int) ([]*GlobalItemActivity, int64, error)
func (d *GlobalItemActivityDao) ListByItem(ctx context.Context, itemType string, itemID int64, page, pageSize int) ([]*GlobalItemActivity, int64, error)
func (d *GlobalItemActivityDao) ListByOperator(ctx context.Context, operatorID int64, page, pageSize int) ([]*GlobalItemActivity, int64, error)
```

`BatchInsert` 用于批量操作（`BatchUpdateLevel` / `BatchReplaceSupervisor` / `DeleteByOrgAndAccounts` 同时涉及多个成员），单次 INSERT 多行，上限 500 行。超出由调用方自行分批。

`ListByProject` 利用 `idx_activity_project` 索引，支撑项目维度活动查询。`ListByAccount` 利用 `idx_activity_account` 索引，支撑账号级 item（如 Account API Token）活动查询。

## 8. Migration

Migration 即 Section 2 的 DDL 脚本，无历史数据迁移——老 member / token 操作无完整活动记录，新表从上线时刻开始连续记账。

## 9. 写入示例

```go
// 单人操作（Upsert create）
svc := activitysvc.GetActivityService()
svc.WriteGlobalItemLog(ctx, activity.GlobalItemWriteInput{
    OrgID:      orgID,
    ItemType:   activity.ItemTypeOrgMember,
    ItemID:     accountID,
    ItemName:   member.DisplayName,
    ActionType: activity.ActionTypeCreate,
    PolicyKey:  activity.PolicyGlobalOrgMemberCreate,
    Source:     activity.SourceWeb,
    OperatorID: pvctx.Aid(ctx),
    OperatorName: pvctx.Aname(ctx),
    Detail: activity.Detail{
        Extra: map[string]interface{}{
            "level":    "member",
            "role_ids": []int{1, 2},
        },
    },
})

// 批量操作（BatchUpdateLevel）
svc := activitysvc.GetActivityService()
for _, m := range updatedMembers {
    svc.WriteGlobalItemLog(ctx, activity.GlobalItemWriteInput{
        OrgID:      orgID,
        ItemType:   activity.ItemTypeOrgMember,
        ItemID:     m.AccountID,
        ItemName:   m.DisplayName,
        ActionType: activity.ActionTypeUpdate,
        PolicyKey:  activity.PolicyGlobalOrgMemberUpdateLevel,
        Source:     activity.SourceWeb,
        OperatorID: pvctx.Aid(ctx),
        OperatorName: pvctx.Aname(ctx),
        Detail: activity.Detail{
            Changes: []activity.Change{
                {Field: "level", Action: "changed", Before: m.OldLevel, After: m.NewLevel},
            },
        },
    })
}
```

```go
// 账号级 global item（Account API Token update）
svc := activitysvc.GetActivityService()
svc.WriteGlobalItemLog(ctx, activity.GlobalItemWriteInput{
    AccountID:  accountID,
    ItemType:   activity.ItemTypeAccountAPIToken,
    ItemID:     token.ID,
    ItemName:   token.Label,
    ActionType: activity.ActionTypeUpdate,
    PolicyKey:  activity.PolicyAccountAPITokenUpdate,
    Source:     activity.SourceWeb,
    OperatorID: pvctx.Aid(ctx),
    OperatorName: pvctx.Aname(ctx),
    OldValue:   oldToken,
    NewValue:   token,
})
```

## 10. 边缘场景

| 场景 | 处理 |
|------|------|
| `Upsert` 到已存在且 level/roles 未变 | 不记活动（无实际变更） |
| `BatchReplaceSupervisor` 前后 supervisor 集合相同 | 不记活动（无变更，前提是接入侧做了 diff） |
| `BatchReplaceSupervisor` 加了两名、删了一名 | 3 条记录：都为 `update ORG_MEMBER`，detail 分别标记 `change_kind=supervisor_added` / `supervisor_removed` |
| `DeleteByOrgAndAccounts` 传入已删除成员 | 不记活动（`is_deleted = true` 已过滤，不会触发） |
| `DeleteByOrgAndAccounts` 传入不存在的 account_id | 不上报错，DAO 层 `is_deleted = false` 条件自动跳过 |
| 同一事务内批量操作部分成功 | 行为由中心化 `WritePolicy` 决定；`required_core/full` 回滚，`best_effort` 记录 warning |
| 批量操作中某行 detail 序列化失败 | `required_full` 回滚；`required_core` 可降级 detail；`best_effort` 记录 warning |
| `BatchInsert` 超过 500 行 | 调用方必须自行分批，DAO 不接受超过 500 行的参数 |
| 并发操作同一成员 | 各自事务各自写，两条活动记录都落地，时间戳区分 |
| 注册时自动创建组织然后 add member | 两条：`create ORGANIZATION` + `create ORG_MEMBER`（注册用户既是 creator 也是 member） |
| `Archive` 删 org（级联删 member/project/RBAC） | 一条 `delete ORGANIZATION`。内部级联删除不单独记（否则 N 条记录，且与 活动粒度原则冲突） |
| Account API Token 更新时只有 `last_used_at` 变化 | 不记活动，避免请求使用行为污染管理操作历史 |
| Account API Token refresh | 记录新 token create + 旧 token status update，两条共享 `correlation_id` |
| 系统初始化 `InitSystemToken` 清空旧 token 并创建新 token | 可作为 `source=internal` 的 account-scoped 活动；不记录 token 原文和 token_hash |
| token label 属于历史兼容值（如 MCP API Key） | item_type 仍为 `ACCOUNT_API_TOKEN`，label 只作为展示快照，不影响动作语义 |

## 11. 可观测性

| 指标 | 说明 |
|------|------|
| `global_activity_write_total{item_type, action_type, result}` | 写入计数 |
| `global_activity_write_latency_ms` | 写入延迟 |
| `global_activity_batch_size` | 批量写入条数分布 |

结构化日志：写入失败时打 `ulog.CErrorf`，含 `org_id` / `project_id` / `account_id`、`item_type`、`action_type`、`error`。

## 12. 部署

1. migration 先跑 → `activity_log` 表就位
2. 部署新代码（DAO + global item 接入点 + policy registry），活动写入统一走 `ActivityService.WriteGlobalItemLog/BatchWriteGlobalItemLog`
3. OP 查询接口上线
4. 接入点代码使用 write-only feature flag 保护，异常时可快速关闭活动写入而不影响业务逻辑

## 13. 验证

- 添加成员 → `activity_log` 出现 `action_type=create, item_type=ORG_MEMBER`
- 变更角色 → `action_type=update`，detail 含 old/new level
- 替换主管 → 每个变更的主管各一条 `update`
- 移除成员 → `action_type=delete`
- 创建/归档组织 → `create`、`delete`
- 创建/更新/删除项目 → `create`、`update`、`delete`
- 组织信息/配置修改 → `update`
- 创建/更新/禁用/启用/刷新/删除 Account API Token → `ACCOUNT_API_TOKEN` 活动落地，且不包含 raw token / token_hash
- 批量操作 → `BatchInsert` 多行落地，不超过 500 行
- DB insert 失败 → 行为符合对应 `WritePolicy`
- `ListByOrg` / `ListByProject` / `ListByAccount` / `ListByItem` / `ListByOperator` 分页和 `total` 正确
- OP 查询权限：非管理员被拒绝
