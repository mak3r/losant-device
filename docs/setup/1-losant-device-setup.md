# Step 1: Losant Device Setup

Configure the Losant Application and the API credentials the controller needs. The controller now handles Edge Compute device creation and GEA credential bootstrap automatically.

## Quick Start

You only need to perform three manual steps before deploying:

1. **Create a Losant Application** (Step 1 below)
2. **Create an Application API Token** (Step 2 below)
3. **Create the provisioning Kubernetes Secret** with that token (Step 3 below)

The controller provisions the Edge Compute device, GEA Access Key, and `losant-gea-credentials` Secret automatically on first reconcile. See [Step 4](4-operator-configuration.md) for applying the LosantSync CR that triggers provisioning.

---

## Prerequisites

- A Losant account at [app.losant.com](https://app.losant.com)
- `kubectl` access to your cluster (for Step 3 — creating the Kubernetes secret)

> **Auto-provisioning**: The controller creates the cluster Edge Compute device and all GEA credentials automatically on first reconcile. Manual device creation is no longer required for standard deployments. See [Advanced: Manual Pre-Provisioning](#advanced-manual-pre-provisioning) at the bottom of this page for environments where outbound REST calls from the controller are restricted.

---

## Step 1: Create a Losant Application

1. Log into [app.losant.com](https://app.losant.com)
2. Click **Applications** → **Add Application**
3. Give it a name (e.g., `k8s-cluster-monitor`)
4. Note the **Application ID** — this goes into `LosantSync.spec.applicationID`

---

## Step 2: Create an Application API Token

The controller authenticates to the Losant REST API using a Losant Application API Token — **not** a device access key. Device access keys are MQTT-only credentials; they cannot make REST API calls.

1. In your Application → **Security** → **API Tokens** → **Add API Token**
2. Give it a name (e.g., `losant-device-controller`)
3. Set the expiration as appropriate for your security policy (or leave as no expiration)
4. Note the **API Token** value (shown only once)

---

## Step 3: Create the Provisioning Kubernetes Secret

The secret must go into the `losant-system` namespace. Create it first — this command is idempotent and safe to re-run:

```bash
kubectl create namespace losant-system --dry-run=client -o yaml | kubectl apply -f -
```

```bash
# Provisioning credentials — used by the controller for Losant REST API calls
kubectl create secret generic losant-provisioning-credentials \
  --from-literal=api-token=<application-api-token-from-step-2> \
  -n losant-system
```

> **Key name matters**: The controller reads a single `api-token` key from this secret. Using any other key name will cause the operator to fail at startup with a missing-key error.

---

## Next step

**[Step 2 → Cluster deployment](2-cluster-deployment.md)** — deploy the operator and GEA pod to the cluster.

---

## Advanced: Manual Pre-Provisioning

For environments where the operator cannot make outbound REST API calls to `api.losant.com` at startup (e.g., strict egress firewall rules), you can disable auto-provisioning and create the cluster device manually.

Deploy with auto-provisioning disabled:

```bash
# Helm
helm install losant-device helm/ \
  --namespace losant-system \
  --create-namespace \
  --set gea.autoProvision=false \
  ...

# Kustomize — set the GEA_AUTO_PROVISION=false env var in a kustomize overlay
```

When `autoProvision=false`, complete these manual steps **before** deploying:

1. **Create the Edge Compute device** in Losant: Application → **Devices** → **Add Device** → **Edge Compute**

   Under **Attributes**, add these grouped by type:

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

   Note the **Device ID**.

2. **Create a GEA Access Key**: on the device page → **Security** → **Add Access Key**. Note the **Access Key** and **Access Secret** (shown only once).

3. **Create the `losant-gea-credentials` secret**:
   ```bash
   kubectl create secret generic losant-gea-credentials \
     --from-literal=DEVICE_ID=<edge-compute-device-id> \
     --from-literal=ACCESS_KEY=<gea-access-key> \
     --from-literal=ACCESS_SECRET=<gea-access-secret> \
     -n losant-system
   ```

The controller will use this pre-existing secret and will not attempt to provision a device via the REST API.
