# AddAuditLog — 活动日志统一基础设施

> Wave 新增一套 `activity` 基础设施，用统一的 `item_type + action_type + detail` 模型记录关键活动。<br/>
> 项目内 item → `meta.activity_log` | Global item → `global.activity_log` | OP 操作 → 维持 `op_operation_log` | 账号活跃 → `global.account` 字段

---

## 当前状态

| 维度 | 状态 | 下一步 |
|------|------|--------|
| spec 评审 | 🔎 评审中 | 继续收敛 global 查询模型与交付边界 |
| 技术方案 | 🔎 评审中 | 按 V1 最小闭环裁剪后再锁定 |
| 代码实现 | ⏳ 未开始 | Phase 0：建表 + activity 模块 + 查询 API |
| Wave 项目 | `add_audit_record` worktree | 代码落点为 `/Users/wenshiqin/wave-worktrees/add_audit_record` |

### 总体边界

| 域 | 存储 | 典型 item | 查询主视角 |
|----|------|-----------|------------|
| Project item activity | `meta.activity_log` | Chart / Dashboard / Cohort / AB / Metric / Pipeline / Event / Property | `item_type + item_id` |
| Global item activity | `global.activity_log` | Organization / Project / Org Member / Project Member / Account API Token | `org_id / project_id / account_id` + `item_type` |
| OP operation log | `global.op_operation_log`（维持现状） | OP 人员配置客户、配额、模板 | 维持现状 |
| Account activity fields | `global.account` | last_login_at / last_logout_at / last_active_at | account 主表字段 |

---

## 阅读指南

| 角色 | 先看 | 再看 |
|------|------|------|
| **评审者**：检查方案完整性 | [spec.md](spec.md) — 需求、验收标准 | [plan.md](plan.md) — 能否满足需求 |
| **实现者**：开始编码 | [plan.md](plan.md) §2.3 — 端到端流程闭环 | [plan.md](plan.md) §12 接入 SOP |
| **新成员**：快速了解完整方案 | [plan.md](plan.md) §1-3 范围、流程与数据模型 | [plan.md](plan.md) §5 写入模型 |
| **决策者**：了解关键选择和理由 | [decisions.md](decisions.md) | [discussion.md](discussion.md) |

---

## 关键决策速览

| 决策 | 结论 | 详见 |
|------|------|------|
| 副本集字段 | `action_name` 不新增，领域语义通过 `item_type + action_type + detail` 表达 | [decisions.md](decisions.md) #枚举与数据模型 |
| 扩展 action_type | 必须注册评审，未注册字符串在入口拒绝 | [plan.md](plan.md) §3.3 |
| 关联标识 | 用 `correlation_id`（基础设施生成），不用业务维护的 `operation_group_id` | [plan.md](plan.md) §5.1 |
| detail_version | 不引入，serializer/parser 兼容由 activity 模块统一维护 | [plan.md](plan.md) §3.4 |
| 存储类型 | `detail_payload` 使用 TEXT，不使用 PG JSONB；V1 默认 readable JSON，压缩仅讨论 | [plan.md](plan.md) §15 |
| 失败策略 | `PolicyKey` 只是稳定场景名到返回行为的轻量映射，不做策略平台 | [plan.md](plan.md) §5.3 |
| Detail 构造 | 写入契约只接收标准 `Detail`；helper 可选，不强制通用 diff 框架 | [plan.md](plan.md) §6 |
| 查询 | 保留 `total`，V1 不引入 cursor-only | [plan.md](plan.md) §7 |
| `last_active_at` 降级 | Redis 故障时跳过 DB 刷新，不倒灌 | [plan.md](plan.md) §10 |

---

## 文档列表

| 文件 | 内容 | 大小 |
|------|------|------|
| [spec.md](spec.md) | 需求规格：用户故事、验收场景、功能/非功能需求 | 精简后 |
| [plan.md](plan.md) | 技术方案：架构、数据模型、写入/查询契约、场景目录、接入指南 | 合并后（原 architecture + 三个 plan） |
| [decisions.md](decisions.md) | 决策记录：只保留最终确认的关键决策 | 精炼后 |
| [discussion.md](discussion.md) | 待讨论：TEXT codec 是否压缩、global 聚合查询是否引入 target 投影 | 持续更新 |
| [_research/](./_research/) | 研究资料：PostHog 调研等 | 未变 |
