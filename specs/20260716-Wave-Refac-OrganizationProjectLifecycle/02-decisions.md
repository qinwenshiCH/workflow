# 决策记录：Wave 组织与项目生命周期治理

> 只记录影响最终方案的结论。人工决策与 AI/`/autoplan` 结论严格分区；被后续结论替代的内容只保留在“已替代”小节。

## 1. 人工决策

### 1.1 范围与产品语义

- `2026-07-16` `[用户确认]`：本 change 只处理组织与项目生命周期，不受其他 spec 阻塞。
- `2026-07-16` `[用户确认]`：项目没有 Archive 概念；Delete 是可恢复逻辑删除，Purge 是单独调用、同步、不可恢复的物理清理。
- `2026-07-16` `[用户确认]`：组织和项目都支持 Restore；组织 Restore 不级联项目，父组织 Delete 时项目不能 Restore。
- `2026-07-16` `[用户确认]`：Organization Delete 前逐个 Delete 项目，Organization Purge 前逐个 Purge 项目；不自动级联或批量代办。
- `2026-07-17` `[用户确认]`：项目 Delete 复用现有 `DISABLE`，不新增 `DELETED`；历史数据清理后，`DISABLE,false` 只表示新 Delete 的可恢复数据。
- `2026-07-17` `[用户确认，后续细化]`：历史 DISABLE 由用户人工处理，本 change 不提供扫描、批处理、脚本、运行手册或自动 migration；后续明确为通过本期 OP Project Purge 逐个处理 `DISABLE,true`。
- `2026-07-17` `[用户确认]`：`INITIALIZING` 项目允许 Purge。
- `2026-07-20` `[用户确认]`：Project、Organization 新增 `PURGING` 和 `PURGED`；新数据以 `PURGING,false` 作为同步清理的持久栅栏，全部业务资源和引用清理完成后才在最终 Global PG 事务写 `PURGED,true`，保留可直接查询的生命周期墓碑；墓碑的后续物理删除不在本期设计范围。
- `2026-07-17` `[用户确认]`：Delete/Restore 必须轻量，只修改 Global PG 生命周期状态和 PM 控制状态，不删除 Scheduler Job 或项目持久资源。
- `2026-07-20` `[用户确认]`：历史旧 Delete 产生的 `DISABLE,is_deleted=true` 项目允许直接 Purge；执行期间保持 `is_deleted=true`，不改回 false，最终归一为 `PURGED,true`。用户会先人工 Purge 这些历史项目，本 change 不设计主记录的后续物理删除。
- `2026-07-20` `[用户确认]`：Project Delete 不要求父 Organization 为 `ENABLE`；Delete 是向更安全状态收缩，只有 Project Restore/Create 继续要求父组织 `ENABLE,false`。

### 1.2 权限、前端与审计

- `2026-07-16` `[用户确认]`：生命周期详情及 Delete、Restore、Purge 只允许 OP 白名单账号会话；普通租户、Token、Project Secret 和内部服务身份不得调用。
- `2026-07-16` `[用户确认]`：本期同时交付 OP 后端和客户详情前端；租户侧不提供生命周期入口。
- `2026-07-17` `[用户确认]`：删除租户 `/project/delete`、`/org/delete` API、Controller 和前端调用。
- `2026-07-17` `[用户确认]`：生命周期管理位于 OP 客户详情“账单”之后、“审计”之前；不做全局列表、搜索、分页、批量操作或复杂交互。
- `2026-07-17` `[用户确认]`：所有动作输入原因和真实组织/项目 ID；组织套餐未过期时增加一次额外警告确认。
- `2026-07-17` `[用户确认]`：前端不展示倒计时；失败后刷新状态，不做自动重试。
- `2026-07-17` `[用户确认]`：六类动作复用现有 `AuditService.LogWithFallback`，记录操作人、对象、原因、前后状态和结果；不建设新审计平台。

### 1.3 工程与文档约束

- `2026-07-16` `[用户确认]`：Purge 代码可重入，失败后从第一步完整重跑；不新增执行表、步骤账本或后台任务。
- `2026-07-16` `[用户确认]`：功能完整但改动克制；拒绝动态插件、DSL、通用协调器、单实现接口、工厂和预留框架。
- `2026-07-17` `[用户确认]`：`03-plan.md` 只保留最终概要方案；文件/函数级范围进入后续 `04-detail.md`。
- `2026-07-17` `[用户确认]`：`02-decisions.md` 使用“人工决策”和“AI 自动决策”两个顶级模块隔离。
- `2026-07-17` `[用户确认]`：流程图使用可正常渲染的 Mermaid。
- `2026-07-17` `[用户确认]`：先评审准确 plan，再生成 detail。
- `2026-07-20` `[用户确认]`：采纳本轮全部 `/ponytail` 收缩建议：不用审计表模拟 Purge receipt，不建设 `purgeStep` 框架，不让长 TTL 锁承担正确性，不扩张通用 Redis 清理接口。
- `2026-07-20` `[用户确认]`：`04-detail.md` 按 `apps/*` 顶级组件及共享项目基础设施梳理项目入口、持久资源、运行资源、进程内状态和 Delete/Restore/Purge 行为；`01-spec.md` 只定义覆盖要求，`03-plan.md` 只保留总览，避免重复堆叠实现细节。
- `2026-07-20` `[用户确认]`：`04-detail.md` 的组件章节先用生命周期控制状态、可用性、运行、项目业务数据四个职责平面建立整体认知，再按职责分组覆盖全部 `apps/*`；职责平面与持久资源/运行资源/进程内状态/入口门禁等资源形态分开，以全景图和统一资源/动作矩阵为主。
- `2026-07-20` `[用户确认]`：`04-detail.md` 表格中的多项资源和动作使用列表标记，避免把多个概念塞进连续句子；同一含义统一使用“持久资源、运行资源、进程内状态、入口门禁、清理 owner、PM Delete Hook、PM Update Hook”等术语，并删除总览与组件明细之间的重复信息。
- `2026-07-20` `[用户确认]`：Markdown 表格单元不使用 HTML `<br>` 做换行；表格只保留单行摘要，多项资源和动作改为表格外的真实 Markdown 无序/有序列表。Mermaid 图中的 `<br>` 保留为图内换行语法。
- `2026-07-20` `[用户确认]`：调整 04-detail 组件章节为“模块 Mermaid 流程图 + 资源台账表”；流程图描述资源生命周期变化，台账表一行一个资源，列出类型、来源、内容与作用、Delete、Restore、Purge 及清理 owner；无独立项目资源的组件只保留结论表。
- `2026-07-20` `[用户确认]`：组件章节固定为“资源清单表 → 资源生命周期变化图 → 适配结论”；表格先说明资源内部情况，图再展示资源在 `ENABLE/DISABLE/PURGED` 三状态和 Delete/Restore/Purge 动作之间的变化，删除或弱化抽象的资源关系图；无项目资源的组件只保留结论表并说明不绘图。
- `2026-07-20` `[用户确认]`：删除 `4.5.1 生命周期与准入图`，保留 `4.2 Wave 项目全链路` 作为唯一全局总览；`apps/web` 后续章节顺延编号，避免重复控制面图。
- `2026-07-20` `[用户确认]`：生命周期图不在资源节点中混写“持久资源/运行资源”等类型；改用 `Global PG`、`PM Redis`、`Kafka`、`进程内存`、`运行中` 等载体框表达资源位置，资源表继续保留生命周期类型列；同一状态内部不绘制资源依赖箭头。
- `2026-07-20` `[用户确认]`：生命周期图改为三状态外层框架 `ENABLE/DISABLE/PURGED`；`Restore` 不再单独建状态框，而是用 `DISABLE -.-> ENABLE` 回退箭头表示；Delete、Purge 使用实线，Restore 虚线仅用于区分回退方向，不表示异步；每项资源在三个状态中完整出现。
- `2026-07-20` `[用户确认]`：生命周期图箭头标签统一使用“动作：资源结果”；资源没有变化时必须明确写“`不变`”，不得只用“保留”或省略动作，避免读者误以为资源变化未说明。
- `2026-07-21` `[用户确认]`：ProjectService 只负责生命周期校验、短锁、状态切换和最终 Global PG 事务；Web 内新增具体的 `ProjectResourcePurger` 按固定顺序同步编排，各资源 owner 负责真正删除和确认资源不存在。它不是通用协调器，不提供 owner 接口、动态注册、步骤配置或执行表。
- `2026-07-21` `[用户确认]`：`03-plan.md` 只回答顶层职责、核心流程、可靠性和取舍；`04-detail.md` 从文件/函数与组件维度说明具体实现，避免在两份文档重复资源介绍。
- `2026-07-21` `[用户确认]`：允许在保持“资源台账 → 生命周期变化图 → 适配结论”风格不变的前提下修正 `04-detail.md` 第 4 章；职责平面和资源形态分开描述，Global PG 明确为“生命周期控制状态的持久化存储”，Meta/Data PG、Doris、Kafka、Redis、OSS 统一称为“项目业务数据”。
- `2026-07-21` `[用户确认]`：资源台账不再只给资源名和类型，统一用“内容与作用”说明保存或承载什么、供谁使用；PM 的 `membership/info` 分别改称“可用项目集合索引”和“项目运行时快照”，避免与成员关系混淆。
- `2026-07-21` `[用户确认]`：普通 Web API、MCP、Internal S2S 一节改为“入口与在途请求边界”，不把请求或回调称为资源；Internal S2S 明确区分新工作命令、在途查询和结果/进度回写，删除没有明确接口含义的 `cleanup 回调`。
- `2026-07-21` `[用户确认]`：`apps/web` 先列 Meta/Data PG Schema、Doris Database、Kafka Topic 集合和 OSS 前缀集合等项目存储根，再单独说明 Pipeline 业务资源与 migration runner；二者不再合并为同一种资源。
- `2026-07-21` `[用户确认]`：C1 Kafka producer 关闭自动建 Topic；上线前检查并按项目初始化配置补齐所有 `ENABLE` 项目的预期 Topic。缺失 Topic 时写入进入现有错误日志和重试路径，直到 Topic 修复或 context 取消，不再静默补建；本 spec 不顺便调整 producer 重试和告警体系。

### 1.4 已替代的人工决策

- `2026-07-21` `[已替代]`：本轮重构不得改动 `04-detail.md` 第 4 章 → 后续评审发现职责平面、资源形态、入口和运行过程混用，允许保持既有风格进行局部重构。
- `2026-07-20` `[已替代]`：生命周期图使用四状态外层框架，并将 `RESTORE` 作为独立状态展示 → 改为 `ENABLE/DISABLE/PURGED` 三状态，Restore 只作为回退动作。
- `2026-07-16` `[已替代]`：暂不接入审计 → 本期复用现有 OP 审计。
- `2026-07-16` `[已替代]`：新增全局生命周期页面 → 改为客户详情生命周期 Tab。
- `2026-07-16` `[已替代]`：保留租户 Delete API → 删除租户入口，仅 OP 可操作。
- `2026-07-17` `[已替代]`：新增 `DELETED` 表示 Delete → 复用 `DISABLE`，清理历史数据后统一语义。
- `2026-07-17` `[已替代]`：Restore 等待 Job/lease 收敛 → Restore 立即恢复；只有 Purge 要求运行面静默。
- `2026-07-17` `[已替代]`：Purge 开始即统一设置 `is_deleted=true` → 新数据改为 `PURGING,false`，历史 `DISABLE,true` 保持 true 进入 `PURGING,true`，最终均写 `PURGED,true`。

## 2. AI 自动决策与 `/autoplan` 结论

状态说明：

- `[自动采纳]`：由代码事实和人工约束直接推导。
- `[用户已确认]`：AI 提出后用户明确要求写入方案。
- `[已替代]`：已经被后续结论取代。
- `[拒绝/延期]`：评审过但不进入当前 change。

### 2.1 `/autoplan` 与 simplify 总结

| 评审 | 结论 |
| --- | --- |
| 产品 | 用现有组织/项目 Service 承担规则，不建设生命周期平台 |
| 设计 | 复用 CustomerDetail 与现有组件，只做组织摘要、项目表和通用确认 Dialog |
| 工程 | PM 是唯一运行开关；Purge 用显式顺序调用、幂等重跑和最终 `PURGED,true` 墓碑 |
| DevEx | OpenAPI 是唯一 API 来源，不手改 codegen 或新建测试框架 |
| simplify | 删除早期 `is_deleted` 墓碑、Purge receipt/audit 反查、`purgeStep` 框架、长 TTL 正确性锁、全表索引切换、逐资源 Restore 校验和逐 Job 删除 |

### 2.2 状态、数据和兼容

- `2026-07-17` `[用户已确认]`：组织 Delete/Restore 使用 `ENABLE/DISABLE`，与项目语义一致；不保留只用于组织的 `DELETED`。
- `2026-07-20` `[用户已确认]`：Project、Organization 的状态集合分别为 `INITIALIZING/ENABLE/DISABLE/PURGING/PURGED` 和 `ENABLE/DISABLE/PURGING/PURGED`；`PURGED` 便于 OP 直接查看最终状态。
- `2026-07-20` `[用户已确认]`：新 Purge 在资源清理期间保持 `is_deleted=false`，最终成功才原子写入 `status=PURGED,is_deleted=true`；旧 `is_deleted=true` 继续兼容，删除 `purge_started_at`，不新增状态表。
- `2026-07-20` `[用户已确认]`：上条规则只约束新生命周期数据；历史 `DISABLE,true` 使用 `PURGING,true -> PURGED,true` 兼容路径，不恢复名称占用，也不为范围外物理删除增加代码。
- `2026-07-20` `[自动采纳]`：Organization Purge 要求全部子项目已为 `PURGED,true`，而不是要求项目行消失；本期保留组织和项目墓碑，不设计其后续物理删除。
- `2026-07-20` `[自动采纳]`：Project `PURGED` 墓碑只保留 OP 识别和归属所需字段；最终事务清空配置并把 `secret` 替换为不可认证且唯一的墓碑值，避免“保留状态”反而保留可用凭据。
- `2026-07-17` `[用户已确认]`：保留现有 `WHERE is_deleted=false` 名称唯一索引，Delete 后名称继续占用。
- `2026-07-20` `[用户已确认]`：migration runner 改用单用途 `GetAllMigrationProjects`，只遍历 `INITIALIZING/ENABLE/DISABLE,is_deleted=false`；`DISABLE` 继续升级，`PURGING` 不再迁移。
- `2026-07-17` `[用户已确认]`：不新增生命周期 CHECK、trigger 或复杂一致性 SQL；只新增 organization status 字段并同步 bootstrap SQL。
- `2026-07-17` `[自动采纳，后续修正]`：生命周期前端开放前必须由用户确认历史 DISABLE 已清理；后续确认改为复用 OP Project Purge 逐个处理 `DISABLE,true`，Purge 保持原 `is_deleted`，Restore 明确拒绝该历史组合，不引入批处理或第二套生命周期语义。

### 2.3 PM、运行面与组件覆盖

- `2026-07-17` `[用户已确认]`：PM 只承担可用项目目录和 Delete/Restore 传播；Delete 用 `DeleteInfo`，Restore 用 `SetInfo`；Purge 只复用 `DeleteInfo` 确保项目已从 PM 消失，不增加 Purge 事件或由 PM 编排清理。
- `2026-07-17` `[用户已确认]`：补强 PM 写错误上抛、调用节点本地同步、订阅重连和“可用项目集合索引 + 项目运行时快照”对账；不增加 Restore/Purge 事件、ACK 或协调器。
- `2026-07-20` `[用户确认]`：PM 中是否存在 Project 是所有项目任务的唯一运行开关，不新增 Stop/Delete/Purge 事件或逐任务命令；Master 不生成、Worker 不领取，运行中 handler 在既有 heartbeat 读取 PM 后只取消本地 context、释放 lease。
- `2026-07-20` `[用户确认]`：Delete 不删除、不 Stop、不标记 `CANCELED`，也不增加 Job/Instance/Task 的业务失败次数；Restore 后由现有 cron/repair 恢复，Delete 期间错过的 cron 不补跑。
- `2026-07-17` `[用户已确认]`：MCP 在统一项目授权函数补 PM 门禁；Internal S2S 阻断新工作命令，Delete 后允许只读查询和结果/进度回写，进入 `PURGING` 后全部拒绝，静默后才删除底层资源。
- `2026-07-17` `[用户已确认]`：Edge、QE、C1 metadata、LiveEvent 和 Asset Behavior 只补真实的项目本地清理；ADTOL、ABOL、Dispatch 和无项目级资源的组件不增加空接口。
- `2026-07-17` `[用户已确认]`：Wagent claim/start 前检查项目；Delete 期间不 ACK 或丢弃可恢复队列消息。
- `2026-07-20` `[用户确认]`：Connector、MA 等 Scheduler handler 不自行订阅生命周期信号，统一由 Scheduler Worker 门禁和 heartbeat 控制，不建设组件专属协调器。
- `2026-07-17` `[自动采纳]`：MA 项目运行资源使用 Web 无法访问的独享 Redis；Purge 增加一个仅供 Web 调用的 MA 内部 endpoint，同步清项目 Key 和 MA 项目消费组。Web/MA 通过同一个环境变量 Secret 鉴权，不让 MA 为此接入 Global DB 或内部 scope 框架；Delete/Restore 不调用该接口，PM 仍不编排 Purge。
- `2026-07-17` `[自动采纳]`：已 Delete 组织必须在统一 HTTP 准入点失效；扩展现有 `OrganizationFilter` 并收紧普通 DAO 查询，不在各 Controller 重复判断，也不新增组织生命周期中间件。
- `2026-07-17` `[自动采纳]`：Wave 自管 OSS 的项目根前缀实际有 `load/backfill/events_cron/users_cron` 四类；Purge 全部清理，但不触碰客户自有目标 bucket。
- `2026-07-17` `[自动采纳]`：Wagent quota/rate-limit 和全局 Stream 不是通用 `p:<pid>:` 前缀；由 Wagent 现有 Service 在 `project_redis` 步骤定向清理，不新增跨进程接口。
- `2026-07-17` `[自动采纳]`：LiveEvent 使用带 project ID 和时间戳的临时 Kafka group；Delete 关闭当前连接，Purge 在 `project_kafka` 步骤按 `live-event-<pid>-` 前缀删除残留 group。
- `2026-07-17` `[自动采纳]`：Dispatch 删除项目后存在 Redis task map 不刷新的漏洞；只修正现有 `refreshTopo` 的 removed-project 变更判定，不增加即时通知 channel 或生命周期协调器。
- `2026-07-17` `[自动采纳]`：项目权限、资产权限、QE lock、Project→Org 和 Account API Token scope cache 不都使用通用项目前缀；Purge 在既有 `project_redis`/最终事务提交后按现有 key 规则清理，不新增 cache registry。
- `2026-07-20` `[自动采纳]`：带 project label 的 metrics、日志和 trace 属于可观测历史，沿用各自 retention，不在同步 Purge 中建设逐进程或远端监控删除接口；它们不得继续驱动项目工作。
- `2026-07-20` `[自动采纳]`：当前生产 Scheduler 共注册 11 个 JobType：Web 8 个、Connector 1 个、MA 2 个；统一由 Scheduler Master/Worker/heartbeat 的 PM 门禁覆盖，不按 handler 复制生命周期逻辑。
- `2026-07-20` `[自动采纳]`：`apps/simulator` 不初始化 PM、不注册生产 Scheduler handler、也不拥有 Wave 项目存储；只在 detail 明确“已检查、无需改动”，不增加空接口。
- `2026-07-21` `[用户已确认]`：`ProjectResourcePurger` 位于 Web，但不替其他进程清理其私有资源；Web 通过窄 client 调用 MA 内部 Purge endpoint，MA Runtime 清理自身共享/独享 Redis 和项目消费组并在确认不存在后返回。多 MA 副本依靠 PM/Scheduler 统一停止进程内工作，不做逐 Pod HTTP fan-out。
- `2026-07-21` `[自动采纳]`：Purge 在最终 Global PG 事务前对第 4 章列出的已知资源做一次显式最终核验；核验由各 owner 的既有查询或幂等清理方法完成，不建设通用 `Verify` 接口。
- `2026-07-21` `[自动采纳]`：Purge 删除资源前必须满足“写入者已停止，或现有入口门禁已保证资源不能被重建”。Kafka producer 关闭自动建 Topic，项目级 PG/Doris/Kafka 容器只能由受状态约束的初始化路径创建；Redis/OSS 等运行数据在对应 writer 静默后清理，不引入 generation fencing。

### 2.4 Restore 保证与失败边界

- `2026-07-17` `[用户已确认]`：Restore 立即更新 DB 并重新发布 PM，不等待后台运行状态收敛，不逐项扫描或重建资源。
- `2026-07-17` `[用户已确认]`：Delete 保证不主动删除项目持久资源，但不承诺补偿被拒绝流量、错过 cron、过期 Kafka/Redis 状态、LiveEvent 实时消息或外部副作用。
- `2026-07-20` `[用户已确认]`：所有运行中 Scheduler handler 采用同一规则，在 heartbeat 发现 PM 不含项目后收到本地 context cancel；不区分短任务和长期 consumer。只有 Purge 必须确认运行面静默。
- `2026-07-17` `[自动采纳]`：重复 Restore 即使 DB 已是 `ENABLE` 也重新执行 PM `SetInfo`，修复 DB 成功但 PM 写失败。
- `2026-07-20` `[用户已确认]`：Purge 先条件写入 `PURGING`，再移除 PM、确认静默并顺序清理；失败保留 `PURGING`，只能重试 Purge，最终写 `PURGED,true`，不在同步请求内硬删主记录。
- `2026-07-17` `[自动采纳]`：Purge 尊重请求 context 和现有依赖 timeout；客户端断开后不转后台执行。

### 2.5 API、前端与测试

- `2026-07-17` `[用户已确认]`：OP API 为一个客户生命周期详情和组织/项目各三个动作；以 `customer_id` 限定范围，不提供全局 list。
- `2026-07-17` `[用户已确认]`：六类动作统一使用 `confirm_value=目标 ID` 和 `reason`。
- `2026-07-20` `[用户确认]`：复用 Wave 通用错误；结构化 data 只保留目标、阻塞项和稳定 Purge step，不返回 `already_absent`。
- `2026-07-17` `[自动采纳]`：组件覆盖测试必须逐项包含 Web、MCP、Internal API、Edge、ADTOL、ABOL、Connector、Scheduler、Dispatch/C1、MA、QE、LiveEvent、Wagent 和 Asset Behavior。
- `2026-07-17` `[自动采纳]`：低保真原型只确认布局，不创建 prototype route、variant switcher 或假后端。
- `2026-07-20` `[用户已确认]`：可重入保证 `PURGING` 中途失败后完整重跑；`PURGED` 墓碑直接返回已完成，主记录不存在时返回 NotFound，不查询审计表模拟 receipt，也不为范围外物理删除增加代码。

### 2.6 已替代、拒绝与延期

- `2026-07-17` `[已替代]`：`status=DELETED` 表示 Delete → 清理历史数据后复用 `DISABLE`。
- `2026-07-17` `[已替代]`：项目 Redis deny Key 作为额外围栏 → 复用 PM 可用项目集合索引、项目运行时快照和关键入口门禁。
- `2026-07-17` `[已替代]`：Delete 删除 Scheduler Job/lease → 数据保留，只阻止新执行；所有运行中 handler 由既有 heartbeat 本地取消并释放 lease。
- `2026-07-17` `[已替代]`：Restore 逐资源检查并等待收敛 → Restore 轻量立即恢复。
- `2026-07-17` `[已替代]`：组织名/项目名切换全表唯一索引 → 继续使用现有部分索引。
- `2026-07-20` `[已替代]`：Purge 开始统一设置 `is_deleted=true` → 新数据进行中使用 `PURGING,false`，历史 `DISABLE,true` 保持 true 进入 `PURGING,true`，两者只有成功结束才写 `PURGED,true`。
- `2026-07-20` `[已替代]`：表格单元中的多项资源和动作使用列表 → 改为“模块 Mermaid 流程图 + 一行一个资源的资源台账表”。
- `2026-07-20` `[已替代]`：固定 `purgeStep{name,run}` 切片 → 具体 `ProjectResourcePurger` 内使用直线式固定顺序调用，并在错误上附稳定 step。
- `2026-07-20` `[已替代]`：成功后重复 Purge 通过审计反查返回 `already_absent` → `PURGED` 墓碑直接返回已完成，墓碑不存在时 NotFound。
- `2026-07-20` `[已替代]`：生命周期锁覆盖整个重 Purge 并设置长 TTL → 既有锁只保护短状态竞争，`PURGING` 条件更新和幂等删除保证正确性。
- `2026-07-20` `[已替代]`：Project Delete 要求父 Organization `ENABLE,false` → Delete 不检查父组织状态；Restore/Create 仍要求父组织可用。
- `2026-07-17` `[拒绝/延期]`：Purge receipt、generation fencing、异步任务、dry-run API、动态插件、通用 adapter、新审计平台、双人审批、业务重放和 TTL 冻结。

## 3. 未决事项

- 无。历史 `DISABLE,true` 的实际数量和逐个 Purge 结果属于上线前操作事实，不由本 spec 推测。
