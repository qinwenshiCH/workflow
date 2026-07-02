# 功能规格：Wave 活动日志与账号活跃记录

**目录**: `20260626-Wave-Feat-AddAuditLog`
**创建日期**: 2026-06-26
**状态**: 评审中 — 基础能力规划中
**技术方案**: [plan-activity-log.md](./plan-activity-log.md)
**备选方案**: [plan-postgre-trigger.md](./plan-postgre-trigger.md)、[plan-pg-audit.md](./plan-pg-audit.md)
**评审入口**: [README.md](./README.md)
**输入**: "Wave 需要一个项目级的操作记录，存储到 PG，所有项目内对象的关键操作（创建/改/删）都要记录，方便追溯"

---

## 背景

现有几套分散的操作记录系统：

| 系统 | 位置 | 存储方式 | 范围 |
|------|------|---------|------|
| OP Console 操作记录 | `global.op_operation_log` | 独立表，before/after 快照 | 客户/组织/项目级运维操作 |
| AB 模块操作记录 | `ab_feature_flag.details` JSONB 内嵌 | 内嵌在 Feature Flag 行内 | AB 实验/Feature Flag 状态变更 |
| Metric 历史 | `meta.metric_define_history` | 独立表，old/new define | 指标口径变更 |

现状问题：
- AB 操作记录内嵌在 JSONB 中，无法独立查询、无法跨对象统一追溯
- Metric 有独立历史表，但与 AB / OP 等没有统一规范
- 缺少统一的**项目内活动记录规范**，不同对象各自演化
- Asset Behavior 表仅记录轻量操作（无快照），定位是分析而非操作记录

新系统定位为**以项目内对象活动为主线的统一活动基础设施**。

### 范围澄清

- **项目内对象** = 本规格的主线，写入 `meta.activity_log`，按 `item_type + item_id` 查看历史
- **组织/项目级管理操作** = 也必须有操作记录，走 `global.activity_log`
- **账号活跃信息** = 最近登录/登出/活跃时间，走 `global.account` 字段

当前 Wave 代码里有 Asset 域和 Metadata/Catalog 域两套分类，但 **asset 基础设施本身并不完善**，不能直接拿来当产品范围的真相来源（`AssetOperator` 只注册了 Chart/Dashboard，且仅覆盖 CRUD）。因此 V1 的范围不沿用 asset 作为顶层概念，而是使用活动域自有的对象模型。

### 三条可独立实施的链路

| 链路 | 目标 | 存储 |
|------|------|------|
| Project item activity | 记录项目内对象活动，支撑单对象排障 | `meta.activity_log` |
| Global item activity | 记录 web global schema item 活动 | `global.activity_log` |
| Account activity fields | 记录账号最近登录/登出/活跃时间 | `global.account` |

---

## 价值定位

当前最明确的直接价值是**内部排查问题时，能够快速回答 who / what / when / where**。V1 重点是把统一操作记录和内部可审查能力做好，而不是在官方产品中同步推出新的通用活动页面。

4 个价值点按优先级排序：

1. **事故止损/根因定位（P0）**：对象被改坏后，内部同学可沿单对象历史快速定位最近一次关键变更
2. **组织放权/权责清晰（P1）**：多人协作编辑时，事后可明确谁在什么时间做了什么改动
3. **成交门槛/企业信任（P1）**：采购评审时可证明敏感对象变更具备可追溯能力
4. **后续治理扩展（P2）**：未来若要做审批、告警、敏感操作规则，可以复用统一活动模型，但不进入 V1 主实现

### 设计原则

1. **Troubleshooting First**：V1 索引、字段取舍优先服务对象历史排查，不优先做分析型报表
2. **Attribution by Default**：默认保留 who / what / when / source / 必要变更详情
3. **V1 Small Core**：V1 只做写入、落库、查询、迁移闭环，不先做审计平台
4. **Extensible Later**：治理、审批、告警、保留策略等只保留扩展点，不作为当前交付内容
5. **Enterprise Traceability**：可追溯性本身就是独立价值，确保当客户问"你们能追溯谁改了什么吗"时，回答是"能"

---

## 用户场景总览

| 优先级 | 场景 | 验收焦点 | 对应方案 |
|--------|------|---------|----------|
| P0 | 项目内对象关键变更自动记录 | create/update/delete/copy 产生统一活动；detail 可解释字段差异 | [plan-activity-log.md](./plan-activity-log.md) §8 |
| P0 | 内部排障按对象还原变更链路 | `item_type + item_id` 分页查询，返回 `total` 和可读 detail | [plan-activity-log.md](./plan-activity-log.md) §7 |
| P1 | 多人协作责任链路 | 操作人、来源、事件时间、展示快照完整 | [plan-activity-log.md](./plan-activity-log.md) §3 |
| P1 | AB / Metric / Pipeline 历史债务收口 | 旧历史复制，新操作只写新活动表 | [plan-activity-log.md](./plan-activity-log.md) §11 |
| P1 | Global item 活动 | 组织/项目/成员/Account API Token 进入 `global.activity_log` | [plan-activity-log.md](./plan-activity-log.md) §9 |
| P1 | 账号最近登录/登出/活跃字段 | 三个时间字段落 `global.account` | [plan-activity-log.md](./plan-activity-log.md) §10 |
| P1 | 活动失败策略明确化 | 业务接入点通过 PolicyKey 声明 required/core/best-effort | [plan-activity-log.md](./plan-activity-log.md) §5.3 |
| P2 | 企业信任与安全问询 | V1 先提供可追溯基础，不承诺治理平台能力 | [plan-activity-log.md](./plan-activity-log.md) §4.4 |

<details>
<summary>完整用户故事和验收场景（点击展开）</summary>

### 用户角色

- **内部支持/研发/值班同学**：对象出问题后，需要第一时间定位是谁、在什么时候、从哪里改了什么
- **OP 审查/运维同学**：排障、核对变更、内部审查时，通过 OP 或内部接口查看操作记录
- **项目负责人/实验负责人/分析师**：多人协作编辑对象时，希望事后能明确责任链路
- **组织管理员/项目管理员**：需要保留组织与项目级配置变更的可追溯记录
- **产品 owner/安全评审对接人**：面对客户安全问询时，需要证明关键对象变更具备可追溯能力
- **平台工程师/系统设计者**：需要把分散的历史记录机制统一成稳定规范

### 用户故事 1 — 自动记录项目内对象关键变更（P0）

作为**项目负责人/实验负责人**，当团队成员对项目内对象执行创建、修改、删除、复制或状态变更时，系统自动留下统一操作记录，事后追溯不依赖个人记忆、聊天记录或零散日志。

**验收场景**：

1. **Given** 用户在项目内创建一个 `CHART`，**When** 创建成功，**Then** 活动日志中出现 `action_type = "create"` 的记录，detail 以结构化 `changes[]` 记录初始字段
2. **Given** 用户修改一个 `DASHBOARD` 的名称，**When** 保存成功，**Then** 活动日志中出现 `action_type = "update"` 的记录，detail 包含变更前后的字段差异
3. **Given** 用户更新一个 `METRIC` 的 define，**When** 提交保存，**Then** 活动日志中出现 `action_type = "update"` 的记录，detail 包含 define 的结构化变更
4. **Given** 用户删除一个对象，**When** 删除成功，**Then** 活动日志中出现 `action_type = "delete"` 的记录，detail 至少包含对象名称快照

### 用户故事 2 — 内部排障时按对象快速还原最近变更链路（P0）

作为**内部支持/研发/值班同学**，当某个实验、配置、图表或指标出问题时，通过统一查询接口按对象查看最近的变更历史，快速回答 who / what / when / source。

**验收场景**：

1. **Given** 项目中存在多个对象的活动日志，**When** 按 `item_type = CHART` 且 `item_id = 123` 查询，**Then** 仅返回该对象的日志
2. **Given** 某对象存在多条活动日志，**When** 按 `(occurred_at DESC, id DESC)` 查询，**Then** 最近的操作排在最前面，每条都包含操作人、时间、来源和必要的变更详情
3. **Given** 某对象的活动日志量超过一页，**When** 分页查询第 2 页，**Then** 返回第 2 页数据，`total` 字段正确标识总记录数
4. **Given** 同一对象的历史此前分散在旧机制中，**When** 完成历史复制后按对象查询，**Then** 不再需要同时访问多套历史存储
5. **Given** 官方产品没有新增通用活动页面，**When** 内部同学审查对象历史，**Then** 仍可通过 OP 或内部接口完成查询

### 用户故事 3 — 多人协作下明确责任链路并支持放权（P1）

作为**项目负责人/团队管理者**，当多人协作编辑实验、指标、配置时，能看清是谁做了哪次关键改动，敢于下放编辑权限。

**验收场景**：

1. **Given** 多位成员可编辑同一个对象，**When** 不同成员分别发起修改，**Then** 活动日志中可区分每次修改的 `operator_id`、`operator_name` 和 `source`
2. **Given** 项目负责人需追溯某次异常变更，**When** 查看对象历史，**Then** 可从记录中辨认责任链路
3. **Given** 某次修改来自内部任务、OpenAPI 或 MCP，**When** 查询对象历史，**Then** 可和人工 Web 操作区分开来

### 用户故事 4 — 非 CRUD 操作与历史债务统一收口（P1）

作为**平台工程师/系统设计者**，AB 状态流转、复制操作、Metric 口径变更以及已有的历史记录都被纳入同一套活动规范。

**验收场景**：

1. **Given** 一个 AB Feature Flag 从 DRAFT 切换到 DEBUG，**When** 触发状态变更，**Then** 活动日志记录 `action_type = "update"`，detail 包含状态字段 before/after
2. **Given** 一个 Experiment 被复制，**When** 执行复制操作，**Then** 活动日志记录 `action_type = "copy"`，detail 包含原始对象和新对象的关联信息
3. **Given** 一个 Metric Define 发生口径变更，**When** 执行更新，**Then** 活动日志记录 `action_type = "update"`，detail 包含口径字段 before/after
4. **Given** 已存在的 AB / Metric 历史操作记录，**When** 新系统上线并完成历史复制，**Then** 旧数据已复制到新活动表，升级后新操作只写新表，旧表保留不删

### 用户故事 5 — 组织/项目级管理操作记录（P1）

作为**组织管理员/项目管理员**，修改组织配置、项目配额、模板等管理动作同样具备可追溯记录。

**验收场景**：

1. **Given** 管理员添加成员到组织，**When** 添加成功，**Then** `global.activity_log` 出现 `action_type = "create"` 的记录
2. **Given** 管理员变更成员角色（Analyst → Admin），**When** 保存成功，**Then** `global.activity_log` 出现 `action_type = "update"` 的记录，含新旧角色
3. **Given** 管理员移除组织成员，**When** 操作成功，**Then** `global.activity_log` 出现 `action_type = "delete"` 的记录
4. **Given** 用户创建/删除组织或项目，**When** 操作成功，**Then** `global.activity_log` 出现对应 create/update/delete 记录
5. **Given** OP 管理员修改组织配置/项目配额（已有），**When** 保存成功，**Then** 继续沿用现有 `update_org_config` 记录

### 用户故事 6 — 面向安全问询与采购评审保留可追溯能力（P2）

作为**产品 owner/安全评审对接人**，当面对客户或内部安全团队"关键对象是谁改的"这类问题时，系统已具备稳定的基础活动模型。

**验收场景**：

1. **Given** 客户或内部安全团队询问某个关键对象最近是谁改过，**When** 查询对象历史，**Then** 可给出稳定、可追溯的变更记录
2. **Given** 后续需增加敏感字段掩盖或保留策略，**When** 在统一活动模型上扩展，**Then** 不需要推翻已有对象类型和 detail 结构

### 用户故事 7 — 活动失败策略明确化（P1）

作为**系统设计者/平台工程师**，活动日志写入失败时有明确、可验证的失败策略。

**验收场景**：

1. **Given** 技术方案选择 best-effort，**When** 活动表写入失败，**Then** 对象主操作仍成功返回，失败被记录到日志或告警
2. **Given** 某业务接入点注册为 required 策略，**When** 活动表写入失败，**Then** ActivityService 返回可识别错误，业务方按自身事务边界决定是否让主操作失败

</details>

---

## 边界情况

- **批量操作** → 用户批量删除 50 个 Chart，活动批量写入，避免逐条插入性能开销
- **超大变更详情** → 通过字段投影和大小预算控制 payload，超限截断并记录 warning；压缩不作为 V1 前置条件
- **同对象高频操作** → 每条独立记录，不合并去重
- **操作人或对象名变更** → 活动日志中的 operator_name / item_name 是写入时快照，历史不随改名更新
- **跨项目查询** → 活动表在 meta schema 下按项目隔离，V1 不提供跨项目全局查询
- **查看入口边界** → V1 不在官方产品新增通用活动查看页面；AB / Metric 保持既有查看能力
- **协作环境** → 多人同时操作同一对象，活动独立记录每条操作，无锁冲突
- **无对象 ID 的操作** → 允许 `item_id = 0` 并用 `item_type` 特殊值标识
- **账号级 global item** → Account API Token 没有天然 org/project 归属，写 `global.activity_log` 时使用 `account_id` scope

---

## 需求

### 功能需求

- **FR-001**: 系统 MUST 为项目内对象定义统一活动规范，并在 meta schema 下提供 `activity_log` 作为项目内对象的标准落盘表
- **FR-002**: 系统 MUST 提供 `ActivityService` 作为公共写入入口，支持 `WriteProjectItemLog` / `BatchWriteProjectItemLog` 和 `WriteGlobalItemLog` / `BatchWriteGlobalItemLog`；写入一致性等级由活动模块按已注册接入场景解析，不由调用方自由传入
- **FR-003**: 活动规范 MUST 使用活动域自有的 `ItemType` 体系，不直接沿用 `def.AssetType`
- **FR-003-bis**: 活动规范 MUST 先提供一组共享基础 `action_type`（V1 为 `create` / `update` / `delete` / `copy`）；扩展 `action_type` 必须在活动模块统一注册并经过评审
- **FR-004**: 对已接入现有基础设施的对象类型（CHART / DASHBOARD），系统 MUST 在对象操作实现中自动调用操作记录
- **FR-005**: 对非 AssetOperator 或元数据对象，系统 MUST 支持模块直接调用 `ActivityService` 写入方法记录
- **FR-006**: 系统 MUST 提供按对象视角的活动日志分页查询接口；V1 支持 `item_type`、`item_id` 过滤，按 `(occurred_at DESC, id DESC)` 排序，返回 `page` / `page_size` / `total`
- **FR-007**: AB / Metric 历史操作记录 MUST 复制到新活动规范中，旧字段或旧表保留不删；升级后新操作只写新活动表
- **FR-008**: 以下 global item 活动 MUST 记录在 `global.activity_log`：组织成员管理、组织生命周期与信息配置、项目生命周期与信息配置、Account API Token 管理；OP 端配置操作继续走 `global.op_operation_log`
- **FR-009**: 账号最近登录时间、最近登出时间、最近活跃时间 MUST 作为 3 个 `TIMESTAMPTZ NULL` 列记录在 `global.account` 表；`last_active_at` 通过 Redis SetNX 做 15 分钟节流刷新
- **FR-009-bis**: 会话活跃刷新 MUST 在 Redis 不可用时跳过本次 DB 写入并记录 warning，不得在故障态降级为每次请求写 DB
- **FR-010**: 系统 MUST 明确活动写入失败策略；至少区分 required_full / required_core / best_effort 三类语义，策略由业务 owner 在接入场景注册时声明，ActivityService 负责执行
- **FR-011**: 系统 MUST 保留最小归因信息集合：`item_type`、`item_id`、`action_type`、`operator_id`、`operator_name`、`source`、`occurred_at` 及必要的结构化变更详情
- **FR-012**: V1 查询与索引策略 MUST 优先优化单对象最近变更链路排查路径，不优先满足按操作人或按组织范围的广义分析型查询
- **FR-012-bis**: 系统 SHOULD 为一次业务操作影响多个对象的场景提供可选 `correlation_id`，由基础设施自动生成或继承请求上下文
- **FR-013**: 活动模型 MUST 为未来企业信任与治理场景保留扩展可能，但敏感字段策略平台、可见性控制、保留策略不进入 V1 主实现
- **FR-014**: V1 MUST NOT 在官方产品中新增通用活动查看页面；AB / Metric 维持现有查看能力
- **FR-015**: 系统 SHOULD 在 OP 或等价内部管理链路暴露对象活动查询接口

### 非功能需求

- **NFR-001**: 活动日志写入额外延迟目标 < 50ms（P99）；若某接入点选择阻塞业务的 required 策略，需在评审中重新确认该路径延迟
- **NFR-002**: 按 `(item_type, item_id, occurred_at DESC, id DESC)` 索引，单项目千万级日志量下查询响应 < 1s（P99）
- **NFR-003**: V1 活动详情 MUST 持久化到 `TEXT` 列，不使用 PG `JSONB`；默认 readable JSON，压缩仅作为后续讨论项
- **NFR-004**: 活动日志默认对官方产品用户不可见，V1 以 OP / 内部接口消费为主

---

## 成功标准

- **SC-001**: 统一活动规范覆盖首批对象（CHART / DASHBOARD / COHORT / EXPERIMENT / FEATURE_GATE / FEATURE_CONFIG / METRIC）
- **SC-002**: 活动失败策略在真实故障注入下与最终选定方案一致
- **SC-003**: P99 活动写入延迟 < 50ms（含序列化和 DB INSERT）
- **SC-004**: AB / Metric 历史操作记录完成复制，新操作全部走新活动表
- **SC-005**: 单项目百万级活动日志下，按 `item_type + item_id` 分页查询响应 < 500ms（P99）
- **SC-006**: 组织/项目级管理操作、Account API Token 操作具备清晰活动落点；账号活跃字段具备明确字段落点
- **SC-007**: 单个对象的最近变更链路可通过统一活动查询直接获得 who / what / when / source
- **SC-008**: V1 不新增官方产品通用活动页面；内部同学仍可通过 OP / 内部接口完成审查

---

## 技术方案

所有技术设计（数据模型、枚举规范、detail schema、Detail helper、写入模型、查询接口、场景目录、接入指南、交付阶段、验证方案）详见 [plan-activity-log.md](./plan-activity-log.md)。
