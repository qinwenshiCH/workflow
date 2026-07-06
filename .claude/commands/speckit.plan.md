---
name: speckit.plan
description: Generate implementation plan artifacts (plan.md, data-model.md) from the feature spec.
handoffs:
  - label: Create Tasks
    agent: speckit.tasks
    prompt: Break the plan into tasks
    send: true
  - label: Create Checklist
    agent: speckit.checklist
    prompt: Create a checklist for the following domain...
---

## User Input

```text
$ARGUMENTS
```

## Overview

Read the feature spec and generate a technical design plan. Create or update `plan.md` in the spec directory. Present the plan to the user for review and approval.

## Mandatory Quality Gates

Every `plan.md` MUST satisfy all of the following quality gates. If any gate cannot be met, document the trade-off explicitly.

### QG-1: Performance — 严禁 N+1 查询

- 所有集合操作必须使用 batch/bulk 接口，禁止在循环中逐条查询或写入
- 涉及批量数据（列表/集合）的处理，必须明确标注使用的查询方式和批次大小
- 大事务需评估锁范围，避免长时间持有行锁或表锁

### QG-2: Data Integrity — 禁止孤儿数据

- 操作必须在同一事务内完成，不能出现「A 成功、B 失败」的不一致状态
- 事务边界必须清晰标注：哪些操作在同一个事务内，哪些是独立事务
- 涉及数据清理/同步的场景需说明异常补偿机制（重试、回滚、对账）

### QG-3: Security

- 每个新增/修改的接口必须标注权限校验逻辑（谁能调用、校验什么）
- 涉及组织级数据的操作需校验调用者是否是该组织的管理员/Supervisor
- 跨组织数据访问必须显式隔离

### QG-4: Simplicity — 禁止过度设计

- 优先在现有架构上做最小改动，不引入新的框架、设计模式或中间件
- 如果一个改动涉及 3 个以上的文件，必须评估是否有更简洁的方式
- 抽象层级不超 2 层（Controller → Service → DAO），禁止添加额外间接层

### QG-5: Completeness

- 必须包含完整的影响范围地图（新增/修改的每个文件、每个函数）
- 必须分析所有失败路径：外部依赖挂了怎么办？参数非法怎么办？
- 新增逻辑必须有对应的测试策略说明

### QG-6: Architecture — 跨模块只能依赖 Service 层

- Service A 如需调用模块 B 的能力，必须通过模块 B 的 **Service 接口**，**禁止**直接注入模块 B 的 DAO
- 同模块内 Service 可以调用同模块 DAO，不受此限
- 跨模块事务传播：通过 context 传递事务上下文，DAO 层通过 `d.TableCtx(ctx)` 自动感知当前是否在事务中
- 跨模块调用不应要求调用方感知内部 DAO 细节，Service 接口应封装好事务边界

### QG-7: Cache / Redis 安全

- **缓存操作必须在事务提交之后执行**，禁止在事务内操作 Redis/缓存（事务回滚会导致缓存已清除但 DB 未变更）
- 批量数据变更后必须清除所有受影响的缓存 key，标注清楚每个操作的缓存影响范围
- 多副本部署下需评估缓存一致性窗口，不依赖本地进程内缓存做跨实例同步

### QG-8: 分布式部署兼容性

- **事务内不能包含跨网络调用**（RPC、HTTP、消息队列发送等）—— 网络调用失败会导致事务超时或悬挂
- 大事务需评估锁范围和持续时间，避免长时间持有行锁/表锁（目标：单事务 < 200ms）
- 所有跨服务调用的补偿/重试逻辑必须**幂等**
- 涉及异步处理的场景需说明消息可靠性保障（至少一次投递 + 消费幂等）

## Steps

### Step 1 — 分析准备

- 读取 spec.md，理解所有用户故事、验收场景、边界情况
- 读取 CLAUDE.md、HABITS.md，了解项目约定和架构约束
- 通过代码搜索/阅读确认涉及的现有代码结构（文件、函数、类型）
- 梳理用户故事之间的事务/数据依赖关系

### Step 2 — 编写 `plan.md`

plan.md 必须包含以下章节，缺一不可：

#### 2.1 影响范围

以表格列出所有变更的文件，标注变更类型（新增/修改/删除），每个文件写明改动点：

| 文件 | 变更类型 | 改动内容 |
| ------ | --------- | --------- |
| `service/xxx.go` | 修改 | 在 `Foo()` 函数中新增调用 `Bar()` |
| `service/xxx_test.go` | 修改 | 新增 `TestXxx` 覆盖场景 |

#### 2.2 详细技术方案

对每个修改点逐项说明：

- 涉及的函数/方法签名（含参数和返回值类型）
- 核心逻辑流程（可用文字+伪代码）
- 事务边界
- 错误处理和异常路径
- 权限校验逻辑

#### 2.3 业务逻辑流程图

使用 Mermaid `flowchart TD` 绘制业务逻辑流程图，覆盖：

- 正常流程的完整链路
- 失败路径/异常分支
- 决策点（if/switch/校验）

#### 2.4 函数调用时序图

使用 Mermaid `sequenceDiagram` 绘制跨模块/跨层次函数调用时序，覆盖：

- 用户请求的完整调用链路（Controller → Service → DAO）
- 事务开始/提交/回滚的时机
- 跨服务调用的顺序

#### 2.5 测试策略

| 类型 | 范围 | 方法 |
| ------ | ------ | ------ |
| 单元测试 | 指定测试的函数 | 测试框架 + 断言 |
| 集成测试 | 涉及 DB 的完整链路 | 事务性测试 |
| 边界测试 | 空列表、极限值、非法输入 | 参数化测试 |

#### 2.6 技术选型及理由

- 语言/框架/库选型
- 对比替代方案及选择理由
- 如无新技术引入，说明「沿用现有技术栈」

### Step 3 — 更新 `data-model.md`（如需）

### Step 4 — 质量自查

覆盖以下六个维度，缺失任一维度则 plan 为草稿：

- [ ] **数据模型 / API 接口定义**（具体到字段、类型、约束）
- [ ] **错误处理**（外部依赖异常、输入非法、事务回滚路径）
- [ ] **边界 case**（空状态、极限数据量、并发操作）
- [ ] **测试策略**（单元/集成/边界测试）
- [ ] **跨模块依赖架构**（是否违反"Service 不能依赖他模块 DAO"规则、事务传播方式是否正确）
- [ ] **缓存安全 + 分布式部署**（事务内不能操作缓存、事务内不能包含跨网络调用、补偿逻辑是否幂等）

同时逐条复核 Quality Gates QG-1 ~ QG-8，不满足的必须在 plan 中标注 trade-off。重点关注：

- [ ] **QG-1 效率**: 集合操作是否使用 batch 接口？有没有 N+1？
- [ ] **QG-2 事务**: 事务边界是否清晰标注？事务传播方式是否明确？
- [ ] **QG-3 安全**: 权限校验逻辑是否覆盖所有接口？
- [ ] **QG-4 简洁**: 是否在现有架构上最小改动？
- [ ] **QG-5 完整**: 影响范围是否完整？失败路径是否分析？
- [ ] **QG-6 跨模块**: 有没有 Service 直接依赖他模块 DAO？
- [ ] **QG-7 缓存**: 缓存清除是否在事务提交后执行？
- [ ] **QG-8 分布式**: 事务内有没有跨网络调用？

### Step 5 — 呈现与确认

向用户呈现 plan 的核心决策摘要，等待反馈。用户确认后说「开始开发」→ 进入 `/speckit.tasks`。
