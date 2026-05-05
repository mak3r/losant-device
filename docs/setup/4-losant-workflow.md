# Step 4: Edge Workflow Deployment

The operator automatically deploys Edge Workflows declared in `spec.workflowDeployments`. Once the LosantSync CR is applied and the cluster device is provisioned, the controller calls the Losant Edge Deployment API on each reconcile cycle to ensure the declared workflow versions are running on the GEA.

> **Previous versions** required manually creating and deploying the Edge Workflow in the Losant UI. This step is now fully automated as of the `workflowDeployments` spec field.

---

## Prerequisites

- LosantSync CR at `Active` phase (see [Step 3](3-deploy.md))
- The Edge Workflow already exists in your Losant Application with at least one named version (create it in **Application → Workflows → Add Workflow → Edge Workflow**)
- The Losant Application API Token has the `edgeDeployments.release` scope (see [Step 1](1-losant-application.md))

---

## Declare workflows in the LosantSync spec

Add a `workflowDeployments` list to your `LosantSync` manifest. Each entry declares one workflow version to keep deployed on the cluster's Edge Compute device:

```yaml
apiVersion: losant.io/v1alpha1
kind: LosantSync
metadata:
  name: prod-edge-01
spec:
  applicationID: "<application-id>"
  provisioningSecretRef:
    name: losant-provisioning-credentials
    namespace: losant-system
  clusterName: "prod-edge-01"
  region: "us-west"
  interval: "5m"
  gea:
    serviceRef: "losant-gea"
    port: 8080
  workflowDeployments:
    - flowId: "<losant-workflow-id>"
      version: "v1.0.0"
```

| Field | Description |
|---|---|
| `flowId` | Losant Edge Workflow ID (see [Finding a workflow ID](#finding-a-workflow-id) below) |
| `version` | Named version string to deploy — must exactly match a saved version name in Losant (see [Finding or creating a named workflow version](#finding-or-creating-a-named-workflow-version) below) |

`workflowDeployments` is optional. If omitted, no workflow deployment is attempted and the `WorkflowDeployed` condition is not set.

---

## Finding a workflow ID

In the Losant UI, the workflow ID appears in the browser URL when you open a workflow:

```
https://app.losant.com/applications/<appId>/flows/<flowId>/...
```

Copy the `<flowId>` segment. Alternatively, retrieve it via the Losant API:

```bash
curl -s -H "Authorization: Bearer <api-token>" \
  "https://api.losant.com/applications/<appId>/flows?flowClass=edge" \
  | jq '.items[] | {id: .id, name: .name}'
```

---

## Finding or creating a named workflow version

Losant **auto-names version snapshots using timestamps** (e.g. `"2026-05-05T12-39-04"`) unless you create a snapshot with an explicit name. If you write `version: "v1.0.0"` in your CR but the workflow has no snapshot with that exact name, the controller returns `WorkflowDeployed=False` with reason `WorkflowNotFound` and a 404 in the logs.

**To find or create a named version snapshot:**

1. In the Losant UI, open your Application and select **Workflows** in the left side menu.
2. Select the **Edge** tab in the center pane near the top.
3. Click the Edge Workflow you want to deploy.
4. Select the **Versions** tab on the right side of the editor window.
5. The currently active version name is displayed at the top.
6. Use the filter field to search for other existing saved versions.
7. To create a new named version: click **Create Version**, enter a meaningful name (e.g. `v1.0.0`), and save. That name is now valid for the `version` field.

> **Tip**: If you are using auto-generated timestamp versions (e.g. the default Losant snapshot name), copy the exact string shown in the Versions tab — including the `T` and dashes — and paste it into your CR.

---

## Monitor deployment status

The controller sets the `WorkflowDeployed` condition on the `LosantSync` status after processing workflow deployments:

```bash
kubectl get losantsync <NAME> -o jsonpath='{.status.conditions[?(@.type=="WorkflowDeployed")]}' | jq .
```

| Reason | Meaning |
|---|---|
| `Deployed` | All declared workflow versions are confirmed running on the GEA |
| `DeploymentPending` | Release was sent to Losant; awaiting GEA confirmation (normal when GEA was just restarted or reconnected) |
| `ReleaseFailed` | Losant API call to release the workflow failed; check controller logs |
| `WorkflowNotFound` | The `flowId` does not exist, or the `version` string does not match any saved version snapshot in Losant; see [Finding or creating a named workflow version](#finding-or-creating-a-named-workflow-version) and [Troubleshooting](7-troubleshooting.md#workflowdeployedfalse--reason-workflownotfound) |

`DeploymentPending` resolves automatically on the next reconcile cycle once the GEA confirms receipt of the deployment.

---

## Payload format reference

The controller sends one HTTP POST per device (cluster + each node) on every sync. The GEA workflow receives these payloads via the HTTP trigger on port `8080`. The wire format:

```json
{
  "deviceId": "<losant-device-id>",
  "time": "2026-04-29T12:00:00Z",
  "data": { ... }
}
```

The `time` field is omitted when the controller does not supply an explicit timestamp; the GEA applies its own clock.

**Cluster device attributes** (`data` fields):

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

**Node device attributes** (`data` fields):

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
| `cpu_request_pct` | number | CPU requests as a fraction of allocatable (0.0–1.0) |
| `mem_request_pct` | number | Memory requests as a fraction of allocatable (0.0–1.0) |

---

## Next step

**[Step 5 → Verify](5-verify.md)** — confirm metrics are flowing and review common failure modes.
