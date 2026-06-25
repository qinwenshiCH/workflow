---
description: Non-destructive cross-artifact consistency and quality analysis across spec.md, plan.md, and tasks.md.
---

## User Input

```text
$ARGUMENTS
```

## Overview

Identify inconsistencies, gaps, and quality issues across the three core artifacts. READ-ONLY — never modifies files.

## Steps

1. **Locate**: Detect feature directory from `specs/` (latest or by name). Read `spec.md`, `plan.md`, `tasks.md`. Also load `CLAUDE.md` for constitution rules.

2. **Build semantic models**:
   - Requirements inventory from spec (functional + non-functional)
   - Task coverage mapping (which requirements have tasks)
   - Constitution rule set (MUST/SHOULD statements)

3. **Run detection passes**:
   - Duplication: near-duplicate requirements or tasks
   - Ambiguity: vague terms, TODOs, placeholders
   - Underspecification: requirements without matching tasks
   - Constitution alignment: violations of MUST principles
   - Coverage gaps: requirements with zero tasks, tasks with no requirement
   - Inconsistency: terminology drift, conflicting statements

4. **Assign severity**: CRITICAL / HIGH / MEDIUM / LOW. Constitution violations are always CRITICAL.

5. **Output analysis report** (no file writes):
   - Findings table (ID, category, severity, location, summary, recommendation)
   - Coverage summary (requirements → tasks mapping)
   - Metrics (total requirements, tasks, coverage %, critical count)

6. **Recommend next actions**: Fix critical issues before implementation.
