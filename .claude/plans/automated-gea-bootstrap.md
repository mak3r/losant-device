---
title: Automated GEA Bootstrap — Eliminate Manual Pre-Provisioning
status: approved
phase: 3-integration
author: product-designer
date: 2026-05-01
issue: "#211"
---

# Automated GEA Bootstrap

## Problem

Deploying the operator today requires three manual steps before `kubectl apply`:

1. Create the Edge Compute device in the Losant dashboard
2. Generate an access key for that device
3. Populate the `losant-gea-credentials` Secret (`DEVICE_ID`, `ACCESS_KEY`, `ACCESS_SECRET`)

The controller already creates the Edge Compute device automatically via `EnsureClusterDevice`
(found in `internal/losant/client.go`). The remaining gap is that the GEA pod reads its
credentials from that Secret at startup and cannot connect to Losant without them.

## Design Decisions

### Q1: Can the Losant REST API create device access keys programmatically?

**Decision: Yes — use `POST /applications/{applicationId}/devices/{deviceId}/accessKeys`.**

The Losant REST API exposes device access key CRUD. The response includes `key` and `secret`
fields (secret shown once, identical to the dashboard flow). The existing `HTTPClient` in
`internal/losant/client.go` can call this endpoint using the same Application API Token already
stored in `provisioningSecretRef`.

New interface method to add to `LosantClient`:
```go
// CreateDeviceAccessKey creates a new Losant access key for the given device.
// Returns the access key ID, key string, and secret string.
// The secret is shown only in this response and is not retrievable later.
CreateDeviceAccessKey(ctx context.Context, applicationID, deviceID string) (keyID, key, secret string, err error)
```

### Q2: Adopt existing device or always create new?

**Decision: Adopt existing device; skip bootstrap if credentials secret is already populated.**

`EnsureClusterDevice` already implements find-or-create by canonical device name
(`k8s-cluster-{clusterName}`). No change needed there. The bootstrap phase adds a separate
idempotency check for the credentials secret:

- If `losant-gea-credentials` Secret exists **and** all three keys (`DEVICE_ID`, `ACCESS_KEY`,
  `ACCESS_SECRET`) are non-empty → skip bootstrap, GEA already has credentials.
- If Secret is missing or any key is empty → execute bootstrap: get/create device, create a new
  access key, write all three values to the Secret (create or patch), restart GEA.

**On re-deploy idempotency**: If the operator is uninstalled and reinstalled on the same cluster,
the device already exists in Losant (found by name) and a new access key will be generated. Old
access keys accumulate in Losant (they remain valid unless manually revoked). The controller names
created keys with a timestamp tag so operators can identify and clean up stale keys from the
dashboard.

### Q3: What RBAC changes are required?

**Decision: Two new permissions required — `core/secrets create+update` and `apps/deployments patch`.**

Current `config/rbac/role.yaml` grants only `core/secrets: get`. The bootstrap requires:

| Resource | New Verbs | Rationale |
|---|---|---|
| `core/secrets` | `create`, `update` | Write the `losant-gea-credentials` Secret |
| `apps/deployments` | `patch` | Trigger GEA rolling restart by patching annotations |

These permissions must go through the security persona review process before the developer
can add the corresponding `// +kubebuilder:rbac` markers.

### Q4: GEA restart mechanism

**Decision: Rolling restart via Deployment annotation patch.**

The GEA reads credentials as environment variables (`envFrom: secretRef`), so it cannot reload
credentials without a restart. The canonical Kubernetes pattern is to patch
`spec.template.metadata.annotations["kubectl.kubernetes.io/restartedAt"]` with an RFC3339
timestamp. The controller needs to know the GEA Deployment name; this is exposed via the existing
`GEASpec.ServiceRef` field or a new optional `GEADeploymentRef` field (see API changes below).

The restart is triggered only once as part of the bootstrap sequence. Subsequent reconcile cycles
do not restart the GEA.

---

## Implementation Plan

### New Bootstrap Lifecycle Phase

The reconciler adds a bootstrap sub-step executed when:
- `ls.Status.Phase == Provisioning`  
- The `GEABootstrapped` condition is absent or `False`

After successful bootstrap, the reconciler sets `GEABootstrapped: True` and continues normally.
Failures set `GEABootstrapped: False` with reason/message and requeue after `requeueOnDegraded`.

```
PhaseProvisioning
  └── Step 0 (new): GEA Bootstrap
        ├── EnsureClusterDevice → deviceID (already exists)
        ├── CreateDeviceAccessKey → (keyID, key, secret)
        ├── Write losant-gea-credentials Secret
        ├── Patch GEA Deployment (rolling restart)
        └── Set condition GEABootstrapped=True
  └── Step 1–9: existing reconcile loop (unchanged)
```

### Files Changed by Persona

#### Developer (`internal/**`, `api/**`, `cmd/**`)

1. **`api/v1alpha1/losantsync_types.go`** — Add optional field to `GEASpec`:
   ```go
   // DeploymentRef is the name of the GEA Deployment for rolling-restart automation.
   // If empty, credential bootstrap writes the Secret but does not restart the GEA.
   // +optional
   DeploymentRef string `json:"deploymentRef,omitempty"`
   ```
   Add `GEABootstrapped` to the known condition types comment on `LosantSyncStatus`.

2. **`internal/losant/client.go`** — Add `CreateDeviceAccessKey` to `LosantClient` interface and
   implement on `HTTPClient`:
   ```go
   POST /applications/{applicationId}/devices/{deviceId}/accessKeys
   ```
   Payload: `{"name": "losant-device-controller-<clusterName>-<timestamp>"}`.
   Returns struct with `key` and `secret`.

3. **`internal/provisioner/bootstrap.go`** (new file) — `GEABootstrapper` struct with a single
   exported method:
   ```go
   func (b *GEABootstrapper) Bootstrap(ctx context.Context, ls *losantv1alpha1.LosantSync) error
   ```
   Reads `losant-gea-credentials`, checks if already populated, calls `LosantClient`,
   writes Secret via `client.Client`, patches Deployment if `DeploymentRef` is set.

4. **`internal/controller/losantsync_controller.go`** — Insert bootstrap call between "first
   reconcile" check and Step 1 (secret read). Bootstrap is skipped if condition
   `GEABootstrapped=True` is already set.

   **Critical**: The controller needs `create` and `update` on Secrets after this change. Add the
   `// +kubebuilder:rbac` markers **only after** security approves the RBAC issue.

#### Security (`config/rbac/**`)

Update `config/rbac/role.yaml` to add:
- `core/secrets: create, update` (extends existing `get` entry)
- `apps/deployments: patch` (extends existing `get; list; watch` entry)

#### Gitops-Manager (`helm/**`)

1. **`helm/templates/secret-gea.yaml`** — Make the entire template conditional on
   `not .Values.gea.credentials.existingSecret` **and** `not .Values.gea.autoProvision`
   (new boolean, default `true`). When `autoProvision: true`, the Secret is created by
   the controller; Helm must not also create it.

2. **`helm/values.yaml`** — Add:
   ```yaml
   gea:
     autoProvision: true   # controller creates losant-gea-credentials automatically
     deploymentRef: "losant-gea"  # name of the GEA Deployment for restart
   ```

3. **`helm/templates/deployment.yaml`** — Pass `gea.deploymentRef` into the controller via
   the `LosantSync` CR template (or directly into `GEASpec.DeploymentRef` in the CR sample).

#### Docs (`docs/**`)

Update `docs/losant-setup.md`:
- **Step 2** (Create Edge Compute Device): remove — automated by controller
- **Step 3** (Create GEA Access Key): remove — automated by controller
- **Step 6** (Create `losant-gea-credentials` Secret): remove or demote to
  "Advanced: Manual Pre-Provisioning" section for users who prefer to manage credentials
  themselves (`helm install --set gea.autoProvision=false`)
- Add new "Quick Start" section showing the minimal input: `applicationID`, 
  `provisioningSecretRef`, `clusterName`, `region`

---

## Minimal User Input After This Change

```yaml
# Provisioning secret (only this manual step remains)
kubectl create secret generic losant-provisioning-credentials \
  --from-literal=api-token=<application-api-token> \
  -n losant-system

# LosantSync CR — same as before
apiVersion: losant.io/v1alpha1
kind: LosantSync
spec:
  applicationID: "<app-id>"
  provisioningSecretRef:
    name: losant-provisioning-credentials
    namespace: losant-system
  clusterName: "prod-edge-01"
  region: "us-west"
  gea:
    serviceRef: "losant-gea"
    deploymentRef: "losant-gea"
    port: 8080
```

The controller creates the Edge Compute device, generates access keys, populates the
`losant-gea-credentials` Secret, and restarts the GEA pod automatically.

---

## Scope Boundaries

| In scope | Out of scope |
|---|---|
| Automated cluster device creation (already done) | Node device access keys (nodes don't authenticate directly to Losant) |
| GEA credential bootstrap | Access key rotation / expiry management |
| Secret write + GEA rolling restart | GEA credential live-reload without restart |
| Idempotent bootstrap on re-deploy | Orphaned access key cleanup in Losant |
| Helm optional flag for manual mode | Multi-cluster federation |

---

## Risk Notes

- **Secret write timing**: If the GEA Deployment is not yet created when bootstrap runs, the
  patch step must be skipped gracefully and retried. Use `errors.IsNotFound` and requeue.
- **Partial bootstrap**: If the controller writes the Secret but the Deployment patch fails, the
  Secret is already written. On retry, the idempotency check sees a populated Secret and skips
  key creation. The controller must separately check whether the Deployment patch succeeded
  (track a `GEARestartTriggered` annotation on the LosantSync status or a separate condition).
- **RBAC ordering**: Developer must not add `// +kubebuilder:rbac` markers until Security
  updates `role.yaml`. Adding markers prematurely will cause `make manifests` to silently
  expand the role beyond the security-approved baseline and fail CI.
