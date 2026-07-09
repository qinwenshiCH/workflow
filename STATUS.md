# 项目状态

## 活跃

（空）

## 待启动

（空）

## 已归档

（空）

---

## 当前 Change：审计日志

| 项目 | 状态 |
|---|---|
| **目录** | `specs/20260626-Wave-Feat-AddAuditLog/` |
| **阶段** | Dev Commit 1 完成 ✅ — 基础设施（pvctx/writer/DDL/query/export/lifecycle）已实现 |
| **待办** | Commit 2：71 个埋点接入点（26 feature），等待启动 |
| **文档状态** | PG 方案（04-detail-pg.md）和 Doris 方案（04-detail-doris.md）均已完整可落地 |
| **Commit 1 交付物** | pvctx 扩展（ClientIP/AuditSource/Aname）、PGWriter（channel+flush+retry）、DDL（迁移+bootstrap）、Log()+Entry+enum 类型、Query+Export+路由注册、MCP/API token Aname 补齐、配置字段 |
| **决策摘要** | PG 方案优先；Doris 方案备选；非阻塞丢弃去 spool；不传敏感字段去脱敏；1 个 drop counter 指标；buffer 4096；batch 上限 100（default）；OrganizationFilter 扩大范围注入 org_id |
