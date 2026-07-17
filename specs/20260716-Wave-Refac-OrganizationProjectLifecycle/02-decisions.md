# 决策记录：Wave 组织与项目生命周期治理

> 只记录影响最终方案的结论。人工决策与 AI/`/autoplan` 结论严格分区；被后续结论替代的内容只保留在“已替代”小节。

## 1. 人工决策

### 1.1 范围与产品语义

- `2026-07-16` `[用户确认]`：本 change 只处理组织与项目生命周期，不受其他 spec 阻塞。
- `2026-07-16` `[用户确认]`：项目没有 Archive 概念；Delete 是可恢复逻辑删除，Purge 是单独调用、同步、不可恢复的物理清理。
- `2026-07-16` `[用户确认]`：组织和项目都支持 Restore；组织 Restore 不级联项目，父组织 Delete 时项目不能 Restore。
- `2026-07-16` `[用户确认]`：Organization Delete 前逐个 Delete 项目，Organization Purge 前逐个 Purge 项目；不自动级联或批量代办。
- `2026-07-17` `[用户确认]`：项目 Delete 复用现有 `DISABLE`，不新增 `DELETED`；历史数据清理后，`DISABLE,false` 只表示新 Delete 的可恢复数据。
- `2026-07-17` `[用户确认]`：历史 DISABLE 由用户人工清理，本 change 不提供扫描、批处理、脚本、运行手册或自动 migration。
- `2026-07-17` `[用户确认]`：`INITIALIZING` 项目允许 Purge；Purge 首步标记 `is_deleted=true`，最终物理删除资源和主记录。
- `2026-07-17` `[用户确认]`：Delete/Restore 必须轻量，只修改 Global PG 生命周期状态和 PM 控制状态，不删除 Scheduler Job 或项目持久资源。

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

### 1.4 已替代的人工决策

- `2026-07-16` `[已替代]`：暂不接入审计 → 本期复用现有 OP 审计。
- `2026-07-16` `[已替代]`：新增全局生命周期页面 → 改为客户详情生命周期 Tab。
- `2026-07-16` `[已替代]`：保留租户 Delete API → 删除租户入口，仅 OP 可操作。
- `2026-07-17` `[已替代]`：新增 `DELETED` 表示 Delete → 复用 `DISABLE`，清理历史数据后统一语义。
- `2026-07-17` `[已替代]`：Restore 等待 Job/lease 收敛 → Restore 立即恢复；只有 Purge 要求运行面静默。

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
| 工程 | PM 负责可用目录和传播；Purge 用固定步骤、幂等重跑和主记录最后删除 |
| DevEx | OpenAPI 是唯一 API 来源，不手改 codegen 或新建测试框架 |
| simplify | 删除 `DELETED`、deny Key、`purge_started_at`、全表索引切换、逐资源 Restore 校验、逐 Job 删除、历史清理工具和专属错误码体系 |

### 2.2 状态、数据和兼容

- `2026-07-17` `[用户已确认]`：组织也使用 `ENABLE/DISABLE`，与项目 Delete 语义一致；不保留只用于组织的 `DELETED`。
- `2026-07-17` `[用户已确认]`：`is_deleted=true` 只表示 Purge 墓碑；删除 `purge_started_at`，不新增状态表。
- `2026-07-17` `[用户已确认]`：保留现有 `WHERE is_deleted=false` 名称唯一索引，Delete 后名称继续占用。
- `2026-07-17` `[自动采纳]`：migration runner 使用 `GetAllNotDeletedProjects`，因此 `DISABLE,false` 项目继续执行 Meta/Data migration，Restore 不需要补迁移。
- `2026-07-17` `[用户已确认]`：不新增生命周期 CHECK、trigger 或复杂一致性 SQL；只新增 organization status 字段并同步 bootstrap SQL。
- `2026-07-17` `[自动采纳]`：生命周期前端开放前必须由用户确认历史 DISABLE 已清理；代码不区分“历史 DISABLE”和“新 DISABLE”。

### 2.3 PM、运行面与组件覆盖

- `2026-07-17` `[用户已确认]`：PM 只承担可用项目目录和 Delete/Restore 传播；Delete 用 `DeleteInfo`，Restore 用 `SetInfo`，Purge 不经过 PM。
- `2026-07-17` `[用户已确认]`：补强 PM 写错误上抛、调用节点本地同步、订阅重连和 membership/info 快照对账；不增加 Restore/Purge 事件、ACK 或协调器。
- `2026-07-17` `[用户已确认]`：Delete 不删除或 Stop Scheduler Job；Master 创建 Instance 和 Worker 领取任务前检查 PM，长期任务在现有 heartbeat 取消。
- `2026-07-17` `[用户已确认]`：MCP 在统一项目授权函数补 PM 门禁；Internal S2S 只阻断新工作入口，保留 finish/update/cleanup 回调。
- `2026-07-17` `[用户已确认]`：Edge、QE、C1 metadata、LiveEvent 和 Asset Behavior 只补真实的项目本地清理；ADTOL、ABOL、Dispatch 和无项目级资源的组件不增加空接口。
- `2026-07-17` `[用户已确认]`：Wagent claim/start 前检查项目；Delete 期间不 ACK 或丢弃可恢复队列消息。
- `2026-07-17` `[用户已确认]`：Connector、MA 长期任务统一由 Scheduler Worker 门禁和 heartbeat 控制，不建设组件专属协调器。
- `2026-07-17` `[自动采纳]`：MA 项目运行态使用 Web 无法访问的独享 Redis；Purge 增加一个仅供 Web 调用的 MA 内部 endpoint，同步清项目 Key 和 MA 项目消费组。Web/MA 通过同一个环境变量 Secret 鉴权，不让 MA 为此接入 Global DB 或内部 scope 框架；Delete/Restore 不调用该接口，PM 仍不编排 Purge。
- `2026-07-17` `[自动采纳]`：已 Delete 组织必须在统一 HTTP 准入点失效；扩展现有 `OrganizationFilter` 并收紧普通 DAO 查询，不在各 Controller 重复判断，也不新增组织生命周期中间件。
- `2026-07-17` `[自动采纳]`：Wave 自管 OSS 的项目根前缀实际有 `load/backfill/events_cron/users_cron` 四类；Purge 全部清理，但不触碰客户自有目标 bucket。
- `2026-07-17` `[自动采纳]`：Wagent quota/rate-limit 和全局 Stream 不是通用 `p:<pid>:` 前缀；由 Wagent 现有 Service 在 `project_redis` 步骤定向清理，不新增跨进程接口。
- `2026-07-17` `[自动采纳]`：LiveEvent 使用带 project ID 和时间戳的临时 Kafka group；Delete 关闭当前连接，Purge 在 `project_kafka` 步骤按 `live-event-<pid>-` 前缀删除残留 group。
- `2026-07-17` `[自动采纳]`：Dispatch 删除项目后存在 Redis task map 不刷新的漏洞；只修正现有 `refreshTopo` 的 removed-project 变更判定，不增加即时通知 channel 或生命周期协调器。
- `2026-07-17` `[自动采纳]`：项目权限、资产权限、QE lock、Project→Org 和 Account API Token scope cache 不都使用通用项目前缀；Purge 在既有 `project_redis`/最终事务提交后按现有 key 规则清理，不新增 cache registry。

### 2.4 Restore 保证与失败边界

- `2026-07-17` `[用户已确认]`：Restore 立即更新 DB 并重新发布 PM，不等待后台运行状态收敛，不逐项扫描或重建资源。
- `2026-07-17` `[用户已确认]`：Delete 保证不主动删除项目持久资源，但不承诺补偿被拒绝流量、错过 cron、过期 Kafka/Redis 状态、LiveEvent 实时消息或外部副作用。
- `2026-07-17` `[自动采纳]`：短的在途任务可以完成；长期 consumer 通过 Worker/lease heartbeat 取消；只有 Purge 必须确认运行面静默。
- `2026-07-17` `[自动采纳]`：重复 Restore 即使 DB 已是 `ENABLE` 也重新执行 PM `SetInfo`，修复 DB 成功但 PM 写失败。
- `2026-07-17` `[自动采纳]`：Purge 首个破坏性步骤前设置 `is_deleted=true`，失败后只能重试 Purge，主记录最后删除。
- `2026-07-17` `[自动采纳]`：Purge 尊重请求 context 和现有依赖 timeout；客户端断开后不转后台执行。

### 2.5 API、前端与测试

- `2026-07-17` `[用户已确认]`：OP API 为一个客户生命周期详情和组织/项目各三个动作；以 `customer_id` 限定范围，不提供全局 list。
- `2026-07-17` `[用户已确认]`：六类动作统一使用 `confirm_value=目标 ID` 和 `reason`。
- `2026-07-17` `[用户已确认]`：复用 Wave 通用错误；结构化 data 只保留目标、阻塞项、Purge step 和 already absent。
- `2026-07-17` `[自动采纳]`：组件覆盖测试必须逐项包含 Web、MCP、Internal API、Edge、ADTOL、ABOL、Connector、Scheduler、Dispatch/C1、MA、QE、LiveEvent、Wagent 和 Asset Behavior。
- `2026-07-17` `[自动采纳]`：低保真原型只确认布局，不创建 prototype route、variant switcher 或假后端。
- `2026-07-17` `[自动采纳]`：项目主记录已硬删后的重复 OP Purge，先用现有审计日志确认该 customer/project 曾成功 Purge；没有归属记录时返回 NotFound，避免绕过 customer → organization → project 校验。

### 2.6 已替代、拒绝与延期

- `2026-07-17` `[已替代]`：`status=DELETED` 表示 Delete → 清理历史数据后复用 `DISABLE`。
- `2026-07-17` `[已替代]`：项目 Redis deny Key 作为额外围栏 → 复用 PM membership/info 和关键入口门禁。
- `2026-07-17` `[已替代]`：Delete 删除 Scheduler Job/lease → 数据保留，只阻止新执行并取消长期任务。
- `2026-07-17` `[已替代]`：Restore 逐资源检查并等待收敛 → Restore 轻量立即恢复。
- `2026-07-17` `[已替代]`：组织名/项目名切换全表唯一索引 → 继续使用现有部分索引。
- `2026-07-17` `[拒绝/延期]`：Purge receipt、generation fencing、异步任务、dry-run API、动态插件、通用 adapter、新审计平台、双人审批、业务重放和 TTL 冻结。

## 3. 未决事项

- 无。历史 DISABLE 的实际数量和人工清理结果属于上线前操作事实，不由本 spec 推测。
