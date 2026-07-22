# Wave 当前基础与背景调研：资源 API、Account API Token、Session Token、MCP

> 调研日期：2026-07-21  
> 调研对象：`/Users/wenshiqin/wave-worktrees/api_qps` 代码快照  
> 文档性质：Wave 本地实现事实，不是实现方案，也不代表生产部署已经启用全部能力。

## 1. 调研边界与结论口径

本调研只关注面向前端资源的 API CRUD 和机器接入入口：Dashboard、Chart、AB 资源的创建、读取、更新、删除，以及 OpenAPI/MCP 的认证、权限和请求保护。

不纳入本次判断的接口包括事件入库、AB 在线评估、实时分流和其他本身有独立容量模型的在线接口。它们可能共享平台总保护，但不应直接拿来推导资源 CRUD 的商业 QPS。

文中使用三种口径：

- **已实现**：可以在当前代码中找到明确的执行路径或测试。
- **已建模但未确认生效**：有字段、函数或 OpenAPI 注释，但当前请求路径未证明会执行。
- **待产品/部署确认**：代码无法决定，例如套餐归属、真实生产流量和网关是否已有保护。

## 2. Wave 的资源 API 面

当前标准 Web API 已经暴露了资源 CRUD 族：

| 资源 | 当前公开 API 族 | 这次调研的意义 |
|---|---|---|
| Dashboard | `/uba/dashboards/add`、`copy`、`list`、`/{id}`、`update`、`delete`、`charts/add`、`charts/delete` | 典型的项目资源 CRUD，存在创建/更新/删除和关联操作 |
| Chart | `/uba/charts/add`、`copy`、`list`、`/{id}`、`query`、`update`、`delete` | 既有元数据 CRUD，也有高成本 query；不能只按路径前缀视为同一种请求 |
| AB 资源 | `/ab/create`、`/ab/exp/update`、`/ab/gate/update`、`/ab/config/update`、`list`、`copy`、`delete` 等 | 资源配置 CRUD 与 report/eval 请求必须区分 |

证据：[Wave OpenAPI Dashboard/Chart 路由](</Users/wenshiqin/wave-worktrees/api_qps/api/web/web.openapi.yaml:7569>)、[Wave OpenAPI AB 路由](</Users/wenshiqin/wave-worktrees/api_qps/api/web/web.openapi.yaml:8847>)。

### 当前已有的资源级产品约束

- **项目配额**：Dashboard/Chart 创建时检查项目配额。[Dashboard service](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/dashboard/dashboard.go:73>)、[Chart quota](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/chart/chart.go:83>)
- **资源权限**：读取/编辑使用 `CheckViewable` / `CheckEditable`。[Dashboard permission](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/dashboard/dashboard.go:48>)、[Chart permission](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/chart/chart.go:47>)
- **审计事件**：Dashboard、Chart、Experiment 已有 `created/updated/deleted` 事件。[Audit registry](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/auditlog/audit.go:40>)
- **Account API Token 限流**：已实现 Redis token bucket（per_token 默认 3/s + global 2000/s），通过全局 Gin 中间件对所有通道生效（含标准 API 和 MCP）。(</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go>) [middleware](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/account_api_token_rate_limit.go>)

以上均非请求速率、并发或套餐额度类的 API 限流。

## 3. 标准 API 的认证和中间件链

标准 `/api` 路由的实际顺序如下：

```text
Language
  -> AccountAPITokenMiddleware
  -> AccountAPITokenRateLimitMiddleware
  -> SessionMiddleware
  -> ProjectFilter
  -> OrganizationFilter
  -> RequestContextMiddleware
  -> generated handler / service
```

证据：[Wave server route registration](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/server.go:497>)。

这个顺序决定了当前限流能看到什么：

- Account API Token 限流发生在 `ProjectFilter` 和 `OrganizationFilter` 之前，所以当前限流器不能直接使用规范化的 `project_id` / `org_id` bucket。
- `ProjectFilter` 主要负责从请求中提取项目并确认项目存在；它不是完整的成员权限检查。[ProjectFilter](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/project.go:85>)
- `OrganizationFilter` 主要负责提取或通过项目反查组织并写入 context，注释也明确“不做鉴权”。[OrganizationFilter](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/organization.go:41>)
- 资源查看、编辑和角色权限由后续 controller/service 负责，例如资产检查和项目成员/角色权限检查。

### Account API Token 的当前模型

Account API Token 是 account 所有的长期机器凭证。数据库记录包含 `account_id`、token hash、hint、status、scope JSON、过期时间、`last_used_at` 和创建/更新者；原始 token 不作为数据库字段保存。[Account API Token DAO](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/dao/global/account_api_token.go:18>)

Token scope 目前建模为：

```text
features       功能范围
resource_mode  all / orgs / projects
org_ids        指定组织
project_ids    指定项目
mcp_resource   MCP audience 绑定，可选
```

证据：[Token scope model](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/service.go:48>)。

但“有模型”不等于“标准 OpenAPI 已经执行”：

- `ValidateScope` 函数本身会检查 token 状态、功能 scope、org/project 资源范围。[ValidateScope](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/service.go:719>)
- 当前全仓代码搜索显示，除 MCP 工具上下文外，没有标准 controller/service 调用 `ValidateScope`。
- 生成的标准 API handler 虽有 scope 校验钩子，但当前 `requiredScope` 是空字符串，因此该钩子不产生实际校验。[Generated handler hook](</Users/wenshiqin/wave-worktrees/api_qps/api/web/codegen/api.gen.go:11134>)
- MCP 工具明确执行项目成员检查、项目反查组织和 `ValidateScope`，再按 tool 声明检查角色权限。[MCP project authorization](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/tools/context.go:29>)、[MCP tool permissions](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/tools/tool.go:51>)

**产品含义**：后续 spec 不能把当前 Account API Token 描述成“已经完整实现了按资源的最小权限”。应把“标准 OpenAPI scope 接入”列为待验证的产品/安全能力，而不能默认当作现状。

## 4. 当前 Account API Token 限流

### 4.1 已实现的维度和算法

当前限流器使用 Redis Lua Token Bucket，Lua 脚本以单 key 的读改写原子性来避免并发超发。当前实际生效维度只有：

```text
per_token + global
```

请求顺序是先检查 token bucket，再检查 global bucket；token key 使用 token 的 SHA-256，不把原 token 放入限流 key。[Rate limiter](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go:26>)、[Check order](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go:155>)

请求 cost 目前是很小的路径规则：默认 1，部分 UBA query/download 为 3，部分 AB report 为 2。Dashboard/Chart/AB 的 create/update/delete 当前没有独立资源操作 cost 体系；它们会落入默认 cost。[Request cost](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit.go:267>)

### 4.2 配置和响应

Web 配置字段是：

```text
aapi_rl_enabled
aapi_rl_fail_open
aapi_rl_per_token_rps
aapi_rl_global_rps
```

代码默认值为 disabled、fail-open、per-token 3、global 2000；当前 prod/dev 配置文件显式打开了 `aapi_rl_enabled`，其余字段未在这些文件中显式覆盖，因此实际值依赖配置合并/默认值，不能把代码常量 `MaxTokenRPS=10` 当作当前生产 RPS。[Web config defaults](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/config/web_cfg.go:155>)、[Prod config](</Users/wenshiqin/wave-worktrees/api_qps/configs/web/web.prod.yml:56>)、[Token service constants](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/service.go:28>)

阻断时返回 HTTP 429，并带 `RateLimit-Limit`、`RateLimit-Policy`、`RateLimit-Remaining`、`Retry-After`、`RateLimit-Reset`；Redis 出错时，fail-open 放行，fail-close 当前也返回 429，但消息是 “Rate limit service unavailable”，并不是 503。[Rate limit middleware](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/account_api_token_rate_limit.go:22>)

### 4.3 已有测试证明的范围

现有测试覆盖 per-token 拒绝、global 拒绝、响应头以及中间件 fail-open/fail-close；这说明当前实现的测试目标是“保护 Account API Token 入口”，尚未覆盖 org/project/account 聚合、资源操作成本或套餐 policy。[Rate limiter tests](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/account/apitoken/ratelimit_test.go:13>)、[Middleware tests](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/account_api_token_rate_limit_test.go:26>)

## 5. Session Token 的当前模型与攻击面

### 5.1 认证机制

Session Token 使用 `st2.<sealed_account_id>.<session_id>` 结构：account ID 用 AES-256-GCM 封装，session ID 为 32 字节随机值；服务端 session 状态保存在 Redis，认证时通过 Lua 原子检查 session 是否存在、是否过期，并清理过期索引。[Token codec](</Users/wenshiqin/wave-worktrees/api_qps/pkg/lib/session/token_codec.go:27>)、[Session Redis auth](</Users/wenshiqin/wave-worktrees/api_qps/pkg/lib/session/session.go:44>)

浏览器 cookie 名为 `atoken`，设置 `HttpOnly`、`SameSite=Lax`，HTTPS 下设置 `Secure`；账号 session TTL 当前为 14 天。[Cookie](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/cookie.go:11>)、[Session TTL](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/controller/account/account.go:30>)

### 5.2 当前缺口

- 标准 API 的 Account API Token 限流中间件只处理 `IsAccountAPIToken=true`，因此 Session Token 不消耗 Account API Token 的 token/global bucket。
- 标准 SessionMiddleware 每次需要认证的请求都会调用 Redis；无效 token 和 Redis 认证错误当前都映射为 Account Unauthorized，无法从 HTTP 行为上区分“token 无效”和“依赖不可用”。[Session middleware](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/session.go:47>)
- 当前代码没有看到统一的 session account/IP/route 保护层。攻击者可以通过大量无效 token 请求反复触发 session Redis 鉴权，或通过多个有效 session 叠加请求。
- `InitTokenCodec` 收到空 secret 时会保留懒初始化逻辑，懒初始化使用代码内固定默认 secret；当前配置样例没有展示 `session_token_secret`。这不等于已证明生产使用默认 secret，但它是上线前必须核查的安全前置条件。[Token codec fallback](</Users/wenshiqin/wave-worktrees/api_qps/pkg/lib/session/token_codec.go:27>)、[Web init](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/server.go:216>)
- pvctx 会把原始 token 放进 request context，并在 `BackGroundCtx` 中复制；审计表本身不保存 token 原文或 token ID。后续设计应避免把 secret 带入日志、异步任务或可观测性字段。[pvctx token propagation](</Users/wenshiqin/wave-worktrees/api_qps/pkg/lib/pvctx/pvctx.go:9>)

**产品含义**：Session Token 不能直接复用 Account API Token 的商业额度，但必须有独立的预认证/IP/账户保护；MCP 是否允许 Session Token 也应被当作产品边界，而非仅仅是认证实现细节。

## 6. MCP 当前行为

`/api/mcp` 是独立注册的 HTTP handler，不经过标准 `/api` 的 Account API Token 限流、SessionMiddleware、ProjectFilter 和 OrganizationFilter；MCP 自己处理认证、项目 header、audience、限流和上下文注入。[MCP route](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/server.go:513>)、[MCP context injection](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/server.go:96>)

MCP 认证当前支持两种凭证：

| 凭证 | 当前行为 |
|---|---|
| Account API Token | 从 token cache 取账号和 scope；校验可选的 `mcp_resource` audience；设置 Account API Token 上下文；使用同一套 per-token/global Redis 限流 |
| Session Token | 通过 session Redis 鉴权；允许访问 MCP；不进入 Account API Token 限流 |

对应代码：[MCP authentication](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/server.go:128>)、[MCP Account API rate limit](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/server.go:182>)。

当前测试名称和断言直接证明 Session Token 绕过 Account API Token 限流；因此”补齐 Session Token 的请求保护”是明确的现状缺口，不是推测。[MCP bypass test](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/server_test.go:357>)

MCP 工具层对资源访问更完整：需要项目上下文的工具检查项目成员；Account API Token 再检查资源 scope；Dashboard/Chart 等写工具按角色权限声明 `PermDashboardEdit` 等。[MCP project scope](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/tools/context.go:29>)、[MCP dashboard permissions](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/tools/asset.go:414>)

### 6.1 与 PostHog MCP 对比

| 维度 | Wave MCP | PostHog MCP |
|------|---------|-------------|
| 部署架构 | 同 Web 服务（Go），`/api/mcp` 裸路由 | 独立 Cloudflare Worker（TypeScript/Hono），边缘节点 |
| 认证方式 | Account API Token（`sm_`）+ Session Token | Personal API Key（`phx_`）+ OAuth token（`pha_`）+ JWT |
| Session 支持 | ✅ 支持且不限流 | ❌ 不支持 |
| 限流器 | 复用 REST API per-token + global（同一组 Redis key） | 独立 Redis，值同 REST API（key 独立命名空间 `mcp:rl:*`） |
| 限流窗口 | 单层 burst | burst（480/min）+ sustained（4800/hour）双窗口 |
| 限流 key 维度 | token hash | userHash（token 的 PBKDF2 哈希） |
| Batch/body 限制 | ❌ 无 | batch ≤ 100，body ≤ 1MB |
| 并发限制 | ❌ 无 | ❌ 无（仅监控） |
| 故障模式 | fail-open（可配置） | fail-open |

关键差异：
1. **PostHog MCP 不接受 session**——只有 API token 能走 MCP，与 D2 理念一致
2. **PostHog MCP 限流独立但与 REST API 值对等**——同一套保护标准，但 Redis key 隔离
3. **PostHog 有 batch/body 上限**——作为资源保护层，Wave 当前无此限制

## 7. 审计与“到底是谁在用 key”

### 已有能力

- Account API Token 认证会写入 account ID、account name、`IsAccountAPIToken=true`、原始 token 和 `AuditSource=openapi`。[Account token middleware](</Users/wenshiqin/wave-worktrees/api_qps/pkg/ginx/middleware/account_api_token.go:14>)
- MCP 不论使用 Account API Token 还是 Session Token，都会把审计 source 设为 `mcp`，并记录 client IP。[MCP context](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/mcp/server.go:116>)
- 资源审计行至少记录 org/project/account/account name、资源类型、动作、source、IP 和 extra。[Audit row](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/service/auditlog/audit.go:171>)、[Audit DAO](</Users/wenshiqin/wave-worktrees/api_qps/apps/web/dao/global/audit_log.go:12>)

### 仍然不准确的地方

当前审计表没有 `api_token_id`、token label 或 token hash/hint 字段。因此：

- 可以知道“哪个 account 通过 openapi/mcp 做了操作”；
- 不能可靠知道“该 account 创建的哪一枚 key 做了操作”；
- 通过同一 account 的多个 token 调用时，审计只能聚合到 account/source；
- IP 只能作为辅助证据，不能替代 token 身份，尤其在 NAT、代理和多租户自动化环境中。

这不是本次 QPS spec 必须马上扩展审计表的结论，但它会影响商业 API 的 token 管理、撤销、事故追溯和用量归因，应该进入产品决策清单。

## 8. 与 PostHog / Amplitude 对 Wave 的直接映射

结合竞品研究，Wave 当前最接近的不是“只按组织”或“只按 token”，而是分层：

```text
organization / plan  -> 商业能力开通、套餐与长期用量归属（待确认）
project              -> 资源实际归属、资源争抢与主要运行时聚合边界
account              -> 跨 token 的拥有者与反滥用聚合边界
token                -> 单凭证公平性、撤销、MCP audience 和短时 burst
route / operation    -> 资源成本分类；query、export、CRUD 不应同价
platform global      -> Wave 自身总容量保护
session / IP         -> Web 认证入口的预认证与安全保护，不消费机器 API 商业额度
```

这是 PostHog 风格“凭证 + 项目/团队聚合 + 高成本查询/并发”的原则性借鉴，不是复制 PostHog 的数值或内部结构。Amplitude 的证据则提醒我们：凭证作用域、资源边界、接口 rate/concurrency/daily quota 和套餐 entitlement 也可以分开建模。

竞品资料：[Amplitude](./amplitude-research.md)、[PostHog](./posthog-research.md)。

## 9. 对本期需求讨论的影响

### 应当进入 P0 的问题

1. 标准 OpenAPI 是否要真正执行 Account API Token 的功能/资源 scope；如果不做，MCP 与 OpenAPI 的权限语义会不一致。
2. Account API Token 的商业能力是否以 organization/plan 开通；如果一个 account 可跨多个 organization，额度如何归属必须先定义。
3. 多 token 不能放大购买容量：至少要有 token 之外的 account 或资源聚合保护。
4. Session Token 和 MCP Session 是否继续允许；若继续允许，必须定义 IP/账户/路由/并发保护，不应继续默认绕过。
5. Dashboard/Chart/AB CRUD 与 query/report/eval 必须分离成本类别；不能用一个全局 QPS 解释所有 API。
6. 认证 Redis 与限流 Redis 的故障语义分开：认证不能把 Redis 错误伪装成无效 token，限流也不能用一个固定 fail-open/close 覆盖所有流量。

### 现阶段不应提前锁死的内容

- 具体套餐名称、RPS、burst、daily/monthly quota 数值；
- 组织与项目是否各自拥有独立可配置额度；
- 是否新增 token ID 到审计表；
- 是否引入本地 fallback limiter；
- 是否把 MCP Session 收敛为仅浏览器交互，不支持自动化。

## 10. 待验证事实清单

| 事实 | 为什么必须验证 | 建议证据 |
|---|---|---|
| 生产实际加载的 `aapi_rl_*` 值 | 代码默认和 YAML 显式配置不等价 | 启动配置快照/部署模板 |
| `session_token_secret` 是否必填且每环境唯一 | 空 secret 可能落入固定默认值 | Secret 管理和启动日志 |
| 网关/WAF 是否已有 IP、路径、并发保护 | 决定应用层 session 预保护是否重复 | ingress/WAF 配置和指标 |
| 标准 OpenAPI handler 的权限覆盖率 | `requiredScope` 当前为空，需知道是否另有业务权限保护 | 路由矩阵 + 端到端 API token 测试 |
| Dashboard/Chart/AB 资源 CRUD 的真实耗时与并发 | 决定是否需要 concurrency，而不只是 QPS | 生产访问日志、trace、Doris/DB 指标 |
| Account 是否可跨组织使用 token | 决定 organization quota 是否足够 | 账号/组织关系与 token scope 数据 |
| audit log 是否需要 token 级归因 | 决定用量、撤销和事故调查产品价值 | 客户/运维访谈与合规要求 |

## 11. 研究结论

Wave 已经有：Account API Token 生命周期和 scope 数据模型、Redis token bucket、项目资源配额、资产权限、MCP 工具授权、资源审计。

Wave 尚未形成的，是把这些零散基础组合成一个一致的产品契约：谁购买 API 能力、谁承担聚合额度、哪个项目消耗资源、哪个 token 负责公平性、Session 如何防攻击、MCP 与 OpenAPI 如何统一、Redis 故障如何区分安全和可用性。

因此，下一版 `01-spec.md` 应以“分层职责 + 明确现状缺口 + 候选策略 + 讨论问题”为主，不应直接把某个 Redis key 设计或某组 RPS 写成最终实现要求。
