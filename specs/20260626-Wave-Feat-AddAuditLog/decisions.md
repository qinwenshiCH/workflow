# 设计决策记录

> 按主题分类，只保留最终确认的决策。历史讨论中被覆盖的废弃思路已按 [spec 开发指挥台](/) 纪律清理。

## 范围

- 活动日志定位为**项目内对象的统一活动规范与落盘基础设施**，不沿用"资产"作为顶层概念
- 指标、事件、属性归入**元数据对象**，不属于传统资产定义但同样适用统一活动规范
- V1 边界分三层：项目内对象活动（主线）、global item 活动（独立表）、账号活跃字段（account 表）
- AB 和 Metric 必须纳入统一规范；事件/属性等元数据对象适用同一模型，按计划分批接入
- V1 最直接需求价值是**内部排障/根因定位**，不是对外售卖的企业合规功能
- V1 技术方案收敛为**最小闭环**：写入、落库、查询、迁移；不先做审计平台或治理平台
- AB 内部冲突解决必须写入活动，`source = "internal"`，operator 继承触发冲突的原始操作人
- Cohort 调度任务生命周期不单独成行，作为 CRUD 活动行的 extra 记录
- Cohort 定时重算执行（RunCohortJob cron 回调）不进入活动表（系统运维操作）
- Dashboard 的 Chart 关联/移除记在 Dashboard 的活动记录中（`changes[]` 体现 `chart_ids` diff）
- Pipeline CRUD 接入项目对象活动；内部 Process / callback 不进入活动表
- MA Campaign CRUD 接入项目对象活动；状态流转（Launch/Pause/Resume/Finish）通过 `update` + `extra.transition` 表达，现有 `ma_operation_log` 历史迁移到新活动表
- AB target pipeline 状态同步 V1 不做活动
- PROJECT_MEMBER 确认纳入 V1，与 org member 独立（独立的权限授予操作）
- 邀请流程：邀请建在自有表上，接受邀请后触发活动记录；邀请本身不落活动

## 架构

- `meta.activity_log`（meta schema）是项目内对象的标准落盘表；global item 走 `global.activity_log`
- 组织/项目级管理操作基于 `global.activity_log`，不入 `meta.activity_log`
- `global.activity_log` 不是 org 专用表：scope 不通过冗余列表达，由 `item_type + item_id` 隐式推导
- V1 不在官方产品新增通用活动查看入口；仅保留 AB / Metric 既有查看能力，其余走 OP / 内部接口
- 不承诺统一查询层；op_operation_log / global.activity_log / meta.activity_log 三套独立存储
- 查询接口保留 `page / page_size / total` 模型，V1 不引入 cursor-only
- V1 不做分区；索引 `(item_type, item_id, occurred_at DESC)`
- `occurred_at` = 事件时间，`created_at` = 入库时间，两者语义明确区分
- 新增操作人索引 `(operator_id, occurred_at DESC)`

## 枚举与数据模型

- `ItemType` 使用全小写字符串（不沿用 `def.AssetType`）
- `action_type` 使用全小写字符串，统一定义在 `activity/types.go`（守口到活动模块）
- 基础动作集锁定为 `create / update / delete / copy`；不新增 `action_name` 字段
- 扩展 action_type 必须注册评审（动作名、理由、detail 最小 schema、迁移映射、测试）
- source 为4个值：`web / openapi / internal / backfill`
- 表名定稿为 `meta.activity_log`（在 project schema 内，`project_` 前缀冗余）
- 枚举与常量统一定义在 `activity/types.go`
- `operator_id` 不设外键，系统操作无真实用户时填 `operator_id=0, operator_name=''`
- 所有 ItemType / ActionType / Source 的字符串值统一全小写，不做大小写区分

## Data Model

- `detail_payload` 列类型锁定为 **TEXT**，不使用 PG JSONB；TEXT 内是否启用压缩见讨论议题
- `detail_version` 不下放给业务方，serializer/parser 兼容由活动模块统一维护
- `operator_name` 保留并作为展示快照（不随用户改名回写历史）
- `item_name` 保留并作为展示快照（删除后仍可追溯对象名）
- `source` 区分 web / openapi / internal / backfill
- `correlation_id` 替代 `operation_group_id`，由基础设施自动生成或继承上下文
- 查询接口返回 `detail`，不直接暴露存储字段名 `detail_payload`
- V1 不记录 IP 地址
- Account 活跃字段为 3 个 `TIMESTAMPTZ NULL` 列加在 `global.account` 表
- Account API Token 活动的敏感字段规则：raw token 永不进入 detail；`token_hash` drop；`token_hint` 可作为有限线索
- global item scope 合法性由 ActivityService 简单表驱动规则校验，不通过 DB CHECK 绑定业务枚举

## 写入与一致性

- 由调用方运行时显式传入 Strong/Core/BestEffort 的方案已废弃，改为通过稳定 `PolicyKey` 解析
- `WritePolicy` 三种等级：`required_full`（主行/detail 失败返回 error）、`required_core`（主行失败返回 error，detail 可降级）、`best_effort`（warning）
- 策略由业务 owner 在接入场景注册时声明，ActivityService 负责执行，不按模块或 `item_type + action_type` 一刀切
- 不允许调用方运行时自由传 `required_full` / `best_effort`
- PolicyKey 注册保障：运行时拒绝是最后防线。加 init-time test，用反射扫描所有 `ActivityPolicyKey` 常量，断言每个都调了 `RegisterPolicy`
- 文档不引入额外平台概念；是否阻塞业务由具体接入点的 PolicyKey 和事务边界共同决定
- `PolicyKey` 在 V1 只是稳定场景名到返回行为的轻量映射，不建设复杂策略框架
- 批量写入：同批共享 `correlation_id`；任一核心字段失败 → 整体返回 error（required_full/core）、warning（best_effort）
- 同一事务跨对象操作（如 CopyDashboard）通过批量写入接口写入
- V1 不实现自动清理、分区、TTL；这些能力进入后续讨论

## 旧值与 Detail 构造

- 旧值来源：复用业务 Update 方法本来就有的 `dao.Get()`（权限校验/乐观锁/业务判断），不是为 activity log 额外引入的读
- 薄方法（无 Get 直接 Update）需加一次预热读，不改 DAO 层、不改事务边界
- 投影函数定义在同包 `projection.go`，与业务代码同目录
- 投影 = 自然的排除声明——不写进 projection map 的字段 ChangesBetween 不记录
- 通用排除字段（id/created_at/updated_at/version 等）硬编码在 ChangesBetween 内部，双重兜底
- 敏感字段两层防护：投影函数内脱敏（主要）+ ActivityService 字段名拦截（兜底名单在 `sensitive.go`）
- Create/Delete/Update 三种场景各有标准代码模板，时序不同，不共用同一个模板
- CDC / Outbox / Trigger 方案已排除（看不到业务语义、引入额外复杂度），保留显式写入调用
- GORM hook 方案已排除：Go 无线程本地存储、无跨 hook 状态传递，强行模拟需要大量 ctx hack 且只覆盖 GORM 一条路径
- PostgreSQL trigger 方案已排除（行级 snapshot 而非 field-level changes，敏感字段落入 JSON 前无法脱敏）
- 采集方案锁定为"显式投影 + 模板化写入"：业务 service 在事务内读旧值、投影、变更、读新值、投影、ChangesBetween、写入
- 新增接入点时可选的 `WithWriteActivity[T]` 泛型模板，但只覆盖标准 Update 场景；Create/Delete 不走模板
- 部署时增加 write-only feature flag，异常时快速关闭写入

## 迁移

- AB `details.operation_records` 和 `meta.metric_define_history` 迁移到新活动表
- `meta.asset_behavior` 和 `global.op_operation_log` 不迁移
- 迁移映射：AB CREATE→create，UPDATE/状态变更→update，COPY→copy，DELETE→delete；Metric→update
- 缺少 before/after 的历史记录允许 changes 为空
- 迁移具备幂等去重键：legacy_source + item_type + item_id + legacy_action_type + operator_id + occurred_at
- Chart/Dashboard/Cohort/Event/Property 无可靠旧操作历史源，不从 asset_behavior 伪造历史

## 边界与异常处理

- 单条 detail 保持大小预算 64KB（可配置），优先通过字段投影和截断控制
- 同一对象高频操作每条独立记录，不合并去重
- 历史复制后新操作只写新表，不做双写；旧表保留不删
- `last_active_at` Redis 不可用时跳过 DB 写入并记录 warning，不降级
- BatchInsert 上限 500 行
- 未注册 action_type 在写入入口直接拒绝，不能透传调用方拼出来的字符串
- AB 状态流转（online / release / debug）的语义通过 `detail.changes` + `extra.transition` 表达，不注册额外 action_type
- `extra` key 实行注册制：新增 key 需与 action_type 同等评审（key 名、值类型、变化预期、历史兼容方案），注册于 `activity/extra_keys.go`
- `extra` 不得承载字段变更信息（不得替代 changes[]），值应为历史稳定的简单类型（string/number/bool），读者对未知 key 或未知值宽容展示

## 开发者体验

- 补充 4 份文档：ActivityService 接入指南、对象类型接入模板、WritePolicy 选择指南、Detail helper 使用说明
- 提供 3 个测试辅助工具：`activitytest.MockService`、`AssertLogWritten`、`AssertChangesContains`
- 每个接入点必须有成功写入测试、无变化不写测试、失败策略测试、查询接口测试

## 交付顺序

- Phase 0（底座）：建表 + activity 模块 + 查询 API + PolicyKey 简单映射
- Phase 1（高价值对象 + 历史迁移）：Chart/Dashboard/Cohort/AB/Metric + AB/Metric 迁移
- Phase 2（元数据长尾）：Event/Property 类对象
- Global item 活动和账号活跃字段独立交付
- 不允许所有 phase 绑成一次总开关上线
