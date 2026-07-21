# 需求调研决策记录

> 本文件只记录已达成的范围和讨论方式。产品方案、套餐归属、限流维度和 Redis 降级策略仍处于讨论中，未确认内容不能视为最终决策。

## 范围 & 优先级

- `2026-07-20`: 本 change 定位为“需求调研与需求讨论 spec”，主要产出竞品证据、商业价值假设、Wave 现状、候选产品方向和待确认问题，不直接进入 Dev。
- `2026-07-20`: 本次研究同时覆盖 Account API Token 和 Session Token，不能只完善 Account API Token 的 QPS 限流。
- `2026-07-21`: 新增 `_research/wave-current-foundation.md` 作为 Wave 当前实现事实的单独证据底座；主 spec 只保留决策摘要，避免把代码现状、竞品事实和候选方案混写。

## 架构 & 技术选型

- `2026-07-20`: 暂不确定最终限流实现、Redis 策略或数据模型；候选方案只作为产品讨论材料，不提前锁定技术方案。
- `2026-07-21`: 参考 PostHog 的分层原则作为工作方向：organization/plan 负责商业 entitlement，project/account/token/global 分别承担资源、聚合、公平性和平台保护；这不是已确认的最终策略。

## 边界 & 异常处理

- `2026-07-20`: Redis 限流故障、Session 鉴权 Redis 故障和普通业务缓存故障需要分开讨论，不能用一个全局 fail-open/fail-close 结论替代。
- `2026-07-21`: 将“标准 OpenAPI scope 是否实际生效”和“审计是否需要精确到 token”列为 P0 需求核查项；当前代码证据显示 MCP 路径更完整，标准生成 handler 的 `requiredScope` 仍为空，审计表也没有 token 级字段。

## 数据模型

- `2026-07-20`: 暂不确定套餐 policy 的最终归属和 project 的落盘方式；spec 只记录 organization、project、account、token、IP 的候选职责。

## 其他

- `2026-07-20`: 本阶段不生成 `tasks.md`，不提交 Wave 代码变更；进入开发前必须由用户确认产品方向和范围。
