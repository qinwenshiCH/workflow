# API QPS 需求梳理与竞品调研（预 spec）

日期：2026-07-17  
状态：需求梳理阶段，仅记录研究结论，不代表最终产品方案。  
范围：Wave 的 Account API Token（OpenAPI/MCP）、Session Token Web API 请求，以及 Redis 限流故障时的生产保护；暂不覆盖 `/internal/v1` S2S API 和事件采集链路。

## 本轮新增判断：这是两套目标，不是一套配额

本 change 同时包含两个不同问题，必须在产品规格中分开：

| 问题 | 目标 | 计数主体 | 是否跟组织套餐绑定 |
| --- | --- | --- | --- |
| Account API Token 接入 OpenAPI/MCP | 让付费客户获得可预期的 API 能力，同时控制成本 | organization 业务额度 + token 凭证公平性 | 是 |
| Session Token Web 请求防护 | 防止被盗 session、机器人、匿名请求拖垮服务 | IP（认证前）+ account（认证后）+ global | 否 |

最有商业价值的是第一项：组织套餐决定是否允许 OpenAPI/MCP 以及对应的有效 API policy；token 只是调用凭证，不应该成为套餐归属。第二项是平台安全能力，不能因为用户套餐高就放宽到足以拖垮服务。

按照 ponytail 原则，本 change 不建议一开始同时引入独立的 account、organization、project、token、route、cost、并发、日配额、月配额八套可配置桶。第一版只保留“组织商业额度 + token 公平性 + session 安全防护 + global 保护”四类真正有业务价值的约束；project 先承载有效配置和请求上下文，只有出现项目间资源争抢证据时再增加独立 project bucket。

## 一、先给结论

API QPS 不应该在“按组织”与“按项目”之间二选一。更稳妥的模型是把不同维度承担的责任拆开：

| 层级 | 建议 key | 主要保护对象 | 是否属于业务配额 |
| --- | --- | --- | --- |
| 平台安全阀 | `global` | 整个 Wave 集群 | 否，运维保护 |
| 凭证公平性 | `token` | 单个 Account API Token | 否，防单凭证滥用 |
| 账户聚合 | `account` | 同一账户创建多个 token 后的总用量 | 可选，防多 token 叠加绕过 |
| 租户聚合 | `organization` | 组织下所有项目的总用量 | 是，适合承载套餐额度 |
| 工作负载隔离 | `project` | 单个项目对分析/查询资源的占用 | 是，适合保护项目资源 |
| 接口/成本 | `route_class` + `cost` | 查询、报表、下载等高成本接口 | 是，体现不同资源消耗 |
| 并发保护 | `project` 或 `organization` | 同时运行的慢查询/报表数量 | 是，但不是 QPS |

对 Wave 的推荐判断是：

1. 如果问“套餐额度归谁”，优先归组织；组织是项目套餐模板和资源归属的上层，更适合做总额度。
2. 如果问“请求打到哪里、谁消耗计算资源”，项目是运行时上下文；但第一版不必因此增加独立 project bucket。
3. 如果一个 token 可以访问多个组织或多个项目，必须保留 token 级限制，同时叠加 organization 共享额度；否则 token 或项目拆分都可能放大总吞吐。
4. 如果当前版本只能增加一个业务维度，建议先增加 `organization`，因为本 change 的商业契约是组织套餐；project 先保存 effective policy，后续有资源争抢证据再独立限流。
5. QPS 只解决“单位时间请求速率”。日报/月度事件量、查询总成本、同时运行数量应分别建模，不能全部塞进一个 QPS 字段。

这与竞品的共同方向一致：Amplitude 把组织级月度事件量、项目级 API/摄取吞吐、用户/设备级反滥用和接口级并发/成本分开；PostHog 同时使用 API key、team/project 聚合、接口类别、IP 和 ClickHouse 并发限制，而不是只选择一个租户层级。

## 二、Wave 当前实现与现状约束

### 2.1 已有能力

当前 Wave 分支 `dev/qws/2607/api_qps_wave` 已有 Account API Token 的 Redis Token Bucket 限流实现。现阶段生效维度只有 `per_token + global`，请求成本按路径归类为 1、2、3，使用 Lua 保证单桶读改写原子；限流后返回 429、`Retry-After` 和 `RateLimit-*` 响应头。但该中间件只识别 Account API Token，Session Token 请求会直接跳过。

- 限流核心：[ratelimit.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go:1>)
- HTTP 中间件：[account_api_token_rate_limit.go](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/account_api_token_rate_limit.go:22>)

当前配置是静态 Web 配置：启用开关、Redis 异常时 fail-open/fail-close、单 token RPS、全局 RPS；默认值为单 token 3 RPS、全局 2000 RPS，burst 由核心逻辑按 RPS 的 2 倍推导。[web_cfg.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/config/web_cfg.go:50>)

Account API Token 本身属于 account，并且 scope 可以覆盖全部资源、指定组织或指定项目；资源范围字段已经有 `OrgIDs`、`ProjectIDs` 和 `ResourceMode`。[service.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/service.go:41>) [service.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/service.go:719>)

### 2.2 关键架构约束

当前中间件顺序是：Account API Token 鉴权 → Account API Token 限流 → Session → ProjectFilter → OrganizationFilter → RequestContext。[server.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/server.go:497>)

因此，当前限流执行时还没有统一注入 `project_id` 和 `organization_id`。如果后续要按项目或组织限流，不能只把 Redis key 拼接上 ID；必须先决定以下一种方式：

- 把租户上下文解析提前到限流前，并定义路径、query、body 中 ID 冲突时的优先级；或
- 限流前只做 global/token/account，完成 ProjectFilter/OrganizationFilter 后再做 project/organization；或
- 在鉴权阶段一次性解析并缓存资源上下文，供限流和 scope 校验共同使用。

第二种方式最容易渐进落地，但需要定义一次请求是否可能被前后两个限流阶段重复计数。

### 2.3 Session Token 当前安全链路

Session Token 通过 `Authorization: Bearer` 或 `atoken` Cookie 提取，SessionMiddleware 调用 Redis 校验；session token 使用 `st2` 前缀、AES-GCM 保护的 account ID 和 256-bit 随机 session ID，当前账号 session TTL 为 14 天。[session.go](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/session.go:47>) [token_codec.go](</Users/wenshiqin/wave-worktrees/api_qps/pkg/lib/session/token_codec.go:15>) [account.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/controller/account/account.go:30>)

浏览器 Cookie 已设置 HttpOnly、HTTPS 下 Secure、SameSite=Lax；这是必要的 token 存储基线，但不能代替 API 限流、CSRF 防护和会话撤销。[cookie.go](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/cookie.go:11>)

当前 Session Token 有两个限流缺口：

1. 标准 Web 路由中，Account API Token 限流中间件在 SessionMiddleware 之前，但它对非 Account API Token 直接跳过；随后 SessionMiddleware 完成认证，没有后续 session 限流层。[server.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/server.go:500>)
2. MCP 认证允许 Session Token，但 `enforceMCPAccessTokenRateLimit` 对非 Account API Token 直接返回放行，已有测试明确记录 session token bypass；因此不能只把现有 Account API Token 限流配置打开就认为 MCP 已受保护。[mcp/server.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/server.go:111>) [mcp/server.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/server.go:182>) [mcp/server_test.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/server_test.go:357>)

Session Token 的最小安全策略应是：认证前按可信来源 IP 做粗粒度保护，认证成功后按 `account_id` 做聚合保护；不要按 session token 单独给额度，否则攻击者拿到多个 session 或轮换 session 后仍可放大总流量。认证失败请求必须仍受 IP/route 保护，否则密码、登录和 token 猜测可以绕过 account 维度。

### 2.4 Redis 故障必须区分“认证故障”和“限流故障”

Session 鉴权本身依赖 Redis 的 session key；Redis 不可用时不能把“无法验证 session”降级成“认证成功”，否则会直接破坏认证边界。受保护的 session 请求应 fail-close，返回可重试的 503；匿名公共接口继续走 IP/global 保护。[session.go](</Users/wenshiqin/wave-worktrees/api_qps/pkg/lib/session/session.go:201>)

限流器 Redis 故障则建议降级，但不是无条件 fail-open：

- 认证前使用进程内、带 TTL 的 IP emergency limiter；
- 认证成功后使用同一个本地 limiter 按 account/token 做临时保护；
- Redis 恢复后自动回到分布式限流；本地 limiter 只保证单实例的应急保护，不宣称套餐额度精确一致；
- 本地保护也无法决策时，高风险或高成本接口返回 503，普通低风险读接口才可按明确的 fail-open 策略放行；
- 记录 Redis error、fallback、blocked、fail-open 次数，并限制 fallback 状态持续时间。

Redis 官方也把分布式限流定位为跨实例共享的 user、IP、API key、tenant 计数，并推荐使用 Lua 保证读-判断-更新原子；本地 fallback 因此只能是故障保护，不能替代正常的 Redis 配额。[Redis rate limiter](https://redis.io/docs/latest/develop/use-cases/rate-limiter/)

### 2.5 当前实现需要在 spec 中明确的技术问题

当前流程先扣 token 桶，再扣 global 桶。如果 token 桶通过、global 桶拒绝，token 桶的令牌已经被消耗；这可以被解释为“请求尝试也计费”，但会造成调用方看到 global 429 时仍损失 token 配额。建议新 spec 明确采用“所有适用桶一次性判断，只有全部通过才扣减”的原子语义，或者明确接受“下游桶拒绝时上游桶仍消耗”的行为。[ratelimit.go](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go:155>)

当前 `WebQuota` 已承载 Web 业务数量和 Wagent 周期额度，但没有 Web API QPS 字段；`AbQuota.Qps`、`EdgeQuota.Qps`、`AdtQuota.Qps` 属于已有产品/服务配额，不应在未确认语义前直接复用。[project_conf.go](</Users/wenshiqin/wave-worktrees/api_qps/pkg/pm/project_conf.go:32>) [project_conf.go](</Users/wenshiqin/wave-worktrees/api_qps/pkg/pm/project_conf.go:46>)

## 三、竞品调研

### 3.1 Amplitude：按“计费实体 + 数据实体 + 资源成本”拆层

Amplitude 的设计不是单层限流：

| 维度 | Amplitude 做法 | 对 Wave 的启示 |
| --- | --- | --- |
| 组织 | 月度事件量限制按 organization 计算，并按自然月重置；付费计划有接近额度的告警 | 组织适合做套餐/账单总额度，而不是唯一的实时 QPS key |
| 项目 | Experiment Management API 的限制按 project；文档给出 100 req/s 和 100,000/day；Dashboard REST API 的 rate/concurrency 也按 project | 项目适合做 API 数据隔离和计算资源隔离 |
| 接口成本 | Dashboard API 按日期范围、条件数量、图表类型计算 query cost，并同时限制 cost 速率和并发 cost | 高成本查询不能与普通 CRUD 共用一个“每请求 1 次”的 QPS |
| 摄取吞吐 | HTTP V2 以 project 为吞吐边界，并对免费计划和单用户/设备更新设置额外限制 | 业务套餐、平台吞吐和单主体反滥用可以并存 |
| 凭证 | API key/secret key 按 project 创建和管理 | 凭证 scope 与 project 绑定时，project key 很自然；Wave 的 Account Token 是跨资源的，需要额外聚合层 |

来源：[Limits and quotas](https://amplitude.com/docs/faq/limits-and-quotas)、[Experiment Management API](https://www.amplitude.com/docs/apis/experiment/experiment-management-api)、[Dashboard REST API](https://amplitude.com/docs/apis/analytics/dashboard-rest)、[HTTP V2 API](https://amplitude.com/docs/apis/analytics/http-v2?h=user+id)、[Authentication](https://www.amplitude.com/docs/apis/authentication)。

Amplitude 最值得借鉴的不是具体数值，而是把三件事分开：

1. 组织级长期用量是商业配额；
2. 项目级速率/并发是服务保护；
3. 查询复杂度是成本单位，不能只按请求次数衡量。

### 3.2 PostHog：按 credential + team + route + concurrency 叠加

PostHog 源码显示了更接近 Wave API 查询场景的组合：

- 普通 API 对 personal API key 使用 burst `480/minute` 和 sustained `4800/hour`；ClickHouse 查询使用更低的 `240/minute` 和 `1200/hour`；HogQL 查询单独使用 `120/hour`，并支持从 Team 上读取自定义 `api_query_rate_limit`。[rate_limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:362>) [rate_limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:425>) [rate_limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:568>) [rate_limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:162>)
- Project Secret API Key 同时有“每 key”限制和“每 team 聚合”限制，源码注释明确说明：只做每 key 限制会让调用方通过创建更多 key 放大总容量；team 聚合桶用于封顶所有 PSAK 的总负载。[rate_limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:385>) [rate_limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:405>)
- QueryViewSet 会根据动作、查询类型和 team 配置选择不同 throttle；HogQL、ClickHouse、API query 和 AI query 使用不同的限制类别。[query.py](</Users/wenshiqin/go-project/posthog-master/posthog/api/query.py:128>)
- 限流之外，ClickHouse 还有独立的并发限制：API query 按 team 默认最多 3 个并发，materialized endpoint 按 team 最多 10 个，events list 按 team 最多 2 个；应用侧还有按 organization 的并发限制。[limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/clickhouse/client/limit.py:202>) [limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/clickhouse/client/limit.py:296>) [limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/clickhouse/client/limit.py:329>)
- Feature Flags 服务在请求体解析和认证之前先做 IP、token 两层保护，并支持 `Allowed/Warned/Blocked` 三态、80% warn threshold、log-only 迁移模式和 token override。[rate-limiting.md](</Users/wenshiqin/go-project/posthog-master/docs/internal/feature-flags/rate-limiting.md:3>)
- Node.js 的 keyed limiter 使用 Redis Token Bucket，支持 cost、单请求参数覆盖、fail-open/fail-close 和批量检查；这说明当一个请求要同时检查多个主体或多个 key 时，限流器本身应支持批量/组合决策。[keyed-rate-limiter.service.ts](</Users/wenshiqin/go-project/posthog-master/nodejs/src/common/services/keyed-rate-limiter.service.ts:16>)

官方文档可作为外部行为参考：[PostHog Rate limits](https://www.mintlify.com/PostHog/posthog/api/rate-limits)、[PostHog API overview](https://www.mintlify.com/PostHog/posthog/api/overview)。本节的实现细节以本地源码为准。

需要注意文档口径与源码层级可能不同：PostHog 对外文档描述部分私有 API 限额为 team 共享，但通用 throttle 源码对某些 endpoint 会优先使用 personal API key。合理解释是不同 endpoint 使用了不同 throttle class，或产品总配额与某一层 burst 限制并非同一个概念。因此，Wave 借鉴 PostHog 时应借鉴“多层叠加”的原则，不应直接复制某个数值或假设所有接口共享同一计数主体。[PostHog API docs](https://posthog.com/docs/api) [rate_limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:236>)

PostHog 对 Wave 最直接的启示是：

> “每 token”解决凭证公平性，“每项目/团队聚合”解决多凭证绕过，“按接口类别”解决资源成本，“并发限制”解决慢查询占满执行资源。

## 四、行业最佳实践

### 4.1 层级限流，而不是单一 key

AWS API Gateway 支持 account/region、API/stage/method、client/API key usage plan 等多个层级，并按层级叠加执行；其核心算法是带 steady-state rate 和 burst capacity 的 token bucket，超过限制返回 429。[AWS API Gateway throttling](https://docs.aws.amazon.com/apigateway/latest/developerguide/api-gateway-request-throttling.html)

Envoy 也允许在 route/vhost/descriptor 上配置多个 token bucket，并通过 descriptor 选择不同业务维度；同时支持 runtime fractional enabled/enforced 配置，用于先观测再强制。[Envoy local rate limit](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/local_rate_limit_filter)

因此 Wave 的限制决策可以抽象为：

```text
请求
  -> 鉴权并解析 account/org/project/token
  -> 识别 route_class 与 cost
  -> 找出所有适用桶
  -> 组合检查 global + account/token + organization + project + route/concurrency
  -> 全部通过才放行；否则返回 429 和最早可重试时间
```

### 4.2 速率、burst、并发、长期额度分开

- **速率**：平均每秒允许消耗多少 cost units。
- **burst**：瞬时允许积累多少 cost units；不应简单等同于“RPS 的固定倍数”，套餐和接口类型可能不同。
- **并发**：当前正在执行的慢查询、下载、报表任务数；一个请求运行 20 秒时，仅限 QPS 仍可能占满后端资源。
- **长期额度**：日/月总请求量、事件量或计算单位，服务于计费和成本控制。

Amplitude 的查询 cost/concurrency 和 PostHog 的 team/org concurrency 都说明了并发必须独立于 QPS 建模。[Amplitude Dashboard REST API](https://amplitude.com/docs/apis/analytics/dashboard-rest) [PostHog concurrency source](</Users/wenshiqin/go-project/posthog-master/posthog/clickhouse/client/limit.py:202>)

OWASP 将“无限制资源消耗”列为 API 风险，建议同时限制交互频率、批量大小、执行时间、输入/响应规模，并针对不同接口按业务场景细化，而不是只加一个全局 RPS。[OWASP API4:2023](https://owasp.org/API-Security/editions/2023/en/0xa4-unrestricted-resource-consumption/)

Stripe 的生产接口也将 global rate、endpoint rate、global concurrency、endpoint concurrency 和 resource-specific limit 分开，并在 429 中返回触发类别；这支持 Wave 将 session 安全保护与付费 API 额度分成两套 policy。[Stripe rate limits](https://docs.stripe.com/rate-limits)

### 4.3 429 与退避契约

HTTP 429 表示在给定时间内请求过多。[RFC 6585](https://www.rfc-editor.org/rfc/rfc6585) 建议通过 `Retry-After` 告诉客户端何时重试；IETF RateLimit Headers 草案进一步定义了用于表达 limit、remaining、reset 和多窗口策略的字段，但它不规定限流算法，也不等于 SLA。[RateLimit Headers draft](https://datatracker.ietf.org/doc/draft-ietf-httpapi-ratelimit-headers/)

Wave 当前已经返回 `Retry-After`、`RateLimit-Limit`、`RateLimit-Remaining`、`RateLimit-Reset`。下一版 spec 需要补齐字段语义：是请求数还是 cost units、对应哪个窗口、触发多个桶时返回哪个桶、reset 是秒数还是绝对时间。

### 4.4 逐步启用与可观测性

成熟做法是先 log-only/metrics-only，再 warn，最后 enforce；需要按 `blocked_by`、route、org、project、token hash、cost、Redis error、fail-open 次数统计，但不能记录明文 token。PostHog 的 feature flags 限流文档给出了 log-only、warn ratio 和分层 metrics 的迁移路径。[PostHog rate-limiting.md](</Users/wenshiqin/go-project/posthog-master/docs/internal/feature-flags/rate-limiting.md:13>)

## 五、建议的需求模型（供下一步 spec 讨论）

### 5.1 Account API Token 的商业规格

对 OpenAPI/MCP，建议产品契约按 organization plan 表达：

1. 组织套餐决定是否有 API/MCP entitlement，以及该 entitlement 的有效速率/额度；
2. token scope 决定能访问哪些组织/项目，不能通过 token 数量绕过组织套餐；
3. organization 是商业总额度的计数主体；project 只在请求进入项目资源时用于解析有效 policy 和资源上下文；
4. 如果未来要按项目单独售卖或隔离资源，再增加 project bucket，并明确是“每项目额度”还是“组织额度在项目间分配”；不能把组织套餐值静默复制成每项目独立额度。

AWS API Gateway 的 usage plan 也是“产品计划 + API key 识别调用方”的模型，但官方同时提醒 usage plan 的 throttle/quota 不是严格成本控制边界；Wave 仍需保留 global 和后端资源保护。[AWS usage plans](https://docs.aws.amazon.com/apigateway/latest/developerguide/api-gateway-api-usage-plans.html)

### 5.2 Session Token 的安全规格

Session Token 不参与组织套餐计费，使用安全保护 policy：

```text
认证前：global + trusted_client_ip + route_class
认证成功：global + account_id
高风险认证接口：IP + account/identifier + 更严格窗口
```

这里的 `account_id` 是所有 session 的聚合 key；不需要单独做 account 下每个 session 的业务额度。对于 cookie 鉴权的写操作，CSRF 校验仍是独立安全要求，不能用 QPS 代替。

### 5.3 一次请求至少需要解析这些字段

```text
auth_kind        session / account_api_token / anonymous
account_id       认证后的账户；匿名请求为空
token_id/hash    Account API Token 使用 hash；Session Token 不记录明文
client_ip        经过可信代理配置校验后的来源 IP
organization_id  当前请求所属组织；无组织上下文时为空
project_id       当前请求所属项目；无项目上下文时为空
route_class      轻量读、写操作、分析查询、报表/下载、异步任务等
cost             本次请求消耗的 cost units，默认 1
plan_policy      Account API Token 的组织套餐解析结果
```

其中 `organization_id` 和 `project_id` 不是互斥选择，而是根据接口上下文决定是否同时存在：项目接口同时命中 org 和 project；组织接口命中 org；账户级接口只命中 account/token/global。

### 5.4 推荐的限流语义

对 Account API Token 请求，第一版只要求所有适用的商业/平台桶通过：

```text
global AND organization AND token
```

对 Session Token 请求，第一版只要求：

```text
认证前：global AND client_ip
认证后：global AND account_id
```

建议的优先级不是“只返回第一个失败维度”，而是内部记录所有检查结果，对外返回最早可重试的限制信息，并在 metrics 中记录真正阻塞的维度。这样可以避免客户端根据错误消息猜测内部 key 结构。

### 5.5 配置/数据模型建议

建议新增明确的 Web API 商业配额模型，例如：

```text
WebQuota.ApiAccess
  enabled
  requests_per_second
  burst_requests
  project_policy（组织套餐解析后的有效配置）
```

组织套餐是商业事实来源，项目保存解析后的 effective policy 以便运行时读取和管理；这不等于每个项目都拥有一份独立组织额度。第一版不增加 token 级客户可配置 override，也不增加 daily/monthly quota、动态 query cost 和并发 quota。建议不要直接复用 `AbQuota.Qps` 等已有字段，因为当前代码中它们代表其他产品配额，语义不清会导致计费和运行时保护耦合。

### 5.6 当前版本建议的落地顺序

| 阶段 | 生效维度 | 目标 |
| --- | --- | --- |
| P0 安全 | global + IP（认证前）+ account（认证后） | 覆盖 Session Token、匿名攻击和 Account API Token，避免服务被打崩 |
| P0 商业 | organization plan + token + global | 让付费组织使用 OpenAPI/MCP，token 不能绕过组织套餐 |
| P1 运行保护 | route class + 特定高成本接口窗口 | 只对真实热点收紧，不提前建设通用 cost/concurrency 平台 |
| 后续证据驱动 | project bucket、account aggregate、daily/monthly quota | 仅当监控证明组织内项目争抢、多 token 绕过或计费需要时增加 |

这里的“project policy”是组织套餐落到项目运行配置的有效值，不自动意味着每项目独立计数。若产品最终选择项目独立配额，需要在另一个决策中明确组织额度如何分配到项目。

## 六、必须在 spec 阶段确认的问题

1. Account API Token 的 API/MCP entitlement 是否按 organization plan 购买和开通？
2. 组织套餐中的 QPS 是整个 organization 共享，还是每个 project 各自获得一份？本轮建议先定为 organization 共享。
3. project 是否只保存组织套餐解析后的 effective policy，还是要成为独立的计数桶？本轮建议先不独立计数。
4. Session Token 是否统一覆盖 Web API 和 MCP？本轮建议覆盖，不能继续保留 MCP session bypass。
5. 认证前 IP 限流使用哪个可信来源？代理头必须只在受信代理链路下采信，不能允许客户端伪造。
6. 认证失败请求和匿名白名单接口是否都要有 IP/route 限制？本轮建议都要有，尤其是登录、注册、密码找回和 MCP 入口。
7. Redis 限流故障时是否接受本地 emergency limiter 的 best-effort 语义？本轮建议接受；它不是商业配额计量。
8. Session 鉴权 Redis 故障时是否统一返回 503，而不是 401 或放行？本轮建议 fail-close。
9. P0 是否只做固定 route class，不引入通用 weighted cost、并发、日/月 quota？本轮建议是。
10. 是否需要向客户展示“组织剩余额度/使用量”？这属于 usage 产品，不建议混入本 change 的运行时限流实现。

## 七、研究来源

### Wave 本地代码

- [Account API Token 限流核心](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go>)
- [Account API Token 限流中间件](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/account_api_token_rate_limit.go>)
- [Web 路由中间件顺序](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/server.go:497>)
- [Account API Token scope 与资源范围](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/service.go:57>)
- [Session Token 鉴权中间件](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/session.go:19>)
- [Session Token 编码与 Redis 鉴权](</Users/wenshiqin/wave-worktrees/api_qps/pkg/lib/session/token_codec.go:15>) [session.go](</Users/wenshiqin/wave-worktrees/api_qps/pkg/lib/session/session.go:201>)
- [MCP Session Token 限流 bypass](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/server.go:182>)
- [Wave quota 模型](</Users/wenshiqin/wave-worktrees/api_qps/pkg/pm/project_conf.go:32>)

### PostHog 本地源码

- [通用 API throttle](</Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py>)
- [QueryViewSet throttle 选择](</Users/wenshiqin/go-project/posthog-master/posthog/api/query.py:128>)
- [PSAK per-key 与 per-team aggregate](</Users/wenshiqin/go-project/posthog-master/products/endpoints/backend/rate_limit.py:127>)
- [ClickHouse 并发限制](</Users/wenshiqin/go-project/posthog-master/posthog/clickhouse/client/limit.py:202>)
- [Feature Flags 分层限流文档](</Users/wenshiqin/go-project/posthog-master/docs/internal/feature-flags/rate-limiting.md>)
- [Redis keyed limiter](</Users/wenshiqin/go-project/posthog-master/nodejs/src/common/services/keyed-rate-limiter.service.ts>)

### 外部资料

- [Amplitude Limits and quotas](https://amplitude.com/docs/faq/limits-and-quotas)
- [Amplitude Experiment Management API](https://www.amplitude.com/docs/apis/experiment/experiment-management-api)
- [Amplitude Dashboard REST API](https://amplitude.com/docs/apis/analytics/dashboard-rest)
- [Amplitude HTTP V2 API](https://amplitude.com/docs/apis/analytics/http-v2?h=user+id)
- [PostHog Rate limits](https://www.mintlify.com/PostHog/posthog/api/rate-limits)
- [AWS API Gateway throttling](https://docs.aws.amazon.com/apigateway/latest/developerguide/api-gateway-request-throttling.html)
- [Envoy local rate limit](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/local_rate_limit_filter)
- [OWASP API4:2023 Unrestricted Resource Consumption](https://owasp.org/API-Security/editions/2023/en/0xa4-unrestricted-resource-consumption/)
- [Stripe rate limits](https://docs.stripe.com/rate-limits)
- [Redis rate limiter](https://redis.io/docs/latest/develop/use-cases/rate-limiter/)
- [RFC 6585](https://www.rfc-editor.org/rfc/rfc6585)
- [IETF RateLimit Headers draft](https://datatracker.ietf.org/doc/draft-ietf-httpapi-ratelimit-headers/)
