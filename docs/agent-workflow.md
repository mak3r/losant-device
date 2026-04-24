# Agent Workflow

This document describes how the AI agent personas collaborate in this repository.

## Branch Structure

```
main
└── develop
    ├── feature/developer/<name>     developer + test-engineer collaborate here
    ├── develop/security             security persona only
    ├── develop/qa                   QA persona only
    ├── develop/gitops-manager       GitOps manager persona only
    ├── develop/test-engineer        test infrastructure only (not feature tests)
    └── develop/docs                 auto-updated by docs agent on every merged PR
```

Feature branches are short-lived and created per-task. Long-lived persona branches (`develop/security`, etc.) accumulate that persona's ongoing work and PR into `develop` when stable.

## Personas

### Developer

Works on `feature/developer/<name>` branches. Implements all application logic:
- CRD type definitions (`api/`)
- Controller reconciliation logic (`internal/controller/`)
- Losant REST client (`internal/losant/`)
- GEA HTTP client (`internal/gea/`)
- Health collectors and HealthStore (`internal/monitor/`)
- Scheduler (`internal/scheduler/`)
- Provisioner (`internal/provisioner/`)
- Controller entry point (`cmd/main.go`)

The developer does not write tests (that is the test engineer's role) and does not modify `helm/`, `config/`, or CI workflows.

### Test Engineer

Works **in the developer's feature branch** (`feature/developer/<name>`). Does not have separate feature branches. Writes:
- `*_test.go` files alongside developer's implementation
- Mock implementations (`internal/*/mock_*.go`)
- Test fixtures and helpers (`test/`)

The test engineer and developer collaborate until `make test` passes on the shared branch before a PR is opened.

**Test engineer's own branch** (`develop/test-engineer`) is reserved exclusively for test infrastructure:
- Ginkgo/Gomega suite setup (`test/integration/suite_test.go`)
- envtest cluster configuration
- Shared mock factories
- Test helper utilities used across multiple test files

The test engineer never modifies non-test `.go` files. If a bug is found while writing tests, a GitHub issue is opened (labeled `persona/developer`, `type/bug`) and the developer fixes it.

### Security

Works on `develop/security`. Reviews and hardens:
- RBAC manifests (`config/rbac/`) — least-privilege, no wildcards
- Secret handling — verifies credentials are never logged
- CI security scanning — secret detection, image scanning
- TLS configuration for any external endpoints

The security persona never modifies application logic. Security findings are raised as GitHub issues (`type/security`) before merge.

### QA

Works on `develop/qa`. Owns:
- End-to-end tests against real k3s clusters (`test/e2e/`)
- Acceptance criteria documentation (`docs/acceptance-criteria.md`)
- Runbook for common failure scenarios (`docs/runbook.md`)

QA does not modify functional implementation code.

### GitOps Manager

Works on `develop/gitops-manager`. Owns:
- Helm chart (`helm/`)
- Kustomize manifests (`config/`)
- GitHub Actions workflows (`.github/workflows/`)
- Makefile
- GEA Kubernetes manifests (`config/gea/`)

The GitOps manager does not modify `internal/**` or `api/**`.

### Docs Agent

Automated — runs via `.github/workflows/docs-agent.yml` on every merged PR. Works only on `develop/docs`. Updates:
- `docs/**`
- `README.md`
- `CLAUDE.md`
- CRD field description comments

Never touches `*.go` files, test files, or Helm templates.

### Merge Manager

**Does not commit code.** Sole responsibilities:

1. **Gate checking**: runs `make test`, checks CI status, checks for open `type/security` blockers
2. **Issue creation**: when a gate fails, creates a GitHub issue with the responsible persona label and links it to the failing PR
3. **PR notification**: comments on the PR with the issue link; the PR author must fix and update
4. **Conflict flagging**: when branches conflict, creates issues for both responsible personas; never resolves conflicts itself
5. **Release tagging**: creates the `develop → main` release PR and applies version tag when all gates pass

## PR Lifecycle

```
Developer creates feature/developer/<name>
    ↓
Test engineer joins the same branch, writes tests
    ↓
Both verify: make test passes locally
    ↓
Developer opens PR: feature/developer/<name> → develop
    ↓
Merge manager runs gate checks
    ├── Gate fails → create issue (persona/developer) → comment on PR → STOP
    └── Gate passes → approve + merge to develop
                          ↓
                   Docs agent fires (develop/docs PR)
```

## Issue Routing

All issues must have:
- `persona/<name>` — who is responsible for fixing it
- `phase/<n>` — which implementation phase it belongs to
- `type/task`, `type/bug`, or `type/security`

The merge manager uses `persona/` labels to determine who to notify and which PRs to block.

## Release Process

1. All Phase N issues closed, `develop` branch CI green
2. Merge manager creates PR: `develop → main` with title `Release vX.Y.Z`
3. Merge manager bumps `internal/version/version.go` (developer reviews)
4. PR approved, merged, tag applied: `git tag vX.Y.Z`
5. Docs agent fires and updates changelog in `develop/docs`
