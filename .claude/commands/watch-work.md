Adopt the persona named in $ARGUMENTS and work through open issues and PRs until the session ends or no work remains.

## Argument Parsing

Parse `$ARGUMENTS` as one of:

| Form | Meaning |
|---|---|
| `<persona>` | Work through items once, then stop |
| `<persona> <minutes>` | Work continuously, polling every ~4.5 min for `<minutes>` minutes |
| `<persona> until:<iso_timestamp>` | Work continuously until absolute deadline (used by self-scheduling wake-ups) |

Valid persona names: `developer`, `test-engineer`, `security`, `qa`, `gitops-manager`, `docs`, `merge-manager`, `product-designer`

If the persona name is not in that list, stop immediately and print:
```
Unknown persona: "<name>". Valid personas: developer, test-engineer, security, qa, gitops-manager, docs, merge-manager, product-designer
```
Do not proceed further.

On first call with `<minutes>`, compute `end_time = now + <minutes> minutes` as ISO 8601 UTC. Subsequent self-scheduled wake-ups pass `until:<end_time>` to preserve the deadline.

---

## Step 1 — Adopt the Persona

You ARE the `<persona>`. Read `CLAUDE.md` now to confirm:
- Which files you are allowed to modify
- Which branch you operate on
- Any hard rules that apply to this persona

Confirm the current branch matches your persona's branch. If not, tell the user which branch to switch to and stop.

---

## Step 2 — Scan for Work (token-efficient, one pass)

**Open issues assigned to this persona:**
```bash
gh issue list \
  --state open \
  --label "persona/<persona-name>" \
  --json number,title,labels,updatedAt \
  --limit 25 \
  --jq '.[] | "#\(.number)  \(.title)  \([.labels[].name] | join(","))  [updated \(.updatedAt[:10])]"'
```

**Open PRs needing this persona's attention:**

> If persona is `merge-manager`: fetch ALL open PRs targeting `develop` — the merge-manager reviews every PR, not just ones it owns.
> ```bash
> gh pr list \
>   --state open \
>   --base develop \
>   --json number,title,headRefName,reviewDecision,statusCheckRollup,comments \
>   --limit 30 \
>   --jq '.[] | "#\(.number)  \(.title)  [\(.headRefName)]  review:\(.reviewDecision // "PENDING")  ci:\(if (.statusCheckRollup // [] | length) == 0 then "unknown" elif (.statusCheckRollup | all(.[]; .state == "SUCCESS")) then "green" else "failing" end)  comments:\(.comments | length)"'
> ```

> All other personas: fetch only PRs on their branch or where they are a requested reviewer.
> ```bash
> gh pr list \
>   --state open \
>   --json number,title,headRefName,reviewDecision,comments,requestedReviewers \
>   --limit 30 \
>   --jq '[.[] | select(
>       (.headRefName | startswith("feature/developer/")) or
>       (.headRefName | startswith("persona/")) or
>       (.requestedReviewers | length > 0)
>   )] | .[] | "#\(.number)  \(.title)  [\(.headRefName)]  review:\(.reviewDecision // "PENDING")  comments:\(.comments | length)"'
> ```

Print the results as a brief queue, then proceed to Step 3.

---

## Step 3 — Pick the Highest-Priority Item

**If persona is `merge-manager`**, use this priority order:

| Priority | Condition |
|---|---|
| 1 | PR with `ci:green` and `review:APPROVED` — ready to merge now |
| 2 | PR with `ci:failing` — create or update a blocking issue labeled `persona/<owner>` and `type/bug` |
| 3 | PR with open `type/security` issues on its branch — comment that it is blocked |
| 4 | PR with `review:CHANGES_REQUESTED` — already handled by the owning persona; add a comment if stale |
| 5 | PR with `review:PENDING` and `ci:green` — leave a review |

**All other personas**, score every item and pick the highest. In case of a tie, prefer the oldest `updatedAt`.

| Priority | Condition |
|---|---|
| 1 | PR on your branch with `review:CHANGES_REQUESTED` — blocking a merge |
| 2 | PR where you are a requested reviewer — blocking the PR author |
| 3 | Issue labeled `type/bug` + `phase/1` |
| 4 | Issue labeled `type/bug` + `phase/2` (or higher) |
| 5 | Issue labeled `type/task` + `phase/1` |
| 6 | Issue labeled `type/task` + `phase/2` (or higher) |
| 7 | Anything else, oldest `updatedAt` first |

To extract phase from an issue's labels: look for a label matching `phase/<n>` and read `<n>` as an integer. If no phase label is present, treat it as phase 99 (lowest).

If the queue is empty, go to Step 5.

---

## Step 4 — Do the Work

For the selected item:

1. **Fetch full details:**
   - Issue: `gh issue view <n>`
   - PR: `gh pr view <n> --comments`

2. **Understand what's needed.** Read only the files required to complete the task — do not explore the codebase broadly.

3. **Make the changes** within your persona's designated file scope (from CLAUDE.md). Do not touch files owned by other personas.

4. **Validate:**
   - If you modified `*.go` files: `make test`
   - If you modified manifests: `make manifests && git diff --exit-code config/rbac/role.yaml`
   - Docs/YAML changes: no build step required

5. **Commit** using conventional commit style:
   ```
   <type>(<scope>): <description>
   
   Closes #<issue-number>
   
   Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
   ```

6. **Update the issue/PR:** comment with a one-line summary of what was done and link the commit SHA.

---

## Step 5 — Loop or Schedule

After completing an item (or finding an empty queue):

**Single-check mode** (no duration): stop and print a summary of what was completed.

**Watch mode:**
- Is `now < end_time`?
  - **Yes and work was just completed**: immediately return to Step 2 to check for more work.
  - **Yes and queue was empty**: call `ScheduleWakeup` with `delaySeconds: 270`, `reason: "Polling for new work for <persona>"`, `prompt: "/watch-work <persona> until:<end_time_iso>"`
  - **No**: print `Session complete for <persona>. Items completed this session: <N>.` and stop.

---

## Token Efficiency Rules

1. `--json <field-list>` on every `gh` call — never omit the field list.
2. `--jq` on every list call — only formatted strings reach context, not raw JSON.
3. Read only files needed for the current task — no broad codebase exploration.
4. Never quote issue or PR body verbatim unless it is directly relevant to a code decision.
5. One issue-list call and one PR-list call per scan cycle.
