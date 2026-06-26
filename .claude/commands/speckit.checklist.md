---
name: speckit.checklist
description: Generate a custom checklist for validating requirements quality in the current feature.
---

## User Input

```text
$ARGUMENTS
```

## Overview

Create "unit tests for requirements" checklists. Each item tests whether the spec/plan/tasks are well-written, not whether the implementation works.

## Steps

1. **Locate**: Detect feature directory from `specs/`. Read `spec.md`, `plan.md`, `tasks.md` for context.

2. **Clarify intent** (up to 3 questions): Determine domain focus (UX, security, API design, etc.), depth (lightweight vs. formal gate), and audience.

3. **Generate checklist** in `specs/<NNN-name>/checklists/<domain>.md`:
   - Each item tests REQUIREMENTS QUALITY, not implementation:
     - ✅ "Are error handling requirements defined for all API failure modes? [Gap]"
     - ❌ "Verify the API returns 200" (implementation test)
   - Categories: Completeness, Clarity, Consistency, Coverage, Measurability, Edge Cases
   - Items numbered CHK001, CHK002...
   - At least 80% of items must reference spec sections

4. **Report**: File path, item count, category breakdown.
