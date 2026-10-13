---
name: create-plan
description: Enters plan mode, researches the codebase, writes an implementation plan, and saves it to implementation-plans/YY-MM-DD-NN-<feature-name>.md before asking for approval.
user-invocable: true
---

# Create Plan

Plan a change before building it, and leave the plan in the repository where it can be read, edited and implemented from later.

## Usage

```
/create-plan <request>
```

## Workflow

### 1. Enter plan mode

Call `EnterPlanMode` first, before any research. If plan mode is already active, carry on. Nothing in the project is edited while planning; the only file written is the plan itself.

### 2. Research

- Read the project's root `CLAUDE.md`, and any design notes it points at for the area being changed. A plan that contradicts a documented invariant is wrong however well it reads.
- Find how the codebase already handles similar work, and note `file:line` references for the patterns to follow. Use the built-in `Explore` subagent when the search is broad.
- Ask the user, with `AskUserQuestion`, about anything the request and the code cannot settle. Do not guess at requirements.

### 3. Write the plan

Write the plan the way you normally would in plan mode. This skill prescribes no template, sections or length.

### 4. Save the plan (MANDATORY)

Save to:

```
<project-root>/implementation-plans/YY-MM-DD-NN-<feature-name>.md
```

- `YY-MM-DD` is today's date.
- `NN` is a zero-padded counter for that day. Glob `implementation-plans/YY-MM-DD-*` and take the next number, starting at `01`.
- `<feature-name>` is kebab-case.
- Example: `implementation-plans/26-02-15-01-page-tree-reordering.md`
- `<project-root>` is the root of the current checkout (`git rev-parse --show-toplevel`), which in a worktree setup is the worktree, not the directory above it. Create `implementation-plans/` if it does not exist.

Then, in order:

1. **Call `Write`** with the complete plan. Intending to is not the same as doing it.
2. **Call `Read`** on the file to confirm it exists.
3. **Tell the user**: "Plan saved to `implementation-plans/YY-MM-DD-NN-<name>.md`".

If plan mode supplies its own plan file, write the same content there as well. That file is what the approval dialog shows; the copy in `implementation-plans/` is the one that lasts. If the harness refuses the write into `implementation-plans/` while plan mode is active, make that write the first action after approval, before any implementation.

### 5. Exit plan mode

**Self-check first:** can you see your own `Write` call to `implementation-plans/`? If not, go back to step 4. A plan that exists only in the conversation is lost.

Then call `ExitPlanMode` and wait for the user's decision. If they ask for changes, update the saved file too, so it never trails the conversation.

### 6. Implement from the file

After approval, read the saved plan back and work from it, never from memory of the conversation: the user may have edited it, and another session may be the one implementing. Say "Implementing from `implementation-plans/<file>`".
