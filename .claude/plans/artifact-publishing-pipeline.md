---
title: Artifact Publishing Pipeline — Source-Independent Operator Distribution
status: approved
phase: 4-hardening
author: product-designer
date: 2026-05-04
issue: "#286"
---

# Artifact Publishing Pipeline

## Problem

The operator cannot be deployed without a local copy of the source tree. `helm install`
references the local `helm/` directory, `Chart.yaml` version fields are never updated by
CI, and no standalone CRD manifest exists as a release asset. This blocks GitOps tooling
(Flux, ArgoCD, Terraform, Ansible) and makes the project unsuitable for production
edge deployments where source checkouts are impractical or unavailable.

Three gaps must be closed:

1. **No published Helm chart** — consumers must clone the repo before installing
2. **No automated version sync** — `appVersion` and `version` in `Chart.yaml` remain
   at their placeholder values regardless of the git tag used for release
3. **No standalone CRD artifact** — GitOps tools that manage CRDs separately from
   Helm have no machine-consumable target

## Architecture Decisions

### D1: OCI registry for the Helm chart

**Decision: Publish the Helm chart as an OCI artifact to `ghcr.io/mak3r/losant-device/charts`.**

OCI registries are now the standard distribution channel for Helm charts (Helm 3.8+).
Publishing to GHCR alongside the container image keeps all artifacts in one registry,
reuses existing GHCR tokens, and requires no third-party chart museum. GitOps tools
(Flux `HelmRepository` with `type: oci`, ArgoCD `Repo`) already understand this format.

The published reference for each release will be:
```
oci://ghcr.io/mak3r/losant-device/charts/losant-device:<version>
```

### D2: Chart version sync strategy

**Decision: Inject `appVersion` and `version` from the git tag in the release CI job
using `sed` immediately before `helm package`; do not commit the modified `Chart.yaml`
back to the repo.**

Committing version bumps as part of the release flow creates circular triggers and
complicates the git history. Instead, the CI job modifies `Chart.yaml` in-place within
the runner workspace, packages the chart, publishes it, then discards the change.
The source `Chart.yaml` remains at a placeholder (e.g. `version: 0.0.0`) as a signal
that the published version is always determined by the git tag.

Version extraction:
```bash
TAG="${GITHUB_REF_NAME}"           # e.g. v0.1.0
APP_VERSION="${TAG#v}"             # e.g. 0.1.0
sed -i "s/^version:.*/version: ${APP_VERSION}/" helm/Chart.yaml
sed -i "s/^appVersion:.*/appVersion: ${APP_VERSION}/" helm/Chart.yaml
```

### D3: CRD artifact strategy

**Decision: Generate a single `crds.yaml` by concatenating all files from `config/crd/bases/`
and publish it as a GitHub Release asset alongside the container image digest.**

GitOps workflows that treat CRDs as cluster-scoped infrastructure separate from the
application chart need a stable URL to fetch the CRD manifest. GitHub Release assets
provide this with no additional infrastructure. The file is generated in the release CI
job from the committed, controller-gen–produced CRD files (not regenerated from source),
ensuring the asset matches the exact manifests tested in that release.

Generation step:
```bash
cat config/crd/bases/*.yaml > crds.yaml
gh release upload "${GITHUB_REF_NAME}" crds.yaml
```

### D4: Secret / permission model for GHCR publishing

**Decision: Use the existing `GITHUB_TOKEN` with `packages: write` permission for GHCR
Helm chart pushes; no new secrets are required.**

`helm push` to GHCR authenticates identically to `docker push`: log in to
`ghcr.io` with `GITHUB_TOKEN`. The release workflow already uses this token for
container image publishing. The security persona must verify the `packages: write`
scope is present in the workflow's `permissions:` block and that the token scope is
not over-provisioned.

### D5: Supply chain — chart provenance

**Decision: Sign the chart using `cosign` with the GitHub OIDC keyless workflow,
matching the existing container image signing posture.**

If the container image is already signed via cosign (or will be in phase 4), the chart
OCI artifact must be signed with the same mechanism for consistency. The security persona
determines whether cosign signing is in scope for this release cycle or deferred.

### D6: Validation strategy

**Decision: The test-engineer adds a CI smoke test that runs `helm install` from the
published OCI reference against a kind cluster in the test workflow.**

This is the only way to verify end-to-end that the artifact publishing pipeline produces
an installable chart. The test must:
- Pull from `oci://ghcr.io/mak3r/losant-device/charts/losant-device:<tag>`
- Confirm the CRD is registered and the controller pod reaches Running state
- Check that `helm get metadata` reports the correct chart version

This test runs in a separate job (`chart-install-smoke`) in the release workflow, after
the publish steps succeed.

### D7: Installation documentation

**Decision: The docs persona updates `docs/setup/` exclusively — the existing
`docs/setup/` guide gets OCI Helm install instructions, a CRD-first install section,
and an upgrade procedure.**

Stale docs elsewhere in `docs/` (e.g. older architecture notes referencing local helm
installs) are out of scope for this issue and will be addressed by the docs agent in a
follow-up pass.

---

## Implementation Sequence

Issues must be implemented in this order. Blocking relationships are explicit below.

```
#287 (security) — approve GHCR permissions + cosign scope
    ↓ unblocks
#288 (gitops-manager) — add chart publish, version sync, CRD asset to release.yml
    ↓ unblocks
#289 (test-engineer) — chart-install-smoke CI job
    ↓ unblocks
#290 (qa) — acceptance criteria + docs/acceptance-criteria.md update
#291 (docs) — OCI install docs, CRD-first pattern, upgrade procedure
```

`#290` and `#291` can proceed in parallel once `#288` is merged (they document the
published pipeline, not the implementation of it).

---

## Files Changed Per Persona (Summary)

| Persona | Files |
|---|---|
| gitops-manager | `.github/workflows/release.yml`, `helm/Chart.yaml` (placeholder note) |
| security | `.github/workflows/release.yml` (permissions block review only), `config/rbac/` if cosign markers needed |
| test-engineer | `.github/workflows/release.yml` (new job), `test/` smoke test harness |
| qa | `docs/acceptance-criteria.md` |
| docs | `docs/setup/*.md` (new or updated pages) |

---

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| GHCR OCI chart support requires Helm 3.8+ | Document minimum Helm version in setup guide; add version check to smoke test |
| `sed` version-inject is fragile on macOS runners | Use `sed -i ''` guard or switch to `yq` in the CI job |
| cosign OIDC requires matching OIDC audience configuration | Security persona validates before gitops-manager implements |
| Release CI runs before chart is available in GHCR during first release | Smoke test job has `needs: [publish-chart]` dependency to prevent race |
