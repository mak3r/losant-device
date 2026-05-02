# Step 1: Losant Device Setup

Configure the Losant Application and the API credentials the controller needs. The controller now handles Edge Compute device creation and GEA credential bootstrap automatically.

## Quick Start

You only need to perform three manual steps before deploying:

1. **Create a Losant Application** (Step 1 below)
2. **Create an Application API Token** (Step 2 below)
3. **Create the provisioning Kubernetes Secret** with that token (Step 3 below)

The controller provisions the Edge Compute device and GEA Access Key automatically on first reconcile. **The `losant-gea-credentials` Secret must still be created manually** (see [Manual Pre-Provisioning](#manual-pre-provisioning-create-gea-credentials) below) until automated secret creation is implemented (#228). See [Step 4](4-operator-configuration.md) for applying the LosantSync CR that triggers provisioning.

---

## Prerequisites

- A Losant account at [app.losant.com](https://app.losant.com)
- `kubectl` access to your cluster (for Step 3 — creating the Kubernetes secret)

> **Auto-provisioning (partial)**: The controller creates the cluster Edge Compute device automatically on first reconcile. Automatic creation of the `losant-gea-credentials` Secret is not yet implemented (see #228) — you must still create that secret manually. See [Manual Pre-Provisioning](#manual-pre-provisioning-create-gea-credentials) below for the required steps.

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

> **Automated provisioning (coming in #228)**: The controller will create the `losant-gea-credentials` Secret automatically on first reconcile. This path is not yet implemented — see issue #228.
>
> **Manual provisioning (currently required)**: Until #228 is merged, all deployments require the `losant-gea-credentials` Secret to be created manually before the GEA pod will start. Follow the steps below.

---

## Manual Pre-Provisioning: Create GEA Credentials

Complete these steps **before** deploying if the `losant-gea-credentials` Secret does not yet exist, or if you are deploying with `gea.autoProvision=false` (e.g., in environments where the controller cannot make outbound REST API calls to `api.losant.com`).

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
