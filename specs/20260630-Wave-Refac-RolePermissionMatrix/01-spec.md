# 功能规格：角色权限底座矩阵更新

**目录**: `20260630-Wave-Refac-RolePermissionMatrix`
**创建日期**: 2026-06-30
**状态**: Draft — 已通过 CEO Review
**输入**: 权限点命名规范化（统一为 `domain:action` 格式）、废弃权限清理、新增 userlist/insight/sql 权限、按新矩阵重新分配 VIEWER/ANALYST/MANAGER 角色能力。内部重构，无用户感知变化。

---

## 背景

SensorsWave 的权限系统经过多轮迭代，权限点的命名方式已经出现不一致：

| 问题 | 示例 | 影响 |
|------|------|------|
| 命名格式不统一 | `dashboard:upsert` 使用了 `upsert` 而不是 `edit` | 新开发者难以理解，缺乏一致的命名规范 |
| 过时领域术语 | `audience:*` → 已改名为 Cohort，但权限名未更新 | 与产品实际命名脱节，造成混乱 |
| 废弃权限残留 | `dashboard:download`、`discover:*` 已不再使用 | 污染位图空间，矩阵维护者困惑 |
| 缺少新权限 | userlist/insight/sql 等新功能需要对应的权限点 | 新功能上线后无法做细粒度权限控制 |

这些问题的根源是：权限点定义自系统早期建立以来，从未做过系统性清理。随着产品功能不断扩展，命名不一致和废弃权限累积到了需要统一治理的程度。

本次变更定位为**内部重构**，不涉及 DB schema 变更、API 契约变更或用户感知的变化。

---

## 价值定位

这是权限底座的一次系统性治理，不直接产生用户可见功能，但为后续权限相关功能扫清障碍：

1. **降低维护成本（P0）**：统一的 `domain:action` 命名规范使权限点的含义一目了然，新成员加入后不再需要猜测 `upsert`、`use` 等语义模糊的命名
2. **消除歧义（P0）**：清理废弃权限、对齐领域术语，避免"有权限点但实际上没用"或"名称和实际行为不符"的问题
3. **扩展准备（P1）**：新增 userlist/insight/sql 权限为后续功能上线提供权限控制基础

### 设计原则

1. **向后兼容优先**：位图布局变更遵守"旧位位置不变"原则；已废弃 bit 位（3/12/13）确认无存量数据依赖后安全复用给新权限；剩余新权限追加到高位（18-20）。预置角色位图为内存态，每次重启从代码重建，无 DB 迁移风险
2. **一次性清理到位**：不留旧常量别名，避免长期维护负担和累积技术债
3. **编译期安全保障**：利用 Go 编译检查确保所有旧常量引用被更新，不留运行时隐患

---

## 用户故事

### 用户故事 1 — 权限点命名统一 (P0)

作为**平台工程师 / 系统维护者**，当我阅读或新增权限检查代码时，我希望权限点的命名遵循一致规范（`domain:action`），这样不需要每次查阅文档理解 `upsert`、`use` 等模糊术语的实际含义。

**优先级理由**: 这是本次重构最直接的改进——统一命名降低日常开发和代码审查的认知负担。

**独立测试**: 检查所有 Perm 常量，验证命名格式均为 `domain:action`，不存在 `upsert`、`use` 等旧命名。

**验收场景**:

1. **Given** 代码库中存在旧命名 `PermDashboardUpsert`，**When** 重构完成，**Then** 该常量已被重命名为 `PermDashboardEdit`，且所有引用点同步更新
2. **Given** 代码库中存在旧命名 `PermAudienceUpsert`，**When** 重构完成，**Then** 该常量已被重命名为 `PermCohortEdit`
3. **Given** 代码库中存在旧命名 `PermMarketingUpsert`，**When** 重构完成，**Then** 该常量已被重命名为 `PermMAEdit`

---

### 用户故事 2 — 废弃权限清理 (P0)

作为**平台工程师 / 矩阵维护者**，当我管理角色权限矩阵时，我希望已废弃的权限点已经被清理，这样不需要在矩阵中纠结"这个权限还有用吗?"。

**优先级理由**: 废弃权限点会严重干扰矩阵维护者的判断，必须清理。

**独立测试**: 搜索代码库，验证 `PermDashboardDownload`、`PermDiscoverView`、`PermDiscoverFavorite` 三个常量已不存在，且没有引用遗留。

**验收场景**:

1. **Given** 代码库中存在 `PermDashboardDownload` 常量，**When** 重构完成，**Then** 该常量已被删除，编译检查无报错
2. **Given** 代码库中存在 `PermDiscoverView` 常量，**When** 重构完成，**Then** 该常量已被删除
3. **Given** 删除的 bit 位（Bit 3、12、13），**When** 检查位图布局，**Then** 这些位已被正确复用，分配给 `userlist:query`、`userlist:download`、`insight:query`

---

### 用户故事 3 — 新权限支持用户列表 / 自定义分析 / SQL 查询 (P1)

作为**功能开发工程师**，当 userlist、insight、sql 等功能需要做权限控制时，我希望对应的权限点已经定义好，这样我不需要去修改预定义权限基础设施。

**优先级理由**: 这些新功能已经在开发或规划中，权限点是它们可以上线细粒度控制的前置条件。

**独立测试**: 验证 `PermUserlistQuery`、`PermUserlistDownload`、`PermInsightQuery`、`PermInsightDownload`、`PermSqlQuery`、`PermSqlDownload` 六个常量已定义，且分配了正确的 bit 位置（3、12、13、18、19、20）。

**验收场景**:

1. **Given** userlist 功能需要查询权限，**When** 检查 `predef/permission.go`，**Then** `PermUserlistQuery` 已定义且分配了 bit 3
2. **Given** insight 功能需要下载权限，**When** 检查位图，**Then** `PermInsightDownload` 已定义且分配了 bit 18
3. **Given** MANAGER 角色，**When** 检查其权限集，**Then** 包含所有 6 个新权限
4. **Given** ANALYST 角色，**When** 检查其权限集，**Then** 包含 `userlist:query`、`insight:query`、`insight:download`、`sql:query`，但不包含 `userlist:download`、`sql:download`

---

### 用户故事 4 — 角色矩阵重新分配 (P0)

作为**系统管理员 / 角色设计者**，我需要重新审视每个预设角色（VIEWER/ANALYST/MANAGER）应该拥有哪些能力和权限，确保权限分配符合最小权限原则。

**优先级理由**: 矩阵重新分配是本次重构的核心——命名转换只是手段，正确的权限映射才是目的。

**独立测试**: 对三个预设角色分别检查其位图，验证与矩阵定义完全一致。

**验收场景**:

1. **Given** VIEWER 角色，**When** 检查其权限集，**Then** 仅包含：`dashboard:view`、`cohort:view`、`data:catalog:view`、`ab:view`
2. **Given** ANALYST 角色，**When** 检查其权限集，**Then** 包含分析类权限（dashboard:view/edit、userlist:query、cohort:view/edit、insight:query/download、sql:query），不包含管理类权限（project:config、data:tracking、data:asset、userlist:download、sql:download）
3. **Given** MANAGER 角色，**When** 检查其权限集，**Then** 使用显式枚举（而非位迭代）包含全部 20 个活跃权限
4. **Given** 角色矩阵定义，**When** 验证位图布局，**Then** `PermissionCount == 21`，已废弃 bit 3/12/13 已被正确复用

---

## 边界情况

- **位图字节长度不变（18→21 bits 仍为 3 字节）** → 缓存键版本必须 bump（v3→v4），因为代码注释明确要求 `PermissionCount` 变更时 bump；虽然字节长度未变，但语义上要确保新缓存不兼容旧数据
- **存量角色位图不自动迁移** → 预置角色位图在 `init()` 中构建为 PresetRoleMap（内存态），不写回 DB；无自定义角色，DB 中不存储角色位图，每次重启从代码重建
- **已废弃 bit 3/12/13 被复用** → 无自定义角色、预置角色位图为内存态，复用废弃位不影响任何存量数据
- **自定义角色不受矩阵变更影响** → 当前系统无自定义角色功能，此条不适用
- **滚动部署兼容性** → 新代码和旧代码的位图布局语义一致（旧 bit 位置不变），缓存键 bump 使新旧缓存互不干扰，滚动期内新旧节点均可正常工作
- **前端无硬编码权限字符串** → 前端权限判断基于后端位图数据，不直接引用 `"dashboard:upsert"` 等字符串，因此常量重命名不影响前端运行时行为

---

## 需求

### 功能需求

- **FR-001**: `predef/permission.go` MUST 完成 9 个权限常量重命名（DashboardUpsert→DashboardEdit、Audience*→Cohort*、DataMetadata*→DataCatalog*、ABUpsert→ABEdit、Marketing*→MA*）
- **FR-002**: `predef/permission.go` MUST 删除 3 个废弃常量（PermDashboardDownload、PermDiscoverView、PermDiscoverFavorite）
- **FR-003**: `predef/permission.go` MUST 新增 6 个权限常量（PermUserlistQuery、PermUserlistDownload、PermInsightQuery、PermInsightDownload、PermSqlQuery、PermSqlDownload），复用已废弃 bit 3/12/13 及追加高位 18/19/20
- **FR-004**: `PermissionDisplayName` MUST 同步更新：移除已删除权限的条目，添加新增权限的展示名，更新重命名权限的描述
- **FR-005**: `PermissionBitMap` MUST 按约束重组：旧 bit 位置不变（0-2, 4-11, 14-17），已废弃 bit 3/12/13 复用给新权限，剩余新权限追加到高位（18-20）
- **FR-006**: `PermissionCount` MUST 从 18 更新为 21
- **FR-007**: `CacheKeyPrefix` MUST 从 `"sol:perm:v3:"` 升为 `"sol:perm:v4:"`
- **FR-008**: `buildRolePermissionMatrix` MUST 按新矩阵重写，MANAGER 权限集使用显式枚举而非位迭代
- **FR-009**: 所有引用旧 `Perm*` 常量名的 `.go` 文件 MUST 全局替换为新名
- **FR-010**: 集成测试中的旧常量引用 MUST 同步更新
- **FR-011**: `fe/src/stores/i18n/pack/` 中的权限描述文案 MUST 与重命名后的权限一致

### 非功能需求

- **NFR-001**: 位图操作仍为 O(1) 常数时间，无性能影响
- **NFR-002**: 编译期捕获所有旧常量遗留引用（`go build ./apps/web/...` 零报错）
- **NFR-003**: 位图布局单元测试覆盖：`PermissionCount` 值、旧 bit 位置不变、复用 bit 3/12/13 位置正确、新增 bit 18-20 位置正确

---

## 关键实体

### PermissionBitMap 位图布局

权限以位图形式存储在 PostgreSQL 的 `[]byte` 字段中，通过 `predef.PermissionBitMap` 定义常量到位索引的映射。排列顺序与前端菜单结构对齐。

| Bit | 旧权限 | 新权限 | 变更类型 |
|-----|--------|--------|----------|
| 0 | `project:config` | `project:config` | 不变 |
| 2 | `dashboard:view` | `dashboard:view` | 不变 |
| 1 | `dashboard:upsert` | `dashboard:edit` | 重命名 |
| 3 | `dashboard:download` | `userlist:query` | **复用**（旧功能已废弃） |
| 12 | `discover:view` | `userlist:download` | **复用**（旧功能已废弃） |
| 6 | `audience:view` | `cohort:view` | 重命名 |
| 4 | `audience:upsert` | `cohort:edit` | 重命名 |
| 5 | `audience:use` | `cohort:download` | 重命名 |
| 7 | 保留位（legacy） | 保留位（legacy） | 不变 |
| 8 | `data:tracking` | `data:tracking` | 不变 |
| 14 | `data:metadata:view` | `data:catalog:view` | 重命名 |
| 15 | `data:metadata:upsert` | `data:catalog:edit` | 重命名 |
| 9 | `data:asset` | `data:asset` | 不变 |
| 11 | `ab:view` | `ab:view` | 不变 |
| 10 | `ab:upsert` | `ab:edit` | 重命名 |
| 13 | `discover:favorite` | `insight:query` | **复用**（旧功能已废弃） |
| 18 | — | `insight:download` | **新增** |
| 19 | — | `sql:query` | **新增** |
| 20 | — | `sql:download` | **新增** |
| 17 | `marketing:view` | `ma:view` | 重命名 |
| 16 | `marketing:upsert` | `ma:edit` | 重命名 |

`PermissionCount` = 21（旧值 18），位图占用 3 字节。

### 角色权限矩阵

按前端菜单顺序排列，对齐用户心智模型。

| 权限 | VIEWER | ANALYST | MANAGER | 说明 |
|------|--------|---------|---------|------|
| `project:config` | ❌ | ❌ | ✅ | 项目设置 |
| `dashboard:view` | ✅ | ✅ | ✅ | 查看仪表盘和图表 |
| `dashboard:edit` | ❌ | ✅ | ✅ | 创建、编辑图表和仪表盘 |
| `userlist:query` | ❌ | ✅ | ✅ | 用户明细的查看 |
| `userlist:download` | ❌ | ❌ | ✅ | 用户明细结果的下载（原始数据） |
| `cohort:view` | ✅ | ✅ | ✅ | 查看分群元数据 |
| `cohort:edit` | ❌ | ✅ | ✅ | 创建、编辑、删除规则分群 |
| `cohort:download` | ❌ | ❌ | ✅ | 分群导出（原始数据） |
| `data:tracking` | ❌ | ❌ | ✅ | 进入数据处理、Pipeline、/live-event 等页面 |
| `data:catalog:view` | ✅ | ✅ | ✅ | 查看元数据目录 |
| `data:catalog:edit` | ❌ | ❌ | ✅ | 编辑元数据目录 |
| `data:asset` | ❌ | ❌ | ✅ | 数据资产管理 |
| `ab:view` | ✅ | ✅ | ✅ | 查看 AB 所有资产 |
| `ab:edit` | ❌ | ✅ | ✅ | 创建、编辑 AB 所有资产 |
| `insight:query` | ❌ | ✅ | ✅ | 分析模型的查询 |
| `insight:download` | ❌ | ✅ | ✅ | 分析模型结果的下载（计算结果，非原始数据） |
| `sql:query` | ❌ | ✅ | ✅ | SQL 的查询 |
| `sql:download` | ❌ | ❌ | ✅ | SQL 结果的下载（原始数据） |
| `ma:view` | ❌ | ✅ | ✅ | MA 的查看 |
| `ma:edit` | ❌ | ✅ | ✅ | MA 的编辑、执行 |

### predef/permission.go 常量清单

| 常量名 | 权限值 | 类型 | Bit |
|--------|--------|------|-----|
| `PermProjectConfig` | `project:config` | 不变 | 0 |
| `PermDashboardView` | `dashboard:view` | 不变 | 2 |
| `PermDashboardEdit` | `dashboard:edit` | 重命名 | 1 |
| `PermUserlistQuery` | `userlist:query` | 复用 bit 3 | 3 |
| `PermUserlistDownload` | `userlist:download` | 复用 bit 12 | 12 |
| `PermCohortView` | `cohort:view` | 重命名 | 6 |
| `PermCohortEdit` | `cohort:edit` | 重命名 | 4 |
| `PermCohortDownload` | `cohort:download` | 重命名 | 5 |
| `PermDataTracking` | `data:tracking` | 不变 | 8 |
| `PermDataCatalogView` | `data:catalog:view` | 重命名 | 14 |
| `PermDataCatalogEdit` | `data:catalog:edit` | 重命名 | 15 |
| `PermDataAsset` | `data:asset` | 不变 | 9 |
| `PermABView` | `ab:view` | 不变 | 11 |
| `PermABEdit` | `ab:edit` | 重命名 | 10 |
| `PermInsightQuery` | `insight:query` | 复用 bit 13 | 13 |
| `PermInsightDownload` | `insight:download` | 新增 | 18 |
| `PermSqlQuery` | `sql:query` | 新增 | 19 |
| `PermSqlDownload` | `sql:download` | 新增 | 20 |
| `PermMAView` | `ma:view` | 重命名 | 17 |
| `PermMAEdit` | `ma:edit` | 重命名 | 16 |

**已废弃常量**（`PermDashboardDownload`、`PermDiscoverView`、`PermDiscoverFavorite`）对应的 bit 已被复用，常量本身删除。

**已删除常量**（编译期确保无引用遗留）:

| 旧常量名 | 旧 Bit | 说明 |
|----------|--------|------|
| `PermDashboardDownload` | 3 | 废弃，bit 复用为 `userlist:query` |
| `PermDiscoverView` | 12 | 废弃，bit 复用为 `userlist:download` |
| `PermDiscoverFavorite` | 13 | 废弃，bit 复用为 `insight:query` |

### 缓存键

- **位置**: `apps/web/service/permission/cache.go`
- **当前值**: `CacheKeyPrefix = "sol:perm:v3:"`
- **新值**: `CacheKeyPrefix = "sol:perm:v4:"`
- **理由**: 代码注释明确要求 `PermissionCount` 变更时必须 bump 版本号；虽然 18→21 不改变字节长度（均为 3 字节），但 bump 确保语义一致性

### 角色 Code 映射（Phase 1 已完成）

- **DB 内部编码**: `MANAGER` / `ANALYST` / `CONSUMER`
- **API 对外编码**: `MANAGER` / `ANALYST` / `VIEWER`
- **映射逻辑**: `predef.MapRoleCodeToExternal()` 将 `CONSUMER` → `VIEWER`，其他编码直通
- **converter 层**: `MapDBRoleCodeToAPI()` 调用上述函数并转为 `api.OrgRoleCode` 类型

---

## 成功标准

### 可量化指标

- **SC-001**: `grep -rn "PermDashboardUpsert\|PermAudience\|PermDataMetadata\|PermABUpsert\|PermMarketing\|PermDiscover\|PermDashboardDownload" apps/web/ fe/` 零结果
- **SC-002**: `go build ./apps/web/...` 零报错
- **SC-003**: `go test ./apps/web/predef/...` 全部通过，包含位图布局验证
- **SC-004**: `CacheKeyPrefix` 值为 `"sol:perm:v4:"`
- **SC-005**: `fe/src/stores/i18n/pack/en-US.json` 和 `zh-CN.json` 中权限描述与重命名一致，`yarn check-i18n` 通过
- **SC-006**: `PermissionCount == 21`

---

## 待澄清问题

> 以下问题在 CEO Review 中已确认：

- ~~存量角色（DB 中已有位图数据的组织角色）如何处理？~~ **已解决**：旧位不变 + 新增位只在高位追加，存量位图完全兼容，无需迁移。预置角色位图在 `init()` 中构建为 PresetRoleMap（内存态），不写回 DB；角色重置/编辑操作时惰性更新。
- ~~删除的 bit 在自定义角色中会静默丢失吗？~~ **已解决**：角色编辑器的保存操作基于 DB 原始位图字节做合并，不重新枚举当前活跃权限列表，因此已删除的 bit 不会被静默清空。
- ~~前端是否硬编码权限 code 字符串？~~ **已解决**：前端权限判断基于后端位图数据，不直接引用权限 code 字符串，仅需同步 i18n 展示文案。
- ~~MANAGER "ALL" 实现是否可能错误包含删除位？~~ **已解决**：MANAGER 权限集使用显式枚举，不使用位迭代。
