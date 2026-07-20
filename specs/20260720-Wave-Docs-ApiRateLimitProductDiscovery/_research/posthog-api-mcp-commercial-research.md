# PostHog API / MCP 限流、商业化与实现方案调研

> 调研日期：2026-07-20  
> 调研范围：PostHog 本地源码快照 `/Users/wenshiqin/go-project/posthog-master/`，以及 PostHog 官方公开定价与产品策略资料。  
> 调研目的：为 Wave 的 Account API Token、Session Token、OpenAPI/MCP 机器接入、组织套餐与请求保护提供商业策略和需求讨论参考。  
> 文档性质：竞品研究与需求讨论材料，不是实现 spec，也不代表 Wave 已确认决策。

## 一、结论先行

PostHog 的关键不是“给每个 API token 设置一个 QPS”，而是把不同问题拆成多层控制：

```text
组织 / 套餐       决定购买了哪些产品能力、长期用量和 quota
项目 / Team       决定数据隔离、资源归属和部分运行时聚合
凭证 / API key    决定身份、scope、撤销、审计和公平性
接口 / 资源       决定请求速率、查询成本和并发上限
Redis / Cache     承载短时计数、并发占用和 quota enforcement
```

最值得 Wave 借鉴的五点：

1. **商业归属主要在 organization，运行时保护不只在 organization。** PostHog 的 billing usage 是 organization 级；但 API rate limit 会按 personal API key、project/team、接口类型分别处理，查询并发还区分 team 和 organization。
2. **API key 是访问凭证，不是独立商业容量。** PostHog 对 Project Secret API Key 同时做“单 key 限制”和“team aggregate 限制”，明确防止客户通过创建多个 key 叠加容量。
3. **QPS 不能代表高成本查询的全部保护。** HogQL、ClickHouse 和 API query 有单独 rate/concurrency；API query 的商业 quota 甚至以 `api_queries_read_bytes` 计量，而不是简单请求数。
4. **MCP 不是另一套绕过权限的 API。** MCP 以 token hash 识别调用方，先做认证、scope/tool filtering、project/org pinning，再做 480/min + 4800/hour 的 burst/sustained 限流；工具批量、请求体大小和参数也有限制。
5. **PostHog 的故障取舍偏向“限流依赖故障时放行业务请求”。** MCP Redis 计数失败时 fail-open；REST throttle 的异常路径也返回 allow。与此同时，MCP 进程启动时生产环境要求 Redis 可连接，上游收到 429 则最多重试 3 次、总等待预算 30 秒。这是“运行期不过度扩大故障面，启动期保证依赖存在”的组合，而不是无条件放行。

这些结论中，前四点有源码或官方资料直接支持；对 Wave 的具体套餐模型、限值和 fail-open 边界仍是产品推论，不能直接复制 PostHog 的数值。

## 二、PostHog 的对象模型：组织、项目、凭证不是一回事

PostHog 源码里的历史命名容易造成误解：产品界面称 Project，核心模型常称 `Team`。`Team` 具有 `organization_id`，因此本调研把它表示为“项目 / Team”。

| 对象 | PostHog 语义 | 对 Wave 的研究意义 |
| --- | --- | --- |
| Organization | 商业、成员、可用产品 feature、usage、billing period 的聚合单位 | 更适合承载套餐、购买能力、月度用量和组织级 spend/quota |
| Project / Team | 数据与资源隔离单位；项目有自己的 API token | 更适合承载资源边界、项目级权限、部分 API aggregate |
| Personal API Key | 用户创建的机器凭证；可以 scope 到组织或项目；带 API scopes | 适合自动化、审计、撤销和最小权限，不应天然获得独立套餐容量 |
| Project Secret API Key | 绑定一个项目的项目级机器凭证；不依赖创建者仍是成员 | 适合服务端 ingestion、Feature Flags、Endpoints 等 project-scoped 程序访问 |
| Session auth | 登录用户会话；正常情况下绕过 API scope，但仍受成员关系和访问控制约束 | Session Token 应单独做反滥用和攻击保护，不宜直接当成 Account API Token 的替代品 |
| MCP session | 在 token/userHash、组织、项目、MCP session 等上下文中执行 tool call | MCP session 只解决协议上下文，不应成为权限或套餐边界本身 |

源码证据：[`PersonalAPIKey` 模型](/Users/wenshiqin/go-project/posthog-master/posthog/models/personal_api_key.py:34)、[`ProjectSecretAPIKey` 模型](/Users/wenshiqin/go-project/posthog-master/posthog/models/project_secret_api_key.py:13)、[`APIScopePermission`](/Users/wenshiqin/go-project/posthog-master/posthog/permissions.py:511)、[`Organization.get_plan_tier`](/Users/wenshiqin/go-project/posthog-master/posthog/models/organization.py:396)。

### 2.1 Personal API Key 的安全和生命周期要求

源码体现出一套完整的机器凭证产品，而不是只生成一串 token：

- 每个用户最多创建 10 个 personal API key；创建时保存 secure hash、masked value、label、created/last-used/rolled 时间，原始 token 只在生成时返回。[`posthog/api/personal_api_key.py`](/Users/wenshiqin/go-project/posthog-master/posthog/api/personal_api_key.py:21)
- key 可以携带 `scopes`、`scoped_teams`、`scoped_organizations`；scope 校验和组织/项目范围校验是两个不同步骤。[`PersonalAPIKey` 字段](/Users/wenshiqin/go-project/posthog-master/posthog/models/personal_api_key.py:34)、[`APIScopePermission.check_team_and_org_permissions`](/Users/wenshiqin/go-project/posthog-master/posthog/permissions.py:616)
- personal key 支持 roll/revoke 等生命周期操作；`last_used` 用于管理和审计，而不是用于限流身份本身。[`PersonalAPIKeyViewSet`](/Users/wenshiqin/go-project/posthog-master/posthog/api/personal_api_key.py:227)
- organization 可以通过安全设置禁止普通成员使用 personal API key，管理员例外。[`_check_organization_personal_api_key_restrictions`](/Users/wenshiqin/go-project/posthog-master/posthog/permissions.py:676)
- API 限流指标使用 hashed key，而不是把 raw token 作为标签。[`PersonalApiKeyRateThrottle`](/Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:177)

**产品含义**：如果 Wave 要允许 Account API Token 接入 OpenAPI/MCP，第一版最有价值的不是再做复杂 token 类型，而是补齐 token 的 scope、项目/组织范围、roll/revoke、last-used、审计和组织安全开关。QPS 是其中一个运行时属性。

## 三、商业逻辑：PostHog 卖的是“产品能力 + 用量”，不是单独卖 QPS

### 3.1 官方公开的定价定位

PostHog 官方主页明确采用 usage-based pricing：付费产品按使用量计费，并提供较大的月度免费额度；主页当前展示的示例包括 Product Analytics 1M events/月后按 event 计价、Session Replay 按 recording、Feature Flags 按 request、Data Warehouse 按 row。详见 [PostHog Pricing](https://posthog.com/)。具体数字可能随时间和地区变化，本节只引用其商业结构，不把页面数字作为 Wave 的目标值。

PostHog 自己对定价的说明更能解释其产品逻辑：

- 多数 app 有独立价格，定价是持续的产品决策，而不是一次性财务配置；
- ICP 是高增长 startup 的 product engineer，因此选择 self-serve、透明、usage-based pricing；
- 数据 pipelines 曾从“摄入 events”调整为“导出 rows / triggered events”，因为后者更贴近客户感知的价值；
- billing 页面、用量 dashboard、预估文档、spend limit、超额后的行为说明和必要时的退款/“side project insurance”共同降低账单恐惧；
- enterprise 需要折扣、合同、credits、invoice 和特殊付款方式等 billing flexibility。

证据：[PostHog 的定价方法与商业取舍](https://newsletter.posthog.com/p/non-obvious-pricing-advice-for-startups?isFreemail=true&post_id=176911234&publication_id=1318225&r=5b&triedRedirect=true)、[PostHog ICP 说明](https://newsletter.posthog.com/p/defining-our-icp-is-the-most-important?open=false)、[早期 self-serve 产品策略](https://newsletter.posthog.com/p/how-we-got-our-first-1000-users)。

### 3.2 源码中的 quota：组织聚合、资源分项、billing period

源码中的 `QuotaResource` 不是一个统一总额度，而是按可售卖/可计量资源拆分：events、exceptions、recordings、rows synced、feature flag requests、`api_queries_read_bytes`、survey responses、LLM events、rows exported、AI credits、workflow emails、logs 等。[`QuotaResource`](/Users/wenshiqin/go-project/posthog-master/ee/billing/quota_limiting.py:66)

每个 organization 的 usage summary 包含 `usage`、`todays_usage`、`limit` 和 billing period；超额判断按资源分别计算。部分资源有 overage buffer；Feature Flags 至少有 2 天 grace，AI credits 不享受 grace。[`org_quota_limited_until`](/Users/wenshiqin/go-project/posthog-master/ee/billing/quota_limiting.py:241)

quota 的商业状态和运行时状态是分开的：

```text
Organization.usage[resource]
  -> usage / todays_usage / limit / billing period / grace state
  -> quota_limited_until

Organization.teams[*].api_token
  -> Redis sorted set: resource -> team API token -> expiry timestamp
  -> 每个项目/Team 的请求或摄入路径快速判断是否受限
```

当 organization 被限制时，源码会把组织内各 Team 的 API token 写入 Redis sorted set，过期时间设为当前 billing cycle 结束；解除限制时反向移除。[`Organization.limit_product_until_end_of_billing_cycle`](/Users/wenshiqin/go-project/posthog-master/posthog/models/organization.py:407)、[`replace_limited_team_tokens`](/Users/wenshiqin/go-project/posthog-master/ee/billing/quota_limiting.py:140)

这说明 PostHog 的“商业边界”和“请求执行边界”有意分离：商业判断在 organization，实际 fast path 可以按项目 token 做。

### 3.3 Plan tier 与 resource limit 不一定都阻断请求

PostHog 还维护一个资源数量目录，例如 dashboard、insight、widget、alert、subscription、action 和 organization 级 AI summary；其中部分限制按 free/paid/enterprise tier 变化。[`LimitKey` 与 `REGISTRY`](/Users/wenshiqin/go-project/posthog-master/posthog/resource_limits/registry.py:5)

但该目录的 `check_count_limit` 明确是 notification-only：达到阈值时发出 `resource limit hit` 事件，不抛错、不阻断创建。[`check_count_limit`](/Users/wenshiqin/go-project/posthog-master/posthog/resource_limits/evaluator.py:30)

**产品含义**：不是所有“limit”都应该变成 429。至少需要区分：

- entitlement denied：套餐没有该能力，通常是 403 或产品引导；
- operational rate/concurrency：保护系统，通常是 429/Retry-After；
- billable quota：已购买能力但本周期用量耗尽，可能是降级、暂停、超额计费或到期恢复；
- soft resource limit：先通知、引导升级或清理，不立即阻断。

## 四、REST API 的限流和查询保护

### 4.1 默认 REST rate limit：burst + sustained

PostHog 的 DRF 默认 throttle 在环境开关启用时同时使用 burst 和 sustained 两层：

- `BurstRateThrottle`：除 capture/decide 外，默认 `480/minute`；
- `SustainedRateThrottle`：除 capture/decide 外，默认 `4800/hour`；
- 两个默认类的注释将 scope 描述为 per project，但实际 `get_cache_key` 对 personal API key 优先使用 key hash，其次才回退到 team、user 或 IP。

证据：[`DEFAULT_THROTTLE_CLASSES`](/Users/wenshiqin/go-project/posthog-master/posthog/settings/web.py:394)、[`BurstRateThrottle` 和 `SustainedRateThrottle`](/Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:362)、[`get_cache_key`](/Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:236)。

这是一个值得特别记录的源码 nuance：**不能只读注释就断言 default API key rate 是 per project，也不能只读 MCP 常量就断言所有 PostHog API 都是 per token。** 当前源码体现的是“API key 优先按 key 公平，非 key 请求再按项目/用户/IP 回退”，而某些 endpoint 还有额外 aggregate。

### 4.2 Project Secret API Key：单 key + Team aggregate

对于 project secret API key，源码定义了两层限制：

1. `PersonalOrProjectSecretApiKeyRateThrottle`：按 PSAK 本身计数；
2. `ProjectSecretApiKeyTeamRateThrottle`：按 team 聚合所有 PSAK 请求。

第二层的注释直接说明目的：防止客户通过创建很多 project secret key 来乘倍获得容量。[`ProjectSecretApiKeyTeamRateThrottle`](/Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:385)

**商业推论**：如果 Wave 允许一个组织创建多个 Account API Token，至少应有组织或项目聚合预算；否则“token 级 QPS × token 数量”会把套餐容量变成可无限自助扩容的漏洞。

### 4.3 高成本查询：rate 与 concurrency 分离

Query API 根据 action、组织 feature、团队配置和查询类型选择不同 throttle：

- `HogQLQueryThrottle`：`120/hour`；
- API query burst/sustained：`240/minute` 与 `2400/hour`；
- ClickHouse query：`240/minute` 与 `1200/hour`；
- Team 可以通过 `api_query_rate_limit` 覆盖 HogQL API query rate，例如 `200/day`；
- 组织 feature 可以启用 API query concurrency 检查。[`QueryViewSet.get_throttles`](/Users/wenshiqin/go-project/posthog-master/posthog/api/query.py:128)、[`Team.api_query_rate_limit`](/Users/wenshiqin/go-project/posthog-master/posthog/models/team/team.py:625)

请求 rate 之外，ClickHouse 查询有 Redis + Lua 的原子并发占用：

- API query per-team 默认最大并发为 `3`；
- app query per organization 默认最大并发为 `20`；
- dashboard query per organization 默认最大并发为 `6`；
- materialized endpoint、events list 等特殊 workload 还有各自 team limit；
- API query 可以配置 retry/backoff，超过并发时等待或抛出 `ConcurrencyLimitExceeded`；
- 组织级并发上限可以由 entitlement feature 决定，并缓存 1 小时。[`get_api_team_rate_limiter`](/Users/wenshiqin/go-project/posthog-master/posthog/clickhouse/client/limit.py:202)、[`RateLimit.run`](/Users/wenshiqin/go-project/posthog-master/posthog/clickhouse/client/limit.py:83)、[`get_org_app_concurrency_limit`](/Users/wenshiqin/go-project/posthog-master/posthog/clickhouse/client/limit.py:405)

**产品含义**：对于 OpenAPI/MCP，真正应该售卖或保护的可能是“查询吞吐/并发/扫描量”，而不是只给一个 QPS。长查询即使 QPS 很低，也可以耗尽 ClickHouse worker；并发限制才是保护后端的直接控制。

## 五、Feature Flags 与其他 endpoint 的分层保护

Feature Flags 的 Remote Config throttle 默认 `600/minute`，并支持按 team 覆盖；它与普通 API 限制分开。事件 API 使用 ClickHouse burst/sustained，event values 又有 `60/minute` 和 `300/hour` 的独立限制。[`RemoteConfigThrottle`](/Users/wenshiqin/go-project/posthog-master/products/feature_flags/backend/api/feature_flag.py:454)、[`Event API throttles`](/Users/wenshiqin/go-project/posthog-master/posthog/api/event.py:171)

这不是在说明 Wave 应照抄这些数值，而是说明 PostHog 的 endpoint 设计原则：

- 便宜、短请求可以共享默认保护；
- 查询、写入、实时配置等不同资源使用不同保护；
- 高风险/高成本操作不应和普通 CRUD 共用一个 bucket；
- endpoint 的限值是实现参数，不一定是客户直接购买的商品。

## 六、MCP 的产品需求和实现方案

### 6.1 MCP 的请求生命周期

PostHog MCP 的 Hono handler 顺序体现了产品安全边界：

```text
Bearer token
  -> token 格式与 session 初始化
  -> token hash / org / project 上下文
  -> scope 与 scoped project/org 过滤工具
  -> MCP rate limiter
  -> JSON-RPC body / batch / 参数校验
  -> tool dispatch 到 PostHog API
  -> 上游 429 重试或返回 rate-limit error
```

源码证据：[`MCP token validation`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/index.ts:183)、[`request properties`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/lib/request-properties.ts:8)、[`request state resolver`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/request-state-resolver.ts:90)、[`streamable handler`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/streamable-handler.ts:26)。

### 6.2 MCP 的限流维度和默认数值

MCP `RateLimiter` 会针对同一个 `userHash` 同时检查两个 fixed-window Redis counter：

- burst：`480/minute`；
- sustained：`4800/hour`；
- bucket key 形如 `mcp:rl:<scope>:<userHash>`；
- 超限返回 429，并带 `Retry-After`、`X-RateLimit-Limit`、`X-RateLimit-Remaining`、`X-RateLimit-Reset`、`X-RateLimit-Scope`；
- userHash 是 token 的 hash，不把 raw token 放入计数 key 或 telemetry。

证据：[`MCP rate-limiter defaults`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/rate-limiter.ts:23)、[`RateLimiter.check`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/rate-limiter.ts:37)、[`429 response headers`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/rate-limiter.ts:111)、[`MCP rate-limiter tests`](/Users/wenshiqin/go-project/posthog-master/services/mcp/tests/hono/rate-limiter.test.ts:111)。

这里的 `userHash` 实际由认证 token 计算而来，因而当前 MCP fast path 更接近**每 token 的公平性限制**，不是 organization aggregate。组织/项目权限和底层 API aggregate 仍在其他层处理。这个区分对 Wave 很重要：MCP 入口的 token limiter 不能替代组织商业 quota。

### 6.3 MCP 的 Redis 故障策略

`RateLimiter.check` 对每个 Redis limit 单独捕获异常并返回 `null`；调用方把 `null` 视为没有做出阻断决定，因此放行请求。测试明确覆盖“Redis incr 抛错时 fail-open”。指标名称也把这一行为写进 help 文案：Redis 失败时请求仍会被服务。[`RateLimiter`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/rate-limiter.ts:37)、[`mcp_rate_limit_errors_total`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/metrics.ts:72)、[`fail-open test`](/Users/wenshiqin/go-project/posthog-master/services/mcp/tests/hono/rate-limiter.test.ts:111)

但同一服务在生产启动时要求 `REDIS_URL`，连接失败会退出；Redis 客户端还配置了有限重试、无 offline queue、5 秒连接超时和 2 秒 command timeout。[`MCP Redis bootstrap`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/index.ts:11)

因此 PostHog 的实际策略不是“Redis 挂了就什么都不管”，而是：

- 启动时：把 Redis 当成运行依赖，避免服务处于不确定状态；
- 运行时：限流计数失败优先保护可用性，避免 Redis 故障把所有 MCP 请求变成 5xx；
- 运行后：用 metrics 记录 rate-limit error，允许运营发现限流暂时失效。

这是一条可供 Wave 讨论的参考线。第一版是否要本地 fallback，应取决于我们是否有证据表明 Redis 故障期间的攻击流量足以打崩后端；不要在没有攻击/故障数据前引入多级分布式 fallback。

### 6.4 MCP 本身还有请求形状和工具安全边界

MCP 不是只加一个 rate limiter：

- 单次 body 最大 `1 MiB`；
- JSON-RPC batch 最大 `100` 个请求；
- batch 内请求并行 dispatch；
- 每个 tool 的输入使用 schema 和字段上限；
- 认证失败返回 401；关闭或生命周期切换期间可能返回 503；
- telemetry 对 client-provided project ID 做数字格式、长度和数量上限，防止指标 cardinality/heap 被打爆。

证据：[`MCP dispatcher`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/dispatcher.ts:39)、[`MCP project telemetry bounds`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/rate-limit-telemetry.ts:10)、[`MCP entrypoint`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/index.ts:183)。

**产品含义**：MCP 付费能力应以“可调用哪些工具、能访问哪些项目、读写 scope、包含多少资源/查询预算”表达；batch/body/tool 参数是安全和稳定性约束，不必都暴露成客户套餐字段。

### 6.5 MCP 对上游 API 429 的处理

MCP 内部 API client 把 REST API 视为 rate limit 的权威来源：收到 429 后读取 `Retry-After`，没有有效 header 时使用带 jitter 的指数退避；最多重试 3 次，总等待预算 30 秒；耗尽后返回 `PostHogRateLimitError`。[`fetchJson` 429 retry policy](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/api/client.ts:25)、[`fetchJson`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/api/client.ts:341)

这意味着 MCP 有两层限流：

```text
MCP 入口 token limiter     防止一个 MCP client 持续打入口
上游 REST / query limiter   保护具体资源，并反映真实 endpoint 成本
```

两层都需要存在，但不能各自独立地给客户承诺一份可叠加容量；否则客户从 MCP 入口和 OpenAPI 入口同时调用时，会绕开我们想要的组织聚合预算。

## 七、PostHog 的“商业需求”可以还原成哪些产品能力

下面是从源码和官方商业资料归纳出的需求，而不是 PostHog 官方 PRD 原文。

### P0：机器访问必须是可治理的产品能力

- 组织能否使用 API/MCP 由产品 feature/plan 决定；
- token 必须可命名、查看 last used、roll/revoke；
- 支持 read/write scope 和 project/org scope；
- 组织管理员可以禁止普通成员使用 personal token；
- MCP tool 列表应随 token scope、项目范围和产品 feature 过滤；
- OpenAPI 与 MCP 不能绕过同一资源的权限和 quota。

### P0：限流必须保护“后端成本”，不只是保护 HTTP 入口

- 普通请求：burst + sustained；
- 高成本 query：独立请求预算；
- 长查询：独立 concurrency；
- 项目 secret key：per-key + project/team aggregate；
- organization：商业 quota 和跨项目聚合；
- batch/body/tool params：限制单次请求的放大倍数。

### P1：用量和超额行为必须可解释

PostHog 的官方定价观点把 billing dashboard、usage estimation、spend limits 和“达到 cap 后会发生什么”视为产品的一部分。对 Wave，至少需要明确：

- quota 是按请求数、字节、执行时间还是加权 cost；
- 是 hard stop、降级、超额计费还是仅告警；
- 组织内多个项目和 token 如何合计；
- 当前窗口剩余量、重置时间、升级入口在哪里展示；
- Redis/计量系统不可用时，如何避免错误扣费或错误阻断。

### P1：安全和可观测性要以 token hash 为基础

- 限流 key、metrics、日志不记录 raw token；
- project ID 等 client-controlled label 要做 cardinality 上限；
- 429 需要告诉调用方何时重试；
- 401、403、429、5xx、entitlement denied、quota exceeded 要能区分统计；
- 能按 organization/project/token/channel/tool 看趋势，但敏感 token 只显示 hash 或末四位。

## 八、对 Wave 的方案参考：推荐的分层，而非照搬 PostHog

### 8.1 推荐产品模型

当前最有价值的工作假设是：

| 层 | Wave 建议承担的职责 | 不建议承担的职责 |
| --- | --- | --- |
| Organization / plan | machine API/MCP 是否可用、包含容量、长期 quota、spend limit、跨项目聚合 | 不直接作为所有请求唯一 bucket |
| Project | 数据隔离、scope、资源归属、查询/写入的主要运行时边界 | 不允许通过创建项目无限扩容组织能力 |
| Account API Token | 身份、scope、撤销、审计、token 级公平性和突发保护 | 不把每个 token 当成一份独立套餐容量 |
| Endpoint / tool | 资源成本、请求权重、并发、body/batch 参数边界 | 不让所有操作共享一个等价 QPS |
| Session Token | 登录态安全、预认证/IP/device/行为反滥用、全局后端保护 | 不把浏览器会话直接当成长期机器凭证 |

### 8.2 OpenAPI 与 MCP 的商业包装

可以把 OpenAPI 和 MCP 放入同一个 `machine_api` entitlement：客户购买的是“程序化访问/自动化能力”，而不是某一种 transport。实现上仍保留两个入口 limiter，但它们应共享：

- 组织级 quota/cost ledger；
- project/resource 权限；
- 关键查询的 concurrency budget；
- token 撤销和审计；
- 429、quota exhausted、permission denied 的错误语义。

这样可以同时满足两个场景：

- 客户用 Account API Token 接入 OpenAPI，构建内部集成或付费自动化；
- 客户通过 MCP 接入 AI agent，但不能因为换了协议就获得另一份独立容量。

这仍需要 Wave 产品确认：第一期是只售卖“机器接入能力”，还是同时售卖“更高容量/更高并发/SLA”。在没有客户用量和成本数据前，建议先把 `machine_api` 作为能力开关和计量入口，具体套餐数值后置。

### 8.3 Redis 限流故障是否需要降级

PostHog 提供了一个可讨论的最小方案：

- 入口短时限流：Redis 失败时 fail-open，保证正常请求可用；
- 后端查询并发：仍由资源侧 limiter 保护，必要时等待或拒绝；
- 指标明确记录 limiter error；
- 启动时验证 Redis 依赖；
- 不立即引入本地计数、Redis fallback、数据库 fallback 三套状态。

对 Wave 的需求调研建议先回答三个问题，再决定是否做 fallback：

1. Redis 故障期间，攻击者是否能够绕过现有 ingress/WAF/网关，把请求量放大到足以拖垮服务？
2. 如果限流状态暂时丢失，客户最不能接受的是短暂放行，还是正常流量被 5xx 拒绝？
3. 我们是否已有稳定的进程内/网关级粗粒度保护，可以在 Redis 失效时接管？

在没有这些证据前，`fail-open + 告警 + 后端 concurrency/timeout` 是更符合 ponytail 原则的研究基线；“分布式精确 fallback”应作为风险触发后的增量方案，而非第一版产品需求。

## 九、源码证据索引

| 研究主题 | 关键源码 |
| --- | --- |
| REST 默认 throttle 与 key 维度 | [`posthog/rate_limit.py`](/Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:133)、[`posthog/settings/web.py`](/Users/wenshiqin/go-project/posthog-master/posthog/settings/web.py:394) |
| Project Secret API Key 聚合 | [`posthog/rate_limit.py`](/Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:385) |
| Query API 按查询类型选 throttle | [`posthog/api/query.py`](/Users/wenshiqin/go-project/posthog-master/posthog/api/query.py:128) |
| Query concurrency | [`posthog/clickhouse/client/limit.py`](/Users/wenshiqin/go-project/posthog-master/posthog/clickhouse/client/limit.py:83) |
| Team query rate override | [`posthog/models/team/team.py`](/Users/wenshiqin/go-project/posthog-master/posthog/models/team/team.py:625) |
| Organization usage/quota resource | [`ee/billing/quota_limiting.py`](/Users/wenshiqin/go-project/posthog-master/ee/billing/quota_limiting.py:66) |
| Organization quota 到项目 token enforcement | [`posthog/models/organization.py`](/Users/wenshiqin/go-project/posthog-master/posthog/models/organization.py:407) |
| Plan tier 与资源限制 | [`posthog/models/organization.py`](/Users/wenshiqin/go-project/posthog-master/posthog/models/organization.py:396)、[`posthog/resource_limits/registry.py`](/Users/wenshiqin/go-project/posthog-master/posthog/resource_limits/registry.py:21) |
| API scopes 与组织/项目约束 | [`posthog/permissions.py`](/Users/wenshiqin/go-project/posthog-master/posthog/permissions.py:511) |
| Personal / Project Secret API Key | [`posthog/models/personal_api_key.py`](/Users/wenshiqin/go-project/posthog-master/posthog/models/personal_api_key.py:34)、[`posthog/models/project_secret_api_key.py`](/Users/wenshiqin/go-project/posthog-master/posthog/models/project_secret_api_key.py:13) |
| MCP 入口、session 与 token hash | [`services/mcp/src/index.ts`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/index.ts:183)、[`services/mcp/src/lib/request-properties.ts`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/lib/request-properties.ts:8) |
| MCP rate limiter、Redis fail-open、429 headers | [`services/mcp/src/hono/rate-limiter.ts`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/rate-limiter.ts:23)、[`services/mcp/tests/hono/rate-limiter.test.ts`](/Users/wenshiqin/go-project/posthog-master/services/mcp/tests/hono/rate-limiter.test.ts:111) |
| MCP Redis 启动策略 | [`services/mcp/src/hono/index.ts`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/index.ts:11) |
| MCP batch/body 与 telemetry 保护 | [`services/mcp/src/hono/dispatcher.ts`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/dispatcher.ts:39)、[`services/mcp/src/hono/rate-limit-telemetry.ts`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/hono/rate-limit-telemetry.ts:10) |
| MCP 对上游 429 重试 | [`services/mcp/src/api/client.ts`](/Users/wenshiqin/go-project/posthog-master/services/mcp/src/api/client.ts:25) |

## 十、事实、推论和未确认项

### 已由当前源码直接确认

- REST 有 burst/sustained 多层限流，且 API key、team、user、IP 等身份维度会影响 bucket key；
- PSAK 有 per-key 与 team aggregate；
- Query API 有 rate 与 ClickHouse concurrency 两套保护；
- billing quota 按 organization/resource 计算，并映射到项目 API token 的 Redis 状态；
- MCP 默认 480/min、4800/hour，按 token hash 计数；Redis 计数错误时 fail-open；
- MCP 有 429 headers、body/batch 限制、scope/tool filtering 和上游 429 重试。

### 基于证据的 Wave 推论

- 商业套餐应归属 organization；project 是资源和运行时隔离；token 是身份和公平性维度；
- OpenAPI/MCP 可以共享 machine API entitlement 和资源 quota，不能共享一套简单 QPS；
- Account API Token 的商业价值更接近“可编程访问能力 + 组织容量 + 自动化可靠性”，不是 token 数量或 token QPS；
- Session Token 需要独立的攻击防护，不应仅复用机器 token 限流；
- 第一版 Redis 失效处理优先考虑 fail-open、后端并发/超时和告警，不先设计多级 fallback。

### 仍需产品或实测确认

- PostHog Cloud 当前 snapshot 与本地源码是否完全一致；本调研不能给本地快照打 commit 级版本承诺；
- PostHog MCP 的 480/min、4800/hour 是否在 Cloud 按 organization、用户或不同客户合同覆盖；
- MCP tool 是否所有调用都进入同一个 `api_queries_read_bytes` 商业 quota；
- 企业客户是否可以购买更高 API query concurrency、专属容量或不同 quota behavior；
- quota 超额时，具体产品是 hard stop、grace、降级还是 overage，需要结合 billing service/合同；
- Wave 客户愿意购买的是 machine access entitlement、额度、并发、SLA，还是某个垂直自动化产品。

## 十一、建议的下一轮研究验证

这份文档的下一步不是实现 PostHog 的限流器，而是把下面的问题变成 Wave 的可验证需求：

1. 用 Wave 的真实 API 路径列出低成本 CRUD、高成本 query、写入、MCP tool 四类资源，判断哪些需要独立 rate/concurrency/cost。
2. 用两个 organization、多个 project、多个 token 做容量模型：验证“组织额度是否共享”“token 是否只是公平性”“项目是否是运行时聚合”。
3. 设计 429/403/quota-exhausted 的产品反馈，确认客户能否从响应中知道是权限不足、瞬时超限还是本周期用量耗尽。
4. 盘点 Session Token 的预认证入口、匿名/IP/device 维度和现有网关保护；不要把 Web session 直接套入 Account API Token 的商业模型。
5. 在确定真实攻击/成本数据后，再决定 Redis fail-open 是否需要本地粗粒度 fallback，以及 fallback 是否需要对付费组织区别处理。

## 十二、最终判断

PostHog 最值得参考的不是某个 `480/min` 数字，而是它把以下四件事拆开并连接起来：

```text
谁能访问             -> token / scope / project / organization permission
能访问多少           -> organization/resource quota 与 plan entitlement
短时间能打多快       -> token / team / endpoint rate limit
后端能同时处理多少   -> team / organization / workload concurrency
```

如果 Wave 只补 Account API Token 的 per-token QPS，最多解决单 token 的突发流量，解决不了 token 批量创建、跨项目放大、高成本查询、MCP batch 和 session 攻击。更有商业价值的需求方向是：先定义 organization 购买的 machine API 能力和用量边界，再用 project、token、endpoint、concurrency 组成最小够用的运行时保护层。

