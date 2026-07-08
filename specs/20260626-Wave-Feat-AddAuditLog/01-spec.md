# 功能规格：Wave 审计日志

**目录**: `20260626-Wave-Feat-AddAuditLog`
**创建日期**: 2026-06-26
**最后更新**: 2026-07-08（补充规模假设与方案对比分析）
**状态**: 评审中
**技术方案**: [03-plan-pg.md](./03-plan-pg.md) + [04-detail-pg.md](./04-detail-pg.md)（当前推荐） / [03-plan-doris.md](./03-plan-doris.md) + [04-detail-doris.md](./04-detail-doris.md)（备选）

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
- 没有明确的保留 / 归档策略，数据增长不可控
- 缺少**导出能力**，无法支撑第三方审计

### 方向变更

本规格最初定位为**活动日志**（内部排障），2026-07-02 重构为**审计日志**（第三方合规审计），完全替代原方案。详细决策见 [02-decisions.md](./02-decisions.md)。

---

## 价值定位

1. **合规审计（P0）**：面对 SOC 2 等第三方审计，可导出完整审计日志证明"谁在何时做了什么"
2. **安全追溯（P0）**：关键对象异常变更后可沿审计链定位到具体操作人和来源 IP
3. **组织治理（P1）**：组织管理员可审查成员的管理操作，支撑放权和问责
4. **事故排查（P1）**：排障时可快速回答 who / what / when / where

### 设计原则

1. **Append-only**：审计日志不可修改、不可删除
2. **仅站外流量**：只记录客户主动发起的操作（ui / api_token / mcp），内部流量不记
3. **管理面聚焦**：记录所有实体的管理面操作（CUD），不追踪内部状态流转
4. **全局单表**：统一逻辑模型，不再分散落在各业务表
5. **结构化 detail**：使用 `schema_version / account / target / comment / extra` 版本化 envelope；拿不到就允许为空

---

## 范围

### 纳入审计日志

**账号层**（无组织/项目归属）：

- 登录/登出事件（包括登录失败）
- 账号设置变更（密码修改、邮箱变更等）
- Account API Token created/updated/deleted

**组织层**（组织级管理）：

- 组织设置变更、删除
- 组织成员添加/角色变更/移除
- 成员邀请创建/撤销

**项目层**（项目内对象管理）：

- 项目创建/配置/删除
- 项目成员管理
- Chart / Dashboard / Cohort / Pipeline / TrackingPlan created/updated/deleted
- AB 对象（Experiment / FeatureGate / FeatureConfig / Layer / Holdout / Target）created/updated/deleted
- 元数据对象（Metric / TrackedEvent / VirtualEvent / EventProperty / UserProperty / VirtualProperty）created/updated/deleted

### 不纳入审计日志

| 内容 | 理由 |
| ---- | ---- |
| 内部流量（internal / scheduler / backfill） | 非客户主动操作 |
| AB 状态流转（online/offline/debug/release） | AB 产品功能，各自有记录 |
| Metric 口径变更细节 | Metric 产品功能 |
| Pipeline 内部回调/执行记录 | 系统运行日志 |
| 定时任务/cron 调度 | 系统自动化 |
| 资产基础设施表（asset_behavior/reference/metrics 等） | 非管理面操作 |
| 读取/查看操作 | 仅记录状态变更 |

---

## 数据模型

### 表结构

```sql
CREATE TABLE global.audit_log (
    id              BIGSERIAL,
    event_id        VARCHAR(64) NOT NULL,
    org_id          BIGINT,             -- NULL = 账号层事件
    project_id      BIGINT,             -- NULL = 组织/账号层事件
    account_id      BIGINT,             -- 操作人；无法解析时可为空
    action          VARCHAR(64) NOT NULL,  -- created/updated/deleted/logged_in/logged_out/login_failed
    domain          VARCHAR(64) NOT NULL,  -- 粗粒度领域：account/organization/project/asset/metadata
    feature         VARCHAR(64) NOT NULL,  -- 细粒度实体类型：session/account_setting/api_token/chart/experiment/... 共 25 个
    target_id       VARCHAR(64),        -- NULL = 登录事件等无资源场景
    source          VARCHAR(16) NOT NULL DEFAULT 'ui',  -- ui / api_token / mcp
    ip_address      VARCHAR(64) NOT NULL, -- 操作者 IP（合规刚需）
    detail          TEXT,               -- JSON 字符串：{schema_version, account, target, comment, extra}
    occurred_at     TIMESTAMPTZ NOT NULL, -- 事件发生时间
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(), -- 入库时间
    PRIMARY KEY (id)
);
```

**保留策略**：PG 方案 V1 可先不分区；Doris 方案保留自动月分区。统一要求有明确保留与归档策略。

### Action 枚举

```text
created     创建对象
updated     修改对象（含状态变更）
deleted     删除对象
logged_in   账号登录
logged_out  账号登出
login_failed 账号登录失败
```

所有纳入审计的管理面修改统一用 `updated` 表达；V1 不强制记录字段级 diff。

### Domain / Feature 清单

使用 `domain + feature` 两列替代单一 `scope`，5 个 domain × 25 个 entity。domain 对齐 Wave 内部领域模型：

| domain | feature | 对应实体 | 层级 | 操作 |
| --- | --- | --- | --- | --- |
| `account` | `session` | Account | 账号层 | logged_in / logged_out / login_failed |
| `account` | `account_setting` | Account（密码/邮箱变更） | 账号层 | updated |
| `account` | `api_token` | AccountAPIToken | 账号层 | created / updated / deleted |
| `organization` | `org_setting` | Organization | 组织层 | updated |
| `organization` | `org_member` | OrganizationMember | 组织层 | created / updated / deleted |
| `organization` | `org_member_invitation` | OrganizationInvite | 组织层 | created / deleted |
| `project` | `project_setting` | Project | 项目层 | created / updated / deleted |
| `project` | `project_member` | ProjectMember | 项目层 | created / updated / deleted |
| `asset` | `chart` | Chart | 项目层 | created / updated / deleted |
| `asset` | `dashboard` | Dashboard | 项目层 | created / updated / deleted |
| `asset` | `cohort` | Cohort | 项目层 | created / updated / deleted |
| `asset` | `pipeline` | Pipeline | 项目层 | created / updated / deleted |
| `asset` | `tracking_plan` | TrackingPlan | 项目层 | created / updated / deleted |
| `asset` | `experiment` | Experiment | 项目层 | created / updated / deleted |
| `asset` | `feature_gate` | FeatureGate | 项目层 | created / updated / deleted |
| `asset` | `feature_config` | FeatureConfig | 项目层 | created / updated / deleted |
| `asset` | `layer` | Layer | 项目层 | created / updated / deleted |
| `asset` | `holdout` | Holdout | 项目层 | created / updated / deleted |
| `asset` | `target` | Target | 项目层 | created / updated / deleted |
| `metadata` | `metric` | Metric | 项目层 | created / updated / deleted |
| `metadata` | `tracked_event` | TrackedEvent | 项目层 | created / updated / deleted |
| `metadata` | `virtual_event` | VirtualEvent | 项目层 | created / updated / deleted |
| `metadata` | `event_property` | EventProperty | 项目层 | created / updated / deleted |
| `metadata` | `user_property` | UserProperty | 项目层 | created / updated / deleted |
| `metadata` | `virtual_property` | VirtualProperty | 项目层 | created / updated / deleted |

### Detail 结构

V1 的 `detail` 固定为统一版本化 envelope，两方案（PG / Doris）使用同一结构：

```json
{
    “schema_version”: 1,
    “account”: {“id”: 123, “name”: “张三”},
    “target”: {“id”: “34”, “name”: “增长看板”, “type”: “dashboard”, “visibility”: “project”},
    “comment”: “dashboard charts updated”,
    “extra”: {“chart_ids”: [1, 2, 3]}
}
```

- `schema_version`: 固定版本号，当前为 `1`，后续只做 add-only 扩展
- `account`: 操作时的 actor 快照（`id` 必填，`name` best effort），由 audit.Log 传入时记录；不包含邮箱
- `target`: 过滤后的被操作资源摘要；`update` 场景直接记录 after 摘要；至少保留 `id` + `name`
- `comment`: 可选的人类可读说明，只放调用点天然已知的信息，不额外查库
- `extra`: 批量对象列表或事件专属扩展
- `detail` 是可选字段；不为了构造它额外查库
- **调用方不传敏感字段**：构造 detail 时自行排除密码、token 等敏感内容，入口层不设脱敏过滤
- 如后续确有审计机构要求字段级变化，再按需追加 `changes[]` 或 `before_snapshot`
- 单条 detail 大小预算 64KB，超限直接丢弃 detail（`detail = NULL`，comment 设为 `”detail exceeds 64KB, discarded”`）

**账号名说明**：detail.account.name 是 best effort 的当时名快照，不是当前名，也不会为此额外查库；拿得到就写，拿不到就只保留 `account_id`。PG 读侧可通过 JOIN `global.account` 额外补充当前名作为辅助，但不能把它当成历史快照。

### 索引设计

V1 只保留服务高频审计查询的最小索引集：

```sql
CREATE INDEX idx_alog_project_time ON global.audit_log (project_id, occurred_at DESC);
CREATE INDEX idx_alog_org_time ON global.audit_log (org_id, occurred_at DESC);
CREATE INDEX idx_alog_account_time ON global.audit_log (account_id, occurred_at DESC);
```

- 高频路径是 `org/project + time range` 的查询和导出
- `target_id` 过滤在 scoped 时间范围内完成，V1 不为它单独建复合索引
- 只有当”按对象查历史”成为真实高频流量时，再补 scoped target 索引
- PG V1 维持 3 个高频索引不动，不新增也不删减；Doris 使用 `(occurred_at, org_id, project_id, account_id, event_id)` 作为 DUPLICATE KEY

---

## 写入与读取

### 写入路径

1. HTTP/MCP 请求进入 → Middleware（或 MCP handler）提取 `account_id`、`ip_address` 等上下文；`source` 由 `ui / api_token / mcp` 派生
2. Handler / Service 在业务成功后显式调用 `audit.Log(...)`；登录 / 登出 / 登录失败在 `controller/account` 写入
3. Audit writer 校验 `domain + feature` 组合已注册
4. 主流程只做 enqueue，不等待最终落库
5. 后台批量 flush 到目标存储（PG 或 Doris）
6. flush 失败进入可回放队列并告警，不允许静默 drop

**MCP 入口说明**：MCP 协议不走标准 gin middleware，但现有鉴权已注入 `Aid / Token / IsAccountAPIToken / Pid`；V1 需额外补 `client_ip` 与独立 `audit_source`，并在 MCP 入口显式写入 `source = mcp`。

### 写入入口拦截

- 只有站外管理面请求会显式调用审计写入；其 `source` 只允许 `ui | api_token | mcp`
- `internal / scheduler / backfill` 不进入显式审计写入路径
- MCP 请求在鉴权后显式设为 `source = mcp`

### 读取路径

1. 查询接口：`AuditLogService.List(ctx, &Query{OrgID, ProjectID, AccountID, Domain, Feature, TargetID, Action, StartTime, EndTime, Cursor, PageSize})`
2. 导出与查询以 `account_id` 为准；如需当前操作者名称，再按 `account_id` 补齐
3. 过滤维度：时间范围、组织、项目、操作人、domain、feature、target_id、action；其中 `domain / feature / target_id` 只在 scoped 查询内有意义
4. 排序：`occurred_at DESC`
5. 分页：**cursor 模式**，使用 `(occurred_at, id|event_id)` 作为复合游标，避免同时间戳下漏数/重数
6. 返回结构含 `next_cursor` 和 `has_more`，cursor 为空表示无更多数据

### 导出

- 格式：CSV / Excel
- 应用当前查询过滤条件
- V1 提供 OpenAPI 导出，不提供前端查看页面

---

## 边界与异常处理

- **单条 detail 大小预算**：64KB（JSON 序列化后），超限时直接丢弃 detail（`detail_payload = NULL`，comment 设为 `"detail exceeds 64KB, discarded"`），打 warn 日志。审计行本身正常写入
- **同对象高频操作**：每条独立记录，不合并去重
- **缺少 IP 的请求**：审计写入视为非法，记录 critical 告警；不回滚已成功的业务操作
- **不传敏感字段**：调用方构造 detail 时自行排除敏感内容，入口层不设脱敏过滤，不依赖运行时脱敏
- **未注册的 domain + feature 组合**：写入入口直接拒绝
- **Append-only**：不可修改、不可删除
- **批量写入**：上限 500 行/批；channel 满时非阻塞 enqueue，满则丢弃并记 drop counter + error 日志，不设本地 spool
- **IP 地址获取**：gin 路由依赖 APISIX 透传的 `X-Real-IP`（`c.ClientIP()`）；MCP 路由在 `net/http` 入口读取 `X-Real-IP`。V1 不配置全局 `TrustedProxies`，文档说明 IP 可能不准确
- **分区管理**：超出保留期限的分区 DROP 或 detach 归档

---

## 需求

### 功能需求

- **FR-001**: 系统 MUST 提供统一审计日志存储；PG 方案为 `global.audit_log`，Doris 方案为 `sw_dw_global.audit_log`
- **FR-002**: 系统 MUST 记录所有 domain + feature 的管理面操作（created/updated/deleted + logged_in/logged_out/login_failed），覆盖 5 个 domain × 25 个实体
- **FR-003**: 系统 MUST 仅记录站外流量（source ∈ {ui, api_token, mcp}），内部流量不写入
- **FR-004**: 审计日志 MUST 包含 `event_id`、`org_id`、`project_id`、`account_id`、`action`、`domain`、`feature`、`target_id`、`ip_address`、`detail`、`created_at`
- **FR-005**: 系统 MUST 在写入入口校验 domain + feature 合法性，未注册组合拒绝写入
- **FR-006**: 系统 MUST 提供按时间范围、组织、项目、domain、feature、target_id、操作人、action 过滤的查询接口
- **FR-007**: 系统 MUST 支持通过 OpenAPI 导出 CSV / Excel 格式的审计日志
- **FR-008**: 写入策略 MUST 为异步；主流程只负责 enqueue，不等待最终落库完成
- **FR-009**: 审计日志 MUST 为 append-only，不可修改、不可删除
- **FR-010**: 登录/登出/登录失败事件 MUST 写入审计日志，domain=`account`、feature=`session`，action=`logged_in` / `logged_out` / `login_failed`

### 非功能需求

- **NFR-001**: 主流程审计附加开销 P99 < 5ms（仅含构造 entry + enqueue）
- **NFR-002**: 常规审计查询（`org_id|project_id + time range`，可选带 `account_id/domain/feature/action/target_id` 过滤）P99 < 1s
- **NFR-003**: 单条 detail 大小预算 64KB
- **NFR-004**: 必须有明确保留策略；Doris 可用自动月分区，PG 在数据规模证明需要前可保持单表
- **NFR-005**: 导出默认包含 `account_id`；如需可读名称，只补当前 `account_name`，不导出邮箱
- **NFR-006**: channel 满时 MUST 非阻塞 enqueue，满则丢弃并记 +1 drop counter + error 日志；不设本地 spool
- **NFR-007**: MUST 暴露 `audit_channel_drop_total` 一个 counter 指标，用于监控丢弃事件

---

## 成功标准

- **SC-001**: 审计日志覆盖 5 domain × 25 feature 的管理面操作（created/updated/deleted + logged_in/logged_out/login_failed）
- **SC-002**: 仅站外流量写入，内部流量验证不进表
- **SC-003**: 主流程审计附加开销 P99 < 5ms；最终落库异步完成
- **SC-004**: 按 `org/project + time range` 的常规审计查询与导出分页 P99 < 1s；单对象历史查询在该范围内可完成筛选
- **SC-005**: 支持 CSV/Excel 导出
- **SC-006**: 保留 / 归档策略可运维；当选择 Doris 时自动月分区可用

---

## 规模假设与方案对比

### 规模假设

基于 Wave 平台特征（每个 org 数十个活跃用户，管理面操作为低频），且按月分区为前提：

| 场景 | 每 org 日均事件数 | 100 org | 500 org | 1000 org |
| --- | --- | --- | --- | --- |
| **CUD**（CUD + 登录） | ~50 | 5K/天 → ~1.8M/年 | 25K/天 → ~9M/年 | 50K/天 → ~18M/年 |
| **CRUD**（CRUD + 登录） | ~5,000 | 500K/天 → ~180M/年 | 2.5M/天 → ~900M/年 | 5M/天 → ~1.8B/年 |

> 读操作按每人每天 ~100 次估算（Dashboard 加载、Chart 查询、列表翻页等），每 org 约 50 个活跃用户。实际量级取决于接入范围，以上为上限估算。

### 月分区存储对比

按 ~1KB/行（PG 行存）、~0.3KB/行（Doris 列存 + ZSTD 压缩）、保留 1 年估算：

| 场景 | 规模 | PG 每年存储 | Doris 每年存储 | Doris 压缩收益 |
| --- | --- | --- | --- | --- |
| CUD | 1,000 org | ~18 GB | ~5 GB | ~3.6x |
| CRUD | 1,000 org | ~1.8 TB | ~0.5 TB | ~3.6x |

### 方案适用性

双方均以月分区为前提：

| 场景 | PG 方案 | Doris 方案 |
| --- | --- | --- |
| **CUD, ≤ 1,000 org**（V1 当前范围） | ✅ 月分区后无压力，团队运维熟练 | ✅ 但改动面大，V1 优先选 PG |
| **CRUD, 1,000 org**（若读操作全量接入） | ⚠️ 月分区后索引/vacuum 可控，但存储 ~1.8TB/年，行存代价持续存在 | ✅ 列存压缩 ~0.5TB/年，append-only 零维护 |

### 关键分歧

CUD-only 时 PG 和 Doris 的差距主要是运维审美问题——PG 的 B-tree 索引和 autovacuum 在几千万行级别完全成熟，团队也熟练。

CRUD（加入读操作）之后，**月分区解决了 PG 的索引和 vacuum 问题**，剩余差距收窄为**存储成本和分区管理体验**：
- 存储：PG 行存 ~1.8TB/年 vs Doris 列存 ~0.5TB/年（物理差异，分区无法改变）
- 管理：PG 需手动建 12+ 个分区/年 vs Doris `AUTO PARTITION BY RANGE` 一行声明

这也是当前方案设计保持两套并行的原因：**PG 方案最小改动落地，Doris 方案作为量级上升时的自然演进路径，两者共享同一套应用层代码和查询接口。**

---

## 技术方案

所有技术设计详见 [03-plan-pg.md](./03-plan-pg.md)、[04-detail-pg.md](./04-detail-pg.md)、[03-plan-doris.md](./03-plan-doris.md)、[04-detail-doris.md](./04-detail-doris.md)。当前评审偏向是：先落 PG，再根据真实规模决定是否引入 Doris。
