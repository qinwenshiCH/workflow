# Amplitude API 限流、凭证设计与商业策略调研

> 调研日期：2026-07-20  
> 调研范围：Amplitude 官方公开文档与定价页面  
> 文档性质：竞品调研，所有结论均有公开官方证据支持  
> 阅读建议：先读核心发现，再按"现状→设计逻辑→商业策略→启示"顺序阅读

## 核心发现

Amplitude 没有一个叫 "Account API" 的统一入口。它有 6 种凭证，分布在 Analytics、Data、Experiment 三个相对独立的产品线上。限流也因此按凭证和 endpoint 分散——Experiment Management API 是 100 req/s + 100,000 req/day，Dashboard REST 是 5 并发，User Profile 是 600/min——不存在一个统一的 QPS 值。

这种分离不是历史债，而是最小权限原则的有意设计。凭证的作用域（project / organization / account）天然不同，统一反而会带来安全风险。

对 Wave 最关键的五条线索：

1. **凭证是身份，不天然是容量**——Data API Token 最接近 Wave 的 Account API Token，它继承 account 全局权限，但不代表是一份独立套餐容量。
2. **至少存在四类限制维度**——请求速率、时间窗口预算、并发容量、资源加权成本；不是所有 API 都适用同一个 QPS。
3. **MCP 是平台能力而非单独计费项**——官方页面说明 MCP 对每个 plan（含 Free）可用，但有套餐 volumes/capabilities 限制；没有公开的 MCP 单次调用价目表。
4. **套餐、限流、凭证权限是三层不同的事**——套餐决定能力 entitlement，限流保护系统，RBAC 控制谁可以访问什么。
5. **商业模型以组织套餐和事件用量为锚点**——Free / Plus / Growth / Enterprise 的区分核心是月度事件量、MTU 和 feature access，API 限流数值是背后的实现参数而非直接售卖商品。

## 一、Token 分类与限流现状

Amplitude 的 6 种凭证分属三个产品线，各自作用域不同：

| 凭证 | 产品归属 | 作用域 | 可公开 | 可调用 API |
| --- | --- | --- | --- | --- |
| Project API Key | Analytics | 单 project | ✅ | HTTP API v2（事件写入）、Identify API、User Mapping API |
| Project Secret Key | Analytics | 单 project | ❌ | Export API、Dashboard REST API、User Activity API、User Search API、User Privacy API v1 |
| Org-level Key/Secret | Analytics | 组织 | ❌ | Audit Logs API、DSAR / User Privacy API v2（GDPR 数据删除） |
| Data API Token | Data | 账号级 | ❌ | Amplitude Data 产品认证（Tracking Plan 管理、Ampli CLI）；**具体 REST endpoint 集合官方未完整公开** |
| Deployment Key | Experiment | 单部署 | 分场景 | Evaluation API（实验变体实时获取，**无公开限流文档**） |
| Experiment Mgmt API Key | Experiment | 单 project | ❌ | Experiment Management API（Feature Flag、实验、互斥组等 CRUD） |

> [!NOTE]
> Data API Token 的 "account 全局权限"不等于它可以调所有 REST endpoint。具体 endpoint 仍可能要求 Project Secret Key、Org-level Key 或 Experiment Mgmt API Key，需以对应 API 文档的认证要求为准。

各 API 的限流数值（均为 Amplitude 官方文档公开值）：

| API | 限流值 | 作用域 | 超限行为 |
| --- | --- | --- | --- |
| Experiment Management API | 100 req/s + 100,000 req/day（UTC 重置） | per project | 429；另有 401（无效/已撤销 key）、403（无环境权限） |
| Dashboard REST API | 最多 5 并发（跨所有 REST 端点） | per project | 429 |
| User Activity / User Search | 最多 10 并发，360 queries/hour | per project | 429 |
| Dashboard REST（基于 cost） | ≤ 1,000 cost / 5 min，≤ 108,000 cost / hour | per project | 429 |
| User Profile API | 600 req/min（所有 endpoint 共享） | organization | 429；需更高上限时联系 Support |
| HTTP V2 事件写入 | Starter: 100 batches/sec, 1,000 events/sec；另按 user 维度: > 30 events/sec 可能被限 | per project + per user/device | 429；建议暂停约 30 秒后重试 |
| User Privacy API v1 | 1 req/s，每次 ≤ 100 user_id，≤ 8 并发/project | per project | 未明确 |
| Export API | 响应体 < 4GB，查询跨度 ≤ 365 天 | per project | **未找到请求频率限制文档** |
| Audit Logs API | 查询范围 ≤ 30 天，数据保留 90 天 | organization | **未找到请求频率限制文档** |
| DSAR API | POST = 8 cost，GET = 1 cost，所有端点共享 cost/小时预算 | organization | 429 |
| User Mapping API | 每批 < 2000 条 / < 1MB；30 秒窗口内最多 1500 次（约 50/s） | per project | 限制 |
| Identify API | 与 HTTP v2 共享限流逻辑 | per project | 429，建议暂停 15 秒重试 |

> [!NOTE]
> **文档缺口**——以下 API 的 Amplitude 官方文档中未找到明确限流数值：Data API Token（全部）、Deployment Key / Evaluation API、Export API 的请求频率、Audit Logs API 的请求频率。
>
> 此外，HTTP API v2 另有请求体限制：单次请求体 < 1MB，每次 < 2000 个事件；超出返回 413。Data API Token 生成时需立即复制，之后无法再次查看。Organization-level API Key 需通过 Amplitude Support 获取，非自助配置。

**限流表证据来源**——每条数据来自对应的 Amplitude 官方文档：[Experiment Management API](https://www.amplitude.com/docs/apis/experiment/experiment-management-api)、[Dashboard REST API](https://amplitude.com/docs/apis/analytics/dashboard-rest)（含 cost、User Activity、User Search）、[User Profile API](https://amplitude.com/docs/apis/analytics/user-profile)、[HTTP V2 API](https://amplitude.com/docs/apis/analytics/http-v2)（含 Identify API、User Mapping API）、[Export API](https://amplitude.com/docs/apis/analytics/export)、[User Privacy API](https://amplitude.com/docs/apis/analytics/user-privacy)（含 v1/v2/DSAR）、[Audit Logs API](https://amplitude.com/docs/apis/analytics/audit-logs)。

**限流的四种维度**——同样叫 "limit" 在 Amplitude 里有不同含义：

- **请求速率**：如 Experiment Mgmt API 的每秒请求数
- **时间窗口预算**：如每日请求数、每小时 query 数
- **并发容量**：如 Dashboard REST 的并发请求数
- **资源加权成本**：复杂查询按 days × conditions × query type 换算 cost

> 因此，如果将 Wave 的 API 额度只设计成一个 `qps`，会覆盖不了高成本查询场景。对 Dashboard REST 来说，提高 QPS 无法解决长查询占满后端的问题；对 User Profile API 来说，每日总量比瞬时 QPS 更重要。

**凭证生命周期限制**——一个 project 最多 50 个 active API keys；撤销是永久操作，撤销后最长 6 小时完全生效。这影响 Wave 对 token 数量、撤销生效时间和事件响应的产品承诺。

## 二、设计逻辑：凭证分离和限流拆分是有意选择

### 2.1 凭证为什么分成 6 种，而不是一个统一 Token

Amplitude 三个产品线（Analytics / Data / Experiment）历史上有不同起源，但凭证不统一更核心的原因是三个有意选择：

| 设计决策 | 解释 | 安全含义 |
| --- | --- | --- |
| 作用域不同，不适合同一把钥匙 | Project Key 不能跨项目，Org-level Key 权限更大 | 统一意味着必须给最大权限，反而不安全 |
| 安全隔离是目标，不是限制 | 不同凭证泄露的影响面不同 | 最小权限原则的刻意实现 |
| 不同凭证面向不同角色 | 前端用 Project API Key，后端用 Secret Key，合规用 Org-level Key | 角色天然需要不同访问级别 |

> 对 Wave 研究的含义：Amplitude 分开凭证不是因为历史债，而是因为不同产品、不同作用域、不同角色统一反而会带来安全风险和权限混乱。这种设计在大型 SaaS 平台中很常见（类比 AWS IAM：一个 root 账号但最佳实践是给每个服务/角色单独的最小权限凭证）。

### 2.2 限流为什么不是统一数值

Amplitude 至少有两种限流设计意图：

**保护后端容量**——Dashboard REST、User Profile、事件写入的限流是在保护共享后端资源。其中 Dashboard REST 引入了 cost 计算（cost = days × conditions × query type cost），说明价格越高的查询，即使请求数很少，也应该被更严格地控制。

**保护公平性**——HTTP V2 事件写入在 project 级限流外还叠加了 user/device 维度（> 30 events/sec），防止单个用户影响同 project 的其他用户。Experiment Management API 同时限制速率和日总量，防止单个 project 耗尽组织级共享资源。

### 2.3 cost 体系的启示

Dashboard REST 的 cost 计算值得注意：它不是简单的"一次请求扣一分"，而是按查询复杂度加权（查询天数 × 条件数 × 查询类型）。这是 Amplitude 对"QPS 不等于后端成本"的明确表达——一个跨 365 天的 cohort 分析和一个 1 天的简单计数，消耗的后端资源可以差 100 倍。

对 Wave，这意味着对于数据查询类 API，应区分"简单请求"和"高成本查询"并分别保护，而不是统一用一个 QPS 值。

## 三、商业策略

### 3.1 套餐层级与定价逻辑

Amplitude 公开定价页面的四个套餐，核心区分锚点是"事件量"和"产品能力"而非 API QPS：

| 套餐 | 公开核心信息 | 商业逻辑 |
| --- | --- | --- |
| Free | 每月最多 2M events，无需信用卡 | 降低试用门槛 |
| Plus | 按月 tracked users / event volume 扩展，超额同单位计价；Plus 有 MTU guardrail | pay-as-you-go 用量模型 |
| Growth | 按 volume、features 等定制 | 匹配中型团队需求 |
| Enterprise | 按 volume、features、add-on 等定制报价 | 大客户合同化 |

**事件量超额机制**——付费账户在月度事件量达到 80%、90%、100%、110% 时触发提醒，超额可能产生费用。非付费账户的超额处理不同，详见官方 Limits and quotas 页面。

> 证据：[Amplitude Pricing](https://www.amplitude.com/pricing)、[Billing and plans](https://amplitude.com/docs/faq/billing-and-plans)、[Limits and quotas](https://amplitude.com/docs/faq/limits-and-quotas)

### 3.2 API 限额与计费额度的关系

Amplitude 的 API 文档给出的是 operational limits（429、并发数、速率），而非面向客户的价目表。计费文档围绕 event volume、MTU、套餐 overage 展开。

当前最有证据支持的三层模型：

```text
套餐/合同 entitlement      → 能不能使用某种能力、多少月度产品容量
API operational limit      → 短时速率、并发、查询成本、每日请求保护
凭证与 RBAC                → 谁能访问哪些 organization / project / resource
```

这三层都可能让请求失败或产生费用，但语义不能混为一个 QPS 配置。

### 3.3 MCP 的商业定位

Amplitude 把 MCP 定位为**平台能力而非独立计费产品**：

- MCP 对所有 plan（含 Free）可用
- 不同 plan 有不同 limited volumes / capabilities
- Growth / Enterprise 可购买 dedicated add-ons
- **没有公开的 MCP 按 tool call、token 或 request 单次计费的价格表**

> [!NOTE]
> 不能把"没有公开单次 MCP 价格"写成"Amplitude 不会按合同收费"。Enterprise 的具体能力、容量、add-on 和 overage 仍可能以合同为准。本次公开资料核查无法确认。

## 四、MCP 权限治理（非限流，但影响产品设计）

Amplitude MCP 的公开设计重点在权限治理而非限流数值：

- **OAuth 认证**——使用现有 Amplitude 用户权限，不是独立凭证
- **组织可禁用**——开关位于 organization 设置
- **project 级 RBAC**——`USE_MCP_READ` / `USE_MCP_WRITE` 可按 project 分配给角色、组或 service account
- 没有公开的统一 MCP 数值 QPS，不能据此宣称无限制

> 证据：[Amplitude MCP](https://amplitude.com/docs/amplitude-ai/amplitude-mcp)、[Docs MCP Server](https://amplitude.com/docs/amplitude-ai/docs-mcp-server)

## 五、对 Wave 的启示

### 5.1 Account API Token 的产品定位

建议将 Account API Token 定义为**客户组织已购买的机器接入能力的凭证**，而非"一个 token 一份 QPS"：

| 层 | 建议承担的职责 | 不建议承担的职责 |
| --- | --- | --- |
| organization / plan | 决定 API/MCP 是否可用、组织级商业额度 | 不直接作为所有请求的唯一定价桶 |
| project | 决定可访问的数据资源和主要运行时保护边界 | 不允许通过创建 project 无限扩容组织能力 |
| token / account | 身份、撤销、审计、token 级公平性保护 | 不把每个 token 当成一份独立套餐容量 |
| endpoint / resource | 决定高成本查询的独立并发或成本限制 | 不让所有操作共享一个等价 QPS |
| organization aggregate | 防止通过创建多个 project 或 token 叠加超出套餐的容量 | 不影响正常运行时的资源隔离 |

### 5.2 OpenAPI 与 MCP 的关系

工作假设：OpenAPI 与 MCP 可归入同一个 `machine_api` 套餐 entitlement。客户购买的是"程序化访问和自动化能力"，不是某一种传输协议。

但运行时限制不必完全相同：

- OpenAPI 的 endpoint 可以按请求数、cost 或并发计量
- MCP 的每次 tool call 应使用相同的 resource permission，并映射到底层操作成本
- MCP 不应成为绕过 OpenAPI 总额度或 project 权限的旁路
- 读写能力应分离，写操作和高成本查询应有更严格的保护

> 这是 Wave 的产品方向建议。需要确认的关键事实是：MCP tool call 是否和 OpenAPI 请求共享同一份底层资源预算。

### 5.3 不建议第一版只卖 QPS

Amplitude 的公开设计至少展示了三类可售卖/可运营的价值：

1. **能力价值**——是否允许机器访问、自动化和 MCP
2. **容量价值**——月度事件量、MTU、查询/调用预算
3. **可靠性价值**——并发、响应时间、专属 add-on 或企业化容量

对 Wave，更值得验证的产品包装是"组织 API 能力 + 包含容量 + 超额/升级路径"，而不是只销售一个抽象 QPS。QPS 可以作为实现和套餐中的一个参数，但不应直接代表全部商业价值。

### 5.4 区分不同的"请求失败"语义

在研究 Wave 限流时，建议明确区分以下错误类型，而不混为一类：

| 失败类型 | HTTP 类比 | 产品反馈 |
| --- | --- | --- |
| entitlement denied（未购买能力） | 403 | 引导升级套餐 |
| quota exceeded（用量耗尽） | 429 / 403 | 展示剩余量、重置时间、升级入口 |
| rate limited（瞬时超限） | 429 + Retry-After | 告诉调用方何时重试 |
| dependency unavailable（依赖故障） | 502 / 503 | 服务降级告知 |
| auth failed（认证失败） | 401 | 凭证无效/已撤销 |

### 5.5 建议的研究模型至少包含这些字段

如果未来将研究成果转化为数据模型，建议至少覆盖以下字段（仅供参考，非本期实现承诺）：

| 字段 | 含义 |
| --- | --- |
| `organization_id` / `plan_id` | 商业归属和套餐能力 |
| `project_id` | 资源与权限边界 |
| `credential_id` / `token_id` | 凭证身份、撤销和公平性维度 |
| `channel` | `openapi`、`mcp`、`session` 等入口 |
| `endpoint_or_tool` | 具体操作及其资源成本 |
| `rate_limit` / `concurrency_limit` | 瞬时保护参数 |
| `window_budget` / `cost_unit` | 每日、每小时或加权查询预算 |

## 六、未确认问题

以下问题不能从当前公开资料安全推出结论：

| 问题 | 为什么重要 | 建议验证方式 |
| --- | --- | --- |
| MCP 是否有数值 rate/concurrency limit（per org / user / service account / project / tool）？ | 决定 Wave 是否需要单独的 MCP limiter | 用测试组织进行受控压测，记录 429、响应头和错误体 |
| MCP tool call 是否消耗 Dashboard REST 的 query cost / concurrency budget？ | 决定 OpenAPI/MCP 能否共用一个成本模型 | 对同一 project 发起等价 REST 与 MCP 查询，观察 quota/cost/429 行为 |
| Data API Token 可访问的具体 endpoint 集合和各 endpoint 限额是什么？ | Data API Token 是 account 全局权限，存在跨 project 放大风险 | 建立最小权限测试账号，逐 endpoint 验证 401/403/429 |
| MCP 的 Growth/Enterprise dedicated add-on 包含什么容量和 SLA？ | 决定是否能把 MCP 作为可售卖 API 能力 | 获取销售/合同条款 |
| REST API 的限额是否会按合同提高？ | 避免把公开默认值误当作企业硬上限 | 以具体账号合同和 Support 回复为准 |

## 七、证据索引

所有引用来源均为 Amplitude 官方文档：

1. [Keys and Tokens](https://amplitude.com/docs/apis/keys-and-tokens)
2. [Manage your API keys and secret keys](https://amplitude.com/docs/admin/account-management/manage-your-api-keys-and-secret-keys)
3. [Experiment Management API](https://www.amplitude.com/docs/apis/experiment/experiment-management-api)
4. [Dashboard REST API](https://amplitude.com/docs/apis/analytics/dashboard-rest)
5. [User Profile API](https://amplitude.com/docs/apis/analytics/user-profile)
6. [HTTP V2 API](https://amplitude.com/docs/apis/analytics/http-v2)
7. [Export API](https://amplitude.com/docs/apis/analytics/export)
8. [User Privacy API](https://amplitude.com/docs/apis/analytics/user-privacy)（v1 / v2 / DSAR）
9. [Audit Logs API](https://amplitude.com/docs/apis/analytics/audit-logs)
10. [Amplitude MCP](https://amplitude.com/docs/amplitude-ai/amplitude-mcp)
11. [Docs MCP Server](https://amplitude.com/docs/amplitude-ai/docs-mcp-server)
12. [Amplitude Pricing](https://www.amplitude.com/pricing)
13. [Billing and plans](https://amplitude.com/docs/faq/billing-and-plans)
14. [Limits and quotas](https://amplitude.com/docs/faq/limits-and-quotas)
