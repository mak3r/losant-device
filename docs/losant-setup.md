# Losant Setup

One-time configuration steps required before deploying the operator.

## Prerequisites

- A Losant account at [app.losant.com](https://app.losant.com)
- An existing Losant Application (or follow step 1 to create one)

> **Bootstrap constraint**: The cluster Edge Compute device (Step 2) must exist in Losant **before** the operator starts. The controller discovers and manages peripheral (node) devices automatically, but the cluster-level Edge Compute device must be pre-provisioned manually.

> **Ordering note**: Steps 1–3 (Application, Edge Compute device, and GEA access key) can be completed entirely within the Losant UI — no running cluster is required. However, Step 4 step 13 (deploying the Edge Workflow to the device) requires the GEA pod to already be running and connected to Losant. Complete [docs/deployment.md](deployment.md) to deploy the operator and GEA before attempting that step.

---

## Step 1: Create a Losant Application

1. Log into [app.losant.com](https://app.losant.com)
2. Click **Applications** → **Add Application**
3. Give it a name (e.g., `k8s-cluster-monitor`)
4. Note the **Application ID** — this goes into `LosantSync.spec.applicationID`

---

## Step 2: Create the Edge Compute Device (Cluster Device)

This device represents the cluster in Losant. Its credentials are used by the GEA pod.

1. Inside your Application, click **Devices** → **Add Device**
2. Choose device type: **Edge Compute**
3. Name it `cluster-<your-cluster-name>` (e.g., `cluster-prod-edge-01`)
4. Add the following tags (these can also be managed by the controller at runtime):
   ```
   device_type  = cluster
   cluster_name = <your-cluster-name>
   region       = <your-region>
   health_status = provisioning
   ```

   > **Tags vs. Attributes**: Losant *tags* are device metadata used for filtering and search — they are not time-series data. Losant *attributes* store state values reported over time (what the controller POSTs on every sync). `health_status` appears as both: a tag that holds the initial provisioning state and an attribute that receives ongoing string state updates from the controller. You must create it in the Attributes list below so the controller's state reports are accepted.

5. Under **Attributes**, add these grouped by type:

   **Number** (data type: `number`):
   ```
   total_nodes, ready_nodes, unhealthy_nodes
   total_pods, running_pods, failed_pods, pending_pods, crashloop_pods
   degraded_pvcs, event_warnings, health_score
   ```

   **Boolean** (data type: `boolean`):
   ```
   coredns_healthy
   ```

   **String** (data type: `string`):
   ```
   health_status
   ```
6. Note the **Device ID** — this is the GEA's identity

---

## Step 3: Create the GEA Access Key

The GEA pod needs its own credentials to authenticate to Losant as the Edge Compute device.

1. On the Edge Compute device page → **Security** → **Add Access Key**
2. Note the **Access Key** and **Access Secret** (the secret is shown only once)

---

## Step 4: Create the Edge Workflow

The GEA runs an Edge Workflow that receives state reports from the controller over HTTP and forwards them to Losant as device state. The controller POSTs one request per device (cluster device + one per node) on every sync cycle.

> **Prerequisite**: The GEA pod must be running and connected to Losant before the Edge Compute device appears as a workflow target. If your device is not listed in the workflow creation dialog, verify the GEA pod is healthy and the device shows **Online** status in Losant before continuing.

### Creating the workflow

1. In your Application → **Workflows** → **Add Workflow** → **Edge Workflow**
2. Give the workflow a name (e.g., `k8s-state-receiver`)
3. Under **Edge Devices**, select the Edge Compute device you created in Step 2

> **Note on Device trigger blocks**: The Losant workflow editor shows cloud-side trigger nodes labelled "Device" (command, connect, disconnect, startup). These fire on MQTT events from a cloud perspective and are **not** what you need here. You need an **HTTP Trigger** node, which is an edge-side trigger that listens for local HTTP requests on the GEA pod.

### Configuring the HTTP Trigger node

4. In the workflow canvas, add an **HTTP Trigger** node
5. Set the trigger path to `/state`
6. Set the method to `POST`
7. Leave the port at `8080` (the default; must match `LosantSync.spec.gea.port`)

### Payload format

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

### Routing state to the correct device

Because `deviceId` is included in every POST body, a single workflow handles both cluster and node state without branching on device type:

8. From the HTTP Trigger, add a **Device: Set State** node
9. Set the **Device** field to **Payload Path** and enter `data.body.deviceId`
10. Set the **State** field to **Payload Path** and enter `data.body.data`
11. Add a **HTTP Response** node after the Set State node:
    - Set **Response Code** to `200`
    - Set **Response Body Source** to **String Template**
    - Set **Response Body Template** to `{}`

    > The controller only checks the HTTP status code; the response body is ignored. `{}` is a valid minimal JSON body that satisfies the required field.

### Deploying the workflow

> **Prerequisite before step 13**: The GEA pod must be deployed to the cluster and show **Online** status in Losant before the workflow deployment will succeed. If you attempt to deploy while the device has never connected, Losant will show:
> > "Some of your selected devices have an agent version lower than the target version for this deployment."
>
> This error means the GEA has not yet registered an agent version with Losant — it has not connected. Complete [docs/deployment.md](deployment.md) first, then return here.

12. Save the workflow
13. Click **Deploy** to push it to the Edge Compute device

Once deployed, the GEA will start accepting state POSTs from the controller on `http://losant-gea:8080/state`.

---

## Step 5: Create an Application API Token

The controller authenticates to the Losant REST API using a Losant Application API Token — **not** a device access key. Device access keys are MQTT-only credentials used by the GEA pod; they cannot make REST API calls.

1. In your Application → **Security** → **API Tokens** → **Add API Token**
2. Give it a name (e.g., `losant-device-controller`)
3. Set the expiration as appropriate for your security policy (or leave as no expiration)
4. Note the **API Token** value (shown only once)

---

## Step 6: Create Kubernetes Secrets

```bash
# GEA credentials — mounted into the GEA pod as environment variables
kubectl create secret generic losant-gea-credentials \
  --from-literal=DEVICE_ID=<edge-compute-device-id-from-step-2> \
  --from-literal=ACCESS_KEY=<gea-access-key-from-step-3> \
  --from-literal=ACCESS_SECRET=<gea-access-secret-from-step-3> \
  -n losant-system

# Provisioning credentials — used by the controller for Losant REST API calls
kubectl create secret generic losant-provisioning-credentials \
  --from-literal=api-token=<application-api-token-from-step-5> \
  -n losant-system
```

> **Key name matters**: The controller reads a single `api-token` key from this secret. Using any other key name will cause the operator to fail at startup with a missing-key error.

---

## Step 7: (Optional) Create a Device Recipe for Bulk Node Provisioning

For clusters with many nodes, a Device Recipe enables bulk peripheral device creation via CSV.

1. Application → **Devices** → **Device Recipes** → **Add Recipe**
2. Configure the recipe with the node device attribute schema (same attributes as above but for node devices)
3. Note the **Recipe ID** — this goes into `LosantSync.spec.deviceRecipeID`

---

## Step 8: Apply the LosantSync CR

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

---

## Dashboard Setup

Once the operator is running and reporting data, create the three-level dashboard hierarchy in Losant:

1. **Fleet Dashboard**: filter on `device_type=cluster`; add table block with `cluster_name`, `region`, `health_score`
2. **Cluster Dashboard**: add context variable `cluster_name`; filter on `device_type=node` + context variable
3. **Node Dashboard**: add context variable for device ID; use single-device blocks for time-series

See [docs/architecture.md](architecture.md#dashboard-hierarchy) for the full dashboard specification.

---

## Troubleshooting

### Secret key names are wrong

The GEA pod and the controller each require exact key names in their respective secrets. Hyphenated or lowercase variants are not accepted.

| Secret | Required key names |
|---|---|
| `losant-gea-credentials` | `DEVICE_ID`, `ACCESS_KEY`, `ACCESS_SECRET` |
| `losant-provisioning-credentials` | `api-token` |

To verify the keys that are actually stored in a secret:

```bash
kubectl get secret losant-gea-credentials -n losant-system \
  -o jsonpath='{.data}' | jq 'keys'
```

Expected output:
```json
["ACCESS_KEY", "ACCESS_SECRET", "DEVICE_ID"]
```

If any key is missing or named differently (e.g., `access-key`, `ACCESS-KEY`, `device_id`), delete and recreate the secret using the exact names shown in Step 6.

---

### GEA is in CrashLoopBackOff but the cluster looks healthy

A failing liveness or readiness probe will cause Kubernetes to restart the GEA pod and report `CrashLoopBackOff`, even when the GEA has already established an MQTT connection to Losant. Check the logs before assuming the GEA itself is broken:

```bash
kubectl logs deploy/losant-gea -n losant-system
```

If the output contains the line:

```
Connected to: mqtts://broker.losant.com
```

the GEA is healthy. The restart loop is caused by a probe misconfiguration, not a GEA failure. Verify that the probe endpoint and port in your GEA deployment match the HTTP Trigger path (`/state`, port `8080`).

---

### Confirming the controller is reaching the GEA

Once the GEA pod is stable (probes passing, no crash loop), confirm that the controller is successfully POSTing state to it:

```bash
# Watch the GEA pod logs for incoming requests
kubectl logs deploy/losant-gea -n losant-system --follow

# In another terminal, trigger a reconcile by patching the LosantSync
kubectl annotate losantsync prod-edge-01 force-sync=$(date +%s) --overwrite
```

Each reconcile cycle should produce log lines similar to:

```
POST /state 200  deviceId=<cluster-device-id>
POST /state 200  deviceId=<node-device-id>
```

If no POST lines appear, check that `LosantSync.spec.gea.serviceRef` and `.spec.gea.port` match the GEA service name and port (`losant-gea`, `8080`) in the cluster.
