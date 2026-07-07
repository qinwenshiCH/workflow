---
name: speckit.detail
description: Generate a detailed design (LLD) from spec.md and plan.md. Use when plan.md is approved and the change involves DB schema, async/retry, cross-module collaboration, security, or 3+ core files.
handoffs:
  - label: Create Tasks
    agent: speckit.tasks
    prompt: Break the approved detailed design into implementation tasks.
    send: true
  - label: Create Checklist
    agent: speckit.checklist
    prompt: Create a quality checklist for the detailed design.
---

## User Input

```text
$ARGUMENTS
```

## Overview

读取 `01-spec.md` 和已确认的 `03-plan.md`，生成或更新 **详细设计** `04-detail.md`，严格遵循 `specs/_template/04-detail.md` 的结构。产出文件统一命名为 `04-detail.md`（单方案）或 `04-detail-{后缀}.md`（多方案，如 `04-detail-pg.md`、`04-detail-doris.md`）。

`detail.md` 是实现前的落地说明书：

| Artifact | 回答的问题 |
|---|---|
| `plan.md` | 读懂方案 |
| `detail.md` | **准备实现** |

**核心要求：** 不要复制 plan.md。detail.md 必须比 plan.md 更具体、更可执行。如果发现 plan.md 有误，先修正再写 detail，不照抄。

---

## 适用边界

> speckit.detail 只在 speckit.plan 判定为"中/大"变更时使用，或用户明确要求时进入。

典型触发条件（满足任一）：

- 涉及数据库 schema / 索引 / migration
- 涉及异步、队列、重试、幂等、分布式补偿
- 涉及跨模块协作或外部依赖
- 涉及权限、安全、审计、缓存一致性
- 涉及 3 个以上核心文件
- 顶层方案已有争议

---

## 写作原则

### 强制约束

1. **先验证再写** — detail.md 必须包含一致性校验章节。发现 plan 假设不成立时，必须在 detail 中修正
2. **事务边界必须精确标注** — 使用 `🟢 Begin Transaction` / `🔴 Commit` / `🔄 Rollback` 标记
3. **每类失败路径必须分析** — 外部依赖异常、参数非法、事务回滚、并发冲突
4. **每个文件必须有修改理由** — 为什么是改这个文件而不是其他文件
5. **接口设计必须评估"深度"** — Deep Module（好）vs Shallow Module（避免）
6. **测试策略必须落到具体场景** — 具体列出正常/边界/异常场景

### 常见失败模式

| 失败模式 | 表现 | 预防 |
|---|---|---|
| 照抄 plan | 没有新增信息 | Step 1 先搜索代码，让细节从代码中浮现 |
| 只写正常路径 | 90% 内容是一切正常时怎么做 | 每个模块都必须分析失败路径 |
| 事务描述模糊 | "在事务中执行"但没标注范围 | 使用 🟢🔴🔄 标记精确边界 |
| 理由缺失 | 改了文件但不说为什么选这个文件 | 文件影响清单必须有"理由"列 |
| 测试不具体 | "覆盖所有场景"没有具体 | 列出到具体场景级别 |
| 接口不评估 | 设计了接口但不说好不好 | 必须做接口深度评估 |

---

## Steps

### Step 1 — 分析准备与一致性校验

#### 1.1 读取 artifacts

- [x] 读取 `01-spec.md` — 确认用户故事和验收条件
- [x] 读取 `03-plan.md` — 理解推荐方案和架构决定
- [x] 读取 `02-decisions.md`、`HABITS.md` — 确认约束
- [x] 阅读 `02-decisions.md` 中已有的决策记录，避免重复讨论已定事项
- **设计过程中产生新决策时，即时追加到 `02-decisions.md**（参见 CLAUDE.md 「决策记录纪律」）

#### 1.2 代码探索（必须做）

针对 plan 中提到的每个模块、接口、数据模型，在代码库中验证：

```bash
# 确认模块是否存在
find src/{{module-path}} -type f -name "*.go" | head -20

# 确认现有接口签名
grep -r "func .*{{InterfaceName}}" src/ --include="*.go"

# 确认现有数据模型
grep -r "type {{Entity}} struct" src/ --include="*.go"

# 确认事务使用模式（了解当前代码的事务风格）
grep -r "tx\." src/{{module-path}}/ --include="*.go" | head -10

# 确认测试模式
ls src/{{module-path}}/*_test.go
```

**重要：** 记录与 plan 假设不符的发现。这些必须在 detail.md 的"一致性校验"章节显式记录。

#### 1.3 Spec vs Plan 校验

逐项检查：
1. Spec 所有 P0 用户故事在 plan 中有对应方案
2. Plan 的 out-of-scope 与 spec 一致
3. Spec 的边界情况在 plan 中有考量
4. Plan 的技术假设与 spec 的非功能需求一致

#### 1.4 Plan vs 代码现实校验

| 假设 | 验证方式 | 预期 | 实际 |
|---|---|---|---|
| 模块 X 存在 | `ls path/to/X` | 存在 | 存在 / 不存在 |
| 接口 Y 签名 | `grep "func Y"` | `func Y(a, b)` | `func Y(a, b, c)` |
| 表 Z 结构 | `grep "CREATE TABLE Z"` | 有 F1, F2 | 有 F1, F2, F3 |

**发现不匹配时：**
- 小差异（多一个参数、多一个字段）→ 在 detail 中修正
- 大差异（模块不存在、架构完全不同）→ 回退修改 plan

---

### Step 2 — 编写 `04-detail.md`

严格按照编号规则命名产出文件：`04-detail.md`（单方案）或 `04-detail-{后缀}.md`（多方案）。严格按 `specs/_template/04-detail.md` 的结构输出。

#### 2.1 背景承接（第 1 章）

回顾 plan 的推荐方案和关键决策。明确写出本 detail 要解决的具体实现问题。

#### 2.2 一致性校验（第 2 章）

如果 Step 1 发现偏差，在此显式记录。没有偏差时也应保持章节存在并标记"全部一致"。

#### 2.3 实现总览（第 3 章）

**文件影响清单每个条目必须写修改理由。**

✅ 好例子：
```
| src/service/order.go | 修改 | ConfirmOrder() 中新增审计日志写入 |
|                        |      | 理由：ConfirmOrder 是订单确认的唯一入口 |
```

❌ 差例子：
```
| src/service/order.go | 修改 | 新增审计日志写入 |
```

#### 2.4 数据模型 / API / 配置（第 4 章）

精确到字段、类型、约束，不允许占位符。DDL 语句必须可执行。

#### 2.5 分模块方案（第 5 章）

每个模块必须完整覆盖：

**① 函数签名** — 精确到参数和返回值类型

**② 核心逻辑** — 自然语言，重点说明关键决策点的选择理由

**③ 事务边界** — 使用标准标记：

```
🟢 ── Begin Transaction ──────────────────────────
    │ 1. 更新 order 表（SET status='confirmed'）
    │ 2. 插入 audit_log 表
    │    影响范围：order 表第 {{id}} 行（行锁）
🔴 ── Commit / 🔄 Rollback ──────────────────────
```

**④ 错误处理** — 包括但不限于外部依赖超时、DB 死锁、参数非法、并发写入、事务超时

**⑤ 权限校验** — 标注每个接口的校验方式和跨组织隔离

**⑥ 缓存/监控/异步** — Key 模式、过期、更新策略；监控指标；异步可靠性保障

**⑦ 接口深度评估：**

| 维度 | 结果 | 说明 |
|---|---|---|
| Interface 大小 | 少量方法 / 多方法 | |
| 隐藏复杂度 | 大量实现 / 薄实现 | |
| 可测试性 | 好 / 中 / 差 | |
| 评价 | Deep ✅ / Shallow ⚠️ | |

#### 2.6 流程图（第 6 章）

必须包含正常流程和失败流程两张图。

#### 2.7 时序图（第 7 章）

展示完整调用链路，标注事务开始/提交/回滚时机。

#### 2.8 测试策略（第 8 章）

落实到具体场景级别：

| 类型 | 范围 | 具体场景 | 方法 |
|---|---|---|---|
| 单元测试 | {{函数}} | 正常输入、空列表、非法值 | {{框架}} + Mock |
| 集成测试 | {{模块链路}} | 事务回滚、并发写入 | {{docker-compose}} |
| 边界测试 | {{边界值}} | 极限值、null、重复提交 | {{参数化}} |

#### 2.9 实现风险（第 9 章）

拷问实现过程中最可能出错的地方：

| 风险点 | 概率 | 影响 | 预防 | 补救 |
|---|---|---|---|---|
| {{最易出错的地方}} | 高/中/低 | 高/中/低 | {{预防}} | {{补救}} |

**引导问题：** 事务范围太大导致死锁？缓存与 DB 不一致窗口？并发竞态？第三方依赖挂了？回滚方案？

---

### Step 3 — 质量自查

逐项检查 QG-1 ~ QG-11（完整清单见模板末尾）。任一 gate 未通过则 detail.md 为草稿，不得 handoff。

重点关注容易被忽视的：
- QG-2：所有"过程状态"都有对应的失败处理
- QG-4：抽象层级 ≤ 2 层；3+ 文件评估合并方案
- QG-7：缓存操作在事务提交后执行
- QG-8：事务内无跨网络调用
- QG-9：一致性校验已完成
- QG-10：每个外部依赖都有故障影响评估
- QG-11：回滚检查清单和上线观测指标已定义

---

### Step 4 — handoff

自检全部通过后 handoff 到 `speckit.tasks`。传递实现依赖顺序、并行可能、测试依赖信息。

---

## Mandatory Quality Gates

| QG | 核心要旨 |
|---|---|
| QG-1 Performance | 无 N+1，大事务 < 200ms，关键查询有索引 |
| QG-2 Data Integrity | 同一事务内完成，无孤儿数据，有补偿机制 |
| QG-3 Security | 每个接口标注权限，跨组织显式隔离 |
| QG-4 Simplicity | 无新框架，抽象 ≤ 2 层，3+ 文件评估合并 |
| QG-5 Completeness | 文件清单完整，失败路径分析，测试策略 |
| QG-6 Architecture | 跨模块仅 Service 依赖，无 DAO 注入 |
| QG-7 Cache/Redis | 缓存操作事务后执行，评估一致性窗口 |
| QG-8 分布式部署 | 事务内无跨网络调用，补偿幂等 |
| QG-9 一致性校验 | Spec↔Plan↔Code 交叉验证完成 |
| QG-10 外部依赖 | 所有依赖接口契约已记录，故障影响已评估 |
| QG-11 上线回滚 | Migration 有 DOWN 语句，观测指标和回滚条件已定义 |
