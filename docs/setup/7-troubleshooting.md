# Troubleshooting the GEA Bootstrap

This page covers the GEA bootstrap sequence, what to expect at each stage, and how to diagnose failures. For general operator health, see [docs/runbook.md](../runbook.md).

---

## Expected Bootstrap Sequence

After `helm install` and `kubectl apply -f LosantSync.yaml`, the bootstrap proceeds in this order:

| Step | What happens | What you see |
|---|---|---|
| 1 | Helm creates namespace, deploys controller + GEA | GEA pod in `CreateContainerConfigError` or `Init:0/1` |
| 2 | Controller starts; `LosantSync` CR is applied | `LosantSync` phase: `Provisioning` |
| 3 | Controller calls Losant REST API, creates/finds Edge Compute device | Controller logs: `provisioning device` |
| 4 | Controller generates GEA Access Key, writes `losant-gea-credentials` Secret | Secret appears in `losant-system` |
| 5 | Controller patches GEA Deployment with `restartedAt` annotation | GEA pod restarts |
| 6 | GEA reads credentials Secret, connects to `mqtts://broker.losant.com` | GEA pod: `Running 1/1` |
| 7 | Controller provisions per-node peripheral devices | Controller logs: `provisioned node` |
| 8 | Controller reports cluster + node state to GEA via HTTP POST | `LosantSync` phase: `Active` |

**Normal timeline**: the bootstrap typically completes in under 60 seconds. If the `LosantSync` phase has not reached `Active` after 3 minutes, use the checks below.

---

## First Check: Read the Conditions

Most bootstrap failures are reported directly in the `LosantSync` conditions. Always start here:

```bash
kubectl describe losantsync <your-cr-name>
```

Look for the `Status.Conditions` block. Each condition has a `Reason` and `Message` that identifies exactly which step failed. Common conditions to look for:

| Condition | Failure reason | Meaning |
|---|---|---|
| `GEABootstrapped` | `ProvisioningFailed` | Controller could not write `losant-gea-credentials` |
| `GEABootstrapped` | `SecretWriteFailed` | Kubernetes API rejected the secret write (usually RBAC) |
| `DevicesProvisioned` | `LosantAPIError` | REST API returned an error; message contains the HTTP status code |
| `GEAReachable` | `ConnectionRefused` | Controller cannot POST to the GEA HTTP endpoint |

---

## GEA Pod Stuck in `Init:0/1`

**Symptom**: After `helm install`, the GEA pod sits in `Init:0/1` and does not progress.

`Init:0/1` means the GEA init container is running but has not completed. The init container waits for the `losant-gea-credentials` Secret to exist before allowing the main container to start. This is expected during bootstrap — the controller writes that secret during the first reconcile.

**Check 1 — Has the `LosantSync` CR been applied?**

The controller cannot bootstrap credentials until it sees a `LosantSync` CR. Confirm one exists:

```bash
kubectl get losantsync -A
```

If the output is empty, apply your `LosantSync` manifest:

```bash
kubectl apply -f losantsync.yaml
```

**Check 2 — Is the controller running?**

```bash
kubectl get deploy -n losant-system losant-device-controller-manager
kubectl logs -n losant-system deploy/losant-device-controller-manager --tail=20
```

If the Deployment shows `0/1` or logs show errors, the controller has not started yet. Wait for `READY 1/1` before expecting credentials to appear.

**Check 3 — Did the controller write the credentials Secret?**

```bash
kubectl get secret losant-gea-credentials -n losant-system
```

If the secret exists, the init container should complete shortly. If it still has not progressed after 30 seconds, check for the empty-fields failure below.

**Check 4 — Inspect the `GEABootstrapped` condition**

```bash
kubectl describe losantsync <your-cr-name>
```

Look for `GEABootstrapped: False` — the `Message` field will contain the specific API error or Kubernetes error that prevented the secret from being written.

---

## `losant-gea-credentials` Exists but GEA Cannot Connect

**Symptom**: The `losant-gea-credentials` Secret exists, but the GEA pod stays in `Init:0/1` or `CrashLoopBackOff`, and GEA logs show connection failures.

**Check the secret fields are populated:**

```bash
kubectl get secret losant-gea-credentials -n losant-system \
  -o jsonpath='{.data}' | python3 -c "
import sys, json, base64
d = json.load(sys.stdin)
for k, v in d.items():
    val = base64.b64decode(v).decode()
    print(f'{k}: {repr(val[:20])}...' if len(val) > 20 else f'{k}: {repr(val)}')
"
```

Expected output: `DEVICE_ID`, `ACCESS_KEY`, and `ACCESS_SECRET` all contain non-empty values.

**If any field is empty**, the provisioner called the Losant API but received an incomplete response, or the controller wrote the secret before provisioning completed. Check the `GEABootstrapped` condition message for the specific API error:

```bash
kubectl describe losantsync <your-cr-name>
```

Common causes for empty fields:

- **API token lacks permissions** — the token used in `losant-provisioning-credentials` must have `Device:Create` and `Application:Read` permissions. Regenerate the token in Losant and update the secret (see [Step 1](1-losant-application.md)).
- **Losant REST API rate limit** — the controller will retry on the next reconcile. Check whether the phase returns to `Active` within the configured `interval`.
- **Network partition** — the controller cannot reach `api.losant.com`. Test outbound connectivity:

  ```bash
  kubectl run -it --rm curl-test --image=curlimages/curl --restart=Never \
    --namespace=losant-system -- curl -I https://api.losant.com
  ```

---

## Controller Running Wrong Image Version

**Symptom**: The operator behaves unexpectedly; you suspect a stale image is deployed.

**Verify the running image:**

```bash
kubectl get deploy -n losant-system losant-device-controller-manager \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
```

Compare the tag in the output to the expected release. If they differ, upgrade:

```bash
helm upgrade losant-device \
  oci://ghcr.io/mak3r/losant-device/charts/losant-device:<EXPECTED_VERSION> \
  --namespace losant-system
```

See [Step 3 — Upgrade](3-deploy.md#upgrade) for the full upgrade procedure.

---

## GEA Pod Stuck in `CreateContainerConfigError`

**Symptom**: The GEA pod shows `CreateContainerConfigError` for more than 2 minutes after the `LosantSync` phase reaches `Active`.

During normal bootstrap, `CreateContainerConfigError` is expected and resolves automatically once the controller writes `losant-gea-credentials`. If it persists after `Active`:

```bash
# Confirm the secret was written
kubectl get secret losant-gea-credentials -n losant-system

# Check controller logs for bootstrap errors
kubectl logs -n losant-system deploy/losant-device-controller-manager \
  | grep -i "bootstrap\|credential\|error"
```

If the secret is missing, check the `GEABootstrapped` condition (see above).

---

## GEA Access Key Rejected After Bootstrap

**Symptom**: GEA pod is `Running` but logs repeat:

```
[warn] Unable to connect to mqtts://broker.losant.com, access key/secret rejected.
```

This means the credentials in `losant-gea-credentials` were written successfully but are no longer valid — the device was deleted in Losant or the access key was revoked.

```bash
# Check which device ID the GEA is using
kubectl get secret losant-gea-credentials -n losant-system \
  -o jsonpath='{.data.DEVICE_ID}' | base64 -d; echo
```

Locate that device ID in the Losant UI (**Application → Devices**). If the device no longer exists, force re-provisioning by deleting the credentials secret — the controller will recreate it on the next reconcile:

```bash
kubectl delete secret losant-gea-credentials -n losant-system
```

For the full manual credential procedure, see [Appendix A — Manual Provisioning](A-manual-provisioning.md).

---

## Checking Phase Transitions

Watch the `LosantSync` phase in real time:

```bash
kubectl get losantsync <your-cr-name> -w
```

Expected transitions: `(empty)` → `Provisioning` → `Active`

If the phase stalls at `Provisioning` for more than 3 minutes, check the conditions:

```bash
kubectl describe losantsync <your-cr-name>
```

Phase meanings:

| Phase | Meaning |
|---|---|
| `Provisioning` | First reconcile is in progress |
| `Active` | Bootstrap complete; sync cycles are running |
| `Degraded` | A sync step is failing; check conditions for which one |
| `Suspended` | `spec.suspend: true` is set; no metrics are being reported |

To resume from `Suspended`:

```bash
kubectl patch losantsync <your-cr-name> --type=merge -p '{"spec":{"suspend":false}}'
```

---

## Next Step

If bootstrap completes but data does not appear in Losant, see [Step 5 — Verify](5-verify.md).
