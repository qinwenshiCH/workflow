# Wave API 访问能力与请求保护 — 产品需求文档

**目录**: `20260720-Wave-Docs-ApiRateLimitProductDiscovery`  
**创建日期**: 2026-07-20  
**状态**: 需求讨论中  
**版本说明**: 基于三篇调研文档（Amplitude、PostHog、Wave 当前基础）和产品讨论重新组织的 PRD。竞品证据为参考佐证而非驱动力，需求推导以客户价值和服务安全为第一性原理。

---

## 一、产品定位与场景

### 1.1 产品定位

Wave 的 API 能力不是"独立 API 产品"，而是**已有平台资源的程序化访问层**。客户购买 Wave 已经因为它提供的 Dashboard、Chart、实验平台等产品能力。API 能力是让这些资源可以被脚本、集成工具和自定义应用以程序化方式操控。

### 1.2 三类客户场景

| 场景 | 典型客户 | 核心操作 | 流量特征 |
| --- | --- | --- | --- |
| **A) 数据集成** | 数据工程/分析团队 | 查询数据、导出报表、接入数据管道或 AI 工具 | 读为主；高成本查询波动大；重完成可靠性，延迟容忍高；可重试 |
| **B) 自定义应用** | 高阶客户/ISV | 封装 Wave 资源为自身业务界面，CRUD + 查询混合 | 读写混合；持续流量；需要稳定 SLA |
| **C) 个人提效** | 独立分析师/工程师 | 通过 MCP 或脚本完成日常分析、报表、资源配置 | 读为主；零星 ad-hoc 流量；不会写重试逻辑；对可理解的错误消息敏感 |

### 1.3 范围：纳入本次产品设计的 API 面

本次限流覆盖 Wave Web API 的客户侧资源接口，无论入口是 OpenAPI还是 MCP，都在同一套中间件链保护下。以下为涉及的资源域：

| 资源域 | 资源 | 操作 | 成本类别 | 路由前缀 |
|--------|------|------|---------|---------|
| **分析** | Dashboard | add、update、delete、copy、list、charts/add/delete、query | CRUD + Query | `/uba/dashboards/` |
| | Chart | add、update、delete、copy、list、query | CRUD + Query | `/uba/charts/` |
| | Cohort（人群） | add、delete、list、update | CRUD | `/uba/cohorts/` |
| **实验** | AB 实验 | create、update、copy、delete；gate/config/exp/holdout/layer CRUD；report | CRUD + Query | `/ab/` |
| **营销** | 营销活动 | create、duplicate、launch、pause、resume、update、list、dashboard | CRUD | `/ma/campaign/` |
| **数据** | 事件元数据 | event/virtual-event/event-property/user-property/metrics CRUD | CRUD | `/dc/event/`、`/dc/metrics/` |
| | 数据管道 | create、delete、list、update、stop、test connection | CRUD | `/dc/pipeline/` |
| **配置** | 组织设置 | 查询、更新 org 信息、成员、角色（部分支持 API Token） | CRUD | `/org/` |
| | 项目设置 | 查询、更新项目信息、成员、配置（部分支持 API Token） | CRUD | `/project/` |
| | 账号管理 | 个人信息查询、token 管理（部分支持 API Token） | CRUD | `/account/` |

> 运营后台 `/op/` 也通过标准中间件链（受全局 RPS 和 session 防滥用保护），但属于内部运营管理接口，不纳入客户 API 能力的产品设计范围。


---

## 二、核心设计原则

以下是本需求文档的推导基点。这些原则从客户价值和服务安全的第一性原理出发，竞品证据仅作为佐证参考。

### 原则 1：容量归项目/组织，Token 是钥匙

**逻辑推导**：客户购买的"API 能力"是一个组织或项目的聚合容量。Token 只是访问这个容量的凭证。如果每个 token 都拥有一份独立容量，客户可以通过创建多个 token 任意放大总调用量——这使套餐容量失去意义。

**竞品佐证**：PostHog 的 Project Secret API Key 有 per-key + team aggregate 两层，注释直接写明"防止客户通过创建很多 key 乘倍获得容量"（[`ProjectSecretApiKeyTeamRateThrottle`](../../posthog-master/posthog/rate_limit.py:385)）。Amplitude 的凭证如 Data API Token 继承 account 权限，不创造独立套餐容量。

> **本需求文档的工作结论**：Token 是身份和公平性维度；Project 是实际运行时边界和容量聚合单位；Organization 是商业归属和套餐主体。

### 原则 2：操作成本决定保护策略

**逻辑推导**：不同类型请求的后端成本天然不同（不是从竞品借来的概念）。

| 操作类型 | 例子 | 后端成本特征 | 保护需求 |
| --- | --- | --- | --- |
| **元数据 CRUD** | 创建/更新/删除 Dashboard、Chart、AB 实验 | 快速（毫秒级），消耗 DB，成本低且可预测 | 基础的 rate limit 已足够 |
| **数据查询** | 查询 Chart 数据、导出报表、AB report | 慢（秒级甚至更久），消耗 DB + 计算，成本高且波动大 | 需要独立的 rate limit + concurrency limit |

> 配置获取类操作（如 AB gate 评估、远程配置拉取）为毫秒级可缓存的轻量请求，不属于本次 API QPS 保护设计范围。

### 原则 3：Session 与机器 API 隔离保护

**逻辑推导**：浏览器用户已经通过席位付费，他们的日常操作不应消耗 API 调用额度。但 Session Token 不能成为绕过 API 限流的后门——客户可能使用 Session 跑脚本，造成大量 QPS 请求。

**工作结论**：Session Token 使用独立的防滥用保护（account 级 + IP 级），不消耗项目/组织的 API 商业容量。MCP 中的 Session 场景需单独定义。

> **关键限制**：此原则不能默认"Session 已安全"。当前 Wave 实现中，Session Token 绕过 Account API Token 限流体系，也有明确的 MCP 测试确认此行为（[MCP bypass test](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/server_test.go:357>)）。因此补齐 Session 保护是产品上线的前置条件，不是可选的增强。

### 原则 4：入口不创造额外容量

OpenAPI 和 MCP 操作同一批底层资源，应共享同一份项目/组织的预算保护。这在本阶段作为讨论项（见第七节），不做出最终产品决策。

---

## 三、容量与保护模型

### 3.1 归属层级

```text
Organization / Plan
  → 是否开通 API 能力
  → 组织级 API 总预算上限（防止跨项目放大）
  → 商业套餐映射（有待讨论）
       ↓
Project（主要运行时边界）
  → 项目分配到的 API 容量（project aggregate）
  → 元数据 CRUD 的 rate limit 预算
  → Query 的 rate + concurrency 预算
  → 所有 token 之和不超过此边界
       ↓
API Token（多个）
  → per-token 公平性（防止单 key 打爆项目）
  → 身份、scope、撤销、审计
  → 不创造独立容量
       ↓
Session（浏览器用户）
  → 独立防滥用保护（account 级 + IP 级）
  → 不消耗项目/组织的 API 商业容量
```

> **关于 Organization 的定位**：本阶段认为 organization 是商业归属单位（是否开通、套餐映射、长期用量汇总），project 是运行时限流单位。如果产品确认后需要 organization 总容量上限（防止多 project 放大），可在同一模型上增加但不在第一版锁死。

### 3.2 三层保护

| 层 | 保护对象 | 风险场景 | 设计 |
| --- | --- | --- | --- |
| **Layer 1: Per-token** | 单个 API key | 一个 CI/CD token 的脚本出 bug 每秒打 1000 次 | 每个 token 有独立 rate limit；超限只影响该 token，不影响其他 token |
| **Layer 2: Project aggregate** | 项目内所有 token 之和 | 同一项目用了 5 个 token，加起来打爆后端 | 项目级 CRUD rate + Query rate + Query concurrency 各自有上限 |
| **Layer 3: Session 防滥用** | 浏览器用户的请求 | 客户拿 session token 跑脚本，大量 QPS 请求 | per-account 频率 + IP 级保护；不消耗项目 API 容量 |

**为什么三层缺一不可：**

- 只有 Layer 1 没有 Layer 2：token 数量成为无限扩容的漏洞（创建 100 个 token 获得 100 倍容量）
- 只有 Layer 2 没有 Layer 1：一个 token 出问题可能拖垮整个项目（其他 token 被连坐）
- 没有 Layer 3：session 成为绕过 API 限流的永久后门

### 3.3 保护维度

单一 QPS 不足以覆盖所有保护场景，需要三个独立的保护维度：

| 维度 | 控制什么 | 实现方式 | 竞品参考 |
| --- | --- | --- | --- |
| **Rate（速率）** | 每秒能发起多少请求，含突发上限（Burst）和长期总量（Sustained） | Token Bucket | PostHog: 480/min + 4800/h；Amplitude: 100/s + 100,000/d |
| **Concurrency（并发）** | 同一时刻正在处理中的请求数，查询完成才释放槽位 | Semaphore | PostHog: query 3/team、20/org |
| **Cost（加权成本）** | 复杂查询按维度加权计算（本阶段不作为 P0） | 待定 | Amplitude: cost = days × conditions × query_type |

> Rate 和 Concurrency 互不替代——只有 Rate 没有 Concurrency，慢查询堆积可占满 worker；只有 Concurrency 没有 Rate，瞬间流量打爆入口。**本阶段先实现 Rate + Concurrency**，Cost 需生产数据校准后评估。

### 3.4 Quota 执行与错误语义

不同的超额类型需要不同的产品反馈，客户才能自助定位问题。以下按 3.2/3.3 的三层保护 + 平台层对齐：

| 超额类型 | 触发层 | HTTP 状态 | 产品反馈 |
|---------|-------|---------|---------|
| Token 无效/已撤销 | 认证 | 401 | 凭证问题，建议重新创建 |
| 无权访问资源 | 权限 | 403 | 权限不足，联系组织管理员 |
| 速率超限（token/project）| Layer 1/2 Rate | 429 + Retry-After | 请求速率已达上限，稍后重试 |
| 并发超限（project） | Layer 2 Concurrency | 429 + Retry-After | 查询并发已满，稍后重试 |
| Session 速率超限 | Layer 3 | 429 + Retry-After | 浏览器请求太频繁 |
| 全局 RPS 超限 | 平台 | 429 + Retry-After | 服务繁忙，稍后重试 |
| Redis 不可用 | 依赖 | 503 | 服务暂时降级，非客户问题 |

> 当前 Wave 限流中间件返回 429 时已携带 `RateLimit-Limit`、`RateLimit-Remaining`、`Retry-After`、`RateLimit-Reset` 等响应头。本设计在现有头字段基础上补充错误语义区分，不改变已有格式。

### 3.5 Redis 故障

Wave 当前所有 Redis 用途（Session 认证、限流、缓存、Pub/Sub、分布式锁）共享同一单例 Redis。因此 **Redis 挂 = Web 服务不可用**——限流器的 fail-open/fail-close 在当前架构下无实际区别，因为 Redis 挂时 session 认证已经先于限流器失败。

> 代码证据：[GetRedisClient 返回单例](`/Users/wenshiqin/wave-worktrees/api_qps/pkg/dal/redisx/redis.go:155`)、[NewStandaloneRedisClient](`/Users/wenshiqin/wave-worktrees/api_qps/pkg/dal/redisx/redis.go:162`) 存在但未用于 session/rate-limit 隔离。

**结论**：第一版保持单例，不额外增加 Redis 部署复杂度。如果将来出现 Redis 故障导致 Web 不可用的事故，再考虑拆出独立限流 Redis——届时 revisit fail-open/close 策略。在此之前，限流器的 Redis 错误处理只需保证不误导客户（不把 Redis 错误映射为 401 "无效 token"），不需要复杂的 fail 策略设计。

---

## 四、用户故事

### 用户故事 1：客户使用 Account API Token 接入 Wave（P0）

作为购买了 API 能力的客户，我希望使用 Account API Token 调用 OpenAPI 或 MCP 来操作项目资源，并能从响应中准确知道是"没买这个能力"、"暂时打得太快了"还是"本月用量用完了"——而不是收到一个无法理解的错误码。

**商业价值**：API 能力是平台程序化访问的入口。客户买的是组织/项目的一份聚合容量，不是每一枚 token 的独立额度。

**逻辑推导**：从原则 1（容量归项目/组织，token 是钥匙）和原则 2（操作成本区分保护）直接导出。

**验收场景**：

1. **Given** 组织未开通 API 能力，**When** 客户使用任意 Account API Token 调用任何 OpenAPI/MCP 端点，**Then** 系统返回 403 并明确告知"套餐未开通此能力"，响应体区分"无权限"和"已超限"。
2. **Given** 组织已开通 API 能力，**When** 同一项目下的多个 token 并发调用，**Then** 所有 token 的请求数之和不超过项目 aggregate 上限；单个 token 爆发时不拖垮其他 token。
3. **Given** 客户在同一个项目下创建 N 个 token，**When** N 个 token 同时以各自的最大速率调用，**Then** 每个 token 各自的 burst limit 生效，且 N 个 token 之和不超过项目 aggregate。
4. **Given** 客户发起复杂查询，**When** 查询到达后端的并发超过限制，**Then** 超额的请求返回 429，响应头包含 `Retry-After`，而非直接失败或挂起。
5. **Given** 请求被限流拒绝，**When** 客户检查响应头，**Then** 能获取 `X-RateLimit-Limit`、`X-RateLimit-Remaining`、`X-RateLimit-Reset` 和 `Retry-After`，以及从 HTTP 状态码判断超额类型。

### 用户故事 2：浏览器用户在 Session 下正常使用（P0）

作为普通 Web 用户，我希望在多个页面和浏览器标签页同时操作时不频繁遇到 429；同时，攻击者不能通过伪造、盗用或轮换 session token 把我的账号或服务打崩。

**商业价值**：保持正常 Web 用户体验不受 API 限流影响；但防止 Session 成为绕过 API 保护的脚本通道。

**逻辑推导**：从原则 3（Session 与机器 API 隔离保护）导出。

**验收场景**：

1. **Given** 攻击者不断发送无效 session token，**When** 请求到达 API 入口，**Then** 在网关层受 IP 速率限制；在应用层通过 Session Token 格式预检（AES-GCM 解密失败即拒绝）阻断无效 token，防止触发 Redis 鉴权。
2. **Given** 同一 account 有多个有效浏览器 session（多设备/多标签页），**When** 这些 session 共同访问高成本接口，**Then** account 级聚合保护生效，不能通过多设备绕过。
3. **Given** 正常用户使用多个标签页浏览，**When** 请求量处于正常交互范围（非脚本），**Then** 不应因按单 session 或单 IP 粗暴限制而误伤。
4. **Given** 普通 Web 用户通过浏览器使用 MCP（如 AI 辅助分析），**When** MCP 上使用 Session Token 认证，**Then** 遵守 Session 的防滥用保护体系，不进入 Account API Token 的限流和配额路径。
5. **Given** 客户尝试用 Session Token 跑脚本（如 curl 反复调用），**When** 请求频率超过 session 保护阈值，**Then** 被 429 拒绝，不影响同一 account 在浏览器中的正常使用。

### 用户故事 3：客户管理 API 凭证（P1）

作为使用 API 的客户/组织管理员，我希望能管理多个 API token：知道每个 token 是谁创建的、上次什么时候用过、能限制每个 token 只能读或只能写、能限制只能访问某些项目，并在泄露时能快速撤销。

**商业价值**：没有基本的 token 生命周期管理，API 能力不敢真正开放给客户；没有 scope，token 一旦泄露就拥有全部权限。

**逻辑推导**：如果 API 能力要面向客户开放（场景 A/B/C），token 的可管理性是被信任的前提。参考 PostHog 源码中 Personal API Key 的完整产品设计（scope、roll/revoke、last_used、组织管控）。

**验收场景**：

1. **Given** 组织管理员查看 API token 列表，**When** 进入管理页面，**Then** 能看到每个 token 的 label、创建时间、scope 范围、状态（活跃/已撤销）和最后使用时间（异步更新，精度到小时级即可）。
2. **Given** 客户创建 API token，**When** 设置权限，**Then** 可选择 read-only 或 read-write scope，并可限定到指定项目或组织。
3. **Given** 客户发现 token 泄露，**When** 在管理页面撤销该 token，**Then** 已经分发的 token 立即失效。
4. **Given** 组织管理员不希望普通成员使用 API token，**When** 在组织设置中关闭"非管理员可使用 API key"，**Then** 运行时校验请求者角色，非管理员立刻无权使用；新 token 只允许管理员创建。
5. **Given** 客户审计 API 调用记录，**When** 查看审计日志，**Then** 能追溯到调用来自哪一枚 token（而非仅知道哪个 account）。

### 用户故事 4：平台在故障下保持可控（P1）

作为平台运维者，我希望限流依赖故障时错误语义正确，不误导客户；且攻击流量和高成本查询不会把整个服务拖垮。

> **架构约束**：Wave 当前使用单例 Redis（[`redisx.GetRedisClient()`](`/Users/wenshiqin/wave-worktrees/api_qps/pkg/dal/redisx/redis.go:155`)），session 认证和 rate-limit 计数器共享同一实例。"限流 Redis 挂但 Session Redis 正常"的场景在拆分前不存在。详见 3.5。

**商业价值**：依赖故障时的行为必须可预期、可观测。

**验收场景**：

1. **Given** Redis 不可用，**When** 已认证的请求到达限流中间件，**Then** 请求被放行（fail-open），限流错误记录到 metrics 供运营发现（此场景仅当 Redis 在请求处理中途故障、或已认证请求的后续处理依赖 Redis 时有效）。
2. **Given** Session Redis 不可用，**When** 用户发起需要认证的请求，**Then** 返回 503，而非把 Redis 错误映射为 401 "无效 token"——此条不依赖 Redis 架构，可直接修复。
3. **Given** 某个项目发起大量高成本查询，**When** 查询并发达到上限，**Then** 超额请求被合理节流，不影响其他项目的查询——此条不依赖 Redis。

### 用户故事 5：产品团队能配置套餐规则（P2）

作为产品和运营人员，我希望能用少量清晰的字段表达 API 能力套餐，而不是为每个 token、每条路径维护一套难以解释的配置。

**商业价值**：复杂的定价规则难以交付客户、难以解释超限行为。

**验收场景**：

1. **Given** 运营人员在后台配置 API 套餐，**When** 设置组织套餐，**Then** 套餐字段至少包含：API 能力是否开通、项目级默认 rate limit、默认 concurrency limit。
2. **Given** 套餐配置变更，**When** 新的计费周期开始，**Then** 新规则生效，旧周期的计数不应用于新周期。

---

## 五、限制规则矩阵

### 5.1 Account API Token 保护设计

Account API Token 是面向开发者的机器凭证，通过 `Authorization: Bearer <token>` 传递。请求依次经过 3 层保护，每层覆盖不同的风险场景。

#### 5.1.1 保护路径

| 层级 | 保护层 | 维度 | 作用域 | 风险场景 | 商业容量 |
|------|--------|------|--------|---------|---------|
| #1 | **凭证** | 单 token 突发速率 | per token | 单枚 token 代码 bug 或异常突发，保护 Redis 和下游 | 否，安全阈值 |
| #2 | **项目** | 项目聚合速率（加权） | per project | 多 token 多路径并发叠加超出套餐承诺 | ✅ 套餐容量 |
| #3 | **平台** | 全局 RPS | 全站 | 基础设施过载，保护 Redis 单例 | 否，硬编码保护线 |

**各层设计说明：**

- **#1 单 token 突发速率 (10/s 全局统一)**：不区分套餐。如果 per-token 按套餐划分，客户可创建多枚 token 叠加超出项目容量（例：PRO 的 project=100/s，若 token 上限为 60/s，2 枚即可达 120/s → 超出项目聚合）。10/s 是安全阈值，不是容量承诺。
- **#2 项目聚合速率（加权）**：这是**真正的套餐容量**。所有指向同一项目的 token 共享加权容量池。加权 cost 参考 Wave 现有 [`resolveRequestCost`](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go:267>)——UBA 查询/下载=3 单位、AB report=2 单位、CRUD=1 单位。
- **#3 全局 RPS (2000/s)**：全站硬上限，保护 Redis 和下游服务。不区分套餐，不在商业合同中承诺。

> **账号聚合说明**：不设独立运行时限流桶。`MaxTokensPerUser=3` + per-token 10/s 间接约束了单账号最大聚合约 30/s，这是创建配额而非运行时保护。

#### 5.1.2 限制规则

套餐阈值为**建议初始值（待产品确认）**：

| # | 保护层 | 保护维度 | 作用域 | PLUS | PRO | MAX | 现状 |
|---|--------|---------|--------|------|-----|-----|------|
| 1 | **凭证** | 单 token 突发速率 | per token | **10/s** | **10/s** | **10/s** | ✅ per-token 已有，无 plan 感知 |
| 2 | **项目** | 项目聚合速率（加权） | per project | **30/s** | **100/s** | **150/s** | ❌ 未实现 |
| 3 | **平台** | 全局 RPS | 全站 | **2,000/s** | **2,000/s** | **2,000/s** | ✅ 硬编码 2000/s |

> **#3 加权说明**：项目聚合使用加权 cost，参考 Wave 现有 [`resolveRequestCost`](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go:267>)——UBA 查询/下载=3 单位、AB report=2 单位、CRUD=1 单位。
>
> **#1 与 #3 的关系**：Project 允许多个 token，#1 保证单枚 token 不异常突发，#3 保证所有 token 之和不超过套餐容量。#1 应 ≤ #3 / 预期最小 token 数。例：PLUS project=30/s，预期最多 3 个 token → #1 ≤ 10/s ✅。

#### 5.1.3 竞品对比

| 对比维度 | Wave | PostHog | Amplitude |
| --- | --- | --- | --- |
| **限流层级** | 3 层：单 token → 项目加权 → 全局 | 2 层：单 key → team 聚合 | 因 API 类型而异，1–3 层 |
| **窗口算法** | 纯突发(burst) | 突发(480/min) + 持续(4,800/h) | 突发(100/s) + 日额度(100,000/d) |
| **操作差异化** | 路径加权（UBA=3、AB=2、CRUD=1） | 查询类型独立 throttle | cost 加权（DSAR=8、GET=1） |
| **Plan 差异化** | 项目聚合梯度（30/100/150） | 统一值，不随 plan 变化 | 统一值，不随 plan 变化 |
| **并发保护** | 无 | 查询执行层 per-team=3 | 部分 API 5 并发 |

> **窗口算法**：竞品普遍双窗口，Wave 第一版只做 burst。(1) 当前 per-token + global 已是单层 burst，增量最小；(2) 上线后若指标显示持续跑满，再追加 sustained quota，不影响已有规则。
>
> **保护层级差异**：PostHog 只需 2 层（key → team），因其 Team=Project，team 聚合即项目级聚合。Wave 需要全局层是因 token 可跨项目，防止多 project 叠加超出平台容量。

### 5.2 Session Token 保护设计

Session Token 是面向浏览器用户的认证凭证。当前可通过 `Authorization: Bearer` 直接 curl 调用 API，且绕过 Account API Token 的所有限流（已有测试确认），构成安全缺口。

#### 5.2.1 当前架构与风险

Session 中间件（[session.go](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/session.go:62>)）的 token 提取优先级：`Authorization: Bearer` 优先，Cookie `atoken` 备选。这意味着：

- **可直接 curl**：攻击者只需从浏览器 DevTools 复制出 session token，即可用 curl 无限调用 API
- **绕过 API 限流**：Session token 不经过 #1-#4 任何一层 API token 限流
- **即使加限流也只能降频**：D1 方案（per-account + per-IP 限流）只能降低攻击速率，不能阻止单次有效请求

**根因**：Session Token 当前是 bearer token（谁持有谁使用），与 Account API Token 只有"能否创建更多 token"的区别，没有认证通道上的本质差异。

#### 5.2.2 保护方案

| | D1: 加限流 | D2: 改认证架构 |
|--|-----------|---------------|
| **做法** | Redis INCR 计数，per-account + per-IP 限流 | Session 只从 cookie 读，不接受 bearer；加 CSRF |
| **矩阵影响** | 新增 #5（账号级防滥用）、#6（IP 级防滥用） | 无需新增保护维度 |
| **优点** | 改动小，后端约 20 行 | 安全性对齐 PostHog；产品模型更简洁 |
| **缺点** | 不能阻止 curl，只能降频 | 后端约 55 行 + CSRF 中间件 + 前端 CSRF header 注入 |
| **安全级别** | 频率限制（降低攻击速率） | CSRF 阻挡写操作；HttpOnly 阻挡 XSS 窃取 |

**D2 实际改动评估（基于代码调查）：**

后端：
- **去掉 Bearer（~5 行）**：[`session.go`](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/session.go:62>) 的 `extractBearerToken` 中删除 `c.GetHeader(HeaderAuthorization)` 代码路径，仅保留 cookie 读取
- **CSRF 中间件（~50 行）**：Double Submit Cookie 模式——登录时下发一个非 HttpOnly CSRF cookie，mutating 请求比较 header 值与 cookie 值；Account API Token 请求（带 `Authorization: Bearer`）自动跳过
- **登录响应返回 token（可选）**：当前 `ConstructAPILoginResponse` 在 JSON body 中返回 `atoken` 字段，前端未使用。可选择移除

前端（已确认无需改动认证流程）：
- 当前前端**已经完全不发 Bearer 了**：[`request.ts`](</Users/wenshiqin/wave-worktrees/api_qps/fe/src/utils/request.ts:36-42>) 的请求拦截器注释："不再添加 Authorization Header，完全依赖 Cookie"。前端不存 token、不读 token、不发 token
- **仅需 CSRF header（~10 行）**：在 Axios 请求拦截器中，对 POST/PUT/DELETE 读取 CSRF cookie 并注入 `X-CSRF-Token` header
- `SetAuthTokenCookie` 已有的 `HttpOnly: true` + `SameSite: Lax` 已是最佳配置

**CSRF 过渡方案**：CSRF 中间件可先以 warn-only 模式上线（记录日志不拦截），确认前端全部覆盖 mutating 请求后再切换为 enforce 模式，避免部署时序导致请求被拒。

**CSRF token 旋转**：不做旋转，会话期固定。与 PostHog（Django 默认行为）一致。CSRF 解决的是跨站伪造攻击，跨站场景下攻击者无法读取 cookie 值，旋转与否无区别。凭据窃取（DevTools/XSS）非 CSRF 能力范围，需 HttpOnly + 限流等其他手段。

**MCP 混合方案**：MCP 无 cookie 环境，若 MCP session 是真实需求，可在 MCP 层保留 Bearer 解析。这样 D2 只影响浏览器 session，不影响 MCP 集成。

**历史浏览器兼容性**：由于前端已不发送 Bearer，后端去掉 Bearer 入口后现有浏览器会话不受影响——cookie 仍在每次请求时自动发送。仅以下场景会失效：
- 外部脚本/curl 直接 `Authorization: Bearer <session_token>` 调用 API（这正是要封堵的攻击面）
- 无 cookie 环境的 MCP session（通过混合方案解决）

> **建议**：D2 安全性更好，同时消除新增保护维度的需求。实际改动成本比之前预估更低（前端认证流程无需改动）。

#### 5.2.3 竞品对比

| 维度 | Wave | PostHog |
|------|------|---------|
| 凭证提取 | `Authorization: Bearer` 优先，cookie 备选 | Django cookie 优先，不接受 bearer |
| CSRF 保护 | ❌ 无 | ✅ `CsrfViewMiddleware` |
| 限流覆盖 | API token 限流被 session 绕过 | `BurstRateThrottle` 用 `personal_api_key_only=True` 显式跳过 session |
| curl 攻击面 | ✅ 可复制 token 直接 curl | ❌ cookie 无法被 curl 直接利用（写操作需 CSRF token） |

PostHog 没有为 session 设计独立的速率限制，原因不是"没想到"，而是其认证架构使 session *不能*作为 curl 的机器凭证：

1. **Cookie 优先**：Django session 依赖 `HttpOnly` cookie，浏览器不暴露给 `document.cookie`
2. **CSRF 保护**：所有 mutating 请求需过 `CsrfViewMiddleware`，curl 无法获取 CSRF token
3. **因此安全跳过限流**：`BurstRateThrottle` 可以安全地通过 `personal_api_key_only=True` 跳过 session——因为 session 本身就不具备 curl 攻击能力

Amplitude 类似，其 session 凭证不用于 API 调用。

### 5.3 套餐字段与定价

**套餐字段：**

| 字段 | 类型 | 说明 | PLUS | PRO | MAX |
|------|------|------|------|-----|-----|
| `max_projects` | int | 最大项目数 | 2 | 2 | 6 |
| `project_agg_rps` | int | 项目聚合速率（加权） | 30 | 100 | 150 |

API 能力是合同级 entitlement，不由 plan 模板控制；账号聚合由 token 创建上限（`MaxTokensPerUser=3`）+ per-token 10/s 间接覆盖，不作为独立套餐字段。全局 RPS（#4）和 Session 保护为全站统一配置，不按套餐区分。所有字段支持按合同独立调整。

**扩容倍率：**

| 维度 | PLUS | PRO | 倍率 | MAX | 倍率 |
|------|------|-----|------|-----|------|
| Project aggregate | 30/s | 100/s | 3.3× | 150/s | 1.5× |

Per-token 突发速率为全局 10/s，不体现套餐差异。

**token 与 project 容量关系：**
- Project 允许多个 token，但 #3 是所有 token 之和
- #1 token burst ≤ project 容量 / 预期最小 token 数
- 例：PLUS project=30/s，预期最多 3 个 token → token burst ≤ 10/s ✅

### 5.4 设计自检

- [x] 产品定位清晰，场景已覆盖三类客户
- [x] 核心设计原则从逻辑推导（客户价值、服务安全），竞品证据仅作佐证
- [x] 容量归属层级已定义（org → project → token → session）
- [x] 三层保护都有独立的风险驱动（per-token / project / session）
- [x] 操作成本已按 CRUD / Query / Config 分类
- [x] 限流形状（burst/concurrency/cost）有出处和取舍
- [x] 错误语义已区分 401/403/429/503
- [x] Redis 故障策略已在 3.5 明确
- [x] Token 生命周期能力按 P0-P2 排优先级
- [x] MCP/OpenAPI 关系标记为讨论项，不影响 P0 设计
- [x] Wave 当前缺口已按严重性标注
- [x] 边界情况和排除范围已明确
- [x] 限制规则矩阵覆盖 4 个 API token 保护维度 + session 保护方案
- [x] 套餐字段定义明确，支持按合同调整

---

## 六、待产品决策的设计选项

以下选项罗列利弊，不在此阶段替产品做选择。

### A) 查询成本加权方案（已决策，影响矩阵 #3 加权机制）

Wave 已有路径级别的 cost 加权实现（[`resolveRequestCost`](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go:267>)）：UBA 查询/下载=3 单位、AB report=2 单位、其余 CRUD=1 单位。项目聚合速率（#3）统一应用此加权，不拆分独立的并发维度。

**已做决策**：查询为异步，HTTP 返回不代表查询完成，API 层 semaphore 无效。因此不走 A1/A3，直接使用现有的 A2 风格加权。

> **PostHog 的参考**：PostHog 的查询也是异步的（POST 返回 202 + Celery 异步执行），但其并发限制不在 HTTP 层，而是在查询执行层——用 Redis ZSET 信号量控制同时在跑的 ClickHouse 查询数（per-team=3、per-org=20）。Wave 未来也可以在查询执行器中增加类似机制（Redis 计数 + 退避重试），独立于 #3 项目聚合加权速率之外。

---

### B) 套餐变更时已有 token 的速率行为

场景：组织从 PLUS 升级到 PRO（或降级），已分发的 token 该怎么办？

| 方案 | 做法 | 优点 | 缺点 |
|------|------|------|------|
| **B1: 即时继承** | 套餐变更后，所有已存在 token 立即使用新套餐的限流值 | 无歧义，客户体验一致；API 行为可预期 | 如果客户在 PLUS 下创建了 50 个 token 但每个只用到 2/s，升 PRO 后所有 token 同时扩容到 60/s，可能产生突发峰 |
| **B2: 新周期生效** | 当前计费周期结束前保持原值，新周期开始后生效 | 行为可预期；避免"月中突然变快/变慢"的困惑 | 客户可能抱怨"我已经付了 PRO 的钱，为什么还要等" |
| **B3: 保留创建时值** | token 创建时锁定套餐值，不随套餐变更而改变 | 最可预期——token 行为终身不变 | 套餐升级后新 token 和老 token 行为不同，产生管理混乱；降级时老 token 可能超过新套餐上限，需额外处理 |
| **B4: 即时继承 + 渐进扩散** | 套餐变更后立即生效，但在新值的约束下用更短窗口（如几秒内逐步爬升到目标值）平滑过渡 | 避免 B1 的突发峰；兼顾即时性 | 实现复杂；客户可能困惑"为什么我的 token 速度在变" |

**建议**：B1（即时继承）最简洁，token 继承套餐值，商业逻辑清晰。如果担心突发峰，可在配置中心加上限流爬升参数。

---

### C) API Token 访问范围

第一版只锁定 project scope，但数据库模型可以预留 org scope 的扩展路径：

| 范围 | 可访问资源 | 推出时机 |
|------|-----------|---------|
| **project scope（第一版）** | 项目内的 dashboard、chart、experiment、cohort、营销活动、事件元数据、数据管道 | P0，随 API 能力一同上线 |
| **org scope（可扩展）** | 审计日志、用量统计、组织级只读报表 | 客户需求驱动，不在第一版承诺 |
| **account scope（不建议）** | 个人 setting、token 管理自己 | 不建议——token 管理自己是鸡生蛋问题；个人 setting 应走 Web UI |

**当前代码状态**：ScopeData 已有 `org_ids` 和 `project_ids` 字段，标准 OpenAPI handler 的 scope 校验未生效。数据模型已支持 org scope，只是运行时没拦。第一版只需"锁住 project scope + 补上校验"，不做模型变更。

---

### D) Rate Limit 与 Billing Quota 的分工（设计哲学参考）

本 spec 当前将 plan 差异化直接体现在 rate limit 值上（PLUS=30/s、PRO=100/s、MAX=150/s）。但 PostHog 和 Amplitude 选择了不同的路线：

| 对比维度 | Wave 本设计 | PostHog | Amplitude |
| --- | ----------- | --------- | ----------- |
| **Rate limit 角色** | 同时做基础设施保护 + 商业容量 | 仅做基础设施保护（所有 plan 相同值） | 仅做基础设施保护（所有 plan 相同值） |
| **商业差异化手段** | 通过 rate limit 值体现 plan 层级 | 通过 billing quota / usage 控制 | 月度事件量、MTU、产品能力 entitlement |
| **超额后果** | 秒级超限 429 | 秒级超限 429（rate limit）；月度超额 block（billing quota） | QPS 超额 429（同一阈值）；事件量超额按使用计费或 block |
| **plan 对速率影响** | PLUS=30/s → PRO=100/s（3.3× 扩容） | Free/Paid/Enterprise 均 480/min burst | 所有 plan 统一 QPS 值（不存在 plan 级速率梯度） |

PostHog 和 Amplitude 这么做的基础：

1. **Rate limit 值本身就低**——PostHog 480/min=8/s burst，Enterprise 客户也一样。Amplitude 的 Dashboard REST 仅 5 并发、Experiment Management 100/s 同样对所有 plan 统一。不需要更高的 rate limit，因为商业容量不通过速率表达
2. **Billing quota / 用量承载商业差异化**——PostHog 按月度 API 查询限额计费；Amplitude 按月度事件量和 MTU 计费
3. **分工清晰**——Infra 团队管 rate limit（保护后端资源），商业团队管 quota（控制客户用量）

**对本设计的含义**：这不是"对错"问题，而是选择问题：

| 路线 | 优点 | 风险 |
|------|------|------|
| **当前（rate limit 含商业容量）** | 客户感知直接（"PRO 比 PLUS 快 6 倍"）；无需用量计费体系 | 三套 plan 值 + 套餐变更的速率迁移逻辑（见 B）；rate limit 调低影响客户体验 |
| **PostHog 路线（rate limit 统一，billing quota 差异化）** | infra 保护与商业逻辑解耦；运维简单；未来可接 usage 计费 | 需要 billing quota 体系和计费基础设施；客户可能困惑"为什么买 PRO 跟 PLUS 一样快" |

**本阶段建议**：保留当前 plan 差异化设计（project_agg_rps 梯度值），因为第一版缺乏 billing quota 基础设施。但在实现时预留路线——`project_agg_rps` 在代码中作为软件限流参数，而非硬编码合同条款，未来若引入 usage 计费，可收窄 plan 间差异，将商业差异化转移到 quota。

---

## 七、MCP 与 OpenAPI 关系（讨论项）

以下问题在本阶段未做出最终产品决策，记录为讨论项。

### 7.1 现状

- MCP 当前是独立 HTTP handler，不经标准 API 中间件链
- MCP 支持 Account API Token 和 Session Token 两种凭证
- MCP 中 Account API Token 复用同一套 per-token/global 限流
- Session Token 在 MCP 中绕过 Account API Token 限流（已有测试确认）

### 7.2 预算共享方案

MCP 与 OpenAPI 的预算共享关系：

| 方案 | 含义 | 对矩阵的影响 |
|------|------|-------------|
| **A) 共享预算** | MCP tool call 和 OpenAPI 请求扣同一份 project/org 额度 | 无需新增维度，#1-#4 已覆盖 MCP |
| **B) 独立预算** | MCP 有独立的 per-token/project/org 速率配置 | 需新增一套 MCP 列（或加倍现有字段） |

**本阶段建议**：按方案 A 推进（共享预算），原因是：

- 客户购买的是"程序化访问能力"，不是特定协议——MCP 不应成为绕过 OpenAPI 额度的旁路
- 共享预算意味着矩阵无需增加维度，减少产品认知负担
- 如果上线后发现 MCP 调用模式与 OpenAPI 差异大（如 batch 批量调用），可在#1-#4 内为 MCP 通道增加独立的权重系数

### 7.3 待讨论问题

1. MCP tool call 和 OpenAPI 请求是否共享同一份项目/组织预算？（上文的方案 A/B）
2. 如果共享，MCP batch（同时 dispatch 多个 tool call）如何折算为 cost？
3. 如果分离，客户购买的是"API 能力"还是"特定传输协议的访问权"？
4. MCP 是否需要额外保护参数（batch 大小上限、单次 body 大小上限、高频工具限流）？
   - 参考 PostHog：body 最大 1 MiB、batch 最大 100 请求

**本阶段建议**：无论共享还是分离，MCP 都不应成为绕过 OpenAPI 权益和保护的后门。MCP 的 batch/body/tool 参数保护（如 1 MiB body、100 batch）是稳定性和安全约束，需从零实现——当前 MCP server.go 的 HTTP handler 没有请求 body 大小限制，JSON-RPC handler 也没有 batch 大小限制。不必等待商业决策，但在实现评估中应列为独立开发项。

### 7.4 MCP 凭证策略建议

参考 PostHog 的做法——其 MCP 只接受 API token（`phx_`/`pha_`），**不接受 session**（详见 [_research/posthog-research.md §1.7](./_research/posthog-research.md#17-mcp-限流独立-typescript-服务--独立-redis-限流器)）：

| 凭证 | 当前 Wave MCP | 建议 | 依据 |
|------|-------------|------|------|
| Account API Token | ✅ 支持，走 per-token + global 限流 | ✅ 保持不变 | 与 REST API 行为一致 |
| Session Token | ✅ 支持但不限流 | ❌ **不再支持** | PostHog MCP 亦不支持；避免 session 成为 OpenAPI 旁路 |

理由：
- **一致性**：若 D2 方案被采纳，OpenAPI 入口已不再接受 session bearer，MCP 也应对齐
- **安全**：MCP 接受 session 等于为 session curl 攻击开放了第二个入口（OpenAPI 被 D2 封堵后攻击者可转向 MCP）
- **竞品印证**：PostHog MCP 只认 API token，session 不能走 MCP，这是行业惯例

如 MCP session 是真实用户需求（如浏览器内 MCP 客户端以登录用户身份调用），可考虑在 MCP 层保留 OAuth 流程——OAuth 产出的 access token 本身就是 Account API Token（`sm_` 前缀），天然受 per-token 限流保护，不需要 session 凭据直接入站。

---

## 八、Token 生命周期与现状缺口

### 8.1 产品能力（P0-P2 排序）

以下是从 PostHog 源码提取的机器凭证最低产品需求，顺序按实现优先级排列：

| 优先级 | 能力 | 原因 | 实现成本 | 参考 |
| --- | --- | --- | --- | --- |
| P0 | 可见性：label、创建时间、scope、状态 | 客户需要知道"有哪些 key、能用不能用" | 低，token 模型已有这些字段 | PostHog: label |
| P0 | 可撤销：撤销后立即失效 | 泄露时的唯一应对手段 | 低，已有 `deleted_at` 机制 | PostHog: revoke |
| P0 | 审计归因：token_id 入审计表 | 事故追溯和用量归因 | 中，需加列 + 透传 token 上下文到审计管道 | 当前 Wave 审计表缺少此字段 |
| P1 | 最后使用时间（last_used_at） | 帮助客户识别"僵尸 token" | **高**，每次请求写 DB 在规模下有写放大问题。需异步批量（如定时刷 Redis → DB）或接受近似值 | PostHog: last_used |
| P1 | 读写 scope：read-only / read-write | 最小权限，写操作泄露后损失可控 | 低，ScopeData 模型已有 `features` 字段 | PostHog: scopes |
| P1 | 项目/组织 scope：限定到特定项目 | 跨项目 token 泄露的影响面最小化 | 低，ScopeData 已有 `org_ids`/`project_ids` | PostHog: scoped_teams |
| P1 | 可轮换：生成新 token，旧 token 立即失效 | 安全最佳实践 | 低，创建 + 撤销的组合即可实现 | PostHog: roll |
| P2 | 组织管控：禁止非管理员使用 API key | 组织级安全策略 | **高**，需运行时校验请求者角色或在配置变更时批量失效 token | PostHog: org 可选禁用 |

### 8.2 当前 Wave 关键缺口

| 缺口 | 严重性 | 影响 |
| --- | --- | --- |
| **标准 OpenAPI scope 未生效** | P0 | OpenAPI 和 MCP 权限语义不一致 |
| **Session Token 绕过 API 限流（含 MCP）** | P0 | Session 可成为绕过 API 限流的脚本后门 |
| **缺少 project/account aggregate 限流** | P0 | 多 token 可叠加超出套餐容量 |
| **缺少 query concurrency 保护** | P1 | 几个慢查询即可占满 worker |
| **缺少 session 防滥用保护** | P0 | Session 无 account/IP/频率保护 |
| **Redis 认证错误伪装为 401，非 503** | P1 | 故障时无法区分"token 无效"和"依赖不可用" |
| **审计缺少 token_id** | P1 | 无法追溯哪一枚 key 做了操作 |

> **scope 现状**：Account API Token 已有 scope 数据模型（features、resource_mode、org_ids、project_ids）和 `ValidateScope` 函数，但标准 OpenAPI handler 生成的 `requiredScope` 当前为空字符串（`""`），scope 校验未实际生效。MCP 工具路径已执行 `ValidateScope`。补齐 "标准 OpenAPI 真正执行 scope" 的工程难度取决于 codegen 模板是否可以稳定注入 scope 逻辑——如果 codegen 生成的标准 handler 模板中 `requiredScope` 是硬编码空串，修改可能涉及 codegen 规则变更，并非简单的中间件补丁。需在实现阶段评估 codegen 侵入程度后确定工作量等级。
>
> 证据：[Token scope model](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/service.go:48>)、[Generated handler requiredScope](</Users/wenshiqin/wave-worktrees/api_qps/api/web/codegen/api.gen.go:11134>)、[MCP project authorization](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/tools/context.go:29>)。

### 8.3 已有能力摘要

| 能力 | 状态 |
| --- | --- |
| Account API Token 生命周期和 scope 数据模型 | 已实现 |
| Redis Token Bucket 限流（per-token + global） | 已实现 |
| 请求 cost（路径规则：UBA query/download=3，AB report=2，默认=1） | 已有雏形 |
| 429 响应头（RateLimit-Limit、Retry-After 等） | 已实现 |
| MCP 工具层资源权限和 ValidateScope | 已实现 |
| Session Token AES-GCM 编码 + Redis 认证 | 已实现 |
| 资源审计（account/source/project/org/IP） | 已实现 |

> 完整代码证据链见 [_research/wave-current-foundation.md](./_research/wave-current-foundation.md)。以上所有缺口均有对应的代码路径和测试验证。

---

## 九、边界情况

- **同一 token 跨多个 organization/project**：per-token 公平性保护仍然生效；业务容量按实际资源上下文（项目）决定，token 不绑定固定容量。
- **同一 organization 下多个 project 同时调用**：如果组织级总上限未启用，各 project 独立消耗自己的 aggregate；组织级上限待产品确认。
- **同一 account 创建多个 token**：不允许通过 token 数量线性放大项目容量（project aggregate 限制所有 token 之和）。
- **Session Token 多设备并发**：按 account 聚合保护，不绑定单一 IP；正常多标签页操作不误伤。
- **伪造 session token**：应在 IP/路由层级获得足够早的保护，不应无限触发 Redis 鉴权。
- **MCP Session Token**：当前会绕过 Account API Token 限流（已确认）。若继续支持 Session → MCP，必须纳入 Session 防滥用体系。
- **没有 project/org 上下文的接口**：如账户级接口，使用 channel/account/global 规则，不拼接空 project key。
- **Redis 限流计数失败**：区分"限流依赖故障"和"认证依赖故障"，不共用错误语义。当前单例 Redis 下两者同时发生，但代码中错误映射路径不同，应分别处理（见 3.5）。
- **横向扩缩容**：任何本地 fallback 限流器都会改变实际总容量，必须单独验证。
- **套餐变更**：需要定义新周期的生效时机（即时 vs 新周期）、Redis 现有桶如何处理。
- **`0` / 负数 / 缺省配置**：显式定义为"禁用"、"无限制"或"继承默认值"，不依赖 zero value 猜测。
- **Audit 没有 token_id**：确认用量归因需求前，不把 API 用量承诺为精确到 key。

---

## 十、不纳入本阶段的范围

- 不实现代码或修改中间件（这是需求文档，不是 tasks）
- 不确定最终 RPS、burst、daily/monthly quota 数值
- 不设计客户用量账单和自助 usage 控制台
- 不锁定查询成本保护方案（semaphore / cost 模型均为待选项，见六-A）
- 不引入本地 fallback、滑动窗口、多地域一致性等实现
- 不把 Session Token 直接包装为客户长期机器凭证
- 不在此阶段决定 MCP 与 OpenAPI 是否共享预算

---

## 十一、需要下一轮确认的问题

| 序号 | 问题 | 为什么需要确认 |
| --- | --- | --- |
| 1 | API 能力是按组织套餐提供，还是未来有独立 API 开发者套餐？ | 决定 organization 是唯一商业单位 |
| 2 | Project 是否是运行时的主要资源边界？Organization 是否需要共享总上限？ | 决定容量模型的具体实现 |
| 3 | MCP 是否允许 Session Token？如果允许，交互式场景 vs 自动化的范围如何？ | 决定 MCP Session 保护策略 |
| 4 | Redis 单例架构下，SessionMiddleware 和限流中间件的错误映射是否已正确区分 503 和 401？ | 当前映射为 401，见 3.5 |
| 5 | 当前部署入口是否已有可信 IP 解析和网关级限流？ | 决定应用层 session 预保护是否重复 |
| 6 | 用量与审计是否必须精确归因到 token ID？ | 决定审计表扩展方案 |
| 7 | 后续优先验证哪项数据：项目级资源争抢、查询并发还是 Redis 故障 SLO？ | 决定第一版的验证优先级 |
| 8 | 生产实际 `aapi_rl_*` 配置值和 `session_token_secret` 管理状态？ | 决定当前限流参数的实际基线 |

---

## 附录：竞品证据索引

完整调研文档见 [_research/ 目录](./_research/)。以下仅列出本需求文档直接引用的证据。

### Amplitude

| 证据 | 对应设计原则/需求 | 参考链接 |
| --- | --- | --- |
| 6 种凭证、每种不同作用域（project/org/account） | 原则 1：凭证是身份不天然是容量 | [amplitude-research.md](./_research/amplitude-research.md#一token-分类与限流现状) |
| Dashboard REST：5 并发 + cost 加权 | 原则 2：操作成本决定保护策略 | [Dashboard REST API](https://amplitude.com/docs/apis/analytics/dashboard-rest) |
| Experiment Mgmt：100/s + 100,000/day per project | 3.3 限流形状 | [同上](./_research/amplitude-research.md#一token-分类与限流现状) |
| 401/403/429 区分语义 | 3.4 错误语义 | [同上](./_research/amplitude-research.md#二设计逻辑凭证分离和限流拆分是有意选择) |
| 三层模型：套餐/operational limit/RBAC | 3.1 归属层级 | [同上](./_research/amplitude-research.md#三商业策略) |

### PostHog

| 证据 | 对应设计原则/需求 | 源码/参考路径 |
| --- | --- | --- |
| PSAK per-key + team aggregate | 原则 1：多 token 不能叠容量 | [`rate_limit.py:385`](../../posthog-master/posthog/rate_limit.py:385) |
| Burst 480/min + Sustained 4800/hour | 3.3 限流形状 | [rate-limiter.ts:23](../../posthog-master/posthog/rate_limit.py:133) |
| Query concurrency: 3/team, 20/org, 6/dashboard | 3.3 查询并发 | [limit.py:83](../../posthog-master/posthog/clickhouse/client/limit.py:83) |
| Personal API Key: scope、roll/revoke、last_used、org 管控 | 第八节（Token 生命周期） | [personal_api_key.py:34](../../posthog-master/posthog/models/personal_api_key.py:34) |
| MCP 两层限流：入口 480/min + 上游 429 重试 | 第七节（MCP 讨论项） | [rate-limiter.ts:23](../../posthog-master/services/mcp/src/hono/rate-limiter.ts:23) |
| Redis 运行时 fail-open + 启动时 fail-close | 3.5 Redis 故障策略 | [index.ts:11](../../posthog-master/services/mcp/src/hono/index.ts:11) |
