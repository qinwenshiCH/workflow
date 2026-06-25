---
description: Identify underspecified areas in the current feature spec by asking up to 5 targeted clarification questions and encoding answers back into the spec.
handoffs:
  - label: Build Technical Plan
    agent: speckit.plan
    prompt: Create a plan for the spec. I am building with...
---

## User Input

```text
$ARGUMENTS
```

## Overview

Detect and reduce ambiguity or missing decision points in the active feature spec, recording clarifications directly into the spec file.

## Steps

1. **Locate feature spec**: Look in `specs/` — if a feature name is provided as argument, use it; otherwise use the newest directory matching `NNN-*`. Read `spec.md`.

2. **Scan for ambiguity** across these categories. For each, mark status: `Clear / Partial / Missing`:
   - Functional scope & behavior (goals, out-of-scope, roles)
   - Domain & data model (entities, lifecycle, scale)
   - Interaction & UX (journeys, error/loading states)
   - Non-functional (performance, reliability, observability, security)
   - Integration & dependencies (external APIs, failure modes)
   - Edge cases (negative scenarios, conflicts)
   - Terminology consistency

3. **Generate up to 5 clarification questions** prioritizing highest-impact categories. Each must be answerable as multiple-choice or short answer (≤5 words). Present ONE at a time with a recommended option.

4. **After each answer**, update the spec in-memory. Create a `## Clarifications` section if needed with `### Session YYYY-MM-DD`. Append the Q&A and apply the clarification to the relevant section. Save after each integration.

5. **Validation**: Ensure no contradictory statements remain, markdown is valid, and each clarification is applied.

6. **Report**: Number of questions asked, sections touched, coverage summary. Recommend next command (`/speckit.plan` or manual refinement).
