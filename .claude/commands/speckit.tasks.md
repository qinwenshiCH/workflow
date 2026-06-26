---
name: speckit.tasks
description: Generate an actionable, dependency-ordered tasks.md from spec and plan artifacts.
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

Generate `tasks.md` from `plan.md` and `spec.md`. Tasks are organized by user story in priority order.

## Steps

1. **Locate**: Detect feature directory. Read `plan.md` (required), `spec.md` (required), `data-model.md` (optional).

2. **Extract**:
   - From plan: tech stack, phases, file structure
   - From spec: user stories with priorities

3. **Generate `tasks.md`** following this format:
   ```
   - [ ] T001 [P] [Story] Description with exact file path
   ```
   - `[P]` = parallelizable (different files, no dependencies)
   - `[Story]` = US1, US2, etc. maps to user story

4. **Phase structure**:
   - Phase 1: Data model & infrastructure (prerequisites)
   - Phase 2: State layer & cache
   - Phase 3+: UI components (by user story)
   - Final Phase: Testing & polish

5. **Validation**: Every user story has tasks, file paths are exact, dependencies are clear, [P] markers are correct.

6. **Report**: Total task count, per-story breakdown, parallel opportunities.
