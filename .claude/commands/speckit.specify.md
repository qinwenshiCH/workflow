---
name: speckit.specify
description: Create or update a feature specification from a natural language feature description.
handoffs:
  - label: Build Overview Plan
    agent: speckit.plan
    prompt: Create an overview design plan for the spec.
  - label: Clarify Spec Requirements
    agent: speckit.clarify
    prompt: Clarify specification requirements
    send: true
---

## User Input

```text
$ARGUMENTS
```

## Overview

Create a new feature spec in `specs/` directory. The user's description is the feature definition.

## Steps

1. **Extract short name** (2-4 words kebab-case) from the description.

2. **Determine next feature number** by scanning `specs/` directories for highest `NNN-` prefix, then increment.

3. **Create directory**: `specs/NNN-short-name/`.

4. **Write `spec.md`** 使用中文模板结构（参考 `specs/_template/spec.md`）:
   - 标题：功能名称
   - 用户故事（P0-P2），每个包含：优先级理由、独立测试、验收场景（Given/When/Then）
   - 边界情况
   - 需求（功能 + 非功能）
   - 关键实体
   - 成功标准（可量化、技术无关）

5. **Quality validation**: Ensure no implementation details leak, all acceptance criteria are testable, edge cases identified. Add `[NEEDS CLARIFICATION]` for critical unknowns only (max 3).

6. **Report**: Path to spec, number of user stories, readiness for next phase.
