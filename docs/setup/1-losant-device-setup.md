# Step 1: Losant Device Setup

Configure the Losant Application and Edge Compute device. All steps in this document are performed in the Losant UI — no running cluster is required.

## Prerequisites

- A Losant account at [app.losant.com](https://app.losant.com)
- `kubectl` access to your cluster (for Step 5 — creating Kubernetes secrets)

> **Bootstrap constraint**: The cluster Edge Compute device (Step 2 below) must exist in Losant **before** the operator starts. The controller discovers and manages peripheral (node) devices automatically, but the cluster-level Edge Compute device must be pre-provisioned manually.

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

## Step 4: Create an Application API Token

The controller authenticates to the Losant REST API using a Losant Application API Token — **not** a device access key. Device access keys are MQTT-only credentials used by the GEA pod; they cannot make REST API calls.

1. In your Application → **Security** → **API Tokens** → **Add API Token**
2. Give it a name (e.g., `losant-device-controller`)
3. Set the expiration as appropriate for your security policy (or leave as no expiration)
4. Note the **API Token** value (shown only once)

---

## Step 5: Create Kubernetes Secrets

The secrets must go into the `losant-system` namespace. Create it first — this command is idempotent and safe to re-run:

```bash
kubectl create namespace losant-system --dry-run=client -o yaml | kubectl apply -f -
```

```bash
# GEA credentials — mounted into the GEA pod as environment variables
kubectl create secret generic losant-gea-credentials \
  --from-literal=DEVICE_ID=<edge-compute-device-id-from-step-2> \
  --from-literal=ACCESS_KEY=<gea-access-key-from-step-3> \
  --from-literal=ACCESS_SECRET=<gea-access-secret-from-step-3> \
  -n losant-system

# Provisioning credentials — used by the controller for Losant REST API calls
kubectl create secret generic losant-provisioning-credentials \
  --from-literal=api-token=<application-api-token-from-step-4> \
  -n losant-system
```

> **Key name matters**: The GEA secret requires uppercase `DEVICE_ID`, `ACCESS_KEY`, `ACCESS_SECRET`. The controller secret requires lowercase `api-token`. Using any other key names will cause the respective component to fail at startup. To verify:
> ```bash
> kubectl get secret losant-gea-credentials -n losant-system \
>   -o jsonpath='{.data}' | jq 'keys'
> # Expected: ["ACCESS_KEY", "ACCESS_SECRET", "DEVICE_ID"]
> ```

---

## Next step

**[Step 2 → Cluster deployment](2-cluster-deployment.md)** — deploy the operator and GEA pod to the cluster.
