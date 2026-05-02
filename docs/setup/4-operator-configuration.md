# Step 4: Operator Configuration

Optionally create a Device Recipe for bulk node provisioning and configure Losant dashboards.

## Prerequisites

- LosantSync CR applied and Edge Compute device Online (see [Step 3](3-losant-workflow-setup.md))
- Edge Workflow deployed to the GEA (see [Step 3](3-losant-workflow-setup.md))

---

## (Optional) Create a Device Recipe for Bulk Node Provisioning

For clusters with many nodes, a Device Recipe enables bulk peripheral device creation via CSV.

1. Application → **Devices** → **Device Recipes** → **Add Recipe**
2. Configure the recipe with the node device attribute schema (same attributes as the cluster device but for per-node data)
3. Note the **Recipe ID** — this goes into `LosantSync.spec.deviceRecipeID`

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
