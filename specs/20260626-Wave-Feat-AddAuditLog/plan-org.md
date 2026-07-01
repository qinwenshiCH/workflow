# 技术方案：组织 / 项目管理活动

## 1. 落点

两条独立活动链路，按操作主体分界：

| 链路 | 存储 | 覆盖范围 |
|------|------|---------|
| **OP 操作活动** | `global.op_operation_log`（不变） | OP 人员对 org/project 的配置操作（update_org_config、配额、客户管理等） |
| **客户侧管理活动** | `global.activity_log`（新表） | 客户侧操作：组织创建/归档、项目创建/删除、成员管理、权限同步 |

分界原则：OP 人员在 OP 后台的操作走 `op_operation_log`；客户（含组织管理员）在业务系统中的操作走 `activity_log`。OP 未来通过内部接口在同一页面展示两类日志。

字段集 = `meta.activity_log` + `org_id` + `project_id`。管理活动与项目对象活动共享基础事件模型：稳定 `action_type`（create/update/delete/copy），可选 `action_name` 表达领域语义；detail 使用同一套 JSON envelope。

邀请流程：邀请建在自有表上。接受邀请 → 触发生效操作 → 写入 `activity_log`。邀请本身（创建/发送/撤回）不落活动。

## 2. 新表：`activity_log`

```sql
CREATE TABLE IF NOT EXISTS activity_log (
    id              BIGSERIAL PRIMARY KEY,
    org_id          BIGINT       NOT NULL,
    project_id      BIGINT       DEFAULT NULL,       -- org 级操作不填
    item_type     VARCHAR(64)  NOT NULL,            -- 'ORG_MEMBER' / 'ORGANIZATION' / 'PROJECT'/ ...
    item_id       BIGINT       NOT NULL,            -- account_id / org_id / project_id
    item_name     VARCHAR(255) NOT NULL DEFAULT '', -- 展示快照
    action_type     VARCHAR(32)  NOT NULL,            -- create / update / delete
    action_name     VARCHAR(64)  NOT NULL DEFAULT '',
    operator_kind   VARCHAR(32)  NOT NULL DEFAULT 'account',
    operator_id     BIGINT       DEFAULT NULL,
    operator_name   VARCHAR(255) NOT NULL DEFAULT '',
    source          VARCHAR(32)  NOT NULL DEFAULT 'web',
    correlation_id  VARCHAR(64)  NOT NULL DEFAULT '',
    detail_version  SMALLINT     NOT NULL DEFAULT 1,
    detail_payload  JSONB        NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     TIMESTAMPTZ  NOT NULL,
    recorded_at     TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_activity_org
    ON activity_log(org_id, item_type, occurred_at DESC, id DESC);
CREATE INDEX idx_activity_project
    ON activity_log(project_id, item_type, occurred_at DESC, id DESC);
CREATE INDEX idx_activity_item
    ON activity_log(item_type, item_id, occurred_at DESC, id DESC);
CREATE INDEX idx_activity_operator
    ON activity_log(operator_id, occurred_at DESC, id DESC);
```

- `action_type` 复用共享基础枚举；管理域细语义放在 `action_name`
- `detail_version` 与 `meta.activity_log` 复用同一版本号空间，V1 固定为 1
- `idx_activity_project` 支撑"查某个 project 的成员变更"场景
- `idx_activity_operator` 支撑"某操作人最近对组织做了什么"的排障路径

## 3. 能力矩阵

### 3.1 全量 item_type（含 V1 / 后续）

| item_type | action_type / action_name | V1 | 说明 |
|------------|-------------|:--:|------|
| `ORGANIZATION` | `create` / `create_org` | ✓ | 组织初始化 |
| | `update` / `archive_org` | ✓ | 组织归档（status→archived） |
| | — | — | config 变更走 `op_operation_log`，不进此表 |
| `ORG_MEMBER` | `create` / `add_org_member` | ✓ | 添加成员 |
| | `update` / `update_org_member_level` 或 `replace_org_supervisor` | ✓ | 变更成员级别 / 替换主管 |
| | `delete` / `remove_org_member` | ✓ | 移除成员 |
| `PROJECT` | `create` / `create_project` | ✓ | 创建项目 |
| | `delete` / `delete_project` | ✓ | 删除项目 |
| | — | — | config 变更走 `op_operation_log`，不进此表 |
| `PROJECT_MEMBER` | `create` / `add_project_member` | ✓ | 将成员加入项目 |
| | `update` / `update_project_member_roles` | ✓ | 变更成员在项目中的角色 |
| | `delete` / `remove_project_member` | ✓ | 将成员移出项目 |
| `ORG_INVITE` | — | V2 | V1 不做：成员加入已有 `create ORG_MEMBER` |
| `ORG_ROLE` | — | V2 | V1 不做：预设角色变更极低频 |

**V1 不做项说明**：

- 邀请创建/发送/撤回 — 邀请建在自有表上，接受邀请后触发生效操作走 activity_log
- org/project 重命名 — 排障价值低
- 预设角色变更 — 极低频
- 组织级联操作子项（Archive 内部删 member/project/RBAC） — 顶层 `update ORGANIZATION` 已记录

### 3.2 接入点

#### 组织成员（4 项）

item_type = `"ORG_MEMBER"`，item_id = `account_id`，project_id = NULL。

| action_type / action_name | 接入位置 | 触发条件 | item_name | detail |
|------------|---------|---------|------------|--------|
| `create` / `add_org_member` | `organization/member.go` `Upsert` | 成员不存在 | `display_name` | `{"level":"<level>","role_ids":[<ids>]}` |
| `update` / `update_org_member_level` | `organization/member.go` `BatchUpdateLevel` | level 实际变更 | `display_name` | `{"old_level":"<old>","new_level":"<new>"}` |
| `update` / `replace_org_supervisor` | `organization/member.go` `BatchReplaceSupervisor` | supervisor 集合变更 | `display_name` | `{"role":"supervisor_added"}` 或 `"supervisor_removed"` |
| `delete` / `remove_org_member` | `organization/member.go` `DeleteByOrgAndAccounts` | 成员被软删除 | `display_name` | `{}` |

**Upsert 的 create vs update 判定**：先读已有成员状态。不存在 → `create`；已存在且 level/roles 变更 → `update`；已存在且未变更 → 不记。

**级联项目成员删除**：`DeleteByOrgAndAccounts` 级联删除该成员在所有项目下的 membership。只记一次 `action_type=delete, item_type=ORG_MEMBER`，项目级删除不重复记。

#### 项目成员（3 项）

item_type = `"PROJECT_MEMBER"`，item_id = `account_id`，project_id = 项目 ID。

| action_type / action_name | 接入位置 | 触发条件 | item_name | detail |
|------------|---------|---------|------------|--------|
| `create` / `add_project_member` | `project/member.go` `BatchUpsert` | 成员在项目中不存在 | `display_name` | `{"roles":[<ids>]}` |
| `update` / `update_project_member_roles` | `project/member.go` `BatchUpdateRoles` | 角色实际变更 | `display_name` | `{"old_roles":[<ids>],"new_roles":[<ids>]}` |
| `delete` / `remove_project_member` | `project/member.go` `BatchDeleteByProjectAndAccounts` | 成员被移出项目 | `display_name` | `{}` |

**org member 和 project member 是否重复？** 不重复。一个用户可以是 org member（能看到 org 下的项目列表）但没有某个项目的成员资格（不能访问该项目数据）。`create PROJECT_MEMBER` 是独立的权限授予操作，排障价值等同于 org member。两者的操作链不同："Alice 什么时候被加到 org 的"和"Alice 什么时候被加到 project X 的"是两个不同的问题。

**`UpdateAccountProjectAuths`**（全量同步用户的跨项目授权）：视作一条 management activity log 记录。内部批处理逻辑不细拆到单行写入，而是记录一次 `action_type=update, action_name=sync_project_auths, item_type=ORG_MEMBER`，detail 包含变更摘要（涉及项目数、成员数）。

#### 组织/项目生命周期（4 项）

| action_type / action_name | item_type | item_id | project_id | 接入位置 | item_name | detail |
|------------|------------|-----------|----------|---------|------------|--------|
| `create` / `create_org` | `ORGANIZATION` | org_id | NULL | `organization/organization.go` `Init` | org name | `{"creator_id":<id>}` |
| `update` / `archive_org` | `ORGANIZATION` | org_id | NULL | `organization/organization.go` `Archive` | org name | `{}` |
| `create` / `create_project` | `PROJECT` | project_id | project_id | `project/create.go` `Create` | project name | `{"org_id":<id>}` |
| `delete` / `delete_project` | `PROJECT` | project_id | project_id | `project/delete.go` `Archive` | project name | `{"org_id":<id>}` |

## 4. 操作人解析

| 场景 | operator_kind | operator_id / operator_name 来源 |
|------|---------------|-------------------------------|
| Web 用户操作 | `account` | `pvctx.Aid(ctx)` / `pvctx.Aname(ctx)` |
| 注册时自动创建组织 | `account` | 注册用户本人 |
| 系统同步 / 回填 | `system` / `backfill` | 可为空，或继承触发任务的账号 |

`operator_name` 是写入时的展示快照，不随用户后续改名回写。

## 5. 错误处理与事务

管理活动不再要求调用方二选一 `Log` / `LogWithFallback`。统一调用 `ActivityService.Log/BatchLog`，由活动模块内部策略决定：

- `required_full`：失败回滚业务事务
- `required_core`：主活动行失败回滚，detail 可降级
- `best_effort`：失败只记日志 + 指标

管理活动默认推荐 `required_core` 或 `best_effort`，具体由中心化 policy registry 按场景配置，而不是在业务代码里随意选择。

**批量操作约束**：单次 `BatchInsert` 不超过 500 行（见 DAO 节约束），避免大事务锁表。

**detail_payload** 存储为 JSONB。大小约束与 `meta.activity_log` 对齐：通过字段投影与大小预算控制单条 detail；超限时优先截断明确的大字段并记录 warning。V1 不默认引入应用层压缩。

## 6. 查询接口

新增 OP 查询端点，按 org 维度拉取该客户侧的所有管理活动记录，供 OP 内部排障使用。查询模型优先 cursor，而不是 `page + total`。

**Request**：
```json
{
  "org_id": 1,
  "item_type": "ORG_MEMBER",
  "operator_id": 7,
  "cursor": "",
  "limit": 20
}
```

**Response**：
```json
{
  "items": [
    {
      "id": 1001,
      "org_id": 1,
      "item_type": "ORG_MEMBER",
      "item_id": 42,
      "item_name": "Alice",
      "action_type": "create",
      "action_name": "add_org_member",
      "operator_kind": "account",
      "operator_id": 7,
      "operator_name": "Bob",
      "source": "web",
      "detail_payload": {"level": "member", "role_ids": [1, 2]},
      "detail_version": 1,
      "occurred_at": "2026-07-01T10:00:00Z"
    }
  ],
  "next_cursor": "opaque-cursor"
}
```

查询权限：OP / 组织管理员可查看所属 org 的管理活动日志。普通成员不可查看。

## 7. DAO

新增 `apps/web/dao/activity/mgmt.go`（~70 行），复用 `globaldb.TableDao` 模式：

```go
func (d *ManagementActivityDao) Insert(ctx context.Context, log *ManagementActivity) error
func (d *ManagementActivityDao) BatchInsert(ctx context.Context, logs []*ManagementActivity) error
func (d *ManagementActivityDao) ListByOrg(ctx context.Context, orgID int64, itemType string, cursor string, limit int) ([]*ManagementActivity, string, error)
func (d *ManagementActivityDao) ListByProject(ctx context.Context, projectID int64, itemType string, cursor string, limit int) ([]*ManagementActivity, string, error)
func (d *ManagementActivityDao) ListByItem(ctx context.Context, itemType string, itemID int64, cursor string, limit int) ([]*ManagementActivity, string, error)
func (d *ManagementActivityDao) ListByOperator(ctx context.Context, operatorID int64, cursor string, limit int) ([]*ManagementActivity, string, error)
```

`BatchInsert` 用于批量操作（`BatchUpdateLevel` / `BatchReplaceSupervisor` / `DeleteByOrgAndAccounts` 同时涉及多个成员），单次 INSERT 多行，上限 500 行。超出由调用方自行分批。

`ListByProject` 利用 `idx_activity_project` 索引，支撑项目维度活动查询。

## 8. Migration

Migration 即 Section 2 的 DDL 脚本，无历史数据迁移——老 member 操作无活动记录，新表从上线时刻开始连续记账。

## 9. 写入示例

```go
// 单人操作（Upsert create）
svc := activitysvc.GetActivityService()
svc.Log(ctx, ManagementWriteInput{
    OrgID:      orgID,
    ItemType: "ORG_MEMBER",
    ObjectID:   accountID,
    ObjectName: member.DisplayName,
    ActionType: "create",
    ActionName: "add_org_member",
    Source:     "web",
    OperatorKind: "account",
    OperatorID: pvctx.Aid(ctx),
    OperatorName: pvctx.Aname(ctx),
    Detail: activity.Detail{
        Version: 1,
        Extra: map[string]interface{}{
            "level":    "member",
            "role_ids": []int{1, 2},
        },
    },
})

// 批量操作（BatchUpdateLevel）
svc := activitysvc.GetActivityService()
for _, m := range updatedMembers {
    svc.Log(ctx, ManagementWriteInput{
        OrgID:      orgID,
        ItemType: "ORG_MEMBER",
        ObjectID:   m.AccountID,
        ObjectName: m.DisplayName,
        ActionType: "update",
        ActionName: "update_org_member_level",
        Source:     "web",
        OperatorKind: "account",
        OperatorID: pvctx.Aid(ctx),
        OperatorName: pvctx.Aname(ctx),
        Detail: activity.Detail{
            Version: 1,
            Changes: []activity.Change{
                {Field: "level", Action: "changed", Before: m.OldLevel, After: m.NewLevel},
            },
        },
    })
}
```

## 10. 边缘场景

| 场景 | 处理 |
|------|------|
| `Upsert` 到已存在且 level/roles 未变 | 不记活动（无实际变更） |
| `BatchReplaceSupervisor` 前后 supervisor 集合相同 | 不记活动（无变更，前提是接入侧做了 diff） |
| `BatchReplaceSupervisor` 加了两名、删了一名 | 3 条记录：2 条 `create` + 1 条 `delete` |
| `DeleteByOrgAndAccounts` 传入已删除成员 | 不记活动（`is_deleted = true` 已过滤，不会触发） |
| `DeleteByOrgAndAccounts` 传入不存在的 account_id | 不上报错，DAO 层 `is_deleted = false` 条件自动跳过 |
| 同一事务内批量操作部分成功 | 行为由中心化 `WritePolicy` 决定；`required_core/full` 回滚，`best_effort` 记录 warning |
| 批量操作中某行 detail 序列化失败 | `required_full` 回滚；`required_core` 可降级 detail；`best_effort` 记录 warning |
| `BatchInsert` 超过 500 行 | 调用方必须自行分批，DAO 不接受超过 500 行的参数 |
| 并发操作同一成员 | 各自事务各自写，两条活动记录都落地，时间戳区分 |
| 注册时自动创建组织然后 add member | 两条：`create ORGANIZATION` + `create ORG_MEMBER`（注册用户既是 creator 也是 member） |
| `Archive` 删 org（级联删 member/project/RBAC） | 一条 `update ORGANIZATION`。内部级联删除不单独记（否则 N 条记录，且与 活动粒度原则冲突） |

## 11. 可观测性

| 指标 | 说明 |
|------|------|
| `mgmt_activity_write_total{item_type, action_type, result}` | 写入计数 |
| `mgmt_activity_write_latency_ms` | 写入延迟 |
| `mgmt_activity_batch_size` | 批量写入条数分布 |

结构化日志：写入失败时打 `ulog.CErrorf`，含 `org_id`、`item_type`、`action_type`、`error`。

## 12. 部署

1. migration 先跑 → `activity_log` 表就位
2. 部署新代码（DAO + 8 个接入点 + policy registry），活动写入统一走 `ActivityService.Log/BatchLog`
3. OP 查询接口上线
4. 接入点代码使用 write-only feature flag 保护，异常时可快速关闭活动写入而不影响业务逻辑

## 13. 验证

- 添加成员 → `activity_log` 出现 `action_type=create, action_name=add_org_member, item_type=ORG_MEMBER`
- 变更角色 → `action_type=update, action_name=update_org_member_level`，detail 含 old/new level
- 替换主管 → 每个变更的主管各一条，`action_name=replace_org_supervisor`
- 移除成员 → `action_type=delete, action_name=remove_org_member`
- 创建/归档组织 → `create/create_org`、`update/archive_org`
- 创建/删除项目 → `create/create_project`、`delete/delete_project`
- 批量操作 → `BatchInsert` 多行落地，不超过 500 行
- DB insert 失败 → 行为符合对应 `WritePolicy`
- `ListByOrg` / `ListByProject` / `ListByItem` / `ListByOperator` cursor 分页正确
- OP 查询权限：非管理员被拒绝
