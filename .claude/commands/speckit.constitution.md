---
description: Review and update the project constitution (CLAUDE.md) to reflect current principles and governance.
handoffs:
  - label: Build Specification
    agent: speckit.specify
    prompt: Implement the feature specification based on the updated constitution. I want to build...
---

## User Input

```text
$ARGUMENTS
```

## Overview

Review and update the project constitution in `CLAUDE.md`. This file defines the project's non-negotiable principles, architecture rules, and workflow governance.

## Steps

1. **Load** existing `CLAUDE.md`. Identify sections: architecture rules, workflow, quality gates.

2. **Review** against current project reality:
   - Architecture layers still accurate?
   - Workflow steps still match actual practice?
   - Any new patterns or tools that should be documented?

3. **Update** if needed:
   - Version (semantic: MAJOR for breaking principle changes, MINOR for additions, PATCH for clarifications)
   - Sync dependent templates if any exist

4. **Report**: Version change, what was modified, suggested commit message.
