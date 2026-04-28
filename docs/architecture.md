# Architecture

## Communication Model: GEA + REST Hybrid

The operator uses two distinct communication paths:

| Concern | Path | Rationale |
|---|---|---|
| Device provisioning | Controller → `api.losant.com` (REST) | GEA cannot create devices; provisioning is infrequent and tolerates latency |
| State reporting (metrics) | Controller → GEA (in-cluster HTTP) → `broker.losant.com` (MQTT) | GEA provides 65k-message SQLite buffer, proven reconnect logic, and Losant-aware rate-limit replay |

The Losant Gateway Edge Agent (`losant/edge-agent`) runs as a Deployment inside each monitored cluster. It owns the outbound MQTT connection to Losant cloud and absorbs all offline buffering responsibility — the controller itself has no persistent buffer.

```
┌─────────────────────────────────────────────┐
│  k3s Cluster (losant-system namespace)      │
│                                             │
│  ┌──────────────────────────────────────┐   │
│  │  losant-device-controller            │   │
│  │                                      │   │
│  │  HealthWatcherReconciler             │   │
│  │    watches: Node, Pod, Deployment,   │   │
│  │            PVC, Event                │   │
│  │    writes: HealthStore (in-memory)   │   │
│  │                                      │   │
│  │  LosantSyncReconciler                │   │
│  │    reads: HealthStore                │   │
│  │    provisioning ──────────────────────┼───┼──► api.losant.com
│  │    state reporting ──────────────┐   │   │    (REST, device create/update)
│  └──────────────────────────────────┼───┘   │
│                                     │        │
│  ┌──────────────────────────────────▼───┐   │
│  │  GEA pod (losant/edge-agent)         │   │
│  │    HTTP trigger: port 8080           │   │
│  │    SQLite buffer: /data (PV-backed)  ├───┼──► broker.losant.com
│  │    reconnect: every 30s             │   │    (MQTT, state reports)
│  │    buffer capacity: 65,000 messages  │   │
│  └──────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
```

## Losant Device Model

Each k3s cluster maps to a set of Losant devices:

### Cluster Device (Edge Compute type)

One per cluster. This device IS the GEA's Losant identity — its credentials are the GEA pod's environment variables.

**Tags** (set at provisioning, updated at runtime):
```
device_type   = cluster
cluster_name  = <spec.clusterName>
region        = <spec.region>
rancher_url   = <spec.rancherURL>
health_status = healthy | degraded | critical   ← updated each sync
k8s_version   = <serverVersion.GitVersion>
```

**Attributes** (time-series state data):
```
total_nodes, ready_nodes, unhealthy_nodes
total_pods, running_pods, failed_pods, pending_pods, crashloop_pods
degraded_pvcs, coredns_healthy (1/0), event_warnings
health_score (0-100)
```

### Node Devices (Peripheral type)

One per k8s node, associated with the cluster gateway. Must be provisioned via REST API before the GEA can report on them.

**Tags**:
```
device_type  = node
cluster_name = <spec.clusterName>
region       = <spec.region>
node_name    = <node.Name>
node_role    = control-plane | worker
health_status = healthy | degraded | critical   ← updated each sync
```

**Attributes**:
```
cpu_request_pct, mem_request_pct
pod_count, not_ready_pods, crashloop_pods
node_ready (1/0), memory_pressure (1/0), disk_pressure (1/0), pid_pressure (1/0)
health_score (0-100)
```

All boolean values are encoded as `1`/`0` for Losant time-series graph compatibility.

## CRD: LosantSync

A single cluster-scoped custom resource configures the entire operator for one cluster.

### Spec

```yaml
apiVersion: losant.io/v1alpha1
kind: LosantSync
metadata:
  name: my-cluster
spec:
  applicationID: "abc123"          # Losant Application ID
  provisioningSecretRef:
    name: losant-provisioning-credentials
    namespace: losant-system
  clusterName: "prod-edge-01"
  region: "us-west"
  rancherURL: "https://rancher.example.com"

  # Exactly one of cronSchedule or interval must be set
  cronSchedule: "*/5 * * * *"      # every 5 minutes
  # interval: "1m"                 # alternative: duration string

  gea:
    serviceRef: "losant-gea"       # in-cluster Service name
    port: 8080

  deviceRecipeID: "xyz789"         # optional: bulk peripheral provisioning
  suspend: false
```

### Status

```yaml
status:
  phase: Active                    # Provisioning | Active | Degraded | Suspended
  conditions:
    - type: GEAReachable
      status: "True"
    - type: DevicesProvisioned
      status: "True"
    - type: LastSyncSucceeded
      status: "True"
  clusterDeviceID: "losant-device-id-abc"
  nodeDevices:
    node-01: "losant-device-id-111"
    node-02: "losant-device-id-222"
  lastSyncTime: "2026-04-24T01:00:00Z"
  nextScheduledTime: "2026-04-24T01:05:00Z"
```

## Controller Architecture

### Two Controllers, One Manager

**HealthWatcherReconciler** reacts to every resource change event:
- Watches: Node, Pod, Deployment, PVC, Event (all namespaces)
- Updates the shared `HealthStore` (in-memory, RWMutex-protected)
- Never calls Losant or the GEA
- Fast path: processes events as they arrive, keeps HealthStore current

**LosantSyncReconciler** runs on schedule:
- Reads `LosantSync` CR and `ProvisioningSecretRef` Secret
- Checks `Status.NextScheduledTime` — if not yet due, returns `RequeueAfter`
- When due: runs the full reconcile sequence (see below)
- Updates `Status.LastSyncTime`, `Status.NextScheduledTime`, phase, and conditions
- Returns `RequeueAfter(nextTime - now)`

### Scheduling

Uses `github.com/robfig/cron/v3` to compute the next fire time from either `spec.cronSchedule` (cron expression) or `spec.interval` (duration string). Schedule state is persisted in `Status.NextScheduledTime`, so controller restarts re-arm without missing a cycle.

### Reconcile Sequence

On each scheduled reconcile, `LosantSyncReconciler` executes in order:

1. **Ping** (`lc.Ping`) — verifies provisioning credentials via `POST /auth/device`; sets `phase=Degraded` on failure
2. **EnsureClusterDevice** (`lc.EnsureClusterDevice`) — creates or retrieves the Edge Compute device; stores device ID in `Status.ClusterDeviceID`; sets `phase=Degraded` on failure
3. **EnsureNodeDevice** per node in HealthStore (`lc.EnsureNodeDevice`) — creates or retrieves peripheral devices; sets `phase=Degraded` on first failure
4. **ReportState cluster** (`gc.ReportState`) — POSTs cluster-level metrics to GEA HTTP endpoint; sets `phase=Degraded` on failure
5. **ReportState per node** (`gc.ReportState`) — best-effort: failures are logged but do not set `Degraded` or block the reconcile
6. Compute next `NextScheduledTime`; set `DevicesProvisioned=True`, `GEAReachable=True`, `LastSyncSucceeded=True`, `phase=Active`

For removed nodes: the controller stops reporting (device remains in Losant for historical data).

## Health Score Algorithm

```
base = 100

Per-node deductions (averaged across all nodes):
  -20  node not Ready
  -10  MemoryPressure condition True
  -10  DiskPressure condition True
  -5   PIDPressure condition True

Cluster-level deductions:
  -2 per crashloop pod  (max -20)
  -1 per failed pod     (max -10)
  -1 per warning event  (max -10)
  -15 CoreDNS not healthy
  -1 per degraded PVC   (max -10)

final = clamp(base + sum(deductions), 0, 100)
```

Health status tag mapping:
- 80–100 → `healthy`
- 50–79  → `degraded`
- 0–49   → `critical`

## Dashboard Hierarchy

### Level 1 — Fleet View

Filter: `device_type=cluster`

Shows all clusters as a table: `cluster_name`, `region`, `health_score`, `unhealthy_nodes`, `crashloop_pods`. Color-coded by `health_score`. Each row links to Level 2 via `cluster_name` context variable.

### Level 2 — Cluster View

Filter: `device_type=node` + `cluster_name=<ctx>`

Per-node bar charts (CPU%, memory%), pod distribution, CoreDNS and PVC status indicators. **Rancher Manager button** reads the `rancher_url` tag from the cluster device and renders a direct link. Each node row links to Level 3 via device ID.

### Level 3 — Node View

Single device selected by device ID context variable. Time-series CPU%, memory%, pod count over the last 6 hours. Indicator tiles for all Node condition flags (Ready, MemoryPressure, DiskPressure, PIDPressure).

## RBAC

The controller ClusterRole requires:

```yaml
- get/list/watch: nodes, pods, persistentvolumeclaims, events, deployments
- get/update/patch: losantsyncs, losantsyncs/status, losantsyncs/finalizers
- get: secrets (provisioning credentials only)
- get/list/watch/create/update/patch: leases (leader election)
```

The GEA pod has its own ServiceAccount with no Kubernetes API permissions — it only communicates outbound to `broker.losant.com`.

## Key Go Packages

```
sigs.k8s.io/controller-runtime  v0.19.x   — controller framework
k8s.io/api                       v0.31.x   — Kubernetes API types
k8s.io/client-go                 v0.31.x   — Kubernetes client
github.com/robfig/cron/v3        v3.0.1    — cron schedule parsing
go.uber.org/zap                  v1.27.x   — structured logging
github.com/onsi/ginkgo/v2                  — BDD test framework
github.com/onsi/gomega                     — test assertions
sigs.k8s.io/controller-runtime/pkg/envtest — integration test cluster
```
