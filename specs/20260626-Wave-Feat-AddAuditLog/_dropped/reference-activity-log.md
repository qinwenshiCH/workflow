# 活动日志实现参考

> 本文档是 [plan-activity-log.md](./plan-activity-log.md) 的实现参考，包含完整示例、代码模板、场景目录、迁移方案和接入 SOP。设计方案请参见 plan-activity-log.md。

## 1. Pipeline 完整示例

以下三个示例展示完整数据流：从业务投影 → WriteInput → ActivityService 构造 → 序列化落盘。

以下三个示例展示完整的数据流：从业务投影 → WriteInput → ActivityService 构造 → 序列化落盘。

---

#### 示例 A：更新账号手机号（Update + 脱敏）

**场景**：用户 "张三" 在个人设置中将手机号从 `13800138000` 改为 `13900139000`。

**Step ①-②: 投影原始值**

```go
oldProj = map[string]any{
    "name":  "张三",
    "phone": "13800138000",    // 原始值，不脱敏
}

newProj = map[string]any{
    "name":  "张三",
    "phone": "13900139000",    // 原始值，不脱敏
}
```

**Step ③: 调用 WriteLog**

```go
svc.WriteLog(ctx, activity.WriteInput{
    ItemType:      "account",
    ItemID:        1024,
    ItemName:      "张三",
    ActionType:    "update",
    PolicyKey:     "account.update_profile",
    OldProjection: oldProj,
    NewProjection: newProj,
    MaskRules: activity.MaskRules{
        "phone": func(v any) any {
            s := v.(string)
            return s[:3] + "****" + s[len(s)-4:]   // "138****8000"
        },
    },
})
```

**ActivityService 内部执行：**

ChangesBetween(oldProj, newProj) → `"name"` 未变化，`"phone"` 变化：
```json
[
  {"field": "phone", "action": "changed", "before": "13800138000", "after": "13900139000"}
]
```

ApplyMaskRules → `"phone"` 命中 `MaskRules`，脱敏后：
```json
[
  {"field": "phone", "action": "changed", "before": "138****8000", "after": "139****9000"}
]
```

序列化 → INSERT → `detail_payload`：
```json
{"changes":[{"field":"phone","action":"changed","before":"138****8000","after":"139****9000"}]}
```

> 脱敏后仍能看出号码确实变了（138→139），但看不到完整号码。变更事件不丢、隐私值已抹。

**Step ④: 处理结果**

PolicyKey `account.update_profile` → WritePolicy `blocking` → err != nil 则返回 error。

---

#### 示例 B：AB Experiment 发布（多字段变更 + extra）

**场景**：实验 "new_checkout" 从 RUNNING 发布为 RELEASED。伴随状态、流量、分桶等多个字段同时变更。

**Step ①-②: 投影原始值**

```go
oldProj = map[string]any{
    "status":      "RUNNING",
    "enabled":     true,
    "traffic":     30,
    "bucket":      "slot_7",
    "bucket_bits": 10,
}

newProj = map[string]any{
    "status":       "RELEASED",
    "enabled":      true,
    "traffic":      100,
    "release_plan": []map[string]any{
        {"step": 1, "traffic": 30, "duration_min": 60},
        {"step": 2, "traffic": 60, "duration_min": 60},
        {"step": 3, "traffic": 100},
    },
    "bucket":       "",
    "bucket_bits":  0,
}
```

**Step ③: 调用 WriteLog**

```go
svc.WriteLog(ctx, activity.WriteInput{
    ItemType:      "experiment",
    ItemID:        15,
    ItemName:      "new_checkout",
    ActionType:    "release",
    PolicyKey:     "ab.release",
    OldProjection: oldProj,
    NewProjection: newProj,
    // 无敏感字段，MaskRules 为空；extra 为空（状态语义已在 action_type 中表达）
})
```

**ActivityService 内部执行：**

ChangesBetween(oldProj, newProj) → `"enabled"` 未变化，其余 4 个字段变更：
```json
[
  {"field": "status",       "action": "changed", "before": "RUNNING",   "after": "RELEASED"},
  {"field": "traffic",      "action": "changed", "before": 30,          "after": 100},
  {"field": "release_plan", "action": "created", "after": [{"step":1,"traffic":30,"duration_min":60},{"step":2,"traffic":60,"duration_min":60},{"step":3,"traffic":100}]},
  {"field": "bucket",       "action": "changed", "before": "slot_7",    "after": ""},
  {"field": "bucket_bits",  "action": "changed", "before": 10,          "after": 0}
]
```

ApplyMaskRules → MaskRules 为空，跳过。组装 Extra。

序列化 → INSERT → `detail_payload`：
```json
{
  "changes": [
    {"field": "status",       "action": "changed", "before": "RUNNING", "after": "RELEASED"},
    {"field": "traffic",      "action": "changed", "before": 30,        "after": 100},
    {"field": "release_plan", "action": "created", "after": [{"step":1,"traffic":30,"duration_min":60},{"step":2,"traffic":60,"duration_min":60},{"step":3,"traffic":100}]},
    {"field": "bucket",       "action": "changed", "before": "slot_7",  "after": ""},
    {"field": "bucket_bits",  "action": "changed", "before": 10,        "after": 0}
  ],
  "extra": {}
}
```

> `release_plan` 是一个嵌套数组，ChangesBetween 不区分标量和嵌套，直接记录全量 before/after。受 64KB 预算约束。

**Step ④: 处理结果**

PolicyKey `ab.release` → WritePolicy `blocking` → 如果 `err != nil` 则返回 error 回滚事务。

---

#### 示例 C：批量删除 Chart（BatchWriteLog + Delete → 自动捕获快照）

**场景**：用户选中 3 张图表批量删除——折线图（ID=101，v3，属于 dashboard #5/#8）、柱状图（ID=102，v1）、饼图（ID=103，v5）。

**Step ①-②: 投影**（delete 场景只需 old 投影，投影 = 我关心的全部字段）

```go
// Chart #101 — DAU趋势，存在于 2 个 Dashboard 中
oldProj_101 = map[string]any{
    "name":          "DAU趋势",
    "type":          "line",
    "version":       3,
    "dashboard_ids": []int64{5, 8},
}

// Chart #102 — 新增用户
oldProj_102 = map[string]any{
    "name":          "新增用户",
    "type":          "bar",
    "version":       1,
    "dashboard_ids": []int64{},
}

// Chart #103 — 来源分布
oldProj_103 = map[string]any{
    "name":          "来源分布",
    "type":          "pie",
    "version":       5,
    "dashboard_ids": []int64{5},
}
```

**Step ③: 调用 BatchWriteLog**

```go
svc.BatchWriteLog(ctx, []activity.WriteInput{
    {ItemType: "chart", ItemID: 101, ItemName: "DAU趋势", ActionType: "delete",
     PolicyKey: "chart.delete", OldProjection: oldProj_101},
    {ItemType: "chart", ItemID: 102, ItemName: "新增用户", ActionType: "delete",
     PolicyKey: "chart.delete", OldProjection: oldProj_102},
    {ItemType: "chart", ItemID: 103, ItemName: "来源分布", ActionType: "delete",
     PolicyKey: "chart.delete", OldProjection: oldProj_103},
})
// 同批共享 correlation_id，任意一条失败 → 整体 error，业务事务回滚。
```

**ActivityService 内部执行：**

`ActionType == "delete"` → 不跑 ChangesBetween（没必要），直接将 oldProj 捕获为 snapshot。

序列化 → INSERT：
```json
// Row 1
{"snapshot":{"name":"DAU趋势","type":"line","version":3,"dashboard_ids":[5,8]}}
// Row 2
{"snapshot":{"name":"新增用户","type":"bar","version":1,"dashboard_ids":[]}}
// Row 3
{"snapshot":{"name":"来源分布","type":"pie","version":5,"dashboard_ids":[5]}}
```

> Delete 时 changes[] 无意义（对象已删、字段级 `{action:"deleted"}` 是噪音）。规则：`Create → changes(nil, newProj)`，`Update → changes(oldProj, newProj)`，`Delete → snapshot(oldProj)`，由 ActionType 自动推导，业务方只管投影。

**Step ④: 处理结果**

## 1.5 读路径：前端查询与展示

> 写路径定义了"活动数据怎么落"，读路径定义"前端怎么查、怎么渲染"。两者通过统一的 `ListByQuery` API 和 `Detail.Changes[]` 结构衔接，不需要每个业务域重复写查询逻辑。

### 1.5.1 查询 API

业务 controller 只需一行调用：

```go
result, total, err := activitySvc.ListByQuery(ctx, activity.Query{
    ItemType: "metric",     // 必填
    ItemID:   metricID,     // 必填
    ActionType: nil,        // 可选过滤，空 = 不过滤
    Page:     1,
    PageSize: 20,
})
ginx.JSON(c, ginx.ListResult{Total: total, Data: result})
```

返回的 `Item` 结构（后端已自动将 `detail_payload` 反序列化为 `Detail`）：

```json
{
    "id": 1001,
    "item_type": "metric",
    "item_id": 42,
    "item_name": "DAU",
    "action_type": "update",
    "operator_id": 100,
    "operator_name": "张三",
    "source": "ui",
    "occurred_at": "2026-07-01T10:00:00Z",
    "detail": {
        "changes": [
            {"field": "aggregations", "action": "changed", "before": "avg", "after": "p90"},
            {"field": "rollup", "action": "changed", "before": "daily", "after": "hourly"}
        ]
    }
}
```

### 1.5.2 两层渲染模型

前端把每条活动记录拆成两层展示：

```text
┌─ action_type: "update"        ← 标题层：做了什么
│  operator: "张三"
│  time: "2026-07-01 10:00"
│
├─ Changes[0]: aggregations     ← 详情层：什么变了
│     avg → p90
│
├─ Changes[1]: rollup
│     daily → hourly
```

**标题层由 `action_type` 驱动**，是活动记录的"一句话摘要"。前端维护一张 `action_type → 中文标签` 映射（和 AB 现有 `getOperationType()` 完全一致）：

```typescript
const actionTypeLabels: Record<string, string> = {
    create:  "创建",
    update:  "更新",
    delete:  "删除",
    copy:    "复制",
    online:  "上线",
    offline: "下线",
    debug:   "调试",
    release: "发布完成",
    launch:  "启动",
    pause:   "暂停",
    resume:  "恢复",
    finish:  "完成",
    stop:    "停止",
}

// 渲染标题："张三 更新了指标"
`${record.operator_name} ${actionTypeLabels[record.action_type]}了${record.item_name}`
```

**详情层由 `Changes[]` 驱动**，展示具体哪些字段发生了变化。前端维护一张可选的 `item_type + field → 中文标签` 映射。注意 field 名是投影函数 `toActivityProj()` 输出的 map key，不是数据库列名。

```typescript
// 按 item_type 分组的字段标签映射（非必须，没有映射时直接显示 field 名）
const fieldLabels: Record<string, Record<string, string>> = {
    metric: {
        name:          "指标名称",
        aggregations:  "聚合方式",
        rollup:        "汇总周期",
        define:        "口径定义",
    },
    campaign: {
        status:        "状态",
        name:          "活动名称",
        schedule_type: "调度类型",
    },
}

function renderChanges(itemType: string, changes: Change[]): string[] {
    const labels = fieldLabels[itemType] ?? {}
    return changes.map(c => {
        const label = labels[c.field] ?? c.field
        const actionMap: Record<string, string> = {
            created: "设为",
            changed: "→",
            deleted: "删除",
        }
        return `${label}: ${c.before ?? ""} ${actionMap[c.action] ?? c.action} ${c.after ?? ""}`
    })
}

// 输出：["聚合方式: avg → p90", "汇总周期: daily → hourly"]
```

### 1.5.3 四种常见展示形态

| 形态 | 何时出现 | 示例 | 渲染方式 |
| --- | --- | --- | --- |
| **纯标题** | Changes 为空，action_type 本身已表达语义 | `action_type = "launch"` | "启动" + item_name |
| **标题 + 变更列表** | 最常见的 Update 场景 | `action_type = "update"` + Changes 有 1-N 条 | 标题 + 字段 diff 列表 |
| **标题 + 展开面板** | 涉及配置类字段，变化项多 | `action_type = "update"` + Changes 多条长文本 | 标题 + 可展开的 diff 详情 |
| **删除快照** | Delete 场景捕获快照 | `action_type = "delete"` + Snapshot 非空 | "删除" + 可展开的删除前快照 |

删除快照渲染示例：

```json
// 后端返回
{
    "action_type": "delete",
    "detail": {
        "snapshot": {"name": "DAU", "aggregations": "p90"}
    }
}
```

前端：显示"张三 删除了指标 DAU"，提供"查看删除前快照"展开按钮展示 Snapshot 内容。不能展示为 changes[]，因为对象已不存在。

### 1.5.4 示例：Metric 活动历史

**后端 controller 层**——3 行代码：

```go
// GET /api/projects/:pid/metrics/:id/activity
func (c *MetricController) ListActivity(ctx *gin.Context) {
    result, total, err := c.activitySvc.ListByQuery(ctx, activity.Query{
        ItemType: "metric",
        ItemID:   param.ID,
    })
    ginx.JSON(c, ginx.ListResult{Total: total, Data: result})
}
```

**前端渲染效果**（Timeline 展示）：

```text
┌─────────────────────────────────────────────────────┐
│ 今天 14:30                                           │
│   ● 张三 更新了指标 DAU                               │
│     ┌────────────────────────────────────────┐       │
│     │ 聚合方式  avg → p90                     │       │
│     │ 汇总周期  daily → hourly                │       │
│     └────────────────────────────────────────┘       │
│                                                      │
│ 今天 10:00                                           │
│   ● 张三 创建了指标 DAU                               │
└─────────────────────────────────────────────────────┘
```

### 1.5.5 示例：MA Campaign 活动历史

**后端 controller 层**——同样 3 行代码：

```go
// GET /api/projects/:pid/campaigns/:id/activity
func (c *CampaignController) ListActivity(ctx *gin.Context) {
    result, total, err := c.activitySvc.ListByQuery(ctx, activity.Query{
        ItemType: "campaign",
        ItemID:   param.ID,
    })
    ginx.JSON(c, ginx.ListResult{Total: total, Data: result})
}
```

**前端渲染效果：**

```text
┌─────────────────────────────────────────────────────┐
│ 2026-07-01 15:00                                     │
│   ● 李四 启动了营销活动 双十一大促                      │
│     ┌────────────────────────────────────────┐       │
│     │ 状态  draft → running                   │       │
│     └────────────────────────────────────────┘       │
│                                                      │
│ 2026-07-01 14:00                                     │
│   ● 李四 创建了营销活动 双十一大促                      │
└─────────────────────────────────────────────────────┘
```

### 1.5.6 字段标签映射维护原则

前端有两张映射表，维护成本不同：

| 映射表 | 必须？ | 维护方式 |
| --- | --- | --- |
| `action_type → 中文标签` | **必须** | 活动模块统一定义，所有业务域共用。新增 extension action_type 时同步补充 |
| `item_type + field → 中文标签` | **可选** | 各业务域按需维护。没有映射时直接显示 field 名 |

**为什么 field 标签映射是可选的？**

因为 Changes[] 的结构化设计已经保证了数据的基本可读性。`{"field":"aggregations","action":"changed","before":"avg","after":"p90"}` 即使前端不知道"aggregations"的中文，也能直接展示为 `aggregations: avg → p90`。排障场景下完全可接受。产品化场景下按需补充标签映射即可。

### 1.5.7 关键规则

1. **API 不承担展示逻辑**。`ListByQuery` 返回原始 Changes[]，不做字段名翻译、不做展示文案拼接。展示逻辑全部在前端。
2. **Changes[] 既是存储格式也是展示格式**。不需要后端再做一次转换。
3. **field 名 = 投影函数输出的 map key**。不是数据库列名。`toActivityProj()` 输出的 key 决定了 Changes[].field 的值。
4. **前端不需要知道 Detail 内部结构**。后端已反序列化 `detail_payload`，前端直接消费即可。

## 2. 代码模板

### 2.1 三种场景代码模板

三个模板的共同模式：业务方只做**投影 + 调用 Write**，ChangesBetween / ApplyMaskRules 由 ActivityService 内部完成。

#### 2.1.Create（无旧值，old=nil）

```go
func (s *MetricService) CreateMetric(ctx, req) (*Metric, error) {
    metric, err := s.dao.Create(req)
    if err != nil {
        return nil, err
    }

    newProj := toActivityProj(metric)          // 投影（原始值），old=nil
    writeErr := s.activitySvc.WriteLog(ctx, activity.WriteInput{
        ItemType:      "metric",
        ItemID:        metric.ID,
        ItemName:      metric.Name,
        ActionType:    "create",
        PolicyKey:     PolicyMetricCreate,
        NewProjection: newProj,                 // ActivityService 内部 ChangesBetween(nil, newProj)
        MaskRules:     activity.MaskRules{...}, // 有敏感字段时声明
    })
    // WritePolicy 决定 writeErr 是否返回
    if writeErr != nil && policyRegistry[PolicyMetricCreate] == WritePolicyRequiredFull {
        return nil, writeErr
    }
    return metric, nil
}
```

#### 2.1.Update（old + new 双投影）

```go
func (s *MetricService) UpdateMetric(ctx, id, req) error {
    // ① 读旧值（复用业务本来就有的读）
    old := s.dao.Get(id)
    if old.OrgID != userOrg(ctx) {
        return ErrForbidden
    }

    oldProj := toActivityProj(old)           // ② old 投影（原始值）

    if err := s.dao.Update(id, req); err != nil { // ③ 业务变更
        return err
    }

    cur := s.dao.Get(id)                       // ④ 读新值
    newProj := toActivityProj(cur)           // ⑤ new 投影（原始值）

    err := s.activitySvc.WriteLog(ctx, activity.WriteInput{
        ItemType:      "metric",
        ItemID:        cur.ID,
        ItemName:      cur.Name,
        ActionType:    "update",
        PolicyKey:     PolicyMetricUpdate,
        OldProjection: oldProj,                // ActivityService 内部 ChangesBetween(oldProj, newProj)
        NewProjection: newProj,
    })
    if err != nil {
        // WritePolicy 决定是否回滚业务事务
        if policyRegistry[PolicyMetricUpdate] == WritePolicyRequiredFull {
            return err
        }
        slog.Warn("activity log best-effort failed", "err", err)
    }
    return nil
}
```

> **事务说明**：`dao.Get(①)` → `dao.Get(④)` 必须在同一个事务连接上执行，才能保证④读到的是③刚写入的值。调用方需要将事务连接透传到 dao 方法中（Wave 现有模式是用 `tx *gorm.DB` 或在 ctx 中携带事务）。

#### 2.1.Delete（删除前投影，new=nil）

```go
func (s *MetricService) DeleteMetric(ctx, id) error {
    old := s.dao.Get(id)              // 删除前读
    oldProj := toActivityProj(old)  // 删除前投影（原始值）

    if err := s.dao.Delete(id); err != nil {
        return err
    }

    // new=nil → ActivityService 内部 ChangesBetween(oldProj, nil)
    // changes 全部为 {action: "deleted", before: 值}

    err := s.activitySvc.WriteLog(ctx, activity.WriteInput{
        ItemType:      "metric",
        ItemID:        old.ID,
        ItemName:      old.Name,
        ActionType:    "delete",
        PolicyKey:     PolicyMetricDelete,
        OldProjection: oldProj,
    })
    // ...
}
```


## 3. 项目内对象活动场景目录


以下所有操作写入 `meta.activity_log`。每条记录标注 `action_type`（基础动作），细语义通过 `detail.changes/extra` 表达。注意：表中的 `online/release/stop` 等列在"场景"列的只是业务语义说明，不是额外落库字段。

### 3.0 现有日志系统归并分析

AB、Metric、MA Campaign 三套系统各自维护内部操作记录，下面是它们是否应归并到统一活动日志的分析：

| 系统 | 现存存储 | 现状评估 | 归并策略 | 理由 |
| ----- | ---------- | --------- | ---------- | ------- |
| **AB** | `details.operation_records` JSONB 数组，嵌在 `ab_feature_flag` 行内 | **差**——操作记录埋在深层 JSONB 里，无法独立查询和索引 | **新操作走 activity_log，旧历史全量迁移** | 现存存储严重拖累可查性，而 AB 操作是活动日志第一排障需求源；状态流转映射为独立 action_type（online/release/…），conflict resolution 通过 source=internal 表达 |
| **Metric** | `meta.metric_define_history` 独立表 | **好**——表结构简单，已独立可查，只记录 define 字段变更，目的单一 | **新操作走 activity_log，旧历史不迁移** | 现存表已够用，迁移到 activity_log 反而因投影模型只保留 define 的 changes before/after 而非全文，fidelity 未必更高；迁移收益低，不值得为此承担映射成本 |
| **MA Campaign** | `ma_operation_log` 独立表 | **中**——结构完整，可独立查询，但 schema 与活动模型不同，无法串查 | **新操作走 activity_log，旧历史不迁移** | 新写入用独立 action_type（launch/pause/resume/finish）表达状态流转；旧 `ma_operation_log` 已是独立可查表，迁移收益抵不上映射成本和 fidelity 损失 |

**共性前提**：活动日志模型（action_type + changes[] + extra + source）对三者均可行，覆盖所有操作语义。分歧只在"历史数据是否迁移"。

#### 活动日志的定位差异

活动日志与业务自有日志的本质区别在于**完整度**：

| 维度 | 业务自有日志 | 活动日志 |
|------|------------|---------|
| 操作覆盖 | 单一场景（Metric 只记 define update，Campaign 只记状态跳转） | **全生命周期**（每个对象的 create/update/delete，面向排障的完整时序） |
| 字段覆盖 | 只记录业务关心的字段 | **投影声明了什么就记什么**，字段有变更就在 `changes[]` 中体现 |
| 快照 | 无（删除后查不到对象名等上下文） | `item_name` / `operator_name` 写入时快照，删除后仍可追溯 |
| 操作人 | 可有可无（类型/可空性不一致） | `operator_id` + `operator_name` 双快照，系统操作兜底填 0 |
| 关联性 | 孤岛（不能跨对象查"前后发生了什么"） | `correlation_id` 串联跨对象操作（如 CopyDashboard 同时影响 dashboard + charts） |

业务日志不是"活动日志的弱化版"，它们**根本不打算回答"这个对象一生发生了什么"**（Metric 的表名叫 `metric_define_history` 不叫 `metric_activity_log`）。活动日志填补的是**对象全生命周期**这个整个缺失的维度，每个业务系统自己的日志不会也没动力去覆盖它。

#### 活动记录完整，但展示可以窄

活动日志记录完整不等于业务页面必须展示完整。**写端由活动模块控制（完整），读端由业务控制（按需）**：

- **查询条件已可筛选**：`ListByQuery` 支持 `item_type + item_id + action_type` 等条件，业务只查自己需要的部分
- **业务层做展示过滤**：业务 service 拿到 `ListByQuery` 返回的完整 `detail` 后，只渲染自己关心的字段和场景。例如 AB 历史页只展示该 flag 的操作记录（通过 `item_type + item_id`），不需要过滤其它对象的数据
- **AB / Metric / Campaign 的既有页面数据源切换到 activity_log 后，展示范围不变**：它们各自通过 `item_type + item_id` 查询，天然隔离，不会看到对方的数据
- **额外的展示过滤策略**：如果一个业务不想展示所有 action_type（如某些 update 是内部噪音），可以在业务 service 中后过滤，activity 模块不负责展示语义

**结论**：activity 模块提供完整记录 + 基础筛选（item_type/item_id/action_type），不提供字段级预过滤。业务层在查询结果上做展示决策。

### 3.1 CHART

| 场景 | action_type | detail.changes[] 最小集合 | extra / 备注 |
|------|------------|---------------------------|--------------|
| 创建 | `create` | `name`, `type`, `query_type`, `api_request`, `config`, `version` | 含初始字段；`config` 需投影控制 |
| 更新 | `update` | 仅变更字段的 before/after | 若只有噪音字段变化则跳过 |
| 删除 | `delete` | 至少 `name` 快照 | `snapshot.version`, `snapshot.dashboard_ids`；读 DB 获取删除前快照 |
| 批量删除 | `delete` × N | 同上，每条一行 | `extra.batch_id`, `extra.batch_index`；`BatchWriteLog` 共享事务 |
| 复制 | `copy` | 可为空 | `extra.source_item_id`, `extra.source_item_name`, `extra.target_name` |

### 3.2 DASHBOARD

| 场景 | action_type | detail.changes[] 最小集合 | extra / 备注 |
|------|------------|---------------------------|--------------|
| 创建 | `create` | `name`, `description`, `version` | 若含初始 chart 则记 `chart_ids` |
| 更新（含 chart/layout） | `update` | `name`, `description`, `chart_ids`, `layout_overrides` 中有变更的字段 | `chart_ids` diff 需处理空集合、重复 ID |
| 仅更新 meta | `update` | `name` 或 `description` 的 before/after | `PatchDashboardMeta` |
| 仅更新 layout | `update` | `layout_overrides` 变更 | `SetDashboardChartLayouts` |
| 添加 Chart | `update` | `chart_ids` before/after | `extra.added_chart_ids`；记在 Dashboard 活动中 |
| 移除 Chart | `update` | `chart_ids` before/after | `extra.removed_chart_ids`；记在 Dashboard 活动中 |
| 删除 | `delete` | 至少 `name` 快照 | `snapshot.version`, `snapshot.chart_count` |
| 批量删除 | `delete` × N | 同上 | `extra.batch_id` |
| 复制 | `copy` | 可为空 | `extra.source_item_id`, `extra.copy_charts`；若 `copyCharts=true` 则每个 Chart 也产一条 |

### 3.3 COHORT

| 场景 | action_type | detail.changes[] 最小集合 | extra / 备注 |
|------|------------|---------------------------|--------------|
| 创建 | `create` | `name`, `description`, `rule_config`, `calc_mode`, `calc_time`, `cohort_version` | `extra.scheduler_job_id` |
| 更新（含规则） | `update` | 变更字段 before/after | 若调度参数变更则记 `extra.scheduler_job_updated` |
| 更新（仅调度） | `update` | `calc_mode`/`calc_time` before/after | `extra.scheduler_job_action: updated/created` |
| 删除 | `delete` | 至少 `name` 快照 | `snapshot.rule_summary`, `extra.scheduler_job_deleted`；读 DB 获取删除前快照 |
| 手动重算 | — | — | **不进入活动表**，是 create/update 内部副作用 |
| 定时重算 | — | — | **不进入活动表**，cron 调度回调，属系统运维日志 |

### 3.4 AB: EXPERIMENT / FEATURE_GATE / FEATURE_CONFIG

三者遵循相同模型，差异在变化字段列表和 extra。

| 场景 | action_type | detail.changes[] 最小集合 | extra / 备注 |
|------|------------|---------------------------|--------------|
| 创建 | `create` | `ffkey`, `name`, `status`, `enabled`, `traffic`, `version` | Experiment 额外 `extra.subject_id`, `extra.layer_id` |
| 更新配置 | `update` | 变更字段 before/after | 若 `details` 变更则仅投影摘要 |
| 状态变更：Debug | `debug` | `status`, `enabled` before/after | |
| 状态变更：Online | `online` | `status`, `enabled` before/after | 触发冲突解决时补 `extra.conflict_resolution` |
| 状态变更：Offline | `offline` | `status`, `enabled` before/after | |
| Release | `release` | `status`, `release_plan` before/after | `extra.release_scope` |
| 删除 | `delete` | `status` before/after, 至少 `name` 快照 | `extra.buckets_released`, `extra.references_removed` |
| 复制 | `copy` | 可为空 | `extra.source_item_id`, `extra.source_ffkey` |
| 内部下线（冲突解决） | `offline` | `status`, `enabled` before/after | `source=internal`；`extra.reason: conflict_resolution`, `extra.conflict_ffkey` |
| 内部删除（冲突解决） | `delete` | `status` before/after, 至少 `name` 快照 | `source=internal`；同上 |

FEATURE_CONFIG 额外：
- **变体变更** → `update`，`changes[]` 含变体字段 before/after，`extra.change_kind = "variant_change"`，`extra.changed_variant_keys`

### 3.5 METRIC

| 场景 | action_type | detail.changes[] 最小集合 | extra / 备注 |
|------|------------|---------------------------|--------------|
| 创建 | `create` | `name`, `description`, `define`, `precision` | `define` 是核心字段 |
| 更新 | `update` | 变更字段 before/after | 若仅 `define` 变更则 `changes=[{field:"define",...}]` |
| 删除 | `delete` | 至少 `name` 快照 | `snapshot.define_summary` |

### 3.6 TRACKED_EVENT / VIRTUAL_EVENT / EVENT_PROPERTY / USER_PROPERTY / VIRTUAL_PROPERTY

所有元数据对象遵循统一模式，差异在 `changes[]` 字段列表：

| 场景 | action_type | detail.changes[] 最小集合 | extra / 备注 |
|------|------------|---------------------------|--------------|
| 创建 | `create` | `name`, `display_name`, `description`, 核心配置字段摘要 | 事件/属性各自字段列表不同 |
| 更新 | `update` | 变更字段 before/after | 含敏感值字段统一掩盖 |
| 删除 | `delete` | 至少 `name` 快照 | `snapshot.display_name`, `snapshot.category/data_type` 等 |

### 3.7 PIPELINE

| 场景 | action_type | detail.changes[] 最小集合 | extra / 备注 |
|------|------------|---------------------------|--------------|
| 创建 | `create` | `name`, `type`, `pipeline_type`, `data_type` | `extra.work`, `extra.data_source_id` |
| 更新 | `update` | 变更字段 before/after | |
| 删除 | `delete` | 至少 `name` 快照 | `snapshot.pipeline_type`, `snapshot.work`；软删除 |
| 停止 | `stop` | `exec_status` before/after | `extra.stop_reason` |

**不进入 `activity_log`**：
- Pipeline Process（系统级执行，已有 `exec_info` / `batch_export_run` 跟踪）
- Pipeline callback（AB target 状态同步，属基础设施层状态变更）

### 3.8 CAMPAIGN (MA)

Campaign 现有 `ma_operation_log` 记录状态变更。纳入统一活动模型后，新操作走 `meta.activity_log`，旧历史走迁移。

| 场景 | action_type | detail.changes[] 最小集合 | extra / 备注 |
|------|------------|---------------------------|--------------|
| 创建 | `create` | `name`, `channel`, `trigger_type`, `status` | `extra.audience_type`, `extra.goal_type` |
| 更新配置 | `update` | 变更字段 before/after | |
| 状态变更：Launch | `launch` | `status` before/after | |
| 状态变更：Pause | `pause` | `status` before/after | |
| 状态变更：Resume | `resume` | `status` before/after | |
| 状态变更：Finish | `finish` | `status` before/after | |
| 删除 | `delete` | 至少 `name` 快照 | `snapshot` 记录触发类型、渠道等关键配置 |

**不进入 `activity_log`**：
- MA Job 调度执行（cron 回调，属系统运维）
- Audience 重算（系统自动，非人工操作）

**历史迁移**：
- 源：`ma_operation_log`（campaign 状态变更历史）
- 映射：`create` → `create`，`update` → `update`，`launch/pause/resume/finish` → 对应同名 action_type
- 幂等去重键：`ma_operation_log` + `campaign_id` + `operation_type` + `operated_at`

### 3.9 明确不记录的操作

| 操作 | 原因 | 替代落点 |
|------|------|---------|
| `asset_behavior` 的 VIEW/MODIFY/DELIVER | 分析/热度用途，非活动语义 | 保持现有表 |
| Cohort 定时重算/清理 | 系统自动运维 | scheduler 日志 |
| AB target pipeline 状态同步 | 基础设施层变更 | V1 不做 |
| AB 调度报告任务创建/停止 | 已在 Experiment 状态变更的 extra 引用 | 不单独成行 |
| Asset 收藏（Add/Remove） | 轻量交互，排障价值低 | 保持现有表 |
| Asset 权限变更 | V1 先不做 | 后续可扩展 |
| 项目成员增删/角色变更 | global item 活动域 | `global.activity_log` |
| 组织成员管理 | global item 活动域 | `global.activity_log` |

---


以下操作写入 `global.activity_log`。

### 3.10 V1 item_type 与 action_type

| item_type | create | update | delete | 说明 |
|-----------|--------|--------|--------|------|
| `organization` | ✓ | ✓ | ✓ | 初始化、信息修改/配置变更、归档 |
| `org_member` | ✓ | ✓ | ✓ | 添加成员、级别/主管变更、移除；`create` vs `update` 通过先读已有状态判定 |
| `project` | ✓ | ✓ | ✓ | 创建、信息修改/配置变更、删除 |
| `project_member` | ✓ | ✓ | ✓ | 加入项目、角色变更、移出项目 |
| `account_api_token` | ✓ | ✓ | ✓ | 创建、更新 label/scope/expires_at/启用禁用、软删除；refresh 记两条（新 create + 旧 update） |

**V1 不做**：邀请创建/发送/撤回（接受邀请后生效操作写 `activity_log`）、预设角色变更（极低频）、组织级联操作子项（顶层 `delete ORGANIZATION` 已记录）。

### 3.11 Global item 接入要点

每个 global item 接入必须先回答 4 个问题：

| 问题 | 规则 |
|------|------|
| `item_type` 是什么？ | 用业务对象名，不用动作名（如 `project_member`，不是 `ADD_PROJECT_MEMBER`） |
| `item_id` 指向谁？ | 成员类用 `account_id`，组织类用 `org_id`，项目类用 `project_id`，token 类用 token id；复合身份放 `detail.extra` |
| 是否需要批量写？ | 批量用 `BatchWriteLog`，同批共享 `correlation_id` |
| 策略是什么？ | 用 `PolicyKey` 注册，不在调用处传策略 |

**操作人解析**：

| 场景 | 操作人来源 |
|------|-----------|
| Web 用户操作 | `pvctx.Aid(ctx)` / `pvctx.Aname(ctx)` |
| 注册时自动创建组织 | 注册用户本人 |
| Account API Token 管理 | token owner（系统初始化 token 继承 `accountID`） |
| 系统同步 / 回填 | 优先继承触发任务的账号 |

### 3.12 组织成员（4 项）

item_type = `"org_member"`，item_id = `account_id`。

| action_type | 接入位置 | 触发条件 | item_name | detail 最小形态 |
|------------|---------|---------|-----------|----------------|
| `create` | `Upsert` | 成员不存在 | `display_name` | `{"level":"...","role_ids":[...]}` |
| `update` | `BatchUpdateLevel` | level 实际变更 | `display_name` | `{"old_level":"...","new_level":"..."}` |
| `update` | `BatchReplaceSupervisor` | supervisor 集合变更 | `display_name` | `{"change_kind":"supervisor_added"}` 或 `"supervisor_removed"` |
| `delete` | `DeleteByOrgAndAccounts` | 成员被软删除 | `display_name` | `{}` |

**注意**：
- `Upsert` 先读已有状态，无变更不记
- `DeleteByOrgAndAccounts` 级联删除所有项目 membership，只记一条 `ORG_MEMBER delete`
- **Org member 与 Project member 不重复**：`create PROJECT_MEMBER` 是独立的权限授予操作

### 3.13 项目成员（3 项）

item_type = `"project_member"`，item_id = `account_id`。

| action_type | 接入位置 | 触发条件 | item_name | detail 最小形态 |
|------------|---------|---------|-----------|----------------|
| `create` | `BatchUpsert` | 成员在项目中不存在 | `display_name` | `{"roles":[...]}` |
| `update` | `BatchUpdateRoles` | 角色实际变更 | `display_name` | `{"old_roles":[...],"new_roles":[...]}` |
| `delete` | `BatchDeleteByProjectAndAccounts` | 成员被移出项目 | `display_name` | `{}` |

`UpdateAccountProjectAuths`（全量同步跨项目授权）：视作一条 `action_type=update, item_type=ORG_MEMBER`，detail 含变更摘要（涉及项目数、成员数）。

### 3.14 组织/项目生命周期（6 项）

| action_type | item_type | item_id | 接入位置 | item_name | detail 最小形态 |
|------------|-----------|---------|---------|-----------|----------------|
| `create` | `organization` | org_id | `Init` | org name | `{"creator_id":<id>}` |
| `update` | `organization` | org_id | 信息/配置变更入口 | org name | 按 diff 记录变更字段 |
| `delete` | `organization` | org_id | `Archive` | org name | `{"status_before":"active","status_after":"archived"}` |
| `create` | `project` | project_id | `Create` | project name | `{"org_id":<id>}` |
| `update` | `project` | project_id | 信息/配置变更入口 | project name | 按 diff 记录变更字段 |
| `delete` | `project` | project_id | `Archive` | project name | `{"org_id":<id>}` |

### 3.15 Account API Token（5 项）

item_type = `"account_api_token"`，item_id = token id。

接入点对应 controller → service → DAO 三层，由业务方按实际代码结构确定。

| action_type | 接入位置 | 触发条件 | item_name | detail 最小形态 |
|------------|---------|---------|-----------|----------------|
| `create` | `CreateTokenWithExpiry` / `CreateTokenNoQuotaWithExpiry` | 创建成功 | token label | `changes` 记录 `label/status/scopes/expires_at` 初始值 |
| `update` | `UpdateTokenWithExpiry` | label/scopes/expires_at 实际变更 | token label | 变更字段 before/after |
| `update` | `EnableToken` / `DisableToken` / `DisableByRawToken` | status 实际变更 | token label | `changes: [{"field":"status","before":"ACTIVE","after":"DISABLED"}]` |
| `update` | `RefreshToken` | 新 token 创建并禁旧 token | new token label | `extra.refresh_from_token_id`；与旧 token status update 共享 `correlation_id` |
| `delete` | `DeleteToken` | 软删除 | token label | `snapshot` 记录 `status/scopes/expires_at/token_hint` |

敏感字段规则：token 原文永不进入 detail；`token_hash` drop；`token_hint` 可记录但不可反推原 token。

---


## 4. 历史迁移


### 4.0 历史源原始记录格式

三种现有日志的原始结构、字段映射和幂等去重键。Metric 和 Campaign 不迁移，此处列出供扩展参考。

#### AB `details.operation_records`

| 维度 | 内容 |
|------------|--------|
| 存储位置 | `ab_feature_flag.details` TEXT 列（存 JSON），`operation_records` 数组 |
| 记录方式 | 每次操作 append 一条到 JSON 数组；同 flag 的所有历史记录在同一行内 |
| `typ` 枚举 | `1=gate` / `2=config` / `3=exp` / `4=layer` / `5=holdout`（前三种纳入活动日志） |

**原始格式：**

```json
{
  "action": "UPDATE",
  "name": "someone@example.com",
  "timestamp": 1680000000,
  "old_value": "...",
  "new_value": "..."
}
```

**字段映射：**

| activity_log 字段 | 来源 | 说明 |
|-------------------|------|------|
| `item_type` | `f.typ` → `experiment/gate/config` | SMALLINT 映射（1=gate, 2=config, 3=exp；4=layer, 5=holdout 不接入） |
| `item_id` | `f.id` | flag 主键 |
| `item_name` | `f.name` | flag 名称快照 |
| `action_type` | `record.action` | 已定 action 映射函数 |
| `operator_id` | 不包含 | 旧记录无 operator_id；`name` 字段是邮箱字符串，迁移时通过 account 表反查（查不到填 0） |
| `operator_name` | `record.name` | 直接使用 |
| `source` | `backfill` | 恒定为 backfill |
| `detail.changes` | `record.old_value` / `record.new_value` | 通过 `PreBuiltChanges` 直接构造变化项 |
| `occurred_at` | `record.timestamp` | Unix 时间戳转换 |

**幂等去重键：** `legacy_source("ab_feature_flag.details.operation_records")` + `item_type` + `item_id` + `legacy_action_type` + `operator_name` + `occurred_at`

**注意事项：**
- 不存在 `operator_id`：旧历史没有数值化的操作人 ID，迁移时需 `name` → account 表反查，查不到的填 0
- `old_value` / `new_value` 可能有空字符串：一条记录可能只有新值没有旧值（如 CREATE 场景），映射时 ChangesBetween 的 one side 为 nil
- 缺少原始字段级别的 before/after：只有 value 粒度的变更描述（非结构化），所以即使迁到活动表也只能得到近似 detail，不可能还原出精确的字段级 changes
- 同 flag 的旧记录分散在 JSONB 数组中，迁移前按 feature_flag_id + action + timestamp 排序以保持时序

---

#### Metric `meta.metric_define_history`

| 维度 | 内容 |
|------------|--------|
| 存储位置 | `meta.metric_define_history` 独立表（当前**不迁移**，仅存档参考） |
| 记录方式 | 每次 metric define 变更 insert 一行 |

**表结构（来自 `meta.sql`，已核对建表语句）：**

| 列 | 类型 | 说明 |
|---|------|------|
| `id` | SERIAL | PK |
| `metric_id` | INTEGER NOT NULL | 关联的 metric |
| `old_define` | TEXT | 变更前的 define 全文（可空） |
| `new_define` | TEXT NOT NULL | 变更后的 define 全文 |
| `updated_by` | INTEGER NOT NULL DEFAULT 0 | 操作人 ID |
| `created_at` | TIMESTAMPTZ NOT NULL DEFAULT NOW() | 记录时间 |
| `updated_at` | TIMESTAMPTZ NOT NULL DEFAULT NOW() | 更新时间（有触发器自动更新） |

**如果迁移，映射方案：**

| activity_log 字段 | 来源 |
|-------------------|------|
| `item_type` | `"metric"`（常量） |
| `item_id` | `metric_id` |
| `item_name` | `m.name`（JOIN metric 表） |
| `action_type` | `"update"`（恒定为 update） |
| `operator_id` | `updated_by` |
| `source` | `backfill` |
| `detail.changes` | `[{field:"define", before:old_define, after:new_define}]` |
| `occurred_at` | `created_at` |

**幂等去重键（如迁移）：** `legacy_source("meta.metric_define_history")` + `item_type` + `item_id` + `operator_id` + `occurred_at`

**不迁移的理由：**
- 记录单一（只有 define 字段变更），现存表已可独立查询
- 迁移后 `changes.before/after` 存储的是 define 字段值，和原表存的是同一份数据，无查询增益
- 仅存量数据，无后续增长（新操作走 activity_log）

---

#### Campaign `ma_operation_log`

| 维度 | 内容 |
|------------|--------|
| 存储位置 | `ma_operation_log` 独立表（当前**不迁移**，仅存档参考） |
| 记录方式 | 每次 campaign 状态变更 insert 一行 |

**表结构（来自 `meta.sql`，已核对建表语句）：**

| 列 | 类型 | 说明 |
|---|------|------|
| `id` | SERIAL | PK |
| `campaign_id` | INTEGER NOT NULL | 关联的 campaign |
| `operation_type` | VARCHAR(32) NOT NULL | `create` / `update` / `launch` / `pause` / `resume` / `auto_finish` |
| `old_status` | VARCHAR(16) | 变更前状态（可空） |
| `new_status` | VARCHAR(16) | 变更后状态（可空） |
| `operated_by` | BIGINT | 操作人（可空，`0` 为系统操作） |
| `operated_at` | TIMESTAMPTZ NOT NULL DEFAULT NOW() | 操作时间 |
| `detail` | TEXT | 附加描述（可空） |

索引：`idx_ma_oplog_campaign ON (campaign_id, operated_at)`

**如果迁移，映射方案：**

| activity_log 字段 | 来源 |
|-------------------|------|
| `item_type` | `"campaign"`（常量） |
| `item_id` | `campaign_id` |
| `action_type` | `operation_type` 直接映射（create/update/launch/pause/resume/finish 均与 action_type 同名；auto_finish 映射为 finish） |
| `operator_id` | `operated_by`（空值填 0） |
| `source` | `backfill` |
| `occurred_at` | `operated_at` |

**幂等去重键（如迁移）：** `legacy_source("ma_operation_log")` + `item_type("campaign")` + `item_id` + `operation_type` + `operated_at`

**不迁移的理由：**
- 现存表已经是独立完整的状态变更日志
- 旧历史在原表查询效率不亚于在新活动表查询
- 与其他对象的关联查询（"在 campaign launch 前后实验是否有变更"）可以通过 correlation_id 实现，但旧历史不存在 correlation_id，迁移也无法补齐

---

### 4.1 迁移原则

1. 一次性复制旧历史
2. 迁移后查询只读新活动表
3. 升级后新写入只写新活动表
4. 旧字段或旧表保留，不做双写

### 4.2 迁移源

| 历史源 | 迁移到新项目活动 | 原因 |
|--------|----------------|------|
| `ab_feature_flag.details.operation_records` | **是** | AB 现存存储最差，操作数据嵌在 JSONB 中无法独立查询，活动模型是明确改进 |
| `meta.metric_define_history` | **否** | 现存表结构简单、独立可查，迁移 fidelity 未必更高；迁移收益低，不值得承担映射成本 |
| `ma_operation_log` | **否** | 现存表已是独立可查的规范日志，迁移收益抵不上 fidelity 损失；新操作已走 activity_log |
| `meta.asset_behavior` | **否** | 只有 VIEW 有效，不具备可靠活动语义 |
| `global.op_operation_log` | **否** | 作用域不同，继续留在 OP 操作记录链路 |

附录：Metric 和 Campaign 现存日志**不迁移**的完整分析见 §3.0。

### 4.3 映射规则

| 源操作 | → action_type |
|--------|-------------|
| AB `CREATE` | `create` |
| AB `UPDATE`, `VARIANT_CHANGE` | `update`（配置变更） |
| AB `DEBUG` | `debug` |
| AB `ONLINE` | `online` |
| AB `OFFLINE` | `offline` |
| AB `RELEASE` | `release` |
| AB `COPY` | `copy` |
| AB `DELETE` | `delete` |

对历史 AB 记录缺少 before/after 的场景：
- 允许 `changes` 为空
- 必须保留 `name`、`source = "backfill"`、`extra.legacy_source`
- `operator_name` 尽量回填，查不到允许空字符串
- `occurred_at` 直接回填原始操作时间

**幂等去重键**：`legacy_source` + `item_type` + `item_id` + `legacy_action_type` + `operator_id` + `occurred_at`

### 4.4 Chart/Dashboard/Cohort/Event/Property

该类对象**当前没有可靠旧操作历史源**，不从 `asset_behavior` 或访问记录伪造历史。上线后从新表开始连续记账。

### 4.5 迁移数据流

```mermaid
flowchart LR
    legacy["旧历史源<br/>AB operation_records"] --> scan["批量扫描"]
    scan --> map["映射 ItemType / ActionType / Detail"]
    map --> dedupe["生成幂等去重键"]
    dedupe --> insert["BatchWriteLog<br/>source=backfill"]
    insert --> table["meta.activity_log"]
    table --> verify["抽样查询 + 数量校验"]
```

迁移闭环要求：

- 回填必须走 activity 模块的 serializer，不允许脚本直接拼 `detail_payload` SQL。
- `occurred_at` 使用旧历史事件时间，`created_at` 使用回填入库时间。
- 每批回填记录 `legacy_source`，便于问题排查和回滚识别。
- 迁移完成后，AB 的历史查询入口只读新活动表；旧字段保留但不再作为在线查询源。
- 验证至少包含总量对账、重复执行幂等、抽样 detail 可读、按对象查询能串起旧历史四项。

### 4.6 具体迁移脚本示例

#### AB operation_records 提取

AB 历史存储在 `ab_feature_flag.details` 的 JSONB `operation_records` 数组中。每条记录的原始结构：

```json
{
  "action": "UPDATE",
  "name": "someone@example.com",
  "timestamp": 1680000000,
  "old_value": "...",
  "new_value": "..."
}
```

**SQL 提取**（分批扫描，避免全表锁）：

```sql
-- 每批 500 条
SELECT
    f.id              AS feature_flag_id,
    f.name            AS flag_name,
    f.typ             AS flag_type,       -- 1=gate, 2=config, 3=exp
    jsonb_array_elements(f.details->'operation_records') AS record
FROM ab_feature_flag f
WHERE f.id > :last_id
  AND f.details->'operation_records' IS NOT NULL
  AND jsonb_array_length(f.details->'operation_records') > 0
ORDER BY f.id
LIMIT 500;
```

**Go 映射伪代码**：

```go
type ABOperationRecord struct {
    Action    string `json:"action"`
    Name      string `json:"name"`      // operator_name
    Timestamp int64  `json:"timestamp"` // occurred_at (unix)
    OldValue  string `json:"old_value"`
    NewValue  string `json:"new_value"`
}

func mapABRecordToActivity(flagID int64, flagName, flagType string, rec ABOperationRecord) (*activity.WriteInput, error) {
    actionType := mapABAction(rec.Action)

    var changes []activity.Change
    if rec.OldValue != "" || rec.NewValue != "" {
        changes, _ = activity.ChangesBetween(
            map[string]any{"value": rec.OldValue},
            map[string]any{"value": rec.NewValue},
        )
    }

    return &activity.WriteInput{
        ItemType:         itemTypeForABFlag(flagType),
        ItemID:           flagID,
        ItemName:         flagName,
        ActionType:       actionType,
        PolicyKey:        string(activity.PolicyActivityBackfill),
        PreBuiltChanges:  changes,                        // 迁移走快捷路径
        Extra:            map[string]any{
            "legacy_source":      "ab_feature_flag.details.operation_records",
            "legacy_flag_type":   flagType,
            "legacy_action_type": rec.Action,
        },
        OccurredAt:       time.Unix(rec.Timestamp, 0),
    }, nil
}

func mapABAction(action string) string {
    switch action {
    case "CREATE":         return "create"
    case "UPDATE", "VARIANT_CHANGE": return "update"
    case "DEBUG":          return "debug"
    case "ONLINE":         return "online"
    case "OFFLINE":        return "offline"
    case "RELEASE":        return "release"
    case "COPY":           return "copy"
    case "DELETE":         return "delete"
    default:               return "update" // fallback
    }
}

func itemTypeForABFlag(flagType int) string {
    switch flagType {
    case 1:   return "feature_gate"
    case 2:   return "feature_config"
    case 3:   return "experiment"
    default:  return "experiment"
    }
}
```

#### 批量回填与幂等控制

```go
func BackfillABHistory(ctx context.Context, batchSize int) (int, error) {
    var lastID int64
    total := 0
    for {
        rows, err := dao.ScanABOperationRecords(ctx, lastID, batchSize)
        if err != nil || len(rows) == 0 {
            break
        }
        lastID = rows[len(rows)-1].FeatureFlagID

        var inputs []activity.WriteInput
        for _, row := range rows {
            input := mapABRecordToActivity(row.FlagID, row.FlagName, row.FlagType, row.Record)
            inputs = append(inputs, *input)
        }

        // 使用 BatchWrite 走统一 SerializeDetail → DAO insert
        // 幂等性由 DB 层 unique index 或写入前按去重键去重保障
        err = activitySvc.BatchWriteLog(ctx, inputs)
        if err != nil {
            return total, fmt.Errorf("batch %d: %w", lastID, err)
        }
        total += len(rows)
    }
    return total, nil
}
```

#### 迁移验证

```sql
-- 总量对账
SELECT COUNT(*) FROM meta.activity_log WHERE source = 'backfill' AND detail_payload LIKE '%"legacy_source":"ab_feature_flag%';

-- 按对象抽样
SELECT * FROM meta.activity_log
WHERE item_type = 'FEATURE_GATE' AND item_id = 15
ORDER BY occurred_at DESC;

-- 幂等校验（重复执行去重键不应该增加记录）
SELECT item_type, item_id, action_type, operator_id, occurred_at, COUNT(*)
FROM meta.activity_log
WHERE source = 'backfill'
GROUP BY item_type, item_id, action_type, operator_id, occurred_at
HAVING COUNT(*) > 1;
```

#### 关闭旧查询入口

迁移完成后，在所有历史查询入口处切换数据源：

```diff
- list := dao.GetABOperationRecords(ctx, flagID)
+ list := activitySvc.ListByQuery(ctx, activity.Query{
+     ItemType: itemTypeForABFlag(flag.Typ),
+     ItemID:   flag.ID,
+ })
```

---


## 5. 接入 SOP


### 5.1 接入 SOP

项目对象接入必须按以下步骤，不能只在某个 service 里临时拼一条 JSON：

| 步骤 | 要做什么 | 产物 |
|------|----------|------|
| 1 | 确认对象身份 | `ItemType`、`item_id`、`item_name` 规则 |
| 2 | 确认动作语义 | 基础 `action_type`；必要时走扩展 action_type 注册流程 |
| 3 | 注册接入场景 | `PolicyKey` |
| 4 | 构造 Detail | 手写 `*Detail` 或使用 `ChangesBetween` 生成 `changes` |
| 5 | 构造 Detail（投影 + 计算 Change 列表） | 固定 `ItemType/ActionType/PolicyKey`，调用方只传业务快照 |
| 6 | 接业务事务 | create 用创建后对象；update/delete 在修改前读旧快照；批量用 `BatchWrite*` |
| 7 | 补验证 | 成功写入、无变更不写、失败策略、查询返回 detail/total |

最小代码形态：

```go
// 项目 item — 传原始投影，ActivityService 做 diff
err = activitySvc.WriteLog(ctx, activity.WriteInput{
    ItemType:      activity.ItemTypeChart,
    ItemID:        chart.ID,
    ItemName:      chart.Name,
    ActionType:    activity.ActionTypeUpdate,
    PolicyKey:     string(activity.PolicyChartUpdate),
    OldProjection: oldProj,                            // 原始值，不脱敏
    NewProjection: newProj,                            // 原始值，不脱敏
    Extra:         map[string]any{"version": chart.Version},
})

// Global item — 手写 changes（无投影函数，少量字段）
err = activitySvc.WriteLog(ctx, activity.WriteInput{
    ItemType:   activity.ItemTypeOrgMember,
    ItemID:     accountID,
    ItemName:   member.DisplayName,
    ActionType: activity.ActionTypeCreate,
    PolicyKey:  string(activity.PolicyOrgMemberCreate),
    PreBuiltChanges: []activity.Change{                // 手写 changes
        {Field: "level", Action: activity.ChangeCreated, After: "member"},
        {Field: "role_ids", Action: activity.ChangeCreated, After: []int64{1, 2}},
    },
    Extra: map[string]any{"org_id": orgID},
})

// Global item — 有敏感字段，声明 MaskRules
err = activitySvc.WriteLog(ctx, activity.WriteInput{
    ItemType:      activity.ItemTypeAccountAPIToken,
    ItemID:        token.ID,
    ItemName:      token.Label,
    ActionType:    activity.ActionTypeUpdate,
    PolicyKey:     string(activity.PolicyAccountAPITokenUpdate),
    OldProjection: oldProj,
    NewProjection: newProj,
    MaskRules:     activity.MaskRules{
        "token_hash": activity.MaskValue("***"),
    },
})
```

### 5.2 主要接入点

| 对象域 | 主要接入方法 |
|--------|-------------|
| Chart | Create / Update / Delete / BatchDelete / Copy |
| Dashboard | Create / Update / PatchMeta / SetLayouts / Delete / BatchDelete / Copy / AddCharts / RemoveCharts |
| Cohort | Create / Update / Delete |
| AB | Create / Update / StatusRelease / StatusOnline / Stop / Copy |
| Metric | Create / Update / Delete |
| Event / Property | Create / Update / Delete |
| Pipeline | Create / Update / Delete / Stop |
| Campaign (MA) | Create / Update / Delete / Launch / Pause / Resume / Finish |
| Org member | Upsert / BatchUpdateLevel / BatchReplaceSupervisor / BatchDelete |
| Project member | BatchUpsert / BatchUpdateRoles / BatchDelete |
| Org lifecycle | Init / Archive / 配置变更 |
| Project lifecycle | Create / 配置变更 / Archive |
| Account API Token | Create / Update / Enable / Disable / Refresh / Delete |

### 5.3 不依赖的基础设施

以下基础设施不能作为活动覆盖的判断标准：

| 基础设施 | 问题 |
|----------|------|
| `AssetOperator` | 只注册了 Chart / Dashboard，接口仅含 CRUD；覆盖不到 Cohort / AB / copy / status_change |
| `asset_behavior` | 主要是 view 行为，modify/delete/add 基本死代码，不是可靠活动系统 |
| AB 自带历史页 | 只能看 AB，且数据嵌在 JSONB 里 |
| Metric history 表 | 只覆盖 define 变更 |
| Pipeline `exec_info` | 仅系统执行日志，不记录谁做了 CRUD |

### 5.4 开发者体验

V1 需提供：

| 文档 | 内容 | 形式 |
|------|------|------|
| ActivityService 接入指南 | 调用契约、必填字段、detail 构建规范、常见错误 | Markdown |
| 对象类型接入模板 | 每个 ItemType 的写入调用示例 | Go 示例代码 |
| WritePolicy 选择指南 | blocking/best_effort 适用场景、决策树 | Markdown |
| Detail helper 使用说明 | `ChangesBetween` 调用方式、稳定投影约定、敏感字段禁入规则 | Markdown + 注释 |

测试辅助工具：

| 工具 | 说明 |
|------|------|
| `activitytest.MockService` | 内存实现 ActivityService，不依赖 DB |
| `activitytest.AssertLogWritten` | 断言某条活动记录已被写入 |
| `activitytest.AssertChangesContains` | 断言 detail.changes 包含特定字段变更 |

MockService 行为约定：
- `WriteLog` 默认返回 nil，调用方可配置预期 error（测试写入失败）
- `BatchWriteLog` 默认追加到内存切片，调用方可读取断言
- 不提供与真实 DAO 一致的 mock（那是集成测试的职责）

### 5.5 Write-only Feature Flag

部署时增加 write-only feature flag 保护，异常时可快速关闭活动写入而不影响业务逻辑。

---


## 6. 开发者工具

### 6.1 测试辅助工具

V1 需提供：

| 文档 | 内容 | 形式 |
|------|------|------|
| ActivityService 接入指南 | 调用契约、必填字段、detail 构建规范、常见错误 | Markdown |
| 对象类型接入模板 | 每个 ItemType 的写入调用示例 | Go 示例代码 |
| WritePolicy 选择指南 | blocking/best_effort 适用场景、决策树 | Markdown |
| Detail helper 使用说明 | `ChangesBetween` 调用方式、稳定投影约定、敏感字段禁入规则 | Markdown + 注释 |

测试辅助工具：

| 工具 | 说明 |
|------|------|
| `activitytest.MockService` | 内存实现 ActivityService，不依赖 DB |
| `activitytest.AssertLogWritten` | 断言某条活动记录已被写入 |
| `activitytest.AssertChangesContains` | 断言 detail.changes 包含特定字段变更 |

MockService 行为约定：
- `WriteLog` 默认返回 nil，调用方可配置预期 error（测试写入失败）
- `BatchWriteLog` 默认追加到内存切片，调用方可读取断言
- 不提供与真实 DAO 一致的 mock（那是集成测试的职责）

