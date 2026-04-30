# Design: Persona Handoff and Definition of Done

**Status:** Decided  
**Date:** 2026-04-30  
**Author:** product-designer  
**Related issues:** #147 (this design), #146 (docs implementation — partial), #144 (incident)

---

## Problem Statement

Personas complete their scoped work but do not consistently hand off to the next persona in the chain. The `watch-work` queue only surfaces issues labeled for the running persona — so if the completing persona does not re-label or create a follow-up, downstream work is invisible and stalls. This requires human intervention to unstick.

Observed in issue #144: security committed an RBAC approval and commented with developer instructions but did not re-label the issue. The developer's queue never saw it.

---

## Design Decisions

### Q1: Universal or persona-specific handoff rule?

**Decision: Universal rule.**

Every persona in the chain can unlock another. Enumerating only the high-risk pairs (security → developer, developer → test-engineer, etc.) creates fragile documentation that requires updates whenever the workflow evolves. A universal principle is harder to misread and eliminates gaps.

The general rule: *A persona's work is not complete until the next persona in the chain can find and act on it.*

The docs persona has already implemented this as a section in CLAUDE.md (issue #146). The examples and DoD sections below extend that foundation.

---

### Q2: Re-label vs. new issue — when to use which?

**Decision: Context-dependent, with a clear decision rule.**

| Situation | Action |
|-----------|--------|
| The next persona is doing a second stage of the same change | **Re-label** — same issue, swap `persona/<old>` for `persona/<next>`, comment with handoff instructions |
| The next persona's work is distinct, additive, or requires a different scope | **New issue** — create `persona/<next>` issue with explicit instructions, close your issue |

**Decision rule (easy to apply):** "Can the existing issue title describe what the next persona must do?" If yes, re-label. If no, open a new issue.

**Examples:**

- Security approves RBAC change → developer adds `// +kubebuilder:rbac` marker: **Re-label**. Title "Add secrets/get verb to RBAC" describes both stages.
- Merge-manager merges a PR → docs needs to update README: **New issue**. "Merge PR #X" does not describe "Update README for provisioner flags."
- Developer completes implementation → test-engineer writes tests: **Re-label** (if tests are explicitly in scope of the original issue) or new issue (if tests were implicitly expected but never tracked).

---

### Q3: Should watch-work enforce handoff before closing an issue?

**Decision: Yes — add an explicit handoff checkpoint to the watch-work Step 4.**

Documentation alone is insufficient; the incident in #144 proves that. The skill must prompt the completing persona to answer: "Does this work unblock another persona?" before it may comment/close.

**Proposed Step 4 addition (after validation, before commit):**

> **Handoff check** — before closing the issue, ask: does completing this work make another persona's work available? If yes, execute the handoff (re-label or new issue) as part of this work item. The handoff action is a required deliverable, not a follow-on task.

This needs a docs issue to update the `watch-work` skill in `.claude/commands/watch-work.md`.

---

### Q4: Universal Definition of Done?

**Decision: Yes — add a short DoD checklist to CLAUDE.md that applies to all personas.**

Personas currently have scope rules (what files to edit) but no completion criteria. A universal DoD closes that gap.

**Proposed DoD (for every persona, every issue/PR):**

1. Changes are within this persona's designated file scope.
2. Committed; tests pass where applicable (`make test` for Go changes, `make manifests && git diff --exit-code config/rbac/role.yaml` for manifest changes).
3. Issue or PR has a one-line summary comment linking the commit SHA.
4. Handoff complete — if this work unblocks another persona, the next persona's queue has been updated (re-labeled issue or new issue created with explicit instructions).

This also needs a docs issue to add the DoD checklist to CLAUDE.md.

---

## Follow-up Issues Required

| Issue | Persona | Work |
|-------|---------|------|
| New | persona/docs | Add re-label vs. new-issue decision rule and universal DoD checklist to CLAUDE.md |
| New | persona/docs | Add handoff checkpoint to `watch-work` skill (Step 4, after validation, before commit) |

Note: issue #146 (already closed) implemented the general handoff rule and security→developer example. These new issues extend that work with the DoD and decision rule that #146 did not cover.

---

## Personas Most Commonly Unlocking Others

For documentation and testing priority:

| Completing persona | Unlocks |
|-------------------|---------|
| product-designer | Any persona (creates issues) |
| security | developer (RBAC approval) |
| developer | test-engineer (implementation available to test) |
| developer + test-engineer | merge-manager (PR ready) |
| merge-manager | docs (changes merged, docs update needed) |
| gitops-manager | merge-manager (Helm/CI changes ready for merge review) |
