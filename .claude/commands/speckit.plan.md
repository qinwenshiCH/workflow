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

## Steps

1. **Locate**: Read the latest spec directory under `specs/YYYYMMDD-*/spec.md`. Also read `HABITS.md` and `CLAUDE.md` for preferences and architecture constraints.

2. **Write `plan.md`** covering all technical dimensions:

   **整体架构** — 分层架构图（模块划分、职责边界），各层职责说明，模块间依赖关系

   **数据流 / 状态管理方案** — 数据流向图（用户操作 → 处理 → 存储 → 展示），状态管理选型及结构，数据持久化方案

   **组件树 / 模块结构** — 组件/模块层级结构，各组件/模块职责，复用策略

   **API / 接口设计** — 输入输出定义（具体到字段类型），接口协议（REST/gRPC/事件等），错误响应格式

   **关键逻辑流程** — 核心算法/业务逻辑流程，异常/边界路径处理

   **技术选型及理由** — 语言/框架/库选型，对比替代方案及选择理由

3. **Update `data-model.md`** if the feature involves data:
   - Entities with fields, types, relationships
   - State transitions / lifecycle
   - API contracts or event frame types
   - Persistence schema if applicable

4. **Validation**: Ensure plan covers all user stories from spec, and addresses all four Design requirements dimensions from CLAUDE.md.

5. **Present to user**: Summarize key decisions, wait for user feedback. Iterate if requested. User says "开始开发" → proceeds to /speckit.tasks.
