# API 访问能力与限流产品调研资料索引

本目录对应的 spec 是需求调研型 spec，不是实现方案。先读主 spec，再按需要查看以下资料。

## 研究记录（推荐阅读）

> 重构版文档，信息不变但按”现状→设计逻辑→商业策略→启示”重新组织，更利于阅读。

- [Amplitude API 限流、凭证设计与商业策略调研](./amplitude-research.md)：凭证作用域、OpenAPI/REST 限额、MCP 权限治理、商业策略及对 Wave 的产品推论。
- [PostHog API 限流、凭证设计与商业策略调研](./posthog-research.md)：基于 PostHog 源码拆解 organization/project/token 分层、API/MCP 限流、查询并发、quota 计费和 Redis 故障策略。
- [Wave 当前基础与背景调研](./wave-current-foundation.md)：资源 CRUD、Account API Token、Session Token、MCP、权限、审计和现有限流实现的代码证据。

## 历史版本

旧版调研文档已移入同级 `_dropped/`，不作为当前结论来源；阅读时以本目录中的三个重构版文档为准。

## 相关外部资料

- [API QPS 竞品与行业调研](../../_research/20260717-api-qps-competitive-analysis.md)：Amplitude、PostHog、AWS、Envoy、Wave 当前 Account API Token 限流基础和初步分层模型。

## 推荐阅读顺序

1. [01-spec.md](../01-spec.md)：产品问题、用户故事、竞品事实、候选方向和待确认问题。
2. [Wave 当前基础与背景](./wave-current-foundation.md)：先确认 Wave 已实现什么、哪些只是模型或注释。
3. [Amplitude 调研](./amplitude-research.md) 或 [PostHog 调研](./posthog-research.md)：再看竞品的商业与运行时分层。
4. [API QPS 竞品与行业调研](../../_research/20260717-api-qps-competitive-analysis.md)：上一轮竞品与行业资料汇总。

## 本阶段研究输出

- 商业 API 与 Web 安全流量的边界；
- organization 套餐、project 运行时边界、token/account 公平性之间的关系；
- Session Token 的攻击面和预认证保护需求；
- Redis 限流故障的 fail-open、fail-close 与本地 fallback 取舍；
- 下一轮需要产品确认的关键问题。

研究结论在用户确认前都属于工作假设，不直接转化为实现任务。
