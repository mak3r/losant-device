# Step 4: Operator Configuration

Apply the LosantSync custom resource to begin cluster monitoring. Optionally create a Device Recipe for bulk node provisioning.

## Prerequisites

- Operator and GEA deployed and running (see [Step 2](2-cluster-deployment.md))
- Edge Workflow deployed to the GEA (see [Step 3](3-losant-workflow-setup.md))

---

## (Optional) Create a Device Recipe for Bulk Node Provisioning

For clusters with many nodes, a Device Recipe enables bulk peripheral device creation via CSV.

1. Application → **Devices** → **Device Recipes** → **Add Recipe**
2. Configure the recipe with the node device attribute schema (same attributes as the cluster device but for per-node data)
3. Note the **Recipe ID** — this goes into `LosantSync.spec.deviceRecipeID`

---

## Apply the LosantSync CR

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

See [docs/architecture.md](../architecture.md#crd-losantsync) for the full CRD field reference.

> **Known issue — cluster device duplication**: The controller's provisioner will create a second Edge Compute device in Losant if it cannot match `LosantSync.spec.clusterName` to the device you created manually in [Step 1](1-losant-device-setup.md). Until automated provisioning matching is fully implemented, you may see two cluster-level devices in the Losant UI after the first reconcile.
>
> **Workaround:**
> 1. In the Losant UI, identify the controller-created device (it will have tags set by the controller, such as `health_status=provisioning`)
> 2. Note its **Device ID**
> 3. Delete the original manually created device
> 4. Update the `DEVICE_ID` key in the `losant-gea-credentials` secret to the controller-created device's ID:
>    ```bash
>    kubectl create secret generic losant-gea-credentials \
>      --from-literal=DEVICE_ID=<controller-created-device-id> \
>      --from-literal=ACCESS_KEY=<existing-access-key> \
>      --from-literal=ACCESS_SECRET=<existing-access-secret> \
>      -n losant-system \
>      --dry-run=client -o yaml | kubectl apply -f -
>    ```
> 5. Restart the GEA pod to pick up the new `DEVICE_ID`:
>    ```bash
>    kubectl rollout restart deployment/losant-gea -n losant-system
>    ```
>
> This workaround will be removed once issue #211 (automated provisioning matching) is resolved.

---

## Dashboard Setup

Once the operator is running and reporting data, create the three-level dashboard hierarchy in Losant:

1. **Fleet Dashboard**: filter on `device_type=cluster`; add table block with `cluster_name`, `region`, `health_score`
2. **Cluster Dashboard**: add context variable `cluster_name`; filter on `device_type=node` + context variable
3. **Node Dashboard**: add context variable for device ID; use single-device blocks for time-series

See [docs/architecture.md](../architecture.md#dashboard-hierarchy) for the full dashboard specification.

---

## Troubleshooting

### Secret key names are wrong

| Secret | Required key names |
|---|---|
| `losant-gea-credentials` | `DEVICE_ID`, `ACCESS_KEY`, `ACCESS_SECRET` |
| `losant-provisioning-credentials` | `api-token` |

```bash
kubectl get secret losant-gea-credentials -n losant-system \
  -o jsonpath='{.data}' | jq 'keys'
# Expected: ["ACCESS_KEY", "ACCESS_SECRET", "DEVICE_ID"]
```

### GEA is in CrashLoopBackOff but the cluster looks healthy

Check the logs before assuming the GEA itself is broken:

```bash
kubectl logs deploy/losant-gea -n losant-system
```

If the output contains `Connected to: mqtts://broker.losant.com`, the GEA is healthy — the restart loop is caused by a probe misconfiguration, not a GEA failure. Verify that the probe endpoint and port match the HTTP Trigger path (`/state`, port `8080`).

### Confirming the controller is reaching the GEA

```bash
# Watch the GEA pod logs for incoming requests
kubectl logs deploy/losant-gea -n losant-system --follow

# In another terminal, trigger a reconcile
kubectl annotate losantsync prod-edge-01 force-sync=$(date +%s) --overwrite
```

Each reconcile cycle should produce log lines similar to:
```
POST /state 200  deviceId=<cluster-device-id>
POST /state 200  deviceId=<node-device-id>
```

If no POST lines appear, check that `LosantSync.spec.gea.serviceRef` and `.spec.gea.port` match the GEA service name and port (`losant-gea`, `8080`).
