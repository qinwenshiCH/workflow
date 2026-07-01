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
