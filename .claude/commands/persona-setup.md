Set up this Go workspace for a new persona session. Follow these steps exactly, checking state before running anything.

## Step 1 — Identify your persona

Ask the user which persona they are invoking, or infer it from the current working directory:

| Current directory ends with... | Persona |
|---|---|
| `losant-device` (main clone) | merge-manager or triage |
| `losant-device-worktrees/security` | security |
| `losant-device-worktrees/gitops-manager` | gitops-manager |
| `losant-device-worktrees/docs` | docs |
| `losant-device-worktrees/qa` | qa |
| `losant-device-worktrees/product-designer` | product-designer |
| `losant-device-worktrees/development` | developer or test-engineer |

If the directory is `development`, the active persona is either developer or test-engineer — ask the user which one if not obvious from context.

## Step 2 — Verify environment

Run the following checks and report any failures before proceeding:

```bash
# Confirm git branch matches expected persona branch
git branch --show-current

# Confirm working tree is clean (no unexpected staged changes)
git status

# Confirm .claude/commands/ is present (slash commands available)
ls .claude/commands/

# For developer/test-engineer worktrees: confirm Go module and build
go mod verify 2>&1 | tail -3
go build ./... 2>&1 | tail -5
```

If the branch does not match the persona, stop and instruct the user to start Claude from the correct worktree directory.

## Step 3 — Verify toolchain (developer and test-engineer only)

The project installs four tools into `./bin/`. Check each one:

| Binary glob | Make target |
|---|---|
| `bin/controller-gen-*` | `make controller-gen` |
| `bin/kustomize-*` | `make kustomize` |
| `bin/setup-envtest-*` | `make envtest` |
| `bin/golangci-lint-*` | `make golangci-lint` |

Run `ls bin/` and for each missing tool run its make target. Also verify envtest Kubernetes assets:

```bash
./bin/setup-envtest list --bin-path ./bin 2>/dev/null | grep "1.31.0" || echo "not downloaded"
# If not downloaded:
./bin/setup-envtest use 1.31.0 --bin-path ./bin
```

Skip this step for non-developer personas.

## Step 4 — Review persona scope

Remind the persona of their file ownership rules from CLAUDE.md:

- **security**: `config/rbac/**`, security CI steps in `.github/workflows/**`, secret handling review
- **gitops-manager**: `helm/**`, `config/**` (except `config/rbac/`), `.github/workflows/**`, `Makefile`
- **docs**: `docs/**`, `README.md`, `CLAUDE.md`, inline `// +kubebuilder:` marker comments
- **qa**: `test/e2e/**`, `docs/acceptance-criteria.md`, `docs/runbook.md`
- **developer**: `api/**`, `cmd/**`, `internal/controller/**`, `internal/losant/**`, `internal/gea/**`, `internal/monitor/**`, `internal/scheduler/**`, `internal/provisioner/**`
- **test-engineer**: `*_test.go` files, `test/**`, mock implementations (`internal/*/mock_*.go`)

Violations are caught by the merge-manager before any PR merges.

## Step 5 — Find open issues for this persona

Fetch open GitHub issues assigned to this persona's label (frozen issues are excluded):

```bash
gh issue list \
  --repo mak3r/losant-device \
  --label "persona/<name>" \
  --state open \
  --json number,title,labels,updatedAt \
  --jq '.[] | select(.labels | map(.name) | contains(["type/freeze"]) | not) | "#\(.number)  \(.title)  [updated \(.updatedAt[:10])]"'
```

Replace `<name>` with the persona name (e.g. `developer`, `security`).

Present the list to the user and ask which issue to work on, or suggest the lowest-numbered unblocked issue.

## Step 6 — Create the feature branch (developer and test-engineer only)

**For all other personas:** worktrees already exist on a fixed branch — skip to Step 7.

**For developer and test-engineer:** you are already in the correct worktree (`development/`). After picking an issue, create a feature branch from `origin/develop` within this same session:

```bash
git fetch origin
git checkout -b feature/developer/<short-descriptor> origin/develop
# e.g. git checkout -b feature/developer/rancher-session origin/develop

# Confirm branch
git branch --show-current
```

The test-engineer starts their own separate Claude session from the same `development/` directory, then checks out the same branch:

```bash
# The test-engineer runs in their own terminal:
cd ~/projects/losant-device-worktrees/development
# Then inside Claude: git checkout feature/developer/<short-descriptor>
```

**Do not create a new worktree directory per feature.** The `development/` worktree is permanent — switch branches within it for each new feature.

## Step 7 — Confirm readiness and begin work

Summarize:
- Persona name and branch
- Working directory
- Issue being worked on (title, number, URL)
- Files in scope for this persona
- Any blockers (blocked-by issues that must close first)

Then either:
- Begin working on the issue if in a valid persona worktree
- Wait for the user to start a new session in the correct worktree (developer/test-engineer case)

## Handoff reminder

When work is complete, follow the handoff protocol in CLAUDE.md before closing any issue:
1. Commit and push the branch
2. Open a PR targeting `develop`
3. Re-label or create a new issue for the next persona in the chain
4. Comment on any upstream blocking issues
