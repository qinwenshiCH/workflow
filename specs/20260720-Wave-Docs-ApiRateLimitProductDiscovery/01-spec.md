# 产品探索规格：Wave API 访问能力与请求保护

**目录**: `20260720-Wave-Docs-ApiRateLimitProductDiscovery`  
**创建日期**: 2026-07-20  
**状态**: 需求调研 / 需求讨论中  
**类型**: 产品探索 spec，不是可直接进入 Dev 的实现 spec  
**输入**: 完善 Account API Token 与 Session Token 的请求保护，调研竞品做法、商业价值、套餐归属和 Redis 故障降级策略。

> 本文的目标是把问题空间理清楚，不承诺本期立即实现。除“当前 Wave 现状”外，方案均属于候选方向或工作假设，必须经过产品讨论后才能进入 plan/tasks。

---

## 一、探索目标

### 1.1 要回答的产品问题

1. Wave 是否应该把 OpenAPI、MCP 等机器接入做成客户付费的 API 能力？
2. 如果做成商业能力，套餐、额度和实际运行时限制分别属于 organization 还是 project？
3. Account API Token 和 Session Token 是否应当共享一套限流策略？
4. Session Token 如何在不影响正常 Web 使用的前提下抵御攻击？
5. Redis 限流依赖故障时，应优先保护可用性，还是优先保护后端成本和稳定性？
6. 哪些能力是本期真正有商业或生产价值的，哪些属于过度设计？

### 1.2 探索完成的定义

本 spec 完成调研阶段，而不是完成代码实现。完成条件是：

- 有一份可追溯的竞品与行业证据，区分事实和 Wave 推断；
- 明确 API 商业能力、Web 安全保护、平台容量保护三者的边界；
- 至少比较两种产品方向，并说明各自的客户价值、成本和风险；
- 确定 organization、project、account、token、IP 各自承担的职责；
- 明确 Redis 故障时的候选行为和取舍；
- 形成下一轮产品讨论问题；
- 未经用户确认，不生成 `tasks.md`，不进入开发阶段。

---

## 二、产品场景与用户故事

### 用户故事 1：客户通过机器凭证接入 Wave（P0）

作为购买了 API 能力的客户，客户希望使用 Account API Token 调用 OpenAPI 或 MCP，而不依赖浏览器登录态，并能知道自己的套餐边界和超限行为。

**商业价值假设**：API 能力是可售卖的集成入口。客户购买的是 organization 下的 API 使用能力，不是某一枚 token 的独立额度。

**需要验证**：客户实际愿意为访问能力、吞吐、并发、调用量还是特定高级接口付费。

**验收场景**:

1. **Given** organization 套餐未开通 API 能力，**When** 客户使用 Account API Token 调用 OpenAPI/MCP，**Then** 系统能区分“未购买/无权限”和“已购买但超限”。
2. **Given** organization 已开通 API 能力，**When** 同一 organization 下多个 project 发起调用，**Then** 产品能够表达 project 独立上限与 organization 总上限的关系。
3. **Given** 客户创建多个 Account API Token，**When** 所有 token 共同调用，**Then** 不能通过 token 数量线性放大 organization 的购买额度。

### 用户故事 2：客户选择不同接入通道（P0）

作为 API 客户，客户可能选择 OpenAPI、MCP 或未来的其他机器接入方式。客户希望不同通道的规则稳定、可预期，不因换了传输协议就获得额外容量。

**工作假设**：OpenAPI 与面向自动化的 MCP 应共享商业 API policy；通道可以有不同的 endpoint/resource cost，但不能绕过 organization/project 的总体能力边界。

**需要验证**：MCP 是否同时支持“交互式登录用户”和“客户自动化服务账号”两种场景。如果两者都支持，是否应使用不同凭证类型和不同 quota class。

### 用户故事 3：Web 用户在 Session Token 下正常使用（P0）

作为普通 Web 用户，用户希望多个页面、浏览器标签和设备同时使用而不频繁遇到 429；同时，攻击者不能通过伪造、盗用或轮换 session token 把服务打崩。

**工作假设**：Session Token 是浏览器登录凭证，不是客户购买 API 能力的凭证。正常 Web 流量应采用 account/IP/route 的安全保护，而不是消耗 Account API Token 的商业额度。

**验收场景**:

1. **Given** 攻击者不断发送无效 token，**When** 请求到达 API 入口，**Then** 请求应在足够早的阶段受到 IP/路由保护，不能无限触发 session Redis 鉴权。
2. **Given** 同一 account 有多个有效 session，**When** 这些 session 共同访问高成本接口，**Then** account 级保护仍然生效，不能通过多设备绕过限制。
3. **Given** 正常用户使用多个标签页，**When** 请求量处于正常交互范围，**Then** 不应因为按单 session 或按 IP 粗暴限制而误伤。

### 用户故事 4：平台在攻击和依赖故障下保持可控（P0）

作为平台运维者，希望攻击流量、Redis 故障和某个客户的高成本调用不会把整个 Web/API 服务拖垮，并且能判断是客户超限、平台保护还是限流依赖异常。

**需要验证**：当前部署是否已有网关/WAF/IP 限流。如果已有，应复用其能力；如果没有，应用侧必须承担最小的预认证保护。

### 用户故事 5：产品团队能够包装套餐和运营规则（P1）

作为产品和运营人员，希望能用少量清晰的套餐字段表达 API 能力，而不是为每个 token、每条路径维护一套难以解释的配置。

**工作假设**：套餐 policy 由 organization/plan 定义；project 使用继承后的有效策略参与运行时计数；token 只作为身份和反滥用维度，不单独承载客户套餐。

---

## 三、竞品与行业证据

### 3.1 Amplitude

Amplitude 没有把限制简化成一个组织或项目字段：

- 组织承载月度事件量和长期商业额度；
- project 承载 API key、数据隔离和多类 API 吞吐；
- Dashboard API 还按 project 区分 rate、concurrency 和 query cost；
- HTTP/Batch 摄取场景还存在 user/device 级保护。

**对 Wave 的启示**：套餐归属和实时运行时限流可以是不同实体；高成本查询、并发和长期额度不能都叫 QPS。

来源：[Amplitude Limits and quotas](https://amplitude.com/docs/faq/limits-and-quotas)、[Experiment Management API](https://www.amplitude.com/docs/apis/experiment/experiment-management-api)、[Dashboard REST API](https://amplitude.com/docs/apis/analytics/dashboard-rest)。

Amplitude 的 account token、OpenAPI/REST、MCP 权限与计费专项证据见 [_research/amplitude-account-api-openapi-mcp-billing.md](./_research/amplitude-account-api-openapi-mcp-billing.md)。

### 3.2 PostHog

PostHog 的实现组合包括：

- personal/project secret API key 级 burst/sustained 限制；
- HogQL、ClickHouse 等高成本接口使用独立限制；
- Project Secret API Key 还叠加 team aggregate，避免创建多个 key 放大总容量；
- 查询并发按 team 或 organization 独立限制；
- Feature Flags 服务还有 IP、token、team 多层保护和 warn/log-only 迁移模式。

**对 Wave 的启示**：token 解决凭证公平性，project/team 解决租户聚合，endpoint 解决资源成本，并发解决长任务占满后端的问题。

来源：[PostHog rate_limit.py](</Users/wenshiqin/go-project/posthog-master/posthog/rate_limit.py:362>)、[PostHog endpoint rate limit](</Users/wenshiqin/go-project/posthog-master/products/endpoints/backend/rate_limit.py:127>)、[PostHog ClickHouse concurrency](</Users/wenshiqin/go-project/posthog-master/posthog/clickhouse/client/limit.py:202>)、[PostHog Feature Flags rate limiting](</Users/wenshiqin/go-project/posthog-master/docs/internal/feature-flags/rate-limiting.md:3>)、[PostHog API docs](https://posthog.com/docs/api)。

PostHog 的源码专项拆解见 [_research/posthog-api-mcp-commercial-research.md](./_research/posthog-api-mcp-commercial-research.md)，包括 organization/project/token 的商业与运行时分层、MCP 实现、query concurrency、quota enforcement 和 Redis 故障取舍。

**注意**：PostHog 对外文档描述的 team 共享口径与部分源码中 personal API key 优先的实现并不完全相同。这里借鉴的是分层原则，不直接复制具体数值。

### 3.3 行业网关

- Stripe 将 global/endpoint rate 与 global/endpoint concurrency 分开，并用响应头区分触发原因。[Stripe Rate limits](https://docs.stripe.com/rate-limits)
- AWS API Gateway 使用 account、API/method、client/API key、usage plan 等多层 token bucket。[AWS throttling](https://docs.aws.amazon.com/apigateway/latest/developerguide/api-gateway-request-throttling.html)
- Kong 区分 local、cluster、Redis 策略，并明确“交易准确性”和“后端保护”是两种不同诉求；其 `fault_tolerant` 会在外部存储故障时放行请求，等价于暂时关闭限流。[Kong rate limiting](https://developer.konghq.com/plugins/rate-limiting/)、[Kong configuration reference](https://developer.konghq.com/plugins/rate-limiting/reference/)

---

## 四、Wave 当前基础

### 4.1 Account API Token

Wave 当前已有 Account API Token 的 Redis Token Bucket 限流：

- 当前生效维度：`per_token + global`；
- 请求成本按路径归类；
- 限流中间件返回 429、`Retry-After` 和 `RateLimit-*`；
- 当前配置为静态 Web 配置，默认单 token 3 RPS、global 2000 RPS；
- 当前 `fail-open` 默认开启。

来源：[Account API Token rate limiter](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go:93>)、[rate limit middleware](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/account_api_token_rate_limit.go:22>)、[Web config](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/config/web_cfg.go:50>)。

Account API Token 归属于 account，scope 可以覆盖全部资源、指定组织或指定项目。[Token scope](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/service.go:57>)

### 4.2 Session Token

当前 Session Token：

- 使用 AES-GCM 保护 token 中的 account ID；
- 使用 32 字节随机 session ID；
- session 数据保存在 Redis，鉴权时服务端查询 Redis；
- Cookie 设置 `HttpOnly`、`SameSite=Lax`，HTTPS 下设置 `Secure`；
- 当前账号 session TTL 为 14 天。

来源：[token codec](</Users/wenshiqin/wave-worktrees/api_qps/pkg/lib/session/token_codec.go:15>)、[session store](</Users/wenshiqin/wave-worktrees/api_qps/pkg/lib/session/session.go:44>)、[auth cookie](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/cookie.go:11>)、[session TTL](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/controller/account/account.go:30>)。

当前 SessionMiddleware 负责认证，但没有 account/IP/route 的统一请求限流。当前 API 路由顺序是 Account API Token 鉴权、Account API Token 限流、Session 鉴权、ProjectFilter、OrganizationFilter，因此还不能直接以 project/org 上下文执行所有业务限流。[API middleware order](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/server.go:497>)

### 4.3 当前问题边界

| 问题 | 当前状态 | 影响 |
|---|---|---|
| Account API Token 滥用 | 有 token/global 限流 | 还没有套餐、project、organization 的产品语义 |
| Session Token 滥用 | 只有 Redis 鉴权 | 伪造 token 请求可能反复触发鉴权依赖 |
| 多 token 绕过 | per-token 限制无法单独解决 | 需要 account/org/project 聚合语义 |
| 高成本接口 | 已有路径 cost 雏形 | 尚未形成产品化的 cost/concurrency policy |
| Redis 故障 | Account API Token 可配置 fail-open | session 鉴权 Redis 故障与限流 Redis 故障未分开建模 |
| MCP 会话接入 | MCP 同时接受 Account API Token 和 Session Token | 需要明确交互式会话与机器客户接入是否同一产品能力 |

---

## 五、候选产品方向

### 方案 A：组织套餐 + 项目实际限流（推荐工作方向）

**核心思路**：organization/plan 决定 API 能力和额度档位，project 是运行时主要计数边界；Account API Token 只做凭证级保护；Session Token 使用独立的安全限流。

| 维度 | 设计 |
|---|---|
| 商业能力 | organization 套餐决定 API/MCP/OpenAPI 是否可用 |
| 运行时 | project bucket + organization aggregate + token bucket + global bucket |
| Session | IP 预保护 + account 级保护；高成本接口再看 project concurrency |
| 优点 | 商业归属清晰，项目资源隔离清晰，能防多 token 绕过 |
| 风险 | 需要明确 organization 与 project 的额度关系和上下文解析时机 |
| 当前判断 | 最符合 Wave 现有 org/project quota 基础，作为讨论起点 |

### 方案 B：全部按 organization 限制

**核心思路**：所有 OpenAPI、MCP 和 Web Session 请求都以 organization 为主要额度边界，project 只做资源归属。

| 维度 | 设计 |
|---|---|
| 优点 | 商业解释简单，配置字段少 |
| 风险 | 一个 noisy project 可能拖累同组织其他项目，项目资源隔离弱 |
| 不适合之处 | Wave 的 API 路径大量以 project 为实际数据和查询上下文 |
| 当前判断 | 可作为小客户/早期产品方案，但不适合作为长期运行时模型 |

### 方案 C：Account API Token 作为独立 API 产品，Session 完全独立

**核心思路**：客户购买的是 token/API 产品，token 自带额度；Session 只做 Web 安全保护，不参与组织套餐。

| 维度 | 设计 |
|---|---|
| 优点 | API 产品容易单独包装，接入边界直观 |
| 风险 | token 数量容易变成容量放大器，需另做 account aggregate；与组织套餐关系复杂 |
| 适用条件 | 如果未来确实销售独立 API account 或独立开发者计划 |
| 当前判断 | 当前 Wave 的 Account Token 可跨 org/project，暂不适合作为唯一商业边界 |

### 工作性推荐

当前不把方案 A 记录为最终决策，只把它作为后续讨论的默认假设：

```text
organization/plan = 商业能力与套餐来源
project           = 主要运行时资源边界
account/token     = 凭证公平性与反滥用
session           = Web 安全流量，不消耗机器 API 套餐
global            = 平台总保护
```

---

## 六、Redis 故障与降级议题

这里要先区分两个 Redis 角色：

1. **Session Redis**：用于判断 session 是否有效。不可验证时不能放行，安全语义是 fail-close；产品体验上应区分无效 token 的 401 和依赖故障的 503。
2. **Rate-limit Redis**：用于分布式计数。可以按流量类型选择 fail-open 或 fail-close。

### 候选策略

| 流量类型 | Fail-open | Fail-close | 待讨论的默认方向 |
|---|---|---|---|
| OpenAPI/MCP 机器调用 | Redis 故障时可能放大成本和攻击流量 | 客户短时收到 503 | 高成本/付费 API 倾向 fail-close |
| 普通 Session 读请求 | 体验更连续，但可能暂时失去保护 | 大面积 Web 失败 | 仅在已有入口保护时考虑 fail-open |
| 登录、注册、密码重置 | 易被撞库或刷接口 | 依赖故障时暂时不可用 | 倾向 fail-close 或独立 IP 保护 |
| 高成本查询/导出 | 极易拖垮后端 | 直接阻断成本风险 | fail-close |

**本 spec 的研究假设**：先不引入一套复杂的本地 fallback 限流器。第一步只需要定义短超时、故障指标和按流量类型的降级策略；如果生产 SLO 证明 Redis 故障时必须继续服务，再单独评估本地粗粒度保护。

---

## 七、候选数据模型与接口语义（非最终实现设计）

以下只是产品讨论所需的字段，不代表马上改表或改 API：

```text
ApiAccessPolicy
  enabled                       是否允许机器 API 接入
  project_rate                  项目持续速率
  project_burst                 项目突发容量
  organization_rate             组织聚合速率，可选
  max_concurrency               高成本请求并发上限，可选
  source                       继承自 plan / project override
```

运行时请求上下文至少需要能够识别：

```text
channel        session / account_api_token / mcp / openapi
account_id
token_hash
organization_id?
project_id?
route_class
cost
```

需要继续讨论的接口语义：

- 套餐未开通、没有资源权限、超过限流、限流依赖故障，是否使用不同错误码；
- OpenAPI/MCP 是否共享 `channel=machine_api` 的商业额度；
- Session Token 调用 MCP 是否仅允许交互式场景；
- `Retry-After` 和 `RateLimit-*` 代表请求数还是 cost units；
- 通过多个 token 发起的请求是否共享 account/org/project 额度；
- 限流拒绝是否消耗上游桶的额度。

---

## 八、边界情况

- 同一 Account API Token 跨多个 organization/project：token 总保护仍生效，业务额度按实际资源上下文决定。
- 同一 organization 下多个 project 同时调用：需要验证 organization aggregate 是否共享。
- 同一 account 创建多个 token：不能通过 token 数量线性增加购买容量。
- Session Token 多设备并发：按 account 聚合保护，但不绑定单一 IP。
- 伪造 session token：应先受 IP/路由保护，再进入 Redis 鉴权。
- 没有 project/org 上下文的账户级接口：不能强行拼接空 project key，应使用 channel/account/global 规则。
- OpenAPI/MCP/Session 访问同一个高成本查询：需要确认是否共享后端 concurrency，不能只看身份类型。
- Redis 限流计数失败：必须区分 limiter 故障和 session 鉴权故障，不能共用一个错误语义。
- 横向扩容或缩容：任何本地 fallback 都会改变实际总容量，必须作为单独方案验证。
- 组织套餐变更：需要定义即时生效、缓存延迟和已有 Redis 桶如何处理。
- `0`、负数、缺省配置：必须分别定义为禁用、无限制或继承默认值，不能依赖 Go zero value 猜测。

---

## 九、需求与研究验收标准

### 研究功能要求

- **RR-001**：研究必须区分商业 API 配额、Web 反滥用保护和平台总容量保护。
- **RR-002**：研究必须覆盖 Account API Token、Session Token、MCP、OpenAPI 四类场景。
- **RR-003**：研究必须分别说明 organization、project、account、token、IP 的职责，不得只给出“按组织/按项目”的二选一结论。
- **RR-004**：研究必须比较至少两种产品方向，并明确推荐的是工作假设还是已确认决策。
- **RR-005**：研究必须覆盖 Redis 限流故障的 fail-open、fail-close 和是否采用本地 fallback 的取舍。
- **RR-006**：研究必须记录竞品事实来源，并区分竞品事实与对 Wave 的推断。

### 研究质量要求

- **RQ-001**：不输出未经产品确认的具体套餐数值、RPS 数值或数据库迁移任务。
- **RQ-002**：不把“进入开发”的实现任务混入本 spec；下一阶段必须由用户确认后单独生成 plan/tasks。
- **RQ-003**：所有候选方案都要说明商业价值、用户体验、运营成本和安全风险。
- **RQ-004**：至少覆盖空上下文、多 token、多 project、无效 session、Redis 故障和横向扩容边界。

### 调研验证方式

- 文档核对：Amplitude、PostHog、AWS、Stripe、Kong、Envoy 官方资料；
- 源码核对：Wave Account API Token、Session、MCP 中间件和本地 PostHog 源码；
- 产品讨论：确认 API 是否收费、套餐主体、MCP session 使用范围和 Redis 故障优先级；
- 实现前置验证：仅在产品方向确认后，补充真实流量分布、接口成本、并发和 Redis SLO 数据。

---

## 十、明确不纳入本阶段的内容

- 不实现限流代码或 SessionMiddleware 改造；
- 不确定最终 RPS、burst、daily/monthly quota 数值；
- 不设计客户用量账单和自助 usage 控制台；
- 不引入复杂的动态查询成本模型；
- 不引入本地 fallback、滑动窗口、多地域一致性等实现，除非研究结论证明它们是产品上线前置条件；
- 不把 Session Token 直接包装成客户长期机器接入凭证；
- 不生成 `tasks.md`，不进入 Dev 阶段。

---

## 十一、下一轮讨论问题

1. Wave 是否确认把 OpenAPI/MCP 作为可售卖的 organization API 能力？
2. API 能力是按组织套餐提供，还是未来存在独立 API account/开发者套餐？
3. project 是否是实际运行时的主要资源边界？组织是否需要共享总上限？
4. MCP 是否允许 Session Token？如果允许，是交互式体验，还是也支持客户自动化？
5. Session Token 是否只做 account/IP/route 的安全保护，不消耗机器 API 商业额度？
6. Redis 限流故障时，API/MCP 是否接受 503 fail-close？普通 Web 请求是否允许 fail-open？
7. 当前部署入口是否已有可信 IP 解析和网关级限流？
8. 后续要优先验证哪项数据：客户 API 使用意愿、项目级资源争抢、查询并发，还是 Redis 故障 SLO？

---

## 当前结论状态

| 结论 | 状态 |
|---|---|
| 本 change 是需求调研和产品讨论，不是立即开发 | 已确认 |
| Account API Token 与 Session Token 需要同时纳入研究 | 已确认 |
| API 商业能力建议围绕 organization 套餐讨论 | 工作假设，待确认 |
| project 作为运行时主要边界 | 工作假设，待确认 |
| Session Token 不直接消费机器 API 商业额度 | 工作假设，待确认 |
| Redis 故障需要分流量类型降级 | 工作假设，待确认 |

---

## Quality Gates

- [x] 背景与目标清晰，且明确标注为调研型 spec
- [x] 用户场景覆盖商业 API、MCP、OpenAPI、Session 安全和平台运维
- [x] 当前 Wave 架构与已有能力已通过代码搜索确认
- [x] 至少两种产品方向有对比和工作性推荐
- [x] 组织、项目、账户、token、IP 的职责已拆分
- [x] Redis 故障、空上下文、多 token、多 project、横向扩容边界已列出
- [x] 数据模型/API 语义以候选字段方式记录，未伪装成最终实现
- [x] 错误处理和研究验证方式已覆盖
- [x] 未生成 tasks，未进入 Dev
