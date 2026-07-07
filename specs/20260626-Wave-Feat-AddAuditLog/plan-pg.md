# 概要设计 A：Wave 审计日志（PostgreSQL 方案）

**日期**: 2026-07-06  
**结论**: 当前推荐  
**关联文档**: [spec.md](./spec.md)、[decisions.md](./decisions.md)、[detail-pg.md](./detail-pg.md)、[plan-doris.md](./plan-doris.md)

---

## 一、方案定位

这套方案的目标不是“做一套完美的通用审计平台”，而是先让 Wave 拿出一套能被第三方审计公司接受、能稳定导出、能解释控制措施的审计日志底座。

PG 方案的 V1 范围收敛为四件事：

1. 统一审计表 `global.audit_log`
2. 异步写入，不阻塞业务主流程
3. spool / replay / metrics，失败不静默吞掉
4. OpenAPI 查询与导出，支撑第三方审计取证

V1 明确不做：

- 通用 `changes[]` diff 引擎
- 前端审计页面
- 事务内强一致同步写审计
- 为 `target_id` 单独建高成本复合索引

---

## 二、为什么当前优先 PG

对 Wave 现在这次需求来说，PG 是更短、更稳、更容易解释的路径。

| 维度 | PG 方案判断 |
| --- | --- |
| 交付目标 | 当前是“应付第三方审计导出”，不是“先做超大规模冷数仓” |
| 代码贴合度 | Wave 已有 `globaldb`、现成账号查询、现成导出链路，改动面最小 |
| 读侧复杂度 | 查询与导出直接读同库，补 `account_name` 也最自然 |
| 幂等复杂度 | `event_id` 唯一约束 + `ON CONFLICT DO NOTHING` 足够 |
| 运维复杂度 | 不需要补 Doris Stream Load label 幂等这一层 |
| 审计解释性 | “业务成功后异步入 PG，失败进 spool，恢复后回放”更容易向审计方说明 |

一句话判断：**如果当前目标是先把审计证据链做出来，PG 是最好的第一方案。**

---

## 三、与 Wave 的适配结论

这次不是凭空设计，而是按 `~/go-project/wave` 真实代码落点反推出来的。

| Wave 现状 | 代码证据 | 设计结论 |
| --- | --- | --- |
| `pvctx.IsAccountAPIToken` 已存在 | `pkg/lib/pvctx/pvctx.go:80-91` | `source` 不需要新加 `audit_source` 字段，直接派生 `ui / api_token` |
| `BackGroundCtx` 已复制鉴权与链路字段 | `pkg/lib/pvctx/pvctx.go:11-37` | 只需要补一个 `client_ip` 透传位 |
| 登录 / 登出不在 auth middleware 内完成 | `apps/web/controller/account/account.go:70-114`、`pkg/ginx/middleware/session.go:19-127` | `logged_in / logged_out / login_failed` 应在 `controller/account` 写审计，不应写在认证 filter |
| 服务启动 / 关闭有明确初始化点 | `apps/web/server.go:175-229`、`304-312`、`391-399`、`402-429` | 审计 writer 可以自然接入 `initService()` 与退出 drain 顺序 |
| 现有异步 writer 满队列会丢数据 | `pkg/qm/async_batch_writer.go:87-110`、`113-136` | 可以借生命周期模式，但不能直接复用实现 |
| 读侧已有批量补当前账号名模式 | `apps/web/service/account/account.go:390-408`、`apps/web/op/service/audit.go:128-158` | detail.account 存当时名快照（审计证据）；读侧可额外 JOIN 补当前名作为辅助 |
| MCP 已注入 `Aid / Token / IsAccountAPIToken / Pid` | `apps/web/mcp/server.go:221-281` | MCP 只要补 `client_ip` 即可复用同一套审计 writer |

---

## 四、顶层架构

### 4.1 写路径

1. 请求进入 gin / MCP
2. 业务 Controller / Service 正常完成写操作
3. 业务成功后显式调用 `auditlog.Log(...)`
4. `auditlog.Log(...)` 只做校验、脱敏、enqueue
5. 后台 writer 批量写入 `global.audit_log`
6. 批量失败则落本地 spool，后台 replay 重试

核心约束：

- 审计必须异步
- 失败不能静默 drop
- 审计失败不回滚已成功的业务事务

### 4.2 读路径

1. 按 `org_id / project_id / account_id + time range` 查询
2. 在 scoped 结果集上追加 `domain / feature / action / target_id` 过滤
3. 用 `account_id` 批量补当前 `account_name`
4. 导出 CSV / Excel 给审计公司

### 4.3 数据模型

核心字段保持极简：

- 作用域：`org_id / project_id / account_id`
- 事件：`event_id / domain / feature / target_id / action / source`
- 证据：`ip_address / detail / occurred_at / created_at`

`detail` 统一版本化 envelope（与 Doris 方案对齐）：

- `schema_version`
- `account`（actor 快照：id + name）
- `target`（资源摘要：id + name + type + 业务字段）
- `comment`
- `extra`

detail.account 记录操作时的名字（审计证据），不是当前名。读取时可通过 JOIN `global.account` 额外补当前名作为辅助，但导出默认以 detail.account.name 为准。

---

## 五、核心改动范围

### 5.1 底座

- `script/sql/pgsql/global.sql`
- `pkg/config/app_cfg.go`
- `pkg/lib/pvctx/pvctx.go`
- `apps/web/server.go`
- `apps/web/metrics/metrics.go`
- `apps/web/dao/global/audit_log.go`
- `apps/web/service/auditlog/*`
- `apps/web/controller/auditlog/audit.go`

### 5.2 一期接入范围

一期一次性接入 spec 定义的全部 22 个 feature，覆盖以下 controller：

- `apps/web/controller/account/account.go` — session（登录/登出/登录失败）、account_profile、token
- `apps/web/controller/organization/organization.go` — org
- `apps/web/controller/organization/member.go` — org_member
- `apps/web/controller/organization/invite.go` — org_member_invite
- `apps/web/controller/project/project.go` — project
- `apps/web/controller/project/member.go` — project_member
- `apps/web/controller/chart/chart.go` — chart
- `apps/web/controller/dashboard/dashboard.go` — dashboard
- `apps/web/controller/analysis/cohort.go` — cohort
- `apps/web/controller/pipeline/pipeline.go` — pipeline
- `apps/web/controller/campaign/campaign.go` — campaign
- `apps/web/controller/experiment/experiment.go` — experiment / feature_gate / feature_config
- `apps/web/controller/metric/metadata.go` — metric / tracked_event / virtual_event / event_property / user_property / virtual_property

接入策略保持 ponytail：

- 不上全局 GORM callback
- 不做一套通用 hook 框架
- 直接在写入口显式打点

---

## 六、风险与边界

### 6.1 可靠性边界

这套方案是：

- `async`
- `spool + replay`
- `at-least-once`（以 `event_id` 幂等为前提）

它不是：

- 事务内强一致审计
- 进程崩溃绝对零丢失

真实含义要写清楚：

- 优雅重启场景可做到基本不丢
- `kill -9` / OOM / Pod 直接重建时，内存队列仍有有限丢失窗口
- `audit_log_spool_dir` 只有挂持久盘才可靠

### 6.2 性能边界

- 主流程只 enqueue，不等待落库
- `detail` 不允许额外查库
- `detail` 超限截断，不让单条记录无限膨胀
- V1 只保留 3 个高频索引，避免提前把写放大做重

### 6.3 数据增长边界

PG 会线性增长，但对当前审计目标仍然可接受，因为：

- 当前只记管理面 CUD + 登录类事件，不记读流量
- V1 没有大 JSON diff
- 可先靠保留期、归档和后续分区控制规模

如果未来真实压力来自“长期保留成本”，更合理的升级路径是：

**PG 先落地 -> 再评估 PG 分区 / 归档 -> 再考虑 Doris 镜像或迁移**

---

## 七、为什么它比 Doris 更适合当前需求

和 Doris 相比，PG 方案少掉了三层额外复杂度：

1. 不需要给 `StreamLoader` 补 stable label 幂等能力
2. 不需要在读侧跨 Doris + PG 双存储来解释数据一致性
3. 不需要把“Label Already Exists 是否算成功”讲给审计方听

当前这次交付的关键不是把长期单 GB 成本做到最低，而是：

- 审计底座先成立
- 文档容易评审
- 工程能快速上线
- 失败路径讲得清楚

---

## 八、落地顺序

1. 先补底座：DDL、配置、`pvctx.client_ip`、writer、spool、metrics
2. 一期接入全部 5 domain × 22 feature 的管理面操作（详见 §5.2）
3. 然后补查询 / 导出接口
4. 最后根据真实量级再评估分区、归档和 Doris

---

## 九、自查结论

### 9.1 是否适配 Wave

适配。关键原因是：

- 现有上下文字段、服务初始化点、账号补名能力都已具备
- 只需补最少的新能力：`client_ip`、audit writer、audit DAO
- 接入点明确，且都在现有 controller / service 成功路径上

### 9.2 是否可落地实施

可落地。没有依赖一个“大而全的新基础设施”，也没有要求重构 Wave 的权限、事务或路由体系。

### 9.3 是否是当前最优方案

是。对“第三方审计导出”这个目标，PG 方案是当前最省改动、最容易解释、最容易上线的方案。

详细字段定义、异常路径、接口、测试策略见 [detail-pg.md](./detail-pg.md)。
