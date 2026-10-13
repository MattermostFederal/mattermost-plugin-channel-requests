---
name: review-plan
description: Validates an implementation plan before any code is written, using parallel read-only review lenses. Supports --spec mode for requirements validation and default mode for technical feasibility. Focuses on 80/20 high-impact issues.
user-invocable: true
---

# Plan Review

Validate an implementation plan before writing code. **Focuses on 80/20 analysis**: the critical few issues that matter, not the trivial many that do not.

**Two modes:**
- **Default**: Technical feasibility review (can this be built?)
- **`--spec`**: Requirements validation review (are we building the right thing?)

It needs nothing beyond the built-in tools. If the project also has `/create-plan`, that is where plans come from, but any plan file works.

## Usage

```
/review-plan <plan-file>                            # Technical review (default)
/review-plan <plan-file> --spec                     # Requirements validation
/review-plan <plan-file> --req <requirements>       # Compare against the original request
/review-plan <plan-file> --context <relevant-files> # Include codebase context
/review-plan <plan-file> --quick                    # One combined reviewer instead of separate lenses
```

## Two Review Modes

### Default Mode: Technical Feasibility

**Question**: Can this plan be implemented?

| Validates | Questions |
|-----------|-----------|
| Feasibility | Do the required APIs exist? Are dependencies available? |
| Risks | Data integrity? Security? Breaking changes? |
| Ambiguity | Can a developer implement it without guessing? |
| Scope | Over-engineered? What can be cut? |

### Spec Mode (`--spec`): Requirements Validation

**Question**: Are we building the right thing?

| Validates | Questions |
|-----------|-----------|
| Completeness | All user requirements captured? |
| Clarity | Requirements testable and unambiguous? |
| Scope | Out-of-scope clearly defined? |
| Acceptance | Criteria specific and verifiable? |
| Stakeholder | Could a non-technical person approve this? |

**Use `--spec` FIRST**, then default mode:
```bash
/review-plan plan.md --spec    # Step 1: Requirements OK?
/review-plan plan.md           # Step 2: Technical OK?
```

## Core Philosophy: 80/20 Prioritization

**Not all issues are equal.**

| Category | Criteria | Action |
|----------|----------|--------|
| **MUST FIX** | Blocks implementation, causes data loss, security risk, or will definitely fail | Fix before coding |
| **SHOULD FIX** | Improves quality but the plan works without it | Address if time permits |
| **DEFER** | Valid concern but not for this iteration | Track for future |
| **SKIP** | Over-engineering, premature optimization, or gold-plating | Ignore |

### Technical Mode Blockers

**A blocker IS:**
- A missing critical step that makes implementation impossible
- A data integrity risk (orphaned records, corruption)
- A security vulnerability (injection, auth bypass)
- An undefined contract that blocks integration
- Ambiguity that requires guessing during implementation
- A contradiction with an invariant the project's CLAUDE.md or design notes document

**A blocker is NOT:**
- Missing documentation
- Imperfect error messages
- Edge cases that affect under 1% of users
- A "best practice" that is not required

### Spec Mode Blockers

**A blocker IS:**
- A user requirement from the original request that is missing
- Acceptance criteria that cannot be tested
- No "out of scope" section (scope creep risk)
- Contradictory requirements
- Requirements the stakeholder has not agreed to

**A blocker is NOT:**
- Missing nice-to-have features
- Imperfect wording
- Missing implementation details (that is for the technical review)

## Review Lenses

A **lens** is one narrow question asked of the plan by a fresh reviewer that has not seen how the plan was written. Each lens runs as a built-in read-only `Explore` subagent. Launch every selected lens in parallel, in a single message.

These lenses review design, sequence and contracts. Do not point implementation-bug lenses (races, nil handling, XSS in a component) at a plan: there is no code to read yet.

### Always run

| Lens | Asks |
|------|------|
| Design flaws | Logical flaws, missing steps, impossible sequences, contradictions, states nobody handles |
| Simplicity | Over-engineering, YAGNI violations, premature abstraction, anything that can be cut |

In `--spec` mode, run one more and skip the domain lenses:

| Lens | Asks |
|------|------|
| Requirements | The Spec Review Prompt below, verbatim |

### Run when the plan touches the domain (default mode)

| Plan mentions | Lens | Asks |
|---------------|------|------|
| API, endpoint, route, payload | API contract | Completeness, consistency, breaking changes, auth on every route |
| database, schema, migration, store | Data and schema | Integrity, migrations and rollback, indexes, unbounded queries |
| UI, component, page, panel | UX and edge states | User flows, and the error, empty and loading states |
| permission, role, auth, secret, user input | Security | Trust boundaries, authorization gaps, injection surfaces |
| type, struct, interface, model | Type design | Invariants the types express or fail to, encapsulation |
| both a server and a client change | Client-server alignment | Methods, paths and shapes agree on both sides |
| several components or layers | System design | Boundaries, misplaced responsibilities, coupling of independent concerns |

**Quick mode (`--quick`)**: one `Explore` subagent given the full prompt template for the mode, no separate lenses.

## Workflow

### Step 1: Gather Context

1. Read the plan file
2. Read the project's root `CLAUDE.md` and any design notes it points at for the area the plan changes
3. If `--req` is provided, read the original requirements
4. If `--context` is provided, read those files
5. Detect the mode (`--spec` or default)

### Step 2: Run the Lenses in Parallel

One message, one `Agent` call per lens, `subagent_type="Explore"`. Each prompt is the mode's prompt template below, prefixed with:

```
Review through one lens only: [lens name]. [The lens's "Asks" text.]
You may read the repository to check the plan's claims against what exists.
```

and ending with:

```
Report findings only. Do not edit any file.
```

### Step 3: Synthesize with the 80/20 Filter

A lens is a fresh reader, not an authority. **Verify each claimed blocker yourself** against the plan and the code before accepting it.

1. **MUST FIX**: meets the blocker criteria AND you have verified it
2. **SHOULD FIX**: valid but not blocking
3. **DEFER**: valid concerns for future iterations
4. **SKIP**: over-engineering suggestions, rejected

**Be ruthless.** Most reviewer "warnings" are nice-to-haves.

### Step 4: Offer to Update the Plan

After presenting the results, **always ask the user whether to update the plan file**. Use `AskUserQuestion`:

> "Would you like me to update `<plan-file>` with the MUST FIX and SHOULD FIX changes?"

Options:
- **Yes, apply all**: apply MUST FIX and SHOULD FIX changes to the plan file
- **MUST FIX only**: apply only the blocker fixes
- **No, just the review**: leave the plan file unchanged

If the user chooses to update, edit the plan file in place, preserving its structure and touching only the sections the findings affect.

## Prompt Templates

### Technical Review Prompt (Default)

```
Review this implementation plan for readiness to code.

## The Plan
<plan>
[paste plan content]
</plan>

## Original Requirements (if provided)
<requirements>
[paste requirements]
</requirements>

## CRITICAL: Apply 80/20 Thinking

Focus on the 20% of issues that cause 80% of problems.

**A MUST FIX blocker is ONLY:**
- Missing step that makes implementation impossible
- Data integrity risk (orphaned records, corruption, loss)
- Security vulnerability (injection, auth bypass, SSRF)
- Undefined contract that blocks integration
- Ambiguity requiring guesswork during implementation
- Contradiction with a documented project invariant

**NOT a blocker (put in DEFER or SKIP):**
- Missing docs, imperfect error messages
- Edge cases affecting under 1% of users
- "Best practices" not strictly required
- Future-proofing for hypothetical scenarios

## Evaluate (priority order)

1. **Feasibility**: Do the required APIs and functions exist?
2. **Risks**: Data integrity? Security? Breaking changes?
3. **Ambiguity**: Can a developer implement without guessing?
4. **Scope**: What can be cut for an MVP?
5. **Project fit**: Does it honor the conventions and invariants in the project's CLAUDE.md?

## Output

1. **MUST FIX** (0-3 max): What breaks? How to fix?
2. **SHOULD FIX** (0-5): Why it matters, why not blocking
3. **DEFER**: Valid for later
4. **SKIP**: Over-engineering to reject
5. **VERDICT**: READY / NEEDS WORK / MAJOR REVISION
```

### Spec Review Prompt (`--spec` flag)

```
Review this plan's REQUIREMENTS for completeness and clarity.
DO NOT review technical implementation, only requirements.

## The Plan
<plan>
[paste plan content]
</plan>

## Original User Request (if provided)
<request>
[paste original request]
</request>

## CRITICAL: Requirements-Only Review

You are validating "are we building the right thing?" NOT "can we build it?"

**A MUST FIX blocker is ONLY:**
- User requirement from the original request NOT captured in the plan
- Acceptance criteria that cannot be tested or verified
- Missing "Out of Scope" section (scope creep risk)
- Contradictory or ambiguous requirements
- Unstated assumptions that could surprise stakeholders

**NOT a blocker (put in DEFER or SKIP):**
- Missing implementation details
- Technical approach concerns
- Nice-to-have features not in the original request
- Imperfect wording that is still clear

## Evaluate

1. **Completeness**: Every requirement from the original request captured?
2. **Testability**: Each requirement has verifiable acceptance criteria?
3. **Scope Boundaries**: "Out of Scope" section exists and is clear?
4. **Clarity**: Could a stakeholder approve without asking questions?
5. **Assumptions**: Are implicit assumptions made explicit?

## Output

1. **MUST FIX** (0-3 max): Missing or unclear requirements
2. **SHOULD FIX** (0-5): Improvements to clarity
3. **DEFER**: Nice-to-haves for future
4. **SKIP**: Scope creep suggestions to reject
5. **VERDICT**: READY / NEEDS WORK / MAJOR REVISION
```

## Output Format

```markdown
## Plan Review: [plan-name]
### Mode: Technical / Spec

### MUST FIX (Blockers)
| Issue | Found By | What Breaks | Fix |
|-------|----------|-------------|-----|
| [description] | [lens] | [why blocked] | [fix] |

*If empty: "None, the plan is ready"*

### SHOULD FIX (Quality Improvements)
- [Issue] - [why it matters but is not blocking]

### DEFER (Future Iterations)
- [Issue] - [why it can wait]

### SKIP (Rejected Suggestions)
- [Suggestion] - [why this is over-engineering or scope creep]

### What's Good
- [Validated aspects]

---

### Verdict: READY / NEEDS WORK / MAJOR REVISION
### Confidence: HIGH/MEDIUM/LOW
```

## Verdict Criteria

| Verdict | Criteria |
|---------|----------|
| **READY** | 0 MUST FIX items. Proceed. |
| **NEEDS WORK** | 1-2 MUST FIX items. Quick fixes needed. |
| **MAJOR REVISION** | 3+ MUST FIX or a fundamental flaw. Rethink. |

## Examples

```bash
# Requirements validation first
/review-plan implementation-plans/feature.md --spec

# Then technical review
/review-plan implementation-plans/feature.md

# With the original user request for comparison
/review-plan implementation-plans/feature.md --spec --req "User asked for X with Y"

# Quick check
/review-plan implementation-plans/small-fix.md --quick
```

## Tips

- **Run `--spec` before default.** There is no point validating the technical approach if the requirements are wrong.
- **Be skeptical of reviewer "blockers".** Most are SHOULD FIX or DEFER.
- **Parallel execution.** Launch all lenses in a single message.
- **Trust judgment.** If it feels like over-engineering, it probably is.
