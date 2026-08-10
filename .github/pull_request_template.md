<!-- PR template. Keep it tight — reviewers skip long preambles.
     Delete sections that don't apply rather than writing "N/A". -->

## Summary

<!-- 1-3 bullets. What changed and why. Use a Conventional Commit
     style title (feat:, fix:, chore:, docs:, ...) so release-please
     can pick it up. -->

## Test plan

<!-- Concrete checklist of what you verified. Replace these bullets
     with what you actually did. -->

- [ ] `make check-style` clean
- [ ] `make test` green
- [ ] `make sbom-audit` clean (no HIGH/CRITICAL CVEs)
- [ ] Manually exercised the changed behavior (describe how)

## Risk + rollback

<!-- One sentence: worst case if this lands broken, and how to roll
     back. Skip for pure refactor or docs. -->

## Related issues

<!-- Closes #N, refs #M -->
