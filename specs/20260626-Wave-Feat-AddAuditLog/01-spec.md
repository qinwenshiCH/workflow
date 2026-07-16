# 功能规格：Wave 审计日志

**目录**: `20260626-Wave-Feat-AddAuditLog`
**创建日期**: 2026-06-26
**最后更新**: 2026-07-13（实现收口：source、extra、调用点、事务与可靠性语义）
**状态**: 评审中
**技术方案**: [03-plan.md](./03-plan.md) + [04-detail-pg.md](./04-detail-pg.md)（当前推荐） / [04-detail-doris.md](./04-detail-doris.md)（备选）

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
2. **仅站外流量**：只记录客户主动发起的操作（web / openapi / mcp），内部流量不记
3. **管理面聚焦**：记录所有实体的管理面操作（CUD），不追踪内部状态流转
4. **全局单表**：统一逻辑模型，不再分散落在各业务表
5. **自由格式 extra**：业务方控制内容；审计基础设施只把它当 text，不承诺统一结构或可解析 JSON

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
| 内部流量（空 source / internal，包括 scheduler、backfill、cron 等） | 非客户主动操作 |
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
    id              VARCHAR(64) PRIMARY KEY, -- UUID v7，嵌入 ms 时间戳，可用作游标
    org_id          BIGINT,                 -- NULL = 账号层事件
    project_id      BIGINT,                 -- NULL = 组织/账号层事件
    account_id      BIGINT,                 -- 操作人；无法解析时可为空
    account_name    VARCHAR(128),           -- 事件发生时操作人名称快照（非当前名），best effort
    target_id       VARCHAR(64),            -- NULL = 登录事件等无资源场景
    target_name     VARCHAR(256),           -- 事件发生时被操作对象名称快照
    action          VARCHAR(64) NOT NULL,   -- created/updated/deleted/logged_in/logged_out/login_failed
    domain          VARCHAR(64) NOT NULL,   -- 粗粒度领域：account/organization/project
    feature         VARCHAR(64) NOT NULL,   -- 细粒度实体类型：session/account_setting/api_token/... 共 26 个
    source          VARCHAR(16) NOT NULL,  -- web / openapi / mcp
    ip_address      VARCHAR(64) NOT NULL,   -- 操作者 IP（合规刚需）
    extra           TEXT,                   -- 自由格式文本，业务方控制内容
    occurred_at     TIMESTAMPTZ NOT NULL    -- 事件发生时间，唯一时间戳
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

使用 `domain + feature` 两列替代单一 `scope`，3 个 domain × 26 个 entity。domain 对齐 Wave 内部领域模型：

| domain | feature | 对应实体 | 层级 | 操作 |
| --- | --- | --- | --- | --- |
| `account` | `session` | Account | 账号层 | logged_in / logged_out / login_failed |
| `account` | `account_setting` | Account（密码/邮箱变更） | 账号层 | updated |
| `account` | `api_token` | AccountAPIToken | 账号层 | created / updated / deleted |
| `organization` | `org_setting` | Organization | 组织层 | updated / deleted |
| `organization` | `org_member` | OrganizationMember | 组织层 | created / updated / deleted |
| `organization` | `org_member_invitation` | OrganizationInvite | 组织层 | created / deleted |
| `project` | `project_setting` | Project | 项目层 | created / updated / deleted |
| `project` | `project_member` | ProjectMember | 项目层 | created / updated / deleted |
| `project` | `chart` | Chart | 项目层 | created / updated / deleted |
| `project` | `dashboard` | Dashboard | 项目层 | created / updated / deleted |
| `project` | `cohort` | Cohort | 项目层 | created / updated / deleted |
| `project` | `pipeline` | Pipeline | 项目层 | created / updated / deleted |
| `project` | `tracking_plan` | TrackingPlan | 项目层 | created / updated / deleted |
| `project` | `experiment` | Experiment | 项目层 | created / updated / deleted |
| `project` | `feature_gate` | FeatureGate | 项目层 | created / updated / deleted |
| `project` | `feature_config` | FeatureConfig | 项目层 | created / updated / deleted |
| `project` | `layer` | Layer | 项目层 | created / updated / deleted |
| `project` | `holdout` | Holdout | 项目层 | created / updated / deleted |
| `project` | `target` | Target | 项目层 | created / updated / deleted |
| `project` | `metric` | Metric | 项目层 | created / updated / deleted |
| `project` | `tracked_event` | TrackedEvent | 项目层 | created / updated / deleted |
| `project` | `virtual_event` | VirtualEvent | 项目层 | created / updated / deleted |
| `project` | `event_property` | EventProperty | 项目层 | created / updated / deleted |
| `project` | `user_property` | UserProperty | 项目层 | created / updated / deleted |
| `project` | `user_virtual_property` | UserVirtualProperty | 项目层 | created / updated / deleted |
| `project` | `event_virtual_property` | EventVirtualProperty | 项目层 | created / updated / deleted |

### Extra 内容与大小预算

V1 的 `extra` 完全归业务方控制，无统一 envelope、`schema_version` 或公共字段。审计基础设施只将序列化结果当作 text；发生截断后不承诺仍是合法 JSON。

- 由调用方传入 `Entry.Extra map[string]any`，`audit.Log` 内部 JSON 序列化后写入 `extra` 列
- 大小预算由 `audit_log_extra_max_bytes` 配置控制，默认 **2048 字节**；配置为合法正数时可以调低或调高，不再额外施加 2KB 硬上限
- 超限时按 UTF-8 字节预算截断文本，并记录 warn + `audit_extra_truncated_total`
- JSON marshal 失败时记录 error + `audit_extra_marshal_error_total`，丢弃 extra 但保留核心审计事件
- **调用方不传敏感字段**：构造 extra 时自行排除密码、token 等敏感内容，入口层不设脱敏过滤
- 默认不传 extra；核心列已表达的 target、action、actor、source、IP、时间不在 extra 重复记录。只有同一 coarse action 需要区分有意义的业务变化时才补最小字段，例如 API Token 的 metadata/status/rotation

API Token 的 `updated` 使用最小业务字段区分语义：metadata 修改记录 `update_type=metadata` 与实际 `changed_fields`；启用/禁用记录 `from_status/to_status`；轮换以新 record ID 为 target，并只记录 `previous_target_id`。任何场景都不得写 token 原文、hash 或 hint。

### 敏感信息与业务侧脱敏

- `account_id` 保留内部稳定 ID；`account_name` 只允许使用上下文中的 `Account.Name` 快照，不得把 email/mobile 放入该字段；audit 基础设施不做运行时脱敏
- 登录失败由 Account 业务入口调用 `pkg/lib/ulog.MaskEmail` 或 `MaskString`，只允许保存 `identity_type` 和脱敏后的 `identity_hint`，不得保存用户输入原文
- 成员邀请由 Organization 业务代码先脱敏邮箱再传入；密码变更只记录变化类型，不保存 password/salt/hash/reset code
- API Token、Authorization、Cookie、OAuth code/refresh token、secret/private key 均不得出现在核心列或 extra
- audit 基础设施不理解业务字段，也不对 `target_name`、`extra` 或上下文字段做统一脱敏；敏感字段排除与脱敏均由调用方负责

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
- PG V1 维持 3 个高频索引不动，不新增也不删减；Doris 使用 `(occurred_at, org_id, project_id, account_id, id)` 作为 DUPLICATE KEY

---

## 写入

1. Handler/Service 在持久化变化确定成功后显式调用 `audit.LogAfterCommit(ctx, Entry{Domain, Feature, Action, TargetID, TargetName, Extra})`；登录等无事务事件可以调用 `audit.Log`
2. 从 pvctx 提取 org_id/project_id/account_id/account_name/source/client_ip
3. 直接保存上下文中的 `Account.Name` 快照；业务调用方保证所有传入字段已排除敏感内容；Entry.Extra 序列化后按配置预算截断为 text
4. 非阻塞 enqueue 到 channel，满则丢弃 + drop counter + error 日志（不阻塞主流程）
5. 后台 flush worker 批次写入 `global.audit_log`
6. 写入失败重试耗尽后按实际丢失行数增加 drop counter，不设本地 spool；shutdown 使用有截止时间的 context drain 剩余队列

**写入约束**：
- 可持久化 source 仅为 `{web, openapi, mcp}`。空 source 与 `internal` 等价，表示内部调用并静默跳过；内部定时任务无需为了审计逐处补 source
- 显式未知 source（包括旧的 agent/scheduler/backfill 字符串）拒绝入队并告警，防止拼写或过期约定静默绕过
- 公共 Web middleware 注入 `web`，Account API Token middleware 覆盖为 `openapi`，MCP handler 注入 `mcp`；Wagent 控制面请求继承 Web source，但 Wagent tool 重新请求 `/api/mcp` 后触发的业务变更保存为 `mcp`
- 用户操作内部派生的写入使用 `WithAuditSuppressed`，避免项目初始化、级联删除、Target 派生 Pipeline、AB 曝光属性等重复记账
- 事务内调用必须传 tx context；事务提交才 enqueue，回滚不 enqueue。事务外 `LogAfterCommit` 立即 enqueue
- 缺少 client_ip 时记 critical 告警，不回滚业务操作
- Entry.Domain / Feature / Action 使用编译时常量，并在入口 registry 校验完整三元组；未注册组合拒绝写入
- 批量操作按实际成功 target 一对象一条日志；幂等 create、无实际变化 update 和失败对象不记录
- `account_id` 保留稳定内部标识，`account_name` 只允许来自 `Account.Name`；业务调用方不得传入 raw email/mobile/password/token/hash/hint/code/secret

## 读取

查询接口 `List(ctx, req)` 和导出 `Export(ctx, req)` 共享 scope 校验：

| 规则 | 说明 |
|------|------|
| scope 约束 | `OrgID / ProjectID / AccountID` 至少一个必填（防全表扫） |
| 时间范围 | `StartTime / EndTime` 必填 |
| 分页 | **cursor** 模式：基于 `id`（UUID v7），格式 `base64(id)`；返回 `{items, next_cursor, has_more}` |
| 排序 | `id DESC`；ID 正常路径为 UUIDv7，与事件生成时间有序，并以 `id < cursor` 稳定翻页 |
| 导出 | CSV / Excel，通过 OpenAPI 提供，V1 不提供前端查看页面 |

---

## 边界与异常处理

- **单条 extra 大小预算**：由 `audit_log_extra_max_bytes` 控制，默认 2KB；超限截断为 text + warn/metric
- **同对象高频操作**：每条独立记录，不合并去重
- **缺少 IP 的请求**：仍保留核心审计事件，同时记录 critical 告警与 `audit_missing_ip_total`；不回滚已成功的业务操作
- **不传敏感字段**：调用方对所有业务字段自行排除或提前脱敏，入口层不设脱敏过滤，不依赖运行时脱敏
- **未注册的 domain + feature + action 组合**：写入入口直接拒绝
- **Append-only**：不可修改、不可删除
- **批量写入**：由 `audit_log_batch_size` 配置控制（默认 100）；channel 满时非阻塞 enqueue，满则按条增加 drop counter + error 日志，不设本地 spool
- **IP 地址获取**：gin 路由依赖反向代理透传的 `X-Real-IP`（`c.ClientIP()`）；MCP 路由在 `net/http` 入口读取 `X-Real-IP`。V1 不配置全局 `TrustedProxies`，文档说明 IP 可能不准确
- **CSV 导出**：所有文本 cell 必须防止 formula injection；以 `= + - @` 开头（含前导空白）的值按纯文本转义
- **分区管理**：超出保留期限的分区 DROP 或 detach 归档

---

## 需求

### 功能需求

- **FR-001**: 系统 MUST 提供统一审计日志存储；PG 方案为 `global.audit_log`，Doris 方案为 `sw_dw_global.audit_log`
- **FR-002**: 系统 MUST 记录所有 domain + feature 的管理面操作（created/updated/deleted + logged_in/logged_out/login_failed），覆盖 3 个 domain × 26 个实体、74 个合法 domain + feature + action 组合
- **FR-003**: 系统 MUST 仅记录站外流量（source ∈ {web, openapi, mcp}）；空 source/internal 表示内部流量且不写入
- **FR-004**: 审计日志 MUST 包含 `id`、`org_id`、`project_id`、`account_id`、`account_name`、`target_id`、`target_name`、`action`、`domain`、`feature`、`source`、`ip_address`、`extra`、`occurred_at`
- **FR-005**: 系统 MUST 在写入入口校验 domain + feature + action 合法性，未注册组合拒绝写入
- **FR-006**: 系统 MUST 提供按时间范围、组织、项目、domain、feature、target_id、操作人、action 过滤的查询接口
- **FR-007**: 系统 MUST 支持通过 OpenAPI 导出 CSV / Excel 格式的审计日志
- **FR-008**: 写入策略 MUST 为异步；主流程只负责 enqueue，不等待最终落库完成
- **FR-009**: 审计日志 MUST 为 append-only，不可修改、不可删除
- **FR-010**: 登录/登出/登录失败事件 MUST 写入审计日志，domain=`account`、feature=`session`，action=`logged_in` / `logged_out` / `login_failed`

### 非功能需求

- **NFR-001**: 主流程审计附加开销 P99 < 5ms（仅含构造 entry + enqueue）
- **NFR-002**: 常规审计查询（`org_id|project_id + time range`，可选带 `account_id/domain/feature/action/target_id` 过滤）P99 < 1s
- **NFR-003**: 单条 extra 大小预算由配置控制，默认 2KB；超限按 UTF-8 字节预算截断为 text
- **NFR-004**: 必须有明确保留策略；Doris 可用自动月分区，PG 在数据规模证明需要前可保持单表
- **NFR-005**: 导出默认包含 `account_id` 与事件发生时已脱敏的 `account_name` 快照，不导出邮箱
- **NFR-006**: channel 满时 MUST 非阻塞 enqueue，满则每丢失一条增加 drop counter；批量 flush 最终失败按实际丢失行数增加 counter；不设本地 spool
- **NFR-007**: MUST 暴露 `audit_channel_drop_total` 一个 counter 指标，用于监控丢弃事件

---

## 成功标准

- **SC-001**: registry 与调用点清单覆盖 3 domain × 26 feature、74 个合法组合；其中 `org_member_invitation.deleted`、`layer.updated` 当前无产品入口，显式标记为 `no-entry`，其余 72 个组合均有真实调用点
- **SC-002**: 仅站外流量写入，内部流量验证不进表
- **SC-003**: 主流程审计附加开销 P99 < 5ms；最终落库异步完成
- **SC-004**: 按 `org/project + time range` 的常规审计查询与导出分页 P99 < 1s；单对象历史查询在该范围内可完成筛选
- **SC-005**: 支持 CSV/Excel 导出
- **SC-006**: 保留 / 归档策略可运维；当选择 Doris 时自动月分区可用

## 实现调用点与验收闭环

仓库内权威清单为 `docs/auditlog/instrumentation.md`，逐项记录 74 个组合的业务入口、target 语义、批量/幂等/事务边界、extra 使用和两个 `no-entry`。该文档必须与 registry 同步变更。

实现验收至少包含：

1. `TestCompliance_RegistryAndInstrumentationDocumentAreComplete`：registry 必须恰好 74 个组合，文档不得漏项或多项。
2. source 测试：仅 web/openapi/mcp 入库；空值/internal 静默跳过；未知显式值拒绝。
3. `LogAfterCommit` 测试：commit enqueue、rollback 不 enqueue、嵌套事务只在外层 commit 后触发。
4. writer 测试：channel full、批量重试、最终失败按行计数、shutdown deadline 和 drain 不重复 flush。
5. 调用点测试：成功记录、失败不记录、无变化不记录、批量只记录实际成功 target、派生写入 suppression。
6. 敏感数据测试：account name 与登录 identity 脱敏；extra 中不出现密码、token、hash、hint、code、secret 或原始邀请邮箱。

## 规模假设

基于 Wave 平台特征（每个 org 数十个活跃用户，管理面操作为低频），且按月分区为前提：

| 场景 | 每 org 日均事件数 | 1,000 org/年 |
|------|-------------------|-------------|
| **CUD**（CUD + 登录） | ~50 | ~18M 行 / ~18 GB（PG）|
| **CRUD**（CRUD + 登录） | ~5,000 | ~1.8B 行 / ~1.8 TB（PG）|

规模估算、方案对比、存储决策详见 [03-plan.md §4](./03-plan.md#4-存储方案)。
