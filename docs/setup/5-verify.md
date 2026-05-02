# Step 5: Verify

Confirm the operator is reporting data to Losant and review common failure modes.

## Confirm LosantSync is Active

```bash
kubectl get losantsync -n losant-system
# NAME           PHASE    AGE
# prod-edge-01   Active   5m
```

Check the detailed conditions if the phase is not `Active`:

```bash
kubectl get losantsync prod-edge-01 -o jsonpath='{.status.conditions}' | python3 -m json.tool
```

Each condition has a `reason` and `message` that identifies exactly which step failed.

---

## Confirm GEA is connected

```bash
kubectl logs deploy/losant-gea -n losant-system | grep "Connected to"
# Expected: Connected to: mqtts://broker.losant.com
```

---

## Confirm data is arriving in Losant

1. Open your Losant Application → **Devices**
2. Find the Edge Compute device (tagged `clusterName=<your-cluster>`)
3. Click **Data** — timestamps should be within the configured `LosantSync.spec.interval`
4. For node devices, verify each node appears with tag `node=<nodeName>`

---

## Common failure modes

### `losant-provisioning-credentials` Secret missing or wrong key

**Symptom**: Controller logs show `SecretNotFound` or `missing key: api-token`.

```bash
kubectl get secret losant-provisioning-credentials -n losant-system \
  -o jsonpath='{.data}' | python3 -c "import sys,json; print(list(json.load(sys.stdin).keys()))"
# Expected: ['api-token']
```

Fix: re-create the secret with the exact key name `api-token` (see [Step 2](2-kubernetes-preparation.md)).

---

### API token expired or insufficient permissions

**Symptom**: Phase stuck at `Provisioning`; controller logs show `401` or `403` from `api.losant.com`.

Fix: generate a new API Token in Losant (**Application → Security → API Tokens**) and update the secret:

```bash
kubectl create secret generic losant-provisioning-credentials \
  --from-literal=api-token=<new-token> \
  -n losant-system \
  --dry-run=client -o yaml | kubectl apply -f -
```

---

### GEA pod stuck in `CreateContainerConfigError` longer than 2 minutes

**Symptom**: GEA pod never leaves `CreateContainerConfigError` after controller reaches `Active`.

**Expected**: The GEA pod starts in `CreateContainerConfigError`, then resolves automatically within ~60 seconds once the controller writes the `losant-gea-credentials` Secret. If it is still failing after 2 minutes, the bootstrap phase likely failed.

```bash
# Check controller logs for bootstrap errors
kubectl logs -n losant-system deploy/losant-device-controller-manager | grep -i "bootstrap\|credential\|error"

# Confirm the secret was written
kubectl get secret losant-gea-credentials -n losant-system
```

---

### `DevicesProvisioned` condition False

**Symptom**: Conditions show `DevicesProvisioned: False` with reason `LosantAPIError`.

Common causes: Losant REST API rate limit, or the API token lacks device-creation permissions. Check the controller logs for the specific error code, then retry after the rate limit window passes.

---

### Edge Workflow not deployed — GEA accepts POSTs but Losant shows no state updates

**Symptom**: Controller logs show `"GEA report sent"` but the device **Data** tab in Losant is empty.

Cause: The Edge Workflow was not deployed to the device, so the GEA receives the POST but has no workflow node to forward it to Losant.

Fix: In Losant → **Workflows** → select your workflow → click **Deploy**. Confirm the device is listed in the deployment targets.

---

### GEA Access Key rejected

**Symptom**: GEA pod logs repeat:

```
[warn] Unable to connect to mqtts://broker.losant.com, access key/secret rejected.
```

See [docs/runbook.md](../runbook.md#gea-access-keysecret-rejected) for full diagnosis steps.

---

## Dashboard quick-start

Once metrics are flowing, create a three-level dashboard hierarchy:

1. **Fleet Dashboard**: filter `device_type=cluster`; add table block with `cluster_name`, `region`, `health_score`
2. **Cluster Dashboard**: add context variable `cluster_name`; filter `device_type=node` + context variable; add health gauges
3. **Node Dashboard**: add context variable for device ID; use single-device time-series blocks for node metrics

See [docs/architecture.md](../architecture.md#dashboard-hierarchy) for the full specification.

---

## Setup complete

The operator is deployed and reporting cluster health to Losant. For ongoing operations, see [docs/runbook.md](../runbook.md).
