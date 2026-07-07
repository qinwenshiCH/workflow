# Tasks: Wave 审计日志 — PG 方案

> 基于 [03-plan-pg.md](./03-plan-pg.md) + [04-detail-pg.md](./04-detail-pg.md)

| 元数据 | |
|---|---|
| **总计** | 19 个 task |
| **估算** | 底座 2d + 接入 3d + 导出 2d + 测试 1d = **8 天** |

---

## Phase 1: 底座 / DDL / 配置 / 上下文

- [ ] T001 [US1] 创建 audit_log 表 DDL — migration + bootstrap
  - 文件：`script/migration/scripts/global_v20260707_audit_log.sql`
  - 文件：`script/sql/pgsql/global.sql`
  - 内容：`CREATE TABLE schema_global.audit_log` + 3 个索引 + schema_global 前缀校验

- [ ] T002 [P] [US4] pvctx 扩展审计上下文字段
  - 文件：`pkg/lib/pvctx/pvctx.go`
  - 新增：`ClientIP()` / `WithClientIP()` / `AuditSource()` / `WithAuditSource()`
  - 修改：`BackGroundCtx` 补充复制 client_ip / audit_source / org_id

- [ ] T003 [P] [US4] 新增审计配置项
  - 文件：`apps/web/config/web_cfg.go`
  - 配置：Audit.ChannelBuffer(4096), Audit.BatchSize(100), Audit.FlushInterval(1s), Audit.MaxOpenConns(4), Audit.MaxIdleConns(2), Audit.ConnMaxLifetime(30m)

- [ ] T004 [P] [US1] DAO 层 BatchInsert / List / Export
  - 文件：`apps/web/dao/global/audit_log.go`
  - 方法：`BatchInsert(ctx, []*Entry)`, `List(ctx, Query) ([]Entry, error)`, `Export(ctx, Query) (io.Reader, error)`
  - 注意：使用独立连接池 `GetAuditLogDB()`（非业务 `GetGlobalDB()`）

- [ ] T005 [P] [US4] Middleware / 入口注入 audit_source 与 client_ip
  - 文件：`pkg/ginx/middleware/session.go` — 站外 gin 保留 `source = ui`
  - 文件：`pkg/ginx/middleware/account_api_token.go` — token 命中时覆盖 `source = api_token`
  - 文件：`pkg/ginx/middleware/organization.go` — 去掉 IsAccountAPIToken 短路，扩大 org_id 注入
  - 文件：`apps/web/mcp/server.go` — source = mcp + 读取 X-Real-IP

---

## Phase 2: 核心写入服务

- [ ] T006 [US1] audit.Log 函数 + Detail + Registry 校验
  - 文件：`apps/web/service/auditlog/log.go`
  - 函数：`Log(ctx, domain, feature, action, targetID, detail *Detail)`
  - 内部逻辑：registry 校验 domain+feature → Detail 64KB 裁剪 → 构造 Entry → enqueue
  - 文件：`apps/web/service/auditlog/registry.go`
  - 文件：`apps/web/service/auditlog/detail.go`

- [ ] T007 [US1] PGWriter 异步批量写入
  - 文件：`apps/web/service/auditlog/writer_pg.go`
  - 结构：`PGWriter{ channel, batch, ticker, dao, metrics }`
  - 行为：Start() → flushLoop → batch INSERT / 1s ticker → 3 次退避重试 → drop counter
  - 独立连接池：`GetAuditLogDB()`（MaxOpen=4, MaxIdle=2）
  - channel buffer=4096，满时非阻塞 enqueue + drop counter

- [ ] T008 [US1] Server 生命周期集成
  - 文件：`apps/web/server.go`
  - Init: `initService()` 中初始化 `PGWriter.Start()`
  - Shutdown: `server.Shutdown()` → `writer.Stop(ctx, 5s)` → `globaldb.Close()`

---

## Phase 3: 接入点 — 全部 Controller

- [ ] T009 [P] [US2] Account domain 接入
  - 文件：`apps/web/controller/account/account.go`
    - LoginAccount: session.logged_in
    - LogoutAccount: session.logged_out
    - LoginAccount（失败路径）: session.login_failed
    - UpdateAccountInfo / UpdateAccountPwd: account_setting.updated
  - 文件：`apps/web/controller/account/account_api_token.go`
    - CreateAAPIToken: api_token.created
    - UpdateAAPIToken: api_token.updated
    - OperateAAPITokenStatus: api_token.updated
    - DeleteAAPIToken: api_token.deleted

- [ ] T010 [P] [US3] Organization domain 接入
  - 文件：`apps/web/controller/organization/organization.go`
    - UpdateOrgInfo: org_setting.updated
  - 文件：`apps/web/controller/organization/member.go`
    - CreateOrgMemberInvite: org_member.created + org_member_invitation.created
    - UpdateOrgMember: org_member.updated
    - DeleteOrgMember: org_member.deleted

- [ ] T011 [P] [US3] Project domain 接入
  - 文件：`apps/web/controller/project/project.go`
    - UpdateProjectInfo: project_setting.updated
  - 文件：`apps/web/controller/project/member.go`
    - AddProjectMember: project_member.created
    - UpdateProjectMember: project_member.updated
    - DeleteProjectMember: project_member.deleted

- [ ] T012 [P] [US2] Asset domain 接入（chart/dashboard/cohort/pipeline/tracking_plan）
  - 文件：`apps/web/controller/chart/chart.go`
    - AddNewChart: chart.created / UpdateChartDetail: chart.updated / DeleteChart: chart.deleted
  - 文件：`apps/web/controller/dashboard/dashboard.go`
    - CreateNewDashboard: dashboard.created / UpdateDashboardDetail: dashboard.updated / DeleteDashboard: dashboard.deleted
  - 文件：`apps/web/controller/analysis/cohort.go`
    - CreateRuleCohort: cohort.created / UpdateRuleCohort: cohort.updated / DeleteCohort: cohort.deleted
  - 文件：`apps/web/controller/pipeline/pipeline.go`
    - PipelineCreate: pipeline.created / PipelineUpdate: pipeline.updated / PipelineDelete: pipeline.deleted
  - 文件：`apps/web/trackingplan/controller/tracking_plan.go`
    - PostDcTrackingPlanSave: tracking_plan.created / .updated / PostDcTrackingPlanDelete: tracking_plan.deleted

- [ ] T013 [P] [US2] AB domain 接入
  - 文件：`apps/web/controller/ab/ab.go`
    - PostAbCreate: experiment.created / feature_gate.created / feature_config.created
    - PostAbExpUpdate: experiment.updated
    - PostAbGateUpdate: feature_gate.updated
    - PostAbConfigUpdate: feature_config.updated
    - PostAbStatusUpdate(operation_type=DELETE): experiment.deleted / feature_gate.deleted / feature_config.deleted
    - PostAbLayerCreate: layer.created / PostAbLayerDelete: layer.deleted
    - PostAbHoldoutCreate: holdout.created / PostAbHoldoutUpdate: holdout.updated / PostAbHoldoutDelete: holdout.deleted
    - PostAbTargetCreate: target.created / PostAbTargetUpdate: target.updated / PostAbTargetDelete: target.deleted

- [ ] T014 [P] [US2] Metadata domain 接入
  - 文件：`apps/web/controller/metadata/metric.go`
  - 文件：`apps/web/controller/metadata/tracked_event.go`
  - 文件：`apps/web/controller/metadata/virtual_event.go`
  - 文件：`apps/web/controller/metadata/event_property.go`
  - 文件：`apps/web/controller/metadata/user_property.go`
  - 文件：`apps/web/controller/metadata/virtual_property.go`
  - 每个文件：created/updated/deleted 各对应一个 handler

---

## Phase 4: 查询与导出

- [ ] T015 [US3] 审计日志查询接口
  - 文件：`apps/web/service/auditlog/query.go`
  - Query struct + Validate() + List() + cursor 翻页
  - 文件：`apps/web/controller/auditlog/audit.go`
  - List handler: GET /api/audit/logs

- [ ] T016 [US1] CSV/Excel 导出（OpenAPI）
  - 文件：`apps/web/controller/auditlog/export.go`
  - 格式：CSV + Excel（使用项目现有导出工具）
  - 文件：`apps/web/service/auditlog/export.go`
  - 导出时补齐 `account_name`（通过 `GetAccountNamesMapByIds`）

---

## Final Phase: 测试与监控

- [ ] T017 [ALL] 单元 + 集成测试
  - `service/auditlog/log_test.go` — registry 校验、64KB 裁剪、enqueue
  - `service/auditlog/writer_pg_test.go` — batch INSERT、重试、drop counter
  - `service/auditlog/query_test.go` — cursor 编解码、scope 校验
  - `service/auditlog/detail_test.go` — 大小预算、JSON 序列化
  - 集成测试（testcontainers PG） — channel 满丢弃、ON CONFLICT
  - 并发测试 `go test -race` — 多 goroutine 同时 Log()

- [ ] T018 [NFR-001] 压力测试
  - 验证：channel 满时非阻塞 drop 不阻塞调用方
  - 验证：优雅关闭 5s drain 完成
  - 验证：goroutine leak（`-race`）
  - 验证：高频写入下 metric 正确递增

- [ ] T019 [NFR-006] Metrics + 监控验证
  - 指标：`audit_channel_drop_total` counter
  - 指标：channel depth/cap 每 10s log
  - 告警规则：drop_total > 0 时触发告警
