# Rancher Integration — Dynamic Cluster Connect/Disconnect

This guide covers everything an operator needs to connect and disconnect edge clusters from Rancher Manager on demand, via Losant device commands.

---

## Overview

The **Rancher dynamic connect/disconnect** feature lets you register an edge cluster with Rancher Manager through a Losant dashboard button or workflow trigger, and remove it again when done — without touching the edge cluster directly.

### Architecture

```
Losant cloud workflow
  ↓  device command  (action=connect|disconnect, ttlSeconds=N)
GEA edge workflow (on cluster)
  ↓  HTTP POST  (losant-device-trigger:9090/rancher)
Trigger receiver server (inside controller pod)
  ↓  creates / deletes RancherSession CR
RancherSessionReconciler
  ↓  Rancher v3 REST API  (POST/DELETE /v3/clusters)
Rancher Manager
  ↓  generates import manifest
RancherSessionReconciler
  ↓  applies manifest to cluster
cattle-cluster-agent (in cattle-system namespace)
  ↑→ Rancher Manager  (upstream registration complete)
```

### Guarantee

Disconnecting a cluster from Rancher — by any method — removes only `cattle-system` and the Rancher cluster record. It does **not** affect the edge cluster's workloads, `losant-system`, LosantSync reporting, or any other namespace.

---

## Prerequisites

Complete all steps in this section before using the connect/disconnect feature.

### 1. Create the Rancher service account and API token

In Rancher Manager, create a service account token scoped to the minimum permissions required by the controller.

**Create the Global Role:**

```yaml
apiVersion: management.cattle.io/v3
kind: GlobalRole
metadata:
  name: losant-device-controller
rules:
- apiGroups:
  - management.cattle.io
  resources:
  - clusters
  verbs:
  - create
  - get
  - delete
```

**Bind the role to a service account user:**

```yaml
apiVersion: management.cattle.io/v3
kind: GlobalRoleBinding
metadata:
  name: losant-device-controller-binding
globalRoleName: losant-device-controller
userName: <rancher-service-account-user>
```

Generate an API token for that user in **Rancher UI → User Settings → API Keys**. Record the token value — it is shown only once.

> **Scope note:** This token is shared across all edge clusters managed by the controller. Per-cluster tokens are a future hardening option.

### 2. Obtain the Rancher CA certificate (private CAs only)

> **Note:** Skip this step if your Rancher Manager endpoint is signed by a publicly-trusted CA (e.g., AWS ACM, Let's Encrypt). The controller uses the system certificate pool automatically. Supply `RANCHER_CA` only when using a private or self-signed CA.

**From the Rancher cluster secret:**

```bash
kubectl get secret tls-rancher-internal-ca \
  -n cattle-system \
  --context <rancher-cluster-context> \
  -o jsonpath='{.data.cacerts\.pem}' | base64 -d > rancher-ca.pem
```

Adjust the secret name to match your Rancher installation.

**From the TLS endpoint (when cluster access is unavailable):**

```bash
openssl s_client -connect <rancher-host>:443 -showcerts </dev/null 2>/dev/null \
  | awk '/-----BEGIN CERTIFICATE-----/{c=""} {c=c"\n"$0} /-----END CERTIFICATE-----/{last=c} END{print last}' \
  > rancher-ca.pem
```

### 3. Enable Rancher support in Helm

The trigger receiver Service and RancherSession CRD are gated by `rancher.enabled`. Set this in your Helm values or upgrade command:

```bash
helm upgrade losant-device \
  oci://ghcr.io/mak3r/losant-device/charts/losant-device:<VERSION> \
  --namespace losant-system \
  --set rancher.enabled=true
```

Verify the trigger Service exists after upgrade:

```bash
kubectl get svc -n losant-system losant-device-trigger
```

### 4. Create the `rancher-credentials` Secret

Create or update the Secret in `losant-system` on the **edge cluster** using the dry-run/apply pattern, which handles both first-time creation and updates (Helm creates an empty `rancher-credentials` shell when `rancher.enabled=true`):

```bash
kubectl create secret generic rancher-credentials \
  --namespace losant-system \
  --from-literal=RANCHER_URL=https://rancher.example.com \
  --from-literal=RANCHER_TOKEN=<token-from-step-1> \
  --from-file=RANCHER_CA=rancher-ca.pem \
  --dry-run=client -o yaml | kubectl apply -f -
```

If your Rancher endpoint uses a publicly-trusted CA (see Step 2), omit `--from-file=RANCHER_CA`.

The controller reads the following keys:

| Key | Required | Value |
|---|---|---|
| `RANCHER_URL` | Yes | Base URL of your Rancher Manager instance |
| `RANCHER_TOKEN` | Yes | Service account Bearer token from Step 1 |
| `RANCHER_CA` | No | PEM-encoded CA certificate (private/self-signed CAs only; see Step 2) |

> *Omit `RANCHER_CA` when your Rancher endpoint uses a certificate signed by a public CA. Supply it only when using a private or self-signed CA.*

---

## Creating the Losant Cloud Workflow (Required)

The connect/disconnect path does not function without a Losant cloud workflow that sends device commands to the cluster's Losant device.

### Create the cloud workflow

1. Navigate to your Losant Application, select **Workflows** in the left sidebar, click **Add Workflow**, and set the **Type** combo box to **Application**.
2. Name it (e.g., `rancher-connect-disconnect`).
3. Add a trigger node — **Virtual Button** (for manual use) or **Dashboard Button** (for dashboard-triggered operations).
4. Add a **Device Command** node after the trigger:
   - **Device**: select the cluster's Edge Compute Losant device by name or via a context variable
   - **Command Name**: `rancher`
   - **Payload**: construct the payload from your trigger input:

**Connect payload:**
```json
{
  "action": "connect",
  "ttlSeconds": 3600
}
```

**Disconnect payload:**
```json
{
  "action": "disconnect"
}
```

5. Save and deploy the workflow.

### Verify command delivery

After triggering the workflow, confirm the command reached the device:

1. In Losant UI, navigate to the cluster's Edge Compute device.
2. Select **Debug** → **Command Log**.
3. A `rancher` command entry with your payload should appear within a few seconds.

---

## Creating the GEA Edge Workflow (Required)

The GEA edge workflow runs on the GEA pod inside the edge cluster. It receives the Losant device command and forwards it as an HTTP call to the controller's trigger server.

### Create the edge workflow

1. Navigate to your Losant Application, select **Workflows** in the left sidebar, click **Add Workflow**, and set the **Type** combo box to **Edge**.
2. Name it (e.g., `rancher-trigger`).
3. Add a **Device Command** trigger node:
   - **Command Name**: `rancher`
4. Add an **HTTP** node after the trigger:
   - **Method**: `POST`
   - **URL**: `http://losant-device-trigger.losant-system.svc.cluster.local:9090/rancher`
   - **Body**: the full device command payload (pass through from the trigger node output)
   - **Content-Type**: `application/json`
5. Save a named version (e.g., `v1.0.0`) — see [Finding or creating a named workflow version](setup/4-losant-workflow.md#finding-or-creating-a-named-workflow-version).

### Deploy the edge workflow

> **Note:** The rancher-trigger is a second, separate edge workflow — distinct from the health-monitoring edge workflow you deployed during the main setup guide. Add it as an additional entry in `spec.workflowDeployments`; do not replace the existing entry.

Add the workflow to your `LosantSync` CR under `spec.workflowDeployments`:

```yaml
spec:
  workflowDeployments:
    - flowId: "<existing-health-monitoring-workflow-id>"
      version: "<existing-version>"
    - flowId: "<rancher-trigger-workflow-id>"
      version: "v1.0.0"
```

Apply the change and confirm deployment:

```bash
kubectl apply -f losantsync.yaml
kubectl get losantsync <NAME> \
  -o jsonpath='{.status.conditions[?(@.type=="WorkflowDeployed")]}' | jq .
```

The `WorkflowDeployed` condition should reach `Deployed`. See [Step 4: Edge Workflow Deployment](setup/4-losant-workflow.md) for full details on workflow deployment and troubleshooting.

### Verify the edge workflow is active

In Losant UI, navigate to the cluster's Edge Compute device → **Debug** tab. After deploying, the edge workflow appears in the active workflow list.

---

## Connect/Disconnect Operations

### Connect a cluster

Send a `connect` command via the Losant cloud workflow (virtual button, dashboard button, or API call). The command payload must include `action: connect`:

```json
{ "action": "connect", "ttlSeconds": 3600 }
```

The controller creates a `RancherSession` CR, calls the Rancher API to register the cluster, applies the Rancher import manifest, and waits for the `cattle-cluster-agent` to become ready. The full sequence takes 1–3 minutes on a typical k3s cluster.

Monitor progress:

```bash
kubectl get ranchersession -n losant-system
kubectl describe ranchersession <name> -n losant-system
```

The session transitions through phases: `Connecting` → `Connected`.

### Disconnect via Losant workflow

Send a `disconnect` command via the same cloud workflow:

```json
{ "action": "disconnect" }
```

The controller deletes the `RancherSession` CR, which triggers the finalizer cleanup: Rancher's cluster record is deleted and `cattle-system` is removed from the edge cluster.

### Disconnect directly from the Rancher UI

If you delete the cluster entry from the Rancher Manager UI, the controller detects the missing cluster record on the next reconcile cycle (`GetCluster` returns 404) and automatically cleans up the `cattle-system` namespace and removes the `RancherSession` CR.

No action is required in Losant or on the edge cluster.

### TTL-based automatic disconnect

The `ttlSeconds` field in the connect payload controls how long the session remains active. When the TTL expires, the controller runs the same cleanup as a manual disconnect. The `status.expiresAt` field shows the exact expiry time:

```bash
kubectl get ranchersession -n losant-system \
  -o jsonpath='{.items[0].status.expiresAt}'
```

The Losant Level 2 cluster dashboard surfaces `expiresAt` as an indicator tile when the cluster is connected.

---

## Troubleshooting

### `RancherSession` stuck in `Connecting`

The session remains in `Connecting` while the controller waits for `cattle-cluster-agent` to become Ready in `cattle-system`. Check the conditions:

```bash
kubectl describe ranchersession <name> -n losant-system
```

Common causes:
- `cattle-cluster-agent` image pull failure — check image availability from the edge cluster's network
- Rancher import manifest never fetched — check network connectivity from the controller pod to `RANCHER_URL`

```bash
kubectl get pods -n cattle-system
kubectl describe deployment cattle-cluster-agent -n cattle-system
```

### Rancher API unreachable — `RancherAPIReachable=False`

The `RancherAPIReachable` condition is set to `False` when the controller cannot reach `RANCHER_URL` or the API returns an error.

```bash
kubectl get ranchersession <name> -n losant-system \
  -o jsonpath='{.status.conditions[?(@.type=="RancherAPIReachable")]}' | jq .
```

Check:
1. `RANCHER_URL` in the `rancher-credentials` Secret is correct and reachable from the cluster
2. `RANCHER_TOKEN` is valid and not expired
3. If using a private CA: `RANCHER_CA` in the Secret matches the CA used by your Rancher Manager instance. `RANCHER_CA` is optional — omit it when your Rancher endpoint is signed by a publicly-trusted CA (e.g., Let's Encrypt, ACM)
4. Network policies or firewall rules do not block outbound HTTPS from `losant-system`

Verify the Secret keys are populated:

```bash
kubectl get secret rancher-credentials -n losant-system \
  -o jsonpath='{.data}' | jq 'keys'
```

### Cluster not appearing in Rancher after connect

If the `RancherSession` reaches `Connected` but the cluster does not appear as healthy in Rancher Manager:

1. Check `cattle-cluster-agent` status:

```bash
kubectl get deployment cattle-cluster-agent -n cattle-system
kubectl get pods -n cattle-system
```

2. Check for image pull errors — the import manifest references Rancher's agent image, which must be reachable from the edge cluster.

3. Verify `status.manifestApplied` is `true`:

```bash
kubectl get ranchersession <name> -n losant-system \
  -o jsonpath='{.status.manifestApplied}'
```

If `false`, the controller could not apply the import manifest. Check controller logs:

```bash
kubectl logs -n losant-system \
  -l app.kubernetes.io/name=losant-device \
  --since=10m | grep ranchersession
```
