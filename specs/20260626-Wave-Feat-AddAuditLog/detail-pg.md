# 详细设计 A：Wave 审计日志（PostgreSQL 方案）

**日期**: 2026-07-06  
**结论**: 当前推荐  
**关联文档**: [spec.md](./spec.md)、[decisions.md](./decisions.md)、[plan-pg.md](./plan-pg.md)

---

## 一、设计目标

PG 方案的目标是交付一套可被第三方审计使用的审计日志底座，满足以下要求：

1. 统一回答 who / did what / when / where
2. 对业务主流程异步，不能因为审计拖慢正常操作
3. 失败可见、可回放，不允许静默丢失
4. 可按组织 / 项目 / 操作人 / 时间范围查询并导出
5. 不泄漏邮箱、密码、token 等敏感信息

### 1.1 非目标

V1 不做：

- 通用字段级 diff 引擎
- 前端审计页面
- 全量历史迁移
- 事务内强一致写审计

---

## 二、Wave 适配依据

设计必须服从 Wave 的现实代码，不另造体系。

| 结论 | Wave 证据 | 设计含义 |
| --- | --- | --- |
| `source` 可直接派生 | `pkg/lib/pvctx/pvctx.go:80-91` | `pvctx.IsAccountAPIToken(ctx)` 为 `true` 则 `api_token`，否则 `ui`；不新增 `audit_source` |
| 异步上下文已有复制机制 | `pkg/lib/pvctx/pvctx.go:11-37` | 只补 `client_ip`，并在 `BackGroundCtx` 中继续透传 |
| 登录 / 登出在 controller，不在 auth filter | `apps/web/controller/account/account.go:70-114`、`pkg/ginx/middleware/session.go:19-127` | `logged_in / logged_out / login_failed` 在 `controller/account` 写审计 |
| 项目 / 组织 / 账号上下文已有注入 | `pkg/ginx/middleware/project.go:130-145`、`organization.go:21-54`、`account_api_token.go:40-44` | 审计写入可复用现有 `pid / org_id / aid / token` |
| MCP 已注入账号与 token 类型 | `apps/web/mcp/server.go:221-281` | MCP 只需补 `client_ip`，无需单独设计 `source` 字段 |
| 服务启动 / 停机顺序清晰 | `apps/web/server.go:175-229`、`304-312`、`391-429` | 审计 writer 可接在 `initService()`，退出时先停流量再 drain |
| 现有异步 writer 会 drop | `pkg/qm/async_batch_writer.go:87-110`、`113-136` | 审计不能直接复用它 |
| 读侧批量补当前账号名已存在成熟模式 | `apps/web/service/account/account.go:390-408`、`apps/web/op/service/audit.go:128-158` | detail 存当时名快照；读侧可额外 JOIN 补当前名作为辅助 |

结论：**PG 方案不是抽象设计，是真正贴着 Wave 现有边界画出来的。**

---

## 三、数据模型

### 3.1 PG DDL

```sql
CREATE TABLE IF NOT EXISTS global.audit_log (
    id          BIGSERIAL PRIMARY KEY,
    event_id    VARCHAR(36) NOT NULL UNIQUE,
    org_id      BIGINT,
    project_id  BIGINT,
    account_id  BIGINT,
    domain       VARCHAR(64) NOT NULL,
    feature      VARCHAR(64) NOT NULL,
    target_id    VARCHAR(64),
    action       VARCHAR(64) NOT NULL,
    source       VARCHAR(16) NOT NULL DEFAULT 'ui',
    detail       TEXT,
    ip_address   VARCHAR(64) NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `event_id` | 稳定事件 ID，用于回放幂等 |
| `org_id` | 账号层事件可为空 |
| `project_id` | 组织层 / 账号层事件可为空 |
| `account_id` | `login_failed` 等无法确认账号时可为空 |
| `domain + feature` | 审计对象分类；`domain` VARCHAR(64) |
| `target_id` | 被操作对象 ID，登录类事件可为空；VARCHAR(64) 装 BIGSERIAL / UUID 均足够 |
| `source` | 只允许 `ui / api_token / mcp` |
| `detail` | 版本化 JSON envelope，非 JSONB |
| `ip_address` | 审计刚需，必须保留 |
| `occurred_at` | 事件发生时间，由调用方在 `LogInput` 中传入；表示操作真实发生的时间 |
| `created_at` | 入库时间，由数据库 `NOW()` 赋值；异步场景下可能晚于 `occurred_at` |

### 3.2 为什么 `detail` 用 `TEXT`

V1 选 `TEXT`，不选 `JSONB`，原因很直接：

1. 当前主目标是导出和审计解释，不做库内 JSON 条件检索
2. 和 Doris `STRING` 容易保持统一模型
3. 避免过早引入 JSONB + GIN 的写放大

### 3.3 `detail` 结构

```json
{
  "schema_version": 1,
  "account": {"id": 123, "name": "张三"},
  "target": {
    "id": "34",
    "name": "增长看板",
    "type": "dashboard",
    "visibility": "project"
  },
  "comment": "dashboard charts updated",
  "extra": {
    "chart_ids": [1, 2, 3]
  }
}
```

字段约束：

- `schema_version`
  - 必填，当前固定为 `1`
- `account`
  - 操作时的 actor 快照
  - `id` 必填，`name` 尽量填写
  - 记录的是**当时名**（审计证据），不是当前名
- `target`
  - 过滤后的对象摘要
  - `create / update` 记录 after 摘要
  - `delete` 尽量记录删除前最小摘要；拿不到时至少保留 `id` + `name`
  - `type` 字段值为 feature 常量（如 `"chart"` / `"dashboard"`）
- `comment`
  - 可选的人类可读说明
  - 仅放调用点天然已知的信息
- `extra`
  - 可选扩展信息
  - 主要用于批量对象 ID、事件专属上下文

### 3.4 detail 扩展性规则

`detail` 的结构扩展遵循 **add-only** 原则：

- `schema_version` 只增不降，V1 固定为 `1`
- `account`、`extra` 是 `map[string]any` 类型，任意新增字段无需版本升级
- `target` 是 `map[string]any` 类型，同 `account`
- 未来如需顶层新字段（如 `changes[]`、`before_snapshot`），通过 `schema_version` 升级控制
- 解析器对不认识的新顶层字段静默跳过，保证旧版本兼容
- 不允许删除/重命名已有字段

### 3.5 脱敏规则

`detail.snapshot` 与 `detail.extra` 在序列化前统一走脱敏：

- 直接删除：`email`、`password`、`pwd`、`token`、`secret`、`access_key`、`secret_key`
- 视情况掩码：`phone`、`mobile`
- 不写入：SQL、完整请求体、cookie、header 全量快照

约束：

- 审计导出默认不含邮箱
- `detail.account` 只存 `id` 和 `name`，不存邮箱
- 不为了”更好看”额外查库拼装 detail

### 3.7 大小预算

- 单条 `detail`（JSON 序列化后）预算：64KB
- 超出 64KB 时逐级裁剪，顺序如下（每一级记录一次 warning metric）：

  | 级别 | 裁剪动作 | 裁剪后最简形态 |
  |------|----------|----------------|
  | 1 | 丢弃 extra（整个字段置 null） | 无 extra |
  | 2 | 缩短 comment 为首末各 200 字 | 首末各 200 字 |
  | 3 | 截断 target 为仅保留 id + name | 最小资源摘要 |
  | 4 | 截断 account 为仅保留 id（name 丢弃） | 最小 actor 摘要 |

- 裁剪后的 detail 正常写入，不因超限而丢弃审计行
- 裁剪在 enqueue 时完成，不阻塞主流程

### 3.8 Client IP 与反向代理

审计记录的 `ip_address` 来源于 `c.ClientIP()`，依赖 gin 的 `TrustedProxies` 配置。

需要在 `apps/web/server.go` 中补充：

```go
r.SetTrustedProxies([]string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"})
```

具体 CIDR 范围以 Wave 部署拓扑为准。如果配置不当，`c.ClientIP()` 在反向代理（ALB / Nginx）后可能返回代理 IP，导致审计 IP 地址不准确。

### 3.9 V1 索引

```sql
CREATE INDEX IF NOT EXISTS idx_audit_log_project_time
    ON global.audit_log (project_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_log_org_time
    ON global.audit_log (org_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_log_account_time
    ON global.audit_log (account_id, occurred_at DESC)
    WHERE account_id IS NOT NULL;
```

V1 不建：

- `idx_audit_log_project_target_time`
- `idx_audit_log_org_target_time`
- `occurred_at` 单列索引

原因：

- 高频路径是 `org/project + time range`
- `domain / feature / target_id` 只有放在作用域内才有意义
- 单对象追溯先靠 scoped 查询过滤，确认真实高频后再补索引

---

## 四、上下文与入口设计

### 4.1 `source` 派生规则

V1 不新增 `audit_source` 上下文字段，统一规则如下：

- `pvctx.IsAccountAPIToken(ctx) == true` -> `source = api_token`
- MCP 入口（`apps/web/mcp/server.go`）-> `source = mcp`
- 其余进入审计写入的请求 -> `source = ui`

为什么可以这么做：

- 审计只在站外管理面显式调用
- 内部任务 / scheduler / backfill 本来就不应该调用 `auditlog.Log(...)`
- 因此不需要 `internal` source 值
- MCP 走独立认证路径，在 `server.go` 中显式赋值，不依赖 `IsAccountAPIToken` 派生

### 4.2 新增的上下文字段

`pvctx` 只补一个字段：

```go
func ClientIP(ctx context.Context) string
func WithClientIP(ctx context.Context, ip string) context.Context
```

并在 `BackGroundCtx(ctx)` 中复制 `client_ip`。

### 4.3 gin / MCP 注入点

| 入口 | 处理 |
| --- | --- |
| `pkg/ginx/middleware/session.go` | 对已认证 session 请求注入 `client_ip` |
| `pkg/ginx/middleware/account_api_token.go` | 对 API Token 请求注入 `client_ip` |
| `apps/web/mcp/server.go` | 在 MCP 鉴权完成后注入 `client_ip`，并将 `source` 设为 `mcp` |

### 4.4 登录 / 登出特殊路径

登录类事件不应依赖 middleware 自动补齐账号上下文，因为登录本身发生在认证之前。

处理方式：

- `logged_in`
  - 在 `controller/account.LoginAccount` 成功返回前写入
  - 使用 `accountInfo.ID` 临时补 `pvctx.WithAid`
  - 使用 `pvctx.WithClientIP(ctx, c.ClientIP())`
- `login_failed`
  - 在 `LoginValidate` 返回错误处写入
  - `account_id = NULL`
  - `source = ui`
  - `ip_address = c.ClientIP()`
- `logged_out`
  - 在 `LogoutAccount` 成功后写入
  - 只有当前请求已成功解析出 `aid` 时才记
  - 匿名清 cookie 的场景不记审计行

---

## 五、应用层接口

### 5.1 写入接口

```go
type Detail struct {
    SchemaVersion int            `json:"schema_version"`
    Account       map[string]any `json:"account,omitempty"`  // {id, name} 当时 actor 快照
    Target        map[string]any `json:"target,omitempty"`   // {id, name, type, ...} 资源摘要
    Comment       string         `json:"comment,omitempty"`
    Extra         map[string]any `json:"extra,omitempty"`
}

type LogInput struct {
    Domain     string
    Feature    string
    Action     string
    TargetID   string
    Detail     *Detail
    OccurredAt time.Time  // 必填，事件实际发生时间
}

func Log(ctx context.Context, input LogInput) error
```

设计要点：

- `Log` 从 `ctx` 读取 `org_id / project_id / account_id / source / client_ip`
- `OccurredAt` 必填，调用方传入事件实际发生时间
- 不让调用方直接传 email、token 等敏感字段到 detail；account snapshot 由调用方在构造 Detail 时传入（只含 id + name）

### 5.2 查询接口

```go
type Query struct {
    OrgID     *int64
    ProjectID *int64
    AccountID *int64

    Domain    string
    Feature   string
    Action    string
    TargetID  string

    StartTime *time.Time
    EndTime   *time.Time
    Cursor    string
    Limit     int
}

func List(ctx context.Context, query Query) ([]Item, string, bool, error)
func Export(ctx context.Context, query Query, format string, w io.Writer) error
```

查询约束：

- `OrgID / ProjectID / AccountID` 至少要有一个
- `domain / feature / target_id` 只允许在 scope 内过滤
- 默认按 `occurred_at DESC, id DESC`
- cursor 使用 `(occurred_at, id)`

这解决了“`domain, feature, target_id` 没有作用域就没有意义”的问题。

---

## 六、写入详细设计

### 6.1 注册表校验

`auditlog/registry.go` 维护允许的：

- `domain`
- `feature`
- `action`

未注册组合直接报错并记 metric，不写审计行。

### 6.2 Detail 构造规则

| 场景 | 规则 |
| --- | --- |
| `created` | 记录当前对象 `target` 最小摘要；附带 `account` snapshot（`id` + `name`） |
| `updated` | 记录 after `target` 摘要，不额外查 before；附带 `account` snapshot |
| `deleted` | 记录删除前最小 `target` 摘要；拿不到时只保留 `id` + `name`；附带 `account` snapshot |
| 批量操作 | 受影响对象列表放 `detail.extra` |

调用约束：

- 只使用当前调用点已拿到的数据
- 不为生成 `detail` 再查一次库
- `account` snapshot 在 `audit.Log()` 调用时传入（使用 `pvctx` 中的当前账号信息）
- `comment` 只填天然已知的业务说明

### 6.3 队列模型

配置项：

| 配置 | 默认值 |
| --- | --- |
| `audit_log_batch_size` | `100` |
| `audit_log_flush_interval` | `1s` |
| `audit_log_queue_size` | `1000` |
| `audit_log_replay_interval` | `30s` |
| `audit_log_spool_dir` | `${log_dir}/audit-spool` |
| `audit_log_spool_max_bytes` | `1GiB` |

运行模型：

1. `Log(...)` 尝试写入内存队列
2. 队列满时，直接把单条事件 append 到 spool
3. 后台 `flushLoop` 定时批量写 PG
4. 批量失败时，把整个失败批次写回 spool
5. `replayLoop` 周期性回放 spool

### 6.4 为什么不能复用 `qm.AsyncBatchWriter`

`qm.AsyncBatchWriter` 的策略是：

- channel 满了直接 `default:` 丢弃

这对查询日志可以接受，对审计不可以。

因此本设计只复用它的生命周期思路，不复用其 drop 语义。

### 6.5 批量写入 SQL

```sql
INSERT INTO global.audit_log (
    event_id, org_id, project_id, account_id,
    domain, feature, target_id, action,
    source, detail, ip_address, occurred_at
) VALUES
    (...), (...), (...)
ON CONFLICT (event_id) DO NOTHING;
```

原因：

- replay 天然幂等
- graceful shutdown 重试天然幂等
- 不需要额外查重

### 6.6 spool 文件格式

PG 方案使用**单事件 JSONL**：

```json
{"event_id":"01J...","org_id":12,"project_id":34,"account_id":56,"domain":"asset","feature":"dashboard","target_id":"34","action":"updated","source":"ui","ip_address":"10.0.0.1","occurred_at":"2026-07-06T10:00:00Z","detail":{"schema_version":1,"snapshot":{"id":"34","name":"增长看板"}}}
```

原因：

- 便于人工检查
- 便于部分回放
- PG 幂等依赖 `event_id`，不需要保留批次 label

目录建议：

- `${audit_log_spool_dir}/pg/`

超限策略：

- 超过 `audit_log_spool_max_bytes` 后拒绝继续写 spool
- 记 `audit_spool_overflow_total`
- 触发高优告警

### 6.7 关闭流程

接入 `apps/web/server.go` 的推荐顺序：

1. `initService()` 中初始化 audit writer
2. 退出时先 `server.Shutdown()` 停流量
3. 再执行 `auditlog.Shutdown(ctx)` drain 队列
4. 最后再关闭 `globaldb / metadb / dorisx`

如果 drain 时 PG 不可达：

- 仍尝试把内存剩余事件刷回 spool

---

## 七、读取与导出设计

### 7.1 查询路径

查询顺序：

1. 先按 `OrgID / ProjectID / AccountID` 形成 scoped where
2. 再追加 `domain / feature / action / target_id`
3. 再按 `occurred_at / id` cursor 翻页

### 7.2 账号名补齐

detail 中已记录操作时的 account snapshot（`id` + `name`），这是审计证据（当时名）。

PG 读侧额外支持通过 JOIN `global.account` 补当前名作为辅助，但导出默认以 detail.account.name 为准：

1. 收集结果页里的 `account_id`
2. 去重
3. 调用 `account.GetAccountService().GetAccountNamesMapByIds(ctx, ids)`
4. 拼出当前 `account_name` 作为额外列

约束：

- 当前名仅作为辅助信息，不替代 detail.account.name
- 默认不补邮箱
- 导出 CSV/Excel 时输出两列：`account_name`（detail 历史名）、`current_account_name`（当前名，可选）

### 7.3 导出列

默认导出列：

- `occurred_at`
- `event_id`
- `org_id`
- `project_id`
- `account_id`
- `account_name`（从 detail.account.name 提取；PG 额外提供 `current_account_name` 列为辅助）
- `domain`
- `feature`
- `target_id`
- `action`
- `source`
- `ip_address`
- `detail`

明确不导出：

- 邮箱
- token
- 敏感请求体

---

## 八、监控与告警

建议新增指标：

- `audit_enqueue_total{result=queued|spooled|failed}`
- `audit_queue_depth`
- `audit_flush_total`
- `audit_flush_failed_total`
- `audit_flush_duration_seconds`
- `audit_replay_total`
- `audit_replay_failed_total`
- `audit_spool_bytes`
- `audit_spool_overflow_total`
- `audit_oldest_pending_seconds`

告警建议：

- `audit_queue_depth / audit_log_queue_size > 0.8` 持续 5 分钟
- `audit_flush_failed_total` 持续增长
- `audit_spool_bytes > 0` 持续 10 分钟
- `audit_spool_bytes > 0.8 * audit_log_spool_max_bytes`
- `audit_oldest_pending_seconds > 60`

指标实现可直接沿用 `apps/web/metrics/metrics.go` 的 `metricsx.NewFactory(...)` 模式。

---

## 九、测试策略

### 9.1 单元测试

- `detail.go`
  - 脱敏
  - 超长裁剪
  - 空 `snapshot`
- `registry.go`
  - 合法 / 非法枚举校验
- `writer_pg.go`
  - 队列满降级到 spool
  - flush 失败写回 spool
  - shutdown drain
- `spool.go`
  - append / replay / 超限
- `service.go`
  - cursor 编解码
  - scoped 查询校验
  - 批量补名防 N+1

### 9.2 集成测试

- 登录成功写 `logged_in`
- 登录失败写 `login_failed`
- 登出成功写 `logged_out`
- 项目创建 / 更新 / 删除写审计
- Dashboard 更新 / 删图表写审计
- PG 故障时进入 spool，恢复后 replay 成功

### 9.3 边界测试

- `detail > 64KB`
- `account_id = NULL`
- 队列打满
- spool 目录不可写
- 相同 `event_id` replay 不重复落库

---

## 十、自查结论

### 10.1 数据模型 / API

- [x] 已明确 DDL、字段、索引、接口、导出列

### 10.2 错误处理

- [x] 已覆盖非法枚举、缺 IP、队列满、PG 故障、spool 溢出、shutdown drain

### 10.3 边界 case

- [x] 已覆盖超大 detail、未知账号、崩溃窗口、作用域过滤

### 10.4 测试策略

- [x] 已覆盖单元、集成、边界测试

### 10.5 方案判断

- 适配 Wave：是
- 当前可落地：是
- 当前是否优于 Doris：是

核心原因只有一句：**它已经足够满足第三方审计导出，又没有把 Wave 拖进一轮额外的存储复杂度。**
