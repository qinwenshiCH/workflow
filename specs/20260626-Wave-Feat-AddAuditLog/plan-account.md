# 技术方案：账号活跃字段

## 目标

在 `global.account` 表记录账号最近登录、登出、活跃时间，不进入 `meta.activity_log`。

## 数据模型

在 `global.account` 表（现有 DDL：`script/sql/pgsql/global.sql`）新增 3 个 `TIMESTAMPTZ NULL` 列：

| 字段 | 写入时机 | 写入方式 |
|------|---------|---------|
| `last_login_at` | 登录成功（密码登录 + OAuth） | controller 在 `generateToken` 后调用 `accountDao.UpdateFields` |
| `last_logout_at` | 登出成功 | controller 在 `session.Delete` 后调用 `UpdateFields` |
| `last_active_at` | 会话活跃（每个认证请求） | 中间件 Redis `SetNX` 15 分钟节流 |

3 个字段均为 NULLABLE，老账号迁移后全部为 NULL。`UpdateFields` 会连带触发 `account` 表的 `BEFORE UPDATE` 触发器刷新 `updated_at`，接受这个行为。

## 节流策略

`last_active_at` 每个认证请求都写 DB 会放大写压力。采用 Redis-backed 节流：

```
每个认证请求 → Redis SetNX("last_active_throttle:{aid}", 15min)
  ├── SetNX 成功 → accountDao.UpdateFields(last_active_at=now) → DB 写一次
  └── SetNX 失败（key 已存在）→ 跳过，零 DB 开销
```

- 15 分钟粒度对"最近活跃时间"足够，不需要批处理
- Redis 不可用：SetNX 报错 → 跳过本次刷新 + warning 日志 + 指标上报，不在故障态放大 DB 写压力

## 接入点

| 事件 | 接入位置 | 文件 |
|------|---------|------|
| 密码登录成功 | `LoginAccount` controller，`generateToken` 之后 | `apps/web/controller/account/account.go` |
| OAuth 登录成功 | `OauthCallback` controller，OAuth HandleCallback 返回后 | `apps/web/controller/account/oauth.go` |
| 登出 | `LogoutAccount` controller，`session.Delete` 之后 | `apps/web/controller/account/account.go` |
| 会话活跃 | `SessionMiddleware`，`AuthenticateSession` 成功后 | `pkg/ginx/middleware/session.go` |

现有代码现状：
- Account DAO：`apps/web/dao/global/account.go`，已有 `UpdateFields(ctx, id, map[string]interface{})`
- Session 中间件：`pkg/ginx/middleware/session.go`，每个认证请求调用 `AuthenticateSession`
- Session 存储：Redis（`pkg/lib/session/session.go`）

## 边界情况

- **并发登录**：同一账号多设备同时登录，各自写 `last_login_at`，最后覆盖即可
- **登出失败**：`session.Delete` 失败不写 `last_logout_at`，不阻塞登出响应
- **Redis 不可用**：跳过本次刷新 + warning 日志 + 指标上报
- **账号已删除**：`UpdateFields` 带 `is_deleted = false`，软删除账号自动跳过

## 验证

- 登录后检查 `last_login_at` 已刷新
- 登出后检查 `last_logout_at` 已刷新
- 认证请求后检查 15 分钟内 `last_active_at` 只刷新一次
- 并发登录不报错，各自时间戳落地
- Redis 不可用时不写 DB，但会打 warning 和指标
