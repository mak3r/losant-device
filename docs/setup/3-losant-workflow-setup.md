# Step 3: Apply LosantSync CR and Deploy Edge Workflow

Apply the LosantSync custom resource to start the controller reconciliation loop, wait for the Edge Compute device to appear in Losant, then create and deploy the Edge Workflow.

## Prerequisites

- Operator and GEA deployed and running (see [Step 2](2-cluster-deployment.md))
- `losant-gea-credentials` Secret created (see [Step 1 — Manual Pre-Provisioning](1-losant-device-setup.md#manual-pre-provisioning-create-gea-credentials))

---

## Apply the LosantSync CR

```yaml
apiVersion: losant.io/v1alpha1
kind: LosantSync
metadata:
  name: prod-edge-01
spec:
  applicationID: "<application-id-from-step-1>"
  provisioningSecretRef:
    name: losant-provisioning-credentials
    namespace: losant-system
  clusterName: "prod-edge-01"
  region: "us-west"
  rancherURL: "https://rancher.example.com"
  interval: "5m"
  gea:
    serviceRef: "losant-gea"
    port: 8080
```

Apply it:
```bash
kubectl apply -f config/samples/losant_v1alpha1_losantsync.yaml
```

Watch the controller bring the resource to `Active` phase:
```bash
kubectl get losantsync prod-edge-01 -w
```

See [docs/architecture.md](../architecture.md#crd-losantsync) for the full CRD field reference.

---

## Wait for the Edge Compute Device to Appear

The controller provisions the Edge Compute device in Losant on first reconcile, typically within 30 seconds. Before creating the workflow, confirm the device is present and Online:

1. In Losant: **Application → Devices** — the cluster device should appear (named after `LosantSync.spec.clusterName`)
2. Confirm the device shows **Online** status — this means the GEA has connected via MQTT

> If the device is not yet Online when you attempt to deploy the workflow, the deployment will fail with: *"Some of your selected devices have an agent version lower than the target version for this deployment."* Wait for the GEA pod to finish its initial MQTT handshake (check `kubectl logs deploy/losant-gea -n losant-system | grep "Connected to"`) then retry.

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

**[Step 4 → Operator configuration](4-operator-configuration.md)** — optional Device Recipe and dashboard setup.
