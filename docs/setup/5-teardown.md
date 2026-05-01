# Step 5: Teardown and Reset

Use this guide to completely remove a losant-device deployment — for example, to rebuild a test environment or recover from a corrupt state. Steps must be performed in order: cluster resources first, then Losant UI cleanup.

> **Destructive operations ahead.** Deleting Losant devices removes all historical state data permanently. The GEA Access Secret is shown only once at creation — it cannot be recovered after the device is deleted. If you plan to redeploy, note down any IDs or tokens you will need to recreate.

---

## Part 1: Cluster Teardown (Automated)

```bash
make reset
```

This removes the operator, GEA pod, CRDs, RBAC resources, and the `losant-system` namespace in one step. It is equivalent to running `make undeploy` followed by `make uninstall`.

Confirm everything is gone:

```bash
kubectl get namespace losant-system 2>&1
# Expected: Error from server (NotFound): namespaces "losant-system" not found

kubectl get crd losantsyncs.losant.io 2>&1
# Expected: Error from server (NotFound): ...
```

If `make reset` is not available (older release), run the steps manually:

```bash
make undeploy    # removes operator, GEA, RBAC
make uninstall   # removes CRDs
kubectl delete namespace losant-system --ignore-not-found
```

---

## Part 2: Losant UI Cleanup (Manual)

These steps must be done in the Losant UI at [app.losant.com](https://app.losant.com). Each is destructive and cannot be undone.

### 1. Delete peripheral (node) devices

The operator provisioned one Losant device per k8s node. Delete them all before deleting the Edge Compute device.

**Navigation:** Application → **Devices** → filter by tag `device_type=node` → select all → **Delete**

> **Irreversible.** Deleting a device permanently removes all historical state data (time-series metrics, event logs) for that device.

### 2. Delete the Edge Compute device (cluster device)

**Navigation:** Application → **Devices** → select `cluster-<your-cluster-name>` → **Delete Device**

> **Irreversible.** This also invalidates any Access Keys associated with the device. Delete the Access Key first if you want to audit its last-used date.

### 3. Delete the Edge Workflow

**Navigation:** Application → **Workflows** → select `k8s-state-receiver` (or your workflow name) → **Delete**

### 4. Delete the GEA Access Key

If the Access Key was not automatically invalidated when the Edge Compute device was deleted, remove it explicitly.

**Navigation:** Application → **Security** → **Access Keys** → find the key named for the GEA → **Delete**

### 5. Delete the Application API Token

**Navigation:** Application → **Security** → **API Tokens** → find the token named `losant-device-controller` (or your token name) → **Delete**

### 6. (Optional) Delete the Losant Application

If you want a completely clean slate, delete the Application itself. This removes all devices, workflows, dashboards, and credentials in one operation.

**Navigation:** Application → **Settings** → **Delete Application** → confirm

> **Irreversible.** All data, devices, workflows, and API tokens under the Application are permanently deleted.

---

## Redeploying After Reset

To start fresh after a full teardown, follow the setup guide from the beginning:

**[Step 1 → Losant device setup](1-losant-device-setup.md)**
