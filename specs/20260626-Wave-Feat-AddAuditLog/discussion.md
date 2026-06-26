# 需求讨论：项目级操作审计日志

**用途**: 集中管理所有 open questions、trade-offs、待决策事项
**状态**: 讨论中，无定论
**如何用**: 每条讨论包含背景 → 选项 → 推荐 → 最终决定。确认后更新 spec.md 和 decisions.md

---

## 一、Detail 数据模型

### D-01：detail 结构方案

changes[] 和 extra 共存于一个 JSONB：

```json
{
  "name": "资产名（快照）",
  "changes": [
    {"field": "name", "action": "changed", "before": "旧", "after": "新"}
  ],
  "extra": {
    "from_status": "DRAFT",
    "to_status": "ONLINE"
  }
}
```

**理由**:
- `changes[]` → 字段级 diff，覆盖 90% 场景（CRUD 的字段变更）
- `extra` → 承载非字段级语义信息（status_change、copy 的 source）
- 两者可以同时存在（如上线时既改了 status 也改了配置参数）

**决策**: ✅ 已确定

---

### D-02：多字段变更 — 语义单元

**场景**: 用户一次保存改了 5 个字段，是一条审计记录还是多条？

**决策**: **A — 一次 Log() = 一条记录，多个 change 共存于同一条**。不需要 operation_group_id，同一条记录天然是一组。

**理由**: 一次业务操作的多个字段变更是同一个语义单元，拆散则失去了"这些字段是同时被改的"这个信息。PostHog 也是这个做法。

---

### D-03：status_change 是独立 action_type 还是字段级 changed？

**决策**: **B — 优先用字段级 changed 表明含义，不增加独立的 action_type 枚举**。与 PostHog 理念对齐。

**理由**: 状态变化本质上是一个字段（或几个字段）的值从 A 变成 B。如果引入单独的 `status_change`，边界会模糊——"改描述算不算 update？改 status 算不算 status_change？"。统一用 `changes[]` 表达变更语义，查询端通过字段名（如 `status`、`state`）来分辨。

**影响**: 需要更新 spec.md：
- action_type 枚举不再包含 `status_change`
- AB 状态变更示例改为 changes[] 风格：
  ```json
  {
    "name": "experiment-v2",
    "changes": [
      {"field": "status", "action": "changed", "before": "DRAFT", "after": "RUNNING"},
      {"field": "rollout_percentage", "action": "changed", "before": 0, "after": 100}
    ]
  }
  ```

---

## 二、性能与规模

### D-04：索引策略 — 数量与写入放大

**当前 spec 方案**（需修改）:

```sql
idx_pal_project_created  ON (project_id, created_at DESC)
idx_pal_asset            ON (project_id, asset_type, asset_id, created_at DESC)
idx_pal_operator         ON (project_id, operator_id, created_at DESC)
idx_pal_action           ON (project_id, action_type, created_at DESC)
```

**决策**: **B — 精简索引，去掉 project_id 前缀**。

**理由**:
1. 表在 meta schema 下（`meta_{project_id}.project_audit_log`），每个 schema 只属于一个项目。表里的所有数据都已经是该项目的，`project_id` 列仅作为数据冗余，不需要进索引
2. 核心查询维度是**按资产**：`(asset_type, asset_id, created_at DESC)` — 这是主索引
3. 从少开始，逐步加。PostHog 也是上线后根据实际查询补的索引

**新索引方案**:
```sql
-- 主索引：按资产查询
idx_pal_asset ON (asset_type, asset_id, created_at DESC)

-- 辅助索引：按操作人和时间查询（可选，根据实际频率决定是否保留）
-- idx_pal_operator ON (operator_id, created_at DESC)
-- idx_pal_action ON (action_type, created_at DESC)
```

---

### D-05：数据保留策略

**问题**: 审计日志保存多久？到期怎么处理？

**决策**: **A — 目前先不删，但考虑数据量大的情况**。

**理由**: 短期内以功能交付为主，保留策略后续再上。但在设计表结构时预留扩展点。

**新增需求 — 操作来源/渠道**:
- 需要区分操作来源：前端、OpenAPI、MCP、内部定时任务
- 参考 PostHog 的 `client` 字段，Wave 需要一个 `source` 或 `channel` 字段
- 用途：过滤内部系统操作（如定时任务产生的自动变更）vs 人工操作，方便审计时只看用户操作
- 建议加到表结构：`source VARCHAR(32) NOT NULL DEFAULT ''`

---

### D-06：分区方案

**问题**: 按什么维度分区？

**决策**: **V1 不做分区**。

**理由**:
- 表在 meta schema 下，已经按 project_id 物理隔离了
- 配合 `(asset_type, asset_id, created_at DESC)` 索引，单项目千万级查询够用
- 分区表在 PG 中有维护成本（分区裁剪、约束排除），V1 先去这个复杂度
- 设计上保持扩展点：`created_at` 是 TIMESTAMPTZ，后续加按月分区不需要改业务代码

---

## 三、噪音与安全

### D-07：排除字段清单

**问题**: 三层排除体系的具体字段列表。

目前确定的：

```
通用排除: id, created_at, updated_at, created_by, last_modified_by
```

**待补充**:
- 各资产类型的按类型排除字段（需各模块确认）
- 变更级排除（仅当这些字段变更时跳过记录）

**操作**: 需要在 implement 阶段各资产类型配合提供排除字段清单

---

### D-08：敏感字段掩盖粒度

**问题**: 敏感字段替换为 "masked" 的粒度怎么控制？

**讨论**:
- PostHog 的做法是整值替换为 `"masked"`，丢失了"哪些敏感字段被改过"的信息
- 更精细的做法：保留字段名和 action 信息，仅掩盖值

```json
// PostHog 风格（丢失字段信息）
{"field": "email", "action": "changed", "before": "masked", "after": "masked"}

// 更精细的做法（保留"改了 email"的事实）
{"field": "email", "action": "changed", "masked": true}
```

**待讨论**: Wave 需要哪种粒度？安全合规是否有具体约束？

---

### D-09：大 detail 处理策略

**决策**: **优先 LZ4 压缩，压缩后仍超出阈值则逐字段截断**。

**理由**:
- Wave 已有现成的 LZ4 压缩工具 `pkg/lib/util/compress.go`，LZ4 速度快（适合写入路径），对 JSON 文本通常有 2-4x 压缩比
- 阈值 64KB 不变，但超出时不直接截断，先压缩
- 压缩后仍超 64KB 才逐字段截断（方案 B）

**写入流程**:

```text
detail JSON → serialize → < 64KB? → 直接写入
                              ≥ 64KB? → LZ4 压缩 → 带压缩标记写入
                                          → 仍 ≥ 64KB? → 逐字段截断 + 警告日志
```

---

## 四、AB 模块兼容

### D-10：AB 迁移策略

**要求**（已确定）:

1. 旧 `details.operation_records` 保留不删
2. 新操作全部写入新 `project_audit_log` 表
3. 前端查询时合并展示新旧数据

**待讨论**:
- 是否做一次数据回填：把旧的 `details.operation_records` 复制到新表？还是前端查询时做 UNION？
- 如果回填，预计数据量？是一次性迁移还是在线迁移？

**讨论**:
- 回填的好处：查询逻辑统一，后续可以废弃旧字段
- 不回填的好处：零风险，但查询层永远要处理两条路径
- 折中：写一次性迁移脚本，但不作为上线 blocker，什么时候有空什么时候跑

---

### D-11：operator_name 存不存

**问题**: 表结构中有 `operator_name VARCHAR(255)`，为什么要存？

**背景**: 用户信息可能在审计记录产生后被修改或删除。如果 JOIN user 表：
- 用户被删除 → 查不到名字 → 显示 null
- 用户改名 → 历史审计记录里的名字也跟着变（不正确）

**所以存快照是正确的**。代价是每条记录多存一个短字符串。

**决策**: ✅ 保留 `operator_name`，写入时快照。这是审计的常识性做法。

---

### D-12：是否需要 operation_group_id

**问题**: 同一次业务操作的多个字段变更是否需要显式关联 ID？

**结论**: 不需要。同一条记录天然是一组操作，`created_at` 精度足以关联。引入 group_id 增加了写入和查询复杂度但没有实质收益。

---

### D-13：操作来源/渠道字段（source）

**问题**:是否需要记录操作来自哪里？

**背景**: 在讨论 D-05（数据保留策略）时发现，审计日志需要区分操作来源。PostHog 有 `client` 和 `ip_address` 两个字段，Wave 的内部操作（定时任务、MCP）和用户操作（前端、OpenAPI）应该有区分。

**已确定的字段**: `source VARCHAR(32)`，枚举值：web / openapi / mcp / internal

**待讨论**: 是否还需要记录 IP 地址？

---

## 五、待决策清单

| ID | 议题 | 优先级 | 状态 |
|----|------|--------|------|
| D-07 | 排除字段清单 | P1 | ⏳ 需各模块确认 |
| D-08 | 敏感字段掩盖粒度 | P1 | ⏳ 待定 |
| D-10 | AB 迁移策略（回填？） | P1 | ⏳ 待定 |
| D-13 | 是否记录 IP 地址 | P2 | ⏳ 待定 |
