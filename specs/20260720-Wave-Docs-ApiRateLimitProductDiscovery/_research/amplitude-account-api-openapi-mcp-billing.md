# Amplitude Account API、OpenAPI/MCP 限制与计费策略调研

> 调研日期：2026-07-20  
> 调研范围：Amplitude 官方公开文档与定价页面  
> 调研目的：为 Wave 的 Account API Token、OpenAPI/MCP 机器接入、组织套餐和请求保护讨论提供可追溯证据。  
> 文档性质：竞品研究与需求讨论材料，不是实现方案，也不代表 Wave 已确认决策。

## 一、先校正术语：Amplitude 没有一个统一的“Account API”

Amplitude 的公开文档把 API 凭证拆成多个类型，不能直接把它理解为“一个 account token 对应一个 account QPS”。与 Wave 当前问题最接近的是 Data API Token，但它只是其中一种凭证：

| Amplitude 凭证/入口 | 官方描述的作用域或能力 | 对 Wave 研究的意义 |
| --- | --- | --- |
| Project API Key | 标识一个 project，主要用于事件写入 | project 是真实资源隔离边界之一 |
| Project Secret Key | 认证服务端 Analytics API；一个 project 可以有多个 secret key | 不能把“多个 key”直接当成多份套餐容量 |
| Data API Token | 不需要邮箱密码；拥有与直接登录相同的角色和权限；官方称其权限是 account 全局权限 | 最接近 Wave 的 Account API Token，但它本身不是独立 quota |
| Organization-level API Key/Secret | 某些 API 要求组织级 key；可访问整个 organization | 部分 API 的身份与限额天然是 organization 级 |
| Experiment Management API Key | 用于 Experiment Management API 的 Bearer 认证 | API 类型可以拥有独立的凭证和限额模型 |
| Product-data MCP OAuth | 使用现有 Amplitude 用户权限；组织可禁用；`USE_MCP_READ`/`USE_MCP_WRITE` 可按 project 分配给角色、组或 service account | MCP 是受权限模型约束的产品入口，不是匿名的“无限 AI 通道” |

证据：Amplitude 的 [Keys and Tokens](https://amplitude.com/docs/apis/keys-and-tokens) 明确区分 project key、secret key、Data API Token 和 organization-level key，并说明 project key 只能操作所属 project、Data API Token 继承 account 权限；[Amplitude MCP](https://amplitude.com/docs/amplitude-ai/amplitude-mcp) 说明 MCP 使用 OAuth 和现有 account 权限，并提供组织开关与 project 级 RBAC。

### 结论

对“Amplitude 的 account API 调用 OpenAPI”更准确的表达是：

1. 客户使用某种 API 凭证访问某一类 REST API；
2. 凭证权限决定能访问哪些 project/resource；
3. 不同 API 自己定义 project 级、organization 级、并发、查询成本或写入保护；
4. 套餐和组织用量决定商业能力与长期额度；
5. 这些维度不是同一个 QPS 字段。

还需要注意：Data API Token 的“account 全局权限”不等于它可以调用所有 Amplitude REST endpoint。具体 endpoint 仍可能要求 project secret、organization-level key 或专用 Management API key，必须以对应 API 文档的认证要求为准。

### 1.1 凭证生命周期也有独立限制

Amplitude 的 API key 管理文档说明：

- 一个 project 最多有 `50` 个 active API keys；
- key 可以被 Manager/Admin 创建或撤销，撤销是永久操作；
- 极少数情况下，撤销后的 key 停止工作最多可能需要 `6 hours`。

这些不是请求 QPS，但会影响 Wave 对 token 数量、撤销生效时间和安全事件响应的产品承诺。证据：[Manage your API keys and secret keys](https://amplitude.com/docs/admin/account-management/manage-your-api-keys-and-secret-keys)。

## 二、OpenAPI/REST 的限制：按 API 类型拆分，不存在一个统一值

Amplitude 官方页面把 REST API 的限制写在具体 API 文档中。以下是本次核查到的、可直接引用的数值。

| API | 官方限制 | 作用域/计量方式 | 超限行为 |
| --- | --- | --- | --- |
| Experiment Management API | `100 requests / 1 second`；`100,000 requests / day`，每日 UTC 重置 | per project | 返回 HTTP `429 Too Many Requests`；另有 `401` 无效/撤销 key、`403` 无环境权限 |
| Dashboard REST API | 默认最多 `5` 个并发请求，覆盖 Amplitude REST API（含 cohort download）；User Activity/Search 为 `10` 个并发；User Activity/Search 为 `360 queries/hour` | per project；不同 endpoint 还有 concurrency、rate 和 query cost | 超限返回 `429`，响应会说明触发了哪类限制 |
| Dashboard REST API 的部分图表查询 | 文档使用 cost 计算：`cost = days × conditions × query type cost`；部分图表类型有 `1,000 cost / 5 minutes` 和 `108,000 cost / hour` 的限制 | per project；不是简单按请求数，而是按查询成本 | 触发 rate/concurrency/cost 限制时返回 `429` |
| User Profile API | `600 API requests / minute`，覆盖所有 endpoint | organization | 超限返回 `429`；需要更高上限时联系 Support |
| HTTP V2 事件写入 | Starter：`100 batches/sec`、`1,000 events/sec`；另有 user/device 维度保护：超过平均 `30 events/sec` 可能被限流 | project/endpoint + user/device；这是 ingestion，不是查询 OpenAPI | `429`；返回受限 device/user/event 信息，文档建议暂停约 30 秒后重试 |

证据：

- [Experiment Management API limits](https://www.amplitude.com/docs/apis/experiment/experiment-management-api)；
- [Dashboard REST API rate、concurrency 和 cost](https://amplitude.com/docs/apis/analytics/dashboard-rest)；
- [User Profile API organization limit](https://amplitude.com/docs/apis/analytics/user-profile)；
- [HTTP V2 API limits and throttling](https://amplitude.com/docs/apis/analytics/http-v2)。

### 这组证据说明了什么

Amplitude 的限制至少有四种不同含义：

- **请求速率**：例如 Management API 的每秒请求数；
- **时间窗口预算**：例如每天请求数、每小时 query/cost；
- **并发容量**：例如 Dashboard REST 的并发请求数；
- **资源成本**：复杂查询按 days、conditions、query type 换算 cost。

因此，客户看到的“API 额度”不应只设计成一个 `qps`。对高成本查询，仅提高 QPS 也可能无法解决后端被长查询占满的问题；对低成本管理接口，每日总量又可能比瞬时 QPS 更重要。

### 作用域也不是二选一

Amplitude 的官方证据同时覆盖了不同作用域：

- Experiment Management API 是 **per project**；
- Dashboard REST API 也是 **per project**，并进一步按 endpoint 的并发、rate、cost 限制；
- User Profile API 是 **organization** 级；
- HTTP V2 还叠加了 user/device 级保护。

所以不能从“Amplitude 使用 project 限制”或“Amplitude 使用 organization 限制”推导出统一答案。实际模型是：API 类型决定资源边界，组织负责聚合商业和平台容量，project 负责资源隔离，user/device/IP/token 等维度负责反滥用或公平性。

## 三、Amplitude MCP 的权限与限制

### 3.1 MCP 是产品数据访问入口，不是事件高吞吐入口

Amplitude MCP 可以读写 charts、dashboards、experiments、cohorts、events/properties 等产品数据；官方明确建议不要把 MCP 用于生产环境或高容量事件写入，事件写入应使用 SDK 或 HTTP V2。官方还建议脚本化自动化使用 REST API，而 MCP 更适合查询、taxonomy 和内容操作。

证据：[Amplitude MCP 的适用范围、写入边界和 REST API 建议](https://amplitude.com/docs/amplitude-ai/amplitude-mcp)。

另有一个容易混淆的入口：[Docs MCP Server](https://amplitude.com/docs/amplitude-ai/docs-mcp-server) 是公开、只读、无需认证的“文档查询 MCP”，只能访问 Amplitude 文档；它不等于访问客户项目数据的 authenticated Amplitude MCP。

### 3.2 公开文档确认的 MCP 控制点

| 控制点 | 官方证据 | 对 Wave 的含义 |
| --- | --- | --- |
| 认证 | Remote MCP 使用 OAuth 2.0；使用已有 Amplitude 用户权限 | MCP 不应因为换成 AI 客户端而绕过登录用户或服务账号权限 |
| 组织开关 | MCP 默认对组织用户启用；组织管理员可在 Content Access > MCP 关闭/限制，且对所有客户端生效 | 需要 organization 级 feature/安全总开关 |
| 读写权限 | `USE_MCP_READ`、`USE_MCP_WRITE` 可分配给 role/group/service account，并按 project 授权 | MCP 至少要区分 read 与 write，并在 project/resource 层检查 |
| project 执行 | 官方说明每次 tool call 会执行 project-level access enforcement | 不能只在建立 session 时鉴权一次 |
| 区域入口 | 美国：`https://mcp.amplitude.com/mcp`；欧盟：`https://mcp.eu.amplitude.com/mcp` | 区域路由、数据驻留和故障域是部署约束 |

### 3.3 没有找到公开的统一 MCP 数值 QPS

本次核查的官方 MCP 产品页公开了认证、权限、组织开关、project enforcement、传输和 endpoint，但没有公开类似“每个 MCP token 每秒 N 次”或“每个 organization 每小时 N 次”的统一数值限额。

这只能得出一个**证据边界**：

- 不能宣称 MCP 没有限流；
- 不能把 MCP 的数值限制臆测成和某个 REST API 完全相同；
- 也不能据此断言 MCP 调用不计入底层 Dashboard/query cost；这需要产品实测、企业合同或 Support 确认。

更稳妥的产品判断是：MCP 的**商业入口和权限**与 REST API 可以统一，但底层 tool 对应的资源操作仍应继承相应 endpoint 的并发、rate、cost 和组织容量保护。这一句是基于 Amplitude API 分层证据对 Wave 的推论，不是 Amplitude 已公开确认的 MCP 内部实现。

## 四、计费策略：按组织套餐和用量收费，不是公开的 MCP 单次调用价目表

### 4.1 Amplitude 的公开商业单位

Amplitude 的公开套餐包括 Free、Plus、Growth、Enterprise。公开定价页面给出的核心信息是：

| 套餐/商业机制 | 官方公开信息 | 研究判断 |
| --- | --- | --- |
| Free | 每月最多 2 million events；无需信用卡 | 有基础产品访问，但不是无限容量 |
| Plus | 按月 tracked users/event volume 扩展；超过包含量后按同一单位价格产生额外费用 | 典型 pay-as-you-go 用量商业模型 |
| Growth/Enterprise | 按 volume、features 等定制报价 | 大客户能力和容量可以合同化 |
| MCP | 定价页明示 MCP 在包括 Free 在内的每个 plan 可用，但不同 plan 有 limited volumes/capabilities；Growth/Enterprise 可购买 dedicated add-ons | MCP 更像平台能力/套餐 entitlement，而非公开的每次调用计费 |
| 事件量超额 | 组织有月度 event volume；付费账户在 80%、90%、100%、110% 触发提醒，超额可能收费 | 计费额度与 API 实时限流是两个系统 |

证据：

- [Amplitude Pricing](https://www.amplitude.com/pricing) 明示 Free 的事件量、Plus 的扩展方式，以及 MCP 在所有 plan 可用但受套餐能力/容量限制；
- [Billing and plans](https://amplitude.com/docs/faq/billing-and-plans) 说明 Plus 的 MTU/event volume 超额计费与 MTU guardrail；
- [Limits and quotas](https://amplitude.com/docs/faq/limits-and-quotas) 说明组织月度事件量、告警、超额和非付费账户的超额处理。

### 4.2 MCP 是否收费？基于公开证据的准确回答

目前能被官方公开资料支持的说法是：

1. MCP 不是只有付费套餐才能使用；Amplitude 的定价页写明每个 plan（包括 Free）都包含 MCP。
2. 这不等于“所有 MCP 能力无限制”：同一段定价说明同时写明各套餐有不同的 volumes/capabilities，Growth/Enterprise 可购买专属 add-on。
3. 本次公开资料核查没有找到 MCP 按 tool call、token 或 request 单次计费的价格表。
4. 因此，不能把“没有公开单次 MCP 价格”写成“Amplitude 不会按合同收费”；Enterprise 的具体能力、容量、add-on 和 overage 仍可能以合同为准。

### 4.3 API 限额与计费额度应分开理解

Amplitude 的 API 文档给出 `429`、并发数、每秒请求数、每日请求数和 query cost，但这些文档没有把它们描述成一张面向客户的“每调用一次多少钱”的价格表。另一方面，计费文档围绕 organization 的 event volume、MTU、套餐和 overage 展开。

因此目前最有证据支持的模型是：

```text
套餐/合同 entitlement       -> 能不能使用某种能力、多少月度产品容量
API operational limit        -> 短时速率、并发、查询成本、每日请求保护
凭证与 RBAC                  -> 谁能访问哪些 organization/project/resource
```

这三层可能共同导致请求失败或产生费用，但语义不能混为一个 QPS 配置。

## 五、对 Wave 的产品启示（推论，不是 Amplitude 事实）

### 5.1 Account API Token 的商业归属

建议把 Wave 的 Account API Token 定义为**客户组织已购买的机器接入能力的凭证**，而不是“一个 token 买一份 QPS”：

- `organization/plan` 决定 OpenAPI/MCP 是否可用，以及组织级商业额度；
- `project` 决定实际可访问的数据资源和主要运行时保护边界；
- `token/account` 用于身份、撤销、审计和 token 级公平性保护；
- `endpoint/resource` 决定高成本查询的独立并发或成本限制；
- organization aggregate 防止客户通过创建多个 project 或 token 叠加出超出套餐的总容量。

这比“完全按组织”或“完全按项目”更符合 Amplitude 的证据：Amplitude 既有 project 级 API，也有 organization 级 API，还有 endpoint/query cost 和 user/device 级限制。

### 5.2 OpenAPI 与 MCP 是否应该共用商业能力

工作假设：Wave 可以把 OpenAPI 与产品数据 MCP 归入同一个 `machine_api` 或“自动化 API 能力”套餐 entitlement，理由是客户购买的是“程序化访问和自动化能力”，而不是某一种传输协议。

但运行时限制不必完全相同：

- OpenAPI 的 endpoint 可以按请求数、cost 或并发计量；
- MCP 的每次 tool call 应使用相同的 resource permission，并映射到底层操作成本；
- MCP 不应成为绕过 OpenAPI 总额度或 project 权限的旁路；
- 读写能力应分离，写操作和高成本查询应有更严格的保护。

这是 Wave 的产品方向建议，待确认的关键事实是 MCP tool call 是否和 OpenAPI 请求共享同一份底层资源预算。

### 5.3 不建议第一版把“QPS”当成唯一商品

Amplitude 的公开设计至少展示了三类可售卖/可运营的价值：

1. **能力价值**：是否允许机器访问、自动化和 MCP；
2. **容量价值**：月度事件量、MTU、查询/调用预算；
3. **可靠性价值**：并发、响应时间、专属 add-on 或企业化容量。

对 Wave，更值得验证的产品包装是“组织 API 能力 + 包含容量 + 超额/升级路径”，而不是只销售一个抽象 QPS。QPS 可以作为实现和套餐中的一个参数，但不应直接代表全部商业价值。

## 六、待确认问题与证据缺口

以下问题不能从当前公开官方资料安全推出结论：

| 问题 | 为什么重要 | 建议验证方式 |
| --- | --- | --- |
| MCP 每个组织、用户、service account、project 或 tool 是否有数值 rate/concurrency limit？ | 决定 Wave 是否需要单独的 MCP limiter | 用测试组织进行受控压测，记录 429、响应头和错误体；必要时向 Support 询问 |
| MCP tool call 是否消耗 Dashboard REST 的 query cost/concurrency budget？ | 决定 OpenAPI/MCP 是否能共用一个成本模型 | 对同一 project 发起等价 REST 与 MCP 查询，观察 quota/cost/429 行为 |
| Data API Token 可访问的具体 endpoint 集合和各 endpoint 限额是什么？ | Data API Token 是 account 全局权限，存在跨 project 放大风险 | 建立最小权限测试账号，逐 endpoint 验证 401/403/429 |
| MCP 的 Growth/Enterprise dedicated add-on 包含什么容量、SLA 或高级工具？ | 决定是否能把 MCP 作为可售卖 API 能力 | 获取销售/合同条款；公开定价页只说明存在 add-on，不给出完整细节 |
| Amplitude REST API 的限额是否会按合同提高？ | 避免把公开默认值误当作企业硬上限 | 以具体账号合同和 Support 回复为准 |

## 七、研究验证与边界场景

本文件不要求实现 Wave 限流器；如将研究推进为产品决策，至少需要完成以下验证：

- **权限验证**：使用不同角色、project、service account 和 Data API Token，确认 organization/project/resource 的访问矩阵；MCP 需要分别验证组织关闭、`USE_MCP_READ`、`USE_MCP_WRITE` 和跨 project tool call。
- **限额验证**：对低成本 REST、长查询、MCP 等价 tool call 分别施加并发、速率和窗口压力；记录 status、错误码、响应头、错误体、重试建议和恢复时间。
- **边界验证**：多个 token、多个 project、撤销 token、删除角色、每日窗口重置、组织月度容量告警、MCP 组织关闭后已有连接的行为。
- **故障验证**：不把 `429`、`401`、`403`、`5xx` 和“未购买能力”混为一类；研究 Wave 时分别定义 entitlement denied、quota exceeded、dependency unavailable 的产品反馈。
- **计费验证**：不使用真实客户密钥做压测；在可控测试组织中确认哪些行为只触发 operational limit，哪些行为会进入计费/overage 统计。

建议未来的 Wave 研究模型至少能表达以下字段，而不是只保存 `qps`：

| 字段 | 含义 |
| --- | --- |
| `organization_id` / `plan_id` | 商业归属和套餐能力 |
| `project_id` | 资源与权限边界 |
| `credential_id` / `token_id` | 凭证身份、撤销和公平性维度 |
| `channel` | `openapi`、`mcp`、`session` 等入口 |
| `endpoint_or_tool` | 具体操作及其资源成本 |
| `rate_limit` / `concurrency_limit` | 瞬时保护参数 |
| `window_budget` / `cost_unit` | 每日、每小时或加权查询预算 |

这些是需求讨论所需的概念字段，不是本期实现承诺。

## 八、结论摘要

1. **Amplitude 的限制是混合模型**：project、organization、endpoint、query cost、concurrency、user/device 都可能参与，不能归结为组织或项目二选一。
2. **Amplitude 的 Data API Token 最接近 account token**：它继承 account 全局权限；这说明 token 是访问身份，不天然是独立商业容量。
3. **MCP 的公开设计重点是权限和治理**：OAuth、组织级启停、project 级读写 RBAC、每次 tool call 的 project enforcement；官方页面没有公开统一 MCP 数值 QPS，不能据此宣称无限制。
4. **公开计费重点是套餐与组织用量**：事件量、MTU、套餐能力、overage 和 add-on；没有发现公开的 MCP 单次调用价目表。
5. **对 Wave 的当前工作方向**：组织负责购买能力和聚合额度，project 负责资源隔离，token 负责身份/审计/公平性，endpoint/tool 负责成本与并发；OpenAPI 与 MCP 可共用商业 entitlement，但不能绕过同一资源的保护预算。

## 参考资料（均为 Amplitude 官方）

1. [Keys and Tokens](https://amplitude.com/docs/apis/keys-and-tokens)
2. [Manage your API keys and secret keys](https://amplitude.com/docs/admin/account-management/manage-your-api-keys-and-secret-keys)
3. [Experiment Management API](https://www.amplitude.com/docs/apis/experiment/experiment-management-api)
4. [Dashboard REST API](https://amplitude.com/docs/apis/analytics/dashboard-rest)
5. [User Profile API](https://amplitude.com/docs/apis/analytics/user-profile)
6. [HTTP V2 API](https://amplitude.com/docs/apis/analytics/http-v2)
7. [Amplitude MCP](https://amplitude.com/docs/amplitude-ai/amplitude-mcp)
8. [Docs MCP Server](https://amplitude.com/docs/amplitude-ai/docs-mcp-server)
9. [Amplitude Pricing](https://www.amplitude.com/pricing)
10. [Billing and plans](https://amplitude.com/docs/faq/billing-and-plans)
11. [Limits and quotas](https://amplitude.com/docs/faq/limits-and-quotas)
