---
name: speckit.tasks
description: Generate an actionable, dependency-ordered tasks.md from spec, plan, and optional detail artifacts.
handoffs:
  - label: Analyze For Consistency
    agent: speckit.analyze
    prompt: Run a project analysis for consistency
    send: true
  - label: Implement Project
    agent: speckit.implement
    prompt: Start the implementation in phases
    send: true
---

## User Input

```text
$ARGUMENTS
```

## Overview

Generate `tasks.md` from `spec.md`、`plan.md`，以及存在时的 `detail.md`。

默认规则：

- `spec.md` 定义为什么做、做什么
- `plan.md` 定义总体方案和落地顺序
- `detail.md`（如果存在）定义实现边界和具体改动点

如果 `detail.md` 存在，`tasks.md` 必须以它为主要实现依据。

## Steps

1. **Locate**: Detect feature directory. Read `spec.md` (required), `plan.md` (required), `detail.md` (optional), `data-model.md` (optional).

2. **Extract**:
   - From spec: user stories with priorities
   - From plan: top-level phases, recommended rollout
   - From detail (if exists): file map, module breakdown, implementation sequencing

3. **Generate `tasks.md`** following this format:
   ```
   - [ ] T001 [P] [Story] Description with exact file path
   ```
   - `[P]` = parallelizable (different files, no dependencies)
   - `[Story]` = US1, US2, etc. maps to user story

4. **Phase structure**:
   - Phase 1: 底座 / schema / config / infrastructure
   - Phase 2: 核心 service / API / domain flow
   - Phase 3+: 接入点 / UI / jobs / export（按用户故事拆分）
   - Final Phase: Testing / migration / polish / docs

5. **Validation**:
   - Every user story has tasks
   - File paths are exact
   - Dependencies are clear
   - `[P]` markers are correct
   - If `detail.md` exists, every impacted module in `detail.md` is covered by tasks

6. **Report**: Total task count, per-story breakdown, parallel opportunities.
