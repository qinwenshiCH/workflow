# 外部产品调研：Activity / Audit Log 设计参考

**调研时间**: 2026-07-01  
**目的**: 不是找“谁和 Wave 一模一样”，而是把外部成熟方案分成几种 archetype，搞清楚它们各自审计什么、为什么那样设计、哪些点能借鉴、哪些点不能照搬。

---

## 这组调研到底要回答什么

读这组文档，核心是回答 4 个问题：

1. 这个产品审计的“真相对象”到底是什么：业务资源、API 请求，还是数据库行？
2. 它的审计入口放在哪里：应用 service、统一 API 网关，还是数据库 trigger？
3. 它把审计写到哪里：查询表、日志文件、远端 sink，还是 history table？
4. 它为什么这样选：产品形态、风险等级、消费方式、性能边界分别是什么？

如果这 4 个问题没有讲清楚，单看“字段列表”其实没法指导 Wave 的方案设计。

---

## 先建立 3 个 archetype

这 5 个案例本质上分成 3 类，不要混着看。

| archetype | 代表产品 | 审计真相对象 | 入口 | 落点 | 最适合回答的问题 |
|---|---|---|---|---|---|
| **产品资源活动日志** | Grafana、Harbor | 资源操作事件 | 应用层 API / service / event handler | 日志 exporter 或业务查询表 | “谁改了哪个资源” |
| **控制面安全审计** | Kubernetes、Vault | API request / response | 统一 API 入口 | file / webhook / audit device | “谁访问了系统边界” |
| **数据库行历史** | go-history | 行级 before / after | PostgreSQL trigger | history table | “这行数据以前长什么样” |

Wave V1 最接近第一类，最多从第二类借鉴敏感字段和失败策略边界，不应该被第三类主导。

---

## 阅读顺序建议

如果你是第一次进来，建议按这个顺序读：

1. [Harbor](./harbor.md)：最接近 Wave 的“管理员可查询的产品资源审计表”
2. [Grafana](./grafana.md)：最接近 Wave 的“应用层生成事件，但 sink 更偏日志系统”
3. [Kubernetes](./kubernetes.md)：理解“为什么安全审计天然是 request 视角，而不是业务对象视角”
4. [Vault](./vault.md)：理解“为什么高安全系统会把审计当成安全边界的一部分”
5. [go-history](./go-history.md)：理解“为什么 trigger 方案适合 history，不适合 business activity”

---

## 五个案例的总览

| 产品 | 一句话模型 | 有没有审计表 | 主要模块/机制 | 为什么这样设计 |
|---|---|---|---|---|
| [Grafana](./grafana.md) | 审计是 API/UI action 产生的 JSON event，然后输出到 file / Loki / console | **没有内建 PG audit table** | API action, audit event, exporter | Grafana 原生就依赖日志生态消费审计，读路径不要求内建对象表 |
| [Harbor](./harbor.md) | 审计是 Harbor event 被 handler 转成 `AuditLogExt` 再入库，可选再转发 | **有**：`audit_log_ext`，兼容旧 `audit_log` | notifier, handler, manager, DAO, read API | Harbor 有内建管理员 UI，天然需要可分页查询的业务审计表 |
| [Kubernetes](./kubernetes.md) | 审计是 kube-apiserver 对每个 request 在不同 stage 生成的 AuditEvent | **没有** | audit policy, audit context, log/webhook backend | Kubernetes 的核心边界是 API server，统一入口天然比业务 service 更重要 |
| [Vault](./vault.md) | 审计是每个 API request/response 写到所有 enabled audit devices，至少一个成功才放行 | **没有** | audit device, HMAC, elision, availability guarantee | Vault 是 secret 系统，审计是安全边界的一部分，不只是运维日志 |
| [go-history](./go-history.md) | 审计是 PostgreSQL trigger 把行级 before/after 写入 history table | **有**：history table | DDL generator, driver wrapper, session metadata, trigger | 它的目标是覆盖所有行变更，不是表达业务操作语义 |

---

## 一张表看清它们的决策差异

| 维度 | Grafana | Harbor | Kubernetes | Vault | go-history | Wave V1 应取什么 |
|---|---|---|---|---|---|---|
| 审计对象 | action + resources | operation + resource | request lifecycle | request/response | row change | item activity |
| 最小主键思维 | event stream | table row | audit event | audit entry | history row | activity row |
| 写入层 | 应用层 | 应用层 / 事件层 | API server | API boundary | DB trigger | 应用层 |
| 读路径 | 日志后端 | UI/API 查询表 | 外部日志平台 | 外部日志平台 | viewer / SQL | OP / 内部查询 |
| 敏感字段策略 | 尽量别进 event | 摘要化 | policy 控制 | 默认 HMAC / elide | 很难写入前脱敏 | 写入前 mask/drop |
| 失败策略 | 记录导向 | 记录导向 | backend mode 可配置 | 至少一个 device 成功 | 跟 DB 事务绑定 | 按场景 policy |
| 多对象表达 | `resources[]` | 资源行 + 描述 | objectRef + annotations | request path + data | 每行独立 | `correlation_id` 串联多行 |
| 适合 Wave 的程度 | 高 | 高 | 中 | 中低 | 低 | 主体参考 Harbor/Grafana |

---

## Wave 最该借鉴什么

### 1. 从 Harbor 学“查询闭环”

Harbor 说明了一件很重要的事：如果产品内真的有“管理员看审计列表”这个消费路径，那么最终通常会落成一张**可分页查询的业务表**，而不是只写日志。

Wave 当前主需求就是 OP / 内部按对象排障，所以 `activity_log` 表是正路。

### 2. 从 Grafana 学“应用层生成事件”

Grafana 说明：只要问题是“谁做了什么业务动作”，入口就应该在 API / service 层，而不是 DB trigger。

Wave 继续坚持业务层显式写入是对的，因为只有业务层知道：

- 操作人是谁
- 来源是 web / openapi / internal / backfill
- 这次是 create / update / delete / copy 里的哪一种
- 哪些字段是噪音，哪些字段值得进 `changes[]`

### 3. 从 Kubernetes 学“不要把 request 全塞进 detail”

Kubernetes 的 level/stage 设计是在提醒我们：完整 request/response 很贵，而且很容易把敏感数据、噪音数据、巨大 payload 一起打进审计。

Wave 的 `detail` 一定要是稳定投影，不能是“顺手把当前 DTO 全序列化一下”。

### 4. 从 Vault 学“敏感字段和失败策略必须先定义边界”

Vault 的重点不是字段多，而是：

- 明文 secret 默认不进审计
- 审计设备不可用时，系统行为有明确契约

这两个边界 Wave 也必须清晰，只是不能默认做成 Vault 那种强安全 fail-closed。

### 5. 从 go-history 学“它解决的是另一类问题”

go-history 很适合做 history，不适合做 business activity。

因为它天然回答的是：

- 这行数据之前是什么值

而不是：

- 为什么发生了这次业务动作
- 这是不是一次 copy / release / relink / internal conflict resolution
- 这次操作影响了几个对象

---

## 对当前 Wave 方案的总判断

结合这 5 个案例，Wave V1 最合理的定位仍然是：

- **主模型**：Harbor / Grafana 风格的产品资源活动日志
- **主入口**：应用层显式写入
- **主落点**：PostgreSQL `activity_log` 查询表
- **detail 策略**：比 Harbor / Grafana 稍强，保留 `changes / extra / snapshot`
- **敏感字段和失败策略**：借鉴 Vault/Kubernetes 的边界意识，但不把 V1 做成安全审计系统
- **明确排除**：不以 go-history / PostgreSQL trigger 为主方案

---

## 配套文档

- [Grafana](./grafana.md)
- [Harbor](./harbor.md)
- [Kubernetes](./kubernetes.md)
- [Vault](./vault.md)
- [go-history](./go-history.md)

---

## 来源

- Grafana audit logs: <https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/audit-grafana/>
- Grafana repository: <https://github.com/grafana/grafana>
- Harbor audit log: <https://goharbor.io/docs/main/administration/audit-log/>
- Harbor repository: <https://github.com/goharbor/harbor>
- Kubernetes auditing: <https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/>
- Kubernetes API audit config: <https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/>
- Vault audit devices: <https://developer.hashicorp.com/vault/docs/audit>
- Vault repository: <https://github.com/hashicorp/vault>
- go-history repository: <https://github.com/mickamy/go-history>
