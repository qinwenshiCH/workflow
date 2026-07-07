# 决策记录

## 2026-06-30

### 范围

- **复用已废弃 bit 位**：确认当前系统无自定义角色、预置角色位图为 `init()` 内存态不持久化到 DB，因此 bit 3/12/13（旧 `dashboard:download`、`discover:view`、`discover:favorite`）可以安全复用，无需保留墓碑位。PermissionCount = 21（3 字节），不做 24。
- **`marketing` → `ma` 命名**：与产品 MA 命名对齐，权限值为 `ma:view` / `ma:edit`。
- **`insight:download` 属于计算结果非原始数据**：insight 下载的是分析模型的计算结果（图表/PDF），非原始数据，因此下放给 ANALYST。原始数据下载（`cohort:download`、`sql:download`、`userlist:download`）仍是 MANAGER 独占。
- **Insight/SQL 不拆分 edit 权限**：`insight:query` 和 `sql:query` 隐含编辑能力——默认就是可编辑的查询，能 query 就能 create/edit。与其他模块（dashboard/cohort/ab）的 view+edit 配对模式不同，但符合产品实际行为。
- **`dashboard:download` 删除**：该权限目前无对应功能，前端虽有常量和下载按钮但实际未使用。本次重构直接删除，后续如需看板导出功能，用计算结果下载逻辑处理。
- **`audience:use` → `cohort:download` 语义变化**：bit 5 位不变，语义从"将分群用作分析筛选条件"变为"分群导出"。旧 `use` 行为由 `cohort:view` + `insight:query` 隐含覆盖，无需独立权限。无自定义角色，复用位图无风险。

### 位图布局

```
Bit 0:  project:config
Bit 2:  dashboard:view
Bit 1:  dashboard:edit     (was dashboard:upsert)
Bit 3:  userlist:query     (reuse dashboard:download)
Bit 12: userlist:download  (reuse discover:view)
Bit 6:  cohort:view        (was audience:view)
Bit 4:  cohort:edit        (was audience:upsert)
Bit 5:  cohort:download    (was audience:use)
Bit 7:  reserved (legacy)
Bit 8:  data:tracking
Bit 14: data:catalog:view  (was data:metadata:view)
Bit 15: data:catalog:edit  (was data:metadata:upsert)
Bit 9:  data:asset
Bit 11: ab:view
Bit 10: ab:edit            (was ab:upsert)
Bit 13: insight:query      (reuse discover:favorite)
Bit 18: insight:download   (new)
Bit 19: sql:query          (new)
Bit 20: sql:download       (new)
Bit 17: ma:view            (was marketing:view)
Bit 16: ma:edit            (was marketing:upsert)
```

PermissionCount = 21
