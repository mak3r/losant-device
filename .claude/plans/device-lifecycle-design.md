---
title: Device Lifecycle Design — Identity, Attributes, Self-Healing, and Finalizer
status: approved
phase: 3-integration
author: product-designer
date: 2026-05-04
issue: "#294"
---

# Device Lifecycle Design

## Problem

The provisioner creates cluster and node devices in Losant without attribute
definitions, making manual Losant dashboard setup required for every deployment.
Two additional lifecycle gaps exist: no cleanup occurs when the `LosantSync` CR is
deleted from Kubernetes, and cluster identity change behavior on an existing CR is
undefined.

## Current State (Code Review)

**Identity** (`internal/losant/client.go`):
- Cluster device name: `k8s-cluster-<clusterName>` (line 261)
- Node device name: `k8s-node-<clusterName>-<nodeName>` (line 266)
- Node names come from `monitor.NodeHealth.Name` (k8s node object `.metadata.name`)
- Identity scheme is already correct — node name is used, not IP address

**Attribute gap** (`internal/losant/client.go`, lines 194–220):
- `createDevice` sends `name`, `deviceClass`, `tags`, optionally `deviceRecipeId`
  and `gatewayId` — **no `attributes` field**
- `docs/losant-setup.md` step 2 instructs users to add attributes manually
- Attribute names are defined in `clusterAttributes()` and `nodeAttributes()` in
  `internal/controller/losantsync_controller.go` (lines 266–296)

**Self-healing** (`internal/losant/client.go`, lines 113–141):
- `EnsureClusterDevice` and `EnsureNodeDevice` use `findDeviceByName`, which queries
  by name rather than by stored ID
- A device deleted from Losant returns nil from the name query → it is automatically
  recreated on the next reconcile cycle
- Self-healing logic is structurally correct; the only gap is that recreated devices
  also lack attribute definitions (same as original creation)

**Connectivity loss** (`internal/controller/losantsync_controller.go`, line 131–133):
- A failed `Ping` call sets `LosantUnreachable` and retries via `requeueOnDegraded`
  (1 minute)
- Network errors are returned by `doRequest` as Go errors — they are distinct from
  a nil return from `findDeviceByName` (which indicates a successful empty query)
- The connectivity-loss-is-not-deletion invariant is already respected by the
  find-by-name architecture

**CR deletion** (`internal/controller/losantsync_controller.go`, line 66–71):
- `errors.IsNotFound` returns `ctrl.Result{}, nil` immediately
- No finalizer exists; Kubernetes deletes the CR and the controller exits without
  cleaning up Losant devices

**Cluster identity change**:
- If `spec.clusterName` changes on an existing CR, `EnsureClusterDevice` looks up
  the new name, finds nothing, creates a new device — and the old device is orphaned
  in Losant indefinitely

---

## Architecture Decisions

### D1: Peripheral identity uses k8s node name (confirmed, no change needed)

**Decision: node name (`metadata.name`) is the correct component. No change to the
existing `nodeDeviceName` scheme.**

IP addresses change with DHCP leases, node replacements, and cluster reconfigurations.
k8s node names are stable for the lifetime of the node object. The HealthStore already
uses node names as map keys (`monitor.NodeHealth.Name`), so no mapping is needed.

Canonical format (already implemented):
```
k8s-node-<clusterName>-<nodeName>
```

Global uniqueness is guaranteed by the `clusterName` prefix.

---

### D2: Attribute definitions must be sent at device creation time

**Decision: extend `createDevice` to accept an `attributes []DeviceAttribute` parameter
and include it in the POST payload. Both `EnsureClusterDevice` and `EnsureNodeDevice`
must pass the full attribute list.**

The Losant REST API accepts `attributes` in the device POST body:
```json
{
  "name": "k8s-cluster-prod",
  "deviceClass": "edgeCompute",
  "attributes": [
    {"name": "health_score", "dataType": "number"},
    {"name": "health_status", "dataType": "string"},
    ...
  ]
}
```

Supported `dataType` values: `"number"`, `"string"`, `"boolean"`, `"gps"`.

**Cluster device attributes** (13 fields, derived from `clusterAttributes()` in
`losantsync_controller.go` and `docs/losant-setup.md`):

| Attribute name | dataType |
|---|---|
| `health_score` | `number` |
| `health_status` | `string` |
| `total_nodes` | `number` |
| `ready_nodes` | `number` |
| `unhealthy_nodes` | `number` |
| `total_pods` | `number` |
| `running_pods` | `number` |
| `failed_pods` | `number` |
| `pending_pods` | `number` |
| `crashloop_pods` | `number` |
| `degraded_pvcs` | `number` |
| `coredns_healthy` | `boolean` |
| `event_warnings` | `number` |

**Node device attributes** (9 fields, derived from `nodeAttributes()` in
`losantsync_controller.go`):

| Attribute name | dataType |
|---|---|
| `health_score` | `number` |
| `health_status` | `string` |
| `ready` | `boolean` |
| `memory_pressure` | `boolean` |
| `disk_pressure` | `boolean` |
| `pid_pressure` | `boolean` |
| `pod_count` | `number` |
| `not_ready_pods` | `number` |
| `crashloop_pods` | `number` |

**Note**: `NodeHealth` has `CPURequestPct` and `MemRequestPct` fields in the store
struct (store.go lines 28–29) but they are NOT in `nodeAttributes()`. These fields
are not currently reported. They should be added to `nodeAttributes()` and the
attribute definition list as `number` type in the same PR to avoid drift.

---

### D3: Ensure-or-patch attribute idempotency

**Decision: on `EnsureClusterDevice` / `EnsureNodeDevice`, if the device already
exists (found by name), call `PatchDeviceAttributes` to add any missing attributes.
Do not remove or replace existing attributes.**

This handles three cases:
1. **New deployment**: device created with full attribute list (D2)
2. **Existing device from old operator version**: operator patches missing attributes
3. **Manual pre-provisioned device** (as documented in current setup guide): attributes
   are populated automatically without requiring manual steps

`PatchDeviceAttributes` must use `PATCH /applications/{appID}/devices/{deviceID}` with
only the `attributes` field. The Losant API merges attributes on PATCH, so existing
attributes are preserved.

---

### D4: Cluster identity change — orphan with warning

**Decision: when `spec.clusterName` changes on an existing CR, the controller creates
new cluster and node devices under the new name and logs a warning that old devices
are orphaned in Losant. No automatic deletion of the old devices occurs.**

Rationale: renaming may be an operator mistake, not an intentional migration. Automatic
deletion of the old cluster device would destroy historical time-series data in Losant.
The operator logs the old `ClusterDeviceID` at warning level so the operator can
identify and manually clean up the orphaned devices.

Detection mechanism: compare `ls.Status.ClusterDeviceID` against the result of
`findDeviceByName(newName)`. If status has a non-empty ID that is different from the
newly found/created device, log the orphan warning.

**User-facing documentation**: the setup guide must state that `spec.clusterName` is
effectively immutable after installation. Changing it is supported but results in
orphaned devices and reset historical data.

---

### D5: Self-healing — no additional code needed

**Decision: self-healing of Losant-side deletion is already architecturally correct
via the find-by-name query pattern. No new detection code is required.**

When a device is deleted from Losant, the next reconcile cycle calls `findDeviceByName`
(a query, not a GET-by-ID). The query returns empty results (not a 404 error). The
`Ensure*` methods then call `createDevice`, which produces a new device. With D2
implemented, the recreated device will have correct attribute definitions.

The **status deviceID** is always updated from the `Ensure*` return value on each
reconcile, so stale IDs in status are automatically corrected.

Connectivity loss (network error on any `doRequest` call) remains distinct from device
absence: network errors return Go errors that cause `setDegraded` and retry, while
successful empty query results cause device recreation.

---

### D6: CR deletion finalizer

**Decision: add a Kubernetes finalizer `losant.io/device-cleanup` to the LosantSync
CR. On CR deletion, delete all node peripheral devices, then the cluster Edge Compute
device, then remove the finalizer.**

Implementation steps:
1. On every `Reconcile` call (non-deleted CR), ensure the finalizer is present:
   `controllerutil.AddFinalizer(ls, "losant.io/device-cleanup")`
2. When `ls.DeletionTimestamp != nil`, execute the teardown sequence:
   a. Call `DeleteDevice(ctx, appID, nodeDeviceID)` for each entry in `ls.Status.NodeDevices`
   b. Call `DeleteDevice(ctx, appID, ls.Status.ClusterDeviceID)`
   c. Call `controllerutil.RemoveFinalizer(ls, "losant.io/device-cleanup")`
   d. Update the CR to persist the finalizer removal
3. `DeleteDevice` must be added to the `LosantClient` interface and `HTTPClient`:
   `DELETE /applications/{appID}/devices/{deviceID}`

**Idempotency**: `DeleteDevice` receiving a 404 is not an error — the device is
already gone. Return nil in that case.

**Connectivity loss during teardown**: if `DeleteDevice` fails with a network error
(not 404), the reconciler returns an error and the finalizer is not removed. The
controller retries automatically. The CR remains in a terminating state until Losant
is reachable.

**Escape hatch for permanent Losant connectivity loss**: document in the runbook that
an operator can manually remove the finalizer with:
```bash
kubectl patch losantsync <name> -p '{"metadata":{"finalizers":[]}}' --type=merge
```
This bypasses cleanup and deletes the CR without removing Losant devices.

---

### D7: New `LosantClient` interface methods

The developer must add two methods to `LosantClient` in `internal/losant/client.go`:

```go
// EnsureClusterDeviceAttributes patches the attribute definitions on an
// existing cluster device to ensure all required attributes are present.
// Idempotent — existing attributes are not modified.
EnsureClusterDeviceAttributes(ctx context.Context, applicationID, deviceID string) error

// EnsureNodeDeviceAttributes patches the attribute definitions on an
// existing node peripheral device to ensure all required attributes are present.
// Idempotent — existing attributes are not modified.
EnsureNodeDeviceAttributes(ctx context.Context, applicationID, deviceID string) error

// DeleteDevice permanently removes a device from Losant.
// Returns nil if the device does not exist (404 is treated as success).
DeleteDevice(ctx context.Context, applicationID, deviceID string) error
```

Alternatively, a single `EnsureDeviceAttributes(ctx, appID, deviceID string, attrs []DeviceAttribute) error`
method covers both cases — the developer may choose the cleaner factoring.

The `mock_client.go` must be updated to add stubs for these methods. The
test-engineer's coverage tests will then build on these stubs.

---

## Implementation Sequence

```
#295 (developer) — add attributes to createDevice + PatchDeviceAttributes + DeleteDevice + finalizer
    |
    +-- #296 (test-engineer) — unit tests for lifecycle scenarios
    |
    +-- #297 (qa) — E2E acceptance tests for full lifecycle
```

`#296` and `#297` are both blocked by `#295` and can proceed in parallel once `#295`
is merged.

---

## Files Changed Per Persona

| Persona | Files |
|---|---|
| developer | `internal/losant/client.go`, `internal/losant/mock_client.go`, `internal/controller/losantsync_controller.go`, `api/v1alpha1/losantsync_types.go` (finalizer constant only, no schema change) |
| test-engineer | `*_test.go` files in `internal/losant/`, `internal/controller/` |
| qa | `test/e2e/lifecycle_test.go`, `docs/acceptance-criteria.md` |

---

## Edge Cases

| Case | Behavior |
|---|---|
| Device deleted from Losant between two reconciles | Recreated with attributes on next cycle (D5) |
| `spec.clusterName` changed on existing CR | Old devices orphaned + logged; new devices created (D4) |
| CR deleted while Losant unreachable | Finalizer blocks deletion; retries; runbook escape hatch available (D6) |
| Node added to cluster while operator running | `EnsureNodeDevice` creates it with attributes on next reconcile |
| Node removed from cluster | Node device remains in Losant (no automatic deletion — node removal is not observable via the HealthStore snapshot pattern); future cleanup can be addressed separately |
| `DeleteDevice` returns 404 | Treated as success (D6) |
| CPURequestPct / MemRequestPct in NodeHealth but not in `nodeAttributes()` | Developer adds these to `nodeAttributes()` and to the attribute definition list in the same PR (D2 note) |
