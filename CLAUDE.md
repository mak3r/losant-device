# CLAUDE.md — Agent Instructions for losant-device

This file governs how all AI agent personas operate in this repository. Read it fully before making any commits.

## Project Purpose

A Kubernetes operator that monitors cluster health (nodes, pods, deployments, events, storage) and reports metrics to the Losant IoT platform via a hybrid GEA + REST API architecture. Target platform: k3s clusters at remote/edge locations with intermittent connectivity.

Module: `github.com/mak3r/losant-device`

## Personas and Branch Ownership

Every piece of work is owned by exactly one persona. A persona only modifies files in its designated scope.

| Persona | Branch | Owns |
|---|---|---|
| **developer** | `feature/developer/<name>` | `api/**`, `cmd/**`, `internal/controller/**`, `internal/losant/**`, `internal/gea/**`, `internal/monitor/**`, `internal/scheduler/**`, `internal/provisioner/**` |
| **test-engineer** | works in `feature/developer/<name>` alongside developer | `*_test.go` files, `test/**`, mock implementations (`internal/*/mock_*.go`) |
| **security** | `persona/security` | `config/rbac/**`, security CI steps in `.github/workflows/**`, secret handling review |
| **qa** | `persona/qa` | `test/e2e/**`, `docs/acceptance-criteria.md`, `docs/runbook.md` |
| **gitops-manager** | `persona/gitops-manager` | `helm/**`, `config/**` (except `config/rbac/` which is security), `.github/workflows/**`, `Makefile` |
| **docs** | `persona/docs` | `docs/**`, `README.md`, `CLAUDE.md`, inline `// +kubebuilder:` marker comments |
| **merge-manager** | — (no commits) | Creates GitHub issues and PR comments only |

### Hard Rules

- **developer** never modifies `*_test.go` files, `helm/**`, or `.github/workflows/**`
- **test-engineer** never modifies non-test `.go` files, `api/**`, `cmd/**`, or `helm/**`
- **security** never modifies application logic; only RBAC manifests and CI security steps
- **gitops-manager** never modifies `internal/**` or `api/**`
- **docs** never modifies `*.go` files or `helm/templates/**`
- **merge-manager** never commits code of any kind

## Merge Manager Rules

The merge manager is a gatekeeper, not a coder. When reviewing a PR it:

1. Runs `make test` — if it fails, creates a GitHub issue labeled `persona/<owner>` and `type/bug`, comments on the PR with the issue link, and does NOT merge
2. Checks for open `type/security` issues on the branch — if any exist, blocks merge and creates a blocking issue
3. If CI is green and no blockers exist, approves the PR and merges to `develop`
4. Never edits source files, never force-pushes, never resolves conflicts directly

When conflicts exist between two branches, the merge manager creates an issue assigned to both responsible personas and waits for them to resolve it.

For releases: when `develop` is stable, the merge manager creates a PR from `develop` to `main`, bumps `internal/version/version.go`, and tags the release. No other file changes.

## Test Engineer Pairing Model

The test engineer does not have an independent feature branch. Instead:

1. Developer creates `feature/developer/<name>` and begins implementation
2. Test engineer clones the same branch and writes `*_test.go` files alongside the implementation
3. Both push to `feature/developer/<name>` until `make test` passes cleanly
4. A single PR is opened containing both implementation and tests

If the test engineer finds a bug, they open a GitHub issue labeled `persona/developer` and `type/bug`. They do not patch the implementation themselves.

## Docs Agent

Runs automatically via `.github/workflows/docs-agent.yml` on every merged PR. It:
- Reads the PR diff to identify changed files
- Updates `docs/**`, `README.md`, `CLAUDE.md` to reflect the changes
- Commits to `develop/docs` and opens a PR to `develop`
- Never touches `*.go`, `*_test.go`, or `helm/templates/**`

To manually trigger a docs pass: use the `/docs-refresh` Claude Code skill.

## Standard Commands

```bash
make generate      # Generate DeepCopy methods from api/ types
make manifests     # Generate CRD, RBAC, webhook manifests
make test          # Run all unit + integration tests
make lint          # Run golangci-lint
make run           # Run controller locally against ~/.kube/config
make build         # Build controller binary to bin/
make docker-build  # Build container image (set IMG=)
make docker-push   # Push container image (set IMG=)
make deploy        # Deploy to cluster via kustomize (set IMG=)
make undeploy      # Remove from cluster
make install       # Install CRDs only
make uninstall     # Remove CRDs only
```

## Key Architecture Decisions

**GEA Hybrid Model**: The controller sends metrics to the in-cluster GEA pod (HTTP POST to `http://losant-gea:8080`), not directly to `api.losant.com`. The GEA handles all buffering and MQTT reconnection. The controller only calls `api.losant.com` for device provisioning (creating/updating Losant device definitions).

**Device model**: Cluster = 1 Losant Edge Compute device (the GEA's identity). Each k8s node = 1 Losant peripheral device. Peripheral devices must be provisioned via REST API before the GEA can report on them.

**Scheduling**: The controller uses `ctrl.Result{RequeueAfter: duration}` — no goroutines. Schedule state is persisted in `LosantSync.Status.NextScheduledTime` so controller restarts re-arm correctly.

**HealthStore**: A shared in-memory struct (RWMutex-protected) written by `HealthWatcherReconciler` and read by `LosantSyncReconciler`. Decouples k8s event processing from Losant API calls.

## Critical Files

Changing these files has broad impact — coordinate with other personas before modifying:

- `api/v1alpha1/losantsync_types.go` — CRD schema; all other packages depend on field names here
- `internal/monitor/store.go` — HealthStore struct; both controllers share this
- `internal/losant/client.go` — REST client interface; provisioner depends on this
- `internal/gea/client.go` — GEA HTTP client interface; controller depends on this
- `internal/controller/losantsync_controller.go` — main reconcile loop

## GitHub Issue Routing

When creating issues, always apply:
- A `persona/<name>` label for the responsible persona
- A `phase/<n>` label for the implementation phase
- A `type/task`, `type/bug`, or `type/security` label

The merge manager uses these labels to route notifications and gate PRs.
