# 项目状态

## 活跃

### 20260720-Wave-Docs-ApiRateLimitProductDiscovery — API 访问能力与请求保护产品调研

| 项目 | 状态 |
|---|---|
| **阶段** | 需求调研 / 需求讨论中 |
| **定位** | 产品探索 spec，不是立即开发的实现 spec |
| **文档** | [01-spec.md](specs/20260720-Wave-Docs-ApiRateLimitProductDiscovery/01-spec.md) + [02-decisions.md](specs/20260720-Wave-Docs-ApiRateLimitProductDiscovery/02-decisions.md) |
| **当前重点** | Account API Token 商业化、Session Token 安全、organization/project 边界、Redis 故障降级 |
| **Dev 状态** | 未进入 Dev，不生成 tasks |

## 待启动

（空）

## 已归档

### 20260626-Wave-Feat-AddAuditLog — 审计日志

| 项目 | 状态 |
|---|---|
| **阶段** | 全部完成 ✅ |
| **文档** | spec + plan + decisions + PG 详细设计（04-detail-pg.md）+ Doris 候选方案（已归档 _dropped） |
| **交付物** | Commit 1（基础设施：pvctx/writer/DDL/query/export/lifecycle）+ Commit 2（71 个埋点接入点，26 feature 全量覆盖）+ 三层测试（合规静态 64/64 + DAO 集成 8 case + E2E 3 链路 + 单元测试） |
| **决策摘要** | PG 方案优先；Doris 备选；非阻塞丢弃去 spool；不传敏感字段去脱敏；1 个 drop counter；buffer 4096；batch 上限 100；OrganizationFilter 扩大注入 org_id；移除 Sync() 改用 batch_size=1 配置；string 传参无需引入跨层常量依赖 |
