# 功能规格：项目内对象操作活动日志

**目录**: `20260626-Wave-Feat-AddAuditLog`
**创建日期**: 2026-06-26
**状态**: 评审中 — 基础能力重构中
**架构总览**: [architecture.md](./architecture.md)
**输入**: "Wave 需要一个项目级的操作记录，存储到 PG，所有项目内对象的关键操作（创建/改/删）都要记录，方便追溯"

---

## 背景

现有几套分散的操作记录系统：

| 系统 | 位置 | 存储方式 | 范围 |
|---|---|---|---|
| OP Console 操作记录 | `global.op_operation_log` | 独立表，before/after 快照 | 客户/组织/项目级运维操作 |
| AB 模块操作记录 | `ab_feature_flag.details` JSONB 内嵌 | 内嵌在 Feature Flag 行内 | AB 实验/Feature Flag 的状态变更 |
| Metric 历史 | `meta.metric_define_history` | 独立表，old/new define | 指标口径变更 |

现状问题：
- AB 模块的操作记录内嵌在 JSONB 中，无法独立查询、无法跨对象统一追溯
- Metric 有独立历史表，但与 AB / OP / 其他对象没有统一规范
- 缺少统一的**项目内活动记录规范**，不同对象各自演化
- Asset Behavior 表仅记录轻量操作（无快照），定位是分析而非操作记录

新系统定位为**项目内对象的统一活动规范与落盘基础设施**，用于统一收口项目内对象的关键操作记录。这里的“对象”明确包含两类：

- **资产对象**：Chart / Dashboard / Cohort / Experiment / Feature Gate / Feature Config
- **元数据对象**：Metric / Event / Property 等

活动规范**不沿用当前 asset 基础设施的概念作为范围真相**，而是使用活动域自有的对象模型。

这里的“项目级”是**容器边界**，不是“按项目维度浏览历史”的主要查询视角：

- **项目内对象** = 本规格的主线，写入项目内标准活动表，查询按 `item_type + item_id` 查看历史
- **组织 / 项目级管理操作** = 也必须有操作记录，但允许采用独立于项目内标准表的落盘方式
- **账号活跃信息** = 最近登录 / 登出 / 活跃时间，落在 `account` 表或等价账号主表，不进入项目对象活动表

当前 Wave 代码里确实存在 **Asset 域** 和 **Metadata / Catalog 域** 两套分类：

- Asset 域：当前由 `AssetType` + `asset_base_view / asset_view` + `asset_permission / favorites / asset_reference / asset_behavior` 共同定义
- Metadata / Catalog 域：当前由 `CatalogChangeType` 和 QE catalog 刷新链路定义，包含 event / property / metric 等对象

但要注意：**当前 asset 基础设施本身并不完善，不能直接拿来当产品范围的真相来源**。例如：

- `AssetOperator` 当前只注册了 Chart / Dashboard，其他类型仍是 TODO
- `AssetOperator` 接口本身只覆盖 CRUD，不覆盖 copy / status change / relate 等关键操作
- 部分资产能力在不同入口里的覆盖也不一致（如首页搜索的 assetTypes 列表与完整 AssetType 集合并不完全同步）

因此，V1 的范围应理解为：

- **项目内活动记录规范**是主线，不沿用 asset 作为顶层概念
- **AB 与 Metric 必须纳入同一套标准规范**
- **事件、属性等元数据对象**同样属于该规范适用范围
- **组织 / 项目级管理操作**需要活动，但允许单独设计落盘
- **账号最近登录 / 登出 / 活跃时间**走账号表字段，不纳入项目对象活动表

说明：
- `Cohort` 同时属于 Asset 域和 Metadata / Catalog 域
- `Metric` 在当前代码里更偏 Metadata / Catalog，但根据本轮共识需要纳入统一活动规范
- `Event / EventProperty / UserProperty / VirtualProperty / VirtualEvent` 属于元数据对象，不叫资产，但同样适用统一活动规范

---

## 价值定位

当前最明确的直接价值，不是“先做一个对外售卖的企业合规功能”，而是**内部排查问题时，能够快速回答 who / what / when / where**。

用户已明确：当前最真实的使用场景是**内部排查问题**。因此，V1 不应假设自己已经是一个成熟的企业级合规产品，而应优先解决“出了问题后能快速还原最近变更链路”这个核心场景。同时，设计上要显式为另外 3 类更长期的商业价值预留空间。

在交付边界上，这也意味着：**V1 的重点是把统一操作记录和内部可审查能力做好，而不是在官方产品中同步推出新的通用活动页面**。除 AB / Metric 已有查看能力继续保留外，其他对象的活动查看能力优先通过 OP / 内部接口承接，页面不是前置要求。

这次需求的 4 个价值点，按当前判断排序如下：

1. **事故止损 / 根因定位（P0）**：对象被改坏、配置异常、指标口径变化后，内部同学可以沿着单对象历史快速定位最近一次关键变更，不再同时翻多套历史机制
2. **组织放权 / 权责清晰（P1）**：多人协作编辑实验、配置、指标时，团队敢于下放操作权限，因为事后能明确是谁在什么时间做了什么改动
3. **成交门槛 / 企业信任（P1）**：当产品进入更强安全评审或采购流程时，可以证明敏感对象变更具备可追溯能力，而不是临时靠查库和问人
4. **治理基础设施（P2）**：未来若引入审批、变更告警、敏感操作规则、自动化治理，统一活动模型可以作为底座复用

### 设计原则

1. **Troubleshooting First**：V1 的查询路径、索引策略、字段取舍，应优先服务对象历史排查，而不是先做宽泛的人维度报表或 BI 分析
2. **Attribution by Default**：任何进入统一活动规范的记录，都应默认保留对象、操作、操作人、来源、时间和必要变更详情，确保责任链路完整
3. **Enterprise-Ready, Not Enterprise-Heavy**：设计上要支持未来走向更强企业活动能力，但 V1 不为假想合规要求过度建设，避免把当前最核心的排障价值稀释掉
4. **Governance-Extensible**：活动 detail、对象类型和查询模型要保持稳定与可扩展，使后续审批、告警、策略控制可以建立在同一套历史记录之上
5. **Enterprise Traceability**：活动记录的可追溯性本身就是独立价值维度，不是排障的副产品。设计时应显式考虑"客户安全问询时能否直接展示可追溯能力"这一场景，确保 V1 的字段取舍和语义表达在企业视角下同样完整。这不等同于 V1 建设产品 UI，而是确保当客户问"你们能追溯谁改了什么吗"时，回答是"能"，而不是"能，但得查库"。

---

## 用户故事

### 用户角色

- **内部支持 / 研发 / 值班同学**：对象出问题后，需要第一时间定位是谁、在什么时候、从哪里改了什么
- **OP 审查 / 运维同学**：在需要协助排障、核对变更、做内部审查时，需要通过 OP 或内部接口查看统一操作记录
- **项目负责人 / 实验负责人 / 分析师**：多人协作编辑对象时，希望事后能明确责任链路，减少“不敢放权”的顾虑
- **组织管理员 / 项目管理员**：需要保留组织与项目级配置变更的可追溯记录
- **产品 owner / 安全评审对接人**：未来面对客户安全问询、采购评审或内部治理建设时，需要证明关键对象变更具备可追溯能力
- **平台工程师 / 系统设计者**：需要把当前分散的历史记录机制统一成稳定规范，避免后续继续长出新的孤岛

### 用户故事 1 — 自动记录项目内对象关键变更 (P0)

作为**项目负责人 / 实验负责人**，当团队成员对项目内对象执行创建、修改、删除、复制或状态变更时，我希望系统自动留下统一操作记录，这样在事后追溯时不需要依赖个人记忆、聊天记录或零散日志。

**对应价值**: 事故止损、组织放权

**优先级理由**: 这是统一活动规范成立的基础。如果关键对象操作本身都没有被稳定记录，后续查询、排障、治理都无从谈起。

**独立测试**: 创建一个 Chart，修改其名称，删除该 Chart；再更新一个 Metric Define。验证对象操作都能产生统一格式的操作记录。

**验收场景**:

1. **Given** 用户在项目内创建一个 `CHART`，**When** 创建成功，**Then** 活动日志中出现一条 `action_type = "create"` 的记录，detail 优先以结构化 `changes[]` 记录初始字段
2. **Given** 用户修改一个 `DASHBOARD` 的名称，**When** 保存成功，**Then** 活动日志中出现一条 `action_type = "update"` 的记录，detail 中包含变更前后的字段差异
3. **Given** 用户更新一个 `METRIC` 对象的 define，**When** 提交保存，**Then** 活动日志中出现一条 `action_type = "update"` 的记录，detail 中包含 define 字段的结构化变更
4. **Given** 用户删除一个对象，**When** 删除成功，**Then** 活动日志中出现一条 `action_type = "delete"` 的记录，detail 至少包含对象名称快照；必要时允许补充 `snapshot`

---

### 用户故事 2 — 内部排障时按对象快速还原最近变更链路 (P0)

作为**内部支持 / 研发 / 值班同学**或**OP 审查同学**，当某个实验、配置、图表或指标出问题时，我希望能够通过统一查询接口按对象查看最近的变更历史，快速回答 `who / what / when / source`，而不是同时翻多套历史机制。

**对应价值**: 事故止损

**优先级理由**: 这是当前最明确、最真实的直接需求价值，也是 V1 设计必须优先优化的主场景。

**独立测试**: 对同一个 Chart 连续执行创建、修改、删除操作，再按 `item_type + item_id` 查询，验证能够直接获得该对象的完整时间序列历史。

**验收场景**:

1. **Given** 项目中存在多个对象的活动日志，**When** 按 `item_type = CHART` 且 `item_id = 123` 查询，**Then** 返回结果仅包含该对象的日志
2. **Given** 某对象存在多条活动日志，**When** 按 `(occurred_at DESC, id DESC)` 查询，**Then** 最近的操作排在最前面，且每条都包含操作人、时间、来源和必要的变更详情
3. **Given** 某对象的活动日志量超过一页，**When** 分页查询第 2 页，**Then** 返回第 2 页数据，`total` 字段正确标识该对象的总记录数
4. **Given** 同一对象的历史此前分散在旧 AB 内嵌记录、Metric 历史表或其他旧机制中，**When** 完成历史复制后按对象查询，**Then** 查询路径不再要求同时访问多套历史存储
5. **Given** 官方产品没有新增通用活动页面，**When** 内部同学需要审查某个对象历史，**Then** 仍然可以通过 OP 或内部接口完成查询

---

### 用户故事 3 — 多人协作下明确责任链路并支持放权 (P1)

作为**项目负责人 / 团队管理者**，当多人协作编辑实验、指标、配置等对象时，我希望后续能看清是谁做了哪次关键改动，这样我才敢把编辑权限下放给更多成员，而不是把所有高风险操作集中在少数人手里。

**对应价值**: 组织放权

**优先级理由**: 这不是 V1 最先被验证的购买理由，但它是活动从“排障工具”升级为“团队协作基础设施”的关键价值。

**独立测试**: 让两位不同用户先后修改同一个 Experiment 或 Feature Config，验证历史中可以区分两人的修改记录和来源。

**验收场景**:

1. **Given** 多位成员可以编辑同一个对象，**When** 不同成员分别发起修改，**Then** 活动日志中可以明确区分每次修改的 `operator_id`、`operator_name` 和 `source`
2. **Given** 项目负责人需要追溯某次异常变更，**When** 查看对象历史，**Then** 可以从记录中辨认责任链路，而不是只看到匿名或无法归因的系统变更
3. **Given** 某次修改来自内部任务、OpenAPI 或 MCP，**When** 查询对象历史，**Then** 可以和人工 Web 操作区分开来

---

### 用户故事 4 — 非 CRUD 操作与历史债务统一收口 (P1)

作为**平台工程师 / 系统设计者**，我希望 AB 状态流转、复制操作、Metric 口径变更，以及历史上已经存在的 Wave 项目内记录，都能被纳入同一套活动规范，而不是继续分散在各自的私有实现里。

**对应价值**: 事故止损、治理基础设施

**优先级理由**: 如果 V1 只覆盖最简单的 CRUD，而把 AB / Metric 这类高价值变更继续留在旧机制里，那么“统一活动”在最关键的对象上仍然是不完整的。

**独立测试**: 创建一个 AB Feature Flag，执行 debug → online → release 状态变更序列；再更新一个 Metric Define，并验证历史复制完成后都可以通过统一查询路径查看。

**验收场景**:

1. **Given** 一个 AB Feature Flag 从 DRAFT 切换到 DEBUG，**When** 触发状态变更，**Then** 活动日志记录 `action_type = "update"`，detail 的 `changes[]` 中包含状态字段的 before / after
2. **Given** 一个 Experiment 被复制，**When** 执行复制操作，**Then** 活动日志记录 `action_type = "copy"`，detail 中包含原始对象和新对象之间的关联信息
3. **Given** 一个 Metric Define 发生口径变更，**When** 执行更新，**Then** 活动日志记录 `action_type = "update"`，detail 中包含口径字段的 before / after
4. **Given** 已存在的 AB / Metric / Wave 历史操作记录，**When** 新系统上线并完成历史复制，**Then** 旧数据已复制到新活动规范，升级后的新操作只写新活动表，旧字段或旧表保留不删

---

### 用户故事 5 — 组织 / 项目级管理操作记录 (P1)

作为**组织管理员 / 项目管理员**，当我修改组织配置、项目配额、模板或其他管理项时，我希望这些管理动作同样具备可追溯记录，即使它们不和项目内对象活动共用同一张表。

**对应价值**: 组织放权、治理基础设施

**优先级理由**: 用户已经明确要求组织、项目的操作也必须有记录；否则系统只覆盖项目内对象，整体责任链路仍然是不完整的。

**独立测试**: 修改组织配置、调整项目配额或模板，验证系统产生对应的操作记录，且记录链路不依赖 `activity_log` 单表假设。

**验收场景**:

1. **Given** 管理员添加成员到组织，**When** 添加成功，**Then** `global.activity_log` 出现 `action_type = "create"` 的记录，含 `account_id` 和角色信息
2. **Given** 管理员变更成员角色（如 Analyst → Admin），**When** 保存成功，**Then** `global.activity_log` 出现 `action_type = "update"` 的记录，含 `old_level` / `new_level`
3. **Given** 管理员移除组织成员，**When** 操作成功，**Then** `global.activity_log` 出现 `action_type = "delete"` 的记录
4. **Given** 用户创建/删除组织或项目，**When** 操作成功，**Then** `global.activity_log` 出现对应 `create` / `update` / `delete` 记录，领域语义通过 `item_type` 和 `detail` 体现
5. **Given** OP 管理员修改组织配置/项目配额（已有），**When** 保存成功，**Then** 继续沿用现有 `update_org_config` / `update_project_quota` 记录，不做变更

---

### 用户故事 6 — 面向安全问询与采购评审保留可追溯能力 (P2)

作为**产品 owner / 安全评审对接人**，当未来需要回答客户、采购或内部安全团队“关键对象是谁改的、何时改的、是否可追溯”这类问题时，我希望系统已经具备稳定的基础活动模型，而不是临时拼日志和查库，即使官方产品端并没有开放新的通用活动页面。

**对应价值**: 成交门槛、企业信任

**优先级理由**: 当前还没有更强的外部成交证据，因此这不是 V1 的上线前置；但设计阶段必须为这类价值留扩展位。

**独立测试**: 选取一个敏感对象变更场景，验证系统至少能够输出稳定的对象、操作人、时间、来源和必要变更详情，不依赖旧机制拼装。

**验收场景**:

1. **Given** 未来客户或内部安全团队询问某个关键对象最近是谁改过，**When** 查询对象历史，**Then** 系统可以给出稳定、可追溯的变更记录
2. **Given** 后续需要增加敏感字段掩盖、可见性分层或保留策略，**When** 在统一活动模型上扩展，**Then** 不需要推翻已有的对象类型和 detail 结构

---

### 用户故事 7 — 活动失败策略明确化 (P1)

作为**系统设计者 / 平台工程师**，我需要在活动日志写入失败时有明确、可验证的失败策略，并在强活动与 best-effort 之间做显式取舍，避免上线后不同模块各自理解。

**对应价值**: 治理基础设施

**优先级理由**: 该决策会直接影响事务边界、延迟目标、接口语义和用户体验，必须在技术方案评审中明确。

**独立测试**: 模拟 PostgreSQL 写入超时，根据最终选定策略验证主流程行为、错误返回和日志/告警表现与规格一致。

**验收场景**:

1. **Given** 技术方案选择 best-effort，**When** 数据库活动表写入失败，**Then** 对象主操作仍成功返回，活动失败被记录到日志或告警系统
2. **Given** 技术方案选择强活动，**When** 数据库活动表写入失败，**Then** 对象主操作按约定失败，并向调用方返回可识别的错误

---

## 边界情况

- **批量操作** → 用户批量删除多个同类型对象（如 50 个 Chart），活动应该批量写入，避免逐条插入的性能开销
- **超大变更详情** → detail 字段可能包含大型 JSON（如 Chart 的完整配置），V1 优先通过稳定字段投影、大小预算和逐字段截断控制 payload，并记录 warning；不默认启用应用层压缩
- **同对象高频操作** → 同一对象在短时间内被频繁修改（如自动保存场景），需要明确记录每一条变更而不是合并去重
- **操作人或对象名变更** → 活动日志中的 operator_name / item_name 是写入时的快照，历史记录不随名称变更而更新
- **跨项目查询** → 活动表在 meta schema 下按项目隔离，当前不提供跨项目全局查询（如有需要后续可在 global schema 加汇总或由调用方自行跨 schema 查询）
- **查看入口边界** → V1 不要求在官方产品新增通用活动查看页面；除 AB / Metric 保持既有查看能力外，其余活动查看优先走 OP / 内部接口
- **协作环境** → 多人同时操作同一对象，活动日志独立记录每条操作，无锁冲突
- **无对象 ID 的操作** → 极少数项目内规范对象可能不关联具体对象 ID，允许 `item_id = 0` 并用 `item_type` 特殊值标识；组织 / 项目级管理操作优先考虑独立记录链路

---

## 需求

### 功能需求

- **FR-001**: 系统 MUST 为项目内对象定义统一活动规范，并在 meta schema 下提供 `activity_log` 作为项目内对象的标准落盘表
- **FR-002**: 系统 MUST 提供 `ActivityService` 作为公共写入入口，支持 `Log(ctx, input)` 和 `BatchLog(ctx, inputs)` 方法；写入一致性等级由活动模块内的中心化策略决定，而不是由调用方自由传入
- **FR-003**: 活动规范 MUST 使用活动域自有的 `ItemType` 体系，不直接沿用 `def.AssetType` 作为顶层概念；该体系至少能表达资产对象与元数据对象
- **FR-003-bis**: 活动规范 MUST 先提供一组共享基础 `action_type`（V1 为 `create` / `update` / `delete` / `copy`）；如后续确有必要扩展自定义 `action_type`，必须在活动模块统一注册并经过评审，不能由调用方自由拼接
- **FR-004**: 对已接入现有基础设施的对象类型（如 CHART / DASHBOARD），系统 MUST 在对象操作实现中自动调用操作记录
- **FR-005**: 对非 AssetOperator、元数据对象或需要手动控制写入的场景，系统 MUST 支持模块直接调用 `ActivityService.Log()` 记录
- **FR-006**: 系统 MUST 提供按对象视角的活动日志分页查询接口，V1 至少支持 `item_type`、item_id 过滤，按 `(occurred_at DESC, id DESC)` 排序，并返回 `page`、`page_size`、`total`；表在 project schema 内天然隔离，不出 project scope；该接口优先供 OP / 内部链路调用
- **FR-007**: AB / Metric / Wave 项目内历史操作记录 MUST 复制到新活动规范中，旧字段或旧表保留不删；升级后新操作只写新活动表
- **FR-008**: 以下组织 / 项目级管理操作 MUST 记录在 global schema，不写入 `meta.activity_log`。OP 端配置操作继续走 `global.op_operation_log`；Member + 生命周期操作进入 `global.activity_log`（OP 未来可能独立拆分，member 数据不应随 OP 迁移）：
  - **组织成员**：添加成员、变更成员角色/级别、替换主管、移除成员
  - **组织生命周期**：创建组织、归档组织
  - **项目生命周期**：创建项目、删除项目
  - 以下明确 V1 不做：邀请操作（成员加入已有 `create`）、重命名（排障价值低）、预设角色变更（极低频）
- **FR-009**: 账号最近登录时间（`last_login_at`）、最近登出时间（`last_logout_at`）、最近活跃时间（`last_active_at`）MUST 作为 3 个 `TIMESTAMPTZ NULL` 列记录在 `global.account` 表。写入点：登录成功（密码 + OAuth）写 `last_login_at`；登出成功写 `last_logout_at`；`last_active_at` 通过 Redis SetNX 做 15 分钟节流刷新，每个认证请求触发但同一账号 15 分钟内最多写一次 DB。不要求写入 `activity_log`
- **FR-009-bis**: 会话活跃刷新 MUST 在 Redis 不可用时跳过本次 DB 写入，并记录 warning 日志与监控指标；不得在故障态降级为每次请求写 DB。
- **FR-010**: 系统 MUST 在技术方案中明确活动写入的中心化失败策略；至少区分“必须写入核心活动行”“允许 detail 降级”“best-effort”三类语义，但不要求调用方自由传参选择
- **FR-011**: 系统 MUST 保留支撑内部排障所需的最小归因信息集合：`item_type`、item_id、`action_type`、`operator_id`、`operator_name`、`source`、`occurred_at` 以及必要的结构化变更详情
- **FR-012**: V1 的查询接口与索引策略 MUST 优先优化“单对象最近变更链路”的排查路径，而不是优先满足按操作人、按组织范围的广义分析型查询
- **FR-012-bis**: 系统 SHOULD 为一次业务操作影响多个对象的场景提供可选 `correlation_id`；该标识由活动基础设施自动生成或继承请求上下文，不要求业务调用方手工维护 `operation_group_id`
- **FR-013**: 活动模型 MUST 为未来的企业信任与治理场景预留扩展点，包括但不限于敏感字段掩盖、可见性控制、保留策略、审批/告警类上层能力，但这些能力当前不作为 V1 上线前置条件
- **FR-014**: V1 MUST NOT 在官方产品中新增通用活动查看页面；除 AB / Metric 维持现有查看能力外，其他对象的活动查看能力优先通过 OP / 内部接口提供
- **FR-015**: 为方便内部审查与排障，系统 SHOULD 在 OP 或等价内部管理链路暴露对象活动查询接口，但页面不是必需交付物

### 非功能需求

- **NFR-001**: 在既定一致性模式下，活动日志写入的额外延迟目标为 < 50ms（P99）；若方案选择强活动，需在评审中重新确认该指标
- **NFR-002**: 项目内标准活动表在 meta schema 下按项目隔离，配合 `(item_type, item_id, occurred_at DESC, id DESC)` 索引，单项目千万级日志量下查询响应 < 1s（P99）
- **NFR-003**: V1 的活动详情以可读 JSON 文本持久化到 `TEXT` 列；通过字段投影与大小预算控制 payload，暂不默认引入应用层压缩
- **NFR-004**: 活动日志默认对官方产品用户不可见；V1 以 OP / 内部接口消费为主，不要求新增通用 UI 页面

---

## 关键实体

### activity_log（meta schema）

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | BIGSERIAL | PK | 自增主键 |
| `item_type` | VARCHAR(64) | NOT NULL | 活动对象类型：如 CHART / DASHBOARD / COHORT 等，详见 plan-object.md 枚举规范 |
| `item_id` | INTEGER | NOT NULL | 对象 ID |
| `item_name` | VARCHAR(255) | NOT NULL DEFAULT '' | 记录时的对象名称展示快照，便于列表展示和删除后追溯，不随对象后续改名回写 |
| `action_type` | VARCHAR(32) | NOT NULL | 基础操作类型：create / update / delete / copy |
| `operator_id` | INTEGER | NOT NULL | 操作人账号 ID |
| `operator_name` | VARCHAR(255) | NOT NULL DEFAULT '' | 记录时的操作人姓名展示快照，不随用户改名或删除而更新 |
| `source` | VARCHAR(32) | NOT NULL DEFAULT '' | 操作来源：web / openapi / internal / backfill |
| `correlation_id` | VARCHAR(64) | NOT NULL DEFAULT '' | 批量写入或跨对象单次操作的关联标识，由活动基础设施自动生成或继承上下文 |
| `detail_payload` | TEXT | NOT NULL DEFAULT `'{}'` | 活动详情稳定 envelope 的 JSON 文本；V1 不直接存业务结构体；对外查询时返回解析后的 `detail` 对象 |
| `occurred_at` | TIMESTAMPTZ | NOT NULL | 操作时间 / 活动事件时间；在线写入时通常等于操作发生时刻，历史迁移时回填原始事件时间 |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT CURRENT_TIMESTAMP | 活动写入数据库的时间，用于区分事件时间与入库时间 |

**索引**（V1 精简，后续根据实际查询模式补充）:
- `idx_pal_object` ON `(item_type, item_id, occurred_at DESC, id DESC)` — 主索引，按对象查询

补充说明：
- `item_name` 与 `operator_name` 是**展示快照字段**，不是当前对象或当前账号主表的真相字段
- `occurred_at` 是活动事件发生时间，`created_at` 是数据库入库时间，两者语义明确区分

### ItemType（活动域对象类型）

活动域使用自有的 `ItemType` 字符串体系，不直接复用 `def.AssetType`。当前至少需要表达：

- **资产对象**：`CHART` / `DASHBOARD` / `COHORT` / `EXPERIMENT` / `FEATURE_GATE` / `FEATURE_CONFIG` / `PIPELINE`
- **元数据对象**：`METRIC` / `TRACKED_EVENT` / `VIRTUAL_EVENT` / `EVENT_PROPERTY` / `USER_PROPERTY` / `VIRTUAL_PROPERTY`
- **扩展原则**：后续新增对象类型时，通过增加新的 `ItemType` 常量接入，无需改变整体活动模型

### 其他活动落点

- **组织 / 项目级管理活动**：分两条链路。OP 配置操作继续走 `global.op_operation_log`，已覆盖 `update_org_config` / `update_project_quota` 等；客户侧管理操作（成员管理、组织/项目生命周期）走新表 `global.activity_log`，详见 [plan-org.md](./plan-org.md)
- **账号活跃字段**：3 个 `TIMESTAMPTZ NULL` 列直接加在 `global.account` 表上。`last_login_at` 在 `LoginAccount` / `OauthCallback` controller 写，`last_logout_at` 在 `LogoutAccount` controller 写，`last_active_at` 在 `SessionMiddleware` 通过 Redis SetNX 15min 节流刷新。详见 [plan-account.md](./plan-account.md)

### ActivityService（公共活动服务）

- `Log(ctx, input)` → `error`：单条写入，失败策略由活动模块按场景中心化决策
- `BatchLog(ctx, inputs)` → `error`：批量写入；同批次内自动共享 `correlation_id`
- `ListByQuery(ctx, query)` → `(items, total, error)`：分页查询，优先供 OP / 内部排障链路调用

**ActivityWriteInput**:
| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| ItemType | string | 是 | 活动域自有类型 |
| ItemID | int64 | 是 | |
| ItemName | string | 否 | 写入时快照 |
| ActionType | string | 是 | |
| OperatorID | int64 | 是 | 自动从 ctx 解析 |
| OperatorName | string | 否 | 写入时快照，自动从 ctx 解析 |
| OccurredAt | `*time.Time` | 否 | 活动事件时间；为空时由服务层取当前时间，历史迁移可显式回填旧事件时间 |
| CorrelationID | string | 否 | 批量或跨对象关联标识；为空时由服务层自动补齐 |
| Detail | `*ActivityDetail` | 否 | 活动域详情对象，不直接传业务结构体 |

### action_type 约定

- `create` — 创建
- `update` — 更新
- `delete` — 删除
- `copy` — 复制

扩展约束：
- V1 默认只要求以上基础动作集
- 如未来某领域确实无法仅靠 `item_type + detail` 表达语义，才允许新增自定义 `action_type`
- 新增动作必须由活动模块统一注册，避免调用方各自发明近义词

### detail schema 约定

参考 PostHog 的结构化变更设计，活动详情采用**结构化 diff 优先**的稳定 envelope，V1 以可读 JSON 文本持久化，避免直接持久化当前业务结构体，降低历史兼容成本：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `changes` | Change[] | 否 | 字段级变更列表 |
| `extra` | object | 否 | 领域特定扩展信息（如 copy 的 source） |
| `snapshot` | object | 否 | 必要时补充的稳定快照片段（如 delete 场景下的规则摘要），不要求完整业务结构体 |

注：对象名称快照由表字段 `item_name` 承载，`detail` 内不重复。`operator_name` 同理。若 name 在本次操作中被变更，通过 `changes` 中 `field: "name"` 的 before/after 体现。
`detail` 顶层 envelope 由活动模块统一拥有，V1 不新增 `detail_version` 字段；若未来需要不兼容演进，由活动模块统一升级 serializer/parser 兼容逻辑，而不是由各业务域各自管理版本。

**Change 结构**:

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `field` | string | 是 | 变更的字段名 |
| `action` | string | 是 | `created` / `changed` / `deleted` |
| `before` | any | 否 | 旧值（该字段首次创建时为 null） |
| `after` | any | 否 | 新值（该字段被删除时为 null） |

**create 场景**（初始状态的字段列表）:
```json
{
    "changes": [
        {"field": "name", "action": "created", "after": "新图表"},
        {"field": "type", "action": "created", "after": "line"},
        {"field": "dashboard_id", "action": "created", "after": 42}
    ]
}
```

**update 场景**（仅包含有变更的字段）:
```json
{
    "changes": [
        {"field": "name", "action": "changed", "before": "旧名称", "after": "新名称"},
        {"field": "description", "action": "changed", "before": "旧描述", "after": "新描述"}
    ]
}
```

**delete 场景**（变更可以为空，至少保留 name 快照，必要时补充 snapshot）:
```json
{
    "snapshot": {
        "version": 7,
        "dashboard_id": 42
    }
}
```

**AB 状态变更 / 上线场景**（通过 changes[] 中的字段级变更表达状态变化）:
```json
{
    "changes": [
        {"field": "status", "action": "changed", "before": "DRAFT", "after": "RUNNING"},
        {"field": "rollout_percentage", "action": "changed", "before": 0, "after": 100}
    ]
}
```

**copy 场景**:
```json
{
    "extra": {
        "source_item_id": 123,
        "source_item_name": "原始名称"
    }
}
```

### 排除字段体系

为避免 `last_accessed_at`、`updated_at` 等噪音字段淹没活动日志，系统需实现三层排除：

| 层级 | 范围 | 说明 |
|------|------|------|
| **通用排除** | 全对象类型 | `id`, `created_at`, `updated_at`, `created_by`, `last_modified_by` 等系统字段不参与 diff |
| **按类型排除** | 按 ItemType | 各对象类型声明自身不参与活动的字段（如 COHORT 的 `last_calculated_at`） |
| **变更级别排除** | 针对特定实体 | 仅当变更字段出现在此列表时才完全跳过该次记录（如 Dashboard 的 `last_accessed_at`） |

### 敏感字段掩盖

活动模块统一提供 registry + redaction 机制，避免各调用方手写脱敏逻辑。调用方负责提供对象快照或稳定投影原料；对象 registry 负责声明字段级规则；redaction 引擎在 diff 前后统一应用这些规则，确保敏感值不会进入活动载荷。

现有 Wave 基础设施适配如下：

| 字段 | 现有掩码方式 | 活动适配 |
|------|------------|---------|
| 邮箱 | `ulog.MaskEmail()` — 保留首字符 + `***` | 直接复用 |
| 密码/密钥/token | 不读 DB 明文，业务层不持有可活动值 | `before`/`after` 统一写 `"masked"` |
| Integration 的 config/sensitive_config | 各 integration 自行脱敏 | registry 标注敏感字段，redaction 统一输出 `"masked"` |
| Webhook/API endpoint 含凭据 | pipeline/AB target 的后端配置 | registry 剔除或掩盖凭据片段后再进入 diff |
| OAuth client credentials | OAuth 域管理 | registry 不纳入活动字段投影 |

活动模块提供 `MaskedValue = "masked"` 常量。各对象 registry 在声明敏感字段时标注，redaction 引擎直接输出 `"masked"` 而不尝试读取实际值。粗粒度字段值的掩码（如邮箱脱敏）可以在 registry transform 中复用 `ulog.MaskEmail()`。

### 变更检测（diff 引擎）

系统应提供通用 diff 函数 `ChangesBetween(old, new, itemType)`，比较两个模型实例的稳定活动字段并生成 `[]Change`。对象类型通过 registry 注册 include / exclude / mask / transform 规则来控制 diff 行为，避免把当前业务结构直接写入活动载荷。

### 依赖接口

各对象类型需配合提供获取当前对象快照的能力（用于 diff 引擎在 update 前读取旧值）：
- `GetSnapshot(ctx, itemType, itemID) → (interface{}, error)` — 可选接口，非必实现

---

## 成功标准

### 可量化指标

- **SC-001**: 项目内统一活动规范至少覆盖首批对象类型：CHART / DASHBOARD / COHORT / EXPERIMENT / FEATURE_GATE / FEATURE_CONFIG / METRIC；事件与属性对象遵循同一模型接入
- **SC-002**: 活动失败策略在真实故障注入场景下与最终选定方案一致，并由中心化策略决定不同场景的 required / degraded / best-effort 语义
- **SC-003**: P99 活动写入延迟 < 50ms（含 detail 序列化和 DB INSERT；若选择强活动需在评审中重新确认）
- **SC-004**: AB / Metric / Wave 历史操作记录完成复制，新操作全部走新活动表，旧的 `details.operation_records` 或历史表保留不删
- **SC-005**: 单项目百万级活动日志下，按 `item_type + item_id` 的分页查询响应 < 500ms（P99）
- **SC-006**: 组织 / 项目级管理操作具备清晰的活动落点；账号最近登录 / 登出 / 活跃时间具备明确字段落点
- **SC-007**: 单个对象的最近变更链路可以通过统一活动查询路径直接获得 `who / what / when / source`，内部排障不再需要同时翻 AB 内嵌记录、Metric 历史表和 Asset Behavior 等多套机制
- **SC-008**: V1 不新增官方产品通用活动页面；内部同学仍可通过 OP / 内部接口完成对象历史审查，AB / Metric 既有查看能力不受影响

---

## 待澄清问题

> 当前为讨论阶段，以下问题待确认：
> - 各对象类型排除字段清单（需各模块确认）
> - registry 中敏感字段规则与调用方投影边界
> - 各对象的中心化写入策略矩阵（required / degraded / best-effort）
