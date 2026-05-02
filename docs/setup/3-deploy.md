# Step 3: Deploy the Operator

Install the losant-device operator and GEA with Helm, then apply the LosantSync CR to start the first reconcile cycle.

## Prerequisites

- `helm` v3.x installed
- `losant-provisioning-credentials` Secret created (see [Step 2](2-kubernetes-preparation.md))

---

## Install with Helm

```bash
helm install losant-device helm/ \
  --namespace losant-system \
  --create-namespace
```

This deploys:
- The losant-device controller Deployment and RBAC
- The GEA Deployment, Service, PersistentVolumeClaim, and ServiceAccount

> **Published image**: The chart defaults to the latest published image at `ghcr.io/mak3r/losant-device`. To pin a specific version:
> ```bash
> helm install losant-device helm/ \
>   --namespace losant-system \
>   --create-namespace \
>   --set image.tag=v0.1.0-alpha.4
> ```

---

## Expected GEA state after install

Immediately after `helm install`, the GEA pod will be in `CreateContainerConfigError`. **This is normal and expected** — the `losant-gea-credentials` Secret does not exist yet.

```bash
kubectl get pod -n losant-system -l app=losant-gea
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
kubectl get pod -n losant-system -l app=losant-gea
# NAME                         READY   STATUS    RESTARTS
# losant-gea-xxxxxxxxx-xxxxx   1/1     Running   1
```

---

## Manual credential path

If your cluster cannot reach `api.losant.com` outbound, use `--set gea.autoProvision=false` and follow the [Manual Provisioning appendix](A-manual-provisioning.md) **before** running `helm install`.

---

## Next step

**[Step 4 → Edge Workflow](4-losant-workflow.md)** — create and deploy the Edge Workflow in Losant.
