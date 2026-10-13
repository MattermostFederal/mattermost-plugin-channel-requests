---
name: review-code
description: Comprehensive code review through parallel read-only review lenses (concurrency, error handling, simplicity, security, backend, frontend, testing). Works on local branch changes, uncommitted changes, a path, or a GitHub PR.
user-invocable: true
---

# Review Code

Comprehensive code review using **parallel read-only review lenses**. Catches bugs, security issues, and pattern violations.

Works on:
- **Branch changes** (default): commits on the current branch against the trunk, plus uncommitted working-tree changes
- **Uncommitted only** (`--uncommitted`): working-tree changes only
- **A path**: a specific file or directory
- **GitHub PRs** (`--pr`)

It needs nothing beyond the built-in tools, `git`, and `gh` for `--pr`. It finds semantic issues; run the project's own lint target afterwards for formatting.

## Usage

```
/review-code                              # All changes on the current branch since the trunk (default)
/review-code --uncommitted                # Uncommitted working-tree changes only
/review-code <file-or-directory>          # Review a specific path
/review-code --pr 123                     # Review GitHub PR #123
/review-code --quick                      # Core lenses only (fastest)
/review-code --security                   # Core plus the security lenses
/review-code --full                       # Every lens group, including maintenance
```

## What It Does

```
/review-code [--pr <number>]
         |
         v
  Step 1: IDENTIFY CHANGES
    Default: git diff <trunk>...HEAD plus the working tree
    On the trunk itself: working tree only
    --uncommitted: working tree only
    --pr: gh pr diff <number>
    Detect languages and domains from the changed files
         |
         v
  Step 2: RECORD THE TREE
    git status --porcelain, kept for comparison
         |
         v
  Step 3: RUN THE LENSES (parallel, read-only)
    Core always; the other groups by what changed
         |
         v
  Step 4: CHECK THE TREE
    git status --porcelain must match Step 2
         |
         v
  Step 5: VERIFY AND SYNTHESIZE
    Confirm each claimed blocker against the code
    Prioritize by severity, apply the 80/20 filter
         |
         v
  OUTPUT: Review Report (MUST FIX, SHOULD FIX, passed checks)
         |
         v
  Step 6: OFFER TO FIX
```

## Step 1: Identify the Changes

Find the trunk rather than assuming its name:

```bash
trunk=$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null | sed 's|^origin/||')
trunk=${trunk:-main}
```

| Mode | Diff |
|------|------|
| Default | `git diff "$trunk"...HEAD` plus `git diff HEAD` and untracked files |
| On the trunk | `git diff HEAD` and untracked files |
| `--uncommitted` | `git diff HEAD` and untracked files |
| Path | The files under the path, whole |
| `--pr <n>` | `gh pr diff <n>` |

Read the project's root `CLAUDE.md` before reviewing. Its conventions and invariants are what "follows established patterns" means here, and every lens prompt should be given the ones relevant to the changed area.

## Reviewers Are Read-Only

**A review reads; it does not edit.** Every lens runs as the built-in `Explore` subagent, which has no `Write` or `Edit` tool. Do not run a lens on a write-capable agent type, however well suited it seems.

The reason is not tidiness. Once a reviewer can write, its output is indistinguishable from everybody else's work in the tree, and a dirty `git status` stops being evidence of anything. `Explore` can still run shell commands, and a shell can write a file, so the tool list is not the whole guard. The tree check is:

1. Before dispatching, record `git status --porcelain`.
2. After every lens has reported, run it again. It must be identical.
3. If it differs, **establish what wrote the difference before touching it**. A dirty tree is not proof a reviewer did it: another session or the user may be editing the same tree, and reverting unstaged work is not undoable. Only if a reviewer is clearly the writer, revert that change, say so in the report, and name the lens.
4. Never fold a reviewer's edits into the review as though they were findings.

End every lens prompt with: *"Report findings only. Do not edit, create or delete any file."* That line is advisory. The tree check is the enforcement.

If a write-capable specialist is ever truly the right tool, dispatch it with `isolation: "worktree"` so anything it writes lands in a throwaway copy. A worktree is checked out at `HEAD`, so uncommitted changes are invisible there and `--uncommitted` reviews cannot use it.

## Review Lenses

A **lens** is one narrow question asked of the diff by a fresh reviewer. Launch every selected lens in parallel, in a single message, one `Agent` call each with `subagent_type="Explore"`.

### Core (always run)

| Lens | Catches |
|------|---------|
| Concurrency | Data races, TOCTOU, deadlocks, leaked goroutines or promises, unsynchronized shared state |
| Simplicity | Over-engineering, YAGNI violations, abstractions nothing needs yet |
| Error handling | Ignored or swallowed errors, missing context when wrapping, failure paths that crash |

### Security (any non-test change)

| Lens | Run when | Catches |
|------|----------|---------|
| Input validation | Always | Empty, oversized, malformed or cross-referenced input accepted unchecked |
| Hardcoded values | Always | Secrets, magic numbers, config that belongs in a constant or setting |
| XSS and output escaping | Frontend files, or anything that renders HTML | Unescaped author-controlled text reaching markup, unsafe URL schemes |
| Authorization | Code touching sessions, permissions, routes or handlers | Missing or bypassable checks, data returned to the wrong caller |

### Backend (server-side files changed)

| Lens | Run when | Catches |
|------|----------|---------|
| Nil and null safety | Always | Dereferences of values that can be absent |
| Data access | Database or storage calls changed | N+1 queries, unbounded reads, multi-step writes without a transaction |
| Layering | The project documents layers | Calls that skip a layer or put logic in the wrong one |
| Multi-node correctness | Caches, cluster events, shared state | Behavior that only works on a single node |

### Frontend (client-side files changed)

| Lens | Catches |
|------|---------|
| Component patterns | Hook misuse, effects without cleanup, state that belongs elsewhere |
| Type design | Types that fail to express their invariants, `any` hiding a contract |
| Null handling | Optional values used as though present |
| Accessibility and i18n | Missing keyboard support or labels, untranslated or concatenated strings |

### Testing (test files changed, or new code without tests)

| Lens | Catches |
|------|---------|
| Test coverage | New behavior with no test, untested failure paths |
| Test quality | Flaky patterns, assertions that cannot fail, tests coupled to implementation |

### Maintenance (`--full` only)

| Lens | Catches |
|------|---------|
| Duplication | New code that repeats an existing utility |
| Comments and docs | Comments the project's conventions forbid, stale or misleading documentation |
| Backwards compatibility | Removed fields, changed behavior, wire or storage formats that moved alone |
| File structure | Files that do not match the project's layout conventions |

### Lens selection

```python
lenses = ["concurrency", "simplicity", "error-handling"]          # core

if flag == "--quick":
    return lenses

if not test_files_only:
    lenses += ["input-validation", "hardcoded-values"]
    if has_frontend_files or renders_html:
        lenses.append("xss-and-escaping")
    if touches_auth_or_routes:
        lenses.append("authorization")

if flag == "--security":
    return lenses

if has_backend_files:
    lenses.append("nil-safety")
    if has_data_access_changes: lenses.append("data-access")
    if project_documents_layers: lenses.append("layering")
    if touches_caches_or_cluster: lenses.append("multi-node")

if has_frontend_files:
    lenses += ["component-patterns", "type-design", "null-handling", "a11y-and-i18n"]

if has_test_files or new_code_without_tests:
    lenses += ["test-coverage", "test-quality"]

if flag == "--full":
    lenses += ["duplication", "comments-and-docs", "backwards-compatibility", "file-structure"]
```

## Lens Prompt Template

```
Review these code changes through one lens only: [lens name].
[The lens's "Catches" text.]

You may read the rest of the repository to understand the changed code.

## Project conventions that apply
[the relevant invariants and conventions from the project's CLAUDE.md]

## Code Changes
<code>
[the diff, or the file contents]
</code>

## CRITICAL: Apply 80/20 Thinking

**A MUST FIX blocker is ONLY:**
- A bug that will cause a runtime failure
- A security vulnerability (injection, auth bypass, XSS)
- A data integrity risk (corruption, loss)
- A race condition or concurrency bug
- Missing error handling that crashes
- A violation of a documented project invariant

**NOT a blocker (SHOULD FIX or SKIP):**
- Style issues, naming preferences
- Minor optimizations
- "Best practices" that do not affect correctness

## Output

1. **MUST FIX** (0-3 max): What breaks? File:line? Fix?
2. **SHOULD FIX** (0-5): Quality improvements
3. **VERDICT**: PASS / ISSUES

Report findings only. Do not edit, create or delete any file.
```

## Step 5: Verify and Synthesize

A lens is a fresh reader, not an authority. **Open the file and confirm every claimed MUST FIX yourself** before it goes in the report. A finding you cannot reproduce from the code is dropped or downgraded, and the report says which.

- Merge duplicates reported by more than one lens, and credit each
- A finding two lenses reached independently deserves more weight, not automatic acceptance
- Apply the 80/20 filter: most findings are SHOULD FIX

## Output Format

```markdown
## Code Review: [files reviewed]

### MUST FIX (Blockers)
| Issue | File:Line | Lens | Fix |
|-------|-----------|------|-----|
| Race in cache access | `cache.go:45` | Concurrency | Guard with the existing mutex |

### SHOULD FIX (Quality)
| Issue | File:Line | Lens | Recommendation |
|-------|-----------|------|----------------|
| Overly complex function | `utils.go:89` | Simplicity | Extract a helper |

### Passed Checks
- No XSS vulnerabilities
- Input validation present
- Error handling complete

### Lens Summary
| Lens | Verdict | Findings |
|------|---------|----------|
| Concurrency | ISSUES | 1 race |
| Simplicity | PASS | - |

### Working tree
Unchanged by the review. (Or: what changed, what wrote it, what was done.)

---

### Verdict: NEEDS WORK / APPROVED
```

## Step 6: Offer to Fix

After presenting the report, **always ask the user whether to fix the findings**. Use `AskUserQuestion`:

> "Would you like me to fix the issues found in the review?"

Options:
- **Fix all**: apply MUST FIX and SHOULD FIX changes
- **MUST FIX only**: apply only the blocker fixes
- **No, just the review**: leave the code unchanged

If the user chooses to fix, apply the changes with `Edit`, preserving the existing structure, and follow the project's conventions while doing it. Then run the project's lint and test targets (find them in its CLAUDE.md, Makefile or package.json) and report the result.

**Note**: This step is skipped in `--pr` mode when the PR is not the checked-out branch. Suggest the fixes as review comments instead.

## Flags

| Flag | Effect |
|------|--------|
| `--pr <number>` | Review a GitHub PR instead of local changes |
| `--uncommitted` | Review only uncommitted working-tree changes |
| `--quick` | Core lenses only |
| `--security` | Core plus the security lenses |
| `--full` | Every group, including maintenance |

## Examples

```bash
# Full review of branch changes (recommended before a PR)
/review-code

# Only what is uncommitted
/review-code --uncommitted

# A specific file
/review-code server/http.go

# A GitHub PR, quickly
/review-code --pr 123 --quick

# Security-focused
/review-code --security
```

## When to Use

| Scenario | Command | Skip review |
|----------|---------|-------------|
| After implementing a plan | `/review-code` | |
| Before opening a PR | `/review-code` | |
| Before committing WIP | `/review-code --uncommitted --quick` | |
| Reviewing a PR | `/review-code --pr 123` | |
| Security-sensitive code | `/review-code --security` | |
| Tiny typo fix | | Yes |
| Documentation only | | Yes |

## Tips

- **Use `--quick` for WIP**, the default before a PR.
- **Fix MUST FIX immediately.** They are blockers for a reason.
- **SHOULD FIX can wait** for a follow-up if time is short.
- **Verify before believing.** A single lens's finding may be a false positive.
- **Parallel execution.** Launch all lenses in a single message.
