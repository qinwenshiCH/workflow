# 功能规格：Wave 审计日志

**目录**: `20260626-Wave-Feat-AddAuditLog`
**创建日期**: 2026-06-26
**最后更新**: 2026-07-02（方向变更：活动日志 → 审计日志）
**状态**: 评审中
**技术方案**: [plan-doris.md](./plan-doris.md)（Doris 异步攒批，替代原 PG 方案 plan-audit-log.md）
**评审入口**: [README.md](./README.md)

---

## 背景

现有几套分散的操作记录系统，各自为政，无法满足第三方合规审计要求：

| 系统 | 位置 | 存储方式 | 问题 |
| ---- | ---- | ------- | ---- |
| OP Console 操作记录 | `global.op_operation_log` | 独立表，before/after 快照 | 仅 OP 管理面，不覆盖项目内操作 |
| AB 操作记录 | `ab_feature_flag.details` JSONB 内嵌 | 内嵌在行内 | 无法独立查询、无法跨对象追溯 |
| Metric 历史 | `meta.metric_define_history` | 独立表 | 仅覆盖 define 字段变更 |
| Asset Behavior | `meta.asset_behavior` | 独立表 | 分析用途，非操作记录 |

**核心缺失**：

- 没有**全局统一审计表**，无法回答"谁在什么时候做了什么"
- 没有 IP 记录，缺乏合规审计基本要素
- 没有按时间分区的保留策略，数据无限增长
- 缺少**导出能力**，无法支撑第三方审计

### 方向变更

本规格最初定位为**活动日志**（内部排障），2026-07-02 重构为**审计日志**（第三方合规审计），完全替代原方案。详细决策见 [decisions.md](./decisions.md)。

---

## 价值定位

1. **合规审计（P0）**：面对 SOC 2 等第三方审计，可导出完整审计日志证明"谁在何时做了什么"
2. **安全追溯（P0）**：关键对象异常变更后可沿审计链定位到具体操作人和来源 IP
3. **组织治理（P1）**：组织管理员可审查成员的管理操作，支撑放权和问责
4. **事故排查（P1）**：排障时可快速回答 who / what / when / where

### 设计原则

1. **Append-only**：审计日志不可修改、不可删除
2. **仅站外流量**：只记录客户主动发起的操作（ui / api_token），内部流量不记
3. **管理面聚焦**：记录所有实体的管理面操作（CUD + copy），不追踪内部状态流转
4. **全局单表**：不分表，按时间分区
5. **PostHog 模型参考**：Detail 采用结构化 changes 而非 full snapshot

---

## 范围

### 纳入审计日志

**账号层**（无组织/项目归属）：

- 登录/登出事件
- Account API Token created/updated/deleted

**组织层**（组织级管理）：

- 组织信息变更、归档
- 组织成员添加/角色变更/移除
- 成员邀请创建/撤销

**项目层**（项目内对象管理）：

- 项目创建/配置/归档
- 项目成员管理
- Chart / Dashboard created/updated/deleted
- Cohort / Pipeline created/updated/deleted
- AB 对象（Experiment / FeatureGate / FeatureConfig）created/updated/deleted
- Campaign created/updated（无 Delete）
- 元数据对象（TrackedEvent / VirtualEvent / EventProperty / UserProperty / VirtualProperty）created/updated/deleted

### 不纳入审计日志

| 内容 | 理由 |
| ---- | ---- |
| 内部流量（internal / scheduler / backfill） | 非客户主动操作 |
| AB 状态流转（online/offline/debug/release） | AB 产品功能，各自有记录 |
| Metric 口径变更细节 | Metric 产品功能 |
| Pipeline 内部回调/执行记录 | 系统运行日志 |
| 定时任务/cron 调度 | 系统自动化 |
| 资产基础设施表（asset_behavior/reference/metrics 等） | 非管理面操作 |
| AB Layer / Holdout | Experiment 的内部子概念 |
| 读取/查看操作 | 仅记录状态变更 |

---

## 数据模型

### 表结构

```sql
CREATE TABLE global.audit_log (
    id              BIGSERIAL,
    org_id          INT,                -- NULL = 账号层事件
    project_id      INT,                -- NULL = 组织/账号层事件
    account_id      INT NOT NULL,       -- 操作人，查询时 JOIN global.account
    action          VARCHAR(64) NOT NULL,  -- created/updated/deleted/logged_in/logged_out/login_failed
    domain          VARCHAR(64) NOT NULL,  -- 粗粒度领域：account/organization/project/asset/metadata
    feature         VARCHAR(64) NOT NULL,  -- 细粒度实体类型：session/account_profile/token/chart/experiment/...
    target_id       VARCHAR(72),        -- NULL = 登录事件等无资源场景
    source          VARCHAR(16) NOT NULL DEFAULT 'ui',  -- ui / api_token
    ip_address      VARCHAR(64) NOT NULL, -- 操作者 IP（合规刚需）
    detail          TEXT,               -- JSON: {name: str, changes: [...]}
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id)
);
```

**分区策略**：V1 不分区。待数据规模需要时按 `created_at` RANGE 按月分区。

### Action 枚举

```text
created     创建对象
updated     修改对象（含状态变更）
deleted     删除对象
logged_in   账号登录
logged_out  账号登出
login_failed 账号登录失败
```

所有状态变更（如 launch/enable/pause 等）统一用 `updated` + changes 表达。

### Domain / Feature 清单

使用 `domain + feature` 两列替代单一 `scope`，5 个 domain × 22 个 entity。domain 对齐 Wave 内部领域模型：

| domain | feature | 对应实体 | 层级 | 操作 |
| --- | --- | --- | --- | --- |
| `account` | `session` | Account | 账号层 | logged_in / logged_out / login_failed |
| `account` | `account_profile` | Account | 账号层 | updated |
| `account` | `token` | AccountAPIToken | 账号层 | created / updated / deleted |
| `organization` | `org` | Organization | 组织层 | created / updated / deleted |
| `organization` | `org_member` | OrganizationMember | 组织层 | created / updated / deleted |
| `organization` | `org_member_invite` | OrganizationInvite | 组织层 | created / deleted |
| `project` | `project` | Project | 项目层 | created / updated / deleted |
| `project` | `project_member` | ProjectMember | 项目层 | created / updated / deleted |
| `asset` | `chart` | Chart | 项目层 | created / updated / deleted |
| `asset` | `dashboard` | Dashboard | 项目层 | created / updated / deleted |
| `asset` | `cohort` | Cohort | 项目层 | created / updated / deleted |
| `asset` | `pipeline` | Pipeline | 项目层 | created / updated / deleted |
| `asset` | `campaign` | Campaign | 项目层 | created / updated |
| `asset` | `experiment` | Experiment | 项目层 | created / updated / deleted |
| `asset` | `feature_gate` | FeatureGate | 项目层 | created / updated / deleted |
| `asset` | `feature_config` | FeatureConfig | 项目层 | created / updated / deleted |
| `metadata` | `metric` | Metric | 项目层 | created / updated / deleted |
| `metadata` | `tracked_event` | TrackedEvent | 项目层 | created / updated / deleted |
| `metadata` | `virtual_event` | VirtualEvent | 项目层 | created / updated / deleted |
| `metadata` | `event_property` | EventProperty | 项目层 | created / updated / deleted |
| `metadata` | `user_property` | UserProperty | 项目层 | created / updated / deleted |
| `metadata` | `virtual_property` | VirtualProperty | 项目层 | created / updated / deleted |

### Detail 结构

参考 PostHog change 模型，TEXT 列存储 JSON：

```json
{
    "name": "资源展示名称",
    "changes": [
        {"field": "name", "action": "changed", "before": "旧名", "after": "新名"},
        {"field": "active", "action": "changed", "before": false, "after": true}
    ]
}
```

- `name`: 资源展示名快照（不单独设列，查询时 JOIN 原表）
- `changes[]`: 字段级变更列表
- 敏感字段值替换为 `"masked"`
- 不存完整对象快照

### 索引设计

```sql
-- 核心：按组织+领域+实体查询
CREATE INDEX idx_alog_org_domain_feature ON global.audit_log (org_id, domain, feature, target_id);
-- 核心：按项目+领域+实体查询
CREATE INDEX idx_alog_proj_domain_feature ON global.audit_log (project_id, domain, feature, target_id);
-- 时间排序
CREATE INDEX idx_alog_created_at ON global.audit_log (created_at DESC);
-- 按操作人查
CREATE INDEX idx_alog_account ON global.audit_log (account_id, created_at DESC);
```

---

## 写入与读取

### 写入路径

1. HTTP/MCP 请求进入 → Middleware（或 MCP handler）提取 `account_id`、`ip_address`、`source` 注入 context
2. **GORM 审计插件自动拦截**（参见技术方案 §3.3）：
   - `AfterCreate` → 自动写入 action=created，含全字段快照
   - `BeforeUpdate` + `AfterUpdate` → 自动 diff，仅写入有变更的字段
   - `AfterDelete` → 自动写入 action=deleted，含删除前名称
3. 登录/登出在认证 filter 中显式写入（非 DB 操作，GORM 无法捕获）
4. AuditLogService 校验 `domain + feature` 组合已注册
5. INSERT INTO `global.audit_log`
6. 写入策略为 blocking（审计日志不可静默丢失）

**MCP 入口说明**：MCP 协议不走标准 HTTP middleware，在 MCP handler 中独立完成 source 注入（认证后手动将 `source = api_token` 写入 context），下游逻辑统一处理。

### 写入入口拦截

- 只有 `source = ui | api_token` 的请求进入写入路径
- `internal / scheduler / backfill` 来源直接跳过
- MCP 请求：认证后 source = api_token

### 读取路径

1. 查询接口：`AuditLogService.List(ctx, &Query{OrgID, ProjectID, Domain, Feature, TargetID, AccountID, Action, StartTime, EndTime, Cursor, PageSize})`
2. 结果中 `account_id` JOIN `global.account` 获取用户信息（name / email），平铺为 `account_name` / `account_email`
3. 过滤维度：时间范围、组织、项目、domain、feature、target_id、操作人、action
4. 排序：`created_at DESC`
5. 分页：**cursor 模式**（`WHERE created_at < ? ORDER BY created_at DESC LIMIT ?`），避免审计导出时的数据漂移
6. 返回结构含 `next_cursor` 和 `has_more`，cursor 为空表示无更多数据

### 导出

- 格式：CSV / Excel
- 应用当前查询过滤条件
- V1 提供 OpenAPI 导出，不提供前端查看页面

---

## 边界与异常处理

- **单条 detail 大小预算**：64KB，超限截断并记录 warning
- **同对象高频操作**：每条独立记录，不合并去重
- **缺少 IP 的请求**：不合法，拒绝写入
- **未注册的 domain + feature 组合**：写入入口直接拒绝
- **Append-only**：不可修改、不可删除
- **批量写入**：上限 500 行/批，同批失败整体回滚
- **分区管理**：超出保留期限的分区 DROP 或 detach 归档

---

## 需求

### 功能需求

- **FR-001**: 系统 MUST 提供 `global.audit_log` 作为全局统一审计表，按 `created_at` 范围分区
- **FR-002**: 系统 MUST 记录所有 domain + feature 的管理面操作（created/updated/deleted + logged_in/logged_out/login_failed），覆盖 5 个 domain × 22 个实体
- **FR-003**: 系统 MUST 仅记录站外流量（source ∈ {ui, api_token}），内部流量不写入
- **FR-004**: 审计日志 MUST 包含 `org_id`、`project_id`、`account_id`、`action`、`domain`、`feature`、`target_id`、`ip_address`、`detail`、`created_at`
- **FR-005**: 系统 MUST 在写入入口校验 domain + feature 合法性，未注册组合拒绝写入
- **FR-006**: 系统 MUST 提供按时间范围、组织、项目、domain、feature、target_id、操作人、action 过滤的查询接口
- **FR-007**: 系统 MUST 支持通过 OpenAPI 导出 CSV / Excel 格式的审计日志
- **FR-008**: 写入策略 MUST 为 blocking，审计日志写入失败返回 error
- **FR-009**: 审计日志 MUST 为 append-only，不可修改、不可删除
- **FR-010**: 登录/登出/登录失败事件 MUST 写入审计日志，domain=`account`、feature=`session`，action=`logged_in` / `logged_out` / `login_failed`

### 非功能需求

- **NFR-001**: P99 写入延迟 < 50ms（含序列化和 DB INSERT）
- **NFR-002**: 按 `(project_id, domain, feature, target_id)` 索引，亿级数据量下单对象查询 < 1s（P99）
- **NFR-003**: 单条 detail 大小预算 64KB
- **NFR-004**: 按月分区，单个分区建议不超过 1 亿行
- **NFR-005**: 查询时通过 `account_id` JOIN `global.account` 获取用户信息，不做冗余存储

---

## 成功标准

- **SC-001**: 审计日志覆盖 5 domain × 22 feature 的管理面操作（created/updated/deleted + logged_in/logged_out/login_failed）
- **SC-002**: 仅站外流量写入，内部流量验证不进表
- **SC-003**: P99 写入延迟 < 50ms
- **SC-004**: 按 `(project_id, domain, feature, target_id)` 单对象查询 < 1s（P99）
- **SC-005**: 支持 CSV/Excel 导出
- **SC-006**: 按月分区创建和 DROP 脚本可运维

---

## 技术方案

所有技术设计（表 DDL、AuditLogService 接口、Detail 构造、接入模板、查询 API、分区管理、交付阶段）详见 [plan-audit-log.md](./plan-audit-log.md)。
