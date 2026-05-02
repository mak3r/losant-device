---
title: Setup Guide Rewrite — Target Structure
status: approved
phase: 4-hardening
author: product-designer
date: 2026-05-01
issue: "#238"
supersedes: "#235, #237"
---

# Setup Guide Rewrite — Target Structure

This plan defines the complete target structure for a rewrite of the operator setup guide.
It is derived from reading the controller reconcile loop, Helm templates, and provisioner
design — not from the existing `docs/losant-setup.md`. The docs persona should treat this
as a blueprint for a from-scratch replacement, not an incremental patch.

---

## What the Controller Provisions Automatically (Codebase Reading)

Source: `internal/controller/losantsync_controller.go`, `internal/losant/client.go`

| Action | Who | When | Code reference |
|---|---|---|---|
| Create `losant-system` namespace | Helm | `helm install` | `helm/templates/` |
| Create GEA Deployment + Service + PVC | Helm | `helm install` | `helm/templates/gea-deployment.yaml` |
| Create controller Deployment + RBAC | Helm | `helm install` | `helm/templates/deployment.yaml` |
| Set phase to `Provisioning` | Controller | First reconcile (immediate) | `losantsync_controller.go:86-94` |
| Find or create Edge Compute device in Losant | Controller | First sync cycle | `losant/client.go:EnsureClusterDevice` |
| Find or create peripheral device per k8s node | Controller | First sync cycle | `losant/client.go:EnsureNodeDevice` |
| Report cluster + node state to GEA | Controller | Every sync cycle | `losantsync_controller.go:161-182` |
| **Create Losant access keys for cluster device** | Controller | Bootstrap phase (phase/3) | _pending #215_ |
| **Write `losant-gea-credentials` Secret** | Controller | Bootstrap phase (phase/3) | _pending #215_ |
| **Trigger GEA rolling restart** | Controller | Bootstrap phase (phase/3) | _pending #215_ |

**Items in bold are planned but not yet implemented.** The setup guide should be written
against the target state (after #215 merges), with a note on the manual path for clusters
where auto-provision is disabled.

---

## Hard Prerequisites (What Must Exist Before the Controller Starts)

These are inputs the controller cannot create on its own:

| Prerequisite | Who creates it | Why required |
|---|---|---|
| Losant Application | User (Losant UI) | Namespace for all devices; applicationID goes into LosantSync spec |
| Losant Application API Token | User (Losant UI) | Controller authenticates to Losant REST API via Bearer token in `provisioningSecretRef` |
| `losant-system` namespace | Helm (or user pre-creates) | All operator resources live here |
| `losant-provisioning-credentials` Secret | User (`kubectl create secret`) | Contains `api-token`; read at first reconcile — must exist before controller tries Step 1 |
| LosantSync CR | User (`kubectl apply`) | Triggers the reconcile loop |

**The `losant-gea-credentials` Secret is NOT a hard prerequisite in the auto-provision path.**
In the default flow the controller creates it during the bootstrap phase before restarting
the GEA pod.

---

## GEA Startup Dependency

Source: `helm/templates/gea-deployment.yaml` — the GEA pod reads credentials via `envFrom`
env-var references to `losant-gea-credentials` Secret at pod startup.

**Current behaviour (before #215 merges):** The Secret must exist before `helm install`,
otherwise the GEA pod fails to start (`CreateContainerConfigError`). This is the
chicken-and-egg problem the bootstrap design resolves.

**Target behaviour (after #215 merges, `gea.autoProvision: true`):**
1. `helm install` runs — GEA Deployment is created. GEA pod enters `CreateContainerConfigError`
   because `losant-gea-credentials` doesn't exist yet. This is an expected transient state.
2. Controller completes bootstrap: creates device, generates access keys, writes Secret.
3. Controller patches GEA Deployment with `restartedAt` annotation.
4. GEA pod restarts, reads new Secret, connects to Losant as the Edge Compute device.

The guide must explain that a brief GEA pod error state after `helm install` is normal and
resolves automatically within the first reconcile cycle (typically under 60 seconds).

---

## Correct Deployment Sequence (Default Auto-Provision Path)

```
User actions                            Automated / controller actions
───────────────────────────────────     ──────────────────────────────────────────
[Losant UI]
1. Create Application
2. Create Application API Token
                                        
[Kubernetes]
3. kubectl create secret (api-token)
4. helm install

                                        Helm creates: namespace, GEA Deployment,
                                        controller Deployment, RBAC, PVC, Service
                                        
                                        GEA pod: CreateContainerConfigError
                                        (expected — Secret not yet written)
                                        
                                        Controller: Phase → Provisioning
                                        
5. kubectl apply LosantSync CR
                                        
                                        Controller Step 4: EnsureClusterDevice →
                                          finds or creates Edge Compute device
                                        
                                        Controller bootstrap: CreateDeviceAccessKey
                                          → writes losant-gea-credentials Secret
                                        
                                        Controller patches GEA Deployment →
                                          GEA pod rolling restart
                                        
                                        GEA pod: reads Secret, connects to Losant
                                        
                                        Controller Steps 5–7: EnsureNodeDevices,
                                          ReportState to GEA
                                        
                                        Controller: Phase → Active

[Losant UI]
6. Create Edge Workflow
   - Add HTTP Trigger node (path /state)
   - Add Device: Set State node
   - Deploy workflow to device
                                        
                                        GEA receives state POSTs from controller
                                        on every sync cycle
```

---

## Where the Manual Path Branches

The manual path (`gea.autoProvision: false`) applies when:
- The cluster cannot make outbound REST calls to `api.losant.com` at startup
- The operator manages credentials separately (external secrets operator, Vault)
- The user wants to pre-provision devices with a specific device recipe or template

**Exact trigger condition**: `helm install --set gea.autoProvision=false`

**Manual path additional steps** (replace Steps 3b–3d of the automated flow):
1. In Losant UI: create the Edge Compute device manually (`k8s-cluster-<clusterName>`)
2. In Losant UI: generate an access key for that device
3. `kubectl create secret generic losant-gea-credentials --from-literal=DEVICE_ID=... --from-literal=ACCESS_KEY=... --from-literal=ACCESS_SECRET=...`

These three steps must be completed before Step 4 (`helm install`) because the GEA pod
will not start without the Secret in the manual path.

---

## Target Setup Guide Structure

The docs persona should write `docs/setup/` as the following files. Each section below
maps to a file and specifies: **who performs it**, **when** (precondition), and **dependencies**.

---

### File: `docs/setup/README.md` — Entry point and quick-start summary

| Section | Who | When | Dependency |
|---|---|---|---|
| What this guide covers | — | — | — |
| Quick start (numbered list, 6 steps) | User | — | None |
| "What the controller does for you" table | Info | — | None |
| Which path applies (auto vs. manual table) | Info | — | None |

The quick start must be the first thing after the file title — no lengthy prerequisites or
background first. Users should be able to complete setup in under 15 minutes on a happy path.

---

### File: `docs/setup/1-losant-application.md` — Losant UI setup

| Section | Who | When | Dependency |
|---|---|---|---|
| Create Losant Application | User | Before deploy | Losant account |
| Create Application API Token | User | Before deploy | Application exists |
| Note Application ID | User | Before deploy | Application exists |

**Do not include device creation or access key creation steps here.** Those are automated.

---

### File: `docs/setup/2-kubernetes-preparation.md` — Cluster-side steps

| Section | Who | When | Dependency |
|---|---|---|---|
| Create provisioning Secret | User | Before deploy | API Token from step 1 |
| (Optional) Create namespace manually | User | Before deploy if not using Helm namespace creation |
| Verify kubeconfig context | User | Before deploy | Access to target cluster |

---

### File: `docs/setup/3-deploy.md` — Helm install

| Section | Who | When | Dependency |
|---|---|---|---|
| Install with Helm (default auto-provision) | User | After step 2 | Secret exists |
| Verify controller pod is running | User | After install | Helm install succeeds |
| Explain transient GEA error state | Info | After install | Normal — resolves automatically |
| Apply LosantSync CR | User | After controller ready | Helm install done |
| Watch first reconcile | User | After CR applied | Reconcile cycle runs |

Include the exact `kubectl get losantsync -w` watch command and expected output sequence:
`Provisioning` → `Active`.

---

### File: `docs/setup/4-losant-workflow.md` — Edge Workflow setup

| Section | Who | When | Dependency |
|---|---|---|---|
| Wait for device to appear online in Losant | User | After controller reconciles | Controller reaches Active |
| Create Edge Workflow (HTTP Trigger → Device State) | User | After device online | Device exists in Losant |
| Deploy workflow to device | User | After workflow configured | Workflow created |
| Verify state reports arriving | User | After workflow deployed | GEA deployed workflow |

Include the payload schema table (cluster attributes + node attributes). Copy format from
existing `docs/losant-setup.md` Step 4 section — that content is accurate.

---

### File: `docs/setup/5-verify.md` — Verification and troubleshooting

| Section | Who | When | Dependency |
|---|---|---|---|
| Confirm LosantSync is Active | User | After full setup | Everything above |
| Check GEA pod logs | User | If stuck | GEA failing to connect |
| Describe common failure modes | Info | Troubleshoot | — |
| Dashboard quick-start (fleet/cluster/node hierarchy) | User | After Active | Metrics flowing |

Common failure modes to cover:
- `losant-provisioning-credentials` Secret missing or wrong key name → `SecretNotFound`
- API token expired or insufficient permissions → `LosantUnreachable`
- GEA pod stuck in `CreateContainerConfigError` longer than 2 minutes → bootstrap failed; check controller logs
- `DevicesProvisioned` condition False → Losant API rate limit or permission issue
- Edge Workflow not deployed → GEA accepts POST but Losant shows no state updates

---

### File: `docs/setup/A-manual-provisioning.md` — Appendix: Manual credential path

| Section | Who | When | Dependency |
|---|---|---|---|
| When to use this path | Info | Decision point | `gea.autoProvision=false` |
| Create Edge Compute device in Losant UI | User | Before deploy | Application exists |
| Create access key for device | User | Before deploy | Device exists |
| Create `losant-gea-credentials` Secret | User | Before deploy | Access key exists |
| Helm install with `--set gea.autoProvision=false` | User | After secret created | Secret exists |

This file is linked from `3-deploy.md` under "Manual credential management".

---

### File: `docs/setup/6-cleanup.md` — Teardown and reset

| Section | Who | When | Dependency |
|---|---|---|---|
| Delete LosantSync CR | User | Before uninstall | Stops reconciliation cleanly |
| Helm uninstall | User | After CR deleted | Removes operator + GEA + RBAC |
| Remove CRDs | User | After Helm uninstall | `make uninstall` or `kubectl delete` |
| Delete Kubernetes Secrets | User | After Helm uninstall | Provisioning + GEA credential secrets |
| Delete `losant-system` namespace (if manually created) | User | After all resources removed | Namespace not managed by Helm |
| Losant-side cleanup | User | Optional | Devices + access keys created by controller |

**Commands to include verbatim** (from `Makefile` and `docs/deployment.md`):

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

**Losant-side cleanup note**: The controller creates devices and access keys in Losant that
are not automatically removed when the operator is uninstalled. If this cluster will not be
reinstalled, remove them manually from the Losant dashboard:
- Delete peripheral devices (nodes) under the application
- Delete the Edge Compute device (cluster device)
- Revoke the controller-created access key (named `losant-device-controller-<clusterName>-<timestamp>`)
- Delete the Edge Workflow targeting this device

**Reinstall after reset**: After a full namespace deletion, the next `helm install` starts
fresh — the controller will re-provision the cluster device (or find it by name if it was not
deleted from Losant) and re-run the GEA bootstrap sequence.

---

## What to Remove from the Current `docs/losant-setup.md`

These sections are superseded and should not be carried forward:

| Current section | Reason to drop |
|---|---|
| Step 2: Create the Edge Compute Device | Automated by controller |
| Step 3: Create the GEA Access Key | Automated by controller |
| Step 6: `kubectl create secret losant-gea-credentials` | Automated in default path |
| Bootstrap constraint callout at top | No longer accurate after #215 |

Sections to carry forward (content is accurate):
- Step 1 (Create Application) → `docs/setup/1-losant-application.md`
- Step 4 (Edge Workflow) → `docs/setup/4-losant-workflow.md`
- Step 5 (Application API Token) → `docs/setup/1-losant-application.md`
- Step 7 (Device Recipe, optional) → `docs/setup/A-manual-provisioning.md` appendix
- Dashboard setup → `docs/setup/5-verify.md`

---

## Implementation Notes for Docs Persona

1. **Write against the target state** (after #215 merges). If #215 has not merged by the time
   docs begins the rewrite, add a note in `3-deploy.md` that auto-provisioning requires operator
   version X.Y (to be filled in at merge time).

2. **Do not copy from `docs/losant-setup.md`** — that file is the source of the inaccuracy.
   Read `internal/controller/losantsync_controller.go` to confirm what the controller does,
   then write to that truth.

3. **The GEA transient error state** explanation in `3-deploy.md` is the most important new
   content. Without it, users will panic when they see `CreateContainerConfigError` and open
   support tickets or abort the setup.

4. **File names**: use the numbered prefix convention (`1-`, `2-`, etc.) so that filesystem
   order matches reading order.

5. **Cross-references**: `docs/deployment.md` and `docs/runbook.md` both reference the setup
   guide. Update those references to point to `docs/setup/README.md` after the rewrite lands.
