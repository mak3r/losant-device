# Appendix: Manual GEA Credential Provisioning

Use this path when the controller cannot make outbound REST calls to `api.losant.com` at startup — for example in air-gapped environments, or when you manage credentials via an external secrets operator (Vault, ESO).

**Trigger condition**: `helm install --set gea.autoProvision=false`

In the manual path, the `losant-gea-credentials` Secret must exist **before** `helm install`. The GEA pod will not start without it, and the controller will not create it.

---

## When to use this path

| Scenario | Use manual path? |
|---|---|
| Standard cluster with internet access | No — use the default auto-provision path |
| Air-gapped or firewall-restricted cluster | Yes |
| External secrets operator managing GEA credentials | Yes |
| CI/CD pipeline with pre-provisioned device IDs | Yes |
| Cluster with outbound access only on specific ports | Depends — `api.losant.com` uses HTTPS/443 |

---

## Step A: Create the Edge Compute device in Losant

Application → **Devices** → **Add Device** → **Edge Compute**

Give the device a name matching `LosantSync.spec.clusterName` (e.g., `prod-edge-01`).

Under **Attributes**, add these by type:

**Number** (data type: `number`):
```
total_nodes, ready_nodes, unhealthy_nodes
total_pods, running_pods, failed_pods, pending_pods, crashloop_pods
degraded_pvcs, event_warnings, health_score
```

**Boolean** (data type: `boolean`):
```
coredns_healthy
```

**String** (data type: `string`):
```
health_status
```

Note the **Device ID** — this is `DEVICE_ID` in the secret below.

---

## Step B: Create a GEA Access Key

On the Edge Compute device page → **Security** → **Add Access Key**.

Note the **Access Key** (`ACCESS_KEY`) and **Access Secret** (`ACCESS_SECRET`) — the secret value is shown only once.

---

## Step C: Create the `losant-gea-credentials` Secret

```bash
kubectl create namespace losant-system --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic losant-gea-credentials \
  --from-literal=DEVICE_ID=<device-id-from-step-A> \
  --from-literal=ACCESS_KEY=<access-key-from-step-B> \
  --from-literal=ACCESS_SECRET=<access-secret-from-step-B> \
  -n losant-system
```

---

## Step D: Install with `gea.autoProvision=false`

```bash
helm install losant-device helm/ \
  --namespace losant-system \
  --create-namespace \
  --set gea.autoProvision=false
```

With auto-provision disabled, the GEA pod starts immediately (the Secret already exists) and the controller skips the bootstrap phase.

---

## Updating credentials

If the access key is rotated or the device is recreated:

1. Generate a new access key in Losant (**device page → Security → Add Access Key**)
2. Update the Secret:
   ```bash
   kubectl create secret generic losant-gea-credentials \
     --from-literal=DEVICE_ID=<device-id> \
     --from-literal=ACCESS_KEY=<new-key> \
     --from-literal=ACCESS_SECRET=<new-secret> \
     -n losant-system \
     --dry-run=client -o yaml | kubectl apply -f -
   ```
3. Restart the GEA pod:
   ```bash
   kubectl rollout restart deployment/losant-device-gea -n losant-system
   ```

See [docs/runbook.md](../runbook.md#gea-access-keysecret-rejected) for diagnosing connection failures.
