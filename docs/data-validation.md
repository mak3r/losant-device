# Data Validation — Confirming Metrics Reach Losant

This guide walks through the runtime checks that confirm the `losant-device` operator is collecting cluster health data and forwarding it to Losant. Run each section in order; stop and investigate if any check fails before proceeding.

---

## Prerequisites

- `kubectl` configured against the target cluster (`~/.kube/config` or `KUBECONFIG`)
- The operator deployed in-cluster (see [setup guide](setup/README.md)); `make run` mode cannot reach the in-cluster GEA and will fail Step 3 onward
- A `LosantSync` resource applied and at least one reconcile cycle completed

---

## 1. Kubernetes Resources Are Running

All four components must be `Running` before any data can flow.

### Controller pod

```bash
kubectl get pods -n losant-system -l app.kubernetes.io/name=losant-device
```

Expected: one pod in `Running` state with all containers `Ready`.

### GEA pod

```bash
kubectl get pods -n losant-system -l app=losant-gea
```

Expected: one pod in `Running` state. If it is stuck in `Init:0/1`, the `losant-gea-credentials` Secret is missing or empty — see [Step 5](#5-gea-credentials-are-populated).

### GEA Service

```bash
kubectl get svc -n losant-system losant-gea
```

Expected: a `ClusterIP` service with port `8080` listed.

### LosantSync resource

```bash
kubectl get losantsync
```

Expected output columns include the cluster name, phase, GEA status, last sync time, and next sync time:

```
NAME        CLUSTER    PHASE    GEA    LAST SYNC   NEXT SYNC   AGE
my-cluster  my-site    Active   True   2m          5m          10m
```

If `PHASE` is `Provisioning`, the first reconcile cycle has not completed yet — wait 30 seconds and re-run.
If `PHASE` is `Degraded`, proceed to [Step 6](#6-check-controller-logs).

---

## 2. LosantSync Conditions Show All Green

The `LosantSync` status block carries four conditions that map to the four reconcile steps. All must be `True` for data to flow end-to-end.

```bash
kubectl get losantsync <NAME> -o jsonpath='{.status.conditions}' | jq .
```

| Condition | What it means |
|---|---|
| `GEABootstrapped` | The controller created the `losant-gea-credentials` Secret and restarted the GEA pod |
| `DevicesProvisioned` | All cluster and node devices exist in the Losant application |
| `GEAReachable` | The last `POST /state` to the GEA returned HTTP 2xx |
| `LastSyncSucceeded` | The full reconcile cycle (provisioning + reporting) completed without error |

Expected: all four conditions show `status: "True"`.

If any condition is `False`, the `message` field contains the exact error. Example:

```bash
kubectl get losantsync <NAME> -o jsonpath='{.status.conditions[?(@.type=="GEAReachable")].message}'
```

---

## 3. GEA Is Accepting State Payloads

The controller sends a JSON payload to `http://losant-gea:8080/state` on each reconcile. You can test this manually from a temporary pod:

```bash
kubectl run gea-probe --rm -it --image=curlimages/curl --restart=Never -n losant-system -- \
  curl -s -o /dev/null -w "%{http_code}" \
  -X POST http://losant-gea:8080/state \
  -H "Content-Type: application/json" \
  -d '{"deviceId":"test","data":{}}'
```

Expected: HTTP status `200`. Any `4xx` or `5xx` response, or a connection refused error, means the GEA is not ready — check the GEA pod logs (Step 4).

---

## 4. GEA Pod Logs Show MQTT Connection to Losant

The GEA pod logs confirm whether it has established an MQTT connection to `broker.losant.com` and is forwarding buffered state data.

```bash
kubectl logs -n losant-system -l app=losant-gea --tail=100
```

**Signs the connection is established:**

- Log line containing `Connected` or `MQTT connected` — confirms the GEA has authenticated with Losant
- Log lines containing `Sending device state` or `state report` — confirms data is being forwarded
- Absence of `MQTT disconnected` or `reconnecting` lines at the end of the log

**Signs of a problem:**

| Log message | Likely cause |
|---|---|
| `waiting for GEA credentials secret` | `losant-gea-credentials` Secret not yet populated — wait for bootstrap (Step 5) |
| `MQTT connect failed` | Wrong `ACCESS_KEY` / `ACCESS_SECRET` in the Secret, or `broker.losant.com` unreachable |
| `invalid device ID` | `DEVICE_ID` in the Secret does not match a device in the Losant application |
| No output / pod restarting | Init container still waiting, or pod in CrashLoop — `kubectl describe pod` for details |

---

## 5. GEA Credentials Are Populated

The `losant-gea-credentials` Secret contains the three values the GEA needs to authenticate with Losant. Check that all three are non-empty:

```bash
kubectl get secret losant-gea-credentials -n losant-system \
  -o jsonpath='{.data}' | jq 'to_entries[] | {key: .key, empty: (.value | @base64d | length == 0)}'
```

Expected: `"empty": false` for `DEVICE_ID`, `ACCESS_KEY`, and `ACCESS_SECRET`.

If any value is empty, the controller has not completed bootstrap yet. Check the `GEABootstrapped` condition (Step 2) and the controller logs (Step 6) for details.

---

## 6. Check Controller Logs for Reconcile Output

The controller emits a log line for each reconcile step. A successful cycle looks like:

```bash
kubectl logs -n losant-system -l app.kubernetes.io/name=losant-device --tail=200
```

Key log lines in a healthy cycle (in order):

| Log message | What it confirms |
|---|---|
| `sync due, starting provisioning and reporting` | A reconcile cycle started |
| `GEA credentials provisioned` | Bootstrap completed (only on first run) |
| `all devices confirmed in Losant` | Cluster and node devices exist in Losant |
| `GEA accepted cluster state` | Controller successfully POSTed to the GEA |
| `sync complete` | Full cycle succeeded; `nextSync` shows when the next one fires |

**Error patterns to watch for:**

| Log message | Meaning |
|---|---|
| `Losant ping failed` | Cannot reach `api.losant.com` — network or credential issue |
| `failed to ensure cluster device` | API error creating/fetching the cluster Edge Compute device |
| `GEA unreachable, will retry` | `POST /state` to the GEA failed — check GEA pod status |
| `failed to ensure node device` | A node's peripheral device could not be provisioned |
| `reconciliation suspended` | `LosantSync.spec.suspend` is `true` — no data is sent while suspended |

---

## 7. Verify Devices and Attributes Exist in Losant

Log into the [Losant dashboard](https://app.losant.com) and navigate to your application.

### Confirm the cluster Edge Compute device

1. Go to **Devices** and search for the cluster name (value of `spec.clusterName` in the `LosantSync` resource)
2. The device should exist as an **Edge Compute** device type
3. Open the device and check **Recent Device State** — you should see a state report with these attributes:

| Attribute | Description |
|---|---|
| `health_score` | Overall cluster health score (0–100) |
| `health_status` | Human-readable status string |
| `total_nodes` | Total node count |
| `ready_nodes` | Nodes in Ready state |
| `unhealthy_nodes` | Nodes not Ready |
| `total_pods` | Total pod count across all namespaces |
| `running_pods` | Pods in Running phase |
| `failed_pods` | Pods in Failed phase |
| `pending_pods` | Pods in Pending phase |
| `crashloop_pods` | Pods in CrashLoopBackOff |
| `degraded_pvcs` | PersistentVolumeClaims not in Bound state |
| `coredns_healthy` | Whether CoreDNS pods are running |
| `event_warnings` | Count of Warning-level cluster events |

### Confirm node peripheral devices

1. In **Devices**, search for the node hostnames (one device per k8s node)
2. Each node device should show **Peripheral** type and be associated with the cluster Edge Compute device
3. Open a node device and check **Recent Device State** — expected attributes:

| Attribute | Description |
|---|---|
| `health_score` | Node health score (0–100) |
| `health_status` | Human-readable status string |
| `ready` | Node Ready condition |
| `memory_pressure` | MemoryPressure condition |
| `disk_pressure` | DiskPressure condition |
| `pid_pressure` | PIDPressure condition |
| `pod_count` | Total pods scheduled on this node |
| `not_ready_pods` | Pods on this node that are not Ready |
| `crashloop_pods` | CrashLoopBackOff pods on this node |
| `cpu_request_pct` | CPU request utilisation (requests / allocatable) |
| `mem_request_pct` | Memory request utilisation (requests / allocatable) |

If devices exist but **Recent Device State** is empty, the GEA has not forwarded data yet — allow up to one full reconcile interval and recheck.

---

## 8. Quick End-to-End Checklist

Run all checks in sequence and confirm each passes before moving to the next:

```bash
# 1. All pods running
kubectl get pods -n losant-system

# 2. LosantSync phase and GEA column are Active / True
kubectl get losantsync

# 3. All four conditions are True
kubectl get losantsync <NAME> -o jsonpath='{.status.conditions[*].type} {.status.conditions[*].status}'

# 4. Credentials populated
kubectl get secret losant-gea-credentials -n losant-system -o jsonpath='{.data.DEVICE_ID}' | base64 -d | wc -c

# 5. Controller completed a successful sync
kubectl logs -n losant-system -l app.kubernetes.io/name=losant-device --tail=50 | grep "sync complete"

# 6. GEA is connected (look for 'Connected' in GEA logs)
kubectl logs -n losant-system -l app=losant-gea --tail=50 | grep -i "connect"
```

All six checks passing means data is flowing from the cluster to Losant.
