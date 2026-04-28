# Deployment

This document covers how to deploy the losant-device operator and its companion GEA pod. Two paths are supported: **kustomize** (direct manifest apply, suited for GitOps) and **Helm** (suited for parameterized installs and package distribution). Both paths result in the same running components.

## Prerequisites

- `kubectl` connected to a running k3s (or standard Kubernetes) cluster
- Credentials from the [Losant setup guide](losant-setup.md)
- `make` (for kustomize path) or `helm` v3.x (for Helm path)

---

## Namespace

Both paths use the `losant-system` namespace. Create it first if you are not using Helm's `--create-namespace`:

```bash
kubectl create namespace losant-system
```

---

## Credentials Secrets

Both deployment paths expect these two secrets to exist before resources are applied.

### GEA credentials

The GEA pod reads these from the `losant-gea-credentials` secret at startup:

```bash
kubectl create secret generic losant-gea-credentials \
  --from-literal=DEVICE_ID=<edge-compute-device-id> \
  --from-literal=ACCESS_KEY=<gea-access-key> \
  --from-literal=ACCESS_SECRET=<gea-access-secret> \
  -n losant-system
```

### Provisioning credentials

The controller authenticates to the Losant REST API using a Losant Application API Token (see [Losant setup guide](losant-setup.md#step-5-create-an-application-api-token)). Device access keys cannot be used here — they are MQTT-only credentials.

```bash
kubectl create secret generic losant-provisioning-credentials \
  --from-literal=api-token=<application-api-token> \
  -n losant-system
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

The `helm/` chart installs and manages the operator and GEA together. It is a skeleton for Phase 5 — full template rendering will be completed in a later phase. `helm lint helm/` passes clean.

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

See [helm/README.md](../helm/README.md) for all configurable values.

---

## GEA Manifest Layout

The `config/gea/` directory contains raw Kubernetes manifests for the Losant Gateway Edge Agent. These are included by the default kustomization and can also be applied independently:

```bash
kubectl apply -k config/gea/
```

| File | Resource | Purpose |
|---|---|---|
| `serviceaccount.yaml` | ServiceAccount `losant-gea` | Dedicated identity for the GEA pod; has **no** Kubernetes API permissions |
| `pvc.yaml` | PersistentVolumeClaim `losant-gea-data` | 1Gi `ReadWriteOnce` volume for GEA's SQLite offline buffer at `/data` |
| `deployment.yaml` | Deployment `losant-gea` | Runs `losant/edge-agent:latest`; mounts the PVC; reads credentials from `losant-gea-credentials` secret |
| `service.yaml` | ClusterIP Service `losant-gea` | Exposes port 8080 (HTTP trigger) in-cluster; the controller POSTs metric payloads here |
| `kustomization.yaml` | Kustomization | Declares all four resources; sets namespace `losant-system` |

### GEA Deployment details

The GEA Deployment (`config/gea/deployment.yaml`) configures the following:

**Environment variables** (sourced from `losant-gea-credentials` secret):
- `DEVICE_ID` — Losant Edge Compute device ID; establishes the GEA's MQTT identity
- `ACCESS_KEY` / `ACCESS_SECRET` — Losant credentials for the MQTT connection

**Volume**: `/data` is mounted from the PVC and stores the GEA's SQLite buffer, which holds up to 65,000 messages during connectivity gaps.

**Probes**: Both liveness and readiness probes issue `HTTP GET /` on port 8080. The readiness probe has a 10-second initial delay; liveness has a 30-second initial delay to allow the MQTT connection to establish.

**Resource limits**: 500m CPU / 512Mi memory (limits); 100m CPU / 128Mi memory (requests).

**ServiceAccount**: `losant-gea` (no RBAC permissions — the GEA only communicates outbound to `broker.losant.com`).

---

## Apply a LosantSync CR

After the controller and GEA are running, create a `LosantSync` resource to begin monitoring:

```bash
kubectl apply -f config/samples/losant_v1alpha1_losantsync.yaml
```

Watch the controller bring the resource to `Active` phase:

```bash
kubectl get losantsync -w
```

See [docs/architecture.md](architecture.md#crd-losantsync) for the full CRD field reference.
