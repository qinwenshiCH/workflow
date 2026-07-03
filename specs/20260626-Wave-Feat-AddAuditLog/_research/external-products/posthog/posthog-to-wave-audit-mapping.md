# 技术调研：PostHog → Wave 审计日志映射分析

**日期**: 2026-07-02
**来源**: PostHog 官方文档 + 源码分析（`posthog/models/activity_logging/`）

---

## 一、PostHog Activity Log 核心设计

### 1.1 设计定位

PostHog 的 activity log 是**企业级审计功能**（Premium Feature，需要 `AUDIT_LOGS` 订阅），覆盖 60+ 实体类型。

明确的使用场景：
- **调查意外变更**：谁改了什么、什么时候改的
- **合规审计准备**：SOC 2 等审计需要访问控制变更文档
- **跟踪 Feature Flag 发布**：发布百分比变化的时间线
- **监控 API Key 使用**：谁创建/撤销了密钥
- **通知**：通过 Slack/Webhook 订阅特定事件

### 1.2 表结构

**表名**: `posthog_activitylog`

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | UUID (PK) | 时间有序 UUID |
| `team_id` | PositiveInteger | nullable，所属项目 ID |
| `organization_id` | UUID | nullable，所属组织 ID |
| `user_id` | UUID | FK→User, SET_NULL，操作人 |
| `was_impersonated` | Boolean | nullable，是否 OP 模拟操作 |
| `is_system` | Boolean | nullable，是否系统自动 |
| `client` | VARCHAR(32) | nullable，`x-posthog-client` 请求头 |
| `ip_address` | INET | nullable，客户端 IP |
| `activity` | VARCHAR(79) | NOT NULL，操作类型 |
| `scope` | VARCHAR(79) | NOT NULL，实体类型 |
| `item_id` | VARCHAR(72) | nullable，实体 ID |
| `detail` | JSONB | nullable，变更详情 |
| `created_at` | TIMESTAMPTZ | default=now()，记录时间 |

**约束**: `CHECK(team_id IS NOT NULL OR organization_id IS NOT NULL)`
— 每行必须属于团队或组织。

### 1.3 操作类型（activity 字段）

```python
ChangeAction = Literal[
    "changed",      # 修改
    "created",      # 创建
    "deleted",      # 删除
    "merged",       # 合并
    "split",        # 拆分
    "exported",     # 导出
    "revoked",      # 撤销
    "logged_in",    # 登录
    "logged_out",   # 登出
    "copied",       # 复制
]
```

### 1.4 实体类型（scope 字段）

60+ 实体类型，使用 CharField 自由文本存储（应用层验证）：

```
Cohort, FeatureFlag, Person, Group, Insight, Dashboard, Experiment,
Survey, Notebook, HogFunction, Plugin, Organization, Team, User,
Annotation, Tag, BatchExport, Integration, Subscription, Role, ...
```

### 1.5 Detail 结构（JSONB）

```json
{
    "name": "资源展示名称",
    "changes": [
        {
            "field": "name",
            "action": "changed",
            "before": "旧值",
            "after": "新值"
        }
    ],
    "type": "internal_dashboard"
}
```

**Detail 所有可选字段**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 资源的展示名称 |
| `short_id` | string | 短 ID |
| `type` | string | 子类型鉴别器 |
| `changes` | Change[] | 字段级变更列表 |
| `trigger` | {job_type, job_id, payload} | 定时任务触发的事件 |
| `context` | object | 领域特定扩展上下文 |

**Change 结构**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `type` | string | 所属 scope |
| `action` | string | changed/created/deleted |
| `field` | string | 变更的字段名 |
| `before` | any | 旧值（敏感字段为 "masked"） |
| `after` | any | 新值（敏感字段为 "masked"） |

### 1.6 三层事件范围

| 层级 | 字段约束 | 事件示例 |
|------|---------|---------|
| Organization 级 | `organization_id` NOT NULL, `team_id` NULL | 成员邀请/移除/角色变更、SSO 配置、域名验证、组织设置 |
| Project 级 | `team_id` NOT NULL, `organization_id` 可选 | 项目创建/删除/设置变更、API Key 创建/撤销 |
| Resource 级 | `team_id` + `scope` + `item_id` | Feature Flag/Dashboard/Insight/Cohort 的 CRUD 等 |

### 1.7 查询与展示

**三个 API 端点**:

| 端点 | 说明 |
|------|------|
| `GET /api/projects/<id>/activity_log/` | 基础分页查询 |
| `GET /api/projects/<id>/advanced_activity_logs/` | 高级过滤 + 导出 |
| `GET /api/organizations/<org_id>/advanced_activity_logs/` | 组织级查询（管理员） |

**过滤参数**: date_range, user, scope, activity, item_ids, search_text, was_impersonated, is_system, client, ip_addresses, detail_filters

**分页**: Cursor 分页（默认）+ PageNumber 分页（可选）

**展示方式**:
1. **Activity 侧面板**: 在具体资源页面（如 Feature Flag 详情）显示该资源的历史
2. **项目设置页面**: 完整项目级活动日志，含高级过滤
3. **组织级视图**: 管理员查看跨项目活动
4. **资源 History 标签页**: 单一资源的变更历史

**导出**: CSV / Excel，应用当前过滤条件

### 1.8 索引设计

8 个索引（其中 6 个部分索引 + 2 个 GIN），核心模式：

```sql
-- 核心查询索引：按实体查询
(team_id, scope, item_id);

-- 组织级最近活动（部分索引，排除 detail IS NULL）
(organization_id, scope, created_at DESC) WHERE detail IS NOT NULL;

-- 用户常用查询（部分索引，排除 impersonated/system）
(team_id, activity, scope, user_id)
WHERE was_impersonated = false AND is_system = false;
```

---

## 二、Wave 审计日志需求（用户确认）

### 2.1 定位变更

| 维度 | 当前方案（活动日志） | 新方案（审计日志） |
|------|-------------------|------------------|
| 目标 | 内部排障/根因定位 | 第三方合规审计 |
| 客户 | 内部研发/OP | 外部审计/组织管理员 |
| 范围 | 项目内对象活动 | 全局管理面 CRUD + 登录事件 |
| 流量 | 所有来源（ui/api_token/internal/scheduler/backfill）| **仅站外**（ui + api_token）|
| 结构 | `meta.activity_log` + `global.activity_log` 分表 | **全局单表** |
| 存储 | TEXT `detail_payload` | JSONB `detail` |
| IP | V1 不记 | **必须记** IP |
| 业务内部 | 记录 AB 状态流转等 | **不干涉**，那是产品功能 |

### 2.2 范围确认

**记录**:
- 所有实体的管理面 CRUD 操作（Chart / Dashboard / Cohort / Pipeline / AB Experiment / Feature Flag / Metric / MA Campaign 等）
- 组织级管理（创建/修改组织、成员管理、角色变更）
- 项目级管理（创建/修改/删除项目、项目设置变更）
- 凭证管理（Account API Token 创建/删除）
- 登录/登出事件

**不记录**:
- `internal` / `scheduler` / `backfill` 来源的操作
- AB 状态流转（online/offline 等）—— 那是 AB 产品功能
- Metric 口径变更细节 —— 那是 Metric 产品功能
- MA Campaign 启动/暂停 —— 那是 MA 产品功能
- 定时任务/Pipeline 内部回调
- 任何"内部干了什么事"

### 2.3 设计原则确认

1. **Append-only 不可篡改**：用户不可修改或删除
2. **保留策略 + 导出**：按期限保留，超期永久删除；支持 CSV/Excel 导出
3. **按时间分区**：避免单表无限增长
4. **全局单表**：不按项目/组织拆表

### 2.4 待确认问题

以下问题用户还未回答，推进到第 4 问：
1. `detail` 用 JSONB 还是 TEXT？（PostHog 用 JSONB）
2. `item_name` 独立列 vs detail 内嵌？
3. `was_impersonated` V1 是否需要？
4. `source` 字段还需要吗，还是用 `client` + `is_system` 代替？

---

## 三、PostHog → Wave 映射

| 概念 | PostHog | Wave 审计日志 |
|------|---------|-------------|
| 全局表 | `posthog_activitylog` | `global.audit_log` |
| 组织 ID | `organization_id` UUID | `org_id` |
| 项目 ID | `team_id` PositiveInteger | `project_id` |
| 操作人 | `user_id` UUID FK→User | `account_id` FK→Account |
| 操作 | `activity` VARCHAR(79) | `action` VARCHAR |
| 实体类型 | `scope` VARCHAR(79) | `domain` VARCHAR(32) + `feature` VARCHAR(32) |
| 实体 ID | `target_id` VARCHAR(72) | `target_id` VARCHAR |
| 详情 | `detail` JSONB | `detail`（待定 JSONB/TEXT）|
| 来源客户端 | `client` VARCHAR(32) | 待定 |
| 是否系统 | `is_system` Boolean | 由 `source` 隐式表达 |
| 是否模拟 | `was_impersonated` Boolean | 待定 |
| IP | `ip_address` INET | `ip_address` INET |
| 记录时间 | `created_at` TIMESTAMPTZ | `created_at` TIMESTAMPTZ |
| 分区 | ❌ 无 | ✅ 按时间分区 |

### 3.1 PostHog 有而 Wave 可能不需要的

- **`was_impersonated`**：V1 不确定是否需要（OP 模拟用户操作的场景在 Wave 是否存在？）
- **`is_system`**：Wave 用 `source` 已经区分了来源，不需要额外布尔字段
- **`client`**：PostHog 用来标记 `x-posthog-client`（如 "mcp"），Wave V1 是否需要？

### 3.2 Wave 需要而 PostHog 没有的

- **按时间分区**：PostHog 不分区是已知不足，Wave 必须分区
- **`source` 语义**：PostHog 用 `is_system` 区分人 vs 系统，Wave 要更细的 `{ui, api_token}`（且 MCP 入口需独立注入 source）`

---

## 四、Detail 结构建议

### 4.1 PostHog 风格

```json
{
    "name": "资源名称",
    "changes": [
        {"field": "name", "action": "changed", "before": "旧名", "after": "新名"},
        {"field": "description", "action": "changed", "before": "旧描述", "after": null}
    ]
}
```

- `name`: 资源展示名称（冗余快照，即使用户删除资源也能追溯）
- `changes[]`: 字段级变更列表
- 敏感字段值替换为 `"masked"`

### 4.2 Create 场景

```json
{
    "name": "新 Dashboard",
    "changes": [
        {"field": "name", "action": "created", "after": "新 Dashboard"},
        {"field": "description", "action": "created", "after": "描述"}
    ]
}
```

### 4.3 Delete 场景

```json
{
    "name": "已删除的 Chart",
    "changes": [
        {"field": "name", "action": "deleted", "before": "已删除的 Chart"}
    ]
}
```

### 4.4 登录事件

```json
{
    "changes": [],
    "context": {
        "login_method": "Google OAuth",
        "ip_address": "203.0.113.42"
    }
}
```
(activity = "logged_in", scope = "Account")

---

## 五、关键发现与建议

### 5.1 PostHog 做对的地方

| 决策 | 理由 |
|------|------|
| **全局单表** | 60+ 实体类型统一存储，3 年演进没问题 |
| **结构化 changes 而非 raw snapshot** | 精确到字段级 diff，存储仅为 full snapshot 的 1/N |
| **两层排除体系** | 通用排除 + 按类型排除，控制噪音 |
| **请求上下文中间件** | 自动传播 user/IP/client，调用方不需要手动传参 |
| **Append-only 不可修改** | 合规刚需，用户不可删除/修改记录 |
| **付费功能门禁** | 控制使用规模 |

### 5.2 PostHog 的不足（Wave 应避免）

| 不足 | 影响 | Wave 对策 |
|------|------|----------|
| **无分区** | 表无限增长，亿级数据后查询和维护困难 | 按时间分区 |
| **无数据保留策略** | 历史数据无法高效清理 | 按套餐定义保留期限，到期删除 |
| **JSONB 膨胀** | detail 列长期存储，无压缩 | 可考虑 TEXT + 按需压缩 |
| **信号风暴风险** | 批量操作时没有限流机制 | Wave 是显式调用，可控性更好 |

### 5.3 Wave 特有考量

1. **`source` 字段**：PostHog 用 `is_system` 布尔值区分人/系统，Wave 需要 `{ui, api_token}` 两值（因为审计日志只记站外流量，`internal/scheduler/backfill` 来源根本不写入）
2. **IP 地址必记**：PostHog 是 `nullable`，Wave 合规场景下 IP 是必记字段
3. **分区是刚需**：不做分期表会成运维负担

---

## 六、参考链接

- [PostHog Activity Logs 官方文档](https://posthog.com/docs/settings/activity-logs)
- [PostHog API Schema (Swagger)](https://us.posthog.com/api/schema/swagger-ui/)
- 源码分析: `posthog/models/activity_logging/activity_log.py`
