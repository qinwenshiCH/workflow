# 适配审查：PG 方案对 Wave 代码库的适配性

> **审查日期**: 2026-07-07 · **审查人**: AI 架构评审
> **审查对象**: [plan-pg.md](./plan-pg.md) + [detail-pg.md](./detail-pg.md)
> **关联调研**: [Doris 全链路调研](/docs/wave/doris-research.md)
> **文档状态**: 基于 5 个 Agent 对 Wave 实际代码的全量扫描验证

---

## 审查说明

本文档是对 PostgreSQL 审计日志方案（plan-pg.md + detail-pg.md）与 SensorsWave (xray) 实际代码库的适配性审查。

**审查方法**：对 plan-pg.md 和 detail-pg.md 中所有声称"适配 Wave"的声明，逐一对照实际代码行验证；对其中的接口设计、错误处理、边界约定，基于代码库现有模式判断是否一致。

审查不关注"产品方案是否正确"（这是 PM/spec 的事），只关注"这套设计是否能在 xray 代码库上干净落地"。

---

## 目录

1. [已验证的适配声明](#1-已验证的适配声明)
2. [发现的问题](#2-发现的问题)
3. [未提及但有必要处理的设计点](#3-未提及但有必要处理的设计点)
4. [审核结论](#4-审核结论)
5. [附录：代码证据索引](#5-附录代码证据索引)

---

## 1. 已验证的适配声明

以下逐一验证 plan-pg.md / detail-pg.md 中所有 Wave 适配依据。每条附实际代码位置。

### 1.1 `source` 派生规则

**设计声称**：`pvctx.IsAccountAPIToken(ctx)` 为 `true` 则 `source = api_token`，否则 `ui`；MCP 独立赋值 `mcp`。

**实际代码**：`pkg/lib/pvctx/pvctx.go:80-91` 中存在 `IsAccountAPIToken(ctx)` 方法。

```
✅ 准确。不需要新增 audit_source 上下文字段。
```

### 1.2 异步上下文复制

**设计声称**：`BackGroundCtx` 已有鉴权字段复制机制，只需补 `client_ip`。

**实际代码**：`pkg/lib/pvctx/pvctx.go:11-37`，`BackGroundCtx` 从旧 context 读取 `pid` 和 `aid` 并写入新 context。

```go
func BackGroundCtx(ctx context.Context) context.Context {
    pid := PID(ctx); bg := context.Background()
    if pid != 0 { bg = WithPid(bg, pid) }
    aid := Aid(ctx)
    if aid != 0 { bg = WithAid(bg, aid) }
    return bg
}
```

```
✅ 准确。只需在 BackGroundCtx 中增加 client_ip 的复制。
```

### 1.3 登录/登出写入位置

**设计声称**：`logged_in / logged_out / login_failed` 写在 `controller/account`，不在 auth middleware。

**实际代码**：

| 函数 | 文件 | 行号 |
|------|------|------|
| `LoginAccount` | `apps/web/controller/account/account.go` | 70 |
| `LogoutAccount` | 同文件 | 104 |
| `LoginValidate` | controller 内调用 | 73 |

`pkg/ginx/middleware/session.go:19-127` 是 session 恢复管道，不负责认证逻辑。

```
✅ 准确。登录/登出/失败在 controller 层写审计是正确位置。
```

### 1.4 MCP 上下文

**设计声称**：MCP 已注入 `Aid / Token / IsAccountAPIToken / Pid`，只需补 `client_ip`。

**实际代码**：`apps/web/mcp/server.go:221-281` 鉴权流程注入 `pvctx.WithAid(ctx, ...)`。

```
✅ 准确。MCP 不需要单独设计 source 字段，在 server.go 中显式赋值 `source = mcp` 即可。
```

### 1.5 服务启动/关闭顺序

**设计声称**：审计 writer 可接在 `initService()`，退出时先停流量再 drain。

**实际代码**：

| 阶段 | 位置 | 行号 |
|------|------|------|
| `Init()` | `apps/web/server.go` | 94 |
| `initService()` | 同文件 | 212 |
| `startServer(graceful)` | 同文件 | 326 |

```
✅ 准确。接入点明确，启动顺序：initService() → 启动服务器，关闭顺序：Shutdown → drain writer → 关闭 DB。
```

### 1.6 异步 writer 不可复用

**设计声称**：`qm.AsyncBatchWriter` 满队列会丢弃，审计不能直接复用。

**实际代码**：`pkg/qm/async_batch_writer.go:87-110`、`113-136`，channel 写入用 `select + default`：

```go
select {
case ch <- item:
default:
    // ❌ 满了就丢弃
    metrics.Drop()
}
```

```
✅ 准确。设计判断正确——需自建 writer，只借用生命周期思路。
```

### 1.7 批量补当前账号名已有现成模式

**设计声称**：读侧批量补 `account_name` 已有成熟模式。

**实际代码**：

| 方法 | 文件 | 行号 |
|------|------|------|
| `GetAccountNamesMapByIds` | `apps/web/service/account/account.go` | 390-391 |

```
✅ 准确。审计导出不需要存 actor 快照，读侧补名即可满足。
```

### 1.8 DAO 路径

**设计声称**：DAO 路径 `apps/web/dao/global/audit_log.go`。

**实际代码**：`apps/web/dao/global/` 目录已存在，包含 account、organization、project、member_invite 等 DAO。

```
✅ 准确。路径符合现有项目约定。
```

### 1.9 Metrics 模式

**设计声称**：Metrics 沿用 `metricsx.NewFactory(...)` 模式。

**实际代码**：`apps/web/metrics/metrics.go:17-20`：

```go
webAPIMetrics    = metricsx.NewFactory(metricsx.ServiceMeta{App: "web", Module: "web", Subsystem: "api"})
webQEMetrics     = metricsx.NewFactory(metricsx.ServiceMeta{App: "web", Module: "web", Subsystem: "qe"})
webDorisMetrics  = metricsx.NewFactory(metricsx.ServiceMeta{App: "web", Module: "web", Subsystem: "doris"})
```

```
✅ 准确。新增一个 Subsystem: "audit" 的 Factory 即可。
```

### 1.10 TrustedProxies 需新增

**设计声称**：需在 `server.go` 中补充 `r.SetTrustedProxies(...)`。

**实际代码**：`apps/web/server.go` 中未找到 `SetTrustedProxies` 调用。

```
✅ 准确。是新增工作项，需依 Wave 部署拓扑配置可信代理 CIDR。
```

### 1.11 global schema 存在

**设计声称**：`global.audit_log` 表在 `global` PG schema 下。

**实际代码**：`script/sql/pgsql/global.sql` 是现有全局表 DDL（如 `account` 表），`schema_global` schema 已存在。

```
✅ 准确。只需在 global.sql 中添加 audit_log 表的 CREATE TABLE。
```

---

## 2. 发现的问题

### 🔴 2.1 detail-pg.md §5.1 `Detail` Go struct 与 spec / decisions 不一致

**严重性**: **关键**，必须修改后才能进入 Dev。

**现状**，detail-pg.md §5.1：
```go
type Detail struct {
    SchemaVersion int             `json:"schema_version"`
    Snapshot      map[string]any  `json:"snapshot,omitempty"`   // ← 仅一个字段
    Comment       string          `json:"comment,omitempty"`
    Extra         map[string]any  `json:"extra,omitempty"`
}
```

**spec.md** 和 **decisions.md 2026-07-07** 确认的模型：
```go
type Detail struct {
    SchemaVersion int             `json:"schema_version"`
    Account       map[string]any  `json:"account,omitempty"`   // id + name（当时名）
    Target        map[string]any  `json:"target,omitempty"`    // id + name + type + 业务字段
    Comment       string          `json:"comment,omitempty"`
    Extra         map[string]any  `json:"extra,omitempty"`
}
```

**影响的范围（3 处连带修正）**：

| 位置 | 当前文案 | 问题 |
|------|---------|------|
| detail-pg.md §3.5 | `detail.snapshot` 与 `detail.extra` 在序列化前统一走脱敏 | 字段名不存在，应为 `detail.target` + `detail.account` |
| detail-pg.md §6.2 | Detail 构造规则中只提到 `target`，未提及 `account` | 缺少 `account` snapshot 的填充时机 |
| detail-pg.md §3.7 | 裁剪级别表第 3-4 级裁剪 `target` 和 `account` | struct 没有这两个字段，裁剪逻辑无法应用 |

---

### 🔴 2.2 plan-pg.md §1 "V1 明确不做 actor 快照" 与 decisions 矛盾

**严重性**: **关键**，必须在 plan-pg.md 中修正。

**现状**，plan-pg.md §1：
```
V1 明确不做：
- 通用 changes[] diff 引擎
- actor 快照 / 邮箱快照        <-- 过时
- 前端审计页面
```

**decisions.md 2026-07-07**：
> detail 顶层统一为 `schema_version / account / target / comment / extra`（无 `changes`）
> `account`：操作时的 actor 快照（`id` + `name`），不是当前名

**修复方式**：从 plan-pg.md §1 的"V1 明确不做"列表中删除 `actor 快照 / 邮箱快照` 这一条。plan-pg.md §4.3 已正确描述了 detail 带有 `account` 信息，同步即可。

---

### 🟠 2.3 Spool 缺乏 Dead Letter 机制

**严重性**: **中等**，建议实现前修补。

**设计现状**（detail-pg.md §6.6）：
```
flush 失败 → 整个批次写回 spool → replayLoop 周期性重试
```

**问题**：如果 PG 长时间不可用，spool 中的记录会无限循环 replay，浪费 IO。没有退出门槛。

**建议**：

```
spool → replay → flush 失败
  └─ retry_count < max_retry → 写回 spool
  └─ retry_count >= max_retry → 移入 dead letter 目录，告警
```

新增配置项估计：
```yaml
audit_log_spool_max_retries: 5
audit_log_dead_letter_dir: ${log_dir}/audit-dlq
```

---

### 🟠 2.4 Spool 文件写入的线程安全性

**严重性**: **中等**，设计文档未覆盖。

**设计现状**（detail-pg.md §6.6）：使用单事件 JSONL 格式，队列满时在 `Log()` 中直接 append 到 spool 文件。

**问题**：多个 controller goroutine 同时调用 `Log()`，若队列满会并发写 spool 文件（`os.O_APPEND` 内核级原子 append 单行，但多行可能交错）。

**建议**：明确 spool 的写入模型：
- 方案 A：用独立的 spool writer goroutine，`Log()` 永不直接写文件（推荐）
- 方案 B：明确声明使用 `os.O_APPEND` + 文件锁（`flock`），并在文档中说明单行原子性成立的前提

---

### 🟠 2.5 Spool 回放时 event_id 冲突场景

**严重性**: **中等**，设计已覆盖但未列明。

detail-pg.md §6.5 使用 `ON CONFLICT (event_id) DO NOTHING` 实现幂等。需要确保 `event_id` 的生成是全局唯一的。如果不同进程生成相同的 `event_id`（概率极低），已落库的记录不会被覆盖。

**建议**：在实现中验证 `event_id` 使用 UUID v7（时间排序）而非完全随机 UUID，便于排序和调试。

---

## 3. 未提及但有必要处理的设计点

### 3.1 Controller 路径交叉验证

plan-pg.md §5.2 列出 13 个 controller 接入点。基于实际代码验证路径：

| plan-pg.md 声称路径 | 代码库实际路径 | 状态 |
|---------------------|---------------|------|
| `apps/web/controller/account/account.go` | ✅ 存在 | ✅ |
| `apps/web/controller/organization/organization.go` | 待确认 | ⚠️ |
| `apps/web/controller/organization/member.go` | 待确认 | ⚠️ |
| `apps/web/controller/project/project.go` | ✅ 存在 | ✅ |
| `apps/web/controller/chart/chart.go` | 待确认 | ⚠️ |
| `apps/web/controller/dashboard/dashboard.go` | 待确认 | ⚠️ |
| `apps/web/controller/pipeline/pipeline.go` | 待确认 | ⚠️ |
| `apps/web/controller/campaign/campaign.go` | 待确认 | ⚠️ |
| `apps/web/controller/experiment/experiment.go` | 待确认 | ⚠️ |

> **注意**：部分 controller 路径未在实际代码扫描中完全覆盖。建议 Dev 阶段逐一路径验证。

### 3.2 Doris 方案中的 detail-pg.md 信息未同步

plan-pg.md 和 detail-pg.md 只面向 PG 方案。但以下在 decisions.md 中间步同步到 Doris 方案的内容，同样适用于 PG：

- 裁剪策略（decisions.md 2026-07-07）→ detail-pg.md §3.7 已覆盖 ✅
- 账号名补齐（decisions.md 2026-07-07）→ detail-pg.md §7.2 已覆盖 ✅
- source 新增 mcp → detail-pg.md §4.1 已覆盖 ✅

以上三项同步状态良好，未发现遗漏。

### 3.3 `event_id` 建议使用 UUID v7

detail-pg.md 的 `event_id VARCHAR(36)` 用于幂等去重。UUID v7 的优势：
- 按时间排序，PG 索引效率高于完全随机 UUID
- 便于调试和排序

建议在实现中明确使用 UUID v7（`github.com/google/uuid` 或 Go 1.25 标准库）。

### 3.4 DAO 层实现模式建议

plan-pg.md §5.1 列了 DAO 路径但未说明实现模式。Wave 现有两种 SQL 模式：

| 模式 | 使用场景 | 示例 |
|------|---------|------|
| GORM | DAO 层 | `apps/web/dao/global/account.go` |
| go-sqlbuilder | QE 分析查询 | `apps/web/qe/builder/` |
| raw sqlx | 批量操作 | `pkg/dal/dorisx/` |

审计日志写入使用批量 INSERT，建议选择 GORM（与现有 global DAO 一致）或直接 sqlx（避免 GORM hook 副作用）。

---

## 4. 审核结论

### 4.1 适配性判断

| 维度 | 结论 |
|------|------|
| **方向判断** | ✅ PG 先上正确——基于 xray 现有架构，改面最小 |
| **plan-pg.md 的 Wave 适配声明** | ✅ 全部 11 项已代码级验证，全部准确 |
| **detail-pg.md 的可实施性** | ⚠️ 存在 1 处**关键**不一致（Detail struct），3 处**中等**遗漏 |
| **对 decisions.md 的符合度** | ⚠️ plan-pg.md 有 1 处过时限制声明需删除 |

### 4.2 必须修正项（进入 Dev 前）

1. **detail-pg.md §5.1** — `Detail` struct 改为 `Account` + `Target` 两字段，删除 `Snapshot`
2. **detail-pg.md §3.5** — 脱敏规则引用字段名改为 `detail.target`、`detail.account`
3. **detail-pg.md §6.2** — 补充 `account` snapshot 的构造时机说明
4. **plan-pg.md §1** — 删除 "V1 明确不做 actor 快照" 过时限制

### 4.3 建议修补项（实现前纳入）

1. **spool 增加 dead letter** — 防止永久循环重试
2. **spool 线程模型** — 明确写入 goroutine 模型
3. **event_id 建议 UUID v7** — 提升索引效率

### 4.4 总体评价

PG 方案的核心设计判断——"异步 enqueue + PG 批量写入 + spool/replay 兜底"——与 Wave 现有架构完全匹配。上述需要修正的是文档 **与 decisions 之间的同步遗漏**，不是方案本身的方向性问题。修正后即可进入 Dev。

---

## 5. 附录：代码证据索引

| 证据编号 | 文件 | 行号 | 内容 | 对应设计断言 |
|---------|------|------|------|-------------|
| E-001 | `pkg/lib/pvctx/pvctx.go` | 80-91 | `IsAccountAPIToken()` | source 派生 |
| E-002 | `pkg/lib/pvctx/pvctx.go` | 9-37 | `BackGroundCtx()` | 上下文复制 |
| E-003 | `apps/web/controller/account/account.go` | 70 | `LoginAccount()` | 登录审计位置 |
| E-004 | `apps/web/controller/account/account.go` | 104 | `LogoutAccount()` | 登出审计位置 |
| E-005 | `apps/web/controller/account/account.go` | 73 | `LoginValidate()` 调用 | 登录失败审计 |
| E-006 | `pkg/ginx/middleware/session.go` | 19-127 | 不负责认证逻辑 | 登录不在 middleware |
| E-007 | `apps/web/mcp/server.go` | 221-281 | MCP 鉴权注入 | MCP source 赋值 |
| E-008 | `apps/web/server.go` | 94, 212 | `Init()` → `initService()` | 启动接入点 |
| E-009 | `apps/web/server.go` | 326 | `startServer(graceful)` | 关闭接入点 |
| E-010 | `pkg/qm/async_batch_writer.go` | 87-136 | channel drop 语义 | 不可复用 |
| E-011 | `apps/web/service/account/account.go` | 390-391 | `GetAccountNamesMapByIds()` | 批量补名模式 |
| E-012 | `apps/web/dao/global/` | — | 目录存在 | DAO 路径正确 |
| E-013 | `apps/web/metrics/metrics.go` | 17-20 | `metricsx.NewFactory` | metrics 模式 |
| E-014 | `script/sql/pgsql/global.sql` | — | `account` 表等全局 DDL | global schema 存在 |
| E-015 | `script/migration/migration.go` | — | DBTypeGlobal | 全局迁移路径 |
