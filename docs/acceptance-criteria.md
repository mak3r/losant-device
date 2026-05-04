# Acceptance Criteria — LosantSync Reconciliation

This document is the QA ground truth for signing off on `LosantSync` controller behavior.
Every statement below is testable; the e2e suite in `test/e2e/` maps directly to these criteria.

---

## 1. Status.Phase Semantics

| Phase | Meaning | How Entered | How Exited |
|---|---|---|---|
| *(empty)* | Resource just created; no reconcile has run yet | Creation | Controller first reconcile → `Provisioning` |
| `Provisioning` | Controller is bootstrapping: checking GEA reachability and provisioning Losant device records for each k8s node | First non-suspend reconcile | Successful provisioning of all nodes → `Active`; fatal provisioning error → `Degraded` |
| `Active` | All devices provisioned; sync cycles running on schedule | Provisioning completes successfully | GEA unreachable for N consecutive cycles → `Degraded`; `Suspend=true` → `Suspended` |
| `Degraded` | One or more required operations failing (GEA unreachable, Losant REST API failure); controller is retrying | GEA or REST API unreachable | Dependency recovers → `Active`; `Suspend=true` → `Suspended` |
| `Suspended` | All reconciliation halted; no metrics sent, no provisioning | `spec.suspend: true` set | `spec.suspend: false` or field removed → `Provisioning` (restarts lifecycle) |

### Testable Statements — Phase

- **AC-P-01**: The `phase` field MUST be `Provisioning` within 30 seconds of applying a valid `LosantSync` CR when `spec.suspend` is false.
- **AC-P-02**: The `phase` field MUST be `Suspended` within 30 seconds of applying a `LosantSync` CR with `spec.suspend: true`.
- **AC-P-03**: A resource in any phase MUST transition to `Suspended` within 30 seconds of setting `spec.suspend: true`.
- **AC-P-04**: The `phase` field MUST NOT be empty more than 30 seconds after the resource is created.
- **AC-P-05**: The `phase` field MUST be `Active` within 60 seconds of applying a valid `LosantSync` CR when GEA is reachable and all nodes are provisioned.
- **AC-P-06**: The `phase` field MUST be `Degraded` within 60 seconds when the GEA service is unreachable after a successful `Provisioning` state.

---

## 2. Status.Conditions Semantics

The `status.conditions` array uses standard `metav1.Condition` types. Three condition types are defined:

### GEAReachable

| Value | Meaning |
|---|---|
| `True` | Controller successfully POSTed to `http://<gea-service>:<port>` within the most recent sync cycle |
| `False` | HTTP call to GEA failed (connection refused, timeout, non-2xx response) |
| `Unknown` | Condition not yet evaluated (e.g. first reconcile not yet complete) |

- **AC-C-01**: `GEAReachable=True` MUST be set after a successful metric report to the GEA.
- **AC-C-02**: `GEAReachable=False` MUST be set within one reconcile cycle of the GEA becoming unreachable. The condition `reason` field MUST be non-empty.
- **AC-C-03**: `GEAReachable` MUST transition back to `True` after connectivity to the GEA is restored.

### DevicesProvisioned

| Value | Meaning |
|---|---|
| `True` | All k8s nodes currently listed by the API server have a corresponding Losant peripheral device ID in `status.nodeDevices` |
| `False` | One or more nodes lack a Losant device record (provisioning call failed or not yet made) |
| `Unknown` | Provisioning has not yet been attempted |

- **AC-C-04**: `DevicesProvisioned=True` MUST be set after all nodes in the cluster have Losant device IDs.
- **AC-C-05**: When a new node joins, `DevicesProvisioned` MUST transition to `False` until the new node is provisioned, then back to `True`.
- **AC-C-06**: When a node is removed from the cluster, `DevicesProvisioned` MUST remain `True` (removal does not require re-provisioning).

### LastSyncSucceeded

| Value | Meaning |
|---|---|
| `True` | The most recent scheduled metric report to the GEA completed without error |
| `False` | The most recent report attempt failed; `reason` and `message` fields are populated |
| `Unknown` | No sync cycle has completed yet |

- **AC-C-07**: `LastSyncSucceeded=True` MUST be set after each successful GEA report.
- **AC-C-08**: `LastSyncSucceeded=False` MUST be set if the GEA HTTP call returns a non-2xx response or times out.

---

## 3. Verifying a Completed Sync Cycle

### kubectl checks

```bash
# Confirm phase is Active
kubectl get losantsync <name> -o jsonpath='{.status.phase}'
# Expected: Active

# Confirm LastSyncTime was updated recently
kubectl get losantsync <name> -o jsonpath='{.status.lastSyncTime}'
# Expected: timestamp within the configured interval

# Confirm all conditions are True
kubectl get losantsync <name> -o jsonpath='{range .status.conditions[*]}{.type}={.status} {end}'
# Expected: GEAReachable=True DevicesProvisioned=True LastSyncSucceeded=True

# List per-node device IDs
kubectl get losantsync <name> -o jsonpath='{.status.nodeDevices}'
# Expected: map with one entry per k8s node
```

### Controller logs

```bash
kubectl logs -n losant-system deploy/losant-device-controller-manager -f | grep losantsync
```

Look for lines containing:
- `"sync due"` — reconciler reached the reporting branch
- `"provisioning complete"` — all nodes have Losant device IDs
- `"GEA report sent"` — HTTP POST to GEA succeeded

### Losant dashboard

1. Open the Losant Application → **Devices** tab.
2. The Edge Compute device for this cluster MUST appear with tag `clusterName=<spec.clusterName>` and `region=<spec.region>`.
3. A peripheral device MUST appear for each k8s node with tag `node=<nodeName>`.
4. The **Data** view on any device MUST show a timestamp within the configured sync interval.

### Testable Statements — Sync Verification

- **AC-S-01**: `status.lastSyncTime` MUST be updated after every successful sync cycle.
- **AC-S-02**: `status.lastSyncTime` MUST NOT be updated if the GEA HTTP call fails.
- **AC-S-03**: `status.nodeDevices` MUST contain exactly one entry per ready k8s node after provisioning completes.

---

## 4. GEA Unreachability

- **AC-GEA-01**: When the GEA pod/service is unreachable, the controller MUST NOT crash; it MUST requeue and retry.
- **AC-GEA-02**: The `phase` MUST transition to `Degraded` (not remain `Active`) when GEA is unreachable for a full sync cycle.
- **AC-GEA-03**: `GEAReachable=False` MUST be set. The condition `reason` MUST describe the failure (e.g. `ConnectionRefused`, `Timeout`).
- **AC-GEA-04**: `status.lastSyncTime` MUST NOT be updated during a failed GEA call.
- **AC-GEA-05**: When GEA recovers, the controller MUST resume within one reconcile cycle: `phase` returns to `Active` and `GEAReachable=True`.
- **AC-GEA-06**: The retry interval MUST use exponential backoff capped at a reasonable maximum (≤5 minutes); it MUST NOT spin without delay.

---

## 5. Losant REST API Unreachability During Provisioning

- **AC-REST-01**: When `api.losant.com` is unreachable during device provisioning, the controller MUST NOT crash; it MUST requeue with backoff.
- **AC-REST-02**: `phase` MUST remain `Provisioning` (not advance to `Active`) until all devices are provisioned.
- **AC-REST-03**: `DevicesProvisioned=False` MUST be set with a descriptive `reason` (e.g. `APIUnavailable`).
- **AC-REST-04**: Once the REST API becomes reachable, provisioning MUST resume automatically within one reconcile cycle.
- **AC-REST-05**: Partial provisioning state MUST be persisted: if N of M nodes were provisioned before the failure, those N entries MUST remain in `status.nodeDevices` and MUST NOT require re-provisioning.

---

## 6. New Node Joins the Cluster

- **AC-NODE-ADD-01**: Within one reconcile cycle of a new node appearing in `kubectl get nodes`, the controller MUST attempt to provision a Losant peripheral device for it.
- **AC-NODE-ADD-02**: `status.nodeDevices` MUST contain an entry for the new node within 60 seconds of it becoming `Ready`.
- **AC-NODE-ADD-03**: `DevicesProvisioned` MUST transition to `False` temporarily and then back to `True` after the new node is provisioned.
- **AC-NODE-ADD-04**: Metrics for the new node MUST appear in the next GEA report after its device is provisioned.

---

## 7. Node Removal or Cordoning

### Node removed from cluster

- **AC-NODE-RM-01**: Removing a node from the cluster MUST NOT cause the controller to error or enter `Degraded` phase.
- **AC-NODE-RM-02**: The removed node's entry MAY remain in `status.nodeDevices` (the Losant device is retained for historical data); it MUST NOT re-trigger provisioning.
- **AC-NODE-RM-03**: The removed node MUST NOT appear in subsequent GEA metric reports.

### Node cordoned (SchedulingDisabled)

- **AC-NODE-CORDON-01**: A cordoned node MUST still be included in health metric reports (cordoning affects scheduling, not health visibility).
- **AC-NODE-CORDON-02**: The node's health snapshot in the GEA payload MUST reflect its actual condition (e.g. `Ready=True` even if unschedulable).

---

## 8. Schedule Behavior

### Interval-based scheduling (`spec.interval`)

- **AC-SCHED-01**: `status.nextScheduledTime` MUST be set on the first reconcile to approximately `time.Now()`.
- **AC-SCHED-02**: After each successful sync, `status.nextScheduledTime` MUST advance by exactly `spec.interval` from the previous `nextScheduledTime` (not from `time.Now()`), preventing schedule drift.
- **AC-SCHED-03**: The controller MUST NOT report metrics before `status.nextScheduledTime` is reached.
- **AC-SCHED-04**: The controller MUST NOT skip a sync cycle even if the reconcile fires late (catchup behavior: fire immediately if overdue).

### Cron-based scheduling (`spec.cronSchedule`)

- **AC-SCHED-05**: `status.nextScheduledTime` MUST be set to the next cron tick after the first reconcile.
- **AC-SCHED-06**: After each sync, `status.nextScheduledTime` MUST advance to the next cron tick relative to the previous `nextScheduledTime`.
- **AC-SCHED-07**: Standard cron expressions (5-field) MUST be accepted. Invalid expressions MUST be rejected at admission time.

### Controller restart mid-schedule

- **AC-SCHED-08**: On controller restart, `status.nextScheduledTime` MUST be read from the persisted status (not reset to `time.Now()`).
- **AC-SCHED-09**: If `status.nextScheduledTime` is in the past when the controller restarts, the controller MUST fire a sync cycle immediately (within one reconcile).
- **AC-SCHED-10**: If `status.nextScheduledTime` is in the future when the controller restarts, the controller MUST wait until that time before syncing (no early or double-fire).

---

## 9. Suspension Behavior

- **AC-SUSP-01**: Setting `spec.suspend: true` on a resource in any phase MUST transition it to `Suspended` within 30 seconds.
- **AC-SUSP-02**: While `Suspended`, `status.nextScheduledTime` MUST NOT advance.
- **AC-SUSP-03**: While `Suspended`, no HTTP calls MUST be made to the GEA or Losant REST API.
- **AC-SUSP-04**: Removing `spec.suspend` (or setting it to `false`) MUST restart the lifecycle from `Provisioning`.
- **AC-SUSP-05**: The `Suspended` phase MUST be idempotent: repeated reconciles on a suspended resource MUST NOT change any status field.

---

## 10. Full Device Lifecycle (Attribute Definitions, Self-Healing, Cleanup)

These criteria require real Losant credentials (`LOSANT_APP_ID`, `LOSANT_API_TOKEN`) and a live cluster with a reachable GEA pod. Tests skip automatically when credentials are absent.

### Cluster device attributes (13)

`health_score`, `health_status`, `total_nodes`, `ready_nodes`, `unhealthy_nodes`, `total_pods`, `running_pods`, `failed_pods`, `pending_pods`, `crashloop_pods`, `degraded_pvcs`, `coredns_healthy`, `event_warnings`

### Node device attributes (11)

`health_score`, `health_status`, `ready`, `memory_pressure`, `disk_pressure`, `pid_pressure`, `pod_count`, `not_ready_pods`, `crashloop_pods`, `cpu_request_pct`, `mem_request_pct`

### Testable Statements — Device Lifecycle

- **AC-LIFECYCLE-01**: When a `LosantSync` CR reaches phase `Active` on a clean application (no existing devices), the cluster Edge Compute device MUST have all 13 cluster attribute definitions and each node peripheral device MUST have all 11 node attribute definitions.
  - *Preconditions*: No existing Losant devices for this cluster name. Valid KUBECONFIG, LOSANT_APP_ID, LOSANT_API_TOKEN. GEA reachable.
  - *Steps*: Apply CR → wait for `Active` → `GET /applications/{appID}/devices/{clusterDeviceID}` → assert 13 attributes → repeat for each node device ID in `Status.NodeDevices`.
  - *Expected*: All cluster and node devices carry the expected attribute schema.

- **AC-LIFECYCLE-02**: When a cluster device already exists in Losant with no attributes, the operator MUST patch all 13 cluster attribute definitions onto it during the next `EnsureClusterDevice` call.
  - *Preconditions*: A device named `k8s-cluster-<clusterName>` exists in Losant with no attributes. Valid credentials and reachable GEA.
  - *Steps*: Pre-create bare device → apply CR with matching `clusterName` → wait for `Active` → `GET` the pre-existing device → assert all 13 attributes present.
  - *Expected*: The pre-existing device's attribute set is augmented to the full cluster schema without recreation.

- **AC-LIFECYCLE-03**: When the cluster device is deleted directly from Losant, the controller MUST detect the absence on the next reconcile cycle and recreate it with a new device ID and all 13 cluster attribute definitions. Phase MUST return to `Active`.
  - *Preconditions*: CR is `Active`. Valid credentials. GEA reachable.
  - *Steps*: Wait for `Active` → note `Status.ClusterDeviceID` → delete device via Losant API → wait up to 2× the configured interval → assert `Status.ClusterDeviceID` changed AND phase is `Active` → fetch new device → assert 13 attributes.
  - *Expected*: Controller self-heals by recreating the missing device within one reconcile cycle.

- **AC-LIFECYCLE-04**: Deleting a `LosantSync` CR MUST trigger the `losant.io/device-cleanup` finalizer, which MUST delete the cluster device and all node devices from Losant before the CR is removed from the API server.
  - *Preconditions*: CR is `Active`. Valid credentials.
  - *Steps*: Note `Status.ClusterDeviceID` and all `Status.NodeDevices` values → delete CR → wait for CR to be fully gone (no finalizer remaining) → `GET` each device ID → assert all return 404.
  - *Expected*: All registered Losant devices are deleted; none return 200 after CR removal.

---

## 11. E2E Test Coverage Map

| Criteria | Test File | Test Description | Status |
|---|---|---|---|
| AC-P-01 | lifecycle_test.go | "moves to Provisioning on the first reconcile" | Implemented |
| AC-P-02 | lifecycle_test.go | "enters Suspended immediately when Suspend=true on creation" | Implemented |
| AC-P-03 | lifecycle_test.go | "transitions from Provisioning to Suspended when Suspend is enabled" | Implemented |
| AC-P-04 | lifecycle_test.go | "does not remain in the empty phase indefinitely" | Implemented |
| AC-P-05 | lifecycle_test.go | "transitions from Provisioning to Active after successful GEA report" | Implemented |
| AC-P-06 | lifecycle_test.go | "transitions to Degraded when the GEA is unreachable" | Implemented |
| AC-C-01..08 | lifecycle_test.go | Conditions (GEAReachable, DevicesProvisioned, LastSyncSucceeded) | Implemented |
| AC-S-01..03 | lifecycle_test.go | LastSyncTime, nodeDevices | Implemented |
| AC-GEA-01..06 | lifecycle_test.go | GEA failure/recovery | Implemented |
| AC-REST-01..05 | lifecycle_test.go | REST API failure/recovery | Implemented |
| AC-NODE-ADD-01..04 | lifecycle_test.go | New node join | Skip (requires live node add) |
| AC-NODE-RM-01..03 | lifecycle_test.go | Node removal | Skip (requires live node remove) |
| AC-NODE-CORDON-01..02 | lifecycle_test.go | Node cordon | Implemented |
| AC-SCHED-01 | scheduling_test.go | "is set close to the time of creation" | Implemented |
| AC-SCHED-02 | scheduling_test.go | "advances NextScheduledTime by the configured interval after each sync" | Implemented |
| AC-SCHED-05..06 | scheduling_test.go | Cron scheduling behavior | Implemented |
| AC-SCHED-08..10 | scheduling_test.go | Controller restart mid-schedule | Skip (requires controller restart) |
| AC-SUSP-01 | lifecycle_test.go | Phase transitions with suspend | Implemented |
| AC-SUSP-02 | scheduling_test.go | "stops advancing NextScheduledTime after Suspend is set" | Implemented |
| AC-SUSP-03 | lifecycle_test.go | No HTTP calls while suspended | Implemented |
| AC-SUSP-04 | lifecycle_test.go | Resume from suspension → Provisioning | Implemented |
| AC-SUSP-05 | lifecycle_test.go | "phase remains Suspended when reconciled repeatedly" | Implemented |
| AC-LIFECYCLE-01 | lifecycle_test.go | "creates cluster and node devices with all expected attribute definitions" | Implemented |
| AC-LIFECYCLE-02 | lifecycle_test.go | "patches all cluster attributes onto a pre-existing device that has no attributes" | Implemented |
| AC-LIFECYCLE-03 | lifecycle_test.go | "recreates a cluster device that was manually deleted from Losant" | Implemented |
| AC-LIFECYCLE-04 | lifecycle_test.go | "deletes all Losant devices when the CR is deleted" | Implemented |
| CRD validation | validation_test.go | CEL rules, required fields, port range, defaults | Implemented |
