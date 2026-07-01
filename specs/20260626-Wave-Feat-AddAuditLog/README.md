# AddAuditLog 评审入口

> 这份 README 是评审入口。目标是让读者先理解整体设计和闭环流程，再进入各分域方案；详细决策、历史讨论和研究资料仍保留在原文件中。

## 一句话方案

Wave 新增一套 `activity` 基础设施，用统一的 `item_type + action_type + detail` 模型记录关键活动：

- 项目内 item 写 `meta.activity_log`
- web global schema 下的 item 写 `global.activity_log`
- OP 后台人员操作继续写 `global.op_operation_log`
- 账号最近登录 / 登出 / 活跃时间写 `global.account` 字段，不进入活动表

## 推荐阅读顺序

| 顺序 | 文件 | 读完应理解什么 |
|------|------|----------------|
| 1 | [architecture.md](./architecture.md) | 总架构、两张活动表、服务路由、完整接入流程 |
| 2 | [plan-object.md](./plan-object.md) | `meta.activity_log` 的项目 item 接入细节 |
| 3 | [plan-global.md](./plan-global.md) | `global.activity_log` 的 global item 接入细节，含组织/项目/账号级 item |
| 4 | [plan-account.md](./plan-account.md) | 账号最近登录 / 登出 / 活跃字段方案 |
| 5 | [spec.md](./spec.md) | 需求边界、验收标准、详细用户场景 |
| 6 | [discussion.md](./discussion.md) | 当前唯一未锁定的 TEXT codec 问题 |
| 7 | [decisions.md](./decisions.md) | 历史决策和被覆盖的旧方案 |

研究资料在 [_research/](./_research/) 中，评审主线不需要先读。

## 总体边界

| 域 | 存储 | 典型 item | 查询主视角 |
|----|------|-----------|------------|
| Project item activity | `meta.activity_log` | Chart / Dashboard / Cohort / AB / Metric / Pipeline / Event / Property | `item_type + item_id` |
| Global item activity | `global.activity_log` | Organization / Project / Org Member / Project Member / Account API Token，后续可扩展 integration、billing config 等 | `org_id` / `project_id` / `account_id` + `item_type` |
| OP operation log | `global.op_operation_log` | OP 人员配置客户、配额、模板 | 维持现状 |
| Account activity fields | `global.account` | last_login_at / last_logout_at / last_active_at | account 主表字段 |

`global.activity_log` 不是 org 专用表。它是 web global schema 内的 item activity 表，org/project/member/account token 都只是 global schema item 的不同 scope。账号 API Token 这类对象没有天然 `org_id`，因此 global activity 的 scope 字段必须支持 `org_id`、`project_id`、`account_id` 按场景为空或填写，不能把 `org_id` 设计成所有记录的必填字段。

## 闭环流程

一次新活动接入必须按这个流程走，避免各业务模块临时拼 JSON：

```text
1. 定义对象
   -> 注册 ItemType
   -> 确定 storage scope: project 或 global
   -> 明确 item_id / item_name 规则

2. 定义动作
   -> 优先使用 create / update / delete / copy
   -> 如确实需要扩展 action_type，先走注册评审

3. 定义接入场景
   -> 注册 PolicyKey
   -> 配置 required_full / required_core / best_effort
   -> 不允许调用方运行时自由传策略

4. 定义 detail 生成规则
   -> 注册 projection allowlist
   -> 注册 exclude / mask / drop / transform
   -> 活动模块统一 diff、redaction、截断、序列化

5. 接入业务事务
   -> create 使用创建后的 ID 和展示名
   -> update/delete 在修改前读取旧快照
   -> 批量操作使用 BatchWrite*，共享 correlation_id

6. 写入与降级
   -> ActivityService 校验注册项
   -> 按 PolicyKey 决定失败行为
   -> DAO 写 meta.activity_log 或 global.activity_log

7. 查询与验证
   -> 查询接口返回 page/page_size/total/items
   -> detail 统一反序列化后返回
   -> 单测 / 集成测试覆盖成功、无变更、失败策略、权限
```

## 核心 API 形态

项目 item：

```go
activity.WriteProjectItemLog(ctx, activity.ProjectItemWriteInput{
    ItemType:   activity.ItemTypeChart,
    ItemID:     chart.ID,
    ItemName:   chart.Name,
    ActionType: activity.ActionTypeUpdate,
    PolicyKey:  activity.PolicyChartUpdate,
    Source:     activity.SourceWeb,
    OldValue:   oldChart,
    NewValue:   chart,
})
```

global item：

```go
activity.WriteGlobalItemLog(ctx, activity.GlobalItemWriteInput{
    OrgID:      orgID,
    ProjectID:  projectID,
    ItemType:   activity.ItemTypeProjectMember,
    ItemID:     accountID,
    ItemName:   member.DisplayName,
    ActionType: activity.ActionTypeCreate,
    PolicyKey:  activity.PolicyGlobalProjectMemberCreate,
    Source:     activity.SourceWeb,
    Extra:      map[string]any{"roles": roleIDs},
})
```

account-scoped global item：

```go
activity.WriteGlobalItemLog(ctx, activity.GlobalItemWriteInput{
    AccountID:  accountID,
    ItemType:   activity.ItemTypeAccountAPIToken,
    ItemID:     token.ID,
    ItemName:   token.Label,
    ActionType: activity.ActionTypeUpdate,
    PolicyKey:  activity.PolicyAccountAPITokenUpdate,
    Source:     activity.SourceWeb,
    OldValue:   oldToken,
    NewValue:   token,
})
```

## 已锁定决策

- 不新增 `action_name`；领域语义通过 `item_type + action_type + detail` 表达。
- 基础 `action_type` 为 `create / update / delete / copy`；扩展 action_type 必须注册评审。
- 不引入业务维护的 `operation_group_id`；批量/跨对象串联用基础设施生成或继承的 `correlation_id`。
- `detail_version` 不下放给业务方；serializer/parser 兼容由 activity 模块统一维护。
- `detail_payload` 列类型为 `TEXT`，不使用 PG `JSONB`。
- `global.activity_log` 是 web global schema item activity 表，不是 org 专用表；scope 使用 `org_id` / `project_id` / `account_id` 表达，不强制所有记录都有 `org_id`。
- diff/redaction 由 activity 模块统一处理；调用方提供业务快照或必要 extra，不手写敏感字段 diff。
- 查询接口保留 `total`，因为 V1 主场景是 OP / 内部排障。
- `last_active_at` Redis 故障时跳过本次 DB 写入，不降级为每个请求写 DB。

## 尚未锁定

`detail_payload` 的 TEXT 内部 codec 尚未锁定：

- `TEXT + readable JSON`
- `TEXT + compressed payload`

最终用真实样本测算 P50/P95/P99 payload 大小、压缩率、写入量、保留周期和排障体验后再定。详见 [discussion.md](./discussion.md)。

## Wave 代码对齐

Wave 项目代码路径：`/Users/wenshiqin/wave-worktrees/add_audit_record`

当前接入点主要位于：

- Project item：`apps/web/service/chart`、`apps/web/service/dashboard`、`apps/web/service/cohort`、`apps/web/service/ab`、`apps/web/service/metadata`、`apps/web/service/pipeline`
- Global item：`apps/web/service/organization`、`apps/web/service/project`、`apps/web/service/account/apitoken`
- DAO 风格参考：`apps/web/ma/dao/operation_log.go`、`apps/web/dao/global/*`、`pkg/dal/pgsqlx/metadb`、`pkg/dal/pgsqlx/globaldb`

因此方案采用显式 service 接入，而不是 CDC / trigger / AssetOperator 自动覆盖。
