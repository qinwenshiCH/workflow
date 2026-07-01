# 待讨论议题

> 本文件只存放尚未锁定、会影响实现的议题。已锁定的约束不再反复展开。

## D-P4: TEXT detail_payload 是否启用应用层压缩

**状态**: 未锁定 | **已锁定约束**: `detail_payload` 使用 `TEXT`，不使用 PG `JSONB`

### 背景

当前已经确定：

- 表字段类型统一为 `TEXT`
- 服务层统一通过 `SerializeDetail` / `ParseDetail` 读写
- 查询接口返回解析后的 `detail`，不暴露 `detail_payload`
- 业务调用方不感知 codec，不直接读写压缩格式

尚未确定的是：`TEXT` 内部直接存可读 JSON，还是存带 codec marker 的压缩文本。

### 方案对比

| 维度 | TEXT + readable JSON | TEXT + compressed payload |
|------|----------------------|---------------------------|
| 查库排障 | 最好，SQL/psql 直接可读 | 较差，需要工具或服务层解压 |
| 存储体积 | 较大，依赖 PG TOAST | 更小，通常可降低 WAL、备份、复制体积 |
| 写入延迟 | 最低 | 增加压缩 CPU，预计小但需要实测 |
| 实现复杂度 | 最低 | 需要 codec marker、兼容测试、调试工具 |
| 未来演进 | 容易 | codec 升级需兼容旧数据 |
| 对 action_type 的压力 | 低，detail 可直接读 | 中，不能用扩展 action_type 替代 detail 可读性 |

### 推荐评估流程

上线前用样本数据跑一次容量测算，而不是在文档里拍死：

1. 抽取 Chart config、Dashboard layout、AB details、Metric define 等高风险 detail 样本。
2. 生成 create/update/delete/copy 的真实 envelope。
3. 统计单条 `detail_payload` 的 P50/P95/P99、压缩率、压缩/解压耗时。
4. 用预计日写入量和保留周期估算年度存储、WAL、备份影响。
5. 若 P95 小于 16KB 且年度增长可接受，优先 `TEXT + readable JSON`。
6. 若 P95/P99 大字段明显、年度增长或 WAL 压力不可接受，再启用 `TEXT + compressed payload`。

### 实现边界

无论是否压缩，代码必须保持同一个边界：

```go
payload, err := activity.SerializeDetail(detail)
detail, err := activity.ParseDetail(payload)
```

如果启用压缩，payload 需要自带 codec marker，例如：

```text
json:<raw-json>
lz4:<base64-lz4-json>
```

这样未来可以逐行兼容旧数据，不需要一次性 backfill。

### 当前倾向

当前倾向先做 `TEXT + readable JSON`，但文档不把它写成已锁定。最终以容量测算结果为准。

---

## D-P5: Global 聚合查询是否引入 activity_log_target 投影

**状态**: 未锁定 | **当前约束**: 暂不为该方向新增字段或新表

### 背景

`global.activity_log` 既要记录 Organization / Project / Member / Account API Token 等 web global schema item，又可能被问到“查看某个组织或项目下所有成员变更”。如果主记录只表达 `item_type + item_id`，它天然擅长查单个 item 的历史，不天然擅长查某个 container 下的所有相关 item 历史。

### 候选方案

| 方案 | 优点 | 风险 |
|------|------|------|
| 主表增加 scope 字段 | 查询直接、实现简单 | `activity_log` 主表会携带 org/project/account 等业务维度，未来维度继续膨胀 |
| 仅 `item_type + item_id` | 主表最干净，身份模型统一 | 无法高效查询组织/项目下所有成员变更，除非 join 业务表或解析 detail TEXT |
| 增加 `activity_log_target` 投影表 | 主表保持事件语义，查询视角独立扩展 | 需要维护额外写入和一致性，V1 实施成本更高 |

`activity_log_target` 的可能形态：

```text
activity_log_id
target_type
target_id
target_role  # item / container / subject
item_type
occurred_at
```

例如组织成员变更可投影为：

```text
item       ORG_MEMBER    organization_member.id
container  ORGANIZATION  org_id
subject    ACCOUNT       member_account_id
```

### 当前处理

该方向先保留为 discussion，不进入当前 DDL。若后续评审确认“组织/项目下所有成员变更记录”是 V1 必须高效支持的主路径，再重新决定是保留 scope 字段，还是改为 `activity_log_target` 查询投影。

---

## D-P6: 从 V1 主线移出的扩展能力

**状态**: 已裁剪出 V1 主实现 | **原则**: 先证明 activity log 闭环，再平台化

### 背景

当前方案一度把 registry、diff engine、redaction、PolicyKey、分区、保留策略、治理能力都放进主方案，阅读体验接近“审计平台”。评审后确认 V1 应收敛为最小闭环：写入、落库、查询、迁移。

### 暂不进入 V1 主实现

| 能力 | 暂缓原因 | 后续触发条件 |
|------|----------|--------------|
| 通用 redaction registry | 需要维护字段规则和敏感字段分类，容易变成平台工程 | 接入 integration / webhook / OAuth 等敏感配置对象 |
| 通用 diff engine | 不同业务结构差异大，过早抽象会增加接入成本 | 多个对象重复出现同一类字段投影逻辑 |
| 分区 / TTL / 保留策略 | 当前没有真实写入量和保留周期数据 | 活动表规模、备份或查询性能出现明确压力 |
| 审批 / 告警 / 可见性控制 | 属于治理产品，不是 activity log 底座 | 客户或内部流程明确要求 |
| Outbox / 异步写入 | V1 主诉求是立即可排障；异步会增加一致性和重试复杂度 | 高频写入路径对主事务延迟造成可测压力 |

### V1 保留的轻量替代

- `ActivityDetail` envelope 固定为 `changes / extra / snapshot`。
- `Detail helper` 可选使用，但业务 wrapper 可以显式构造 detail。
- `PolicyKey` 只做稳定场景名到返回行为的简单映射。
- 敏感字段默认不进入 detail；必须进入时由 wrapper 写已脱敏值，ActivityService 做字段名兜底拦截。
