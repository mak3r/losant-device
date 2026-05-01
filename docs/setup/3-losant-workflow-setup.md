# Step 3: Losant Workflow Setup

Create and deploy the Edge Workflow that receives state reports from the controller and forwards them to Losant as device state.

## Prerequisites

- LosantSync CR applied and at least one reconcile completed (see [Step 4](4-operator-configuration.md)) — the controller creates the Edge Compute device automatically on first reconcile, typically within 30 seconds
- GEA pod deployed and showing **Online** status in Losant (see [Step 2](2-cluster-deployment.md))

> **Wait for the device to appear**: The Edge Compute device is provisioned by the controller on first reconcile. If you have not yet applied the LosantSync CR, do that now ([Step 4](4-operator-configuration.md)) and wait ~30 seconds for the device to appear in **Application → Devices** before creating the workflow.

> If the device is not Online when you attempt to deploy the workflow, the deployment will fail with: *"Some of your selected devices have an agent version lower than the target version for this deployment."* This means the GEA has not yet registered an agent version with Losant.

---

## Creating the workflow

1. In your Application → **Workflows** → **Add Workflow** → **Edge Workflow**
2. Give the workflow a name (e.g., `k8s-state-receiver`)
3. Under **Edge Devices**, select the Edge Compute device provisioned by the controller (visible in **Application → Devices** after first reconcile)

> **Note on Device trigger blocks**: The Losant workflow editor shows cloud-side trigger nodes labelled "Device" (command, connect, disconnect, startup). These fire on MQTT events from a cloud perspective and are **not** what you need here. You need an **HTTP Trigger** node, which is an edge-side trigger that listens for local HTTP requests on the GEA pod.

---

## Configuring the HTTP Trigger node

4. In the workflow canvas, add an **HTTP Trigger** node
5. Set the trigger path to `/state`
6. Set the method to `POST`
7. Leave the port at `8080` (the default; must match `LosantSync.spec.gea.port`)

---

## Payload format

The controller sends a separate POST for each device on every sync. The JSON body has this structure:

```json
{
  "deviceId": "<losant-device-id>",
  "time": "2026-04-29T12:00:00Z",
  "data": { ... }
}
```

The `time` field is omitted when the controller does not supply an explicit timestamp; the GEA then applies its own clock.

**Cluster device payload** (`data` fields):

| Field | Type | Description |
|---|---|---|
| `health_score` | number | Composite cluster health score (0–100) |
| `health_status` | string | Human-readable summary (e.g. `Healthy`, `Degraded`) |
| `total_nodes` | number | Total node count |
| `ready_nodes` | number | Nodes in Ready state |
| `unhealthy_nodes` | number | Nodes not in Ready state |
| `total_pods` | number | Total pod count across all namespaces |
| `running_pods` | number | Pods in Running phase |
| `failed_pods` | number | Pods in Failed phase |
| `pending_pods` | number | Pods in Pending phase |
| `crashloop_pods` | number | Pods in CrashLoopBackOff |
| `degraded_pvcs` | number | PersistentVolumeClaims not in Bound state |
| `coredns_healthy` | boolean | `true` if all CoreDNS pods are Running |
| `event_warnings` | number | Count of Warning-level events in the last observation window |

**Node device payload** (`data` fields):

| Field | Type | Description |
|---|---|---|
| `health_score` | number | Node health score (0–100) |
| `health_status` | string | Human-readable summary |
| `ready` | boolean | `true` if the node condition is Ready |
| `memory_pressure` | boolean | `true` if MemoryPressure condition is active |
| `disk_pressure` | boolean | `true` if DiskPressure condition is active |
| `pid_pressure` | boolean | `true` if PIDPressure condition is active |
| `pod_count` | number | Total pods scheduled on this node |
| `not_ready_pods` | number | Pods on this node not in Running phase |
| `crashloop_pods` | number | Pods on this node in CrashLoopBackOff |

---

## Routing state to the correct device

Because `deviceId` is included in every POST body, a single workflow handles both cluster and node state without branching on device type:

8. From the HTTP Trigger, add a **Device: Set State** node
9. Set the **Device** field to **Payload Path** and enter `data.body.deviceId`
10. Set the **State** field to **Payload Path** and enter `data.body.data`
11. Add a **HTTP Response** node after the Set State node:
    - Set **Response Code** to `200`
    - Set **Response Body Source** to **String Template**
    - Set **Response Body Template** to `{}`

    > The controller only checks the HTTP status code; the response body is ignored. `{}` is a valid minimal JSON body that satisfies the required field.

---

## Deploying the workflow

12. Save the workflow
13. Click **Deploy** to push it to the Edge Compute device

Once deployed, the GEA will start accepting state POSTs from the controller on `http://losant-gea:8080/state`.

---

## Next step

**[Step 4 → Operator configuration](4-operator-configuration.md)** — apply the LosantSync CR to start monitoring.
