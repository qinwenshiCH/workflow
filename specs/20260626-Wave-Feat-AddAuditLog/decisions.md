# 设计决策记录

> 按分类整理的决策记录，包含讨论期间的探索性思路和已确认的最终结论。

## 范围 & 优先级

- 2026-06-26: 活动日志定位调整为**项目内对象的统一活动规范与落盘基础设施**，不再沿用"资产"作为顶层概念
- 2026-06-26: 指标、事件、属性归入**元数据对象**，不算资产；但同样属于项目内对象活动规范的适用范围
- 2026-06-26: V1 的边界分三层：**项目内对象活动**是主线，**组织/项目级管理操作活动**也必须存在但允许独立落盘，**账号登录/登出/活跃时间**落在 `account` 表或等价账号主表
- 2026-06-26: 当前 `asset` 基础设施仍不完善，只能作为接入现状参考，不能直接当产品范围真相：`AssetOperator` 只注册了 Chart / Dashboard，接口也仅覆盖 CRUD
- 2026-06-26: AB 和 Metric 必须纳入同一套项目内对象活动规范；事件/属性等元数据对象也适用同一模型，允许按实施计划分批接入
- 2026-06-26: 当前最明确、最直接的需求价值是**内部排查问题 / 根因定位**，而不是先把活动当作对外售卖的企业合规功能
- 2026-06-26: 方案评审时要同时用 4 个价值点审视设计：事故止损、组织放权、成交门槛、治理基础设施；但 V1 优先级以事故止损为第一位
- 2026-06-26: 用户故事按 P0（核心）→ P1（重要）→ P2（增强）分级；P2 当前承接企业信任 / 安全问询类延伸价值，但不作为 V1 上线前置
- 2026-06-30: **AB 内部冲突解决**（internalOfflineFF / internalDeleteFF）必须写入活动记录，`source = "internal"`，`operator` 继承触发冲突的原始操作人。这是真实的状态变更，排障时必须可追溯。
- 2026-06-30: **Cohort 调度任务**的生命周期操作（create/update/delete job）不单独成行，作为 cohort CRUD 活动行的 `extra` 附加上下文记录。
- 2026-06-30: **Cohort 定时重算执行**（RunCohortJob cron 回调）不进入活动表。这是系统运维操作，每天自动执行，记录它会稀释真正的用户操作。若未来需要追踪计算历史，应作为 cohort 自身的运行日志。
- 2026-06-30: Dashboard 的 Chart 关联/移除操作记在 Dashboard 的活动记录中（`changes[]` 体现 `chart_ids` diff），被操作的 Chart 自身不产生额外活动行。
- 2026-06-30: Pipeline CRUD（create / update / delete / stop）接入项目对象活动。Pipeline 现有的 `exec_info` 和 `pipeline_batch_export_run` 是执行跟踪，不替代操作活动。
- 2026-06-30: Pipeline 内部 Process / callback 不进入活动表，属于系统运维操作。
- 2026-06-30: AB target pipeline 状态同步（pipeline callback）V1 不做活动，后续可考虑加入。

## 架构 & 技术选型

- 2026-06-26: `meta.activity_log`（meta schema）是**项目内对象**的标准落盘表，但活动体系不要求所有场景都共用这一张表
- 2026-06-26: 组织 / 项目级管理操作可以基于现有 `global.op_operation_log` 演进，或设计新的 global 级记录表；不强行并入 `meta.activity_log`
- 2026-06-26: 活动一致性等级（强一致性 vs best-effort）重新打开，需在方案评审中显式定夺；`LogWithFallback` 仅作为候选方案，不视为最终结论
- 2026-06-26: detail 采用**活动域自有的版本化载荷**，结构化 `changes: [{field, action, before, after}]` 优先，必要时允许 `extra` / `snapshot` 补充；不使用 JSONB，不直接持久化现有业务结构体（来源：PostHog 研究 D-01 + 本轮会话确认）
- 2026-06-26: V1 的查询路径和索引策略优先服务**单对象变更链路排查**，不优先按操作人或全局分析视角优化
- 2026-06-26: 活动模型需要为未来企业信任与治理能力预留扩展点（如敏感字段掩盖、可见性控制、保留策略、审批/告警），但这些不作为 V1 上线前置条件
- 2026-06-29: V1 不在官方产品中新增通用活动查看入口；仅保留 AB / Metric 既有查看能力，其他活动查看能力优先通过 OP / 内部接口提供
- 2026-06-29: 为了方便内部审查与排障，可以在 OP 暴露对象活动查询接口，但不要求同步建设页面
- 2026-06-30: **一致性等级**：调用方在每个 `Log`/`BatchLog` 调用时通过 `consistency` 参数自行决定。API 提供三种等级 `Strong` / `Core` / `BestEffort`。不设系统级一刀切策略。建议：删除/发布/高风险操作用 `Strong`，常规 CRUD 用 `Core`，内部操作/回填用 `BestEffort`。
- 2026-06-30: **BatchLog 原子性**：同一业务事务内的多条活动记录通过 `BatchLog` 一次性写入。任一记录的核心字段写入失败 → 整体失败 → 业务事务回滚。
- 2026-06-30: **活动保留策略**：V1 不实现自动清理，预留 `created_at` 作为未来分区键；按月份分区为推荐扩展路径。
- 2026-06-30: `detail_payload` 使用 BYTEA + LZ4 压缩——LZ4 压缩后是二进制数据，BYTEA 是自然选择。不使用 JSONB，因为 V1 查询只按 (item_type, item_id) 过滤，不在 detail 上做搜索。未来如需全文检索 detail，需评估从 TEXT / JSONB + GIN 索引迁移的成本。
- 2026-06-30: `meta.activity_log` 部署在 project schema（meta），`project_id` 列冗余，不设。索引为 `(item_type, item_id, created_at DESC)`。
- 2026-06-30: **枚举规范最终定义**：
  - `ItemType` 使用 UPPER_SNAKE_CASE 字符串，共 13 个值（含 PIPELINE）
  - `action_type` 使用全小写描述性字符串，所有常量**统一定义在 `activity/types.go`**（守口到活动模块）。新增 action_type 需在此文件加 const 并 PR review 确认语义无重叠。
  - 内部操作（冲突解决）的 action_type 与正常操作保持一致，用 `source="internal"` 区分。
  - `source` 使用全小写字符串，共 4 个值：`web` / `openapi` / `internal` / `backfill`
- 2026-06-30: **表名定稿为 `meta.activity_log`**（在 project schema 内，`project_` 前缀冗余）。
- 2026-06-30: **两期交付策略**：第一期（Phase 0+1）交付项目对象活动底座（meta.activity_log + activity 模块 + 对象 live-write + 历史迁移 + OP 查询），独立可验证。第二期（Phase 2+3）交付 metadata 长尾 + account/org/project 管理活动。两期分别 commit，但都在本 spec 内完成。
- 2026-07-01: **基础能力方案重新打开评审，不宣称已最终锁定**。当前需重新收敛的一组核心设计包括：一致性模型、action_type 体系、payload 物理格式、批量/跨对象关联模型、脱敏职责边界。
- 2026-07-01: **action_type 先提供基础动作集，再允许扩展自定义动作**。基础集合优先保持精简（如 `create` / `update` / `delete` / `copy`），对象域如确有高价值语义，可在活动模块统一注册扩展 action_type；当前不锁死最终全集。

## 边界 & 异常处理

- 2026-06-26: 单条 detail 序列化后最大 64KB（可配置），超出时优先 LZ4 压缩（复用 `pkg/lib/util/compress.go`）；压缩后仍超则逐字段截断并记录警告（D-09）
- 2026-06-26: 同一对象高频操作每条独立记录，不合并去重
- 2026-06-26: AB / Metric / Wave 项目内历史操作记录需要复制进新活动规范；升级后新操作只写新活动表，不要求双写；旧字段或旧表保留不删（D-10，本轮会话确认）
- 2026-07-01: `last_active_at` 的 Redis 节流若不可用，**不降级为每次请求写 DB**。改为跳过本次刷新并记录 warning / 指标，避免在故障态放大数据库写压力。

## 数据模型

- 2026-06-26: 活动域使用自有 `ItemType` 体系，不直接复用 `def.AssetType`
- 2026-06-26: V1 不做分区，主索引 `(item_type, item_id, created_at DESC)`，按需后续补充（D-04/D-06）
- 2026-06-26: action_type 精简为 create/update/delete/copy，状态变更通过 changes[] 字段级 changed 表达（D-03，与 PostHog 理念对齐）
- 2026-06-26: item_id 允许为 0（仅用于极少数项目内规范对象的特殊场景；组织/项目级管理操作优先独立记录）
- 2026-06-26: 当前不提供跨项目全局查询，由调用方自行跨 schema 查询
- 2026-06-26: V1 查询范围收敛为**按对象视角查看历史**，当前不要求按操作人维度检索，因此不承诺 `operator_id` 维度查询/索引

## 数据字段

- 2026-06-26: 保留 `operator_name` 字段，写入时快照——用户改名或删除后历史记录仍可追溯操作人（D-11）
- 2026-06-29: 保留 `item_name` 字段，作为对象名称的展示快照；它不是当前对象主表真相，而是为了删除后追溯和列表可读性
- 2026-06-29: `operator_name`、`item_name` 都按展示快照处理，不要求随主表数据变更回写历史
- 2026-06-26: 新增 `source` 字段，区分操作来源：web / openapi / internal / backfill（D-13）
- 2026-06-26: 不需要 `operation_group_id`，同一条记录天然是一组操作（D-12）
- 2026-06-26: 账号最近登录/登出/活跃时间不进入 `meta.activity_log`，而是作为账号活跃字段落在 `account` 表或等价账号主表
- 2026-06-29: `detail_version` 表示 `detail_payload` 的 schema version，不是业务对象版本号；V1 固定为 `1`，仅在 detail 解码语义发生不兼容变化时升级
- 2026-06-29: `created_at` 直接定义为操作时间 / 活动事件时间，而不是数据库插入时间；历史迁移时回填旧事件时间
- 2026-06-30: V1 不记录 IP 地址。排障场景 operator_id 已够用，不增加字段复杂度。
- 2026-06-30: **Account 活跃字段完整方案**：3 个 `TIMESTAMPTZ NULL` 列加在 `global.account` 表。详见 [plan-account.md](./plan-account.md)。

## 代码对齐

- 2026-06-30: LZ4 压缩复用 `pkg/lib/util/compress.go` 中的 `LZ4()`/`UnLZ4()` 函数，非 gzip 函数。
- 2026-06-30: Dashboard 的 `AddChartsToDashboard`/`RemoveChartsFromDashboard` 不单独产生 Chart 活动记录。关联变更只在 Dashboard 的 `update` 活动中体现。
- 2026-06-30: Cohort 的 `DeleteCohort` 接收 `*dto.CohortDeleteDTO`（仅含 ID 和 Name），delete 场景需要的规则快照（snapshot.rule_summary）需额外从 DB 读取。

## 其他

- 2026-06-29: 活动日志默认对官方产品用户不可见；V1 以 OP / 内部接口消费为主，页面不是前置要求
- 2026-06-30: **Member/生命周期活动不往 `op_operation_log` 塞**。新建 `global.global.activity_log`（增加 `org_id`/`project_id`，无 `customer_id`/`result_status`/`error_message`）。理由：`op_operation_log` 为 OP 设计，OP 未来可能独立拆分，member 数据不应随 OP 迁移。OP 端 9 项配置操作继续走 `op_operation_log` 不动。
- 2026-06-30: **ItemType 常量也统一定义在 `activity/types.go`**，与 action_type 同文件。
- 2026-06-30: **plan.md 拆分为 3 个**：`plan-object.md`（项目对象活动）、`plan-org.md`（组织/项目管理活动）、`plan-account.md`（账号活跃字段），三条链路独立演进、独立 commit。
- 2026-06-30: 同一事务中跨对象操作（如 CopyDashboard 同时复制 dashboard 和 charts）产生多条活动记录，通过 `BatchLog` 写入，共享事务。
- 2026-06-30: coverage-matrix.md 和 granularity-matrix.md 已合并入对应的 plan 文件后删除，避免内容重复。

## autoplan 审查（2026-06-30）

- 2026-06-30: **global.activity_log 的 action_type** 使用自有枚举（create/update/delete），不共享 meta.activity_log 的枚举。域操作语义由 item_type + detail_payload 承载，不设 object_action 字段。模块可注册新 item_type 来扩展，无需改 action_type。
- 2026-06-30: **新增操作人索引 idx_activity_operator**。支撑"按操作人反查组织操作"的排障路径。新增 DAO 方法 ListByOperator。
- 2026-06-30: **BatchInsert 上限 500 行**。超过时调用方自行分批，DAO 层不接受超过 500 行的参数。
- 2026-06-30: **增加 write-only feature flag**。部署方案增加 feature flag 保护，异常时可快速关闭活动写入而不影响业务逻辑。
- 2026-06-30: **detail 格式与 meta.activity_log 对齐**。global.activity_log 增加 detail_version + detail_payload（替代原 detail TEXT），V1 固定 detail_version=1。
- 2026-06-30: **一致性模型由调用方决定**。活动服务层提供 `Log`（强一致性）和 `LogWithFallback`（best-effort）两种方法，管理活动推荐 `LogWithFallback`，但不硬编码。detail_payload 大小约束对齐 meta.activity_log（64KB + LZ4）。

## autoplan scope 确认（2026-06-30）

- 2026-06-30: **PROJECT_MEMBER 确认纳入 V1**。与 org/member 独立，是独立的权限授予操作。
- 2026-06-30: **不承诺统一查询层**。op_operation_log / global.activity_log / meta.activity_log 三套独立存储，按需分别查询。
- 2026-06-30: **UpdateAccountProjectAuths 是一条 global.activity_log 记录**。记录 action_type=update, item_type=ORG_MEMBER，detail 含变更摘要。
- 2026-06-30: **邀请流程**：邀请建在自有表上，接受邀请后触发 global.activity_log 记录。邀请本身（创建/发送/撤回）不落活动。
- 2026-06-30: **OP 操作 vs 客户操作分界**：OP 人员在 OP 后台的操作走 op_operation_log；客户在业务系统中的操作走 global.activity_log。OP 未来在内部接口同时展示两类日志。
- 2026-06-30: **第一期受众为内部排障**。查询端点通过 OP 内部接口暴露，不对外。
