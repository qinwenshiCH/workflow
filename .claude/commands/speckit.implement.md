---
name: speckit.implement
description: Execute the implementation plan by processing all tasks in tasks.md phase by phase.
---

## User Input

```text
$ARGUMENTS
```

## Overview

Execute implementation tasks from `tasks.md` in phase order, respecting dependencies and parallel markers. Full TDD cycle with quality gates.

## Pre-Execution

Read `CLAUDE.md` workflow section for quality gate requirements (make precheck, make test).

## Steps

1. **Locate**: Detect feature directory. Read `tasks.md`, `plan.md`, `detail.md` (if exists), `data-model.md` (if exists).

2. **Analyze tasks**:
   - Extract phases, dependencies, parallel markers
   - Note sequential vs. parallel execution rules
   - If `detail.md` exists, use it as the primary source for implementation specifics

3. **For each phase, for each task**:

   a. **Check technical debt**: If modifying an existing file >300 lines, refactor first.
   
   b. **TDD cycle** (if task involves code):
      - Understand the requirement
      - Write/find tests first
      - Verify tests fail
      - Implement
      - Verify tests pass
   
   c. **Quality self-check**:
      - TypeScript: `npx tsc --noEmit` (type safety)
      - Unit tests: run relevant test file
      - Smoke: `make precheck` (SSR guard + smoke test)
   
   d. **Mark task complete** in tasks.md: `[ ]` → `[x]`

4. **All tasks complete**:
   - Run `make validate` (typecheck + test + build)
   - Run `make precheck`
   - Commit with message format: `<type>: <描述>`
   - Report completion summary

## Error Handling

- If a task fails: diagnose, fix, re-run quality check, retry
- If parallel tasks share files, run them sequentially
- Never ask the user "should I continue?" — just fix and proceed
