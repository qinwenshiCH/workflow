# 设计决策记录

> 按主题分类，只保留最终确认的决策。历史讨论中被覆盖的废弃思路已按 [spec 开发指挥台](/) 纪律清理。

## 方向变更（2026-07-02）

**定位从"活动日志"重构为"审计日志"，完全替代当前方案。**

- 目标从内部排障变为**第三方审计合规**，支持导出审计日志
- 全局单表 `audit_log` 作为统一逻辑模型，废弃 `meta.activity_log` / `global.activity_log` 分表方案
- 只记录**站外流量**（source ∈ {ui, api_token, mcp}），internal/scheduler/backfill 不写入
- 记录所有实体的**管理面操作（created/updated/deleted）** + 登录/登出/登录失败事件
- **不干涉各业务内部状态流转**（AB 状态变更、Metric 口径变更、MA Campaign 启动/暂停等属于产品功能）
- 账号活跃字段方案废弃，登录/登出/登录失败事件直接走审计日志

## 方案并行（2026-07-06）

- 为适配 Wave 当前代码与运维现状，**同时保留 PostgreSQL 与 Doris 两套候选技术方案**
- 文档拆分为**概要设计 + 详细设计**两层：`03-plan-pg.md / 04-detail-pg.md` 与 `03-plan-doris.md / 04-detail-doris.md`
- `03-plan-pg.md`：PG 异步批量落库，优先满足第三方审计导出与查询解释性
- `03-plan-doris.md`：Doris 异步攒批落库，优先满足长期存储成本与压缩比
- 在明确真实数据量、导出频率、运维约束前，spec 阶段**暂不锁定最终存储引擎**
- 两套方案共享同一条产品约束：**主流程必须异步，不因审计阻塞日常操作**
- 但”异步”不等于”可静默丢失”：channel 满时必须非阻塞丢弃 + error 日志 + drop counter
- 当前评审偏向：**先上 PG**；Doris 保留为规模化 / 成本优化备选
- Doris 若要作为主存落地，需要在 `service/auditlog/` 中自建专注的 Stream Load 客户端（支持 Bearer Auth、stable label、幂等判定）
- 如果未来确有长期保留成本压力，优先考虑 **PG 主存 + Doris 镜像**，而不是 day-1 直接 Doris 单写

## 范围（新 — 审计日志）

- 审计日志记录所有实体的**管理面操作（created/updated/deleted）**，不干涉各业务内部状态流转
- 指标、事件、属性归入**元数据对象**，适用统一审计规范，按计划分批接入
- AB 和 Metric 必须纳入；事件/属性等元数据对象适用同一模型
- Dashboard 的 Chart 关联/移除记在 Dashboard 的审计记录中，相关对象列表放 `detail.extra.chart_ids`
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

## Doris 候选方案（2026-07-03）

**引入 Apache Doris 作为候选审计存储方案，写入方式改为显式调用。**

- Doris 是 Wave 现有的基础设施（`dorisx` 包），无需引入新组件
- Doris 的自动月分区和列存压缩仍然成立，但它是成本优化价值，不是当前 V1 首要价值
- ZSTD 列存压缩在 CUD 场景下有 ~3.6x 优势（详见 01-spec.md §规模假设），非早期估算的 7x
- Stream Load HTTP PUT JSON 异步攒批写入，不阻塞主流程
- 去掉了 GORM 插件（~450 行）和 changes diff 引擎，改为 ~150 行显式 `audit.Log()` 调用
- Detail 收敛为版本化的 `schema_version / account / target / comment / extra` JSON envelope，不记录 field-level changes
- 写入策略统一为 async enqueue + background batch flush，主流程不等待最终落库
- 详见 [03-plan-doris.md](./03-plan-doris.md) 与 [04-detail-doris.md](./04-detail-doris.md)

## 架构（新 — 审计日志）

- 全局单表 `global.audit_log`，废弃 `meta.activity_log` 和 `global.activity_log` 分表方案
- 所有审计事件统一写入审计日志主表，按 `domain + feature` 区分实体类型层级
- `org_id` / `project_id` 均可 NULL（NULL = 无对应层级）
  - 账号层事件：`org_id = NULL, project_id = NULL`（登录/登出、API Token 管理）
  - 组织层事件：`org_id = NOT NULL, project_id = NULL`（成员管理、组织设置变更）
  - 项目层事件：`org_id = NOT NULL, project_id = NOT NULL`（Chart/Dashboard/AB 等 created/updated/deleted）
- PG 方案 V1 可先单表；是否按 `occurred_at` 月分区，等真实数据量证明需要时再加
- Append-only，不可修改/删除
- `occurred_at` 记录事件实际发生时间，`created_at` 记录入库时间；异步场景下两者可能不一致，查询默认按 `occurred_at` 排序

## 架构（旧 — 已废弃）

- ~~`meta.activity_log`（meta schema）是项目内对象的标准落盘表；global item 走 `global.activity_log`~~
- ~~组织/项目级管理操作基于 `global.activity_log`，不入 `meta.activity_log`~~
- ~~`global.activity_log` 不是 org 专用表：scope 不通过冗余列表达，由 `item_type + item_id` 隐式推导~~
- ~~V1 不在官方产品新增通用活动查看入口；仅保留 AB / Metric 既有查看能力，其余走 OP / 内部接口~~
- ~~不承诺统一查询层；op_operation_log / global.activity_log / meta.activity_log 三套独立存储~~
- ~~查询接口保留 `page / page_size / total` 模型，V1 不引入 cursor-only~~
- ~~V1 不做分区；索引 `(item_type, item_id, occurred_at DESC)`~~

## 枚举与数据模型

- 实体分类使用 `domain + feature` 两列替代单一 `scope`：
  - `domain`：粗粒度领域（`account` / `organization` / `project` / `asset` / `metadata`，共 5 个）
  - `feature`：细粒度实体类型（共 25 个，每个 domain 下 1:1 或 1:N 映射）
- 完整 domain/feature 清单（25 个 entity），对齐 Wave 内部领域模型：

  | domain | feature | 对应实体 |
  | --- | --- | --- |
  | `account` | `session` | Account（登录/登出/登录失败） |
  | `account` | `account_setting` | Account（密码/邮箱变更） |
  | `account` | `api_token` | AccountAPIToken |
  | `organization` | `org_setting` | Organization |
  | `organization` | `org_member` | OrganizationMember |
  | `organization` | `org_member_invitation` | OrganizationInvite |
  | `project` | `project_setting` | Project |
  | `project` | `project_member` | ProjectMember |
  | `asset` | `chart` | Chart |
  | `asset` | `dashboard` | Dashboard |
  | `asset` | `cohort` | Cohort |
  | `asset` | `pipeline` | Pipeline |
  | `asset` | `tracking_plan` | TrackingPlan |
  | `asset` | `experiment` | Experiment |
  | `asset` | `feature_gate` | FeatureGate |
  | `asset` | `feature_config` | FeatureConfig |
  | `asset` | `layer` | Layer |
  | `asset` | `holdout` | Holdout |
  | `asset` | `target` | Target |
  | `metadata` | `metric` | Metric |
  | `metadata` | `tracked_event` | TrackedEvent |
  | `metadata` | `virtual_event` | VirtualEvent |
  | `metadata` | `event_property` | EventProperty |
  | `metadata` | `user_property` | UserProperty |
  | `metadata` | `virtual_property` | VirtualProperty |

- `domain` 使用 `VARCHAR(64)`，5 个 domain 名最长 12（organization），64 留足余量
- `feature` 使用 `VARCHAR(64)`，25 个 feature 名最长 18（org_member_invitation）
- `action` 使用 `VARCHAR(64)`，基础动作集锁定为 `created / updated / deleted / logged_in / logged_out / login_failed`
  - 不含 read / view 等读操作
  - 不含 copy（V1 不做，后续按需接入）
  - 不含各业务专用 action（状态流转等属于产品功能，不走审计日志）
- `source` 存储于审计日志表（`VARCHAR(16)`），区分 `ui` / `api_token` / `mcp`
- 逻辑表名定稿为 `audit_log`；PG 物理表为 `global.audit_log`，Doris 物理表为 `sw_dw_global.audit_log`
- `account_id` 不设外键；`login_failed` 在无法解析账号时允许为空（Doris 可用 `0` 表示未知账号）
- `target_id` 使用 `VARCHAR(64)`，不限制格式（支持 UUID / 自增 ID / 字符串标识）；BIGSERIAL 最大 19 位、UUID 固定 36 位，64 足够
- `ip_address` 使用 `VARCHAR(64)`，NOT NULL（合规刚需）
- `event_id` 作为稳定事件标识保留，用于导出对账与回放去重
- `detail` 统一存储过滤后的 JSON 字符串；PG 用 `TEXT`，Doris 用 `STRING`
- `detail` 固定为版本化 envelope：`schema_version / account / target / comment / extra`；`account.name` 为 best effort，不存邮箱；`changes[]` 仅作为未来扩展保留，不是 P0 必选
- PG V1 只保留 `project_time / org_time / account_time` 三类高频索引；`target_id` 的 scoped 复合索引后置到真实高频对象回查场景

## Data Model（新 — 审计日志）

- `global.audit_log` 表结构：

  | 字段 | 类型 | 约束 | 说明 |
  | ------ | ------ | ------ | ------ |
  | `id` | BIGSERIAL | PK | 自增主键 |
  | `org_id` | BIGINT | NULL | 组织 ID（账号层事件为 NULL） |
  | `project_id` | BIGINT | NULL | 项目 ID（组织/账号层事件为 NULL） |
  | `account_id` | BIGINT | NULL | 操作人 ID；失败登录等无法解析时可为空 |
  | `action` | VARCHAR(64) | NOT NULL | created / updated / deleted / logged_in / logged_out / login_failed |
  | `domain` | VARCHAR(64) | NOT NULL | 粗粒度领域，如 account / project / asset |
  | `feature` | VARCHAR(64) | NOT NULL | 细粒度实体类型，如 session / chart / experiment |
  | `target_id` | VARCHAR(64) | NULL | 资源 ID（登录事件为 NULL） |
  | `source` | VARCHAR(16) | NOT NULL, DEFAULT 'ui' | 来源：ui / api_token / mcp |
  | `ip_address` | VARCHAR(64) | NOT NULL | 操作者 IP |
  | `event_id` | VARCHAR(64) | NOT NULL | 稳定事件标识，用于对账与去重 |
  | `detail` | TEXT / STRING | NULL | 结构化审计详情（schema_version / account / target / comment / extra） |
  | `occurred_at` | TIMESTAMPTZ | NOT NULL | 事件发生时间，由调用方传入 |
  | `created_at` | TIMESTAMPTZ | NOT NULL, default now() | 入库时间，异步场景下可能晚于 `occurred_at` |

- PG 方案 V1 不分区；Doris 方案保留自动月分区
- `detail` 类型两方案统一用 `TEXT`（PG 原生 `TEXT`，Doris 4.x 中 `TEXT` 等价于 `STRING`；Wave 现有 Doris DDL 也统一用 `TEXT`）
- Detail 结构以对象摘要 envelope 为主，不强制 field-level diff，也不为此额外查库
- V1 索引只服务 `org/project + time range` 与 `account + time range` 高频查询，不为 `target_id` 额外建专用复合索引
- V1 不冗余 `actor_name` / `target_name` / `request_id` / `trace_id` 顶层列
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

- 写入策略统一为 **async enqueue + background flush**，主流程不等待最终落库
- 不设 blocking 模式；性能优先是硬约束
- channel 满时非阻塞 enqueue，满则丢弃并记 +1 drop counter + error 日志；不设本地 spool
- 批量写入上限 500 行/批
- 同一批操作共享信息在写入层组装，不强制调用方传 correlation_id
- `source` 表达的是**接入通道**，不是认证方式：gin 默认 `ui`，Account API Token 覆盖为 `api_token`，MCP 固定为 `mcp`
- 为了稳定表达 `mcp`，需要新增独立 `audit_source` 上下文字段；不能只靠 `pvctx.IsAccountAPIToken(ctx)` 推导
- **MCP 入口**：MCP 协议不走标准 gin middleware，除鉴权字段外还需显式注入 `client_ip` 与 `audit_source = mcp`
- 优雅重启时必须先停流量、再 drain audit writer、最后关闭数据库连接；异常崩溃仍存在有限内存丢失窗口

### 写入分工（新 — 显式调用 + 异步 writer）

- Handler / Service 在业务成功后显式调用 `audit.Log(...)`
- 登录 / 登出 / 登录失败在 `apps/web/controller/account/account.go` 写入，不放在认证 filter
- 批量操作允许写 1 条汇总审计记录，批量对象明细放 `detail.extra`
- V1 不再以 GORM 全局 callback 作为主方案，避免跨 `globaldb` / `metadb` / batch 场景语义过重

### 写入分工（旧 — 已废弃）

- ~~通过 GORM 全局 callback 机制自动捕获 CRUD，业务 service 和 handler 零修改~~
- ~~create / update / delete 由 GORM 回调统一拦截~~
- ~~注册表：以 DB 表名映射到 domain / feature 组合~~
- ~~Create / Delete / Copy 在 handler 层写入，Update 在 service 层写入~~
- ~~助手函数收口到 `auditlog.Helper`~~

## 旧值与 Detail 构造（新 — 审计日志）

V1 不再以通用 diff 引擎为前提，Detail 构造遵循以下规则：

- `detail` 顶层固定为 `schema_version / account / target / comment / extra`
- `create`：记录过滤后的当前对象 `snapshot`
- `update`：记录过滤后的 after `snapshot`，不额外查询 before/after 做全量 diff
- `delete`：记录删除前最小 `snapshot`，至少保留 `id / name`
- `batch` 操作：受影响对象列表写入 `detail.extra`
- 不为导出可读性在写入时冗余 actor 摘要；默认在读侧按 `account_id` 补当前名称
- 若采用 `detail.account`，则 `id` 优先、`name` best effort；不为补 `name` 额外查库，也不写邮箱
- **调用方不传敏感字段**：入口层不设脱敏过滤，不依赖运行时脱敏
- 如后续确有第三方审计需求要求字段级变化，可在 `detail.changes[]` 中追加，不改主表结构

## PG 落地约束（2026-07-07）

- PG 方案的现网 rollout 以 `script/migration/scripts/global_v*.sql` 为准，不能只改 `script/sql/pgsql/global.sql`
- `global.sql` 仍需同步更新，但它只作为全新环境 bootstrap 快照，不承担现网增量变更职责
- Wave 当前 SQL migration 运行在事务中，因此新增 `audit_log` 时使用普通 `CREATE INDEX IF NOT EXISTS`，不使用 `CREATE INDEX CONCURRENTLY`

## 旧值与 Detail 构造（旧 — 已废弃）

- ~~Create/Delete/Update 三种场景各有标准代码模板，时序不同，不共用同一个模板~~
- ~~新增接入点时可选的 `WithWriteActivity[T]` 泛型模板~~
- ~~部署时增加 write-only feature flag，异常时快速关闭写入~~

## 迁移（新 — 审计日志）

- V1 为全新系统，**不做历史迁移**
- 旧 AB `details.operation_records`、`meta.metric_define_history` 保留不动
- `meta.asset_behavior`、`global.op_operation_log` 保留不动
- 审计日志上线后，新操作开始写入审计日志主表（PG 或 Doris）
- 历史数据由后续项目决定是否迁移

## 边界与异常处理（新 — 审计日志）

- 单条 detail 保持大小预算 64KB，超限截断并记录 warning
- 同一对象高频操作每条独立记录，不合并去重
- 审计日志不可修改、不可删除（Append-only）
- 分区管理：超出保留期限的分区直接 DROP 或 detach 归档
- 未注册的 domain + feature 组合在写入入口直接拒绝
- 缺少 IP 的站外流量视为非法审计输入：不写审计行、记 critical 告警，但不回滚已成功的业务操作

## 读取路径

- 查询与导出默认使用 `account_id`；PG 可直接 JOIN `global.account` 补当前名称，Doris 可按 `account_id` 应用层补齐；默认不导出邮箱
- 管理面查询（组织管理员、项目管理员）按层级范围过滤
- 官方产品不默认展示审计日志（类 PostHog premium feature）
- 导出格式：CSV / Excel，应用当前过滤条件，通过 OpenAPI 提供
- V1 不提供前端查看页面，仅通过 OpenAPI 导出
- 分页使用 **cursor** 模式，采用 `(occurred_at, id|event_id)` 复合游标，避免同时间戳下漏数/重数
- 返回结构含 `next_cursor` 和 `has_more`，支持前端"加载更多"和导出循环

## 交付顺序（新 — 审计日志）

- **Phase 0（底座）**：补齐 `03-plan-pg.md` / `03-plan-doris.md`，统一异步 writer 约束
- **Phase 1（全部接入）**：一期一次性接入全部 5 domain × 25 feature 的管理面操作，覆盖 account(session/account_setting/api_token)、organization(org_setting/org_member/org_member_invitation)、project(project_setting/project_member)、asset(chart/dashboard/cohort/pipeline/tracking_plan/experiment/feature_gate/feature_config/layer/holdout/target)、metadata(metric/tracked_event/virtual_event/event_property/user_property/virtual_property)
- **Phase 2（导出）**：OpenAPI 导出接口（CSV / Excel），可作为独立二期上线

## Detail 结构统一（2026-07-07）

**确认采用 Doris 方案带 actor 快照的 detail 结构，两方案统一该模型。**

- detail 顶层统一为 `schema_version / account / target / comment / extra`（无 `changes`）
- `account`：操作时的 actor 快照（`id` + `name`），不是当前名
- `target`：被操作资源的过滤后摘要（`id` + `name` + `type` + 其他业务字段）
- `comment`：可选，只放调用点天然已知的信息
- `extra`：批量对象列表或事件专属扩展
- `schema_version` 当前固定为 `1`，后续 add-only 扩展
- V1 不做 `changes[]` 字段级 diff 引擎
- 两方案的 detail JSON key 对齐，确保 PG ↔ Doris 之间数据天然可迁移
- 裁剪策略和调用方不传敏感字段约定见 [04-detail-pg.md](./04-detail-pg.md)

## 账号名补齐策略（2026-07-07）

**两方案统一：`account_id` 是主审计证据，detail 中的 `account.name` 只是 best effort 的当时名快照；PG 读侧额外补当前名作为辅助信息。**

- Doris 方案：detail 自带 `account.id`，`account.name` 拿得到就写，不需要为补名额外查库
- PG 方案：写入时 detail 存 `account.id` + best effort `account.name`；读取时可额外通过 JOIN `global.account` 补充当前名，但这只是辅助信息，不能当成历史快照

## Client IP 与反向代理（2026-07-07）

- gin 审计请求的 `ip_address` 来源为反向代理透传的 `X-Real-IP`，通过 `c.ClientIP()` 获取
- MCP 审计请求的 `ip_address` 在 net/http 入口读取 `X-Real-IP`（反向代理透传），降级读 `RemoteAddr`
- V1 **不配置全局 `TrustedProxies`**（影响所有 gin 路由，超出审计范围）
- 文档说明此限制：无 TrustedProxies 时 IP 在某些场景下可能为容器 IP，V2 按需引入
- 配置细节见 [04-detail-pg.md §4.5](./04-detail-pg.md)

## Client IP 修正（2026-07-07）← 替代旧版 APISIX 描述

- Wave 项目不存在 APISIX 组件；所有 docker-compose 使用 host 网络
- IP 获取就是简单的 `c.ClientIP()`，反向代理透传 `X-Real-IP`
- 文档中删去所有 "APISIX" 字样，统一为"反向代理透传"

## OrganizationFilter 扩大注入 org_id（2026-07-07）

- 移除 ``if !IsAccountAPIToken`` guard，OrganizationFilter 对所有接口执行 org_id 提取
- 已确认现有代码中：组织级接口从 body/query 提取，项目级接口可通过 `GetOrgIDByProjectCached`（有缓存）获取，账号/OP 接口自然跳过
- 具体实现见 [04-detail-pg.md §4.4](./04-detail-pg.md)

## Doris DDL bootstrap 方案（2026-07-07）

- `sw_dw_global.audit_log` DDL 不通过 `DBTypeDorisGlobal` 迁移框架执行
- 改用 `initDatabase()` 中新增 `doris_global.sql` embed，在 `dorisx.Init()` 后执行 bootstrap
- 原因：现有 `DBTypeDoris` 迁移是每项目循环（per-project），对全局数据库无相关基础设施
- 详见 [04-detail-doris.md §2.3](./04-detail-doris.md)

## Handler 命名规则（2026-07-07）

- 文档中的 controller/handler 名称以真实 OpenAPI 生成的代码为准，不使用简写或虚构名
- 例如：使用 `LoginAccount` / `AddNewChart` / `PostAbCreate`，而非 `CreateChart` / `UpdateExperiment`
- Metadata 域 controller 位于 `controller/metadata/`，非 `controller/event/` 或 `controller/property/`
- 完整映射表见 [04-detail-pg.md §10](./04-detail-pg.md)

## 裁剪策略（2026-07-07）

- 单条 detail (JSON 序列化后) 大小上限：64KB
- 超出 64KB 时直接丢弃整个 detail（`detail_payload = NULL`），comment 设为 `"detail exceeds 64KB, discarded"`，打 warn 日志
- 审计行本身正常写入，不因 detail 超限丢失整条
- 裁剪在 enqueue 时完成，不阻塞主流程
- **决策理由**：V1 简单性优先于 detail 完整性。合规审计只需要"谁在何时做了什么"，detail 为空不影响行级追溯。后续如有细化需求可补充逐级裁剪。
- 详细说明见 [04-detail-pg.md §5.4](./04-detail-pg.md)

## Doris 方案补充决策（2026-07-07，修正 2026-07-08）

针对第三方审核发现的缺口，以下决策立即生效：

- **`occurred_at` 字段**：Doris 表结构加 `occurred_at` 列，DUPLICATE KEY 第一列改为 `occurred_at`
- **DUPLICATE KEY**：调整为 `(occurred_at, org_id, project_id, account_id, event_id)`：`occurred_at` 确保时间分区剪枝，scope 列加速范围查询；`event_id` 放末位使 ORDER BY 无需额外排序，也支持按 event_id 对账查询
- **`event_id` 字段**：Doris 表结构加 `event_id` 列；Stream Load 使用稳定 label（`audit_log_{first_event_id_prefix}_{last_event_id_prefix}_{batch_size}`）实现幂等
- **登录审计写入位置**：确认在 `controller/account`（LoginAccount/LogoutAccount 成功返回前、LoginValidate 错误路径），不在 session 认证 filter
- **source 补充 mcp**：枚举值新增 `mcp`；MCP 入口鉴权完成后显式赋值 `source = mcp`
- **一期全量接入**：不拆 Phase 1/2/3，一期一次性接入全部 25 feature
- **DDL 精度对齐 Wave 规范**：使用 `DATETIME(3)` 而非 `DATETIME(6)`，与 Wave 现有 Doris DDL（`doris.sql`）一致
- **Doris Stream Load 客户端：自建，不修 dead code**：`stream_loader.go` 是 ID-Merge 时期遗留的死代码（零生产调用），不在此之上修补。改为在 `service/auditlog/doris_stream.go` 中写一份专注的 Stream Load 客户端，只包含审计写入需要的能力。认证头使用 Wave Stream Load 真实约定（`Bearer <base64(user:pass)>`），非 `doris_apix.go` 的 Query API Basic Auth 模式。标准 header：`Expect: 100-continue`、`strip_outer_array: true`、`format: json`
- **Doris 不设本地 spool（同 PG 方案）**：写入失败时非阻塞丢弃 + drop counter + error 日志，不 spill 到磁盘。原因：Wave 代码库中无任何 spool 基础设施（`grep -rn "spool"` 全库零结果），完整建设需 1-2 天，与 PG 方案的 no-spool 原则对齐
- **Phase 0 前置验证**：在 dev 环境用 curl 验证 Stream Load Bearer Auth、label 幂等、AUTO PARTITION（需 Doris ≥ 2.1.0）后再进入实现；未通过则 Doris 方案暂停（详见 [04-detail-doris.md §12](./04-detail-doris.md)）

## PG 索引维持（2026-07-07）

- **PG 索引维持 3 个**：`(project_id, occurred_at)`, `(org_id, occurred_at)`, `(account_id, occurred_at WHERE account_id IS NOT NULL)`，暂不增减

## OrgID 传递方案（2026-07-07）

- `audit.Log()` 签名不加 `org_id` / `project_id` 参数，保持 `(ctx, domain, feature, action, targetID, detail)`
- `org_id` 和 `project_id` 在 `audit.Log()` 内部从 `pvctx` 提取，拿不到时写入 NULL
- 调用方在调用 `audit.Log()` 前确保 `pvctx` 中已注入 org_id（详见 [04-detail-pg.md §4.4](./04-detail-pg.md)）
- pvctx 扩展见 [04-detail-pg.md §5.3](./04-detail-pg.md)：新增 `ClientIP()` / `WithClientIP()` / `AuditSource()` / `WithAuditSource()`；`BackGroundCtx` 补充复制 `client_ip` / `audit_source` / `org_id`

## Doris 查询与 detail 约束（2026-07-08）

- **新增 `dorisx.UseGlobalDB()`**：Doris 全局表查询需不拼 `sw_dw_{pid}` 的查询方法。新增 `UseGlobalDB()` 返回 `globalDB`，不改 `UseDB()` 行为。只加一行，不影响既有 per-project 查询
- **Doris detail 最大字节数（与 PG 对齐）**：Doris `STRING` 类型默认 ~1 MB、最大 ~2 GB，无硬限压力。V1 统一设为 64000（与 PG 64KB 对齐），按需可调的合理预算。

## 安全与性能审查结果（2026-07-07）

基于对 PG 方案的全面安全与性能审查，以下决策记录入档：

- **IP 地址限制已文档化**：在 [04-detail-pg.md §9.1](./04-detail-pg.md) 风险表及告警中明确说明 V1 无 TrustedProxies 白名单的局限
- **防篡改列入 V2**：对 `audit_log` 表施加 TRIGGER/RLS/独立 DB 用户防篡改保护，列入 V2 规划。V1 优先保证功能通路和安全基线（access control + scope 隔离），不因防篡改延迟交付
- **event_id 不单独建索引**：event_id 非业务查询入口，独立索引无查询收益。V2 可替换现有 `(project_id, occurred_at DESC)` 为 `(project_id, event_id DESC)`，利用 UUID v7 时序性消除冗余排序
- **连接池隔离（V1）**：PGWriter 复用 globaldb 连接池，不创建独立池。分布式下多副本 × 独立池会无谓增加 PG 连接数，且审计丢弃可接受，无需隔离。详见 [04-detail-pg.md §4.5](./04-detail-pg.md)
- **连接池隔离（V2）**：如审计流量明显影响业务 P99，再引入独立 `*sql.DB`；详见 [04-detail-pg.md §12](./04-detail-pg.md)
- **V2 规划**：新增 [04-detail-pg.md §12](./04-detail-pg.md) 记录防篡改、IP 可信、复合索引优化三项 V2 规划

## 12 项模型修订（2026-07-09）

基于 2026-07-09 讨论，对审计日志数据模型、API 签名和结构做 12 项修订。所有文档（spec/plan/detail-pg/decisions）同步更新。

1. **id 改为 VARCHAR UUID v7**：删除 `BIGSERIAL id` + `event_id VARCHAR(64)`，单列 `id VARCHAR(64) PRIMARY KEY`，使用 `pkg/lib/util.NewUUID()`（Google UUID v7）。PK 即稳定事件标识，UUID v7 嵌入 48-bit ms 时间戳，天然按时间排序，可用作游标。PG 无特殊 UUID v7 加速器，VARCHAR(64) 为 PG/Doris 兼容选择。

2. **游标改为基于 id**：`(occurred_at, event_id)` → `id`（UUID v7），ORDER BY id DESC + WHERE id < ?，格式 base64(id)。

3. **domain 取消 asset/metadata → 合并到 project**：5 domain → 3 domain（account/organization/project），所有 asset.* 和 metadata.* feature 移至 project.*

4. **virtual_property 拆分**：`project.virtual_property` → `project.user_virtual_property` + `project.event_virtual_property`，feature 总数 25 → 26。

5. **account_name / target_name 入表**：`account_name VARCHAR(128)` 和 `target_name VARCHAR(256)` 作为顶层列，均为事件发生时快照。account_name 从 pvctx.Aname() 取，所有链路必须都有值。API token 链路在 GetTokenInfoCached 中嵌入 account_name，缓存在 TokenInfo 中，缓存命中零额外成本。

6. **detail → extra，简化**：`detail TEXT` → `extra TEXT`，自由格式 JSON，业务方控制内容。删除结构化 envelope（schema_version/account/target/comment/extra）。超 2KB 直接截断至前 2KB + warn 日志。marshal 失败 → error 日志 + 跳过事件。

7. **删除 created_at**：occurred_at 是唯一时间戳。

8. **source 枚举扩展**：`ui` → `web`，`api_token` → `openapi`，保留 `mcp`，新增 `agent`。检测矩阵覆盖 5 条入口路径，wagent 路由通过 `/api/wagent/` 前缀区分。

9. **audit.Log() 改为 struct 参数**：`Log(ctx, Entry{Domain, Feature, Action, TargetID, TargetName, Extra})`，Target 摘 要拆为 TargetID 和 TargetName 两个顶层列。

10. **domain/feature/action 为枚举类型**：`type Domain/Feature/Action string` 编译时常量，消除 runtime registry 验证。

11. **extra 大小预算 64KB → 2KB**：正常事件 < 100B，2KB 充足。

12. **channel 满时非阻塞丢弃**：select { case ch <- e: default: }，永不阻塞主流程，满时 drop counter +1 + error 日志。Wave 已有相同模式（pkg/qm/async_batch_writer.go:104-110）。
