Adopt the persona named in $ARGUMENTS and work through open issues and PRs until the session ends or no work remains.

**Autonomous operation**: Do not ask for confirmation at any point. Invoking this skill is authorization to do all the work. Never ask "want me to proceed?", "shall I work on this?", "should I fetch the issue?", or any similar question. Scan → pick → act, without pausing.

## Argument Parsing

Parse `$ARGUMENTS` as one of:

| Form | Meaning |
|---|---|
| `<persona>` | Work through items once, then stop |
| `<persona> <minutes>` | Work continuously, polling every ~4.5 min for `<minutes>` minutes |
| `<persona> until:<iso_timestamp>` | Work continuously until absolute deadline (used by self-scheduling wake-ups) |

Valid persona names: `developer`, `test-engineer`, `security`, `qa`, `gitops-manager`, `docs`, `merge-manager`, `product-designer`, `triage`

If the persona name is not in that list, stop immediately and print:
```
Unknown persona: "<name>". Valid personas: developer, test-engineer, security, qa, gitops-manager, docs, merge-manager, product-designer, triage
```
Do not proceed further.

On first call with `<minutes>`, compute `end_time = now + <minutes> minutes` as ISO 8601 UTC. Subsequent self-scheduled wake-ups pass `until:<end_time>` to preserve the deadline.

---

## Step 1 — Adopt the Persona

You ARE the `<persona>`. Read `CLAUDE.md` now to confirm:
- Which files you are allowed to modify
- Which branch you operate on
- Any hard rules that apply to this persona

**Branch validation by persona:**

- **developer**: Valid starting states are `develop` or `feature/developer/*`. If on `develop`, you will create a feature branch in Step 4 when picking up an issue. If on `feature/developer/<name>`, continue work on that branch. If on any other branch, stop and tell the user.
- **test-engineer**: Valid starting states are `develop`, `feature/developer/*`, or `persona/test-engineer`. You will select the correct branch in Step 2 based on available work. If on any other branch, stop and tell the user.
- **All other personas**: The current branch must match your persona's designated branch exactly. If not, tell the user which branch to switch to and stop.

If persona is `triage`: print "The triage persona is invoked via /triage, not /watch-work. Run /triage instead." and stop.

---

## Step 2 — Scan for Work (token-efficient, one pass)

> **test-engineer only — branch selection before scanning:**
> Run the issue and PR queries below first (without checking out anything yet), then apply this logic:
> 1. If there are open `feature/developer/*` PRs or issues labeled `persona/test-engineer` **and** `type/task` associated with a feature branch: prioritize those. Run `git fetch origin && git checkout feature/developer/<name>`.
> 2. Otherwise if there are issues labeled `persona/test-engineer` for test infrastructure work: run `git checkout persona/test-engineer && git pull`.
> 3. If both feature and infra work exist, choose feature work (bugs block shipping; infra can wait).
> 4. If no work exists, go to Step 5 (empty queue).

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
>   --json number,title,headRefName,reviewDecision,comments,reviewRequests \
>   --limit 30 \
>   --jq '[.[] | select(
>       (.headRefName | startswith("feature/developer/")) or
>       (.headRefName | startswith("persona/")) or
>       (.reviewRequests | length > 0)
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

Do not announce the item and wait. Fetch details and begin immediately.

For the selected item:

1. **Fetch full details:**
   - Issue: `gh issue view <n>`
   - PR: `gh pr view <n> --comments`

2. **Understand what's needed.** Read only the files required to complete the task — no broad codebase exploration.

3. **Make the changes** within your persona's designated file scope (from CLAUDE.md). Do not touch files owned by other personas.

   > **developer — branch lifecycle:**
   > - If currently on `develop`: derive a branch slug from the issue title and number (e.g. `feature/developer/123-add-node-metrics`), then run `git checkout -b feature/developer/<slug> origin/develop` before making any changes.
   > - After the PR for this issue is merged: `git checkout develop && git pull origin develop && git branch -d feature/developer/<slug>`.
   > - Start the next loop from `develop`.

   > **test-engineer — branch cleanup:**
   > - After completing work on a feature branch: push, then `git checkout develop && git pull origin develop`.
   > - After completing test-infra work on `persona/test-engineer`: push, then `git checkout develop && git pull origin develop`.
   > - Start the next loop from `develop`.

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

5b. **Verify the commit SHA exists** — run `git rev-parse --verify <sha>` immediately after committing. Do not reference a SHA in any issue comment or PR description unless this command returns successfully. If the command fails, the commit did not happen — do not fabricate or guess a SHA.

5c. **Open a PR** (branch-owning personas only — all except `merge-manager` and `product-designer`): if no open PR already targets `develop` for this branch, open one now with `gh pr create`. Work is not done until a PR is open. Do not close the issue before the PR exists.

5d. **Handoff check** — before commenting on the issue, ask: does completing this work create a dependency for another persona? Common examples:
   - Security approves RBAC change → developer can now add the marker
   - Developer completes implementation → test-engineer can now write tests
   - PR is open and CI passes → merge-manager can review

   If yes, you must execute the handoff before marking the issue complete:
   - Re-label the current issue from `persona/<you>` to `persona/<next>` and comment with what was done and exactly what the next persona must do, OR
   - Create a new issue labeled `persona/<next>` with explicit instructions, then close your issue

   **Do not comment with a commit SHA or close the issue until the handoff action is complete.**

6. **Update the issue/PR:** comment must include all of the following — do not summarize or paraphrase, paste the actual output:
   - Output of `git log --oneline -1` (proves the commit exists with its real message and SHA)
   - PR URL (from `gh pr create` output or `gh pr view --json url --jq '.url'`)
   - For any file-level fix: output of a `grep` or `head` command confirming the change is present in the file (e.g. `grep '^FROM golang:' Dockerfile`)

   Fabricated or assumed output will be caught by the merge-manager. If you cannot produce real output, the commit did not happen — do not close the issue.

---

## Step 5 — Loop or Schedule

After completing an item (or finding an empty queue):

**Single-check mode** (no duration): stop and print a summary of what was completed.

**Watch mode:**
- Is `now < end_time`?
  - **Yes and work was just completed**: immediately return to Step 2 to check for more work.
  - **Yes and queue was empty**: call `ScheduleWakeup` with `delaySeconds: 270`, `reason: "Polling for new work for <persona>"`, `prompt: "Resume watch-work: you are the <persona> persona. Continue from Step 2 — scan for open issues and PRs, pick the highest-priority item, do the work, then loop. Session ends at <end_time_iso>. Do not use slash commands; invoke the watch-work skill directly via the Skill tool with skill name 'watch-work' and args '<persona> until:<end_time_iso>'."`
  - **No**: print `Session complete for <persona>. Items completed this session: <N>.` and stop.

---

## Token Efficiency Rules

1. `--json <field-list>` on every `gh` call — never omit the field list.
2. `--jq` on every list call — only formatted strings reach context, not raw JSON.
3. Read only files needed for the current task — no broad codebase exploration.
4. Never quote issue or PR body verbatim unless it is directly relevant to a code decision.
5. One issue-list call and one PR-list call per scan cycle.
