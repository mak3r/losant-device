Review open GitHub issues and PRs for a persona, optionally looping until a session deadline.

## Argument Parsing

Parse `$ARGUMENTS` as one of:

| Form | Meaning |
|---|---|
| `<persona>` | Single check, then stop |
| `<persona> <minutes>` | Loop every ~4.5 min for `<minutes>` minutes |
| `<persona> until:<iso_timestamp>` | Loop until absolute deadline (used by self-scheduling wake-ups) |

Valid persona names: `developer`, `test-engineer`, `security`, `qa`, `gitops-manager`, `docs`, `merge-manager`, `product-designer`

On first call with `<minutes>`, compute `end_time = now + <minutes> minutes` as an ISO 8601 UTC string (e.g. `2026-04-30T16:00:00Z`). All subsequent self-scheduled wake-ups pass `until:<end_time>` so the deadline survives across sessions.

---

## Work-Check Cycle (run once per wake-up)

### 1 — Open issues for this persona

```bash
gh issue list \
  --state open \
  --label "persona/<persona-name>" \
  --json number,title,updatedAt \
  --limit 25 \
  --jq '.[] | "#\(.number)  \(.title)  [updated \(.updatedAt[:10])]"'
```

Print the results verbatim. If there are zero results, print `  (none)`.

Only fetch an issue's full body (`gh issue view <n>`) if the user asks you to act on it or if you need the body to determine what action is required. Do **not** read it proactively.

### 2 — PRs needing this persona's attention

```bash
gh pr list \
  --state open \
  --json number,title,headRefName,reviewDecision,comments,requestedReviewers \
  --limit 30 \
  --jq '[.[] | select(
      (.headRefName | startswith("feature/developer/")) or
      (.headRefName | startswith("persona/")) or
      (.requestedReviewers | length > 0)
  )] | .[] | "#\(.number)  \(.title)  [\(.headRefName)]  review:\(.reviewDecision // "PENDING")  comments:\(.comments | length)"'
```

Print results verbatim. For each PR that shows `review:CHANGES_REQUESTED` or `comments:` > 0, add a note: `  → fetch comments with: gh pr view <n> --comments` but do **not** fetch them automatically. Only fetch comments if you need to understand what to address.

### 3 — Output format

```
=== watch-work: <persona> @ <HH:MM UTC> | session ends <HH:MM UTC or "single check"> ===
ISSUES (<N> open):
  #42  Fix provisioner crash  [updated 2026-04-29]
  #57  Add node label support [updated 2026-04-28]

PRs (<N>):
  #17  "Add node metrics" [feature/developer/node-metrics]  review:CHANGES_REQUESTED  comments:3
    → fetch comments with: gh pr view 17 --comments

===
```

If both sections are empty: `No open issues or PR activity for <persona> @ <HH:MM>. Next check: <HH:MM> (or "done").`

---

## Self-Scheduling Logic

After printing the report:

**If running in single-check mode** (no duration given): stop here. Output nothing more.

**If running in watch mode**:
- Check: is `now < end_time`?
- **Yes** → call `ScheduleWakeup` with:
  - `delaySeconds`: 270 (4.5 min — stays inside the 5-min prompt-cache window)
  - `reason`: `"Polling issues/PRs for <persona> (session ends <end_time>)"`
  - `prompt`: `/watch-work <persona> until:<end_time_iso>`
- **No** → output: `Watch session complete for <persona>. Ran until <end_time_iso>.` and stop.

---

## Token Efficiency Rules

1. **Fetch only named fields.** Every `gh` call must include `--json <field-list>`. Never omit the field list.
2. **Filter with `--jq`.** Use the `--jq` expressions above so only the formatted strings reach your context, not raw JSON objects.
3. **Never read local repo files** during a watch cycle unless the user explicitly asks.
4. **Never quote issue or PR body verbatim** unless directly asked. Summarise in one line if action is needed.
5. **One issue-list call and one PR-list call per cycle.** Do not make additional API calls unless the user asks you to act on a specific item.
6. **Report concisely.** One line per issue, one line per PR. No preamble, no trailing explanation.
