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
| **阶段** | Spec 完成 ✅ — 双方案已通过 Wave 专家评审 + 代码库真实性验证 |
| **待办** | 用户确认可进入 Dev 阶段 |
| **文档状态** | PG 方案（04-detail-pg.md）和 Doris 方案（04-detail-doris.md）均已完整可落地，对齐 Wave 代码库实际代码 |
| **决策摘要** | PG 方案优先；Doris 方案备选；非阻塞丢弃去 spool；不传敏感字段去脱敏；1 个 drop counter 指标；buffer 4096；batch 上限 500；OrganizationFilter 扩大范围注入 org_id |
| **Doris 关键补完** | DDL 对齐 Wave 规范、StreamLoader 5 项差距、Phase 0 验证、snapshot→account+target 清理、规模对比分析 |
