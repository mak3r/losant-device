# Step 1: Losant Device Setup

Configure the Losant Application and the API credentials the controller needs. The controller now handles Edge Compute device creation and GEA credential bootstrap automatically.

## Quick Start

You only need to perform three manual steps before deploying:

1. **Create a Losant Application** (Step 1 below)
2. **Create an Application API Token** (Step 2 below)
3. **Create the provisioning Kubernetes Secret** with that token (Step 3 below)

The controller provisions the Edge Compute device, GEA Access Key, and `losant-gea-credentials` Secret automatically on first reconcile — no manual secret creation required. See [Step 3](3-losant-workflow-setup.md) for applying the LosantSync CR that triggers provisioning.

---

## Prerequisites

- A Losant account at [app.losant.com](https://app.losant.com)
- `kubectl` access to your cluster (for Step 3 — creating the Kubernetes secret)

> **Auto-provisioning (fully implemented)**: The controller creates the cluster Edge Compute device, GEA Access Key, and `losant-gea-credentials` Secret automatically on first reconcile. Manual secret creation is only required when `gea.autoProvision=false` (see [Manual Pre-Provisioning](#manual-pre-provisioning-create-gea-credentials) for that path).

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

## GEA Credentials: Which Path Applies to You

**Automated provisioning (default)**: The controller creates the `losant-gea-credentials` Secret automatically on first reconcile. No manual steps needed — skip directly to [Step 2](2-cluster-deployment.md).

**Manual provisioning (exception)**: Required only if `gea.autoProvision=false` is set — for example, in air-gapped environments where the controller cannot reach `api.losant.com`. Follow the steps below if this applies to your deployment.

---

## Automated Provisioning: What to Expect

When the LosantSync CR is applied (see [Step 3](3-losant-workflow-setup.md)), the controller performs these actions on first reconcile:

1. **Creates the Edge Compute device** in Losant (named after `LosantSync.spec.clusterName`) via the REST API
2. **Generates a GEA Access Key** on the device
3. **Creates the `losant-gea-credentials` Secret** in the `losant-system` namespace with `DEVICE_ID`, `ACCESS_KEY`, and `ACCESS_SECRET`

The GEA pod picks up the secret on its next restart and connects to `mqtts://broker.losant.com`.

**Verify provisioning succeeded:**

```bash
# Confirm the secret was created
kubectl get secret losant-gea-credentials -n losant-system

# Confirm the GEA connected to Losant
kubectl logs deploy/losant-gea -n losant-system | grep "Connected to"
```

> If the secret is not present after the first reconcile completes, check the controller logs:
> ```bash
> kubectl logs -n losant-system deploy/losant-device-controller-manager | grep -i "provision\|error"
> ```

---

## Manual Pre-Provisioning: Create GEA Credentials (`gea.autoProvision=false` only)

Complete these steps **before** deploying if you are running with `gea.autoProvision=false` (e.g., air-gapped environments where the controller cannot reach `api.losant.com`).

> **No device yet?** If you are following this guide top-to-bottom, no Edge Compute device exists in Losant yet — the controller would have created it on first reconcile. You must create it manually before you can generate access keys.

### A. Create the Edge Compute device in Losant

Application → **Devices** → **Add Device** → **Edge Compute**

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

Note the **Device ID** — this is the `DEVICE_ID` value for the secret below.

### B. Create a GEA Access Key

On the Edge Compute device page → **Security** → **Add Access Key**. Note the **Access Key** (`ACCESS_KEY`) and **Access Secret** (`ACCESS_SECRET`) — the secret is shown only once.

### C. Create the `losant-gea-credentials` Kubernetes Secret

```bash
kubectl create secret generic losant-gea-credentials \
  --from-literal=DEVICE_ID=<device-id-from-step-A> \
  --from-literal=ACCESS_KEY=<access-key-from-step-B> \
  --from-literal=ACCESS_SECRET=<access-secret-from-step-B> \
  -n losant-system
```

The GEA pod reads these values at startup. If the secret is missing or has incorrect values, the GEA will fail to connect to Losant with: `access key/secret rejected`. See [docs/runbook.md](../runbook.md#gea-access-keysecret-rejected) for diagnosis steps.

---

## Next step

**[Step 2 → Cluster deployment](2-cluster-deployment.md)** — deploy the operator and GEA pod to the cluster.
