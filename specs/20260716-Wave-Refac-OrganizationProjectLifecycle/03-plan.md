# Wave 组织与项目生命周期治理：概要设计

> 状态：待评审（详细设计已生成）  
> 依据：[01-spec.md](./01-spec.md) 与 [02-decisions.md](./02-decisions.md)

## 1. 背景与目标

当前项目 `Archive` 会进入旧 Delete，依次修改状态、删除一个 Scheduler Job、删除 Doris/PG/Kafka、软删除成员和项目，并移除 PM 信息。它既不是可恢复 Delete，也不是覆盖完整资源的 Purge。

本方案完成三件事：

- Delete：`ENABLE -> DISABLE`，从业务入口和后台执行面拒绝新工作，不碰持久资源。
- Restore：`DISABLE -> ENABLE` 并重新发布 PM，从当前时刻恢复。
- Purge：同步清理全部 Wave 管理资源，失败后可从头重试。

目标是功能完整但不建设生命周期平台：复用现有 OrgService、ProjectService、PM、Scheduler 和资源 client，只补真实旁路与本地资源。

## 2. 现状与约束

### 2.1 代码事实

| 事实 | 影响 |
| --- | --- |
| `project.status` 已有 `INITIALIZING/ENABLE/DISABLE` | 清理历史数据后复用 `DISABLE`，不新增同义状态 |
| 旧 Delete 会删除 Doris、Meta/Data PG、Kafka、成员和一个 Job | 旧资源删除能力全部收口到 Purge |
| 名称唯一索引只排除 `is_deleted=true` | Delete 保持 `is_deleted=false`，自然保留名称 |
| migration 使用 `GetAllNotDeletedProjects` | 长期 DISABLE 项目继续升级，无需 Restore 补 migration |
| PM 被多数请求入口、Token 解析和后台枚举使用 | PM 可作为主要可用目录，但不能假定所有组件都直接依赖 PM |
| PM 部分写错误只记日志，订阅关闭后会退出 | 需要错误上抛、本地同步、重订阅和快照对账 |
| Scheduler 已加载 cron/notify 和 Worker 不会统一重查 PM | 需要在 Master/Worker 中心入口补门禁 |
| MCP、Internal S2S、LiveEvent、Wagent 存在 PM 旁路 | 必须分别在现有统一入口补最小检查 |
| Edge、QE、C1 metadata、Asset Behavior 存在项目本地状态 | 只补实际清退，不增加统一资源接口 |
| OP 已有权限、确认和审计能力 | 直接复用，不新建权限或审计体系 |

### 2.2 设计约束

- 生命周期只允许 OP 操作，旧租户 Delete 接口删除并在混部期间保持阻断。
- 组织必须逐项目 Delete/Purge，不自动级联。
- Delete/Restore 不能删除、Stop、扫描或重建持久资源。
- Purge 同步、可重入，不建执行表、步骤账本或后台任务。
- 历史 DISABLE 由用户人工清理，不交付清理工具。
- 不增加 deny Key、`purge_started_at`、全表索引切换、生命周期 CHECK/trigger、专属错误码或通用协调器。

## 3. 顶层方案

### 3.1 状态模型

```text
Project:
  INITIALIZING / ENABLE / DISABLE

Organization:
  ENABLE / DISABLE

is_deleted=false:
  主记录存在，继续受 migration 和名称索引管理

is_deleted=true:
  Purge 已开始或旧流程墓碑，只能继续 Purge
```

| 转换 | Global PG | PM | 持久资源 |
| --- | --- | --- | --- |
| Project Delete | `ENABLE -> DISABLE` | `DeleteInfo` | 不变 |
| Project Restore | `DISABLE -> ENABLE` | `SetInfo` | 不变 |
| Project Purge | 标记 `is_deleted=true`，最终删主记录 | 确保已移除 | 按固定步骤删除 |
| Organization Delete | `ENABLE -> DISABLE` | 项目已逐个移除 | 不变 |
| Organization Restore | `DISABLE -> ENABLE` | 不级联项目 | 不变 |
| Organization Purge | 标记 `is_deleted=true`，最终删主记录 | 无 | 按固定步骤删除 |

`INITIALIZING` 项目可直接 Purge。`ENABLE,false` 项目不能 Purge，任何 `is_deleted=true` 记录不能 Restore。

### 3.2 组件关系

```mermaid
flowchart LR
    OP["OP 客户详情"] --> Ops["现有 Customer Ops"]
    Ops --> Org["OrgService"]
    Ops --> Project["ProjectService"]
    Ops --> Audit["现有 OP Audit"]

    Org --> Global["Global PostgreSQL"]
    Project --> Global
    Project --> PM["Project Manager"]
    Project --> Purge["固定 Purge 步骤"]

    PM --> Admission["请求准入<br/>Web · MCP · Edge · ADTOL · ABOL · Connector"]
    PM --> Hooks["本地收敛<br/>Edge · QE · Dispatch/C1 · MA · LiveEvent"]
    PM --> Workers["执行准入<br/>Scheduler · Connector · MA · Wagent"]

    Purge --> Redis
    Purge --> MAState["MA 共享/独享 Redis / 消费组"]
    Purge --> OSS
    Purge --> Kafka
    Purge --> Doris
    Purge --> ProjectPG["Meta / Data PG"]
```

PM 是可用项目目录和通知通道，不是生命周期编排器。Purge 不通过 PM 逐组件调用清理。

## 4. 核心流程

### 4.1 Project Delete

```mermaid
sequenceDiagram
    participant OP as OP API
    participant S as ProjectService
    participant L as Lifecycle Lock
    participant DB as Global PG
    participant PM as Project Manager
    participant C as Components

    OP->>S: Delete(projectID)
    S->>L: lock organization then project
    S->>DB: load project and parent organization
    S->>S: require organization ENABLE and project ENABLE/DISABLE
    S->>PM: DeleteInfo(projectID)
    PM-->>C: OnProjectDelete / PM gate
    S->>DB: conditional ENABLE to DISABLE
    S-->>OP: DISABLE
```

要点：

1. 对已 `DISABLE` 项目的重复 Delete 仍调用 `DeleteInfo`，用于修复部分失败。
2. PM 先失效是 fail-closed：DB 更新失败时项目可能暂时不可用，但不会继续接收新流量。
3. 不调用资源删除函数、JobDelete/JobStop、成员删除、权限缓存扫描或业务 Redis 清理。
4. Delete 成功表示 DB/PM 权威状态已提交，不表示所有远端进程已经 ACK。
5. 已开始的短任务可以完成；长期任务由 Worker/lease heartbeat 取消。

### 4.2 Project Restore

```mermaid
sequenceDiagram
    participant OP as OP API
    participant S as ProjectService
    participant L as Lifecycle Lock
    participant DB as Global PG
    participant PM as Project Manager

    OP->>S: Restore(projectID)
    S->>L: lock organization then project
    S->>DB: require organization ENABLE and project DISABLE/is_deleted=false
    S->>DB: conditional DISABLE to ENABLE
    S->>PM: SetInfo(existing project config)
    S-->>OP: ENABLE
```

要点：

- Restore 不等待 Job/lease 或远端 Hook 收敛，不检查 Meta/Data PG、Doris、Kafka、OSS。
- 不创建 Job、不重新初始化项目、不补 migration。
- PM 写失败时返回依赖错误；重复 Restore 即使 DB 已为 `ENABLE` 也重新执行 `SetInfo`。
- 历史 DISABLE 必须在功能开放前人工清理；代码不维护第二套历史状态判断。

### 4.3 Project Purge

```mermaid
flowchart TD
    A["OP Purge"] --> B["获取组织锁和项目锁"]
    B --> C{"项目允许 Purge"}
    C -->|否| X["返回 Conflict"]
    C -->|是| D["确保 PM 已移除"]
    D --> E{"长期工作已停止"}
    E -->|否| Y["返回 Conflict 和阻塞信息"]
    E -->|是| F["条件设置 is_deleted=true"]
    F --> G["按固定顺序执行 purgeStep"]
    G --> H{"步骤成功"}
    H -->|否| I["保留墓碑并返回 step"]
    H -->|是| J["Global PG 最终事务"]
    J --> K["返回 purged=true"]
```

允许 Purge：

- `DISABLE,false`。
- `INITIALIZING,false`。
- 任意 `is_deleted=true` 墓碑，用于重试。

运行面静默只检查已有的可观测状态，例如 Scheduler Instance/Task/lease、Dispatch 项目任务数和 Wagent 运行执行；不为进程内 Hook 建远端 ACK。少量仍在收敛的本地状态如果与资源删除竞争，由现有依赖错误和幂等 Purge 重试兜底。

最小步骤顺序：

| 顺序 | step | 内容 |
| --- | --- | --- |
| 1 | `project_redis` | Scheduler 定向清全局通知/lease；Wagent 定向清 Stream、quota/rate-limit；清权限、资产权限、QE lock、Project→Org cache 和共享 `p:<pid>:` 前缀；不删除生命周期锁 |
| 2 | `project_ma` | 通过一个窄的 MA 内部接口删除共享/独享 Redis 项目 Key 和项目消费组 |
| 3 | `project_oss` | 删除 Wave 管理的 `load/backfill/events_cron/users_cron` 四个项目固定前缀并验证 |
| 4 | `project_kafka` | 删除 Connector 专属组、`live-event-<pid>-*` 组和项目 Topic 并验证；MA 组已由前一步处理 |
| 5 | `project_doris` | Drop 项目 Database |
| 6 | `project_pgdata` | Drop Data Schema |
| 7 | `project_meta` | Drop Meta Schema，Scheduler Job 随 Schema 删除 |
| 8 | `project_global` | 最终事务清引用并删除 project 主记录 |

实现只需普通切片：

```go
type purgeStep struct {
    name string
    run  func(context.Context, int64) error
}
```

不存在资源视为成功；只有最终一致资源在自己的 `run` 内验证，不增加通用 verify 接口。

### 4.4 Organization 生命周期

- Delete：组织锁内重查项目；存在任何非 `DISABLE,false` 项目时拒绝；条件更新组织为 `DISABLE`。
- Restore：`DISABLE,false -> ENABLE`，不级联项目。
- Purge：要求组织非 `ENABLE` 且项目记录数为 0；标记 `is_deleted=true`，清组织派生关系，最后删除组织主记录。
- OP Customer Profile、合同历史、共享 Account 和审计日志保留；客户绑定维持 `expired`。

## 5. PM 与执行面

### 5.1 PM 最小修复

`pkg/pm/project_manager.go` 保留现有接口，只补可靠性：

1. `SetInfo/DeleteInfo` 的 membership、info、publish 关键错误返回调用方。
2. Redis 写成功后立即更新调用节点本地 map，不等待订阅回环。
3. 订阅通道关闭后重订阅。
4. 重订阅后根据 Redis membership/info 快照对账本地 map，补发本地 set/delete Hook。
5. Hook panic 继续隔离；不增加远端 ACK、Restore/Purge 事件或生命周期版本号。

### 5.2 组件项目入口与本地资源

| 组件 | PM 依赖或通知现状 | 本期最小改动 | Restore |
| --- | --- | --- | --- |
| Web API | 项目大部分经过 `ProjectFilter`；现有 `OrganizationFilter` 不检查状态 | 保留项目门禁；扩展现有 `OrganizationFilter`，组织列表和项目 Create 只接受组织 `ENABLE` | PM/组织状态恢复后立即可用 |
| MCP | 根路由绕过 `ProjectFilter` | 在统一项目授权函数检查 PM | 同普通请求 |
| Internal S2S | 只解析 Project Header | 新工作入口检查 PM；finish/update/cleanup 允许回写 | 新工作恢复，旧回调不被卡住 |
| Edge | Token 入口依赖 PM，Delete Hook 为空 | 清 token2id、pipelineVersion、internalSecrets map | update hook 重建 |
| ADTOL | 请求均用 PM Token 查项目 | 无改动，补拒绝测试 | 自动恢复 |
| ABOL | PM Hook 已停止 AB Core | 保留实现，补回归测试 | update hook 重建 |
| Connector HTTP | AppsFlyer 入口依赖 PM | 无本地项目资源则不补空 Hook | 自动恢复 |
| Scheduler | reload/repair 用 PM，live cron/Worker 有旁路 | Master 和 Worker 统一门禁；heartbeat 取消长期任务 | 下个调度周期或 repair 继续，不补跑 |
| Connector Worker | 由 Scheduler 驱动长期 Kafka runner | 复用 Scheduler Worker 取消 | 从保留 offset 继续，受 broker 保留期约束 |
| Dispatch/C1 | Hook 只删 `counts`，refresh 未把 Redis 残留项目标为 changed | 修正现有 refresh，重写 task map 后由 TaskManager 关闭 Pipeline；额外驱逐 C1 metadata map | Dispatch 重新分配并懒加载 metadata |
| MA | ConfigSync 有 Hook，另有 cohort/matcher/feedback 本地态；event consumer 由 Scheduler 启动 | 保留 Untrack；Runtime Hook 驱逐本地态，长期 consumer 由 Worker 取消 | 从保留 offset 继续，运行态懒建 |
| QE Catalog | Delete Hook 为空 | 驱逐项目 Catalog | 懒加载 |
| LiveEvent | 只在 WebSocket 注册时检查 PM | Delete 关闭项目 consumer 和连接 | 新连接懒启动，不回放断开期消息 |
| Wagent | HTTP 受 Web 保护，Redis Stream Worker 无门禁；quota/rate-limit Key 不使用通用项目前缀 | claim/start 前检查；禁用时不 ACK/丢弃可恢复消息；Purge 定向清额外 Key | 未过期消息可继续，过期执行需重试 |
| Wagent MCP tool catalog | 进程内 TTL cache 的 key 含 project ID | 不补 Hook；新执行已被 Wagent/PM 门禁阻断，缓存不可单独产生工作并会按原 TTL 过期 | 后续执行按 TTL 重新加载 |
| Asset Behavior | 每项目后台 batcher | 关闭并移除项目 batcher | 懒创建 |
| Pipeline finalizer | 通过 PM 项目列表运行 | 无改动 | 重新进入列表后继续 |

### 5.3 Scheduler 语义

Delete 不调用：

- `DeleteUsageMeteringDailySchedulerJob`
- `JobDelete`
- `JobStop`
- 逐项目修改 Job Instance、Task 或 lease

新的保证：

- 已加载 cron 和 Redis notify 在创建 Instance 前重查 PM。
- Worker 领取 Instance/Task 前重查 PM。
- 长期 handler 在现有 lease heartbeat 发现项目不可用时取消。
- 短任务已经开始后可以完成，不建设分布式强制回滚。
- Restore 不等待这些状态收敛；Purge 必须确认长期工作停止。
- Delete 期间错过的 cron 不补跑，Restore 后从下一个周期继续。

## 6. 数据模型与 DAO

### 6.1 Schema

| 表 | 变更 |
| --- | --- |
| `organization` | 新增 `status varchar(64) not null default 'ENABLE'` |
| `project` | 不新增字段和状态，复用现有 `DISABLE` |
| 名称索引 | 保持现有 `WHERE is_deleted=false` 部分唯一索引 |

业务表只增加组织 status 并同步 bootstrap SQL。migration 不写 token，不注册额外服务身份，不添加 CHECK、trigger、全表索引或项目数据回填。

### 6.2 查询语义

| 查询用途 | 条件 |
| --- | --- |
| 普通组织业务 | `organization.status=ENABLE AND is_deleted=false` |
| 普通项目入口/列表 | `project.status=ENABLE AND is_deleted=false`，并要求父组织 `ENABLE` 或 PM 存在 |
| 项目创建重名 | 所有 `is_deleted=false`，使 DISABLE 名称继续占用 |
| OP Lifecycle | 包含 ENABLE、DISABLE、INITIALIZING 和 Purge 墓碑 |
| Project migration | 所有 `is_deleted=false`，包括 INITIALIZING、DISABLE |
| Purge | 显式 WithDeleted，支持 `is_deleted=true` 重试 |

重点审查现有 `GetByID`、`ListByOrg`、`GetByOrgAndName`、`GetAllEnableProjects` 和 `GetAllNotDeletedProjects`，不把所有 DAO 机械改成同一个条件。

### 6.3 条件更新

- Project Delete：`WHERE id=? AND status=ENABLE AND is_deleted=false`。
- Project Restore：`WHERE id=? AND status=DISABLE AND is_deleted=false`。
- Project Purge marker：新请求要求 `status<>ENABLE`；已有 `is_deleted=true` 墓碑允许幂等重试。
- Organization Delete/Restore 使用同样的 ENABLE/DISABLE 条件更新。
- 项目普通根更新必须带 `status=ENABLE AND is_deleted=false`，不得用无条件 `Save` 恢复旧状态。
- Organization Create、Project Create 与生命周期操作使用相同锁顺序，避免 Create 与 Delete 交叉。

## 7. API、权限与审计

### 7.1 API

删除租户接口：

- `POST /project/delete`
- `POST /org/delete`

新增 OP 接口：

- `POST /op/customer/lifecycle/get`
- `POST /op/customer/lifecycle/project/{delete,restore,purge}`
- `POST /op/customer/lifecycle/org/{delete,restore,purge}`

六类动作统一传 `customer_id`、目标 ID、`confirm_value` 和 `reason`。`confirm_value` 必须等于目标 ID；服务端校验 customer → organization → project 归属。

错误复用现有 BadParam/PermissionDenied/Conflict/InternalError。结构化 data 只保留：

```text
resource_id, blocked_ids, blocked_count, step, already_absent
```

### 7.2 权限和审计

- 现有 Customer Ops 复用 OP `CheckAccess`。
- 生命周期详情和动作拒绝租户会话、Account API Token、Project Secret 和内部服务身份。
- 六类动作均校验非空 reason 和 ID confirm。
- 复用 `AuditService.LogWithFallback`，记录 success、verify_failed、failed。
- before/after 只保存生命周期字段；Purge 失败记录稳定 step，不记录 Secret 或凭据。

## 8. OP 前端

- 生命周期 Tab 位于账单和审计之间，不新增路由或导航。
- 进入 Tab 后按 customer ID 懒加载组织摘要和项目表。
- 只展示项目、状态、阻塞原因、更新时间和操作；不做搜索、分页、批量、统计卡或时间线。

```mermaid
flowchart TD
    Customer["客户详情"] --> Tabs["合同 · 配置 · 账单 · 生命周期管理 · 审计"]
    Tabs --> Lifecycle["生命周期管理"]
    Lifecycle --> Org["组织摘要"]
    Lifecycle --> Projects["项目表"]
    Org --> OrgActions["Delete / Restore / Purge"]
    Projects --> ProjectActions["逐项目操作"]
```

确认交互：

1. 通用 Dialog 展示影响，输入 reason 和真实目标 ID。
2. 组织套餐未过期时，再显示一次额外警告。
3. 成功后刷新 Tab；失败展示阻塞项目或 Purge step。
4. Purge 网络错误不自动重发，只刷新后由 OP 决定是否重试。

[低保真原型](./assets/lifecycle-tab-prototype.svg)只确认页面层级。

## 9. Restore 的长期边界

### 9.1 保留的持久状态

- Global PG 项目、成员、配置和名称占用。
- Meta/Data PG、Doris、Kafka Topic、OSS 和 Scheduler Job。
- 项目迁移持续执行，Schema 版本不会因为 DISABLE 落后。
- PM 和进程内缓存可通过发布、Hook 或懒加载恢复。

### 9.2 不补偿的时间性状态

| 类型 | Restore 后行为 |
| --- | --- |
| 被拒绝的请求、埋点、内部新工作 | 不重放 |
| 错过的 cron | 不补跑，从下个周期继续 |
| 超过 Kafka retention 或 offset retention 的数据 | 无法恢复，时长依线上 broker 配置 |
| Redis/Wagent/MA 临时状态和 MA 内存 feedback queue | 过期或驱逐后不恢复，必要时用户重试 |
| LiveEvent | 断开期间不回放 |
| 已开始的有限任务 | 允许完成 |
| 外部系统副作用 | 不回滚 |
| 组织套餐变化 | 按 Restore 时的当前状态判断 |

因此对外只承诺“Delete 不主动删除持久资源”，不使用“任意时长绝对无损”的表述。

## 10. 错误与一致性

| 场景 | 结果 | 处理 |
| --- | --- | --- |
| PM Delete 成功、DB 更新失败 | DB 仍 ENABLE，但运行面 fail-closed | 返回错误，重复 Delete/Restore 对账 |
| DB Restore 成功、PM SetInfo 失败 | DB ENABLE，PM 暂不承认 | 返回错误，重复 Restore 重发 PM |
| PM Pub/Sub 丢消息 | 远端本地状态暂时陈旧 | 重订阅后的快照对账纠正 |
| Delete 后有限任务仍运行 | 可能完成 | 不阻塞 Restore；Purge 等长期任务停止 |
| Purge step 失败 | `is_deleted=true` 墓碑保留 | 修复依赖后从第一步重试 |
| Purge 目标不存在 | 已 Purged | `already_absent=true` |
| 组织仍有阻塞项目 | 状态不变 | 返回有限 blocked IDs 和总数 |

跨 PG、Doris、Kafka、Redis、OSS 不模拟分布式事务；依靠步骤幂等和主记录最后删除恢复执行。

## 11. 影响范围

| 模块 | 主要改动 | 风险 |
| --- | --- | --- |
| Project/Organization Service | 重写 Delete，新增 Restore/Purge，旧资源删除只供 Purge | 高 |
| Global DAO/Schema | organization status、条件更新、Lifecycle/WithDeleted 查询 | 高 |
| PM | 写错误、本地同步、重订阅、快照对账 | 高 |
| Scheduler | Master/Worker PM 门禁与长期任务取消 | 高 |
| MCP/Internal API | 统一授权门禁和新工作/回调边界 | 高 |
| Edge/C1/MA/QE/LiveEvent/Wagent/Asset | 补真实本地清理或执行入口，不建统一接口 | 中 |
| PG/Doris/Kafka/Redis/OSS client | 复用清理能力，补幂等和必要验证 | 高 |
| MA 内部接口/配置 | 只暴露项目 Purge，清 MA 独享 Redis 与消费组 | 中 |
| OP Customer Ops/OpenAPI | 客户范围接口、权限、ID/reason、审计 | 中 |
| OP CustomerDetail 前端 | 生命周期 Tab、API、类型、确认和测试 | 中 |
| migration/bootstrap SQL | organization status | 低 |

文件、函数、SQL 和生成命令的精确范围放入后续 `04-detail.md`，不在 plan 重复展开。

## 12. Rollout 与兼容

1. 在网关永久阻断旧租户 `/project/delete`、`/org/delete`，防止旧实例继续物理清理。
2. 部署 Global migration、MA 内部 Purge endpoint、全部后端门禁/Hook/OP API，并向 Web/MA 注入同一个 `MA_PROJECT_PURGE_TOKEN`；暂不开放生命周期前端。
3. 用户人工清理历史 DISABLE，并确认不存在需要保留或 Restore 的旧记录；该步骤不是代码交付物。
4. 人工确认完成后部署或开放 OP 生命周期 Tab；此后 `DISABLE,false` 只由新 Delete 产生。
5. 回滚时旧租户路由仍保持阻断，避免恢复旧物理 Delete。

兼容原则：

- 新旧后端混部期间不开放前端动作。
- 不迁移项目状态，不改名称索引和 Secret 约束。
- DISABLE 项目继续执行现有 Project migration。
- 不增加运行时 cutover flag 或“历史/新 DISABLE”双状态判断。

## 13. 测试与验证

### 13.1 单元测试

- Project/Organization ENABLE/DISABLE 转换、幂等和父子约束。
- 条件 UPDATE 和并发生命周期操作。
- INITIALIZING、Purge 墓碑和 NotFound 边界。
- PM 错误、本地同步、重订阅和快照对账。
- Scheduler Master/Worker 门禁和长期任务取消。
- Purge 步骤顺序、失败即停、重跑和主记录最后删。
- OP 权限、归属、ID/reason 和审计映射。

### 13.2 集成测试

- Delete/Restore 前后 Meta/Data PG、Doris、Kafka、OSS、Job、成员和配置不变。
- DISABLE 项目继续执行 migration。
- PM 多实例传播与断线恢复。
- Web、MCP、Internal API、Edge、ADTOL、ABOL、Connector、Scheduler、Dispatch/C1、MA、QE、LiveEvent、Wagent、Asset Behavior 逐项生命周期测试。
- Project/Organization Purge 资源矩阵和 Global 最终事务。

### 13.3 E2E

- 客户详情 Delete → 新请求/新任务拒绝 → Restore → 新工作恢复。
- 长期任务停止，短任务按约定完成，Restore 不等待收敛。
- Project Purge 中途失败 → Restore 拒绝 → 手动重试成功。
- Organization 逐项目 Delete/Purge，Restore 不级联。
- 非 OP、跨客户、ID/reason 错误和套餐有效额外确认。
- 六类动作成功、校验失败、执行失败都有脱敏审计。
- 租户接口、Controller 和前端调用均已移除。

## 14. 主要风险与取舍

| 风险 | 应对 |
| --- | --- |
| 入口绕过 PM | 逐项覆盖已发现的 MCP、Internal、LiveEvent、Wagent 和 Scheduler 旁路 |
| PM 通知丢失 | 关键写报错、本地同步、重订阅和快照对账 |
| 长期任务继续消费 | Scheduler Worker/lease heartbeat 中心取消，不逐组件建协调器 |
| 历史 DISABLE 被误 Restore | 前端开放前由用户人工清理，不在代码中维护双语义 |
| 长期 Delete 被误认为绝对无损 | 明确保留项与不补偿项，不建设重放和 TTL 冻结 |
| Purge 漏资源 | 固定 owner 清单、资源级测试、幂等步骤和主记录最后删 |
| 改动扩散成框架 | 规则留在现有 Service，真实本地状态用现有 Hook/函数处理 |

## 15. 下一步

详细设计已生成于 [04-detail.md](./04-detail.md)，包含具体文件、函数、SQL、OpenAPI、前端组件、资源 owner 和测试命令。下一步先评审 detail；确认后再生成 tasks 并进入开发。

## Quality Gates

- [x] 数据模型和 API 字段明确。
- [x] Delete/Restore/Purge 主流程、错误和并发边界明确。
- [x] 所有已发现的 PM 旁路和本地项目资源有明确处置。
- [x] 长期 Restore 的保留项与不补偿项明确。
- [x] 单元、集成、组件和 E2E 测试策略明确。
- [x] Rollout 明确历史 DISABLE 由用户人工清理。
- [x] simplify 已删除多余状态、清理工具、协调器、执行表和补偿系统。
