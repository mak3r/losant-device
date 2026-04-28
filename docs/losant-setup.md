# Losant Setup

One-time configuration steps required before deploying the operator.

## Prerequisites

- A Losant account at [app.losant.com](https://app.losant.com)
- An existing Losant Application (or follow step 1 to create one)

> **Bootstrap constraint**: The cluster Edge Compute device (Step 2) must exist in Losant **before** the operator starts. The controller reads its Losant device ID from the provisioning Secret at startup (`device-id` key) and uses it to authenticate via `POST /auth/device`. There is no automatic creation of this device — it must be pre-provisioned manually.

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
5. Under **Attributes**, add these (data type: `number` for all):
   ```
   total_nodes, ready_nodes, unhealthy_nodes
   total_pods, running_pods, failed_pods, pending_pods, crashloop_pods
   degraded_pvcs, coredns_healthy, event_warnings, health_score
   ```
6. Note the **Device ID** — this is the GEA's identity

---

## Step 3: Create the GEA Access Key

The GEA pod needs its own credentials to authenticate to Losant as the Edge Compute device.

1. On the Edge Compute device page → **Security** → **Add Access Key**
2. Note the **Access Key** and **Access Secret** (the secret is shown only once)

---

## Step 4: Create the Edge Workflow

The GEA runs an Edge Workflow that receives metric payloads from the controller and routes them to the correct Losant devices.

1. In your Application → **Workflows** → **Add Workflow** → **Edge Workflow**
2. Select the Edge Compute device you created in Step 2
3. Add an **HTTP Trigger** node (this receives POST requests from the controller on port 8080)
4. Add logic to:
   - Parse the incoming JSON payload (cluster state + per-node states)
   - Use **Device: Set State** nodes to report cluster-level attributes to the cluster device
   - Loop over node entries and use **Device: Set State** for each peripheral node device
5. Save and deploy the workflow to the device

> Note: The exact workflow structure depends on the payload format implemented by `internal/gea/payload.go`. See the payload format documentation once that package is implemented.

---

## Step 5: Create the Provisioning Access Key

The controller uses a separate access key to authenticate to the Losant REST API. At startup it exchanges `{deviceId, key, secret}` via `POST /auth/device` to obtain a short-lived bearer token (cached for 23 hours). This key must have read/write access to Devices.

1. Application → **Security** → **Access Keys** → **Add Access Key**
2. Scope: read/write on Devices
3. Note the **Access Key** and **Access Secret**

---

## Step 6: Create Kubernetes Secrets

```bash
# GEA credentials — mounted into the GEA pod as environment variables
kubectl create secret generic losant-gea-credentials \
  --from-literal=DEVICE_ID=<edge-compute-device-id-from-step-2> \
  --from-literal=ACCESS_KEY=<gea-access-key-from-step-3> \
  --from-literal=ACCESS_SECRET=<gea-access-secret-from-step-3> \
  -n losant-system

# Provisioning credentials — used by the controller for REST API calls
# device-id must match the Edge Compute device created in Step 2
kubectl create secret generic losant-provisioning-credentials \
  --from-literal=device-id=<edge-compute-device-id-from-step-2> \
  --from-literal=access-key=<provisioning-key-from-step-5> \
  --from-literal=access-secret=<provisioning-secret-from-step-5> \
  -n losant-system
```

> **Key names matter**: The controller reads `device-id`, `access-key`, and `access-secret` (not `losant-access-key` / `losant-access-secret`). Using the wrong key names will cause the operator to fail at startup with a missing-key error.

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
