# Step 3: Deploy the Operator

Install the losant-device operator and GEA with Helm, then apply the LosantSync CR to start the first reconcile cycle.

## Prerequisites

- **Helm 3.8 or later** — required for OCI registry support (`helm version`)
- `kubectl` with access to your cluster
- Access to `ghcr.io` (no authentication required for public images)
- Application API Token from [Step 1](1-losant-application.md)

---

## Install via OCI Chart

Replace `<VERSION>` with a release tag (e.g. `v0.1.0`). See the [releases page](https://github.com/mak3r/losant-device/releases) for available versions.

### Standard install (Helm-managed CRDs)

This is the default path. Helm installs CRDs as part of the chart install and manages their lifecycle.

```bash
helm install losant-device \
  oci://ghcr.io/mak3r/losant-device/charts/losant-device:<VERSION> \
  --create-namespace \
  --namespace losant-system
```

### CRD-first install (GitOps environments)

GitOps tools such as Flux and ArgoCD typically manage CRDs as cluster-scoped infrastructure, separate from application charts. In these environments, apply the standalone CRD manifest before installing the chart and pass `--skip-crds` so Helm does not attempt to manage them.

```bash
# Step 1 — apply CRDs to the cluster
kubectl apply -f https://github.com/mak3r/losant-device/releases/download/<VERSION>/crds.yaml

# Step 2 — install the chart without the CRD install step
helm install losant-device \
  oci://ghcr.io/mak3r/losant-device/charts/losant-device:<VERSION> \
  --create-namespace \
  --namespace losant-system \
  --skip-crds
```

**Which pattern to choose:**

| Pattern | When to use |
|---|---|
| **Standard (Helm-managed CRDs)** | Direct `helm install`; Helm owns the full lifecycle |
| **CRD-first** | Flux `HelmRelease`, ArgoCD `Application`, Terraform, Ansible — tools that treat CRDs as cluster infrastructure outside of app charts |

This deploys:
- The losant-device controller Deployment and RBAC
- The GEA Deployment, Service, PersistentVolumeClaim, and ServiceAccount

---

## Create the provisioning Secret

Now that Helm has created the `losant-system` namespace, create the Secret the controller uses to authenticate to the Losant REST API. Replace `<application-api-token-from-step-1>` with the token from [Step 1](1-losant-application.md).

```bash
kubectl create secret generic losant-provisioning-credentials \
  --from-literal=api-token=<application-api-token-from-step-1> \
  -n losant-system
```

> **Key name matters**: The controller reads the `api-token` key specifically. Any other key name causes a startup error.

> **Required API token scopes**: The Application API Token must include `devices.*` and `deviceRecipes.*` for device provisioning. If you use `spec.workflowDeployments`, it must also include `edgeDeployments.release` — without this scope, the controller will enter `Degraded` phase with `ReleaseFailed` on the `WorkflowDeployed` condition. Configure scopes in the Losant UI under **Application → Access Keys → Edit**.

---

## Upgrade

To upgrade to a new release, run `helm upgrade` with the new version tag:

```bash
helm upgrade losant-device \
  oci://ghcr.io/mak3r/losant-device/charts/losant-device:<NEW_VERSION> \
  --namespace losant-system
```

If you are using the **CRD-first** pattern, apply the new CRD manifest **before** running `helm upgrade`:

```bash
kubectl apply -f https://github.com/mak3r/losant-device/releases/download/<NEW_VERSION>/crds.yaml

helm upgrade losant-device \
  oci://ghcr.io/mak3r/losant-device/charts/losant-device:<NEW_VERSION> \
  --namespace losant-system
```

To pin the controller image to a specific tag (overrides the chart default):

```bash
helm upgrade losant-device \
  oci://ghcr.io/mak3r/losant-device/charts/losant-device:<NEW_VERSION> \
  --namespace losant-system \
  --set controller.image.tag=<NEW_VERSION>
```

---

## Expected GEA state after install

Immediately after `helm install`, the GEA pod will be in `CreateContainerConfigError`. **This is normal and expected** — the `losant-gea-credentials` Secret does not exist yet.

```bash
kubectl get pod -n losant-system -l app=losant-device-gea
# NAME                         READY   STATUS                       RESTARTS
# losant-gea-xxxxxxxxx-xxxxx   0/1     CreateContainerConfigError   0
```

The controller writes the Secret during the bootstrap phase of the first reconcile (see below). The GEA pod resolves automatically — you do not need to intervene.

---

## Verify the controller is running

```bash
kubectl get deploy -n losant-system losant-device-controller-manager
kubectl logs -n losant-system deploy/losant-device-controller-manager --tail=20
```

Wait for the controller pod to show `READY 1/1` before applying the LosantSync CR.

---

## Apply the LosantSync CR

Create a `LosantSync` manifest and apply it. The Application ID comes from [Step 1](1-losant-application.md).

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
  interval: "5m"
  gea:
    serviceRef: "losant-gea"
    port: 8080
```

```bash
kubectl apply -f losantsync.yaml
```

> See [docs/architecture.md](../architecture.md#crd-losantsync) for the full CRD field reference including `cronSchedule`, `rancherURL`, and `deviceRecipeID`.

---

## Watch the first reconcile

Replace `<your-cr-name>` with the `metadata.name` value from the CR you applied above (e.g., `prod-edge-01`).

```bash
kubectl get losantsync <your-cr-name> -w
```

Expected phase sequence:

```
NAME               PHASE          AGE
<your-cr-name>     Provisioning   2s
<your-cr-name>     Active         18s
```

**What happens during the first reconcile cycle:**

1. Phase set to `Provisioning`
2. Controller calls Losant REST API: finds or creates the cluster Edge Compute device
3. Controller generates a GEA Access Key for the device
4. Controller writes `losant-gea-credentials` Secret to `losant-system`
5. Controller patches the GEA Deployment with a `restartedAt` annotation, triggering a rolling restart
6. GEA pod restarts, reads the new Secret, connects to `mqtts://broker.losant.com`
7. Controller provisions per-node peripheral devices
8. Controller reports cluster + node state to GEA via HTTP POST
9. Phase set to `Active`

After reaching `Active`, the GEA pod should show `Running`:

```bash
kubectl get pod -n losant-system -l app=losant-device-gea
# NAME                         READY   STATUS    RESTARTS
# losant-gea-xxxxxxxxx-xxxxx   1/1     Running   1
```

---

## Manual credential path

If your cluster cannot reach `api.losant.com` outbound, use `--set gea.autoProvision=false` and follow the [Manual Provisioning appendix](A-manual-provisioning.md) **before** running `helm install`.

---

## Next step

**[Step 4 → Edge Workflow](4-losant-workflow.md)** — create and deploy the Edge Workflow in Losant.
