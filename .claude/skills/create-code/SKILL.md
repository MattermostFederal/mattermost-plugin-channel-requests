---
name: create-code
description: Implements an approved plan from its saved file in implementation-plans/, task by task, then runs the project's linters and tests before reporting.
user-invocable: true
---

# Create Code

Implement an approved plan, working from the saved plan file rather than from memory of a conversation.

## Usage

```
/create-code <plan-file>           # Implement the plan
/create-code <plan-file> --tdd     # Write each failing test before the code that passes it
/create-code                       # No file given: list the plans and ask
```

## Workflow

### 1. Read the plan from its file (MANDATORY)

`Read` the plan file, then say "Implementing from `implementation-plans/<file>`".

Never implement from the conversation. The file is the source of truth: the user may have edited it since it was discussed, and the session that wrote it may not be this one.

If no file was given, list `implementation-plans/` newest first, show the most recent few, and ask the user which one with `AskUserQuestion`. Do not guess.

If the plan is unclear, contradicts itself, or contradicts the code as it now stands, stop and ask before writing anything.

### 2. Read the project's rules

Read the project's root `CLAUDE.md`, and any design notes it points at for the area the plan touches. Its conventions (comments, naming, formatting, error handling, dependencies) bind the code you write, and a linter checks only some of them.

### 3. Implement task by task

Take the plan's tasks in order. For each:

1. Read the existing code it touches.
2. Make the change, following the patterns the plan references.
3. Write or update the tests for it.
4. Run those tests, and leave them passing before moving on.

With `--tdd`, swap steps 2 and 3: write the test, run it and watch it fail, write the least code that passes it, then tidy with the test still green.

Stay inside the plan. If a task turns out to need something the plan did not foresee, tell the user what and why instead of quietly widening the change. If the plan lists documentation, help pages or design notes to update, those are tasks too.

### 4. Lint and test

Use the project's own commands. Find them, in this order, in its `CLAUDE.md`, its `Makefile`, then `package.json` scripts or the language's standard tooling (for example `make check-style` and `make test`). Do not invent a command the project does not define.

Run the full test target, not only the new tests, and fix what fails in the code you touched. If something fails that you cannot fix, report it with its output. Do not call a task complete while its tests fail.

### 5. Report

```markdown
## Implementation Summary

Plan: `implementation-plans/<file>`
Status: COMPLETE / PARTIAL / BLOCKED

### Tasks
- [x] Task 1
- [ ] Task 3: blocked, [reason]

### Files Changed
| File | Change |
|------|--------|

### Checks
- Lint: [command], [result]
- Tests: [command], [result]

### Departures from the plan
- [anything done differently, and why]
```

Do not commit unless the user asks. If the project has `/review-code`, suggest it as the next step.
