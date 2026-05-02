# Setup Guide

This guide walks through deploying the losant-device operator and connecting it to Losant. The happy path takes under 15 minutes.

## Quick Start

| Step | Where | What you do |
|---|---|---|
| [1. Losant Application](1-losant-application.md) | Losant UI | Create Application + API Token |
| [2. Kubernetes Preparation](2-kubernetes-preparation.md) | Terminal | Create provisioning Secret |
| [3. Deploy](3-deploy.md) | Terminal | `helm install` + apply LosantSync CR |
| [4. Edge Workflow](4-losant-workflow.md) | Losant UI | Create and deploy Edge Workflow |
| [5. Verify](5-verify.md) | Terminal + Losant UI | Confirm data is flowing |
| [6. Cleanup](6-cleanup.md) | Terminal + Losant UI | Teardown and reset (when needed) |

**[Start here → Step 1: Losant Application](1-losant-application.md)**

---

## What the Controller Does for You

You do not need to create devices, access keys, or GEA credentials manually. The controller handles all of that:

| Action | Who | When |
|---|---|---|
| Create `losant-system` namespace | Helm | `helm install` |
| Deploy operator + GEA pod | Helm | `helm install` |
| Create Edge Compute device in Losant | Controller | First reconcile |
| Create GEA Access Key | Controller | First reconcile |
| Write `losant-gea-credentials` Secret | Controller | First reconcile |
| Restart GEA pod to pick up credentials | Controller | First reconcile |
| Provision per-node peripheral devices | Controller | Every sync cycle |
| Report cluster + node state to GEA | Controller | Every sync cycle |

---

## Which Path Applies to You

| Path | When to use |
|---|---|
| **Automated (default)** | Controller has outbound access to `api.losant.com` — this is most deployments |
| **[Manual (`gea.autoProvision=false`)](A-manual-provisioning.md)** | Air-gapped or firewall-restricted environments; external secrets management (Vault, ESO) |

The default path requires no credential management beyond the initial provisioning API token. Start with Step 1.
