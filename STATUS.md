# 项目状态

## 活跃

- `20260716-Wave-Refac-OrganizationProjectLifecycle`：`01-spec.md`、`02-decisions.md`、`03-plan.md`、`04-detail.md` 已完成并手工 simplify；详细设计已落实组织准入、PM/Dispatch/Scheduler、MA 双 Redis、Wagent/Kafka/OSS 清理、OP 审计和前端范围，等待 detail 评审，尚未进入开发

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
