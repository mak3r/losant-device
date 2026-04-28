# Runbook — losant-device Operator

Operational procedures for the `losant-device` Kubernetes controller running on k3s clusters.

> **Before you begin**: Complete the one-time Losant setup in [docs/losant-setup.md](losant-setup.md) first. You will need the Edge Compute device ID, access key, and access secret from that guide before the commands below will work.

---

## Prerequisites

### Step 1 — Determine your run mode

All subsequent steps branch on how the controller was started. Decide now:

- **`make run`** — controller runs as a local process in your terminal; no Deployment or namespace is created in the cluster
- **`make deploy`** — controller runs as an in-cluster pod in the `losant-system` namespace

### Step 2 — Verify CRD is installed (all modes)

```bash
kubectl get crd losantsyncs.losant.io
```

Expected output shows `losantsyncs.losant.io` with a creation timestamp. If not found, run `make manifests install`.

### Step 3 — Verify controller is running

**If you used `make run`:**

Controller logs appear in the terminal where `make run` is executing. No cluster checks are needed — there is no Deployment or namespace in the cluster.

**If you used `make deploy`:**

```bash
kubectl get deploy -n losant-system losant-device-controller-manager
kubectl logs -n losant-system deploy/losant-device-controller-manager -f
```

---

## Deploying / Upgrading

```bash
# Build and push image (set IMG to your registry)
make docker-build docker-push IMG=my-registry/losant-device:v0.x.y

# Deploy to cluster
make deploy IMG=my-registry/losant-device:v0.x.y

# Install / update CRDs only (no controller restart needed for CRD additions)
make install
```

---

## Creating a LosantSync Resource

1. Create the `losant-system` namespace if it does not already exist (skip if you used `make deploy`, which creates it automatically):

   ```bash
   kubectl create namespace losant-system
   ```

2. Create the provisioning secret:

   ```bash
   kubectl create secret generic losant-credentials \
     --from-literal=device-id=<edge-compute-device-id> \
     --from-literal=access-key=<key> \
     --from-literal=access-secret=<secret> \
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

### Phase stuck at Provisioning

1. Check controller logs for provisioning errors:
   ```bash
   kubectl logs -n losant-system deploy/losant-device-controller-manager | grep "provision"
   ```
2. Verify the provisioning secret exists and contains `device-id`, `access-key`, and `access-secret`:
   ```bash
   kubectl get secret losant-credentials -n losant-system -o jsonpath='{.data.device-id}' | base64 -d
   kubectl get secret losant-credentials -n losant-system -o jsonpath='{.data.access-key}' | base64 -d
   ```
3. Check `DevicesProvisioned` condition for a reason:
   ```bash
   kubectl get losantsync my-cluster -o jsonpath='{.status.conditions}'
   ```
4. Confirm the Losant REST API is reachable from within the cluster:
   ```bash
   kubectl run -it --rm curl-test --image=curlimages/curl --restart=Never -- \
     curl -I https://api.losant.com
   ```

### Phase stuck at Degraded

1. Check `GEAReachable` condition:
   ```bash
   kubectl get losantsync my-cluster \
     -o jsonpath='{range .status.conditions[?(@.type=="GEAReachable")]}{.status}: {.reason} — {.message}{end}'
   ```
2. Verify the GEA pod is running:
   ```bash
   kubectl get pod -n losant-system -l app=losant-gea
   ```
3. Test GEA HTTP trigger from within the cluster:
   ```bash
   kubectl run -it --rm curl-test --image=curlimages/curl --restart=Never -- \
     curl -v http://losant-gea:8080
   ```
4. Check GEA pod logs for MQTT connectivity issues:
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
