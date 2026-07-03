# 设计决策记录

> 按主题分类，只保留最终确认的决策。历史讨论中被覆盖的废弃思路已按 [spec 开发指挥台](/) 纪律清理。

## 方向变更（2026-07-02）

**定位从"活动日志"重构为"审计日志"，完全替代当前方案。**

- 目标从内部排障变为**第三方审计合规**，支持导出审计日志
- 全局单表 `global.audit_log`，按时间分区，废弃 `meta.activity_log` / `global.activity_log` 分表方案
- 只记录**站外流量**（source ∈ {ui, api_token}），internal/scheduler/backfill 不写入
- 记录所有实体的**管理面操作（created/updated/deleted）** + 登录/登出/登录失败事件
- **不干涉各业务内部状态流转**（AB 状态变更、Metric 口径变更、MA Campaign 启动/暂停等属于产品功能）
- 账号活跃字段方案废弃，登录/登出/登录失败事件直接走审计日志

## 范围（新 — 审计日志）

- 审计日志记录所有实体的**管理面操作（created/updated/deleted）**，不干涉各业务内部状态流转
- 指标、事件、属性归入**元数据对象**，适用统一审计规范，按计划分批接入
- AB 和 Metric 必须纳入；事件/属性等元数据对象适用同一模型
- Dashboard 的 Chart 关联/移除记在 Dashboard 的审计记录中（`changes[]` 体现 `chart_ids` diff）
- Pipeline CRUD 接入审计；内部 Process / callback 不进入审计表
- MA Campaign CRUD 接入审计；状态流转（Launch/Pause/Resume/Finish）属于产品功能，不入审计
- PROJECT_MEMBER 确认纳入 V1，与 org member 独立（独立的权限授予操作）
- 邀请流程：邀请建在自有表上，接受邀请后触发审计记录；邀请本身不落审计
- AB target pipeline 状态同步 V1 不做审计

## 范围（旧 — 已废弃）

- ~~活动日志定位为项目内对象的统一活动规范与落盘基础设施~~
- ~~V1 边界分三层：项目内对象活动（主线）、global item 活动（独立表）、账号活跃字段（account 表）~~
- ~~V1 最直接需求价值是内部排障/根因定位，不是对外售卖的企业合规功能~~
- ~~V1 技术方案收敛为最小闭环：写入、落库、查询、迁移~~
- ~~AB 内部冲突解决必须写入活动，source = "internal"~~
- ~~Cohort 调度任务生命周期不单独成行，在 CRUD 活动行的 snapshot 中体现~~
- ~~Cohort 定时重算执行（RunCohortJob cron 回调）不进入活动表~~
- ~~MA Campaign 状态流转通过独立 action_type 表达~~

## 存储引擎变更（2026-07-03）

**审计日志存储从 PostgreSQL 切换到 Apache Doris，写入方式从 GORM 插件自动捕获改为显式调用。**

- Doris 是 Wave 现有的基础设施（`dorisx` 包），无需引入新组件
- DUPLICATE KEY + AUTO PARTITION BY RANGE 按月分区，零运维成本
- ZSTD 列存压缩，预计存储量仅为 PG 的 1/7（~50GB/年 vs ~360GB/年）
- Stream Load HTTP PUT JSON 异步攒批写入，不阻塞主流程
- 去掉了 GORM 插件（~450 行）和 changes diff 引擎，改为 ~150 行显式 `audit.Log()` 调用
- Detail 简化为 `{name, comment}`，不再记录 field-level changes
- 写入策略从 blocking 改为 async channel + ticker（5s/100条），与业界标准对齐（PostHog/CloudTrail/GitHub 均用异步）
- 详见 [plan-doris.md](./plan-doris.md)

## 架构（新 — 审计日志）

- 全局单表 `global.audit_log`，废弃 `meta.activity_log` 和 `global.activity_log` 分表方案
- 所有审计事件统一写入 `global.audit_log`，按 `domain + feature` 区分实体类型层级
- `org_id` / `project_id` 均可 NULL（NULL = 无对应层级）
  - 账号层事件：`org_id = NULL, project_id = NULL`（登录/登出、API Token 管理）
  - 组织层事件：`org_id = NOT NULL, project_id = NULL`（成员管理、组织设置变更）
  - 项目层事件：`org_id = NOT NULL, project_id = NOT NULL`（Chart/Dashboard/AB 等 created/updated/deleted）
- 按 `created_at` 范围分区（推荐按月），BIGSERIAL 主键配合分区键
- Append-only，不可修改/删除

## 架构（旧 — 已废弃）

- ~~`meta.activity_log`（meta schema）是项目内对象的标准落盘表；global item 走 `global.activity_log`~~
- ~~组织/项目级管理操作基于 `global.activity_log`，不入 `meta.activity_log`~~
- ~~`global.activity_log` 不是 org 专用表：scope 不通过冗余列表达，由 `item_type + item_id` 隐式推导~~
- ~~V1 不在官方产品新增通用活动查看入口；仅保留 AB / Metric 既有查看能力，其余走 OP / 内部接口~~
- ~~不承诺统一查询层；op_operation_log / global.activity_log / meta.activity_log 三套独立存储~~
- ~~查询接口保留 `page / page_size / total` 模型，V1 不引入 cursor-only~~
- ~~V1 不做分区；索引 `(item_type, item_id, occurred_at DESC)`~~
- ~~`occurred_at` = 事件时间，`created_at` = 入库时间，两者语义明确区分~~
- ~~新增操作人索引 `(operator_id, occurred_at DESC)`~~

## 枚举与数据模型

- 实体分类使用 `domain + feature` 两列替代单一 `scope`：
  - `domain`：粗粒度领域（`account` / `organization` / `project` / `asset` / `metadata`，共 5 个）
  - `feature`：细粒度实体类型（共 22 个，每个 domain 下 1:1 或 1:N 映射）
- 完整 domain/feature 清单（22 个 entity），对齐 Wave 内部领域模型：

  | domain | feature | 对应实体 |
  | --- | --- | --- |
  | `account` | `session` | Account（登录/登出/登录失败） |
  | `account` | `account_profile` | Account（密码/个人信息修改） |
  | `account` | `token` | AccountAPIToken |
  | `organization` | `org` | Organization |
  | `organization` | `org_member` | OrganizationMember |
  | `organization` | `org_member_invite` | OrganizationInvite |
  | `project` | `project` | Project |
  | `project` | `project_member` | ProjectMember |
  | `asset` | `chart` | Chart |
  | `asset` | `dashboard` | Dashboard |
  | `asset` | `cohort` | Cohort |
  | `asset` | `pipeline` | Pipeline |
  | `asset` | `campaign` | Campaign |
  | `asset` | `experiment` | Experiment |
  | `asset` | `feature_gate` | FeatureGate |
  | `asset` | `feature_config` | FeatureConfig |
  | `metadata` | `metric` | Metric |
  | `metadata` | `tracked_event` | TrackedEvent |
  | `metadata` | `virtual_event` | VirtualEvent |
  | `metadata` | `event_property` | EventProperty |
  | `metadata` | `user_property` | UserProperty |
  | `metadata` | `virtual_property` | VirtualProperty |

- `action` 使用全小写字符串，基础动作集锁定为 `created / updated / deleted / logged_in / logged_out / login_failed`
  - 不含 read / view 等读操作
  - 不含 copy（V1 不做，后续按需接入）
  - 不含各业务专用 action（状态流转等属于产品功能，不走审计日志）
- `source` 存储于审计日志表（`VARCHAR(16)`），区分 `ui` / `api_token`
- 表名定稿为 `global.audit_log`
- `account_id` 不设外键，参考 PostHog 方式查询时 JOIN `global.account` 获取用户信息
- `target_id` 使用 `VARCHAR(72)`，不限制格式（支持 UUID / 自增 ID / 字符串标识）
- `ip_address` 使用 `VARCHAR(64)`，NOT NULL（合规刚需）
- `detail` 列类型锁定为 **TEXT**（存储 JSON，不使用 PG JSONB）
- `detail` 内容包含 `{name: string, changes: Change[]}` 结构，参考 PostHog change 模型

## Data Model（新 — 审计日志）

- `global.audit_log` 表结构：

  | 字段 | 类型 | 约束 | 说明 |
  | ------ | ------ | ------ | ------ |
  | `id` | BIGSERIAL | PK | 自增主键 |
  | `org_id` | INT | NULL | 组织 ID（账号层事件为 NULL） |
  | `project_id` | INT | NULL | 项目 ID（组织/账号层事件为 NULL） |
  | `account_id` | INT | NOT NULL | 操作人 ID，查询时 JOIN account |
  | `action` | VARCHAR(64) | NOT NULL | created / updated / deleted / logged_in / logged_out / login_failed |
  | `domain` | VARCHAR(64) | NOT NULL | 粗粒度领域，如 account / project / asset |
  | `feature` | VARCHAR(64) | NOT NULL | 细粒度实体类型，如 session / chart / experiment |
  | `target_id` | VARCHAR(72) | NULL | 资源 ID（登录事件为 NULL） |
  | `source` | VARCHAR(16) | NOT NULL, DEFAULT 'ui' | 来源：ui / api_token |
  | `ip_address` | VARCHAR(64) | NOT NULL | 操作者 IP |
  | `detail` | TEXT | NULL | JSON 变更详情（PostHog change 模型） |
  | `created_at` | TIMESTAMPTZ | NOT NULL, default now() | 记录时间 |

- V1 不分区。待数据规模需要时按 `created_at` RANGE 按月分区
- `detail` 类型为 TEXT（存储 JSON），不使用 PG JSONB
- Detail 结构参考 PostHog：`{name: string, changes: [{field, action, before, after}]}`
- 不存 `item_name` / `account_name` 等快照列，查询时 JOIN 原表获取展示名
- IP 地址为必记字段（NOT NULL），合规刚需
- V1 不需要 `was_impersonated`、`is_system`、`client` 等字段

## Data Model（旧 — 已废弃）

- ~~`detail_payload` 列类型锁定为 TEXT，不使用 PG JSONB~~
- ~~`operator_name` 保留并作为展示快照~~
- ~~`item_name` 保留并作为展示快照~~
- ~~V1 不记录 IP 地址~~
- ~~Account 活跃字段为 3 个 TIMESTAMPTZ NULL 列加在 global.account 表~~
- ~~`correlation_id` 替代 operation_group_id~~

## 写入与一致性（新 — 审计日志）

- 写入策略为 blocking，审计日志写入失败直接返回 error（合规场景不可静默丢失）
- 不设 WritePolicy 多等级，不存在 best_effort
- 批量写入上限 500 行/批
- 同一批操作共享信息在写入层组装，不强制调用方传 correlation_id
- **MCP 入口**：MCP 协议不走标准 HTTP middleware，在 MCP handler 中独立完成 source 注入（认证后手动将 `source = api_token` 写入 context），下游 service 统一处理

### 写入分工（新 — GORM 审计插件）

通过 GORM 全局 callback 机制自动捕获 CRUD，**业务 service 和 handler 零修改**：

| 操作 | 捕获机制 | 说明 |
| --- | --- | --- |
| create | GORM `AfterCreate` 回调 | 模型已回填 ID/CreatedAt，插件自动提取字段值写 audit_log |
| update | GORM `BeforeUpdate` 读旧值 + `AfterUpdate` diff | 同一事务中先读取当前值，UPDATE 后再读新值，`ChangesBetween` 自动 diff |
| delete | GORM `AfterDelete` 回调 | DELETE 已执行但内存模型仍可读，提取 name 写审计 |
| logged_in / logged_out | 认证 filter 显式调用 | 登录/登出不涉及 DB 操作，GORM 无法捕获，在认证层写入 |

注册表：以 DB 表名映射到 domain/feature 组合，启动时注入 DB 实例。

详细实现见 [plan.md §3.3](../specs/20260626-Wave-Feat-AddAuditLog/plan-audit-log.md#33-gorm-审计插件自动捕获-crud)。

### 写入分工（旧 — 已废弃）

- ~~Create / Delete / Copy 在 handler 层写入，Update 在 service 层写入~~
- ~~助手函数收口到 `auditlog.Helper`~~

## 旧值与 Detail 构造（新 — 审计日志）

CRUD 由 GORM 审计插件自动捕获（见上），Detail 构造遵循以下规则：

- 旧值来源：插件在 `BeforeUpdate` 中 clone session 从 DB SELECT 读取（`readFieldsFromDB`），不依赖业务层传值
- 新值来源：插件在 `AfterUpdate` 中 clone session 从 DB SELECT 读取（UPDATE 已执行，DB 是新状态）
- 投影函数 + ChangesBetween diff 算法保留，参考 PostHog `changes_between()`
- Change 结构锁定为 PostHog 模型：`{field: string, action: "created"|"changed"|"deleted", before: any, after: any}`
- 敏感字段掩盖保留，替换值为 `"masked"`（在 ChangesBetween 之后、序列化之前执行）
- 排除字段体系保留（通用排除 `{id,created_at,updated_at,version,is_deleted,deleted_at,created_by,updated_by}` + 按 feature 排除）
- Detail 结构：`{name: string, changes: Change[]}`，不含 short_id / type / trigger / context 等附加字段
- 登录/登出事件：在认证 filter 中显式写入，detail.changes 为空（非 DB 操作，GORM 无法捕获）

## 旧值与 Detail 构造（旧 — 已废弃）

- ~~Create/Delete/Update 三种场景各有标准代码模板，时序不同，不共用同一个模板~~
- ~~新增接入点时可选的 `WithWriteActivity[T]` 泛型模板~~
- ~~部署时增加 write-only feature flag，异常时快速关闭写入~~

## 迁移（新 — 审计日志）

- V1 为全新系统，**不做历史迁移**
- 旧 AB `details.operation_records`、`meta.metric_define_history` 保留不动
- `meta.asset_behavior`、`global.op_operation_log` 保留不动
- 审计日志上线后，新操作开始写入 `global.audit_log`
- 历史数据由后续项目决定是否迁移

## 边界与异常处理（新 — 审计日志）

- 单条 detail 保持大小预算 64KB，超限截断并记录 warning
- 同一对象高频操作每条独立记录，不合并去重
- 审计日志不可修改、不可删除（Append-only）
- 分区管理：超出保留期限的分区直接 DROP 或 detach 归档
- 未注册的 domain + feature 组合在写入入口直接拒绝
- 不记录 IP 地址为 NULL 的操作（缺少 IP 的站外流量视为不合法请求）

## 读取路径

- 查询时以 `account_id` JOIN `global.account` 获取用户信息（name / email），平铺为 `account_name` / `account_email`
- 管理面查询（组织管理员、项目管理员）按层级范围过滤
- 官方产品不默认展示审计日志（类 PostHog premium feature）
- 导出格式：CSV / Excel，应用当前过滤条件，通过 OpenAPI 提供
- V1 不提供前端查看页面，仅通过 OpenAPI 导出
- 分页使用 **cursor** 模式（`WHERE created_at < ? ORDER BY created_at DESC LIMIT ?`），避免审计导出数据漂移
- 返回结构含 `next_cursor` 和 `has_more`，支持前端"加载更多"和导出循环

## 交付顺序（新 — 审计日志）

- **Phase 0（底座）**：Doris 建表 + audit 包（Log/writer/query）+ 磁盘 fallback
- **Phase 1（高价值对象接入）**：domain=account(session)、organization(org/org_member/org_member_invite)、project(project/project_member)、asset(chart/dashboard/cohort)
- **Phase 2（长尾对象接入）**：domain=account(account_profile/token)、asset(experiment/feature_gate/feature_config/pipeline/campaign)、metadata(metric)
- **Phase 3（元数据补齐）**：domain=metadata(tracked_event/virtual_event/event_property/user_property/virtual_property)
- **Phase 4（导出）**：OpenAPI 导出接口（CSV / Excel）
- 交付只影响底座基础，后续 phase 可独立上线
