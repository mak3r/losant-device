# Runbook — losant-device Operator

Operational procedures for the `losant-device` Kubernetes controller running on k3s clusters.

> **Before you begin**: Complete the one-time Losant setup in [docs/losant-setup.md](losant-setup.md) first. You will need the Edge Compute device ID, access key, and access secret from that guide before the commands below will work.

---

## Prerequisites

### Step 1 — Decide your run mode

All subsequent steps branch on this decision. Pick one now:

- **`make run`** — controller runs as a local process in your terminal; no Deployment or namespace is created in the cluster. Use this for development and local testing. **Limitation**: cannot reach in-cluster Services (e.g., `losant-gea`), so GEA state reporting always fails. Provisioning steps work, but no metrics will be delivered to Losant.
- **`make deploy`** — controller runs as an in-cluster pod in the `losant-system` namespace. Use this for staging, production, and any end-to-end testing that includes GEA metric reporting. Uses the published image at `ghcr.io/mak3r/losant-device` — no Docker build required.

### Step 2 — Install or verify the CRD (all modes)

```bash
kubectl get crd losantsyncs.losant.io
```

Expected output shows `losantsyncs.losant.io` with a creation timestamp. If not found:

```bash
make manifests install
```

### Step 3 — Start the controller

**If you chose `make run`:**

```bash
make run
```

The controller starts in your terminal and connects to the cluster in `~/.kube/config`. Keep this terminal open — logs appear here. No namespace or Deployment is created in the cluster.

**If you chose `make deploy`:**

```bash
# Deploy controller + GEA using the published image
make deploy IMG=ghcr.io/mak3r/losant-device:latest
```

> Available image tags: https://github.com/mak3r/losant-device/pkgs/container/losant-device
>
> If you have modified the controller source and want to test your changes, build and push your own image first:
> ```bash
> make docker-build docker-push IMG=my-registry/losant-device:v0.x.y
> make deploy IMG=my-registry/losant-device:v0.x.y
> ```

### Step 4 — Verify the controller is running

**If you used `make run`:**

Controller logs appear in the terminal from Step 3. No cluster checks are needed.

**If you used `make deploy`:**

```bash
kubectl get deploy -n losant-system losant-device-controller-manager
kubectl logs -n losant-system deploy/losant-device-controller-manager -f
```

---

## Upgrading an Existing Deployment

These commands apply only when the controller is already running via `make deploy` and you want to update it.

```bash
# Roll out a new published image
make deploy IMG=ghcr.io/mak3r/losant-device:<new-tag>

# Update CRDs only (no controller restart needed for CRD additions)
make install
```

> If upgrading from a custom-built image, run `make docker-build docker-push IMG=<your-registry>/losant-device:<new-tag>` first, then `make deploy IMG=<your-registry>/losant-device:<new-tag>`.

---

## Creating a LosantSync Resource

1. Create the `losant-system` namespace if it does not already exist (skip if you used `make deploy`, which creates it automatically):

   ```bash
   kubectl create namespace losant-system
   ```

2. Create the provisioning secret (requires a Losant Application API Token — see [docs/losant-setup.md](losant-setup.md#step-5-create-an-application-api-token)):

   ```bash
   kubectl create secret generic losant-credentials \
     --from-literal=api-token=<application-api-token> \
     -n losant-system
   ```

3. Apply a `LosantSync` manifest:

   ```yaml
   apiVersion: losant.io/v1alpha1
   kind: LosantSync
   metadata:
     name: my-cluster
   spec:
     applicationID: "<losant-app-id>"
     provisioningSecretRef:
       name: losant-credentials
       namespace: losant-system
     clusterName: "my-k3s-cluster"
     region: "us-east-1"
     interval: "5m"
     gea:
       serviceRef: losant-gea
       port: 8080
   ```

4. Monitor startup:

   ```bash
   watch kubectl get losantsync my-cluster
   # Phase should move: (empty) → Provisioning → Active
   ```

---

## Diagnosing Phase Problems

**Start here for any stuck phase** — read the conditions first:

```bash
kubectl get losantsync my-cluster -o jsonpath='{.status.conditions}' | python3 -m json.tool
```

Each condition has a `reason` and `message` that identify exactly which step failed. Use the sections below to dig further based on what you see.

### Phase stuck at Provisioning

1. Check controller logs for provisioning errors:

   **If using `make run`:** look for `provision` in the terminal running the controller.

   **If using `make deploy`:**
   ```bash
   kubectl logs -n losant-system deploy/losant-device-controller-manager | grep "provision"
   ```
2. Verify the provisioning secret exists and contains `api-token`:
   ```bash
   kubectl get secret losant-credentials -n losant-system -o jsonpath='{.data.api-token}' | base64 -d
   ```
3. Confirm the Losant REST API is reachable from within the cluster:
   ```bash
   kubectl run -it --rm curl-test --image=curlimages/curl --restart=Never -- \
     curl -I https://api.losant.com
   ```

### Phase stuck at Degraded

Degraded is set whenever any reconcile step fails — including provisioning steps. Always check the conditions first (above) to determine whether the failure is in provisioning (`DevicesProvisioned: False`) or GEA reporting (`GEAReachable: False`) before proceeding.

**If `DevicesProvisioned` is False:** follow the Provisioning steps above.

**If `GEAReachable` is False:**

1. Verify the GEA pod is running:
   ```bash
   kubectl get pod -n losant-system -l app=losant-gea
   ```
2. Test GEA HTTP trigger from within the cluster:
   ```bash
   kubectl run -it --rm curl-test --image=curlimages/curl --restart=Never -- \
     curl -v http://losant-gea:8080
   ```
3. Check GEA pod logs for MQTT connectivity issues:
   ```bash
   kubectl logs -n losant-system -l app=losant-gea --tail=50
   ```

### Phase unexpectedly Suspended

Check if `spec.suspend` was set:
```bash
kubectl get losantsync my-cluster -o jsonpath='{.spec.suspend}'
```

To resume:
```bash
kubectl patch losantsync my-cluster --type=merge -p '{"spec":{"suspend":false}}'
```

---

## Suspending / Resuming Sync

```bash
# Suspend (halts all metric reporting immediately)
kubectl patch losantsync my-cluster --type=merge -p '{"spec":{"suspend":true}}'

# Resume
kubectl patch losantsync my-cluster --type=merge -p '{"spec":{"suspend":false}}'
```

After resuming, the controller restarts the lifecycle from `Provisioning`. This is safe — existing Losant device IDs are preserved in `status.nodeDevices` and will not be re-provisioned.

---

## Changing the Sync Schedule

```bash
# Switch to a 2-minute interval
kubectl patch losantsync my-cluster --type=merge -p '{"spec":{"interval":"2m"}}'

# Switch to cron (remove interval, set cronSchedule)
kubectl patch losantsync my-cluster --type=json \
  -p '[{"op":"remove","path":"/spec/interval"},{"op":"add","path":"/spec/cronSchedule","value":"*/5 * * * *"}]'
```

> The CRD enforces mutual exclusion: exactly one of `interval` or `cronSchedule` must be set. Attempting to set both will be rejected by the API server.

---

## Controller Restart Recovery

The controller persists `status.nextScheduledTime` to the CR status on every reconcile. On restart:

- If `nextScheduledTime` is in the **past**: the controller fires a sync cycle immediately.
- If `nextScheduledTime` is in the **future**: the controller waits until that time.

No manual intervention is needed after a normal controller restart. If `nextScheduledTime` appears frozen, check the controller logs for status update errors (usually an RBAC problem — see issue #39).

---

## Running the E2E Test Suite

```bash
# Requires: running k3s cluster, CRD installed, controller deployed
export KUBECONFIG=~/.kube/config

# Run all e2e tests
cd test/e2e && go test ./... -v -timeout 5m

# Run a specific describe block
go test ./... -v -run "LosantSync lifecycle"
go test ./... -v -run "LosantSync scheduling"
go test ./... -v -run "LosantSync CRD validation"
```

The suite creates resources in the `losant-e2e` namespace and cleans them up after each test. It is safe to run against a live cluster.

### Prerequisites

```bash
# Install CRD
make manifests install

# Ensure controller is running
make run
# or: make deploy IMG=...
```

### Known skipped tests

Several test cases are marked `Skip` because they require live cluster operations not automatable in CI:
- Node add / node remove (requires adding/removing a real cluster node)
- Controller restart mid-schedule (requires `kubectl rollout restart` against a live controller pod)

All other criteria are implemented and run automatically.

---

## Checking Metric Delivery in Losant

1. Open your Losant Application → **Devices**.
2. Find the Edge Compute device (tagged `clusterName=<your-cluster>`).
3. Click **Data** — timestamps should be within the configured interval.
4. For peripheral devices (nodes), verify each node appears with tag `node=<nodeName>`.

If data is missing but logs show `"GEA report sent"`, the GEA may have lost its MQTT connection to `broker.losant.com`. Check GEA pod logs.

---

## Deleting a LosantSync Resource

```bash
kubectl delete losantsync my-cluster
```

This stops all metric reporting and removes the CR. It does **not** delete devices from the Losant dashboard — historical data is retained. To remove Losant devices, use the Losant UI or API directly.

---

## RBAC Troubleshooting

If the controller logs show `forbidden` errors on `list` or `watch` for `losantsyncs`, refer to issue #39. The generated `role.yaml` may be missing `list;watch` verbs.

```bash
# Check what the role currently grants
kubectl get clusterrole losant-device-manager-role -o yaml | grep -A5 losantsyncs
```

Expected output (minimum):
```yaml
- apiGroups: [losant.io]
  resources: [losantsyncs]
  verbs: [get, list, watch, create, update, patch, delete]
```
