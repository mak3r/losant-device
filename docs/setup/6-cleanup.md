# Step 6: Teardown and Reset

Remove all cluster resources and Losant objects for a clean slate — or to fully uninstall the operator.

---

## Cluster cleanup

Follow these steps **in order** to avoid leaving orphaned resources:

```bash
# 1. Remove the LosantSync CR — stops the controller reconcile loop cleanly
kubectl delete losantsync --all

# 2. Helm uninstall — removes controller, GEA, RBAC, Services, PVC, Deployments
helm uninstall losant-device --namespace losant-system

# 3. Remove CRDs — needed if reinstalling with schema changes
make uninstall
# or: kubectl delete -f config/crd/bases

# 4. Delete Secrets — not removed by Helm uninstall
kubectl delete secret losant-provisioning-credentials -n losant-system
kubectl delete secret losant-gea-credentials -n losant-system  # if created by controller

# 5. Remove namespace (optional — only if you want a full reset)
kubectl delete namespace losant-system
```

> **PVC note**: `helm uninstall` removes the GEA PVC (`losant-gea-data`). Any buffered offline messages stored in the GEA's SQLite database will be permanently deleted.

---

## Losant-side cleanup

The controller creates devices and access keys in Losant that are **not** automatically removed when the operator is uninstalled. If this cluster will not be reinstalled, clean up manually:

1. Delete the peripheral devices (nodes) in **Application → Devices** (tagged `clusterName=<your-cluster>`)
2. Delete the Edge Compute device (cluster device)
3. Revoke the controller-created access key on the cluster device (**device page → Security**)
4. Delete the Edge Workflow targeting this device (**Application → Workflows**)

If the cluster device is left in Losant and later reinstalled, the controller will find the device by name and reuse it rather than creating a duplicate.

---

## Reinstall after reset

After a full namespace deletion, the next `helm install` starts fresh:

1. The controller re-provisions the cluster device (or finds it by name if not deleted from Losant)
2. The controller re-runs the GEA bootstrap sequence, writing a new `losant-gea-credentials` Secret
3. Phase progresses `Provisioning` → `Active` as in the initial install

See [Step 3](3-deploy.md) for the full first-reconcile walkthrough.
