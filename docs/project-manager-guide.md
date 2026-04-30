# Project Manager Guide

This guide covers how to set up and operate the multi-persona Claude agent system for `losant-device`. It assumes familiarity with git and the terminal but not with git worktrees or the Claude Code multi-persona pattern.

---

## Workspace Layout

The agent workspace lives at `~/projects/losant-device-agents/` as a separate git clone from the planning/organizing repo (`~/projects/losant-device`). Most personas use git worktrees so each Claude Code session is automatically isolated to the correct branch.

The `developer` and `test-engineer` personas use **standalone clones** instead of worktrees, because their branches change dynamically and the agents manage their own branch lifecycle autonomously.

```
~/projects/losant-device-agents/
├── main/            ← worktree, develop branch  (merge-manager runs here)
├── security/        ← worktree, persona/security
├── qa/              ← worktree, persona/qa
├── gitops/          ← worktree, persona/gitops-manager
├── docs/            ← worktree, persona/docs
├── product/         ← worktree, persona/product-designer
├── developer/       ← standalone clone, starts on develop
└── test-engineer/   ← standalone clone, starts on develop
```

---

## Initial Setup

Run these steps once when creating the workspace for the first time.

### 1. Create the root clone and worktrees

```bash
# Create the root clone (used as the worktree source)
git clone git@github.com:mak3r/losant-device.git ~/projects/losant-device-agents/main
cd ~/projects/losant-device-agents/main
git checkout develop

# Create worktrees for static personas
git worktree add ../security  persona/security
git worktree add ../qa        persona/qa
git worktree add ../gitops    persona/gitops-manager
git worktree add ../docs      persona/docs
git worktree add ../product   persona/product-designer
```

### 2. Create standalone clones for developer and test-engineer

```bash
cd ~/projects/losant-device-agents
git clone git@github.com:mak3r/losant-device.git developer
git -C developer checkout develop

git clone git@github.com:mak3r/losant-device.git test-engineer
git -C test-engineer checkout develop
```

### 3. Propagate settings

If you have a `settings.local.json` (API key, permissions), symlink it into every directory so all personas share one config:

```bash
for dir in main security qa gitops docs product developer test-engineer; do
  ln -s ~/projects/losant-device-agents/main/.claude/settings.local.json \
        ~/projects/losant-device-agents/$dir/.claude/settings.local.json
done
```

### 4. Initialize each workspace

Open a terminal in each directory, launch `claude`, and run `/persona-setup`. This downloads Go modules and installs the required toolchain binaries (`controller-gen`, `kustomize`, `envtest`, `golangci-lint`) into `./bin/`.

---

## Starting a Persona Session

Open a terminal in the persona's directory, launch `claude`, and run `/watch-work <persona>`. That is all. The agent will scan for open issues and PRs, pick the highest-priority item, and work through the queue until the session ends or no work remains.

To run a session for a fixed duration (e.g. 60 minutes):

```
/watch-work <persona> 60
```

| Directory        | Command                              |
|------------------|--------------------------------------|
| `main/`          | `/watch-work merge-manager`          |
| `security/`      | `/watch-work security`               |
| `qa/`            | `/watch-work qa`                     |
| `gitops/`        | `/watch-work gitops-manager`         |
| `docs/`          | `/watch-work docs`                   |
| `product/`       | `/watch-work product-designer`       |
| `developer/`     | `/watch-work developer`              |
| `test-engineer/` | `/watch-work test-engineer`          |

You never need to create or switch branches for `developer` or `test-engineer` — the agents handle that autonomously (see below).

---

## Developer Feature Lifecycle (Agent-Managed)

The developer agent starts from the `develop` branch each loop iteration:

1. Scans for open issues labeled `persona/developer`
2. Picks the highest-priority issue
3. Derives a branch slug from the issue title and number (e.g. `feature/developer/123-add-node-metrics`)
4. Runs `git checkout -b feature/developer/<slug> origin/develop`
5. Implements the feature, runs `make test`, commits, and opens a PR
6. After the PR is merged: `git checkout develop && git pull origin develop && git branch -d feature/developer/<slug>`
7. Loops back to issue selection

No human action is needed. Do not create worktrees or branches for the developer manually.

---

## Test-Engineer Pairing (Agent-Managed)

The test-engineer agent also starts from `develop` each loop iteration and selects its target branch based on available work:

1. Scans for open `feature/developer/*` PRs and issues labeled `persona/test-engineer`
2. If there are open developer feature PRs needing tests: checks out `feature/developer/<name>` and writes `*_test.go` files alongside the implementation
3. If there is test-infrastructure work (issues on `persona/test-engineer`): checks out `persona/test-engineer` and works there
4. If both exist, feature work takes priority (shipping blockers come first)
5. After completing work: pushes, then returns to `develop`

No human action is needed. The agent selects the right branch automatically.

---

## Merge Manager

The merge manager runs from `main/` (develop branch). It does not commit code — it only uses the `gh` CLI to review PRs, create blocking issues, and merge approved work.

On each loop it:
1. Checks all open PRs targeting `develop`
2. Merges PRs that are CI-green and approved
3. Creates blocking issues for failing CI or open security concerns
4. Leaves reviews on PRs that are pending

No dedicated branch or worktree is needed — `main/` on `develop` is sufficient.

---

## Organizing Work

Use `~/projects/losant-device` (the original planning clone) to:

- Review architecture documents in `docs/` and `.claude/plans/`
- Create and triage GitHub issues
- Review PRs and check CI status
- Direct personas by assigning labels and milestones

Agents in `~/projects/losant-device-agents/` do the implementation. The two repos share the same GitHub remote, so issues and PRs are visible from both.

---

## Memory

Each directory gets its own Claude memory namespace keyed to its filesystem path. Memories accumulate over sessions and help the agent recall project context, preferences, and prior decisions. No action is needed — memory persists automatically.
