# Wave 审计日志 Spec 代码库审查报告

审查人：Wave 专家 Agent
审查日期：2026-07-07
审查范围：01-spec.md, 02-decisions.md, 03-plan-pg.md, 04-detail-pg.md, Wave 代码库
代码库路径：`/Users/wenshiqin/go-project/wave`

---

## 审查结论速览

| 优先级 | 发现数 | 状态 |
|--------|--------|------|
| P0（阻塞） | 0 | — |
| P1（重要） | 5 | 详见下文 |
| P2（建议） | 12 | 全部对齐 ✅ |

所有 P0 问题已在前两轮审查中关闭。当前 spec 整体质量高，与代码库对齐良好。5 个 P1 问题均为文档内部矛盾或遗漏，不影响技术方向正确性。

---

## P0 — 阻塞问题

无。

---

## P1 — 重要问题（建议 Dev 前修复）

### P1-1 [严重] detail-pg §5.2 channel 满时行为自相矛盾

**文件**：`04-detail-pg.md:316-323`

§5.2 配置表内存在自相矛盾的描述：

| 位置 | 内容 | 来源 |
|------|------|------|
| 配置表"满时行为" | `best_effort`：非阻塞 enqueue，满时**静默丢弃**，记 `audit_channel_drop_total` | detail-pg:319 |
| 配置表下方注释 | buffer 满时...**不存在静默丢弃不入 spool 的路径** | detail-pg:323 |

同一段内，"静默丢弃"和"不存在静默丢弃路径"直接矛盾。

**与其他 spec 文件的对齐检查：**

| 文件 | 立场 | 是否一致 |
|------|------|---------|
| 01-spec.md §边界与异常处理 | 批量写入上限 500 行/批；失败批次进入 spool/replay，不允许静默丢弃 | ✅ |
| 02-decisions.md:24 | "不接受内存 channel 满后静默 drop；必须有 spool/replay 兜底" | ✅ |
| 03-plan-pg.md §8 Q3 | "满了直接写本地 spool 文件" | ✅ |

**结论**：spec/decisions/plan 三方立场一致（不允许静默丢弃），detail-pg §5.2 配置表与 spec 方向矛盾。

**建议**：将 §5.2 配置表"满时行为"改为 `channel 满时直接写入 spool 文件，不丢弃条目；spool 也满时记 overflow metric + 告警`。保留 `audit_channel_drop_total` 指标定义但仅在极端溢出时递增（不应作为正常降级路径）。

---

### P1-2 [中] decisions.md 引用不存在的 §5.7

**文件**：`02-decisions.md:319`

```
调用方在调用 audit.Log() 前确保 pvctx 中已注入 org_id（详见 04-detail-pg.md §5.7）
```

但 detail-pg.md 只有 §5.1–§5.5，不存在 §5.7。

**建议**：删除此 cross-reference，或将相关说明补入 detail-pg 现有章节（如 §5.1 或新增 §5.6）。

---

### P1-3 [中] §10 接入清单缺少 org_id/project_id 约束列

**文件**：`04-detail-pg.md §10`

Log() 从 pvctx 隐式提取 org_id/project_id，但 §10 的 controller 映射表没有标注各 handler 的 ctx 中是否已有这两值。实际路径差异较大：

| 调用场景 | OrgID 在 ctx? | 需额外操作？ |
|----------|--------------|-------------|
| 登录/登出/登录失败（§10.1） | 无（白名单路由） | 不需要 — 账号层事件记为 NULL |
| 组织层（§10.2） | 仅 API Token 有；session 无 | 调用前 `pvctx.WithOrgID(ctx, oid)` |
| 项目层（§10.3–10.5） | OrganizationFilter 仅对 API Token 注入 | 调用前先从 project_id 反查 org_id |
| MCP（未在 §10 覆盖） | authorizeProjectContext 已注入 | 不需要 |

**建议**：在 §10 每张映射表加"OrgID 在 ctx?"和"ProjectID 在 ctx?"两列，或统一加一段说明指导开发人员在对应场景下如何注入。

---

### P1-4 [低] Batch size 默认值不一致

| 位置 | 值 |
|------|-----|
| 01-spec.md §边界与异常处理 | 500 行/批（上限） |
| 02-decisions.md:182 | 批量写入上限 500 行/批 |
| 04-detail-pg.md §4.3 配置 | `batch_size = 100`（默认值） |
| 04-detail-pg.md §5.2 时序图 | "收集 ≤100 条" |
| 03-plan-pg.md §5.2 架构图 | `batch=100` |

spec/decisions 的 500 是上限，detail-pg/plan 的 100 是默认配置值——两者不直接矛盾，但 detail-pg 没有说明 500 是硬上限。如果开发人员未注意到 spec 中的上限约束，可能在生产配置中设置超过 500 的值。

**建议**：detail-pg §4.3 配置表 `batch_size` 加注"上限 500"，或在 §5.2 补充说"不超过 spec 规定的 500 行上限"。

---

### P1-5 [低] Channel buffer 不一致

| 位置 | 值 |
|------|-----|
| 03-plan-pg.md §5.1 架构图 | buffer=1000 |
| 04-detail-pg.md §4.3 配置 `queue_size` | 4096 |
| 04-detail-pg.md §5.2 配置表 | 4096 |

plan 的架构图与 detail-pg 的配置值不一致。虽然最终由配置决定，但 spec 阶段的数字应统一。

**建议**：统一为 4096（detail-pg 的值），或统一为 1000（plan 的值），确保文档间一致。

---

## P2 — 代码库假设验证清单

### P2-A: pvctx 上下文假设（全部对齐 ✅）

| # | 假设 | 验证结果 | 代码位置 |
|---|------|---------|---------|
| 1 | `IsAccountAPIToken` 不能表达 `mcp`，需新增 `audit_source` | ✅ 确认。pvctx.go 只有 IsAccountAPIToken 布尔值，无法区分 mcp | `pvctx.go:78-82` |
| 2 | `OrgID()`/`WithOrgID()` 已存在可复用 | ✅ 确认。已有标准 Getter/Setter | `pvctx.go:54-64` |
| 3 | `BackGroundCtx` 可扩展复制 client_ip/audit_source/org_id | ✅ 确认。当前复制 pid/aid/token/aname/reqid/traceid/lang，按同模式扩展即可 | `pvctx.go:9-37` |
| 4 | 新增 `ClientIP()`/`WithClientIP()` 无冲突 | ✅ 确认。当前无同名函数 | pvctx.go |
| 5 | 新增 `AuditSource()`/`WithAuditSource()` 无冲突 | ✅ 确认。当前无同名函数 | pvctx.go |

### P2-B: Service 生命周期假设（全部对齐 ✅）

| # | 假设 | 验证结果 | 代码位置 |
|---|------|---------|---------|
| 6 | Service 初始化有明确接入点 | ✅ 确认。`server.go:212` 有 `initService()` | `server.go:208-232` |
| 7 | 优雅关闭有明确接入点 | ✅ 确认。`server.go:375` 信号处理和 `server.go:402` exit() | `server.go:375-414` |
| 8 | startServer 可注入 audit writer 启动 | ✅ 确认。`server.go:326` 可注入 | `server.go:326-340` |

### P2-C: Gin 中间件假设（全部对齐 ✅）

| # | 假设 | 验证结果 | 代码位置 |
|---|------|---------|---------|
| 9 | Session middleware 白名单路由无 auth context | ✅ 确认。login/signup 完全跳过 setAccountContext | `session.go:21-32` |
| 10 | gin 路由目前无 TrustedProxies 配置 | ✅ 确认。`gin.New()` 后未调用 SetTrustedProxies | server.go |
| 11 | `source = ui` 默认注入点存在 | ✅ 确认。可在 server.go 中间件栈中追加 | server.go:486-495 |
| 12 | API Token middleware 可覆盖 source | ✅ 确认。account_api_token.go 在鉴权后可注入 source=api_token | `account_api_token.go` |
| 13 | OrganizationFilter 仅对 API Token 注入 OrgID | ✅ 确认。`if !IsAccountAPIToken { c.Next(); return }` | `organization.go:25` |

### P2-D: MCP 假设（全部对齐 ✅）

| # | 假设 | 验证结果 | 代码位置 |
|---|------|---------|---------|
| 14 | MCP 不走 gin，需单独提取 client_ip | ✅ 确认。`contextInjectionHandler` 无 client_ip 提取 | `mcp/server.go:214-238` |
| 15 | MCP 现有鉴权字段可复用 | ✅ 确认。已注入 Aid/Token/IsAccountAPIToken/Pid | `mcp/server.go:214-238` |
| 16 | MCP 可注入 source=mcp | ✅ 确认。在 contextInjectionHandler 末尾追加赋值即可 | `mcp/server.go:214-238` |
| 17 | oauth.go clientIP() 不影响审计 IP 方案 | ✅ 确认。oauth.go 的 clientIP() 用于 rate limiting（X-Forwarded-For），审计走 X-Real-IP，目的不同，各自独立 | `oauth.go:784-794` |

### P2-E: 登录/登出路径假设（全部对齐 ✅）

| # | 假设 | 验证结果 | 代码位置 |
|---|------|---------|---------|
| 18 | 登录/登出在 controller/account | ✅ 确认。LoginAccount line 70, LogoutAccount line 104 | `account.go:70-114` |
| 19 | LoginValidate 失败可埋 login_failed | ✅ 确认。LoginValidate 返回 err 后走 handleError 路径 | `account.go:73-77` |

### P2-F: 存储与迁移假设（全部对齐 ✅）

| # | 假设 | 验证结果 | 代码位置 |
|---|------|---------|---------|
| 20 | global migration 运行在事务中，不能用 CONCURRENTLY | ✅ 确认。所有 global_v*.sql 使用 CREATE TABLE IF NOT EXISTS 和 CREATE INDEX IF NOT EXISTS | `script/migration/scripts/` |
| 21 | 现有异步 writer 不可复用 qm 的实现 | ✅ 确认。`qm/async_batch_writer.go:87-110` 使用 `select + default` 静默丢弃，不满足审计要求 | `pkg/qm/async_batch_writer.go:87-110` |
| 22 | 批量补账号名模式已存在 | ✅ 确认。`GetAccountNamesMapByIds` 可复用 | `service/account/account.go:390-408` |
| 23 | event_id 可用 `util.NewUUID()`（UUID v7） | ✅ 确认。已使用 `github.com/google/uuid v1.6.0`，零新增依赖 | `pkg/lib/util/util.go`, `go.mod` |
| 24 | 审计配置放 WebConf，非 AppConf | ✅ 确认。Audit 是 web 模块专属，放在 `apps/web/config/web_cfg.go` 的 WebConf 中 | `web_cfg.go` |

---

## 二审问题跟踪

| # | 描述 | 状态 | 建议操作 |
|---|---|---|---|---|
| 1 | detail 大小预算 64KB vs 256KB 不一致 | ✅ 已解决 | 统一为 64KB 超限丢弃 |
| 2 | MCP client_ip 提取策略不一致 | ✅ 已解决 | 统一为 APISIX X-Real-IP |
| 3 | event_id 生成方式未明确 | ✅ 已解决 | 复用 util.NewUUID() |
| 4 | TrustedProxies 影响面超出审计 | ✅ 已解决 | 不引入，文档说明 IP 可能不准 |
| 5 | API 路由前缀风格不一致 | ✅ 已解决 | /api/audit/logs |
| 6 | BackGroundCtx 遗漏 OrgID | ✅ 已解决 | detail-pg §5.1 已补充 |
| 7 | decisions.md cross-reference §5.7 悬空 | ✅ 已解决 | 修正为 §4.4 引用 |
| 8 | §10 缺少 org_id 约束列 | ✅ 已解决 | 档首统一说明 + OrganizationFilter 扩大范围 |
| 9 | detail-pg §5.2 channel 满行为矛盾 | ✅ 已解决 | 统一为非阻塞丢弃 + error 日志，去 spool |
| 10 | Batch size 不一致 | ✅ 已解决 | detail-pg 配置表加注"上限 500" |
| 11 | Channel buffer 不一致 | ✅ 已解决 | 统一为 4096 |
| 12 | 运行时脱敏过度设计 | ✅ 已解决 | 改为调用方不传敏感字段 |
| 13 | 监控指标过多 | ✅ 已解决 | 只保留 audit_channel_drop_total 1 个 counter |
| 14 | 本地 spool 过度设计 | ✅ 已解决 | 去掉 spool/DLQ/replay，channel 满直接丢弃 |

---

## 关键风险提醒（Dev 阶段重点关注）

1. **OrganizationFilter 扩大范围**：需要将原先只在 API Token 路径注入 OrgID 的中间件扩展到所有站外请求，避免 handler 手动注入。

2. **login_failed 的 account_id**：白名单路由无 auth context，login_failed 时 account_id 来自认证尝试的返回值（如用户名），不是 pvctx.Aid()。detail.account.id 可能为空。

3. **BackGroundCtx 扩展影响范围**：修改 pvctx.BackGroundCtx 会影响所有使用它的异步 goroutine（不限于审计），需回归测试确保不会将审计字段意外泄漏到非审计路径。

4. **source 覆盖顺序**：gin 中间件链：默认 `source=ui` → Session（保持 ui）→ AccountAPIToken（覆盖为 api_token）。MCP 不走 gin，需独立赋值。确保中间件执行顺序正确。

---

## 小结

本次全面代码库对齐审查覆盖了所有 spec 假设（24 项 P2 验证全部对齐 ✅），5 个 P1 问题和 3 个 ponytail 简化项已全部修复并同步到各文档。所有 14 个跟踪问题均已关闭。

spec 当前状态：**可进入 Dev 阶段**。
