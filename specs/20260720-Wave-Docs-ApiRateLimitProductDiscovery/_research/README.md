# API 访问能力与限流产品调研资料索引

本目录对应的 spec 是需求调研型 spec，不是实现方案。先读主 spec，再按需要查看以下资料。

## 现有研究记录

- [Amplitude Account API、OpenAPI/MCP 限制与计费策略调研](./amplitude-account-api-openapi-mcp-billing.md)：Amplitude 凭证作用域、REST/OpenAPI 限额、MCP 权限治理、公开计费策略及对 Wave 的产品推论。
- [PostHog API / MCP 限流、商业化与实现方案调研](./posthog-api-mcp-commercial-research.md)：基于 PostHog 源码拆解 organization/project/token 分层、API/MCP 限流、查询并发、quota 计费和 Redis 故障策略。
- [API QPS 竞品与行业调研](../../_research/20260717-api-qps-competitive-analysis.md)：Amplitude、PostHog、AWS、Envoy、Wave 当前 Account API Token 限流基础和初步分层模型。

## 推荐阅读顺序

1. [01-spec.md](../01-spec.md)：产品问题、用户故事、竞品事实、候选方向和待确认问题。
2. [amplitude-account-api-openapi-mcp-billing.md](./amplitude-account-api-openapi-mcp-billing.md)：本轮 Amplitude 专项证据调研。
3. [posthog-api-mcp-commercial-research.md](./posthog-api-mcp-commercial-research.md)：本轮 PostHog 源码专项调研。
4. [20260717-api-qps-competitive-analysis.md](../../_research/20260717-api-qps-competitive-analysis.md)：上一轮竞品与行业资料汇总。
5. Wave 本地代码：Account API Token、Session、MCP 中间件，链接已写在主 spec 的“Wave 当前基础”章节。

## 本阶段研究输出

- 商业 API 与 Web 安全流量的边界；
- organization 套餐、project 运行时边界、token/account 公平性之间的关系；
- Session Token 的攻击面和预认证保护需求；
- Redis 限流故障的 fail-open、fail-close 与本地 fallback 取舍；
- 下一轮需要产品确认的关键问题。

研究结论在用户确认前都属于工作假设，不直接转化为实现任务。
