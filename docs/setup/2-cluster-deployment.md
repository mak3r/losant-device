# Step 2: Cluster Deployment

Deploy the losant-device operator and companion GEA pod to the cluster. Two paths are supported: **kustomize** (direct manifest apply, suited for GitOps) and **Helm** (suited for parameterized installs).

## Prerequisites

- `kubectl` connected to a running k3s (or standard Kubernetes) cluster
- Kubernetes secrets created in [Step 1](1-losant-device-setup.md)
- `make` (for kustomize path) or `helm` v3.x (for Helm path)

---

## Namespace

Both paths use the `losant-system` namespace. Create it first if you are not using Helm's `--create-namespace`:

```bash
kubectl create namespace losant-system
```

---

## Path 1: Kustomize (Makefile targets)

This is the default path used in development and CI. The Makefile wraps `kubectl` and `kustomize` for each step.

### Install CRDs only

```bash
make install IMG=ghcr.io/mak3r/losant-device:latest
```

Applies the `LosantSync` CRD from `config/crd/bases/` into the cluster. Safe to run repeatedly — it is idempotent.

### Deploy controller + GEA

```bash
make deploy IMG=ghcr.io/mak3r/losant-device:latest
```

Sets the controller image in `config/manager/kustomization.yaml`, then applies the full `config/default` kustomization (controller Deployment, ServiceAccount, RBAC, and the GEA resources from `config/gea/`).

### Remove deployment

```bash
make undeploy
```

Removes the controller and all associated resources. Does not remove CRDs or the `losant-system` namespace.

### Remove CRDs

```bash
make uninstall
```

Deletes the `LosantSync` CRD. Any existing `LosantSync` CR objects will be garbage-collected.

---

## Path 2: Helm

The `helm/` chart installs and manages the operator and GEA together.

### Install

```bash
helm install losant-device helm/ \
  --namespace losant-system \
  --create-namespace \
  --set gea.credentials.secretValues.deviceID=<id> \
  --set gea.credentials.secretValues.accessKey=<key> \
  --set gea.credentials.secretValues.accessSecret=<secret>
```

Alternatively, use an existing secret (skips inline credential values):

```bash
helm install losant-device helm/ \
  --namespace losant-system \
  --create-namespace \
  --set gea.credentials.existingSecret=losant-gea-credentials
```

### Upgrade

```bash
helm upgrade losant-device helm/ --namespace losant-system
```

### Uninstall

```bash
helm uninstall losant-device --namespace losant-system
```

See [helm/README.md](../../helm/README.md) for all configurable values.

---

## GEA Manifest Layout

The `config/gea/` directory contains raw Kubernetes manifests for the Losant Gateway Edge Agent:

| File | Resource | Purpose |
|---|---|---|
| `serviceaccount.yaml` | ServiceAccount `losant-gea` | Dedicated identity for the GEA pod; has **no** Kubernetes API permissions |
| `pvc.yaml` | PersistentVolumeClaim `losant-gea-data` | 1Gi `ReadWriteOnce` volume for GEA's SQLite offline buffer at `/data` |
| `deployment.yaml` | Deployment `losant-gea` | Runs `losant/edge-agent:latest`; mounts the PVC; reads credentials from `losant-gea-credentials` secret |
| `service.yaml` | ClusterIP Service `losant-gea` | Exposes port 8080 (HTTP trigger) in-cluster; the controller POSTs metric payloads here |
| `kustomization.yaml` | Kustomization | Declares all four resources; sets namespace `losant-system` |

**Environment variables** (sourced from `losant-gea-credentials` secret):
- `DEVICE_ID` — Losant Edge Compute device ID; establishes the GEA's MQTT identity
- `ACCESS_KEY` / `ACCESS_SECRET` — Losant credentials for the MQTT connection

**Volume**: `/data` is mounted from the PVC and stores the GEA's SQLite buffer, which holds up to 65,000 messages during connectivity gaps.

**Probes**: Both liveness and readiness probes issue `HTTP GET /` on port 8080. The readiness probe has a 10-second initial delay; liveness has a 30-second initial delay to allow the MQTT connection to establish.

---

## Verify the GEA is Online

After deploying, wait for the GEA pod to reach Running and check that it has connected to Losant:

```bash
kubectl logs deploy/losant-gea -n losant-system | grep "Connected to"
# Expected: Connected to: mqtts://broker.losant.com
```

The device should also show **Online** status in the Losant UI. **This is required before proceeding to Step 3** — the workflow deployment will fail if the GEA has never connected.

---

## Next step

**[Step 3 → Losant workflow setup](3-losant-workflow-setup.md)** — create and deploy the Edge Workflow to the GEA.
